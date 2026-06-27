package factory

import (
	"testing"

	einocallbacks "agent-desk/internal/ai/runtime/internal/impl/callbacks"
	"agent-desk/internal/pkg/toolx"

	"github.com/cloudwego/eino/schema"
)

func TestFilterDynamicToolInfos(t *testing.T) {
	allTools := []*schema.ToolInfo{
		{Name: toolx.BuiltinToolSearch.Name},
		{Name: "mcp_server_a"},
		{Name: "mcp_server_b"},
	}
	messages := []*schema.Message{
		{Role: schema.Tool, ToolName: toolx.BuiltinToolSearch.Name, Content: `{"selectedTools":["mcp_server_b"]}`},
	}

	filtered := filterDynamicToolInfos(allTools, []string{"mcp_server_a", "mcp_server_b"}, messages)
	if len(filtered) != 2 {
		t.Fatalf("unexpected filtered tool count: %d", len(filtered))
	}
	if filtered[0].Name != toolx.BuiltinToolSearch.Name || filtered[1].Name != "mcp_server_b" {
		t.Fatalf("unexpected filtered tools: %#v", filtered)
	}
}

func TestFilterToolInfosBySkill(t *testing.T) {
	allTools := []*schema.ToolInfo{
		{Name: toolx.BuiltinSkill.Name},
		{Name: toolx.BuiltinToolSearch.Name},
		{Name: toolx.GraphHandoffConversation.Name},
		{Name: "mcp_server_refund"},
	}
	toolMetadataByName := map[string]einocallbacks.ToolMetadata{
		toolx.GraphHandoffConversation.Name: {ToolCode: toolx.GraphHandoffConversation.Code, ToolName: toolx.GraphHandoffConversation.Name},
		"mcp_server_refund":                 {ToolCode: "mcp/refund", ToolName: "mcp_server_refund"},
	}

	filtered := filterToolInfosBySkill(allTools, toolMetadataByName, []string{toolx.GraphHandoffConversation.Code})
	if len(filtered) != 3 {
		t.Fatalf("unexpected filtered tool count: %d", len(filtered))
	}
	if filtered[0].Name != toolx.BuiltinSkill.Name || filtered[1].Name != toolx.BuiltinToolSearch.Name || filtered[2].Name != toolx.GraphHandoffConversation.Name {
		t.Fatalf("unexpected filtered tools: %#v", filtered)
	}
}

func TestFilterToolSearchResult(t *testing.T) {
	toolMetadataByName := map[string]einocallbacks.ToolMetadata{
		"mcp_server_refund": {ToolCode: "mcp/refund", ToolName: "mcp_server_refund"},
		"mcp_server_order":  {ToolCode: "mcp/order", ToolName: "mcp_server_order"},
	}

	got, err := filterToolSearchResult(`{"selectedTools":["mcp_server_refund","mcp_server_order"]}`, []string{"mcp/order"}, toolMetadataByName)
	if err != nil {
		t.Fatalf("filterToolSearchResult returned error: %v", err)
	}
	if got != `{"selectedTools":["mcp_server_order"]}` {
		t.Fatalf("unexpected filtered result: %s", got)
	}
}

func TestExtractToolCodesFromInfos(t *testing.T) {
	infos := []*schema.ToolInfo{
		{Name: toolx.BuiltinSkill.Name},
		{Name: toolx.GraphPrepareTicketDraft.Name},
		{Name: "mcp_server_refund"},
	}
	toolMetadataByName := map[string]einocallbacks.ToolMetadata{
		toolx.GraphPrepareTicketDraft.Name: {ToolCode: toolx.GraphPrepareTicketDraft.Code, ToolName: toolx.GraphPrepareTicketDraft.Name},
		"mcp_server_refund":                {ToolCode: "mcp/refund", ToolName: "mcp_server_refund"},
	}
	got := extractToolCodesFromInfos(infos, toolMetadataByName)
	if len(got) != 3 {
		t.Fatalf("unexpected tool codes: %#v", got)
	}
	if got[0] != toolx.BuiltinSkill.Code || got[1] != toolx.GraphPrepareTicketDraft.Code || got[2] != "mcp/refund" {
		t.Fatalf("unexpected tool codes order: %#v", got)
	}
}
