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
	reserveOrderDetailPath  = "/admin-api/hpms/orderManage/reserveOrder/detail"
	reserveOrderByPhonePath = "/admin-api/hpms/orderManage/reserveOrder/detailByPhone"
	receptOrderDetailPath   = "/admin-api/hpms/orderManage/receptOrder/detail"
	receptOrderByPhonePath  = "/admin-api/hpms/orderManage/receptOrder/detailByPhone"
	renewCandidatePath      = "/admin-api/hpms/orderManage/receptOrder/renew/changeOrderCandidate/page"
	renewPath               = "/admin-api/hpms/orderManage/receptOrder/renew"
	roomStatusPath          = "/admin-api/hpms/roomDetails/realTimeRoomStatus/select"
	inventoryPath           = "/admin-api/hpms/changeInventory/query"
)

type Client struct {
	enabled       bool
	baseURL       string
	apiKey        string
	authorization string
	hotelID       string
	allowWrite    bool
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
		enabled:       cfg.Enabled,
		baseURL:       strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/"),
		apiKey:        strings.TrimSpace(cfg.APIKey),
		authorization: strings.TrimSpace(cfg.Authorization),
		hotelID:       strings.TrimSpace(cfg.HotelID),
		allowWrite:    cfg.AllowWrite,
		headers:       headers,
		httpClient:    &http.Client{Timeout: timeout},
	}
}

func (c *Client) Enabled() bool {
	return c != nil && c.enabled && c.baseURL != ""
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
	case "reserve_order_by_phone":
		endpoint = reserveOrderByPhonePath
	case "recept_order_detail":
		endpoint = receptOrderDetailPath
	case "recept_order_by_phone":
		endpoint = receptOrderByPhonePath
	case "renew_candidates":
		endpoint = renewCandidatePath
	case "room_status":
		endpoint = roomStatusPath
	case "inventory":
		endpoint = inventoryPath
	case "member_info_by_phone":
		endpoint = memberInfoByPhonePath
	case "member_benefits_by_grade":
		endpoint = memberBenefitsByGradePath
	default:
		return QueryResult{}, fmt.Errorf("PMS 接口文档尚未提供该查询类型")
	}

	memberQuery := isMemberQuery(action)
	normalizedArgs := normalizeQueryArgs(action, args)
	query := url.Values{}
	for key, value := range allowedQueryArgs(action, normalizedArgs) {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key != "" && value != "" {
			query.Set(key, value)
		}
	}
	if !memberQuery && c.hotelID != "" && query.Get("hotelId") == "" {
		query.Set("hotelId", c.hotelID)
	}
	if action == "inventory" {
		if err := c.bindInventoryTenant(query); err != nil {
			return QueryResult{}, err
		}
	}
	if err := validateReadQuery(action, query); err != nil {
		return QueryResult{}, err
	}
	if err := validateMemberQuery(action, query); err != nil {
		return QueryResult{}, err
	}
	if err := validateCurrentOrderLookup(action, query); err != nil {
		return QueryResult{}, err
	}
	requestURL := c.baseURL + endpoint
	if encoded := query.Encode(); encoded != "" {
		requestURL += "?" + encoded
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return QueryResult{}, fmt.Errorf("PMS 查询地址配置无效")
	}
	c.applyHeaders(request)
	request.Header.Set("Accept", "application/json")

	response, err := c.httpClient.Do(request)
	if err != nil {
		return QueryResult{}, fmt.Errorf("PMS 查询请求失败，请稍后重试")
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if err != nil {
		return QueryResult{}, fmt.Errorf("PMS 查询响应读取失败，请稍后重试")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return QueryResult{}, fmt.Errorf("PMS 查询失败，HTTP %d", response.StatusCode)
	}
	var payload map[string]any
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil {
		return QueryResult{}, fmt.Errorf("PMS 返回格式无法解析")
	}
	if memberQuery {
		if err := validateMemberResponseEnvelope(payload); err != nil {
			return QueryResult{}, err
		}
	}
	if code := firstString(payload, "code", "status"); !isSuccessfulCode(code) || !isSuccessfulResponse(payload) {
		if memberQuery {
			return QueryResult{}, memberQueryBusinessError(code)
		}
		return QueryResult{}, safeQueryBusinessError(payload)
	}
	var data any = payload
	if nested, ok := payload["data"]; ok {
		data = nested
	}
	if memberQuery {
		if _, ok := payload["data"]; !ok {
			return QueryResult{}, fmt.Errorf("会员查询返回缺少数据字段")
		}
		data, err = sanitizeMemberQueryData(action, data)
		if err != nil {
			return QueryResult{}, err
		}
	} else {
		data = sanitizeValue(data)
	}
	return QueryResult{
		Action: action,
		Data:   data,
		Source: "hpms",
		AsOf:   time.Now().Format(time.RFC3339),
	}, nil
}

