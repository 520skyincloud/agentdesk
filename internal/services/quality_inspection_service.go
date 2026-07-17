package services

import (
	"encoding/json"
	"fmt"
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
	"gorm.io/gorm"
)

var QualityInspectionService = &qualityInspectionService{}

type qualityInspectionService struct{}

type QualityPoolQuery struct {
	Page           int
	Limit          int
	ConversationID int64
	SessionNo      int
	AgentID        int64
	TeamID         int64
	Status         string
	StartAt        *time.Time
	EndAt          *time.Time
	Keyword        string
}

type QualityPoolAggregate struct {
	Assignment   models.ConversationAssignment
	Conversation *models.Conversation
	Session      *models.ConversationServiceSession
	Route        *models.ConversationRouteState
	Agent        *models.User
	Team         *models.AgentTeam
	HumanReplies int64
	Inspection   *QualityInspectionAggregate
}

type QualityTemplateAggregate struct {
	Template models.QualityTemplate
	Items    []models.QualityTemplateItem
}

type QualityInspectionAggregate struct {
	Inspection models.QualityInspection
	Items      []models.QualityInspectionItem
	Agent      *models.User
	Team       *models.AgentTeam
}

type qualityItemEvaluation struct {
	Template    models.QualityTemplateItem
	Score       int
	Passed      bool
	HardFailed  bool
	MetricValue string
}

func (s *qualityInspectionService) ListPool(query QualityPoolQuery, operator *dto.AuthPrincipal) ([]QualityPoolAggregate, *sqls.Paging, error) {
	tenantID := AgentTeamScopeService.ActiveTenantID(operator)
	if tenantID <= 0 {
		return nil, nil, errorsx.Forbidden("请先进入需要管理质检的接入公司")
	}
	if query.Page <= 0 {
		query.Page = 1
	}
	if query.Limit <= 0 || query.Limit > 100 {
		query.Limit = 20
	}
	cnd := sqls.NewCnd().Eq("tenant_id", tenantID)
	if query.ConversationID > 0 {
		cnd.Eq("conversation_id", query.ConversationID)
	}
	if query.SessionNo > 0 {
		cnd.Eq("session_no", query.SessionNo)
	}
	cnd.Where(`EXISTS (
		SELECT 1 FROM t_message m
		WHERE m.tenant_id = t_conversation_assignment.tenant_id
		  AND m.conversation_id = t_conversation_assignment.conversation_id
		  AND m.session_no = t_conversation_assignment.session_no
		  AND m.sender_type = ?
		  AND m.sender_id = t_conversation_assignment.to_user_id
		  AND m.recalled_at IS NULL
		  AND m.created_at >= t_conversation_assignment.created_at
		  AND (t_conversation_assignment.finished_at IS NULL OR m.created_at <= t_conversation_assignment.finished_at)
	)`, enums.IMSenderTypeAgent)
	if query.AgentID > 0 {
		cnd.Eq("to_user_id", query.AgentID)
	}
	if query.TeamID > 0 {
		cnd = s.applyAssignmentTeamScope(cnd, tenantID, []int64{query.TeamID})
	}
	if query.StartAt != nil {
		cnd.Gte("created_at", *query.StartAt)
	}
	if query.EndAt != nil {
		cnd.Lte("created_at", *query.EndAt)
	}
	if keyword := strings.TrimSpace(query.Keyword); keyword != "" {
		like := "%" + keyword + "%"
		cnd.Where("conversation_id IN (SELECT id FROM t_conversation WHERE tenant_id = ? AND customer_name LIKE ?)", tenantID, like)
	}
	cnd = s.applyAssignmentScope(cnd, operator)
	if status := strings.TrimSpace(query.Status); status != "" {
		if status == "uninspected" {
			cnd.Where("id NOT IN (SELECT assignment_id FROM t_quality_inspection WHERE tenant_id = ?)", tenantID)
		} else {
			cnd.Where("id IN (SELECT assignment_id FROM t_quality_inspection WHERE tenant_id = ? AND status = ?)", tenantID, status)
		}
	}
	cnd.Desc("created_at").Desc("id").Page(query.Page, query.Limit)
	assignments, paging := repositories.ConversationAssignmentRepository.FindPageByCnd(sqls.DB(), cnd)
	results := make([]QualityPoolAggregate, 0, len(assignments))
	defaultTemplate, _ := s.EnsureDefaultTemplate(tenantID)
	for i := range assignments {
		assignment := assignments[i]
		conversation := repositories.ConversationRepository.GetInTenant(sqls.DB(), assignment.ConversationID, tenantID)
		if conversation == nil || !AgentTeamScopeService.CanViewConversation(operator, conversation.ID) {
			continue
		}
		aggregate := QualityPoolAggregate{
			Assignment:   assignment,
			Conversation: conversation,
			Session:      repositories.ConversationServiceSessionRepository.TakeByKey(sqls.DB(), tenantID, assignment.ConversationID, assignment.SessionNo),
			Route:        repositories.ConversationRouteStateRepository.TakeByConversationInTenant(sqls.DB(), assignment.ConversationID, tenantID),
			Agent:        repositories.UserRepository.GetInTenant(sqls.DB(), assignment.ToUserID, tenantID),
		}
		if teamID := ServiceAnalyticsService.assignmentTeamID(&assignment, tenantID); teamID > 0 {
			aggregate.Team = repositories.AgentTeamRepository.GetInTenant(sqls.DB(), teamID, tenantID)
		}
		aggregate.HumanReplies = s.countAssignmentReplies(sqls.DB(), &assignment)
		if defaultTemplate != nil {
			if inspection := repositories.QualityInspectionRepository.FindOneByAssignment(sqls.DB(), tenantID, assignment.ID, defaultTemplate.Template.ID); inspection != nil {
				aggregate.Inspection = s.buildInspectionAggregate(inspection, tenantID)
			}
		}
		results = append(results, aggregate)
	}
	return results, paging, nil
}

