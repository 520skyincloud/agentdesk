package callbacks

import (
	"testing"

	"agent-desk/internal/pkg/toolx"
)

func TestParseGraphToolOutcome(t *testing.T) {
	action, risk, ready := parseGraphToolOutcome(toolx.GraphAnalyzeConversation.Code, `{"recommendedNextAction":"handoff_to_human","riskLevel":"high"}`)
	if action != "handoff_to_human" || risk != "high" || ready {
		t.Fatalf("unexpected analyze graph outcome: %q %q %v", action, risk, ready)
	}

	action, risk, ready = parseGraphToolOutcome(toolx.GraphTriageServiceRequest.Code, `{"recommendedAction":"prepare_ticket","analysis":{"riskLevel":"medium"},"ticketDraft":{"ready":true}}`)
	if action != "prepare_ticket" || risk != "medium" || !ready {
		t.Fatalf("unexpected triage graph outcome: %q %q %v", action, risk, ready)
	}
}

func TestExtractCandidateToolCodes(t *testing.T) {
	handler := &RuntimeTraceHandler{
		toolMetadataBy: map[string]ToolMetadata{
			"tool_search": {ToolCode: toolx.BuiltinToolSearch.Code, ToolName: toolx.BuiltinToolSearch.Name},
			"foo_model":   {ToolCode: "mcp/server/foo", ToolName: "foo"},
		},
	}

	got := handler.extractCandidateToolCodes(`{"selectedTools":["foo_model"]}`)
	if len(got) != 1 || got[0] != "mcp/server/foo" {
		t.Fatalf("unexpected selectedTools codes: %#v", got)
	}

	got = handler.extractCandidateToolCodes(`{"candidates":[{"toolCode":"mcp/server/bar"}]}`)
	if len(got) != 1 || got[0] != "mcp/server/bar" {
		t.Fatalf("unexpected candidate codes: %#v", got)
	}
}

func TestTryActivateSkill(t *testing.T) {
	collector := NewRuntimeTraceCollector()
	handler := &RuntimeTraceHandler{
		collector: collector,
		skillMetadataBy: map[string]SkillMetadata{
			"after_sales_escalation_skill": {
				Code:             "after_sales_escalation_skill",
				Name:             "售后升级",
				AllowedToolCodes: []string{"graph/handoff_to_human"},
			},
		},
	}

	handler.tryActivateSkill(`{"skill":"after_sales_escalation_skill"}`)

	if collector.Data.Skill.Code != "after_sales_escalation_skill" {
		t.Fatalf("unexpected skill code: %#v", collector.Data.Skill)
	}
	if collector.Data.Skill.Name != "售后升级" {
		t.Fatalf("unexpected skill name: %#v", collector.Data.Skill)
	}
	if collector.Data.Skill.RouteReason != "eino_skill_tool" {
		t.Fatalf("unexpected route reason: %#v", collector.Data.Skill)
	}
	if len(collector.Data.Skill.AllowedToolCodes) != 1 || collector.Data.Skill.AllowedToolCodes[0] != "graph/handoff_to_human" {
		t.Fatalf("unexpected allowed tools: %#v", collector.Data.Skill.AllowedToolCodes)
	}
}