// RenewRequest mirrors the documented HPMS renew payload. IDs stay int64 so
// they never pass through float64 during JSON encoding.
type RenewRequest struct {
	ReceptOrderID       int64              `json:"receptOrderId"`
	NewReserveOrderID   int64              `json:"newReserveOrderId,omitempty"`
	NewReceptOrderID    int64              `json:"newReceptOrderId,omitempty"`
	RenewType           string             `json:"renewType,omitempty"`
	RenewPriceMode      string             `json:"renewPriceMode,omitempty"`
	RenewHomeHandleType string             `json:"renewHomeHandleType,omitempty"`
	StartTime           string             `json:"startTime,omitempty"`
	EndTime             string             `json:"endTime,omitempty"`
	ReserveRemark       string             `json:"reserveRemark,omitempty"`
	PriceDetails        []RenewPriceDetail `json:"priceDetails,omitempty"`
}

type RenewPriceDetail struct {
	Date              string `json:"date,omitempty"`
	ConsumeAmount     string `json:"consumeAmount,omitempty"`
	OriginalAmount    string `json:"originalAmount,omitempty"`
	ConsumeAmountType string `json:"consumeAmountType,omitempty"`
	Currency          string `json:"currency,omitempty"`
}

type RenewResult struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Data      any    `json:"data,omitempty"`
	RequestID string `json:"requestId,omitempty"`
	Source    string `json:"source"`
	AsOf      string `json:"asOf"`
}

