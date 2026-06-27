package factory

import (
	"testing"

	runtimeinstruction "agent-desk/internal/ai/runtime/instruction"
)

func TestBuildInstructionTraceSummary(t *testing.T) {
	got := buildInstructionTraceSummary(runtimeinstruction.AssemblySummary{
		SectionTitles: []string{"Agent 规则", "当前技能上下文"},
		HasAgentRule:  true,
		HasSkillRule:  true,
		HasToolRule:   false,
	})

	if len(got.SectionTitles) != 2 {
		t.Fatalf("unexpected section titles: %#v", got.SectionTitles)
	}
	if !got.HasAgentRule || !got.HasSkillRule {
		t.Fatalf("unexpected summary flags: %#v", got)
	}
	if got.HasToolRule {
		t.Fatalf("expected HasToolRule false, got %#v", got)
	}
}
