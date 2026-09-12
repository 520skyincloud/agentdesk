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
	return &PMSQueryTool{}, nil
}

func (t *PMSQueryTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: toolx.BuiltinPMSQuery.Name,
		Desc: "只读查询 PMS。支持预订单详情、接待单详情、实时房态和房情库存；白名单、实名认证和订单综合搜索需 PMS 提供接口文档后再接入，不执行任何修改操作。",
		ParamsOneOf: schema.NewParamsOneOfByJSONSchema(&einojsonschema.Schema{
			Version:  einojsonschema.Version,
			Type:     "object",
			Required: []string{"action"},
			Properties: orderedmap.New[string, *einojsonschema.Schema](orderedmap.WithInitialData(
				orderedmap.Pair[string, *einojsonschema.Schema]{Key: "action", Value: &einojsonschema.Schema{Type: "string", Description: "reserve_order_detail、recept_order_detail、room_status 或 inventory。"}},
				orderedmap.Pair[string, *einojsonschema.Schema]{Key: "reserveOrderId", Value: &einojsonschema.Schema{Type: "string", Description: "预订单 ID。"}},
				orderedmap.Pair[string, *einojsonschema.Schema]{Key: "receptOrderId", Value: &einojsonschema.Schema{Type: "string", Description: "接待订单 ID。"}},
				orderedmap.Pair[string, *einojsonschema.Schema]{Key: "keyword", Value: &einojsonschema.Schema{Type: "string", Description: "房号、住客、手机号或订单号。"}},
				orderedmap.Pair[string, *einojsonschema.Schema]{Key: "startDate", Value: &einojsonschema.Schema{Type: "string", Description: "库存开始日期，YYYY-MM-DD。"}},
				orderedmap.Pair[string, *einojsonschema.Schema]{Key: "endDate", Value: &einojsonschema.Schema{Type: "string", Description: "库存结束日期，YYYY-MM-DD。"}},
				orderedmap.Pair[string, *einojsonschema.Schema]{Key: "memberId", Value: &einojsonschema.Schema{Type: "string", Description: "会员 ID（若 PMS 支持）。"}},
			)),
		}),
		Extra: map[string]any{"toolCode": toolx.BuiltinPMSQuery.Code, "sourceType": toolx.BuiltinPMSQuery.SourceType},
	}, nil
}

func (t *PMSQueryTool) InvokableRun(ctx context.Context, argumentsInJSON string, opts ...einotool.Option) (string, error) {
	var input struct {
		Action         string `json:"action"`
		ReserveOrderID string `json:"reserveOrderId"`
		ReceptOrderID  string `json:"receptOrderId"`
		Keyword        string `json:"keyword"`
		StartDate      string `json:"startDate"`
		EndDate        string `json:"endDate"`
		MemberID       string `json:"memberId"`
	}
	if err := json.Unmarshal([]byte(argumentsInJSON), &input); err != nil {
		return "", fmt.Errorf("PMS 查询参数 JSON 不合法: %w", err)
	}
	args := map[string]string{
		"reserveOrderId": input.ReserveOrderID,
		"receptOrderId":  input.ReceptOrderID,
		"keyword":        input.Keyword,
		"startDate":      input.StartDate,
		"endDate":        input.EndDate,
		"memberId":       input.MemberID,
	}
	client := pms.NewClient(config.Current().PMS)
	callCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
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
