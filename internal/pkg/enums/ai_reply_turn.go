package enums

type AIReplyTurnStatus string

const (
	AIReplyTurnStatusOpen        AIReplyTurnStatus = "open"
	AIReplyTurnStatusRunning     AIReplyTurnStatus = "running"
	AIReplyTurnStatusCommitted   AIReplyTurnStatus = "committed"
	AIReplyTurnStatusDelivered   AIReplyTurnStatus = "delivered"
	AIReplyTurnStatusInterrupted AIReplyTurnStatus = "interrupted"
	AIReplyTurnStatusClosed      AIReplyTurnStatus = "closed"
	AIReplyTurnStatusFailed      AIReplyTurnStatus = "failed"
)
