package newapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	BaseURL     string
	AccessToken string
	UserID      int64
	Timeout     time.Duration
}

type Client struct {
	config Config
	client *http.Client
}

type UsageLog struct {
	ID                int64  `json:"id"`
	CreatedAt         int64  `json:"created_at"`
	Type              int    `json:"type"`
	TokenName         string `json:"token_name"`
	ModelName         string `json:"model_name"`
	Quota             int64  `json:"quota"`
	PromptTokens      int64  `json:"prompt_tokens"`
	CompletionTokens  int64  `json:"completion_tokens"`
	UseTime           int64  `json:"use_time"`
	ChannelID         int64  `json:"channel"`
	RequestID         string `json:"request_id"`
	UpstreamRequestID string `json:"upstream_request_id"`
}

type apiResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Data    struct {
		Page     int        `json:"page"`
		PageSize int        `json:"page_size"`
		Total    int        `json:"total"`
		Items    []UsageLog `json:"items"`
	} `json:"data"`
}

type UsageLogQuery struct {
	StartTimestamp int64
	EndTimestamp   int64
	TokenName      string
	ModelName      string
	Page           int
	PageSize       int
}

func NewClient(config Config) (*Client, error) {
	config.BaseURL = strings.TrimRight(strings.TrimSpace(config.BaseURL), "/")
	config.AccessToken = strings.TrimSpace(config.AccessToken)
	if config.BaseURL == "" || config.AccessToken == "" || config.UserID <= 0 {
		return nil, fmt.Errorf("new api usage config requires base URL, access token, and user ID")
	}
	if config.Timeout <= 0 {
		config.Timeout = 5 * time.Second
	}
	return &Client{config: config, client: &http.Client{Timeout: config.Timeout}}, nil
}

func (c *Client) FindUsageByRequestID(ctx context.Context, requestID string) (*UsageLog, error) {
	requestID = strings.TrimSpace(requestID)
	if requestID == "" {
		return nil, fmt.Errorf("new api request ID is required")
	}
	query := url.Values{}
	query.Set("p", "1")
	query.Set("page_size", "10")
	query.Set("type", "2")
	query.Set("request_id", requestID)
	items, err := c.listUsageLogs(ctx, query)
	if err != nil {
		return nil, err
	}
	for i := range items {
		if strings.TrimSpace(items[i].RequestID) == requestID {
			item := items[i]
			return &item, nil
		}
	}
	return nil, nil
}

func (c *Client) ListUsageLogs(ctx context.Context, input UsageLogQuery) ([]UsageLog, error) {
	query := url.Values{}
	page := input.Page
	if page <= 0 {
		page = 1
	}
	pageSize := input.PageSize
	if pageSize <= 0 || pageSize > 100 {
		pageSize = 100
	}
	query.Set("p", strconv.Itoa(page))
	query.Set("page_size", strconv.Itoa(pageSize))
	query.Set("type", "2")
	if input.StartTimestamp > 0 {
		query.Set("start_timestamp", strconv.FormatInt(input.StartTimestamp, 10))
	}
	if input.EndTimestamp > 0 {
		query.Set("end_timestamp", strconv.FormatInt(input.EndTimestamp, 10))
	}
	if value := strings.TrimSpace(input.TokenName); value != "" {
		query.Set("token_name", value)
	}
	if value := strings.TrimSpace(input.ModelName); value != "" {
		query.Set("model_name", value)
	}
	return c.listUsageLogs(ctx, query)
}

// ListAllUsageLogs follows New API's paged log endpoint until the requested
// window is exhausted. maxPages is a safety bound, not a silent truncation:
// reaching it while the last page is full returns an error for the caller to
// retry with a narrower time window.
func (c *Client) ListAllUsageLogs(ctx context.Context, input UsageLogQuery, maxPages int) ([]UsageLog, error) {
	pageSize := input.PageSize
	if pageSize <= 0 || pageSize > 100 {
		pageSize = 100
	}
	if maxPages <= 0 {
		maxPages = 100
	}
	items := make([]UsageLog, 0, pageSize)
	for page := 1; page <= maxPages; page++ {
		input.Page = page
		input.PageSize = pageSize
		pageItems, err := c.ListUsageLogs(ctx, input)
		if err != nil {
			return nil, err
		}
		items = append(items, pageItems...)
		if len(pageItems) < pageSize {
			return items, nil
		}
	}
	return nil, fmt.Errorf("new api usage log exceeded %d pages; retry with a narrower time window", maxPages)
}

func (c *Client) listUsageLogs(ctx context.Context, query url.Values) ([]UsageLog, error) {
	endpoint := c.config.BaseURL + "/api/log/self/?" + query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.config.AccessToken)
	req.Header.Set("New-Api-User", strconv.FormatInt(c.config.UserID, 10))
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("new api usage log HTTP %d: %s", resp.StatusCode, compactError(body))
	}
	parsed := apiResponse{}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("parse new api usage log: %w", err)
	}
	if !parsed.Success {
		return nil, fmt.Errorf("new api usage log: %s", strings.TrimSpace(parsed.Message))
	}
	return parsed.Data.Items, nil
}

func compactError(body []byte) string {
	text := strings.Join(strings.Fields(string(body)), " ")
	if len(text) <= 300 {
		return text
	}
	return text[:300] + "..."
}
