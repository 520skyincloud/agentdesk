package services

import (
	"context"
	"fmt"
	"strings"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/utils"

	"github.com/mlogclub/simple/sqls"
)

const standaloneOneReplyText = "本店为无人值守智能化酒店，不设传统前台和房卡。请先在下面的小程序中完成入住登记；登记完成后，到店即可刷脸开门。"

var StandaloneOneReplyService = &standaloneOneReplyService{}

type standaloneOneReplyService struct{}

func isStandaloneOneCustomerMessage(message *models.Message) bool {
	return message != nil &&
		message.SenderType == enums.IMSenderTypeCustomer &&
		utils.IsStandaloneOneTextControl(message.MessageType, message.Content, message.AIReplyTurnID, message.AIReplyTurnVersion)
}

func (s *standaloneOneReplyService) Execute(ctx context.Context, state *aiReplyJobExecutionState) (AIReplyExecutionResult, error) {
	if state == nil || state.Job == nil || state.Conversation == nil || state.Message == nil || state.Instance == nil ||
		!isStandaloneOneCustomerMessage(state.Message) || state.Job.TriggerKind != enums.AIReplyJobTriggerKindStandaloneOne {
		return AIReplyExecutionResult{}, &aiReplyJobTerminalError{code: "standalone_one_scope_invalid"}
	}
	if err := ctx.Err(); err != nil {
		return AIReplyExecutionResult{}, err
	}
	runtimeInstance, err := StoreService.HydrateRuntimeInstanceDB(sqls.DB(), state.Instance)
	if err != nil {
		return AIReplyExecutionResult{}, NewAIReplyExecutionError(AIReplyExecutionErrorResourceInvariantBroken, err)
	}
	miniProgramContent, miniProgramPayload, err := WxWorkProtocolDefaultResourceService.BuildDefaultMiniProgramMessage(runtimeInstance)
	if err != nil {
		return AIReplyExecutionResult{}, NewAIReplyExecutionError(AIReplyExecutionErrorResourceInvariantBroken, err)
	}
	aiAgent := WxWorkProtocolInstanceService.BuildRuntimeAIAgent(runtimeInstance)
	if aiAgent.TenantID != state.Conversation.TenantID {
		return AIReplyExecutionResult{}, NewAIReplyExecutionError(
			AIReplyExecutionErrorResourceInvariantBroken,
			fmt.Errorf("standalone one runtime AI agent unavailable"),
		)
	}
	if err := ctx.Err(); err != nil {
		return AIReplyExecutionResult{}, err
	}
	operatorName := strings.TrimSpace(aiAgent.Name)
	if operatorName == "" {
		operatorName = "AI"
	}
	messages, err := MessageService.SendAIMessageBatchWithRequestID(
		state.Conversation.ID,
		aiAgent.ID,
		[]AIOutboundMessageDraft{
			{
				ClientMsgID: fmt.Sprintf("ai_reply_faq_one_%d_text", state.Message.ID),
				MessageType: enums.IMMessageTypeText,
				Content:     standaloneOneReplyText,
			},
			{
				ClientMsgID: fmt.Sprintf("ai_reply_faq_one_%d_mini_program", state.Message.ID),
				MessageType: enums.IMMessageTypeMiniProgram,
				Content:     miniProgramContent,
				Payload:     miniProgramPayload,
			},
		},
		&dto.AuthPrincipal{
			UserID:         0,
			TenantID:       aiAgent.TenantID,
			ActiveTenantID: aiAgent.TenantID,
			Username:       operatorName,
			Nickname:       operatorName,
		},
		state.Message.RequestID,
	)
	if err != nil {
		return AIReplyExecutionResult{}, NewAIReplyExecutionError(AIReplyExecutionErrorCommitFailed, err)
	}
	if len(messages) != 2 {
		return AIReplyExecutionResult{}, NewAIReplyExecutionError(
			AIReplyExecutionErrorCommitFailed,
			fmt.Errorf("standalone one committed message count %d, want 2", len(messages)),
		)
	}
	return AIReplyExecutionResult{
		Status:              AIReplyExecutionStatusCompleted,
		ReasonCode:          "standalone_one_completed",
		CommittedMessageIDs: []int64{messages[0].ID, messages[1].ID},
	}, nil
}
