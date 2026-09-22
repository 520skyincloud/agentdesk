package jev

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultBaseURL   = "https://api.typesafe.ai/v1/systemone"
	DefaultModel     = "jev-latest"
	maxResponseBytes = 2 << 20
)

type Question struct {
	Type         string         `json:"type"`
	Instructions any            `json:"instructions"`
	Criteria     map[string]any `json:"criteria,omitempty"`
}

type Request struct {
	State     any                 `json:"state"`
	Model     string              `json:"model"`
	Questions map[string]Question `json:"questions"`
}

type Answer struct {
	Type          string             `json:"type"`
	Noul          float64            `json:"noul,omitempty"`
	Choice        string             `json:"choice,omitempty"`
	Score         float64            `json:"score,omitempty"`
	Confidence    float64            `json:"confidence,omitempty"`
	Probabilities map[string]float64 `json:"probabilities,omitempty"`
}

type Usage struct {
	InputTokens  int64 `json:"input_tokens"`
	OutputTokens int64 `json:"output_tokens"`
}

type Response struct {
	Model   string            `json:"model"`
	Answers map[string]Answer `json:"answers"`
	Usage   Usage             `json:"usage"`
}

type Client struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

type APIError struct {
	StatusCode int
	RetryAfter time.Duration
}

func (e *APIError) Error() string {
	return fmt.Sprintf("jev status %d", e.StatusCode)
}

func (e *APIError) IsRetryable() bool {
	return e.StatusCode == http.StatusRequestTimeout || e.StatusCode == http.StatusTooManyRequests ||
		(e.StatusCode >= 500 && e.StatusCode < 600)
}

var ErrRequestFailed = errors.New("jev request failed")

func NewClient(baseURL, apiKey string, timeout time.Duration) (*Client, error) {
	baseURL = normalizeBaseURL(baseURL)
	apiKey = strings.TrimSpace(apiKey)
	endpoint, err := url.Parse(baseURL)
	if err != nil || endpoint.Host == "" || !jevEndpointTransportAllowed(endpoint) ||
		endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" {
		return nil, fmt.Errorf("jev base URL is invalid")
	}
	if apiKey == "" {
		return nil, fmt.Errorf("jev API key is required")
	}
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	return &Client{
		baseURL: baseURL,
		apiKey:  apiKey,
		httpClient: &http.Client{
			Timeout: timeout,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}, nil
}

func (c *Client) Evaluate(ctx context.Context, request Request) (Response, error) {
	if c == nil || c.httpClient == nil {
		return Response{}, fmt.Errorf("jev client is unavailable")
	}
	if strings.TrimSpace(request.Model) == "" {
		request.Model = DefaultModel
	}
	if len(request.Questions) == 0 {
		return Response{}, fmt.Errorf("jev questions are required")
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return Response{}, fmt.Errorf("encode jev request failed")
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL, bytes.NewReader(payload))
	if err != nil {
		return Response{}, fmt.Errorf("build jev request failed")
	}
	httpRequest.Header.Set("Authorization", "Bearer "+c.apiKey)
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Accept", "application/json")

	response, err := c.httpClient.Do(httpRequest)
	if err != nil {
		return Response{}, transportError(ctx, err)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return Response{}, &APIError{StatusCode: response.StatusCode, RetryAfter: parseRetryAfter(response.Header, time.Now())}
	}
	body, readErr := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if readErr != nil {
		return Response{}, transportError(ctx, readErr)
	}
	if len(body) > maxResponseBytes {
		return Response{}, fmt.Errorf("jev response exceeds size limit")
	}
	var decoded Response
	if err := json.Unmarshal(body, &decoded); err != nil {
		return Response{}, fmt.Errorf("decode jev response failed")
	}
	if err := validateResponse(body, decoded, request.Questions); err != nil {
		return decoded, err
	}
	return decoded, nil
}

func transportError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return fmt.Errorf("jev request interrupted: %w", ctx.Err())
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return fmt.Errorf("jev request timed out: %w", context.DeadlineExceeded)
	}
	return ErrRequestFailed
}

