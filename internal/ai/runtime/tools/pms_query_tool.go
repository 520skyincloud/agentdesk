package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"agent-desk/internal/ai/runtime/registry"
	"agent-desk/internal/pkg/config"
	"agent-desk/internal/pkg/toolx"
	"agent-desk/internal/pms"

	einotool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	einojsonschema "github.com/eino-contrib/jsonschema"
	orderedmap "github.com/wk8/go-ordered-map/v2"
)

type PMSQueryTool struct{}

func NewPMSQueryTool() *PMSQueryTool { return &PMSQueryTool{} }

func (t *PMSQueryTool) Spec() toolx.ToolSpec { return toolx.BuiltinPMSQuery }
func (t *PMSQueryTool) Name() string         { return toolx.BuiltinPMSQuery.Name }
func (t *PMSQueryTool) Code() string         { return toolx.BuiltinPMSQuery.Code }

func (t *PMSQueryTool) Enabled(ctx registry.Context) bool {
	current := config.CurrentOrNil()
	return current != nil && current.PMS.Enabled && strings.TrimSpace(current.PMS.BaseURL) != ""
}

func (t *PMSQueryTool) Build(ctx registry.Context) (einotool.BaseTool, error) {
	if !t.Enabled(ctx) {
		return nil, nil
	}
	return NewPMSQueryTool(), nil
}

