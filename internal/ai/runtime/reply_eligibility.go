package runtime

import (
	"strings"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"

	"github.com/mlogclub/simple/common/strs"
)

type replyEligibility struct{}

func newReplyEligibility() *replyEligibility {
	return &replyEligibility{}
}

func (e *replyEligibility) CanReply(conversation models.Conversation, message models.Message, aiAgent models.AIAgent) bool {
	if message.SenderType != enums.IMSenderTypeCustomer {
		return false
	}
	// HandoffAt is historical audit data. Active assignment and route state own
	// whether AI is paused; the route is checked immediately before execution.
	if conversation.CurrentAssigneeID > 0 && !strings.HasPrefix(strings.TrimSpace(message.RequestID), "manual_resume_") {
		return false
	}
	if aiAgent.ServiceMode == enums.IMConversationServiceModeHumanOnly {
		return false
	}
	if strs.IsBlank(message.Content) {
		return false
	}
	return true
}
