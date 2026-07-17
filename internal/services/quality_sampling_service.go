package services

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
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

type QualitySamplingAggregate struct {
	Batch models.QualitySamplingBatch
	Items []models.QualitySamplingItem
}

func (s *qualityInspectionService) ListSamplingBatches(cnd *sqls.Cnd, operator *dto.AuthPrincipal) ([]QualitySamplingAggregate, *sqls.Paging, error) {
	tenantID := AgentTeamScopeService.ActiveTenantID(operator)
	if tenantID <= 0 {
		return nil, nil, errorsx.Forbidden("请先进入需要查看抽样的接入公司")
	}
	if cnd == nil {
		cnd = sqls.NewCnd().Page(1, 20).Desc("created_at").Desc("id")
	}
	cnd.Eq("tenant_id", tenantID)
	if !AgentTeamScopeService.IsAdmin(operator) {
		switch {
		case slices.Contains(operator.Roles, constants.RoleCodeCsUser) && !slices.Contains(operator.Roles, constants.RoleCodeCsTeamLeader):
			cnd.Where(`EXISTS (
				SELECT 1 FROM t_quality_sampling_item sampling_item
				WHERE sampling_item.tenant_id = t_quality_sampling_batch.tenant_id
				AND sampling_item.batch_id = t_quality_sampling_batch.id
				AND sampling_item.agent_id = ?
			)`, operator.UserID)
		case slices.Contains(operator.Roles, constants.RoleCodeCsTeamLeader):
			teams := repositories.AgentTeamRepository.Find(sqls.DB(), sqls.NewCnd().Eq("tenant_id", tenantID).Eq("leader_user_id", operator.UserID).Where("status <> ?", enums.StatusDeleted))
			teamIDs := make([]int64, 0, len(teams))
			for _, team := range teams {
				teamIDs = append(teamIDs, team.ID)
			}
			squadIDs, userIDs := assignmentTeamScopeIDs(tenantID, teamIDs)
			switch {
			case len(squadIDs) > 0 && len(userIDs) > 0:
				cnd.Where(`EXISTS (
					SELECT 1 FROM t_quality_sampling_item sampling_item
					JOIN t_conversation_assignment assignment ON assignment.id = sampling_item.assignment_id AND assignment.tenant_id = sampling_item.tenant_id
					WHERE sampling_item.tenant_id = t_quality_sampling_batch.tenant_id
					AND sampling_item.batch_id = t_quality_sampling_batch.id
					AND (assignment.squad_id IN (?) OR (assignment.squad_id = 0 AND assignment.to_user_id IN (?)))
				)`, squadIDs, userIDs)
			case len(squadIDs) > 0:
				cnd.Where(`EXISTS (
					SELECT 1 FROM t_quality_sampling_item sampling_item
					JOIN t_conversation_assignment assignment ON assignment.id = sampling_item.assignment_id AND assignment.tenant_id = sampling_item.tenant_id
					WHERE sampling_item.tenant_id = t_quality_sampling_batch.tenant_id
					AND sampling_item.batch_id = t_quality_sampling_batch.id
					AND assignment.squad_id IN (?)
				)`, squadIDs)
			case len(userIDs) > 0:
				cnd.Where(`EXISTS (
					SELECT 1 FROM t_quality_sampling_item sampling_item
					JOIN t_conversation_assignment assignment ON assignment.id = sampling_item.assignment_id AND assignment.tenant_id = sampling_item.tenant_id
					WHERE sampling_item.tenant_id = t_quality_sampling_batch.tenant_id
					AND sampling_item.batch_id = t_quality_sampling_batch.id
					AND assignment.squad_id = 0 AND assignment.to_user_id IN (?)
				)`, userIDs)
			default:
				cnd.Eq("id", -1)
			}
		default:
			cnd.Eq("id", -1)
		}
	}
	list, paging := repositories.QualitySamplingBatchRepository.FindPageByCnd(sqls.DB(), cnd)
	results := make([]QualitySamplingAggregate, 0, len(list))
	for i := range list {
		items := repositories.QualitySamplingItemRepository.FindByBatch(sqls.DB(), tenantID, list[i].ID)
		visible := make([]models.QualitySamplingItem, 0, len(items))
		for _, item := range items {
			if s.canViewSamplingItem(operator, &item) {
				visible = append(visible, item)
			}
		}
		batch := list[i]
		if !AgentTeamScopeService.IsAdmin(operator) {
			batch.SampleSize = len(visible)
		}
		results = append(results, QualitySamplingAggregate{Batch: batch, Items: visible})
	}
	return results, paging, nil
}

