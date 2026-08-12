package enums

type ConversationTakeoverRequestStatus string

const (
	ConversationTakeoverRequestStatusPending   ConversationTakeoverRequestStatus = "pending"
	ConversationTakeoverRequestStatusApproved  ConversationTakeoverRequestStatus = "approved"
	ConversationTakeoverRequestStatusRejected  ConversationTakeoverRequestStatus = "rejected"
	ConversationTakeoverRequestStatusCancelled ConversationTakeoverRequestStatus = "cancelled"
)

func IsTerminalConversationTakeoverRequestStatus(status ConversationTakeoverRequestStatus) bool {
	return status == ConversationTakeoverRequestStatusApproved ||
		status == ConversationTakeoverRequestStatusRejected ||
		status == ConversationTakeoverRequestStatusCancelled
}
