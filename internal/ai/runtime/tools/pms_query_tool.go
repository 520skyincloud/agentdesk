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
	return !config.PMSSandboxEnabled() && current != nil && current.PMS.Enabled && strings.TrimSpace(current.PMS.BaseURL) != ""
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
		Desc: "只读查询 PMS 的订单、实时房态、库存和会员信息。查询会员权益、升级或保级规则时，用客户手机号调用 member_benefits_by_phone；工具内部先查会员，再用真实等级查询权益和规则，不需要模型提供等级 ID 或猜测等级名称。仅查会员基本信息时使用 member_info_by_phone。gradeAvailable=false 或会员冻结/挂失时如实说明，不承诺可使用权益；权益配置不代表已升房、延退、发券或已执行其他操作。",
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
						"renew_candidates", "room_status", "inventory",
						"member_info_by_phone", "member_benefits_by_phone",
					},
					Description: "只读查询操作；不支持续住提交、改房、排房、换房、延迟退房、改价或会员权益履约。",
				}},
				orderedmap.Pair[string, *einojsonschema.Schema]{Key: "reserveOrderId", Value: &einojsonschema.Schema{Type: "string", Description: "预订单 ID。"}},
				orderedmap.Pair[string, *einojsonschema.Schema]{Key: "receptOrderId", Value: &einojsonschema.Schema{Type: "string", Description: "接待订单 ID。"}},
				orderedmap.Pair[string, *einojsonschema.Schema]{Key: "keyword", Value: &einojsonschema.Schema{Type: "string", Description: "房号、住客或订单号；按手机号查询时请使用 phone，不要把订单号放入 phone。"}},
				orderedmap.Pair[string, *einojsonschema.Schema]{Key: "phone", Value: &einojsonschema.Schema{Type: "string", Description: "客户提供的准确手机号。member_info_by_phone 和 member_benefits_by_phone 必填，必须为大陆手机号；可沿用当前会话客户已提供的手机号，不要用订单号或 keyword 代替。"}},
				orderedmap.Pair[string, *einojsonschema.Schema]{Key: "startDate", Value: &einojsonschema.Schema{Type: "string", Description: "库存开始日期，YYYY-MM-DD。"}},
				orderedmap.Pair[string, *einojsonschema.Schema]{Key: "endDate", Value: &einojsonschema.Schema{Type: "string", Description: "库存结束日期，YYYY-MM-DD。"}},
				orderedmap.Pair[string, *einojsonschema.Schema]{Key: "customerNo", Value: &einojsonschema.Schema{Type: "string", Description: "会员编号或协议公司编号；可与 phone 同时传入以缩小当前有效订单范围。"}},
				orderedmap.Pair[string, *einojsonschema.Schema]{Key: "memberId", Value: &einojsonschema.Schema{Type: "string", Description: "兼容旧调用的会员编号别名，内部按 customerNo 传递。"}},
				orderedmap.Pair[string, *einojsonschema.Schema]{Key: "currentReceptOrderId", Value: &einojsonschema.Schema{Type: "string", Description: "换单续住候选查询的当前接待单 ID。"}},
				orderedmap.Pair[string, *einojsonschema.Schema]{Key: "reserveOrderNo", Value: &einojsonschema.Schema{Type: "string", Description: "换单续住候选的预订单号。"}},
				orderedmap.Pair[string, *einojsonschema.Schema]{Key: "reserveName", Value: &einojsonschema.Schema{Type: "string", Description: "换单续住候选的预订人。"}},
				orderedmap.Pair[string, *einojsonschema.Schema]{Key: "reservePhone", Value: &einojsonschema.Schema{Type: "string", Description: "换单续住候选的联系电话。"}},
			)),
		}),
		Extra: map[string]any{"toolCode": toolx.BuiltinPMSQuery.Code, "sourceType": toolx.BuiltinPMSQuery.SourceType},
	}, nil
}

func (t *PMSQueryTool) InvokableRun(ctx context.Context, argumentsInJSON string, opts ...einotool.Option) (string, error) {
	if config.PMSSandboxEnabled() {
		return `{"status":"unsupported","source":"测试 PMS","message":"当前使用测试 PMS，业务查询由对应场景执行，未查询外部酒店系统。"}`, nil
	}
	var input struct {
		Action               string `json:"action"`
		ReserveOrderID       string `json:"reserveOrderId"`
		ReceptOrderID        string `json:"receptOrderId"`
		Keyword              string `json:"keyword"`
		StartDate            string `json:"startDate"`
		EndDate              string `json:"endDate"`
		MemberID             string `json:"memberId"`
		CustomerNo           string `json:"customerNo"`
		Phone                string `json:"phone"`
		CurrentReceptOrderID string `json:"currentReceptOrderId"`
		ReserveOrderNo       string `json:"reserveOrderNo"`
		ReserveName          string `json:"reserveName"`
		ReservePhone         string `json:"reservePhone"`
	}
	if err := json.Unmarshal([]byte(argumentsInJSON), &input); err != nil {
		return "", fmt.Errorf("PMS 查询参数 JSON 不合法")
	}
	input.Action = strings.TrimSpace(input.Action)
	if !isPMSReadOnlyAction(input.Action) {
		return `{"status":"unsupported","message":"当前客服 PMS 工具只支持只读查询，未执行任何办理操作。"}`, nil
	}
	args := map[string]string{
		"reserveOrderId":       input.ReserveOrderID,
		"receptOrderId":        input.ReceptOrderID,
		"keyword":              input.Keyword,
		"startDate":            input.StartDate,
		"endDate":              input.EndDate,
		"customerNo":           firstNonEmpty(input.CustomerNo, input.MemberID),
		"currentReceptOrderId": input.CurrentReceptOrderID,
		"reserveOrderNo":       input.ReserveOrderNo,
		"reserveName":          input.ReserveName,
		"reservePhone":         input.ReservePhone,
		"phone":                phoneArgForAction(input.Action, input.Phone, input.Keyword),
	}
	client := pms.NewClient(config.Current().PMS)
	callCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	var result pms.QueryResult
	var err error
	partialMessage := ""
	if input.Action == "member_benefits_by_phone" {
		result, partialMessage, err = queryMemberBenefitsByPhone(callCtx, client, args["phone"])
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
		"renew_candidates", "room_status", "inventory",
		"member_info_by_phone", "member_benefits_by_phone":
		return true
	default:
		return false
	}
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
		return phone
	}
	return firstNonEmpty(phone, keyword)
}
