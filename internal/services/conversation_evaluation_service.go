package services

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"slices"
	"strings"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/errorsx"
	"agent-desk/internal/pkg/utils"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
)

var ConversationEvaluationService = &conversationEvaluationService{}

type conversationEvaluationService struct{}

type ConversationEvaluationInvite struct {
	Evaluation models.ConversationEvaluation
	Path       string
}

type PublicConversationEvaluation struct {
	Evaluation  models.ConversationEvaluation
	CompanyName string
}

func (s *conversationEvaluationService) List(cnd *sqls.Cnd, teamID, agentID int64, operator *dto.AuthPrincipal) ([]models.ConversationEvaluation, *sqls.Paging, error) {
	tenantID := AgentTeamScopeService.ActiveTenantID(operator)
	if tenantID <= 0 {
		return nil, nil, errorsx.Forbidden("请先进入需要查看评价的接入公司")
	}
	if cnd == nil {
		cnd = sqls.NewCnd().Page(1, 20).Desc("invited_at").Desc("id")
	}
	cnd.Eq("tenant_id", tenantID)
	if slices.Contains(operator.Roles, constants.RoleCodeCsUser) && !slices.Contains(operator.Roles, constants.RoleCodeCsTeamLeader) {
		agentID = operator.UserID
	}
	if agentID > 0 {
		cnd.Where("assignment_id IN (SELECT id FROM t_conversation_assignment WHERE tenant_id = ? AND to_user_id = ?)", tenantID, agentID)
	}
	if !AgentTeamScopeService.IsAdmin(operator) || teamID > 0 {
		teamIDs := []int64{}
		if slices.Contains(operator.Roles, constants.RoleCodeCsTeamLeader) {
			for _, team := range repositories.AgentTeamRepository.Find(sqls.DB(), sqls.NewCnd().Eq("tenant_id", tenantID).Eq("leader_user_id", operator.UserID).Where("status <> ?", enums.StatusDeleted)) {
				teamIDs = append(teamIDs, team.ID)
			}
		}
		if AgentTeamScopeService.IsAdmin(operator) && teamID > 0 {
			teamIDs = []int64{teamID}
		} else if teamID > 0 {
			if !slices.Contains(teamIDs, teamID) {
				return []models.ConversationEvaluation{}, &sqls.Paging{Page: cnd.Paging.Page, Limit: cnd.Paging.Limit, Total: 0}, nil
			}
			teamIDs = []int64{teamID}
		}
		if len(teamIDs) > 0 {
			squadIDs, userIDs := assignmentTeamScopeIDs(tenantID, teamIDs)
			switch {
			case len(squadIDs) > 0 && len(userIDs) > 0:
				cnd.Where("assignment_id IN (SELECT id FROM t_conversation_assignment WHERE tenant_id = ? AND (squad_id IN (?) OR (squad_id = 0 AND to_user_id IN (?))))", tenantID, squadIDs, userIDs)
			case len(squadIDs) > 0:
				cnd.Where("assignment_id IN (SELECT id FROM t_conversation_assignment WHERE tenant_id = ? AND squad_id IN (?))", tenantID, squadIDs)
			case len(userIDs) > 0:
				cnd.Where("assignment_id IN (SELECT id FROM t_conversation_assignment WHERE tenant_id = ? AND squad_id = 0 AND to_user_id IN (?))", tenantID, userIDs)
			default:
				cnd.Eq("id", -1)
			}
		} else if !AgentTeamScopeService.IsAdmin(operator) && !slices.Contains(operator.Roles, constants.RoleCodeCsUser) {
			cnd.Eq("id", -1)
		}
	}
	list, paging := repositories.ConversationEvaluationRepository.FindPageByCnd(sqls.DB(), cnd)
	return list, paging, nil
}

func (s *conversationEvaluationService) Invite(req request.InviteConversationEvaluationRequest, operator *dto.AuthPrincipal) (*ConversationEvaluationInvite, error) {
	session, err := ServiceAnalyticsService.GetSession(req.ServiceSessionID, operator)
	if err != nil || session == nil || !session.HumanHandled {
		return nil, errorsx.InvalidParam("仅已产生人工回复的服务轮次可以邀请评价")
	}
	assignmentID := req.AssignmentID
	if assignmentID <= 0 {
		assignmentID = session.LastAssignmentID
	}
	if assignmentID > 0 {
		assignment := repositories.ConversationAssignmentRepository.Get(sqls.DB(), assignmentID)
		if assignment == nil || assignment.TenantID != session.TenantID || assignment.ConversationID != session.ConversationID || normalizedSessionNo(assignment.SessionNo) != session.SessionNo {
			return nil, errorsx.InvalidParam("评价目标人工接待分段无效")
		}
	}
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		return nil, err
	}
	token := hex.EncodeToString(tokenBytes)
	tokenHash := evaluationTokenHash(token)
	policy := ServiceAnalyticsService.GetPolicy(session.TenantID)
	now := time.Now()
	item := &models.ConversationEvaluation{
		TenantID: session.TenantID, ConversationID: session.ConversationID, SessionNo: session.SessionNo,
		AssignmentID: assignmentID, CustomerID: session.CustomerID,
		Status: enums.ConversationEvaluationStatusPending, InviteChannel: "link", TokenHash: tokenHash,
		InvitedBy: operator.UserID, InvitedAt: now, ExpiresAt: now.Add(time.Duration(policy.EvaluationExpiryHours) * time.Hour),
		AuditFields: utils.BuildAuditFields(operator),
	}
	err = sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		if previous := repositories.ConversationEvaluationRepository.TakePendingBySession(ctx.Tx, session.TenantID, session.ConversationID, session.SessionNo); previous != nil {
			if err := repositories.ConversationEvaluationRepository.UpdatesInTenant(ctx.Tx, previous.ID, session.TenantID, map[string]any{
				"status": enums.ConversationEvaluationStatusCancelled, "updated_at": now,
				"update_user_id": operator.UserID, "update_user_name": operator.Username,
			}); err != nil {
				return err
			}
		}
		return repositories.ConversationEvaluationRepository.Create(ctx.Tx, item)
	})
	if err != nil {
		return nil, err
	}
	return &ConversationEvaluationInvite{
		Evaluation: *item,
		Path:       "/support/evaluation?token=" + url.QueryEscape(token),
	}, nil
}

