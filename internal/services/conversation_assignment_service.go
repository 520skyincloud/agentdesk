package services

import (
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/repositories"
	"strings"
	"time"

	"agent-desk/internal/pkg/httpx/params"

	"github.com/mlogclub/simple/sqls"
)

var ConversationAssignmentService = newConversationAssignmentService()

func newConversationAssignmentService() *conversationAssignmentService {
	return &conversationAssignmentService{}
}

type conversationAssignmentService struct {
}

type ConversationAssignmentOptions struct {
	SquadID            int64
	DispatchMode       enums.AgentTeamDispatchMode
	DecisionConfidence int
	WorkloadWeight     int
}

func (s *conversationAssignmentService) Get(id int64) *models.ConversationAssignment {
	return repositories.ConversationAssignmentRepository.Get(sqls.DB(), id)
}

func (s *conversationAssignmentService) Take(where ...interface{}) *models.ConversationAssignment {
	return repositories.ConversationAssignmentRepository.Take(sqls.DB(), where...)
}

func (s *conversationAssignmentService) Find(cnd *sqls.Cnd) []models.ConversationAssignment {
	return repositories.ConversationAssignmentRepository.Find(sqls.DB(), cnd)
}

func (s *conversationAssignmentService) FindOne(cnd *sqls.Cnd) *models.ConversationAssignment {
	return repositories.ConversationAssignmentRepository.FindOne(sqls.DB(), cnd)
}

func (s *conversationAssignmentService) FindPageByParams(params *params.QueryParams) (list []models.ConversationAssignment, paging *sqls.Paging) {
	return repositories.ConversationAssignmentRepository.FindPageByParams(sqls.DB(), params)
}

func (s *conversationAssignmentService) FindPageByCnd(cnd *sqls.Cnd) (list []models.ConversationAssignment, paging *sqls.Paging) {
	return repositories.ConversationAssignmentRepository.FindPageByCnd(sqls.DB(), cnd)
}

func (s *conversationAssignmentService) Count(cnd *sqls.Cnd) int64 {
	return repositories.ConversationAssignmentRepository.Count(sqls.DB(), cnd)
}

func (s *conversationAssignmentService) FinishActiveAssignments(ctx *sqls.TxContext, conversationID int64, finishedAt time.Time) error {
	conversation, err := requireConversationParent(ctx.Tx, conversationID)
	if err != nil {
		return err
	}
	return ctx.Tx.Model(&models.ConversationAssignment{}).
		Where("tenant_id = ? AND conversation_id = ? AND status = ?", conversation.TenantID, conversationID, enums.IMAssignmentStatusActive).
		Updates(map[string]any{
			"status":      enums.IMAssignmentStatusInactive,
			"finished_at": finishedAt,
		}).Error
}

func (s *conversationAssignmentService) CreateAssignment(ctx *sqls.TxContext, conversationID, fromUserID, toUserID int64, assignType enums.IMAssignmentType, reason string, operator *dto.AuthPrincipal, now time.Time) error {
	return s.CreateAssignmentWithSquad(ctx, conversationID, 0, fromUserID, toUserID, assignType, reason, operator, now)
}

func (s *conversationAssignmentService) CreateAssignmentWithSquad(ctx *sqls.TxContext, conversationID, squadID, fromUserID, toUserID int64, assignType enums.IMAssignmentType, reason string, operator *dto.AuthPrincipal, now time.Time) error {
	return s.CreateAssignmentWithOptions(ctx, conversationID, fromUserID, toUserID, assignType, reason, operator, now, ConversationAssignmentOptions{SquadID: squadID})
}

func (s *conversationAssignmentService) CreateAssignmentWithOptions(ctx *sqls.TxContext, conversationID, fromUserID, toUserID int64, assignType enums.IMAssignmentType, reason string, operator *dto.AuthPrincipal, now time.Time, options ConversationAssignmentOptions) error {
	conversation, err := requireConversationParent(ctx.Tx, conversationID)
	if err != nil {
		return err
	}
	workloadWeight := options.WorkloadWeight
	if workloadWeight <= 0 {
		workloadWeight = 1
	}
	confidence := options.DecisionConfidence
	if confidence < 0 {
		confidence = 0
	}
	if confidence > 100 {
		confidence = 100
	}
	dispatchMode := options.DispatchMode
	if !enums.IsValidAgentTeamDispatchMode(dispatchMode) {
		dispatchMode = enums.AgentTeamDispatchModeManual
	}
	assignment := &models.ConversationAssignment{
		TenantID:           conversation.TenantID,
		ConversationID:     conversationID,
		SessionNo:          currentSessionNoDB(ctx.Tx, conversationID, conversation.TenantID),
		SquadID:            options.SquadID,
		FromUserID:         fromUserID,
		ToUserID:           toUserID,
		AssignType:         strings.TrimSpace(string(assignType)),
		Reason:             strings.TrimSpace(reason),
		DispatchMode:       dispatchMode,
		DecisionConfidence: confidence,
		WorkloadWeight:     workloadWeight,
		Status:             enums.IMAssignmentStatusActive,
		CreatedAt:          now,
	}
	if operator != nil {
		assignment.OperatorID = operator.UserID
	}
	return ctx.Tx.Create(assignment).Error
}
