package pms

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

	"agent-desk/internal/pkg/config"
)

const (
	reserveOrderDetailPath = "/admin-api/hpms/orderManage/reserveOrder/detail"
	receptOrderDetailPath  = "/admin-api/hpms/orderManage/receptOrder/detail"
	roomStatusPath         = "/admin-api/hpms/roomDetails/realTimeRoomStatus/select"
	inventoryPath          = "/admin-api/hpms/changeInventory/query"
)

type Client struct {
	baseURL       string
	apiKey        string
	authorization string
	hotelID       string
	headers       map[string]string
	httpClient    *http.Client
}

type QueryResult struct {
	Action string `json:"action"`
	Data   any    `json:"data,omitempty"`
	Source string `json:"source"`
	AsOf   string `json:"asOf"`
}

func NewClient(cfg config.PMSConfig) *Client {
	timeout := time.Duration(cfg.TimeoutMS) * time.Millisecond
	if timeout <= 0 {
		timeout = 12 * time.Second
	}
	headers := make(map[string]string, len(cfg.Headers))
	for key, value := range cfg.Headers {
		key = strings.TrimSpace(key)
		if key != "" {
			headers[key] = value
		}
	}
	return &Client{
		baseURL:       strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/"),
		apiKey:        strings.TrimSpace(cfg.APIKey),
		authorization: strings.TrimSpace(cfg.Authorization),
		hotelID:       strings.TrimSpace(cfg.HotelID),
		headers:       headers,
		httpClient:    &http.Client{Timeout: timeout},
	}
}

func (c *Client) Enabled() bool {
	return c != nil && c.baseURL != ""
}

func (c *Client) Query(ctx context.Context, action string, args map[string]string) (QueryResult, error) {
	if c == nil || !c.Enabled() {
		return QueryResult{}, fmt.Errorf("PMS 未配置或未启用")
	}
	action = strings.TrimSpace(action)
	var endpoint string
	switch action {
	case "reserve_order_detail":
		endpoint = reserveOrderDetailPath
	case "recept_order_detail":
		endpoint = receptOrderDetailPath
	case "room_status":
		endpoint = roomStatusPath
	case "inventory":
		endpoint = inventoryPath
	default:
		return QueryResult{}, fmt.Errorf("PMS 接口文档尚未提供查询类型: %s", action)
	}

	query := url.Values{}
	for key, value := range args {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key != "" && value != "" {
			query.Set(key, value)
		}
	}
	if c.hotelID != "" && query.Get("hotelId") == "" {
		query.Set("hotelId", c.hotelID)
	}
	requestURL := c.baseURL + endpoint
	if encoded := query.Encode(); encoded != "" {
		requestURL += "?" + encoded
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return QueryResult{}, err
	}
	for key, value := range c.headers {
		request.Header.Set(key, value)
	}
	if c.apiKey != "" {
		request.Header.Set("X-API-Key", c.apiKey)
	}
	if c.authorization != "" {
		request.Header.Set("Authorization", c.authorization)
	}
	request.Header.Set("Accept", "application/json")

	response, err := c.httpClient.Do(request)
	if err != nil {
		return QueryResult{}, fmt.Errorf("PMS 查询失败: %w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if err != nil {
		return QueryResult{}, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return QueryResult{}, fmt.Errorf("PMS 查询失败，HTTP %d", response.StatusCode)
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return QueryResult{}, fmt.Errorf("PMS 返回格式无法解析: %w", err)
	}
	if code := firstString(payload, "code", "status"); code != "" && code != "0" && !strings.EqualFold(code, "success") && !strings.EqualFold(code, "ok") {
		message := firstString(payload, "msg", "message", "error")
		if message == "" {
			message = "PMS 返回失败"
		}
		return QueryResult{}, fmt.Errorf("%s", message)
	}
	var data any = payload
	if nested, ok := payload["data"]; ok {
		data = nested
	}
	return QueryResult{
		Action: action,
		Data:   sanitizeValue(data),
		Source: "hpms",
		AsOf:   time.Now().Format(time.RFC3339),
	}, nil
}

func firstString(payload map[string]any, keys ...string) string {
	for _, key := range keys {
		value, ok := payload[key]
		if !ok {
			continue
		}
		switch typed := value.(type) {
		case string:
			return strings.TrimSpace(typed)
		case float64:
			return strconv.FormatInt(int64(typed), 10)
		}
	}
	return ""
}

func sanitizeValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		ret := make(map[string]any, len(typed))
		for key, item := range typed {
			lower := strings.ToLower(strings.TrimSpace(key))
			switch {
			case strings.Contains(lower, "password"),
				strings.Contains(lower, "token"),
				strings.Contains(lower, "secret"),
				strings.Contains(lower, "authorization"),
				strings.Contains(lower, "idcard"),
				strings.Contains(lower, "identity"):
				continue
			default:
				ret[key] = sanitizeValue(item)
			}
		}
		return ret
	case []any:
		ret := make([]any, len(typed))
		for index, item := range typed {
			ret[index] = sanitizeValue(item)
		}
		return ret
	default:
		return value
	}
}
