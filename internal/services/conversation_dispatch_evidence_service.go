package services

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/utils"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

var ServiceAnalyticsCaptureService = &serviceAnalyticsCaptureService{}

type serviceAnalyticsCaptureService struct{}

type DispatchDecisionCandidateEvidence struct {
	UserID              int64   `json:"userId"`
	TeamID              int64   `json:"teamId"`
	SquadID             int64   `json:"squadId"`
	ActiveCount         int     `json:"activeCount"`
	WeightedOpenLoad    int     `json:"weightedOpenLoad"`
	PendingFirstReply   int     `json:"pendingFirstReply"`
	PendingReplyCount   int     `json:"pendingReplyCount"`
	ShiftWorkloadWeight int     `json:"shiftWorkloadWeight"`
	NormalizedLoad      float64 `json:"normalizedLoad"`
}

type DispatchDecisionEvidence struct {
	DecisionKey           string
	ConversationID        int64
	SessionNo             int
	AssignmentID          int64
	Trigger               string
	DecisionMode          string
	Status                enums.DispatchDecisionStatus
	Candidates            []DispatchDecisionCandidateEvidence
	InputLastMessageID    int64
	SelectedUserID        int64
	SelectedTeamID        int64
	SelectedSquadID       int64
	DecisionLatencyMillis int64
	Reason                string
	FallbackReason        string
	OperatorID            int64
	DecidedAt             time.Time
}

func currentSessionNoDB(db *gorm.DB, conversationID, tenantID int64) int {
	route := repositories.ConversationRouteStateRepository.TakeByConversationInTenant(db, conversationID, tenantID)
	if route == nil || route.SessionNo <= 0 {
		return 1
	}
	return route.SessionNo
}

func (s *serviceAnalyticsCaptureService) RecordDispatchEvidence(evidence DispatchDecisionEvidence) error {
	conversation := repositories.ConversationRepository.Get(sqls.DB(), evidence.ConversationID)
	if conversation == nil || conversation.TenantID <= 0 {
		return nil
	}
	if evidence.SessionNo <= 0 {
		evidence.SessionNo = 1
		if route := repositories.ConversationRouteStateRepository.TakeByConversationInTenant(sqls.DB(), conversation.ID, conversation.TenantID); route != nil && route.SessionNo > 0 {
			evidence.SessionNo = route.SessionNo
		}
	}
	if evidence.DecidedAt.IsZero() {
		evidence.DecidedAt = time.Now()
	}
	if evidence.Status == "" {
		evidence.Status = enums.DispatchDecisionStatusFailed
	}
	if evidence.AssignmentID > 0 && repositories.DispatchDecisionLogRepository.TakeByAssignment(sqls.DB(), conversation.TenantID, evidence.AssignmentID) != nil {
		return nil
	}
	userIDs := make([]int64, 0, len(evidence.Candidates))
	for _, candidate := range evidence.Candidates {
		userIDs = append(userIDs, candidate.UserID)
	}
	userIDsJSON, _ := json.Marshal(userIDs)
	candidatesJSON, _ := json.Marshal(evidence.Candidates)
	decisionKey := strings.TrimSpace(evidence.DecisionKey)
	if evidence.AssignmentID > 0 {
		decisionKey = fmt.Sprintf("assignment:%d", evidence.AssignmentID)
	} else if decisionKey == "" {
		fingerprint := fmt.Sprintf("%d|%d|%d|%s|%s|%s|%d|%d|%d|%d|%s|%s|%s",
			conversation.TenantID,
			conversation.ID,
			evidence.SessionNo,
			strings.TrimSpace(evidence.Trigger),
			strings.TrimSpace(evidence.DecisionMode),
			evidence.Status,
			evidence.InputLastMessageID,
			evidence.SelectedUserID,
			evidence.SelectedTeamID,
			evidence.SelectedSquadID,
			strings.TrimSpace(evidence.Reason),
			strings.TrimSpace(evidence.FallbackReason),
			string(candidatesJSON),
		)
		hash := sha256.Sum256([]byte(fingerprint))
		decisionKey = fmt.Sprintf("attempt:%d:%d:%d:%x", conversation.TenantID, conversation.ID, evidence.SessionNo, hash[:8])
	}
	return repositories.DispatchDecisionLogRepository.CreateIfAbsent(sqls.DB(), &models.DispatchDecisionLog{
		TenantID:              conversation.TenantID,
		DecisionKey:           decisionKey,
		ConversationID:        conversation.ID,
		SessionNo:             evidence.SessionNo,
		AssignmentID:          evidence.AssignmentID,
		Trigger:               strings.TrimSpace(evidence.Trigger),
		DecisionMode:          strings.TrimSpace(evidence.DecisionMode),
		Status:                evidence.Status,
		CandidateUserIDsJSON:  string(userIDsJSON),
		CandidateSnapshotJSON: string(candidatesJSON),
		InputLastMessageID:    evidence.InputLastMessageID,
		SelectedUserID:        evidence.SelectedUserID,
		SelectedTeamID:        evidence.SelectedTeamID,
		SelectedSquadID:       evidence.SelectedSquadID,
		DecisionLatencyMillis: evidence.DecisionLatencyMillis,
		Reason:                strings.TrimSpace(evidence.Reason),
		FallbackReason:        strings.TrimSpace(evidence.FallbackReason),
		OperatorID:            evidence.OperatorID,
		DecidedAt:             evidence.DecidedAt,
		AuditFields:           utils.BuildAuditFields(nil),
	})
}
