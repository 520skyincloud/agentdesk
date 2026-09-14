package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"agent-desk/internal/ai/runtime/registry"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/config"
	"agent-desk/internal/pkg/toolx"
	"agent-desk/internal/pms"
	"agent-desk/internal/services"

	einotool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	einojsonschema "github.com/eino-contrib/jsonschema"
	orderedmap "github.com/wk8/go-ordered-map/v2"
)

type PMSQueryTool struct {
	conversation    models.Conversation
	sourceMessageID int64
}

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
	return &PMSQueryTool{conversation: ctx.Conversation, sourceMessageID: ctx.UserMessage.ID}, nil
}

func (t *PMSQueryTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: toolx.BuiltinPMSQuery.Name,
		Desc: "受控查询和续住 PMS。支持预订单/接待单详情、按手机号或会员编号/协议公司编号查当前有效订单、换单续住候选、实时房态和房情库存；续住仅在测试环境显式开启且客户确认后执行。",
		ParamsOneOf: schema.NewParamsOneOfByJSONSchema(&einojsonschema.Schema{
			Version:  einojsonschema.Version,
			Type:     "object",
			Required: []string{"action"},
			Properties: orderedmap.New[string, *einojsonschema.Schema](orderedmap.WithInitialData(
				orderedmap.Pair[string, *einojsonschema.Schema]{Key: "action", Value: &einojsonschema.Schema{Type: "string", Description: "reserve_order_detail、reserve_order_by_phone、recept_order_detail、recept_order_by_phone、renew_candidates、room_status、inventory 或 renew。"}},
				orderedmap.Pair[string, *einojsonschema.Schema]{Key: "reserveOrderId", Value: &einojsonschema.Schema{Type: "string", Description: "预订单 ID。"}},
				orderedmap.Pair[string, *einojsonschema.Schema]{Key: "receptOrderId", Value: &einojsonschema.Schema{Type: "string", Description: "接待订单 ID。"}},
				orderedmap.Pair[string, *einojsonschema.Schema]{Key: "keyword", Value: &einojsonschema.Schema{Type: "string", Description: "房号、住客或订单号；按手机号查询时请使用 phone，不要把订单号放入 phone。"}},
				orderedmap.Pair[string, *einojsonschema.Schema]{Key: "phone", Value: &einojsonschema.Schema{Type: "string", Description: "手机号查当前有效预订单或接待单。"}},
				orderedmap.Pair[string, *einojsonschema.Schema]{Key: "startDate", Value: &einojsonschema.Schema{Type: "string", Description: "库存开始日期，YYYY-MM-DD。"}},
				orderedmap.Pair[string, *einojsonschema.Schema]{Key: "endDate", Value: &einojsonschema.Schema{Type: "string", Description: "库存结束日期，YYYY-MM-DD。"}},
				orderedmap.Pair[string, *einojsonschema.Schema]{Key: "customerNo", Value: &einojsonschema.Schema{Type: "string", Description: "会员编号或协议公司编号；可与 phone 同时传入以缩小当前有效订单范围。"}},
				orderedmap.Pair[string, *einojsonschema.Schema]{Key: "memberId", Value: &einojsonschema.Schema{Type: "string", Description: "兼容旧调用的会员编号别名，内部按 customerNo 传递。"}},
				orderedmap.Pair[string, *einojsonschema.Schema]{Key: "currentReceptOrderId", Value: &einojsonschema.Schema{Type: "string", Description: "换单续住候选查询的当前接待单 ID。"}},
				orderedmap.Pair[string, *einojsonschema.Schema]{Key: "reserveOrderNo", Value: &einojsonschema.Schema{Type: "string", Description: "换单续住候选的预订单号。"}},
				orderedmap.Pair[string, *einojsonschema.Schema]{Key: "reserveName", Value: &einojsonschema.Schema{Type: "string", Description: "换单续住候选的预订人。"}},
				orderedmap.Pair[string, *einojsonschema.Schema]{Key: "reservePhone", Value: &einojsonschema.Schema{Type: "string", Description: "换单续住候选的联系电话。"}},
				orderedmap.Pair[string, *einojsonschema.Schema]{Key: "renewPayload", Value: &einojsonschema.Schema{Type: "object", Description: "续住提交参数。仅在客户已确认当前预览且测试环境允许写操作时使用。"}},
			)),
		}),
		Extra: map[string]any{"toolCode": toolx.BuiltinPMSQuery.Code, "sourceType": toolx.BuiltinPMSQuery.SourceType},
	}, nil
}

