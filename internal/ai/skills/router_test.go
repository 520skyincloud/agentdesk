package skills

import (
	"strings"
	"testing"

	"agent-desk/internal/models"
)

func TestParseSkillExamples(t *testing.T) {
	examples := parseSkillExamples(`[" 退款进度 ","","发票补开","修改收货地址","多余示例"]`)
	if len(examples) != 3 {
		t.Fatalf("expected 3 examples, got %d", len(examples))
	}
	if examples[0] != "退款进度" || examples[1] != "发票补开" || examples[2] != "修改收货地址" {
		t.Fatalf("unexpected examples: %#v", examples)
	}
}

func TestNormalizeRouteDecision(t *testing.T) {
	if got := normalizeRouteDecision("```refund_skill```\n补充说明"); got != "refund_skill" {
		t.Fatalf("unexpected normalized decision: %q", got)
	}
	if got := normalizeRouteDecision(" none "); got != "NONE" {
		t.Fatalf("expected NONE, got %q", got)
	}
}

func TestBuildSkillRoutePrompt(t *testing.T) {
	prompt := buildSkillRoutePrompt("我要申请退款", []models.SkillDefinition{
		{
			Code:        "refund_skill",
			Name:        "退款处理",
			Description: "负责退款和退货相关问题",
			Examples:    `["退款进度","退货运费"]`,
		},
	})

	if !strings.Contains(prompt, "skillCode=refund_skill") {
		t.Fatalf("expected prompt to include skill code, got %q", prompt)
	}
	if !strings.Contains(prompt, "examples=退款进度 | 退货运费") {
		t.Fatalf("expected prompt to include examples, got %q", prompt)
	}
	if !strings.Contains(prompt, "请只输出一个 skillCode 或 NONE。") {
		t.Fatalf("expected prompt to include output constraint, got %q", prompt)
	}
}