func (s *qualityInspectionService) ListTemplates(operator *dto.AuthPrincipal) ([]QualityTemplateAggregate, error) {
	tenantID := AgentTeamScopeService.ActiveTenantID(operator)
	if tenantID <= 0 {
		return nil, errorsx.Forbidden("请先进入需要管理质检的接入公司")
	}
	if _, err := s.EnsureDefaultTemplate(tenantID); err != nil {
		return nil, err
	}
	list := repositories.QualityTemplateRepository.Find(sqls.DB(), sqls.NewCnd().Eq("tenant_id", tenantID).Where("status <> ?", enums.StatusDeleted).Desc("is_default").Asc("id"))
	ret := make([]QualityTemplateAggregate, 0, len(list))
	for i := range list {
		ret = append(ret, QualityTemplateAggregate{Template: list[i], Items: repositories.QualityTemplateItemRepository.FindByTemplate(sqls.DB(), tenantID, list[i].ID)})
	}
	return ret, nil
}

func (s *qualityInspectionService) SaveTemplate(req request.SaveQualityTemplateRequest, operator *dto.AuthPrincipal) (*QualityTemplateAggregate, error) {
	tenantID := AgentTeamScopeService.ActiveTenantID(operator)
	if tenantID <= 0 {
		return nil, errorsx.Forbidden("请先进入需要管理质检的接入公司")
	}
	name := strings.TrimSpace(req.Name)
	if name == "" || len(req.Items) == 0 {
		return nil, errorsx.InvalidParam("质检模板名称和评分项不能为空")
	}
	totalScore := 0
	for _, item := range req.Items {
		ruleType := enums.QualityRuleType(strings.TrimSpace(item.RuleType))
		if ruleType == "" {
			ruleType = enums.QualityRuleTypeScore
		}
		if strings.TrimSpace(item.Name) == "" {
			return nil, errorsx.InvalidParam("质检项名称不能为空")
		}
		switch ruleType {
		case enums.QualityRuleTypeScore:
			if item.MaxScore <= 0 {
				return nil, errorsx.InvalidParam("人工评分项满分必须大于0")
			}
		case enums.QualityRuleTypeMetric:
			if item.MaxScore < 0 || !isSupportedQualityMetric(item.MetricCode) {
				return nil, errorsx.InvalidParam("系统指标项配置无效")
			}
		case enums.QualityRuleTypeProhibited:
			if item.MaxScore != 0 {
				return nil, errorsx.InvalidParam("禁忌项满分必须为0")
			}
		default:
			return nil, errorsx.InvalidParam("质检规则类型无效")
		}
		totalScore += item.MaxScore
	}
	if req.PassScore < 0 || req.PassScore > totalScore {
		return nil, errorsx.InvalidParam("合格分不能超过模板总分")
	}
	var templateID int64
	err := sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		now := time.Now()
		version := 1
		if req.ID > 0 {
			existing := repositories.QualityTemplateRepository.GetInTenant(ctx.Tx, req.ID, tenantID)
			if existing == nil || existing.Status == enums.StatusDeleted {
				return errorsx.InvalidParam("质检模板不存在")
			}
			version = existing.Version + 1
			if existing.IsDefault {
				req.IsDefault = true
			}
		}
		if req.IsDefault {
			if err := ctx.Tx.Model(&models.QualityTemplate{}).Where("tenant_id = ? AND is_default = ?", tenantID, true).Updates(map[string]any{"is_default": false, "updated_at": now}).Error; err != nil {
				return err
			}
		}
		template := &models.QualityTemplate{
			TenantID: tenantID, Name: name, Description: strings.TrimSpace(req.Description), TotalScore: totalScore,
			PassScore: req.PassScore, Version: version, IsDefault: req.IsDefault, Status: enums.StatusOk, AuditFields: utils.BuildAuditFields(operator),
		}
		if err := repositories.QualityTemplateRepository.Create(ctx.Tx, template); err != nil {
			return err
		}
		templateID = template.ID
		for index, input := range req.Items {
			code := strings.TrimSpace(input.Code)
			if code == "" {
				code = fmt.Sprintf("item_%d", index+1)
			}
			item := &models.QualityTemplateItem{
				TenantID: tenantID, TemplateID: templateID, Code: code, Name: strings.TrimSpace(input.Name), Description: strings.TrimSpace(input.Description),
				RuleType: enums.QualityRuleType(strings.TrimSpace(input.RuleType)), MetricCode: strings.TrimSpace(input.MetricCode),
				MaxScore: input.MaxScore, Required: input.Required, HardFail: input.HardFail,
				SortNo: input.SortNo, Status: enums.StatusOk, AuditFields: utils.BuildAuditFields(operator),
			}
			if item.RuleType == "" {
				item.RuleType = enums.QualityRuleTypeScore
			}
			if err := repositories.QualityTemplateItemRepository.Create(ctx.Tx, item); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	template := repositories.QualityTemplateRepository.GetInTenant(sqls.DB(), templateID, tenantID)
	if template == nil {
		return nil, errorsx.InvalidParam("质检模板不存在")
	}
	return &QualityTemplateAggregate{Template: *template, Items: repositories.QualityTemplateItemRepository.FindByTemplate(sqls.DB(), tenantID, templateID)}, nil
}

func (s *qualityInspectionService) GetInspection(id int64, operator *dto.AuthPrincipal) (*QualityInspectionAggregate, error) {
	tenantID := AgentTeamScopeService.ActiveTenantID(operator)
	inspection := repositories.QualityInspectionRepository.GetInTenant(sqls.DB(), id, tenantID)
	if inspection == nil || !AgentTeamScopeService.CanViewConversation(operator, inspection.ConversationID) ||
		!ServiceAnalyticsService.canViewAgent(operator, inspection.AgentID, inspection.TeamID) {
		return nil, errorsx.InvalidParam("质检记录不存在")
	}
	return s.buildInspectionAggregate(inspection, tenantID), nil
}

func (s *qualityInspectionService) SaveInspection(req request.SaveQualityInspectionRequest, operator *dto.AuthPrincipal) (*QualityInspectionAggregate, error) {
	tenantID := AgentTeamScopeService.ActiveTenantID(operator)
	if tenantID <= 0 {
		return nil, errorsx.Forbidden("请先进入需要管理质检的接入公司")
	}
	assignment := repositories.ConversationAssignmentRepository.Get(sqls.DB(), req.AssignmentID)
	if assignment == nil || assignment.TenantID != tenantID || !AgentTeamScopeService.CanViewConversation(operator, assignment.ConversationID) {
		return nil, errorsx.InvalidParam("人工接待分段不存在")
	}
	assignmentTeamID := ServiceAnalyticsService.assignmentTeamID(assignment, tenantID)
	if !ServiceAnalyticsService.canViewAgent(operator, assignment.ToUserID, assignmentTeamID) {
		return nil, errorsx.Forbidden("无权质检该人工接待分段")
	}
	if s.countAssignmentReplies(sqls.DB(), assignment) == 0 {
		return nil, errorsx.InvalidParam("该接待分段没有人工客服回复，不能进入内容质检")
	}
	template := repositories.QualityTemplateRepository.GetInTenant(sqls.DB(), req.TemplateID, tenantID)
	if template == nil || template.Status != enums.StatusOk {
		return nil, errorsx.InvalidParam("质检模板不存在或已停用")
	}
	existingInspection := repositories.QualityInspectionRepository.FindOneByAssignment(sqls.DB(), tenantID, assignment.ID, template.ID)
	if req.ID > 0 && (existingInspection == nil || existingInspection.ID != req.ID) {
		return nil, errorsx.InvalidParam("质检记录不存在")
	}
	templateItems := repositories.QualityTemplateItemRepository.FindByTemplate(sqls.DB(), tenantID, template.ID)
	itemByID := make(map[int64]models.QualityTemplateItem, len(templateItems))
	for _, item := range templateItems {
		itemByID[item.ID] = item
	}
	seen := make(map[int64]bool, len(req.Items))
	evaluated := make(map[int64]qualityItemEvaluation, len(req.Items))
	totalScore := 0
	hardFailed := false
	policy := ServiceAnalyticsService.GetPolicy(tenantID)
	for _, input := range req.Items {
		item, ok := itemByID[input.TemplateItemID]
		if !ok || seen[input.TemplateItemID] {
			return nil, errorsx.InvalidParam("质检评分项无效或重复")
		}
		if err := s.validateEvidenceMessages(assignment, input.MessageIDs); err != nil {
			return nil, err
		}
		result := qualityItemEvaluation{Template: item}
		switch item.RuleType {
		case enums.QualityRuleTypeMetric:
			result.Passed, result.MetricValue = s.evaluateQualityMetric(assignment, item.MetricCode, policy)
			if result.Passed {
				result.Score = item.MaxScore
			}
		case enums.QualityRuleTypeProhibited:
			result.Passed = !input.Violated
			result.HardFailed = input.Violated && item.HardFail
			if input.Violated && req.Status == string(enums.QualityInspectionStatusCompleted) && len(input.MessageIDs) == 0 {
				return nil, errorsx.InvalidParam("命中禁忌项时必须选择人工回复证据")
			}
		default:
			if input.Score < 0 || input.Score > item.MaxScore {
				return nil, errorsx.InvalidParam("质检评分超出评分项范围")
			}
			result.Score = input.Score
			result.Passed = input.Score == item.MaxScore
		}
		seen[input.TemplateItemID] = true
		evaluated[input.TemplateItemID] = result
		totalScore += result.Score
		hardFailed = hardFailed || result.HardFailed
	}
	if req.Status == string(enums.QualityInspectionStatusCompleted) {
		for _, item := range templateItems {
			if item.Required && !seen[item.ID] {
				return nil, errorsx.InvalidParam("完成质检前必须填写全部必评项")
			}
		}
	}
	var inspectionID int64
	err := sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		now := time.Now()
		inspection := repositories.QualityInspectionRepository.FindOneByAssignmentForUpdate(ctx.Tx, tenantID, assignment.ID, template.ID)
		if inspection != nil && inspection.Status == enums.QualityInspectionStatusCompleted {
			return errorsx.InvalidParam("已完成质检不可修改，如需复核请使用新的模板版本")
		}
		if req.ID > 0 && (inspection == nil || inspection.ID != req.ID) {
			return errorsx.InvalidParam("质检记录不存在")
		}
		result := qualityResult(totalScore, template.TotalScore, template.PassScore)
		if hardFailed {
			result = enums.QualityInspectionResultFailed
		}
		values := map[string]any{
			"status": enums.QualityInspectionStatus(req.Status), "total_score": totalScore, "max_score": template.TotalScore,
			"hard_failed": hardFailed, "result": result, "summary": strings.TrimSpace(req.Summary),
			"updated_at": now, "update_user_id": operator.UserID, "update_user_name": operator.Username,
		}
		if req.Status == string(enums.QualityInspectionStatusCompleted) {
			values["inspected_by"] = operator.UserID
			values["inspected_at"] = now
		}
		if inspection == nil {
			inspection = &models.QualityInspection{
				TenantID: tenantID, ConversationID: assignment.ConversationID, SessionNo: assignment.SessionNo, AssignmentID: assignment.ID,
				AgentID: assignment.ToUserID, TeamID: assignmentTeamID, TemplateID: template.ID, Status: enums.QualityInspectionStatus(req.Status),
				TotalScore: totalScore, MaxScore: template.TotalScore, HardFailed: hardFailed,
				Result: result, Summary: strings.TrimSpace(req.Summary), AuditFields: utils.BuildAuditFields(operator),
			}
			if req.Status == string(enums.QualityInspectionStatusCompleted) {
				inspection.InspectedBy = operator.UserID
				inspection.InspectedAt = &now
			}
			if err := repositories.QualityInspectionRepository.Create(ctx.Tx, inspection); err != nil {
				return err
			}
		} else {
			updated, err := repositories.QualityInspectionRepository.UpdatesMutableInTenant(ctx.Tx, inspection.ID, tenantID, values)
			if err != nil {
				return err
			}
			if !updated {
				return errorsx.InvalidParam("已完成质检不可修改，如需复核请使用新的模板版本")
			}
		}
		inspectionID = inspection.ID
		if err := repositories.QualityInspectionItemRepository.DeleteByInspection(ctx.Tx, tenantID, inspection.ID); err != nil {
			return err
		}
		for _, input := range req.Items {
			itemResult := evaluated[input.TemplateItemID]
			templateItem := itemResult.Template
			messageIDs, _ := json.Marshal(input.MessageIDs)
			item := &models.QualityInspectionItem{
				TenantID: tenantID, InspectionID: inspection.ID, TemplateItemID: templateItem.ID, ItemCode: templateItem.Code, ItemName: templateItem.Name,
				RuleType: templateItem.RuleType, MaxScore: templateItem.MaxScore, Score: itemResult.Score,
				Passed: itemResult.Passed, HardFailed: itemResult.HardFailed, MetricValue: itemResult.MetricValue,
				Evidence:       strings.TrimSpace(input.Evidence),
				MessageIDsJSON: string(messageIDs), Comment: strings.TrimSpace(input.Comment), AuditFields: utils.BuildAuditFields(operator),
			}
			if err := repositories.QualityInspectionItemRepository.Create(ctx.Tx, item); err != nil {
				return err
			}
		}
		if err := s.attachInspectionToSamplingBatches(ctx.Tx, tenantID, assignment.ID, inspection.ID, now); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return s.GetInspection(inspectionID, operator)
}

func (s *qualityInspectionService) EnsureDefaultTemplate(tenantID int64) (*QualityTemplateAggregate, error) {
	if tenantID <= 0 {
		return nil, errorsx.InvalidParam("接入公司不存在")
	}
	if existing := repositories.QualityTemplateRepository.FindDefault(sqls.DB(), tenantID); existing != nil {
		return &QualityTemplateAggregate{Template: *existing, Items: repositories.QualityTemplateItemRepository.FindByTemplate(sqls.DB(), tenantID, existing.ID)}, nil
	}
	var templateID int64
	err := sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		if existing := repositories.QualityTemplateRepository.FindDefault(ctx.Tx, tenantID); existing != nil {
			templateID = existing.ID
			return nil
		}
		template := &models.QualityTemplate{
			TenantID: tenantID, Name: "人工回复基础质检", Description: "仅评价人工客服在接待分段内发送的回复，AI与客户消息只作为上下文。",
			TotalScore: 100, PassScore: 80, Version: 1, IsDefault: true, Status: enums.StatusOk, AuditFields: utils.BuildAuditFields(nil),
		}
		if err := repositories.QualityTemplateRepository.Create(ctx.Tx, template); err != nil {
			return err
		}
		templateID = template.ID
		defaults := []models.QualityTemplateItem{
			{Code: "courtesy", Name: "服务礼貌", Description: "称呼、语气和服务态度符合规范", RuleType: enums.QualityRuleTypeScore, MaxScore: 20, Required: true, SortNo: 10},
			{Code: "understanding", Name: "需求理解", Description: "准确理解客户问题，没有答非所问", RuleType: enums.QualityRuleTypeScore, MaxScore: 25, Required: true, SortNo: 20},
			{Code: "accuracy", Name: "信息准确", Description: "回复信息真实、准确且没有不当承诺", RuleType: enums.QualityRuleTypeScore, MaxScore: 25, Required: true, SortNo: 30},
			{Code: "resolution", Name: "解决推进", Description: "给出清晰下一步并推动问题处理", RuleType: enums.QualityRuleTypeScore, MaxScore: 20, Required: true, SortNo: 40},
			{Code: "compliance", Name: "合规安全", Description: "不泄露隐私，不越权承诺退款赔付", RuleType: enums.QualityRuleTypeScore, MaxScore: 10, Required: true, SortNo: 50},
			{Code: "prohibited_privacy", Name: "隐私或安全禁忌", Description: "泄露客户隐私、索要敏感凭证或提供危险指引时一票否决", RuleType: enums.QualityRuleTypeProhibited, MaxScore: 0, Required: true, HardFail: true, SortNo: 60},
		}
		for i := range defaults {
			defaults[i].TenantID = tenantID
			defaults[i].TemplateID = template.ID
			defaults[i].Status = enums.StatusOk
			defaults[i].AuditFields = utils.BuildAuditFields(nil)
			if err := repositories.QualityTemplateItemRepository.Create(ctx.Tx, &defaults[i]); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	template := repositories.QualityTemplateRepository.GetInTenant(sqls.DB(), templateID, tenantID)
	if template == nil {
		return nil, errorsx.InvalidParam("默认质检模板创建失败")
	}
	return &QualityTemplateAggregate{Template: *template, Items: repositories.QualityTemplateItemRepository.FindByTemplate(sqls.DB(), tenantID, templateID)}, nil
}

func isSupportedQualityMetric(metricCode string) bool {
	switch strings.TrimSpace(metricCode) {
	case "first_response_sla", "response_sla":
		return true
	default:
		return false
	}
}

func (s *qualityInspectionService) evaluateQualityMetric(assignment *models.ConversationAssignment, metricCode string, policy models.ServiceAnalyticsPolicy) (bool, string) {
	if assignment == nil {
		return false, "无接待分段"
	}
	session := repositories.ConversationServiceSessionRepository.TakeByKey(sqls.DB(), assignment.TenantID, assignment.ConversationID, normalizedSessionNo(assignment.SessionNo))
	switch strings.TrimSpace(metricCode) {
	case "first_response_sla":
		if session == nil || session.FirstHumanReplyAt == nil {
			return false, "未产生人工首响"
		}
		return session.FirstResponseSeconds <= int64(policy.FirstResponseTargetSeconds), fmt.Sprintf("%d秒", session.FirstResponseSeconds)
	case "response_sla":
		spans := repositories.ConversationResponseSpanRepository.Find(sqls.DB(), sqls.NewCnd().
			Eq("tenant_id", assignment.TenantID).Eq("assignment_id", assignment.ID).
			Eq("status", enums.ResponseSpanStatusReplied))
		if len(spans) == 0 {
			return false, "无可计算响应分段"
		}
		var total int64
		for _, span := range spans {
			total += span.WaitSeconds
		}
		average := total / int64(len(spans))
		return average <= int64(policy.ResponseTargetSeconds), fmt.Sprintf("平均%d秒", average)
	default:
		return false, "不支持的系统指标"
	}
}

func (s *qualityInspectionService) attachInspectionToSamplingBatches(db *gorm.DB, tenantID, assignmentID, inspectionID int64, now time.Time) error {
	var batchIDs []int64
	if err := db.Model(&models.QualitySamplingItem{}).
		Where("tenant_id = ? AND assignment_id = ?", tenantID, assignmentID).
		Distinct().Pluck("batch_id", &batchIDs).Error; err != nil {
		return err
	}
	if len(batchIDs) == 0 {
		return nil
	}
	if err := db.Model(&models.QualitySamplingItem{}).
		Where("tenant_id = ? AND assignment_id = ?", tenantID, assignmentID).
		Updates(map[string]any{"inspection_id": inspectionID, "updated_at": now}).Error; err != nil {
		return err
	}
	for _, batchID := range batchIDs {
		var total, completed int64
		if err := db.Model(&models.QualitySamplingItem{}).Where("tenant_id = ? AND batch_id = ?", tenantID, batchID).Count(&total).Error; err != nil {
			return err
		}
		if err := db.Model(&models.QualitySamplingItem{}).Where("tenant_id = ? AND batch_id = ? AND inspection_id > 0", tenantID, batchID).Count(&completed).Error; err != nil {
			return err
		}
		if total > 0 && total == completed {
			if err := repositories.QualitySamplingBatchRepository.UpdatesInTenant(db, batchID, tenantID, map[string]any{
				"status": enums.QualitySamplingStatusCompleted, "completed_at": now, "updated_at": now,
			}); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *qualityInspectionService) applyAssignmentScope(cnd *sqls.Cnd, operator *dto.AuthPrincipal) *sqls.Cnd {
	if AgentTeamScopeService.IsAdmin(operator) {
		return cnd
	}
	if slices.Contains(operator.Roles, constants.RoleCodeCsUser) && !slices.Contains(operator.Roles, constants.RoleCodeCsTeamLeader) {
		return cnd.Eq("to_user_id", operator.UserID)
	}
	if slices.Contains(operator.Roles, constants.RoleCodeCsTeamLeader) {
		teams := repositories.AgentTeamRepository.Find(sqls.DB(), sqls.NewCnd().Eq("tenant_id", operator.ActiveTenantID).Eq("leader_user_id", operator.UserID).Where("status <> ?", enums.StatusDeleted))
		teamIDs := make([]int64, 0, len(teams))
		for _, team := range teams {
			teamIDs = append(teamIDs, team.ID)
		}
		if len(teamIDs) == 0 {
			return cnd.Eq("id", -1)
		}
		return s.applyAssignmentTeamScope(cnd, operator.ActiveTenantID, teamIDs)
	}
	return cnd.Eq("id", -1)
}

func (s *qualityInspectionService) applyAssignmentTeamScope(cnd *sqls.Cnd, tenantID int64, teamIDs []int64) *sqls.Cnd {
	if len(teamIDs) == 0 {
		return cnd.Eq("id", -1)
	}
	squadIDs, userIDs := assignmentTeamScopeIDs(tenantID, teamIDs)
	switch {
	case len(squadIDs) > 0 && len(userIDs) > 0:
		return cnd.Where("(squad_id IN (?) OR (squad_id = 0 AND to_user_id IN (?)))", squadIDs, userIDs)
	case len(squadIDs) > 0:
		return cnd.In("squad_id", squadIDs)
	case len(userIDs) > 0:
		return cnd.Eq("squad_id", 0).In("to_user_id", userIDs)
	default:
		return cnd.Eq("id", -1)
	}
}

func assignmentTeamScopeIDs(tenantID int64, teamIDs []int64) ([]int64, []int64) {
	squads := repositories.AgentTeamSquadRepository.Find(sqls.DB(), sqls.NewCnd().Eq("tenant_id", tenantID).In("team_id", teamIDs))
	squadIDs := make([]int64, 0, len(squads))
	for _, squad := range squads {
		squadIDs = append(squadIDs, squad.ID)
	}
	profiles := repositories.AgentProfileRepository.Find(sqls.DB(), sqls.NewCnd().Eq("tenant_id", tenantID).In("team_id", teamIDs).Where("status <> ?", enums.StatusDeleted))
	userIDs := make([]int64, 0, len(profiles))
	for _, profile := range profiles {
		userIDs = append(userIDs, profile.UserID)
	}
	return squadIDs, userIDs
}

func (s *qualityInspectionService) countAssignmentReplies(db *gorm.DB, assignment *models.ConversationAssignment) int64 {
	if assignment == nil {
		return 0
	}
	query := db.Model(&models.Message{}).Where("tenant_id = ? AND conversation_id = ? AND session_no = ? AND sender_type = ? AND sender_id = ? AND recalled_at IS NULL AND created_at >= ?",
		assignment.TenantID, assignment.ConversationID, assignment.SessionNo, enums.IMSenderTypeAgent, assignment.ToUserID, assignment.CreatedAt)
	if assignment.FinishedAt != nil {
		query = query.Where("created_at <= ?", *assignment.FinishedAt)
	}
	var count int64
	query.Count(&count)
	return count
}

func (s *qualityInspectionService) validateEvidenceMessages(assignment *models.ConversationAssignment, messageIDs []int64) error {
	for _, messageID := range messageIDs {
		message := repositories.MessageRepository.GetInTenant(sqls.DB(), messageID, assignment.TenantID)
		if message == nil || message.ConversationID != assignment.ConversationID || message.SessionNo != assignment.SessionNo || message.SenderType != enums.IMSenderTypeAgent || message.SenderID != assignment.ToUserID {
			return errorsx.InvalidParam("质检证据只能引用当前接待分段内该客服的人工回复")
		}
		at := message.CreatedAt
		if at.Before(assignment.CreatedAt) || (assignment.FinishedAt != nil && at.After(*assignment.FinishedAt)) {
			return errorsx.InvalidParam("质检证据不属于当前接待分段")
		}
	}
	return nil
}

func (s *qualityInspectionService) buildInspectionAggregate(inspection *models.QualityInspection, tenantID int64) *QualityInspectionAggregate {
	if inspection == nil {
		return nil
	}
	ret := &QualityInspectionAggregate{
		Inspection: *inspection,
		Items:      repositories.QualityInspectionItemRepository.FindByInspection(sqls.DB(), tenantID, inspection.ID),
		Agent:      repositories.UserRepository.GetInTenant(sqls.DB(), inspection.AgentID, tenantID),
		Team:       repositories.AgentTeamRepository.GetInTenant(sqls.DB(), inspection.TeamID, tenantID),
	}
	return ret
}

func qualityResult(score, maxScore, passScore int) enums.QualityInspectionResult {
	if maxScore > 0 && score*100 >= maxScore*90 {
		return enums.QualityInspectionResultExcellent
	}
	if score >= passScore {
		return enums.QualityInspectionResultPassed
	}
	return enums.QualityInspectionResultFailed
}
