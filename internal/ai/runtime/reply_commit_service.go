package runtime

import (
	"fmt"
	"strings"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/repositories"
	svc "agent-desk/internal/services"

	"github.com/mlogclub/simple/sqls"
)

type replyCommitService struct{}

type replyCommitInput struct {
	Conversation   models.Conversation
	Message        models.Message
	AIAgent        models.AIAgent
	ReplyText      string
	Trace          *aiReplyTraceData
	ClientPrefix   string
	IncrementRound bool
}

func newReplyCommitService() *replyCommitService {
	return &replyCommitService{}
}

func (s *replyCommitService) SendAIReply(input replyCommitInput) (*models.Message, error) {
	replyText := strings.TrimSpace(input.ReplyText)
	if replyText == "" {
		return nil, nil
	}
	commitStartedAt := time.Now()
	replyMessage, err := svc.MessageService.SendAIMessageWithRequestID(
		input.Conversation.ID,
		input.AIAgent.ID,
		fmt.Sprintf("%s_%d", strings.TrimSpace(input.ClientPrefix), input.Message.ID),
		enums.IMMessageTypeText,
		replyText,
		"",
		s.buildAIPrincipal(input.AIAgent),
		input.Message.RequestID,
	)
	if input.Trace != nil {
		input.Trace.CommitMs = time.Since(commitStartedAt).Milliseconds()
		input.Trace.ReplySent = err == nil && replyMessage != nil
		if replyMessage != nil {
			input.Trace.ReplyMessageID = replyMessage.ID
		}
	}
	if err != nil || !input.IncrementRound {
		return replyMessage, err
	}
	if err := s.IncrementAIReplyRounds(input.Conversation.ID, input.Conversation.AIReplyRounds+1, input.AIAgent.Name); err != nil {
		return nil, err
	}
	return replyMessage, err
}

func (s *replyCommitService) CommitAIReply(input replyCommitInput) (*models.Message, error) {
	input.IncrementRound = true
	return s.SendAIReply(input)
}

func (s *replyCommitService) IncrementAIReplyRounds(conversationID int64, nextRounds int, aiAgentName string) error {
	return repositories.ConversationRepository.Updates(sqls.DB(), conversationID, map[string]any{
		"ai_reply_rounds":  nextRounds,
		"update_user_id":   0,
		"update_user_name": strings.TrimSpace(aiAgentName),
		"updated_at":       time.Now(),
	})
}

func (s *replyCommitService) buildAIPrincipal(aiAgent models.AIAgent) *dto.AuthPrincipal {
	username := "AI"
	if strings.TrimSpace(aiAgent.Name) != "" {
		username = aiAgent.Name
	}
	return &dto.AuthPrincipal{
		UserID:   0,
		Username: username,
		Nickname: username,
	}
}
