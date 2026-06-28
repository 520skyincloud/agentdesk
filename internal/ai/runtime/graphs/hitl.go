package graphs

import "strings"

const (
	InterruptTypeTicketCreationConfirmation = "ticket_creation_confirmation"
	InterruptTypeHandoffConfirmation        = "handoff_confirmation"
	ConfirmOrCancelPrompt                   = "请回复“确认”或“取消”。"
	NeedExplicitConfirmationPrompt          = "我需要你的明确确认，请直接回复“确认”或“取消”。"
	ConfirmationExpiredReply                = "本次确认已失效，请重新发起。"
	CancelCreateTicketReply                 = "已取消本次工单创建。"
	CancelHandoffReply                      = "已取消本次转人工。"
)

type ConfirmationDecision string

const (
	ConfirmationDecisionConfirm ConfirmationDecision = "confirm"
	ConfirmationDecisionCancel  ConfirmationDecision = "cancel"
)

func ParseConfirmationDecision(value string) ConfirmationDecision {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return ""
	}
	negativeWords := []string{"不确认", "先不", "别建", "不要创建", "不要下单", "不要派", "别派", "不用创建", "不需要创建"}
	for _, item := range negativeWords {
		if strings.Contains(value, item) {
			return ConfirmationDecisionCancel
		}
	}
	cancelWords := []string{"取消", "不用", "不用了", "不需要", "不要", "不要了", "别", "算了", "no"}
	for _, item := range cancelWords {
		if strings.Contains(value, item) {
			return ConfirmationDecisionCancel
		}
	}
	confirmWords := []string{"确认", "确定", "对", "对的", "是", "好的", "好", "可以", "可以的", "行", "没问题", "嗯", "嗯嗯", "ok", "okay", "yes", "继续", "同意"}
	for _, item := range confirmWords {
		if strings.Contains(value, item) {
			return ConfirmationDecisionConfirm
		}
	}
	return ""
}

func IsCancellationReply(replyText string) bool {
	replyText = strings.TrimSpace(replyText)
	return strings.Contains(replyText, CancelCreateTicketReply) || strings.Contains(replyText, CancelHandoffReply)
}