func (t *PMSQueryTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: toolx.BuiltinPMSQuery.Name,
		Desc: "只读查询 PMS 的订单、实时房态、库存、会员信息和差价评估。stay_room_availability 会组合实时房态返回的具体房间、未来订单入住/离店区间和锁房/维修状态，计算整个入住区间无冲突的候选房号；它不锁房、不排房，超过实时房态未来30天覆盖范围时只返回 partial。查询会员权益、升级或保级规则时，用客户手机号调用 member_benefits_by_phone；工具内部先查会员，再用真实等级查询权益和规则，不需要模型提供等级 ID 或猜测等级名称。仅查会员基本信息时使用 member_info_by_phone。price_difference 只调用订单详情和库存 GET 接口，只有日期、每日金额、币种和相同计价口径齐全时才返回 exact；缺失或跨日期价格不完整时返回 quote_only/insufficient_data，不能把空价格当成免费或把估算说成最终差价。gradeAvailable=false 或会员冻结/挂失时如实说明，不承诺可使用权益；权益配置不代表已升房、延退、发券或已执行其他操作。",
		ParamsOneOf: schema.NewParamsOneOfByJSONSchema(&einojsonschema.Schema{
			Version:  einojsonschema.Version,
			Type:     "object",
			Required: []string{"action"},
			Properties: orderedmap.New[string, *einojsonschema.Schema](orderedmap.WithInitialData(
				orderedmap.Pair[string, *einojsonschema.Schema]{Key: "action", Value: &einojsonschema.Schema{
					Type: "string",
					Enum: []any{
						"reserve_order_detail", "reserve_order_by_phone",
						"recept_order_detail", "recept_order_by_phone",
						"renew_candidates", "room_status", "inventory", "stay_room_availability",
						"member_info_by_phone", "member_benefits_by_phone", "price_difference",
					},
					Description: "只读查询操作；stay_room_availability 需要完整入住和离店日期，可选真实 roomTypeId，并只返回无占用冲突的具体候选房号；price_difference 需要真实的 reserveOrderId 或 receptOrderId、目标 roomTypeId 和入住日期。服务端只调用已有 GET 查询；不支持续住提交、改房、排房、换房、延迟退房、改价或会员权益履约。",
				}},
				orderedmap.Pair[string, *einojsonschema.Schema]{Key: "reserveOrderId", Value: &einojsonschema.Schema{Type: "string", Description: "预订单 ID。"}},
				orderedmap.Pair[string, *einojsonschema.Schema]{Key: "receptOrderId", Value: &einojsonschema.Schema{Type: "string", Description: "接待订单 ID。"}},
				orderedmap.Pair[string, *einojsonschema.Schema]{Key: "keyword", Value: &einojsonschema.Schema{Type: "string", Description: "房号、住客或订单号；按手机号查询时请使用 phone，不要把订单号放入 phone。"}},
				orderedmap.Pair[string, *einojsonschema.Schema]{Key: "phone", Value: &einojsonschema.Schema{Type: "string", Description: "客户提供的准确手机号。member_info_by_phone 和 member_benefits_by_phone 必填，必须为大陆手机号；可沿用当前会话客户已提供的手机号，不要用订单号或 keyword 代替。"}},
				orderedmap.Pair[string, *einojsonschema.Schema]{Key: "startDate", Value: &einojsonschema.Schema{Type: "string", Description: "库存开始日期，YYYY-MM-DD。"}},
				orderedmap.Pair[string, *einojsonschema.Schema]{Key: "endDate", Value: &einojsonschema.Schema{Type: "string", Description: "库存结束日期，YYYY-MM-DD。"}},
				orderedmap.Pair[string, *einojsonschema.Schema]{Key: "beginTime", Value: &einojsonschema.Schema{Type: "string", Description: "库存开始日期，YYYY-MM-DD；与 startDate 二选一，优先使用 beginTime。"}},
				orderedmap.Pair[string, *einojsonschema.Schema]{Key: "endTime", Value: &einojsonschema.Schema{Type: "string", Description: "库存结束日期，YYYY-MM-DD；与 endDate 二选一，优先使用 endTime。"}},
				orderedmap.Pair[string, *einojsonschema.Schema]{Key: "roomTypeId", Value: &einojsonschema.Schema{Type: "string", Description: "只读筛选的房型 ID；只能使用 PMS 返回的真实房型 ID，不得猜测。"}},
				orderedmap.Pair[string, *einojsonschema.Schema]{Key: "metrics", Value: &einojsonschema.Schema{Type: "string", Description: "库存指标筛选，按 PMS 支持的逗号分隔值传入；不填写时由服务端使用默认指标。"}},
				orderedmap.Pair[string, *einojsonschema.Schema]{Key: "customerNo", Value: &einojsonschema.Schema{Type: "string", Description: "会员编号或协议公司编号；查询当前有效订单时可单独使用，也可与 phone 同时传入缩小范围；不能代替会员查询所需手机号。"}},
				orderedmap.Pair[string, *einojsonschema.Schema]{Key: "memberId", Value: &einojsonschema.Schema{Type: "string", Description: "兼容旧调用的会员编号别名，内部按 customerNo 传递。"}},
				orderedmap.Pair[string, *einojsonschema.Schema]{Key: "currentReceptOrderId", Value: &einojsonschema.Schema{Type: "string", Description: "换单续住候选查询的当前接待单 ID。"}},
				orderedmap.Pair[string, *einojsonschema.Schema]{Key: "reserveOrderNo", Value: &einojsonschema.Schema{Type: "string", Description: "换单续住候选的预订单号。"}},
				orderedmap.Pair[string, *einojsonschema.Schema]{Key: "reserveName", Value: &einojsonschema.Schema{Type: "string", Description: "换单续住候选的预订人。"}},
				orderedmap.Pair[string, *einojsonschema.Schema]{Key: "reservePhone", Value: &einojsonschema.Schema{Type: "string", Description: "换单续住候选的联系电话。"}},
				orderedmap.Pair[string, *einojsonschema.Schema]{Key: "pageNum", Value: &einojsonschema.Schema{Type: "integer", Description: "换单续住候选页码，默认 1。"}},
				orderedmap.Pair[string, *einojsonschema.Schema]{Key: "pageSize", Value: &einojsonschema.Schema{Type: "integer", Description: "换单续住候选每页数量，默认 20，最大 100。"}},
				orderedmap.Pair[string, *einojsonschema.Schema]{Key: "excludeReserveOrderId", Value: &einojsonschema.Schema{Type: "string", Description: "计算具体房间可用性时排除当前客户自己的预订单 ID。"}},
				orderedmap.Pair[string, *einojsonschema.Schema]{Key: "excludeReceptOrderId", Value: &einojsonschema.Schema{Type: "string", Description: "计算具体房间可用性时排除当前客户自己的接待单 ID。"}},
			)),
		}),
		Extra: map[string]any{"toolCode": toolx.BuiltinPMSQuery.Code, "sourceType": toolx.BuiltinPMSQuery.SourceType},
	}, nil
}

