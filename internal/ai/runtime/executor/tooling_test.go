package executor

import (
	"context"
	"testing"

	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
	"agent-desk/internal/ai/runtime/registry"
	runtimetooling "agent-desk/internal/ai/runtime/tooling"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/toolx"

	einotool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

type generateToolingStub struct {
	name string
}

func (t generateToolingStub) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: t.name}, nil
}

func TestPrepareGenerateToolingForIntentDisablesAllToolsWithoutExplicitToolTask(t *testing.T) {
	tooling := prepareGenerateToolingForIntent(
		[]runtimetooling.MCPToolDefinition{{ToolCode: "mcp/maps/route", ModelName: "mcp_maps_route"}},
		generateToolingStaticSet(),
		callbacks.IntentTraceData{NeedsTool: false, ToolCodes: []string{toolx.BuiltinWeather.Code}},
		true,
	)

	assertEmptyGenerateTooling(t, tooling)
}

func TestPrepareGenerateToolingForIntentPreservesLegacyToolSelectionWithoutRequestedCode(t *testing.T) {
	tooling := prepareGenerateToolingForIntent(
		[]runtimetooling.MCPToolDefinition{{ToolCode: "mcp/maps/route", ModelName: "mcp_maps_route"}},
		generateToolingStaticSet(),
		callbacks.IntentTraceData{NeedsTool: true},
		true,
	)

	assertToolingCodeSet(t, tooling.toolCodes,
		"mcp/maps/route",
		toolx.BuiltinToolSearch.Code,
		toolx.BuiltinSkill.Code,
		toolx.BuiltinWeather.Code,
		toolx.GraphHandoffConversation.Code,
	)
}

func TestPrepareGenerateToolingForIntentKeepsOnlyRequestedWeatherTool(t *testing.T) {
	tooling := prepareGenerateToolingForIntent(
		nil,
		generateToolingStaticSet(),
		callbacks.IntentTraceData{NeedsTool: true, ToolCodes: []string{toolx.BuiltinWeather.Code}},
		true,
	)

	if len(tooling.definitions) != 0 {
		t.Fatalf("weather intent must not expose dynamic definitions: %#v", tooling.definitions)
	}
	if len(tooling.staticTools) != 1 || staticToolModelName(tooling.staticTools[0]) != toolx.BuiltinWeather.Name {
		t.Fatalf("expected only weather static tool, got %#v", tooling.staticTools)
	}
	assertToolingCodeSet(t, tooling.toolCodes, toolx.BuiltinWeather.Code)
	if got := tooling.staticToolCodeMap[toolx.BuiltinWeather.Name]; got != toolx.BuiltinWeather.Code {
		t.Fatalf("unexpected weather model-name mapping: %q", got)
	}
	if _, ok := tooling.staticToolCodeMap[toolx.GraphHandoffConversation.Name]; ok {
		t.Fatalf("unrelated handoff tool must be excluded: %#v", tooling.staticToolCodeMap)
	}
	if _, ok := tooling.staticToolMetadata[toolx.BuiltinWeather.Name]; !ok {
		t.Fatalf("weather metadata must remain aligned: %#v", tooling.staticToolMetadata)
	}
	if len(tooling.toolDefsByModelName) != 1 || tooling.toolDefsByModelName[toolx.BuiltinWeather.Name] != toolx.BuiltinWeather.Code {
		t.Fatalf("unexpected model-name tool map: %#v", tooling.toolDefsByModelName)
	}
}

func TestPrepareGenerateToolingForIntentExcludesWeatherForOtherStaticTool(t *testing.T) {
	tooling := prepareGenerateToolingForIntent(
		nil,
		generateToolingStaticSet(),
		callbacks.IntentTraceData{NeedsTool: true, ToolCodes: []string{toolx.GraphHandoffConversation.Code}},
		true,
	)

	if len(tooling.staticTools) != 1 || staticToolModelName(tooling.staticTools[0]) != toolx.GraphHandoffConversation.Name {
		t.Fatalf("expected only handoff static tool, got %#v", tooling.staticTools)
	}
	assertToolingCodeSet(t, tooling.toolCodes, toolx.GraphHandoffConversation.Code)
	if _, ok := tooling.staticToolCodeMap[toolx.BuiltinWeather.Name]; ok {
		t.Fatalf("weather must not be exposed for a non-weather tool task: %#v", tooling.staticToolCodeMap)
	}
}

