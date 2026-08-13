package executor

import (
	"testing"

	"agent-desk/internal/ai/runtime/contracts"
)

func TestFutureCommitPhraseDetectsPromises(t *testing.T) {
	cases := map[string]bool{
		"我帮你查查有没有优惠":    true,
		"我去查一下，等下给你信儿":  true,
		"我帮你记下了":        true,
		"我这边先确认下再回复你":   true,
		"有停车场，地下车库有充电桩": false,
		"退房后在小程序里申请发票":  false,
	}
	for content, want := range cases {
		if got := futureCommitPhrase(content); got != want {
			t.Fatalf("futureCommitPhrase(%q) = %v, want %v", content, got, want)
		}
	}
}

func TestLooksLikeNonHotelLocation(t *testing.T) {
	cases := map[string]bool{
		"菜市场定位呢": true,
		"银行定位发我": true,
		"菜市场的定位": true,
		"酒店定位发我": false,
		"你们店在哪":  false,
		"停车场怎么走": false,
		"定位":     false,
	}
	for text, want := range cases {
		if got := looksLikeNonHotelLocation(text); got != want {
			t.Fatalf("looksLikeNonHotelLocation(%q) = %v, want %v", text, got, want)
		}
	}
}

func TestFutureCommitClaimIsRepairableNotRejected(t *testing.T) {
	// 直接测未来承诺校验本身：命中 → 产出 issue（可修复），且中性表达不命中。
	input := ReplyValidationInput{
		Plan: contracts.ReplyPlanV2{
			Tasks: []contracts.ReplyPlanTaskV2{{TaskKey: "t1", OutputMode: "text"}},
		},
		Output: contracts.ReplyOutputV2{
			Parts: []contracts.ReplyPartV2{{TaskKeys: []string{"t1"}, Content: "我帮你查查优惠"}},
		},
	}
	if issues := validateReplyFutureCommitClaims(input); len(issues) == 0 {
		t.Fatalf("expected future_commit_claim issue")
	}
}

func TestNeutralPhraseNotBlocked(t *testing.T) {
	input := ReplyValidationInput{
		Plan: contracts.ReplyPlanV2{
			Tasks: []contracts.ReplyPlanTaskV2{{TaskKey: "t1", OutputMode: "text"}},
		},
		Output: contracts.ReplyOutputV2{
			Parts: []contracts.ReplyPartV2{{TaskKeys: []string{"t1"}, Content: "我看看有没有别的选择，具体以门店实际为准。"}},
		},
	}
	if issues := validateReplyFutureCommitClaims(input); len(issues) != 0 {
		t.Fatalf("neutral phrase should pass, got issues: %+v", issues)
	}
}