func (t *PMSQueryTool) InvokableRun(ctx context.Context, argumentsInJSON string, opts ...einotool.Option) (string, error) {
	var input struct {
		Action                string `json:"action"`
		ReserveOrderID        string `json:"reserveOrderId"`
		ReceptOrderID         string `json:"receptOrderId"`
		Keyword               string `json:"keyword"`
		StartDate             string `json:"startDate"`
		EndDate               string `json:"endDate"`
		BeginTime             string `json:"beginTime"`
		EndTime               string `json:"endTime"`
		RoomTypeID            string `json:"roomTypeId"`
		Metrics               string `json:"metrics"`
		MemberID              string `json:"memberId"`
		CustomerNo            string `json:"customerNo"`
		Phone                 string `json:"phone"`
		CurrentReceptOrderID  string `json:"currentReceptOrderId"`
		ReserveOrderNo        string `json:"reserveOrderNo"`
		ReserveName           string `json:"reserveName"`
		ReservePhone          string `json:"reservePhone"`
		PageNum               int    `json:"pageNum"`
		PageSize              int    `json:"pageSize"`
		ExcludeReserveOrderID string `json:"excludeReserveOrderId"`
		ExcludeReceptOrderID  string `json:"excludeReceptOrderId"`
	}
	if err := json.Unmarshal([]byte(argumentsInJSON), &input); err != nil {
		return "", fmt.Errorf("PMS 查询参数 JSON 不合法")
	}
	input.Action = strings.TrimSpace(input.Action)
	if !isPMSReadOnlyAction(input.Action) {
		return `{"status":"unsupported","message":"当前客服 PMS 工具只支持只读查询，未执行任何办理操作。"}`, nil
	}
	if err := validatePMSReadOnlyInput(input.Action, input.ReserveOrderID, input.ReceptOrderID, input.Phone, firstNonEmpty(input.CustomerNo, input.MemberID), input.Keyword,
		input.BeginTime, input.StartDate, input.EndTime, input.EndDate, input.RoomTypeID,
		input.CurrentReceptOrderID, input.ReserveOrderNo, input.ReserveName, input.ReservePhone); err != nil {
		payload, marshalErr := json.Marshal(map[string]any{"status": "unavailable", "message": err.Error()})
		if marshalErr != nil {
			return "", marshalErr
		}
		return string(payload), nil
	}
	if input.PageNum <= 0 {
		input.PageNum = 1
	}
	if input.PageSize <= 0 {
		input.PageSize = 20
	}
	if input.PageSize > 100 {
		input.PageSize = 100
	}
	args := map[string]string{
		"reserveOrderId":        input.ReserveOrderID,
		"receptOrderId":         input.ReceptOrderID,
		"keyword":               input.Keyword,
		"startDate":             input.StartDate,
		"endDate":               input.EndDate,
		"beginTime":             input.BeginTime,
		"endTime":               input.EndTime,
		"roomTypeId":            input.RoomTypeID,
		"metrics":               input.Metrics,
		"customerNo":            firstNonEmpty(input.CustomerNo, input.MemberID),
		"currentReceptOrderId":  input.CurrentReceptOrderID,
		"reserveOrderNo":        input.ReserveOrderNo,
		"reserveName":           input.ReserveName,
		"reservePhone":          normalizePMSPhone(input.ReservePhone),
		"phone":                 phoneArgForAction(input.Action, input.Phone, input.Keyword),
		"pageNum":               fmt.Sprintf("%d", input.PageNum),
		"pageSize":              fmt.Sprintf("%d", input.PageSize),
		"excludeReserveOrderId": input.ExcludeReserveOrderID,
		"excludeReceptOrderId":  input.ExcludeReceptOrderID,
	}
	client := pms.NewClient(config.Current().PMS)
	callCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	var result pms.QueryResult
	var err error
	partialMessage := ""
	if input.Action == "member_benefits_by_phone" {
		result, partialMessage, err = queryMemberBenefitsByPhone(callCtx, client, args["phone"])
	} else if input.Action == "price_difference" {
		result, err = queryPriceDifference(callCtx, client, input.ReserveOrderID, input.ReceptOrderID, input.RoomTypeID, firstNonEmpty(input.BeginTime, input.StartDate), firstNonEmpty(input.EndTime, input.EndDate))
	} else if input.Action == "stay_room_availability" {
		result, err = queryStayRoomAvailability(callCtx, client, input.RoomTypeID,
			firstNonEmpty(input.BeginTime, input.StartDate), firstNonEmpty(input.EndTime, input.EndDate),
			input.ExcludeReserveOrderID, input.ExcludeReceptOrderID)
	} else {
		result, err = client.Query(callCtx, input.Action, args)
		if err == nil {
			result.Data, err = pms.CustomerQueryData(result.Action, result.Data)
		}
	}
	if err != nil {
		payload, marshalErr := json.Marshal(map[string]any{"status": "unavailable", "message": err.Error()})
		if marshalErr != nil {
			return "", marshalErr
		}
		return string(payload), nil
	}
	output := map[string]any{
		"status": "ok",
		"action": result.Action,
		"data":   result.Data,
		"source": result.Source,
		"asOf":   result.AsOf,
	}
	if partialMessage != "" {
		output["status"] = "partial"
		output["message"] = partialMessage
	}
	payload, err := json.Marshal(output)
	if err != nil {
		return "", err
	}
	return string(payload), nil
}

