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
	confirmWords := []string{"确认", "是", "好的", "可以", "ok", "yes", "继续", "同意"}
	for _, item := range confirmWords {
		if strings.Contains(value, item) {
			return ConfirmationDecisionConfirm
		}
	}
	cancelWords := []string{"取消", "不用", "不需要", "算了", "no"}
	for _, item := range cancelWords {
		if strings.Contains(value, item) {
			return ConfirmationDecisionCancel
		}
	}
	return ""
}

func IsCancellationReply(replyText string) bool {
	replyText = strings.TrimSpace(replyText)
	return strings.Contains(replyText, CancelCreateTicketReply) || strings.Contains(replyText, CancelHandoffReply)
}
