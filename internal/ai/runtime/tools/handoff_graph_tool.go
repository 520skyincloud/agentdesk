package tools

import (
	"context"

	"agent-desk/internal/ai/runtime/graphs"
	"agent-desk/internal/ai/runtime/registry"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/toolx"
	"agent-desk/internal/services"

	einotool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	einojsonschema "github.com/eino-contrib/jsonschema"
	orderedmap "github.com/wk8/go-ordered-map/v2"
)

type HandoffGraphTool struct {
	conversation models.Conversation
	aiAgent      models.AIAgent
}

func NewHandoffGraphTool() *HandoffGraphTool {
	return &HandoffGraphTool{}
}

func (t *HandoffGraphTool) Spec() toolx.ToolSpec {
	return toolx.GraphHandoffConversation
}

func (t *HandoffGraphTool) Name() string {
	return toolx.GraphHandoffConversation.Name
}

func (t *HandoffGraphTool) Code() string {
	return toolx.GraphHandoffConversation.Code
}

func (t *HandoffGraphTool) Enabled(ctx registry.Context) bool {
	if ctx.Conversation.ID <= 0 {
		return true
	}
	return services.WxWorkCustomerHandoffSettingService.IsAutoHandoffEnabledForConversation(ctx.Conversation.ID)
}

func (t *HandoffGraphTool) Build(ctx registry.Context) (einotool.BaseTool, error) {
	if !t.Enabled(ctx) {
		return nil, nil
	}
	return &HandoffGraphTool{
		conversation: ctx.Conversation,
		aiAgent:      ctx.AIAgent,
	}, nil
}

func (t *HandoffGraphTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: toolx.GraphHandoffConversation.Name,
		Desc: "Graph Tool。用于执行确定性的转人工流程。仅在用户明确要求人工客服，或你已确认必须转人工处理时调用；工具会直接转接，缺少必要房号时会自行追问房号，不需要再次向用户确认。若返回 auto_handoff_disabled，继续正常回答当前问题，不要声称已经回复或转接；若结果标记 terminal=true 且 shouldRetry=false，不要重复调用。",
		ParamsOneOf: schema.NewParamsOneOfByJSONSchema(&einojsonschema.Schema{
			Version: einojsonschema.Version,
			Type:    "object",
			Properties: orderedmap.New[string, *einojsonschema.Schema](orderedmap.WithInitialData(
				orderedmap.Pair[string, *einojsonschema.Schema]{
					Key: "reason",
					Value: &einojsonschema.Schema{
						Type:        "string",
						Description: "转人工原因，简洁说明为何需要人工介入，例如用户明确要求人工、问题需要人工核验、需要人工售后处理等。",
					},
				},
			)),
		}),
		Extra: map[string]any{
			"toolCode":   toolx.GraphHandoffConversation.Code,
			"sourceType": toolx.GraphHandoffConversation.SourceType,
		},
	}, nil
}

func (t *HandoffGraphTool) InvokableRun(ctx context.Context, argumentsInJSON string, opts ...einotool.Option) (string, error) {
	return graphs.NewHandoffGraph(t.conversation, t.aiAgent).Run(ctx, argumentsInJSON)
}
