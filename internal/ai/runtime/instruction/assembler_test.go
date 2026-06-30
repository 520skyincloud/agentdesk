package instruction

import (
	"strings"
	"testing"
)

func TestAssemblerRespectsProvidedSources(t *testing.T) {
	result := NewAssembler().Assemble(AssemblerInput{
		AgentInstruction: "agent-rule",
		SkillInstruction: "skill-rule",
		ToolAppendices:   []string{"tool-rule-1", "tool-rule-2"},
	})
	if !strings.Contains(result.Text, "Agent 规则：\nagent-rule") {
		t.Fatalf("missing agent instruction: %s", result.Text)
	}
	if !strings.Contains(result.Text, "当前技能上下文：\nskill-rule") {
		t.Fatalf("missing skill instruction: %s", result.Text)
	}
	if !strings.Contains(result.Text, "工具补充规则：\ntool-rule-1") {
		t.Fatalf("missing tool appendix: %s", result.Text)
	}
	if !result.Summary.HasAgentRule || !result.Summary.HasSkillRule || !result.Summary.HasToolRule {
		t.Fatalf("unexpected summary: %#v", result.Summary)
	}
}

func TestAssemblerInjectsBaseInstructionWhenInputIsEmpty(t *testing.T) {
	result := NewAssembler().Assemble(AssemblerInput{})
	if !strings.Contains(result.Text, "基础服务风格") || !strings.Contains(result.Text, "SERVICE_TASK") {
		t.Fatalf("expected base instruction with intent rules, got: %s", result.Text)
	}
	if len(result.Summary.SectionTitles) != 1 || result.Summary.SectionTitles[0] != "基础服务风格" || result.Summary.HasAgentRule || result.Summary.HasSkillRule || result.Summary.HasToolRule {
		t.Fatalf("unexpected summary, got %#v", result.Summary)
	}
}

func TestAssemblerBaseInstructionKeepsHumanToneGuardrails(t *testing.T) {
	result := NewAssembler().Assemble(AssemblerInput{})
	checks := []string{
		"默认 1 句，最多 2 句",
		"少用“您”，优先说“你”",
		"不要说“亲”“为您”“这边”",
		"轻互动不是敷衍回复",
		"不要每次都回“哈哈”",
		"不要说“马上安排”",
		"已记录/已提交/已通知",
		"已经让同事过去",
		"现在只能文字回你，打字发我就行",
	}
	for _, check := range checks {
		if !strings.Contains(result.Text, check) {
			t.Fatalf("missing human tone guardrail %q in: %s", check, result.Text)
		}
	}
}