func (s *conversationEvaluationService) Validate(token string) (*PublicConversationEvaluation, error) {
	tokenHash := evaluationTokenHash(strings.TrimSpace(token))
	if tokenHash == "" {
		return nil, invalidEvaluationTokenError()
	}
	item := repositories.ConversationEvaluationRepository.TakeByTokenHash(sqls.DB(), tokenHash)
	if item == nil {
		return nil, invalidEvaluationTokenError()
	}
	if item.Status == enums.ConversationEvaluationStatusPending && !time.Now().Before(item.ExpiresAt) {
		_ = repositories.ConversationEvaluationRepository.UpdatesInTenant(sqls.DB(), item.ID, item.TenantID, map[string]any{
			"status": enums.ConversationEvaluationStatusExpired, "updated_at": time.Now(), "update_user_name": "system",
		})
		item.Status = enums.ConversationEvaluationStatusExpired
	}
	tenant := repositories.TenantRepository.Get(sqls.DB(), item.TenantID)
	if tenant == nil {
		return nil, invalidEvaluationTokenError()
	}
	companyName := strings.TrimSpace(tenant.ShortName)
	if companyName == "" {
		companyName = strings.TrimSpace(tenant.LegalName)
	}
	return &PublicConversationEvaluation{Evaluation: *item, CompanyName: companyName}, nil
}

func (s *conversationEvaluationService) Submit(req request.SubmitConversationEvaluationRequest) (*PublicConversationEvaluation, error) {
	tokenHash := evaluationTokenHash(strings.TrimSpace(req.Token))
	if tokenHash == "" || req.Rating < 1 || req.Rating > 5 {
		return nil, invalidEvaluationTokenError()
	}
	comment := strings.TrimSpace(req.Comment)
	if len(comment) > 2000 {
		return nil, errorsx.InvalidParam("评价内容不能超过2000个字符")
	}
	tags := uniqueEvaluationTags(req.TagCodes)
	tagsJSON, _ := json.Marshal(tags)
	unlock := lockConversationEvaluation(tokenHash)
	defer unlock()
	now := time.Now()
	err := sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		item, err := repositories.ConversationEvaluationRepository.TakeByTokenHashForUpdate(ctx.Tx, tokenHash)
		if err != nil {
			return err
		}
		if item == nil {
			return invalidEvaluationTokenError()
		}
		if item.Status == enums.ConversationEvaluationStatusSubmitted {
			return nil
		}
		if item.Status != enums.ConversationEvaluationStatusPending || !now.Before(item.ExpiresAt) {
			return invalidEvaluationTokenError()
		}
		result := ctx.Tx.Model(&models.ConversationEvaluation{}).
			Where("id = ? AND tenant_id = ? AND token_hash = ? AND status = ? AND expires_at > ?", item.ID, item.TenantID, tokenHash, enums.ConversationEvaluationStatusPending, now).
			Updates(map[string]any{
				"status": enums.ConversationEvaluationStatusSubmitted, "submitted_at": now,
				"rating": req.Rating, "tag_codes_json": string(tagsJSON), "comment": comment,
				"updated_at": now, "update_user_name": "customer",
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return invalidEvaluationTokenError()
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return s.Validate(req.Token)
}

func evaluationTokenHash(token string) string {
	if token == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func invalidEvaluationTokenError() error {
	return errorsx.InvalidParam("评价链接无效或已过期")
}

func uniqueEvaluationTags(values []string) []string {
	allowed := map[string]struct{}{
		"resolved": {}, "professional": {}, "fast": {}, "friendly": {},
		"unresolved": {}, "slow": {}, "unclear": {}, "rude": {},
	}
	seen := make(map[string]struct{}, len(values))
	ret := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if _, ok := allowed[value]; !ok {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		ret = append(ret, value)
	}
	return ret
}
