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
	if !strings.Contains(result.Text, "基础服务风格") || !strings.Contains(result.Text, "service_request") {
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
		"互动不是敷衍回复",
		"短、自然、有回应感",
		"不能用“有啥事你直接说”",
		"不要每次都回“哈哈”",
		"不要只回“哈哈”“好的”“嗯嗯”",
		"不要说自己“说话比较直”",
		"没有工具、资源提交或接待路由结果支撑",
		"门店不默认代送",
		"不能表达已经登记、提交、通知、安排或开始处理",
		"现场查看",
		"不是正在现场执行任务的员工",
		"没有工具结果支持",
		"不能出现“我帮你送/开/登/转/问/查/确认/过去/安排/提交/记录”",
		"没有工具/上下文明确给出已发送结果",
		"现在只能文字回你，打字发我就行",
		"hotel_info",
		"hotel_variable",
		"human_complaint_risk",
	}
	for _, check := range checks {
		if !strings.Contains(result.Text, check) {
			t.Fatalf("missing human tone guardrail %q in: %s", check, result.Text)
		}
	}
	for _, forbidden := range []string{"这个需要同事处理", "同事会去房间查看", "说明需要同事接手"} {
		if strings.Contains(result.Text, forbidden) {
			t.Fatalf("base instruction should not teach unsupported staff action %q in: %s", forbidden, result.Text)
		}
	}
}