func TestPrepareGenerateToolingForIntentKeepsRequestedDynamicToolAndToolSearch(t *testing.T) {
	definitions := []runtimetooling.MCPToolDefinition{
		{ToolCode: "mcp/maps/route", ServerCode: "maps", ToolName: "route", ModelName: "mcp_maps_route"},
		{ToolCode: "mcp/order/query", ServerCode: "order", ToolName: "query", ModelName: "mcp_order_query"},
	}
	tooling := prepareGenerateToolingForIntent(
		definitions,
		generateToolingStaticSet(),
		callbacks.IntentTraceData{NeedsTool: true, ToolCodes: []string{"mcp/maps/route"}},
		true,
	)

	if len(tooling.definitions) != 1 || tooling.definitions[0].ToolCode != "mcp/maps/route" {
		t.Fatalf("expected only requested dynamic definition, got %#v", tooling.definitions)
	}
	if len(tooling.staticTools) != 0 || len(tooling.staticToolCodeMap) != 0 || len(tooling.staticToolMetadata) != 0 {
		t.Fatalf("dynamic tool request must exclude unrelated static tools: %#v", tooling)
	}
	assertToolingCodeSet(t, tooling.toolCodes, "mcp/maps/route", toolx.BuiltinToolSearch.Code)
	if got := tooling.toolDefsByModelName["mcp_maps_route"]; got != "mcp/maps/route" {
		t.Fatalf("requested dynamic model mapping was not preserved: %q", got)
	}
	if got := tooling.toolDefsByModelName[toolx.BuiltinToolSearch.Name]; got != toolx.BuiltinToolSearch.Code {
		t.Fatalf("tool_search mapping missing: %q", got)
	}
	if _, ok := tooling.toolDefsByModelName["mcp_order_query"]; ok {
		t.Fatalf("unrequested dynamic model mapping leaked: %#v", tooling.toolDefsByModelName)
	}
}

func generateToolingStaticSet() *registry.ToolSet {
	return &registry.ToolSet{
		StaticTools: []einotool.BaseTool{
			generateToolingStub{name: toolx.BuiltinWeather.Name},
			generateToolingStub{name: toolx.GraphHandoffConversation.Name},
		},
		StaticToolCodes: map[string]string{
			toolx.BuiltinWeather.Name:           toolx.BuiltinWeather.Code,
			toolx.GraphHandoffConversation.Name: toolx.GraphHandoffConversation.Code,
		},
		StaticToolMetadata: map[string]registry.ToolMetadata{
			toolx.BuiltinWeather.Name: {
				ToolCode:   toolx.BuiltinWeather.Code,
				ServerCode: toolx.BuiltinWeather.ServerCode,
				ToolName:   toolx.BuiltinWeather.Name,
				SourceType: enums.ToolSourceTypeBuiltin,
			},
			toolx.GraphHandoffConversation.Name: {
				ToolCode:   toolx.GraphHandoffConversation.Code,
				ServerCode: toolx.GraphHandoffConversation.ServerCode,
				ToolName:   toolx.GraphHandoffConversation.Name,
				SourceType: enums.ToolSourceTypeGraph,
			},
		},
	}
}

func assertEmptyGenerateTooling(t *testing.T, tooling preparedTooling) {
	t.Helper()
	if len(tooling.definitions) != 0 || len(tooling.toolCodes) != 0 || len(tooling.toolDefsByModelName) != 0 || len(tooling.staticToolCodes) != 0 || len(tooling.staticTools) != 0 || len(tooling.staticToolCodeMap) != 0 || len(tooling.staticToolMetadata) != 0 {
		t.Fatalf("expected empty Generate tooling, got %#v", tooling)
	}
}

func assertToolingCodeSet(t *testing.T, got []string, want ...string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("unexpected tool codes: got %#v want %#v", got, want)
	}
	for _, code := range want {
		found := false
		for _, item := range got {
			if item == code {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("missing tool code %q in %#v", code, got)
		}
	}
}
