package executor

import (
	"context"
	"testing"

	"agent-desk/internal/ai/runtime/registry"
	"agent-desk/internal/models"

	einotool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	einojsonschema "github.com/eino-contrib/jsonschema"
	orderedmap "github.com/wk8/go-ordered-map/v2"
)

// stubInvokableTool 是单测用的可执行工具，记录被调次数并返回固定结果。
type stubInvokableTool struct {
	name    string
	result  string
	called  *int
	hasJSON bool
}

func (t *stubInvokableTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	info := &schema.ToolInfo{
		Name: t.name,
		Desc: "stub",
	}
	if t.hasJSON {
		info.ParamsOneOf = schema.NewParamsOneOfByJSONSchema(&einojsonschema.Schema{
			Version: einojsonschema.Version,
			Type:    "object",
			Properties: orderedmap.New[string, *einojsonschema.Schema](orderedmap.WithInitialData(
				orderedmap.Pair[string, *einojsonschema.Schema]{
					Key:   "q",
					Value: &einojsonschema.Schema{Type: "string"},
				},
			)),
		})
	}
	return info, nil
}

func (t *stubInvokableTool) InvokableRun(ctx context.Context, args string, opts ...einotool.Option) (string, error) {
	if t.called != nil {
		*t.called++
	}
	return t.result, nil
}

func TestCollectRuntimeBaseToolsEmptyWhenNoToolSet(t *testing.T) {
	tools := collectRuntimeBaseTools(context.Background(), RunInput{})
	if len(tools) != 0 {
		t.Fatalf("expected no tools, got %d", len(tools))
	}
}

func TestBuildRuntimeToolInfosSkipsNilAndNameless(t *testing.T) {
	called := 0
	tools := []einotool.BaseTool{
		nil,
		&stubInvokableTool{name: "ok", result: "r", called: &called},
		&stubInvokableTool{name: "", result: "x", called: &called},
	}
	infos := buildRuntimeToolInfos(context.Background(), tools)
	if len(infos) != 1 || infos[0].Name != "ok" {
		t.Fatalf("unexpected infos: %+v", infos)
	}
}

func TestExecuteRuntimeToolCallRunsMatchingTool(t *testing.T) {
	called := 0
	tools := []einotool.BaseTool{
		&stubInvokableTool{name: "get_weather", result: "28度", called: &called},
	}
	result := executeRuntimeToolCall(context.Background(), tools, schema.ToolCall{Function: schema.FunctionCall{Name: "get_weather", Arguments: `{"location":"合肥"}`}})
	if result != "28度" || called != 1 {
		t.Fatalf("result=%q called=%d", result, called)
	}
}

func TestExecuteRuntimeToolCallReportsMissingTool(t *testing.T) {
	result := executeRuntimeToolCall(context.Background(), nil, schema.ToolCall{Function: schema.FunctionCall{Name: "nope"}})
	if result == "" {
		t.Fatal("expected missing-tool message")
	}
}

func TestRuntimeToolNameToCodeFromToolSetAndExtra(t *testing.T) {
	toolSet := &registry.ToolSet{
		StaticToolCodes: map[string]string{"天气": "builtin/get_weather"},
	}
	infos := []*schema.ToolInfo{
		{Name: "mcp_x", Extra: map[string]any{"toolCode": "mcp/server/x"}},
	}
	m := runtimeToolNameToCode(RunInput{ToolSet: toolSet}, infos)
	if m["天气"] != "builtin/get_weather" || m["mcp_x"] != "mcp/server/x" {
		t.Fatalf("unexpected mapping: %+v", m)
	}
}

// 防止未使用 import 告警。
var _ = models.Conversation{}
var _ = einojsonschema.Schema{}
