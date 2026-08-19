package runtime

import (
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/services"

	"github.com/mlogclub/simple/common/strs"
	"github.com/mlogclub/simple/sqls"
)

type replyEligibility struct{}

func newReplyEligibility() *replyEligibility {
	return &replyEligibility{}
}

func (e *replyEligibility) CanReply(conversation models.Conversation, message models.Message, aiAgent models.AIAgent) bool {
	if message.SenderType != enums.IMSenderTypeCustomer {
		return false
	}
	if aiAgent.ServiceMode == enums.IMConversationServiceModeHumanOnly {
		return false
	}
	if sqls.DB() == nil {
		if conversation.Status == enums.IMConversationStatusClosed || conversation.CurrentAssigneeID > 0 ||
			conversation.ServiceMode == enums.IMConversationServiceModeHumanOnly {
			return false
		}
	} else {
		decision := services.ConversationRuntimeModeService.Resolve(conversation.ID, conversation.TenantID)
		if !decision.AIReplyAllowed {
			return false
		}
	}
	if strs.IsBlank(message.Content) {
		return false
	}
	return true
}