func (s *qualityInspectionService) CreateSamplingBatch(req request.CreateQualitySamplingRequest, operator *dto.AuthPrincipal) (*QualitySamplingAggregate, error) {
	tenantID := AgentTeamScopeService.ActiveTenantID(operator)
	if tenantID <= 0 {
		return nil, errorsx.Forbidden("请先进入需要抽样的接入公司")
	}
	name := strings.TrimSpace(req.Name)
	if name == "" || len(name) > 120 {
		return nil, errorsx.InvalidParam("抽样批次名称不能为空且不能超过120个字符")
	}
	startAt, err := parseAnalyticsTimeInput(req.StartAt, false)
	if err != nil {
		return nil, errorsx.InvalidParam("抽样开始时间格式无效")
	}
	endAt, err := parseAnalyticsTimeInput(req.EndAt, true)
	if err != nil || endAt.Before(startAt) {
		return nil, errorsx.InvalidParam("抽样结束时间格式或范围无效")
	}
	if req.SampleSize <= 0 || req.SampleSize > 1000 {
		return nil, errorsx.InvalidParam("抽样数量必须在1到1000之间")
	}
	cnd := sqls.NewCnd().Eq("tenant_id", tenantID).
		Gte("created_at", startAt).Lte("created_at", endAt).
		Where(`EXISTS (
			SELECT 1 FROM t_message m
			WHERE m.tenant_id = t_conversation_assignment.tenant_id
			AND m.conversation_id = t_conversation_assignment.conversation_id
			AND m.session_no = t_conversation_assignment.session_no
			AND m.sender_type = ?
			AND m.sender_id = t_conversation_assignment.to_user_id
			AND m.created_at >= t_conversation_assignment.created_at
			AND (t_conversation_assignment.finished_at IS NULL OR m.created_at <= t_conversation_assignment.finished_at)
		)`, enums.IMSenderTypeAgent).
		Desc("created_at").Limit(10000)
	cnd = s.applyAssignmentScope(cnd, operator)
	if req.AgentID > 0 {
		cnd.Eq("to_user_id", req.AgentID)
	}
	if req.TeamID > 0 {
		cnd = s.applyAssignmentTeamScope(cnd, tenantID, []int64{req.TeamID})
	}
	candidates := repositories.ConversationAssignmentRepository.Find(sqls.DB(), cnd)
	if len(candidates) == 0 {
		return nil, errorsx.InvalidParam("当前筛选范围没有可质检的人工接待分段")
	}
	seedBytes := make([]byte, 16)
	if _, err := rand.Read(seedBytes); err != nil {
		return nil, err
	}
	seed := hex.EncodeToString(seedBytes)
	sort.SliceStable(candidates, func(i, j int) bool {
		left := sha256.Sum256([]byte(fmt.Sprintf("%s:%d", seed, candidates[i].ID)))
		right := sha256.Sum256([]byte(fmt.Sprintf("%s:%d", seed, candidates[j].ID)))
		return string(left[:]) < string(right[:])
	})
	if len(candidates) > req.SampleSize {
		candidates = candidates[:req.SampleSize]
	}
	criteria, _ := json.Marshal(map[string]any{
		"teamId": req.TeamID, "agentId": req.AgentID,
		"startAt": startAt.Format(time.DateTime), "endAt": endAt.Format(time.DateTime),
	})
	batch := &models.QualitySamplingBatch{
		TenantID: tenantID, Name: name, CriteriaJSON: string(criteria), Seed: seed,
		SampleSize: len(candidates), Status: enums.QualitySamplingStatusReady,
		CreatedBy: operator.UserID, AuditFields: utils.BuildAuditFields(operator),
	}
	err = sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		if err := repositories.QualitySamplingBatchRepository.Create(ctx.Tx, batch); err != nil {
			return err
		}
		for i := range candidates {
			assignment := &candidates[i]
			item := &models.QualitySamplingItem{
				TenantID: tenantID, BatchID: batch.ID, AssignmentID: assignment.ID,
				ConversationID: assignment.ConversationID, SessionNo: normalizedSessionNo(assignment.SessionNo),
				AgentID: assignment.ToUserID, AuditFields: utils.BuildAuditFields(operator),
			}
			if err := repositories.QualitySamplingItemRepository.Create(ctx.Tx, item); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return s.GetSamplingBatch(batch.ID, operator)
}

func (s *qualityInspectionService) GetSamplingBatch(id int64, operator *dto.AuthPrincipal) (*QualitySamplingAggregate, error) {
	tenantID := AgentTeamScopeService.ActiveTenantID(operator)
	batch := repositories.QualitySamplingBatchRepository.GetInTenant(sqls.DB(), id, tenantID)
	if batch == nil {
		return nil, errorsx.InvalidParam("质检抽样批次不存在")
	}
	items := repositories.QualitySamplingItemRepository.FindByBatch(sqls.DB(), tenantID, batch.ID)
	visible := make([]models.QualitySamplingItem, 0, len(items))
	for _, item := range items {
		if s.canViewSamplingItem(operator, &item) {
			visible = append(visible, item)
		}
	}
	if len(items) > 0 && len(visible) == 0 {
		return nil, errorsx.Forbidden("无权查看该抽样批次")
	}
	if !AgentTeamScopeService.IsAdmin(operator) {
		batch.SampleSize = len(visible)
	}
	return &QualitySamplingAggregate{Batch: *batch, Items: visible}, nil
}

func (s *qualityInspectionService) canViewSamplingItem(operator *dto.AuthPrincipal, item *models.QualitySamplingItem) bool {
	if item == nil || !AgentTeamScopeService.CanViewConversation(operator, item.ConversationID) {
		return false
	}
	tenantID := AgentTeamScopeService.ActiveTenantID(operator)
	assignment := repositories.ConversationAssignmentRepository.Get(sqls.DB(), item.AssignmentID)
	if assignment == nil || assignment.TenantID != tenantID || assignment.ConversationID != item.ConversationID {
		return false
	}
	teamID := ServiceAnalyticsService.assignmentTeamID(assignment, tenantID)
	return ServiceAnalyticsService.canViewAgent(operator, assignment.ToUserID, teamID)
}

func parseAnalyticsTimeInput(raw string, endOfDay bool) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	for _, layout := range []string{time.RFC3339, time.DateTime, time.DateOnly} {
		value, err := time.ParseInLocation(layout, raw, time.Local)
		if err != nil {
			continue
		}
		if layout == time.DateOnly && endOfDay {
			value = value.Add(24*time.Hour - time.Nanosecond)
		}
		return value, nil
	}
	return time.Time{}, fmt.Errorf("invalid time")
}