func validateResponse(body []byte, response Response, questions map[string]Question) error {
	var envelope struct {
		Answers map[string]json.RawMessage `json:"answers"`
		Usage   *struct {
			InputTokens  *int64 `json:"input_tokens"`
			OutputTokens *int64 `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil ||
		strings.TrimSpace(response.Model) == "" || len(envelope.Answers) != len(questions) {
		return fmt.Errorf("jev response has invalid model or answer count")
	}
	if envelope.Usage == nil || envelope.Usage.InputTokens == nil || envelope.Usage.OutputTokens == nil ||
		*envelope.Usage.InputTokens < 0 || *envelope.Usage.OutputTokens < 0 {
		return fmt.Errorf("jev response has invalid usage")
	}
	for id, question := range questions {
		var answer struct {
			Type          string              `json:"type"`
			Noul          *float64            `json:"noul"`
			Choice        *string             `json:"choice"`
			Confidence    *float64            `json:"confidence"`
			Probabilities map[string]*float64 `json:"probabilities"`
		}
		raw, exists := envelope.Answers[id]
		if !exists || json.Unmarshal(raw, &answer) != nil || answer.Type != question.Type {
			return fmt.Errorf("jev response has missing or mismatched answer")
		}
		switch question.Type {
		case "noul":
			if !validProbability(answer.Noul) {
				return fmt.Errorf("jev response has invalid noul")
			}
		case "choice":
			if !validProbability(answer.Confidence) || len(answer.Probabilities) == 0 {
				return fmt.Errorf("jev response has invalid confidence or probabilities")
			}
			for _, probability := range answer.Probabilities {
				if !validProbability(probability) {
					return fmt.Errorf("jev response has invalid probability")
				}
			}
			options := question.Criteria
			if len(options) != len(answer.Probabilities) || answer.Choice == nil {
				return fmt.Errorf("jev response has invalid choice")
			}
			if _, exists := options[*answer.Choice]; !exists {
				return fmt.Errorf("jev response choice is outside criteria")
			}
			for option := range options {
				if _, exists := answer.Probabilities[option]; !exists {
					return fmt.Errorf("jev response is missing choice probability")
				}
			}
		default:
			return fmt.Errorf("jev question has unsupported type")
		}
	}
	return nil
}

func jevEndpointTransportAllowed(endpoint *url.URL) bool {
	if endpoint == nil {
		return false
	}
	if endpoint.Scheme == "https" {
		return true
	}
	if endpoint.Scheme != "http" {
		return false
	}
	host := strings.ToLower(strings.TrimSpace(endpoint.Hostname()))
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func validProbability(value *float64) bool {
	return value != nil && !math.IsNaN(*value) && !math.IsInf(*value, 0) && *value >= 0 && *value <= 1
}

func normalizeBaseURL(value string) string {
	value = strings.TrimRight(strings.TrimSpace(value), "/")
	if value == "" {
		return DefaultBaseURL
	}
	switch {
	case strings.HasSuffix(value, "/v1/systemone"):
		return value
	case strings.HasSuffix(value, "/v1"):
		return value + "/systemone"
	default:
		return value + "/v1/systemone"
	}
}

func parseRetryAfter(header http.Header, now time.Time) time.Duration {
	for _, unit := range []struct {
		name  string
		scale time.Duration
	}{{"retry-after-ms", time.Millisecond}, {"Retry-After", time.Second}} {
		raw := strings.TrimSpace(header.Get(unit.name))
		if value, err := strconv.ParseInt(raw, 10, 64); err == nil && value >= 0 {
			if value > math.MaxInt64/int64(unit.scale) {
				return time.Duration(math.MaxInt64)
			}
			return time.Duration(value) * unit.scale
		}
	}
	if retryTime, err := http.ParseTime(header.Get("Retry-After")); err == nil && retryTime.After(now) {
		return retryTime.Sub(now)
	}
	return 0
}
