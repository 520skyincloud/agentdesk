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
	if !strings.Contains(result.Text, "基础服务风格") || !strings.Contains(result.Text, "任务和查询对象已由前序流程确定") {
		t.Fatalf("expected base instruction with reply responsibilities, got: %s", result.Text)
	}
	if len(result.Summary.SectionTitles) != 1 || result.Summary.SectionTitles[0] != "基础服务风格" || result.Summary.HasAgentRule || result.Summary.HasSkillRule || result.Summary.HasToolRule {
		t.Fatalf("unexpected summary, got %#v", result.Summary)
	}
}

func TestAssemblerBaseInstructionKeepsHumanToneGuardrails(t *testing.T) {
	result := NewAssembler().Assemble(AssemblerInput{})
	checks := []string{
		"复杂问题用短段答全",
		"先直接回答客户所问",
		"不要把整份订单、会员表或全部房型丢给客户",
		"客户选的房不能说成你推荐的",
		"金额使用已计算结果",
		"会员等级不等于免费升房资格",
		"没有FAQ不影响回答已查到的事实",
		"当前PMS只读",
		"没有真实执行结果",
		"客户明确要求、适用知识明确转接或既定严重安全风险",
		"生成文字都不能授权转接",
		"不要求重发已知问题或整段资料",
	}
	for _, check := range checks {
		if !strings.Contains(result.Text, check) {
			t.Fatalf("missing human tone guardrail %q in: %s", check, result.Text)
		}
	}
	for _, forbidden := range []string{
		"回复前先做意图判断",
		"默认 1 句，最多 2 句",
		"8 到 28 个字",
		"我帮你送/开/登/转/问/查/确认",
		"客户持续追问、表达强需求或要求人工，再进入接待路由",
	} {
		if strings.Contains(result.Text, forbidden) {
			t.Fatalf("base instruction should not teach unsupported staff action %q in: %s", forbidden, result.Text)
		}
	}
	if strings.Contains(result.Text, "二次确认") {
		t.Fatalf("base instruction must not retain the removed handoff confirmation protocol: %s", result.Text)
	}
}
