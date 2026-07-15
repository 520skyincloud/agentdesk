package runtime

import (
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
	// HandoffAt is historical audit data. The active route state and assignee own
	// whether AI is paused; treating this timestamp as active state permanently
	// disabled AI after the first completed manual handoff.
	if conversation.CurrentAssigneeID > 0 {
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