func (t *PMSQueryTool) InvokableRun(ctx context.Context, argumentsInJSON string, opts ...einotool.Option) (string, error) {
	var input struct {
		Action               string            `json:"action"`
		ReserveOrderID       string            `json:"reserveOrderId"`
		ReceptOrderID        string            `json:"receptOrderId"`
		Keyword              string            `json:"keyword"`
		StartDate            string            `json:"startDate"`
		EndDate              string            `json:"endDate"`
		MemberID             string            `json:"memberId"`
		CustomerNo           string            `json:"customerNo"`
		Phone                string            `json:"phone"`
		CurrentReceptOrderID string            `json:"currentReceptOrderId"`
		ReserveOrderNo       string            `json:"reserveOrderNo"`
		ReserveName          string            `json:"reserveName"`
		ReservePhone         string            `json:"reservePhone"`
		RenewPayload         *pms.RenewRequest `json:"renewPayload"`
	}
	if err := json.Unmarshal([]byte(argumentsInJSON), &input); err != nil {
		return "", fmt.Errorf("PMS 查询参数 JSON 不合法: %w", err)
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
	if input.Action == "renew" {
		if input.RenewPayload == nil {
			return `{"status":"invalid","message":"renewPayload 必填"}`, nil
		}
		draft, err := services.PMSOperationService.CreateRenewDraft(
			t.conversation.ID,
			t.sourceMessageID,
			*input.RenewPayload,
			buildRenewPreview(*input.RenewPayload),
		)
		if err != nil {
			payload, marshalErr := json.Marshal(map[string]any{"status": "unavailable", "message": err.Error()})
			if marshalErr != nil {
				return "", marshalErr
			}
			return string(payload), nil
		}
		payload, err := json.Marshal(map[string]any{
			"status":      "confirmation_required",
			"action":      input.Action,
			"operationId": draft.OperationID,
			"preview":     draft.PreviewText,
			"message":     "已生成续住预览，必须等待客户明确确认后再提交 PMS。",
		})
		if err != nil {
			return "", err
		}
		return string(payload), nil
	}
	result, err := client.Query(callCtx, input.Action, args)
	if err != nil {
		payload, marshalErr := json.Marshal(map[string]any{"status": "unavailable", "message": err.Error()})
		if marshalErr != nil {
			return "", marshalErr
		}
		return string(payload), nil
	}
	payload, err := json.Marshal(map[string]any{
		"status": "ok",
		"action": result.Action,
		"data":   result.Data,
		"source": result.Source,
		"asOf":   result.AsOf,
	})
	if err != nil {
		return "", err
	}
	return string(payload), nil
}

func buildRenewPreview(request pms.RenewRequest) string {
	parts := []string{"续住"}
	if request.StartTime != "" || request.EndTime != "" {
		parts = append(parts, strings.TrimSpace(request.StartTime)+" 至 "+strings.TrimSpace(request.EndTime))
	}
	if request.RenewType != "" {
		parts = append(parts, "类型 "+request.RenewType)
	}
	if request.RenewPriceMode != "" {
		parts = append(parts, "取价 "+request.RenewPriceMode)
	}
	if request.RenewHomeHandleType != "" {
		parts = append(parts, "房间处理 "+request.RenewHomeHandleType)
	}
	return strings.Join(parts, "，")
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
	if action == "reserve_order_by_phone" || action == "recept_order_by_phone" {
		return phone
	}
	return firstNonEmpty(phone, keyword)
}