func queryMemberBenefitsByPhone(ctx context.Context, client *pms.Client, phone string) (pms.QueryResult, string, error) {
	member, err := client.Query(ctx, "member_info_by_phone", map[string]string{"phone": phone})
	if err != nil {
		return pms.QueryResult{}, "", err
	}
	member.Data, err = pms.CustomerQueryData(member.Action, member.Data)
	if err != nil {
		return pms.QueryResult{}, "", err
	}
	memberData, ok := member.Data.(map[string]any)
	if !ok {
		return pms.QueryResult{}, "", fmt.Errorf("会员查询数据结构不正确")
	}
	data := map[string]any{"member": memberData, "grade": nil}
	result := pms.QueryResult{
		Action: "member_benefits_by_phone", Data: data, Source: member.Source, AsOf: member.AsOf,
	}
	gradeID, _ := memberData["gradeId"].(string)
	if strings.TrimSpace(gradeID) == "" {
		return result, "已查到会员信息，但当前会员没有可查询的等级 ID，无法确认权益和等级规则。", nil
	}
	grade, err := client.Query(ctx, "member_benefits_by_grade", map[string]string{"gradeId": gradeID})
	if err == nil {
		grade.Data, err = pms.CustomerQueryData(grade.Action, grade.Data)
	}
	if err != nil {
		return result, "已查到会员信息，但权益和等级规则查询未完成：" + err.Error(), nil
	}
	data["grade"] = grade.Data
	result.AsOf = grade.AsOf
	return result, "", nil
}

func isPMSReadOnlyAction(action string) bool {
	switch action {
	case "reserve_order_detail", "reserve_order_by_phone",
		"recept_order_detail", "recept_order_by_phone",
		"renew_candidates", "room_status", "inventory", "stay_room_availability",
		"member_info_by_phone", "member_benefits_by_phone", "price_difference":
		return true
	default:
		return false
	}
}

func validatePMSReadOnlyInput(action, reserveOrderID, receptOrderID, phone, customerNo, keyword, beginTime, startDate, endTime, endDate, roomTypeID, currentReceptOrderID, reserveOrderNo, reserveName, reservePhone string) error {
	switch action {
	case "reserve_order_detail":
		if strings.TrimSpace(reserveOrderID) == "" {
			return fmt.Errorf("预订单详情查询需要真实预订单 ID")
		}
	case "recept_order_detail":
		if strings.TrimSpace(receptOrderID) == "" {
			return fmt.Errorf("接待单详情查询需要真实接待单 ID")
		}
	case "reserve_order_by_phone", "recept_order_by_phone":
		if normalizePMSPhone(phone) == "" && strings.TrimSpace(customerNo) == "" {
			return fmt.Errorf("订单查询需要客户提供的有效手机号或会员编号/协议公司编号")
		}
	case "member_info_by_phone", "member_benefits_by_phone":
		if normalizePMSPhone(phone) == "" {
			return fmt.Errorf("查询需要客户提供的有效手机号")
		}
	case "inventory":
		if queryDate(firstNonEmpty(beginTime, startDate)) == "" || queryDate(firstNonEmpty(endTime, endDate)) == "" {
			return fmt.Errorf("库存查询需要完整的入住和离店日期")
		}
	case "stay_room_availability":
		if queryDate(firstNonEmpty(beginTime, startDate)) == "" || queryDate(firstNonEmpty(endTime, endDate)) == "" {
			return fmt.Errorf("具体房间可用性查询需要完整的入住和离店日期")
		}
	case "price_difference":
		if strings.TrimSpace(reserveOrderID) == "" && strings.TrimSpace(receptOrderID) == "" {
			return fmt.Errorf("差价评估需要先定位真实订单 ID")
		}
		if strings.TrimSpace(roomTypeID) == "" {
			return fmt.Errorf("差价评估需要真实目标房型 ID")
		}
	case "renew_candidates":
		if strings.TrimSpace(currentReceptOrderID) == "" && strings.TrimSpace(reserveOrderNo) == "" &&
			strings.TrimSpace(reserveName) == "" && normalizePMSPhone(reservePhone) == "" {
			return fmt.Errorf("续住候选查询需要当前接待单或真实预订筛选条件")
		}
	case "room_status":
		_ = keyword
	}
	return nil
}

