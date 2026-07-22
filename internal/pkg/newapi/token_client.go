package newapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type TokenClient struct {
	baseURL string
	apiKey  string
	client  *http.Client
}

type TokenUsageSummary struct {
	UnlimitedQuota bool   `json:"unlimited_quota"`
	TotalGranted   int64  `json:"total_granted"`
	TotalUsed      int64  `json:"total_used"`
	TotalAvailable int64  `json:"total_available"`
	ExpiresAt      int64  `json:"expires_at"`
	Name           string `json:"name"`
}

type TokenUsageLog struct {
	ID               int64  `json:"id"`
	CreatedAt        int64  `json:"created_at"`
	Type             int    `json:"type"`
	TokenName        string `json:"token_name"`
	ModelName        string `json:"model_name"`
	Quota            int64  `json:"quota"`
	PromptTokens     int64  `json:"prompt_tokens"`
	CompletionTokens int64  `json:"completion_tokens"`
	UseTime          int64  `json:"use_time"`
	RequestID        string `json:"request_id"`
}

type TokenBillingSettings struct {
	QuotaDisplayType string  `json:"quota_display_type"`
	QuotaPerUnit     float64 `json:"quota_per_unit"`
	USDExchangeRate  float64 `json:"usd_exchange_rate"`
	Price            float64 `json:"price"`
}

func NewTokenClient(baseURL string, apiKey string, timeout time.Duration) (*TokenClient, error) {
	baseURL = normalizeTokenBaseURL(baseURL)
	apiKey = strings.TrimSpace(apiKey)
	if baseURL == "" || apiKey == "" {
		return nil, fmt.Errorf("new api token query is not configured")
	}
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return &TokenClient{
		baseURL: baseURL,
		apiKey:  apiKey,
		client:  &http.Client{Timeout: timeout},
	}, nil
}

func (c *TokenClient) GetBillingSettings(ctx context.Context) (*TokenBillingSettings, error) {
	body, err := c.get(ctx, "/api/status")
	if err != nil {
		return nil, err
	}
	var result struct {
		Success bool                 `json:"success"`
		Message string               `json:"message"`
		Data    TokenBillingSettings `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse billing settings response")
	}
	if !result.Success {
		return nil, fmt.Errorf("billing settings query rejected")
	}
	result.Data.QuotaDisplayType = strings.ToUpper(strings.TrimSpace(result.Data.QuotaDisplayType))
	if result.Data.QuotaDisplayType != "CNY" {
		return nil, fmt.Errorf("billing display type is not CNY")
	}
	if result.Data.QuotaPerUnit <= 0 {
		return nil, fmt.Errorf("billing quota per unit is invalid")
	}
	if result.Data.USDExchangeRate <= 0 {
		result.Data.USDExchangeRate = result.Data.Price
	}
	if result.Data.USDExchangeRate <= 0 {
		return nil, fmt.Errorf("billing CNY rate is invalid")
	}
	return &result.Data, nil
}

func (c *TokenClient) GetUsageSummary(ctx context.Context) (*TokenUsageSummary, error) {
	body, err := c.get(ctx, "/api/usage/token/")
	if err != nil {
		return nil, err
	}
	var result struct {
		Code    json.RawMessage   `json:"code"`
		Message string            `json:"message"`
		Data    TokenUsageSummary `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse token usage response")
	}
	if !truthyJSON(result.Code) {
		return nil, fmt.Errorf("token usage query rejected")
	}
	return &result.Data, nil
}

func (c *TokenClient) ListUsageLogs(ctx context.Context, startTimestamp int64, endTimestamp int64) ([]TokenUsageLog, error) {
	values := url.Values{}
	if startTimestamp > 0 {
		values.Set("start_timestamp", fmt.Sprintf("%d", startTimestamp))
	}
	if endTimestamp > 0 {
		values.Set("end_timestamp", fmt.Sprintf("%d", endTimestamp))
	}
	path := "/api/log/token"
	if encoded := values.Encode(); encoded != "" {
		path += "?" + encoded
	}
	body, err := c.get(ctx, path)
	if err != nil {
		return nil, err
	}
	var result struct {
		Success bool            `json:"success"`
		Message string          `json:"message"`
		Data    []TokenUsageLog `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse token usage log response")
	}
	if !result.Success {
		return nil, fmt.Errorf("token usage log query rejected")
	}
	return result.Data, nil
}

func (c *TokenClient) get(ctx context.Context, path string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return nil, fmt.Errorf("create token query request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("token query request failed")
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, fmt.Errorf("read token query response")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("token query HTTP %d", resp.StatusCode)
	}
	return body, nil
}

func normalizeTokenBaseURL(value string) string {
	value = strings.TrimRight(strings.TrimSpace(value), "/")
	if strings.HasSuffix(strings.ToLower(value), "/v1") {
		value = strings.TrimRight(value[:len(value)-3], "/")
	}
	return value
}

func truthyJSON(raw json.RawMessage) bool {
	value := strings.TrimSpace(string(raw))
	switch value {
	case "true", "1":
		return true
	default:
		return false
	}
}
