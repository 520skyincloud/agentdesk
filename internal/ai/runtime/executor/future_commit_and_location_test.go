package executor

import (
	"testing"

	"agent-desk/internal/ai/runtime/contracts"
)

func TestPromiseAllowlistRejectsOutsideActions(t *testing.T) {
	cases := []string{
		"好，那改成1203。",          // 完成态编造：假换房
		"正在查你之前的预订信息，稍等。",     // 假执行：查订单能力不存在
		"我可以帮你查一下现在有没有合适的。",   // 幻想订单/房型查询
		"我再帮你看看有没有更大房型能换，稍等。", // 假执行 + 换房型
		"需要加床或用品随时说。",         // 主动 offer 不存在的服务
		"我帮你联系前台处理。",          // 联系前台不在白名单
		"我帮你查查有没有优惠。",         // 查优惠不在白名单
		"我帮你送两瓶水上去。",          // 送物不在白名单
	}
	for _, content := range cases {
		if issues := validateReplyFutureCommitClaims(promiseInput(content)); len(issues) == 0 {
			t.Fatalf("expected promise_outside_allowlist for %q, got none", content)
		}
	}
}

func TestPromiseAllowlistAllowsWhitelistActions(t *testing.T) {
	cases := []string{
		"我帮你转人工，稍等确认下。", // 转人工在白名单
		"定位发你，地址在酒店楼下。", // 无承诺语态，纯发信息
		"我发你酒店电话。",      // 发电话在白名单
		"我发下入住小程序。",     // 发小程序在白名单
	}
	for _, content := range cases {
		if issues := validateReplyFutureCommitClaims(promiseInput(content)); len(issues) != 0 {
			t.Fatalf("unexpected issues for allowlisted %q: %+v", content, issues)
		}
	}
}

func TestPromiseAllowlistAllowsInfoAndGuidance(t *testing.T) {
	cases := []string{
		"有停车场，地下车库有充电桩。",        // 纯信息
		"退房后在小程序里申请发票。",         // 引导自助
		"这个功能可以帮你自助办理入住。",       // 引导自助（含"帮你"但无动作动词，不应误判）
		"我看看有没有别的选择，具体以门店实际为准。", // 中性短语，不含承诺语态
	}
	for _, content := range cases {
		if issues := validateReplyFutureCommitClaims(promiseInput(content)); len(issues) != 0 {
			t.Fatalf("unexpected issues for info/guidance %q: %+v", content, issues)
		}
	}
}

func TestPromiseAllowlistRejectsUncommittedCompletion(t *testing.T) {
	// "已经/已/好了/改成" 等完成态，即便没有 ActionRef，也应被白名单校验拦下（不在白名单表面词）。
	input := promiseInput("好，那改成1203。")
	input.Output.Parts[0].ActionRefs = nil
	if issues := validateReplyFutureCommitClaims(input); len(issues) == 0 {
		t.Fatalf("expected completion claim without evidence to be rejected")
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

func promiseInput(content string) ReplyValidationInput {
	return ReplyValidationInput{
		Plan: contracts.ReplyPlanV2{
			Tasks: []contracts.ReplyPlanTaskV2{{TaskKey: "t1", OutputMode: "text"}},
		},
		Output: contracts.ReplyOutputV2{
			Parts: []contracts.ReplyPartV2{{TaskKeys: []string{"t1"}, Content: content}},
		},
	}
}