func queryStayRoomAvailability(ctx context.Context, client *pms.Client, roomTypeID, startDate, endDate, excludeReserveOrderID, excludeReceptOrderID string) (pms.QueryResult, error) {
	roomStatus, err := client.Query(ctx, "room_status", nil)
	if err != nil {
		return pms.QueryResult{}, err
	}
	assessment, err := pms.AssessStayRoomAvailability(roomStatus.Data, pms.StayRoomAvailabilityRequest{
		RoomTypeID:            strings.TrimSpace(roomTypeID),
		StartDate:             startDate,
		EndDate:               endDate,
		ExcludeReserveOrderID: strings.TrimSpace(excludeReserveOrderID),
		ExcludeReceptOrderID:  strings.TrimSpace(excludeReceptOrderID),
	})
	if err != nil {
		return pms.QueryResult{}, err
	}
	return pms.QueryResult{
		Action: "stay_room_availability",
		Data:   assessment,
		Source: roomStatus.Source,
		AsOf:   roomStatus.AsOf,
	}, nil
}

func queryPriceDifference(ctx context.Context, client *pms.Client, reserveOrderID, receptOrderID, roomTypeID, startDate, endDate string) (pms.QueryResult, error) {
	reserveOrderID = strings.TrimSpace(reserveOrderID)
	receptOrderID = strings.TrimSpace(receptOrderID)
	roomTypeID = strings.TrimSpace(roomTypeID)
	if reserveOrderID == "" && receptOrderID == "" {
		return pms.QueryResult{}, fmt.Errorf("差价评估需要先定位真实订单 ID")
	}
	if roomTypeID == "" {
		return pms.QueryResult{}, fmt.Errorf("差价评估需要真实目标房型 ID")
	}

	orderAction := "reserve_order_detail"
	orderArgs := map[string]string{"reserveOrderId": reserveOrderID}
	if receptOrderID != "" {
		orderAction = "recept_order_detail"
		orderArgs = map[string]string{"receptOrderId": receptOrderID}
	}
	order, err := client.Query(ctx, orderAction, orderArgs)
	if err != nil {
		return pms.QueryResult{}, err
	}
	orderData, err := pms.CustomerQueryData(order.Action, order.Data)
	if err != nil {
		return pms.QueryResult{}, err
	}
	if strings.TrimSpace(startDate) == "" {
		startDate = orderDate(orderData, "checkInTime", "checkInBusinessDate")
	}
	if strings.TrimSpace(endDate) == "" {
		endDate = orderDate(orderData, "checkOutTime", "checkOutBusinessDate")
	}
	startDate = queryDate(startDate)
	endDate = queryDate(endDate)
	inventory, err := client.Query(ctx, "inventory", map[string]string{
		"beginTime":  startDate,
		"endTime":    endDate,
		"roomTypeId": roomTypeID,
		"metrics":    "sold,sellable",
	})
	if err != nil {
		return pms.QueryResult{}, err
	}
	inventoryData, err := pms.CustomerQueryData(inventory.Action, inventory.Data)
	if err != nil {
		return pms.QueryResult{}, err
	}
	assessment, err := pms.AssessPriceDifference(orderData, inventoryData, roomTypeID, startDate, endDate)
	if err != nil {
		return pms.QueryResult{}, err
	}
	return pms.QueryResult{
		Action: "price_difference",
		Data: map[string]any{
			"order":      orderData,
			"inventory":  inventoryData,
			"assessment": assessment,
		},
		Source: inventory.Source,
		AsOf:   inventory.AsOf,
	}, nil
}

func orderDate(orderData any, keys ...string) string {
	source, ok := orderData.(map[string]any)
	if !ok {
		return ""
	}
	for _, key := range keys {
		if value, ok := source[key].(string); ok && strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func queryDate(value string) string {
	value = strings.TrimSpace(value)
	if len(value) < len("2006-01-02") {
		return ""
	}
	value = value[:len("2006-01-02")]
	if _, err := time.Parse("2006-01-02", value); err != nil {
		return ""
	}
	return value
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func phoneArgForAction(action, phone, keyword string) string {
	if action == "reserve_order_by_phone" || action == "recept_order_by_phone" ||
		action == "member_info_by_phone" || action == "member_benefits_by_phone" {
		return normalizePMSPhone(phone)
	}
	return firstNonEmpty(phone, keyword)
}

func normalizePMSPhone(value string) string {
	digits := strings.NewReplacer("+", "", "-", "", " ", "", "\t", "").Replace(strings.TrimSpace(value))
	if strings.HasPrefix(digits, "86") && len(digits) == 13 {
		digits = strings.TrimPrefix(digits, "86")
	}
	if len(digits) != 11 || digits[0] != '1' || digits[1] < '3' || digits[1] > '9' {
		return ""
	}
	for _, digit := range digits {
		if digit < '0' || digit > '9' {
			return ""
		}
	}
	return digits
}
