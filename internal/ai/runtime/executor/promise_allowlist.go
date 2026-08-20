package executor

import (
	"strings"

	"agent-desk/internal/ai/runtime/contracts"
)

// promiseAllowlist identifies the action surface of a promise. Surface words
// never grant permission: only a matching committed/delivered ActionLedger item
// can authorize a completion claim.
//
// 之前用 futureCommitPhrases 黑名单枚举“不能承诺的动作”（查订单、改房号、加床……），
// 但“不能干的事”是无限的，模型换一个措辞就会漏。改成白名单：只允许承诺动作目录里
// 真实可执行的少数几类动作（发定位 / 发小程序 / 发电话 / 转人工 / 建工单 / 查天气）；
// 句子只要出现“承诺去做 / 正在做 / 已经做了”的语态，却没落到白名单动作上，一律判为
// 越权承诺并拒绝（rejected），由人工兜底，而不是让模型换说法蒙混。
//
// 白名单动作与 internal/ai/runtime/actions 目录保持一致；query_room_status /
// query_member_level 属 external 且未接入，不在白名单内，因此“查订单 / 查房型 / 换房 /
// 加床”等一律不能被口头承诺。

// promiseActionSurface 是白名单动作在自然语言里的表面词。命中任意一个，说明这句话
// 承诺的对象是“允许的动作”。词表刻意收敛，不含“位置/在哪/怎么走”这类问句形式。
func promiseActionSurface() []string {
	return []string{
		"转人工", "转接", "人工客服", "找人工", "转给人工",
		"工单",
		"定位", "地址", "导航",
		"小程序",
		"电话", "号码", "联系电话",
	}
}

// promiseCommitmentSignals 是“承诺去做”的语态词，属于封闭的语法引导词，不是业务黑名单：
// 第一人称 + 执行型动词（我帮你查 / 帮你查 / 我查 / 我改 …），或悬置/进行/完成时态，或 offer 语气。
//
// 刻意不含“我看看 / 我确认 / 我这边”这类只表示“在思考、在核对”的中性短语，也不含裸“帮你”
// （避免把“这个功能可以帮你自助办理”这类引导自助的合法回复误判为越权承诺）。
func promiseCommitmentSignals() []string {
	return []string{
		// 第一人称执行承诺：我/帮你 + 动作动词
		"我帮你查", "帮你查", "我查", "我帮你问", "帮你问", "我问",
		"我帮你联系", "帮你联系", "我联系", "我帮你安排", "帮你安排", "我安排",
		"我帮你登记", "帮你登记", "我登记", "我帮你通知", "帮你通知", "我通知",
		"我帮你处理", "帮你处理", "我处理",
		"我帮你改", "帮你改", "我改", "我帮你换", "帮你换", "我换", "我帮你调", "帮你调", "我调",
		"我帮你送", "帮你送", "我送", "我帮你修", "帮你修", "我修", "我帮你加", "帮你加", "我加",
		"我帮你订", "帮你订", "我订", "我帮你办", "帮你办", "我办",
		"我帮你退", "帮你退", "我退", "我帮你取消", "帮你取消", "我取消",
		"我帮你拿", "帮你拿", "我帮你取", "帮你取", "我帮你搬", "帮你搬",
		"我转", "我发", "我找", "帮你看看",
		"会联系", "会安排", "会处理", "会转", "会提交", "会登记", "会通知", "会发送", "会发给",
		"将联系", "将安排", "将处理", "将转", "将提交", "将登记", "将通知", "将发送",
		// 悬置 / 进行 / 完成时态（同步链路里“正在/稍等”永远是假执行）
		"正在", "稍等", "稍后", "稍候", "马上", "立刻", "这就",
		// offer 语气（主动承诺"有需要就给你办"）
		"随时说", "需要就说", "要就说", "有需要随时",
		"改成", "换成", "调到", "已经", "已", "好了", "办好", "安排好了", "登记了", "通知了",
	}
}

// hasPromiseSignal 判断句子是否出现“承诺去做 / 正在做 / 已经做了”的语态。
// 纯信息问答（停车场免费、电话是 xxx、你可以在小程序里申请发票）不含这些语态，直接放行。
func hasPromiseSignal(compact string) bool {
	return containsAny(compact, promiseCommitmentSignals())
}