func (c *Client) Renew(ctx context.Context, input RenewRequest) (RenewResult, error) {
	if c == nil || !c.Enabled() {
		return RenewResult{}, fmt.Errorf("PMS 未配置或未启用")
	}
	if !c.allowWrite {
		return RenewResult{}, fmt.Errorf("PMS 写操作当前未启用")
	}
	if input.ReceptOrderID <= 0 {
		return RenewResult{}, fmt.Errorf("receptOrderId 必填")
	}
	if input.RenewType != "" && input.RenewType != "ORIGINAL" && input.RenewType != "CHANGE_ORDER" {
		return RenewResult{}, fmt.Errorf("renewType 不合法")
	}
	if input.RenewPriceMode != "" && input.RenewPriceMode != "LAST_DAY" && input.RenewPriceMode != "REAL_TIME" && input.RenewPriceMode != "CUSTOM" {
		return RenewResult{}, fmt.Errorf("renewPriceMode 不合法")
	}
	if input.RenewHomeHandleType != "" && input.RenewHomeHandleType != "KEEP_CURRENT_HOME" && input.RenewHomeHandleType != "USE_TARGET_HOME" && input.RenewHomeHandleType != "AUTO_ASSIGN_TARGET_ROOM_TYPE" {
		return RenewResult{}, fmt.Errorf("renewHomeHandleType 不合法")
	}
	body, err := json.Marshal(input)
	if err != nil {
		return RenewResult{}, fmt.Errorf("PMS 续住参数无法编码: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+renewPath, strings.NewReader(string(body)))
	if err != nil {
		return RenewResult{}, err
	}
	c.applyHeaders(request)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	response, err := c.httpClient.Do(request)
	if err != nil {
		return RenewResult{}, fmt.Errorf("PMS 续住提交失败: %w", err)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if err != nil {
		return RenewResult{}, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return RenewResult{}, fmt.Errorf("PMS 续住提交失败，HTTP %d", response.StatusCode)
	}
	var payload map[string]any
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil {
		return RenewResult{}, fmt.Errorf("PMS 返回格式无法解析: %w", err)
	}
	code := firstString(payload, "code", "status")
	message := firstString(payload, "msg", "message", "error")
	if !isSuccessfulCode(code) || !isSuccessfulResponse(payload) {
		if message == "" {
			message = "PMS 续住失败"
		}
		return RenewResult{Code: code, Message: message, Source: "hpms", AsOf: time.Now().Format(time.RFC3339)}, fmt.Errorf("%s", message)
	}
	data := any(payload)
	if nested, ok := payload["data"]; ok {
		data = nested
	}
	return RenewResult{
		Code:      code,
		Message:   message,
		Data:      sanitizeValue(data),
		RequestID: firstString(payload, "requestId", "request_id"),
		Source:    "hpms",
		AsOf:      time.Now().Format(time.RFC3339),
	}, nil
}

func (c *Client) applyHeaders(request *http.Request) {
	for key, value := range c.headers {
		request.Header.Set(key, value)
	}
	if c.apiKey != "" {
		request.Header.Set("X-API-Key", c.apiKey)
	}
	if c.authorization != "" {
		request.Header.Set("Authorization", c.authorization)
	}
}

func allowedQueryArgs(action string, args map[string]string) map[string]string {
	allowed := map[string]map[string]struct{}{
		"reserve_order_detail":     {"reserveOrderId": {}, "hotelId": {}, "tenantId": {}},
		"reserve_order_by_phone":   {"phone": {}, "customerNo": {}, "hotelId": {}, "tenantId": {}},
		"recept_order_detail":      {"receptOrderId": {}, "hotelId": {}, "tenantId": {}},
		"recept_order_by_phone":    {"phone": {}, "customerNo": {}, "hotelId": {}, "tenantId": {}},
		"renew_candidates":         {"currentReceptOrderId": {}, "reserveOrderNo": {}, "reserveName": {}, "reservePhone": {}, "pageNum": {}, "pageSize": {}, "hotelId": {}, "tenantId": {}},
		"room_status":              {"keyword": {}, "buildName": {}, "floorName": {}, "roomIdList": {}, "homeStatusList": {}, "channelTypeList": {}, "linkStatus": {}, "hotelId": {}, "tenantId": {}},
		"inventory":                {"beginTime": {}, "endTime": {}, "roomId": {}, "channelType": {}, "hotelTargetId": {}, "hotelTargetType": {}, "orderType": {}, "hourDuration": {}, "hotelId": {}, "tenantId": {}},
		"member_info_by_phone":     {"phone": {}},
		"member_benefits_by_grade": {"gradeId": {}, "gradeCode": {}},
	}
	set := allowed[action]
	ret := make(map[string]string, len(set))
	for key, value := range args {
		if _, ok := set[key]; ok {
			ret[key] = value
		}
	}
	return ret
}

func (c *Client) bindInventoryTenant(query url.Values) error {
	tenantID := ""
	for key, value := range c.headers {
		if strings.EqualFold(strings.TrimSpace(key), "tenant-id") {
			tenantID = strings.TrimSpace(value)
			break
		}
	}
	if requested := query.Get("tenantId"); requested != "" && requested != tenantID {
		return fmt.Errorf("PMS 库存查询不能覆盖服务端租户")
	}
	if tenantID != "" {
		query.Set("tenantId", tenantID)
	}
	return nil
}

func normalizeQueryArgs(action string, args map[string]string) map[string]string {
	ret := make(map[string]string, len(args)+4)
	for key, value := range args {
		ret[key] = value
	}
	if strings.TrimSpace(ret["customerNo"]) == "" && strings.TrimSpace(ret["memberId"]) != "" {
		ret["customerNo"] = ret["memberId"]
	}
	if action == "inventory" {
		if strings.TrimSpace(ret["beginTime"]) == "" {
			ret["beginTime"] = ret["startDate"]
		}
		if strings.TrimSpace(ret["endTime"]) == "" {
			ret["endTime"] = ret["endDate"]
		}
		if strings.TrimSpace(ret["roomId"]) == "" {
			ret["roomId"] = ret["roomTypeId"]
		}
	}
	if action == "renew_candidates" {
		if strings.TrimSpace(ret["pageNum"]) == "" {
			ret["pageNum"] = "1"
		}
		if strings.TrimSpace(ret["pageSize"]) == "" {
			ret["pageSize"] = "20"
		}
	}
	return ret
}

func validateReadQuery(action string, query url.Values) error {
	switch action {
	case "reserve_order_detail":
		if strings.TrimSpace(query.Get("reserveOrderId")) == "" {
			return fmt.Errorf("预订单详情查询需要真实预订单 ID")
		}
	case "recept_order_detail":
		if strings.TrimSpace(query.Get("receptOrderId")) == "" {
			return fmt.Errorf("接待单详情查询需要真实接待单 ID")
		}
	case "inventory":
		if !validQueryDate(query.Get("beginTime")) || !validQueryDate(query.Get("endTime")) {
			return fmt.Errorf("库存查询需要完整的入住和离店日期")
		}
	case "renew_candidates":
		if strings.TrimSpace(query.Get("currentReceptOrderId")) == "" &&
			strings.TrimSpace(query.Get("reserveOrderNo")) == "" &&
			strings.TrimSpace(query.Get("reserveName")) == "" &&
			strings.TrimSpace(query.Get("reservePhone")) == "" {
			return fmt.Errorf("续住候选查询需要当前接待单或真实预订筛选条件")
		}
	}
	return nil
}

func validQueryDate(value string) bool {
	value = strings.TrimSpace(value)
	if len(value) != len("2006-01-02") {
		return false
	}
	_, err := time.Parse("2006-01-02", value)
	return err == nil
}

func validateCurrentOrderLookup(action string, query url.Values) error {
	if action != "reserve_order_by_phone" && action != "recept_order_by_phone" {
		return nil
	}
	if strings.TrimSpace(query.Get("hotelId")) == "" {
		return fmt.Errorf("酒店ID不能为空")
	}
	if strings.TrimSpace(query.Get("phone")) == "" && strings.TrimSpace(query.Get("customerNo")) == "" {
		return fmt.Errorf("手机号码和会员编号/协议公司编号必须要有一个")
	}
	return nil
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
		case json.Number:
			return typed.String()
		}
	}
	return ""
}

func isSuccessfulCode(code string) bool {
	code = strings.TrimSpace(code)
	return code == "" || code == "0" || code == "200" ||
		strings.EqualFold(code, "success") || strings.EqualFold(code, "ok")
}

func isSuccessfulResponse(payload map[string]any) bool {
	value, ok := payload["success"]
	if !ok {
		return true
	}
	success, ok := value.(bool)
	return ok && success
}

func safeQueryBusinessError(payload map[string]any) error {
	message := firstString(payload, "msg", "message", "error")
	for _, known := range []string{
		"检测到多条匹配订单", "接待单不存在", "预订单不存在",
		"酒店ID不能为空", "租户ID不能为空", "租户id不能为空",
		"手机号码和会员编号/协议公司编号必须要有一个",
	} {
		if strings.Contains(message, known) {
			return fmt.Errorf("%s", known)
		}
	}
	switch firstString(payload, "code", "status") {
	case "401", "403":
		return fmt.Errorf("PMS 查询认证或权限不足")
	default:
		return fmt.Errorf("PMS 查询暂时不可用，请稍后重试")
	}
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
	case json.Number:
		return typed
	default:
		return value
	}
}