// hasUnnegatedPromiseSignal keeps capability-boundary replies such as
// "不能帮你换房" from being mistaken for promises. Every matching signal is
// checked independently so a later positive promise in the same sentence still
// fails validation.
func hasUnnegatedPromiseSignal(compact string) bool {
	if !hasPromiseSignal(compact) {
		return false
	}
	for _, signal := range promiseCommitmentSignals() {
		from := 0
		for from <= len(compact) {
			index := strings.Index(compact[from:], signal)
			if index < 0 {
				break
			}
			index += from
			if !promiseSignalIsExplicitlyNegated(compact[:index]) {
				return true
			}
			from = index + len(signal)
		}
	}
	return false
}

func promiseSignalIsExplicitlyNegated(prefix string) bool {
	prefix = strings.TrimSpace(prefix)
	for {
		trimmed := false
		for _, modifier := range []string{"直接"} {
			if strings.HasSuffix(prefix, modifier) {
				prefix = strings.TrimSuffix(prefix, modifier)
				trimmed = true
			}
		}
		if !trimmed {
			break
		}
	}
	for _, negation := range []string{"不能", "无法", "没法", "不会", "不可以"} {
		if strings.HasSuffix(prefix, negation) {
			return true
		}
	}
	return false
}

func splitPromiseClauses(content string) []string {
	clauses := strings.FieldsFunc(content, func(r rune) bool {
		switch r {
		case '。', '！', '!', '？', '?', '；', ';', '\n', '\r':
			return true
		default:
			return false
		}
	})
	if len(clauses) == 0 && strings.TrimSpace(content) != "" {
		return []string{content}
	}
	return clauses
}

// hasPromiseAllowlistedSurface only classifies the mentioned action. It must not
// be used as an authorization decision.
func hasPromiseAllowlistedSurface(compact string) bool {
	return containsAny(compact, promiseActionSurface())
}

// validateReplyPromiseAllowlist 是白名单承诺校验的入口，返回越权承诺的 issue 列表。
func validateReplyPromiseAllowlist(input ReplyValidationInput) []contracts.ValidationIssueV1 {
	issues := make([]contracts.ValidationIssueV1, 0)
	for _, part := range input.Output.Parts {
		content := strings.TrimSpace(part.Content)
		if content == "" {
			continue
		}
		for _, clause := range splitPromiseClauses(content) {
			compact := compactReplyText(clause)
			if !hasUnnegatedPromiseSignal(compact) {
				continue
			}
			if promiseClauseBackedByCommittedAction(input, part, compact) {
				continue
			}
			issues = append(issues, validationIssue(
				"promise_outside_allowlist",
				"$.parts",
				"reply promises or claims an action outside the allowlist without committed evidence",
			))
			break
		}
	}
	return issues
}

func promiseClauseBackedByCommittedAction(input ReplyValidationInput, part contracts.ReplyPartV2, compact string) bool {
	if !hasPromiseAllowlistedSurface(compact) || len(part.ActionRefs) == 0 {
		return false
	}
	actions := make(map[string]contracts.ActionLedgerItemV1, len(input.ActionLedger.Actions))
	for _, action := range input.ActionLedger.Actions {
		actions[action.ActionKey] = action
	}
	for _, ref := range part.ActionRefs {
		action, ok := actions[ref]
		if !ok || action.Status != "committed" && action.Status != "delivered" {
			continue
		}
		if promiseSurfaceMatchesAction(compact, action.ActionType) {
			return true
		}
	}
	return false
}

func promiseSurfaceMatchesAction(compact, actionType string) bool {
	switch strings.TrimSpace(actionType) {
	case "send_location":
		return containsAny(compact, []string{"定位", "地址", "导航"})
	case "send_mini_program":
		return strings.Contains(compact, "小程序")
	case "send_phone":
		return containsAny(compact, []string{"电话", "号码", "联系电话"})
	case "human_handoff":
		return containsAny(compact, []string{"转人工", "转接", "人工客服", "找人工", "转给人工"})
	default:
		return false
	}
}
