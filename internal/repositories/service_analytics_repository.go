package repositories

import (
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/httpx/params"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ConversationServiceSessionRepository = &conversationServiceSessionRepository{}

type conversationServiceSessionRepository struct{}

func (r *conversationServiceSessionRepository) GetInTenant(db *gorm.DB, id, tenantID int64) *models.ConversationServiceSession {
	ret := &models.ConversationServiceSession{}
	if id <= 0 || tenantID <= 0 || db.Where("id = ? AND tenant_id = ?", id, tenantID).Take(ret).Error != nil {
		return nil
	}
	return ret
}

func (r *conversationServiceSessionRepository) TakeByKey(db *gorm.DB, tenantID, conversationID int64, sessionNo int) *models.ConversationServiceSession {
	ret := &models.ConversationServiceSession{}
	if db.Where("tenant_id = ? AND conversation_id = ? AND session_no = ?", tenantID, conversationID, sessionNo).Take(ret).Error != nil {
		return nil
	}
	return ret
}

func (r *conversationServiceSessionRepository) Create(db *gorm.DB, item *models.ConversationServiceSession) error {
	return db.Create(item).Error
}

func (r *conversationServiceSessionRepository) UpdatesInTenant(db *gorm.DB, id, tenantID int64, values map[string]any) error {
	return db.Model(&models.ConversationServiceSession{}).Where("id = ? AND tenant_id = ?", id, tenantID).Updates(values).Error
}

func (r *conversationServiceSessionRepository) Find(db *gorm.DB, cnd *sqls.Cnd) (list []models.ConversationServiceSession) {
	cnd.Find(db, &list)
	return
}

func (r *conversationServiceSessionRepository) FindPageByParams(db *gorm.DB, query *params.QueryParams) (list []models.ConversationServiceSession, paging *sqls.Paging) {
	query.Cnd.Find(db, &list)
	paging = &sqls.Paging{Page: query.Cnd.Paging.Page, Limit: query.Cnd.Paging.Limit, Total: query.Cnd.Count(db, &models.ConversationServiceSession{})}
	return
}

func (r *conversationServiceSessionRepository) Count(db *gorm.DB, cnd *sqls.Cnd) int64 {
	return cnd.Count(db, &models.ConversationServiceSession{})
}

var ConversationResponseSpanRepository = &conversationResponseSpanRepository{}

type conversationResponseSpanRepository struct{}

func (r *conversationResponseSpanRepository) FindWaiting(db *gorm.DB, tenantID, conversationID int64, sessionNo int) []models.ConversationResponseSpan {
	var list []models.ConversationResponseSpan
	db.Where("tenant_id = ? AND conversation_id = ? AND session_no = ? AND status = ?", tenantID, conversationID, sessionNo, enums.ResponseSpanStatusWaiting).
		Order("started_at ASC, id ASC").Find(&list)
	return list
}

func (r *conversationResponseSpanRepository) FindLastWaiting(db *gorm.DB, tenantID, conversationID int64, sessionNo int) *models.ConversationResponseSpan {
	ret := &models.ConversationResponseSpan{}
	if db.Where("tenant_id = ? AND conversation_id = ? AND session_no = ? AND status = ?", tenantID, conversationID, sessionNo, enums.ResponseSpanStatusWaiting).
		Order("id DESC").Take(ret).Error != nil {
		return nil
	}
	return ret
}

func (r *conversationResponseSpanRepository) Create(db *gorm.DB, item *models.ConversationResponseSpan) error {
	return db.Create(item).Error
}

func (r *conversationResponseSpanRepository) UpdatesInTenant(db *gorm.DB, id, tenantID int64, values map[string]any) error {
	return db.Model(&models.ConversationResponseSpan{}).Where("id = ? AND tenant_id = ?", id, tenantID).Updates(values).Error
}

func (r *conversationResponseSpanRepository) Find(db *gorm.DB, cnd *sqls.Cnd) (list []models.ConversationResponseSpan) {
	cnd.Find(db, &list)
	return
}

func (r *conversationResponseSpanRepository) Count(db *gorm.DB, cnd *sqls.Cnd) int64 {
	return cnd.Count(db, &models.ConversationResponseSpan{})
}

var AgentPresenceSessionRepository = &agentPresenceSessionRepository{}

type agentPresenceSessionRepository struct{}

func (r *agentPresenceSessionRepository) FindActive(db *gorm.DB, tenantID, userID int64) *models.AgentPresenceSession {
	ret := &models.AgentPresenceSession{}
	if db.Where("tenant_id = ? AND user_id = ? AND ended_at IS NULL", tenantID, userID).Order("id DESC").Take(ret).Error != nil {
		return nil
	}
	return ret
}

func (r *agentPresenceSessionRepository) Create(db *gorm.DB, item *models.AgentPresenceSession) error {
	return db.Create(item).Error
}

func (r *agentPresenceSessionRepository) UpdatesInTenant(db *gorm.DB, id, tenantID int64, values map[string]any) error {
	return db.Model(&models.AgentPresenceSession{}).Where("id = ? AND tenant_id = ?", id, tenantID).Updates(values).Error
}

func (r *agentPresenceSessionRepository) Find(db *gorm.DB, cnd *sqls.Cnd) (list []models.AgentPresenceSession) {
	cnd.Find(db, &list)
	return
}

var QualityTemplateRepository = &qualityTemplateRepository{}

type qualityTemplateRepository struct{}

func (r *qualityTemplateRepository) GetInTenant(db *gorm.DB, id, tenantID int64) *models.QualityTemplate {
	ret := &models.QualityTemplate{}
	if db.Where("id = ? AND tenant_id = ?", id, tenantID).Take(ret).Error != nil {
		return nil
	}
	return ret
}

func (r *qualityTemplateRepository) FindDefault(db *gorm.DB, tenantID int64) *models.QualityTemplate {
	ret := &models.QualityTemplate{}
	if db.Where("tenant_id = ? AND is_default = ? AND status = ?", tenantID, true, enums.StatusOk).Order("id DESC").Take(ret).Error != nil {
		return nil
	}
	return ret
}

func (r *qualityTemplateRepository) Find(db *gorm.DB, cnd *sqls.Cnd) (list []models.QualityTemplate) {
	cnd.Find(db, &list)
	return
}

func (r *qualityTemplateRepository) Create(db *gorm.DB, item *models.QualityTemplate) error {
	return db.Create(item).Error
}

func (r *qualityTemplateRepository) UpdatesInTenant(db *gorm.DB, id, tenantID int64, values map[string]any) error {
	return db.Model(&models.QualityTemplate{}).Where("id = ? AND tenant_id = ?", id, tenantID).Updates(values).Error
}

var QualityTemplateItemRepository = &qualityTemplateItemRepository{}

type qualityTemplateItemRepository struct{}

func (r *qualityTemplateItemRepository) FindByTemplate(db *gorm.DB, tenantID, templateID int64) []models.QualityTemplateItem {
	var list []models.QualityTemplateItem
	db.Where("tenant_id = ? AND template_id = ? AND status = ?", tenantID, templateID, enums.StatusOk).Order("sort_no ASC, id ASC").Find(&list)
	return list
}

func (r *qualityTemplateItemRepository) Create(db *gorm.DB, item *models.QualityTemplateItem) error {
	return db.Create(item).Error
}

func (r *qualityTemplateItemRepository) DeleteByTemplate(db *gorm.DB, tenantID, templateID int64) error {
	return db.Where("tenant_id = ? AND template_id = ?", tenantID, templateID).Delete(&models.QualityTemplateItem{}).Error
}

var QualityInspectionRepository = &qualityInspectionRepository{}

type qualityInspectionRepository struct{}

func (r *qualityInspectionRepository) GetInTenant(db *gorm.DB, id, tenantID int64) *models.QualityInspection {
	ret := &models.QualityInspection{}
	if db.Where("id = ? AND tenant_id = ?", id, tenantID).Take(ret).Error != nil {
		return nil
	}
	return ret
}

func (r *qualityInspectionRepository) FindOneByAssignment(db *gorm.DB, tenantID, assignmentID, templateID int64) *models.QualityInspection {
	ret := &models.QualityInspection{}
	if db.Where("tenant_id = ? AND assignment_id = ? AND template_id = ?", tenantID, assignmentID, templateID).Take(ret).Error != nil {
		return nil
	}
	return ret
}

func (r *qualityInspectionRepository) FindOneByAssignmentForUpdate(db *gorm.DB, tenantID, assignmentID, templateID int64) *models.QualityInspection {
	ret := &models.QualityInspection{}
	if db.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("tenant_id = ? AND assignment_id = ? AND template_id = ?", tenantID, assignmentID, templateID).
		Take(ret).Error != nil {
		return nil
	}
	return ret
}

func (r *qualityInspectionRepository) FindPageByParams(db *gorm.DB, query *params.QueryParams) (list []models.QualityInspection, paging *sqls.Paging) {
	query.Cnd.Find(db, &list)
	paging = &sqls.Paging{Page: query.Cnd.Paging.Page, Limit: query.Cnd.Paging.Limit, Total: query.Cnd.Count(db, &models.QualityInspection{})}
	return
}

func (r *qualityInspectionRepository) Find(db *gorm.DB, cnd *sqls.Cnd) (list []models.QualityInspection) {
	cnd.Find(db, &list)
	return
}

func (r *qualityInspectionRepository) Create(db *gorm.DB, item *models.QualityInspection) error {
	return db.Create(item).Error
}

func (r *qualityInspectionRepository) UpdatesInTenant(db *gorm.DB, id, tenantID int64, values map[string]any) error {
	return db.Model(&models.QualityInspection{}).Where("id = ? AND tenant_id = ?", id, tenantID).Updates(values).Error
}

func (r *qualityInspectionRepository) UpdatesMutableInTenant(db *gorm.DB, id, tenantID int64, values map[string]any) (bool, error) {
	result := db.Model(&models.QualityInspection{}).
		Where("id = ? AND tenant_id = ? AND status <> ?", id, tenantID, enums.QualityInspectionStatusCompleted).
		Updates(values)
	return result.RowsAffected == 1, result.Error
}

var QualityInspectionItemRepository = &qualityInspectionItemRepository{}

type qualityInspectionItemRepository struct{}

func (r *qualityInspectionItemRepository) FindByInspection(db *gorm.DB, tenantID, inspectionID int64) []models.QualityInspectionItem {
	var list []models.QualityInspectionItem
	db.Where("tenant_id = ? AND inspection_id = ?", tenantID, inspectionID).Order("id ASC").Find(&list)
	return list
}

func (r *qualityInspectionItemRepository) DeleteByInspection(db *gorm.DB, tenantID, inspectionID int64) error {
	return db.Where("tenant_id = ? AND inspection_id = ?", tenantID, inspectionID).Delete(&models.QualityInspectionItem{}).Error
}

func (r *qualityInspectionItemRepository) Create(db *gorm.DB, item *models.QualityInspectionItem) error {
	return db.Create(item).Error
}

var DispatchDecisionLogRepository = &dispatchDecisionLogRepository{}

type dispatchDecisionLogRepository struct{}

func (r *dispatchDecisionLogRepository) TakeByAssignment(db *gorm.DB, tenantID, assignmentID int64) *models.DispatchDecisionLog {
	ret := &models.DispatchDecisionLog{}
	if tenantID <= 0 || assignmentID <= 0 || db.Where("tenant_id = ? AND assignment_id = ?", tenantID, assignmentID).Take(ret).Error != nil {
		return nil
	}
	return ret
}

func (r *dispatchDecisionLogRepository) Create(db *gorm.DB, item *models.DispatchDecisionLog) error {
	return db.Create(item).Error
}

func (r *dispatchDecisionLogRepository) Find(db *gorm.DB, cnd *sqls.Cnd) (list []models.DispatchDecisionLog) {
	cnd.Find(db, &list)
	return
}

var ServiceAnalyticsPolicyRepository = &serviceAnalyticsPolicyRepository{}

type serviceAnalyticsPolicyRepository struct{}

func (r *serviceAnalyticsPolicyRepository) TakeByTenant(db *gorm.DB, tenantID int64) *models.ServiceAnalyticsPolicy {
	ret := &models.ServiceAnalyticsPolicy{}
	if db.Where("tenant_id = ?", tenantID).Take(ret).Error != nil {
		return nil
	}
	return ret
}

func (r *serviceAnalyticsPolicyRepository) Create(db *gorm.DB, item *models.ServiceAnalyticsPolicy) error {
	return db.Create(item).Error
}

func (r *serviceAnalyticsPolicyRepository) UpdatesInTenant(db *gorm.DB, id, tenantID int64, values map[string]any) error {
	return db.Model(&models.ServiceAnalyticsPolicy{}).Where("id = ? AND tenant_id = ?", id, tenantID).Updates(values).Error
}

var QualitySamplingBatchRepository = &qualitySamplingBatchRepository{}

type qualitySamplingBatchRepository struct{}

func (r *qualitySamplingBatchRepository) GetInTenant(db *gorm.DB, id, tenantID int64) *models.QualitySamplingBatch {
	ret := &models.QualitySamplingBatch{}
	if id <= 0 || tenantID <= 0 || db.Where("id = ? AND tenant_id = ?", id, tenantID).Take(ret).Error != nil {
		return nil
	}
	return ret
}

func (r *qualitySamplingBatchRepository) Create(db *gorm.DB, item *models.QualitySamplingBatch) error {
	return db.Create(item).Error
}

func (r *qualitySamplingBatchRepository) UpdatesInTenant(db *gorm.DB, id, tenantID int64, values map[string]any) error {
	return db.Model(&models.QualitySamplingBatch{}).Where("id = ? AND tenant_id = ?", id, tenantID).Updates(values).Error
}

func (r *qualitySamplingBatchRepository) FindPageByParams(db *gorm.DB, query *params.QueryParams) (list []models.QualitySamplingBatch, paging *sqls.Paging) {
	query.Cnd.Find(db, &list)
	paging = &sqls.Paging{Page: query.Cnd.Paging.Page, Limit: query.Cnd.Paging.Limit, Total: query.Cnd.Count(db, &models.QualitySamplingBatch{})}
	return
}

func (r *qualitySamplingBatchRepository) FindPageByCnd(db *gorm.DB, cnd *sqls.Cnd) (list []models.QualitySamplingBatch, paging *sqls.Paging) {
	cnd.Find(db, &list)
	paging = &sqls.Paging{Page: cnd.Paging.Page, Limit: cnd.Paging.Limit, Total: cnd.Count(db, &models.QualitySamplingBatch{})}
	return
}

var QualitySamplingItemRepository = &qualitySamplingItemRepository{}

type qualitySamplingItemRepository struct{}

func (r *qualitySamplingItemRepository) Create(db *gorm.DB, item *models.QualitySamplingItem) error {
	return db.Create(item).Error
}

func (r *qualitySamplingItemRepository) FindByBatch(db *gorm.DB, tenantID, batchID int64) []models.QualitySamplingItem {
	var list []models.QualitySamplingItem
	db.Where("tenant_id = ? AND batch_id = ?", tenantID, batchID).Order("id ASC").Find(&list)
	return list
}

func (r *qualitySamplingItemRepository) UpdateInspection(db *gorm.DB, tenantID, batchID, assignmentID, inspectionID int64) error {
	return db.Model(&models.QualitySamplingItem{}).
		Where("tenant_id = ? AND batch_id = ? AND assignment_id = ?", tenantID, batchID, assignmentID).
		Update("inspection_id", inspectionID).Error
}

var ConversationEvaluationRepository = &conversationEvaluationRepository{}

type conversationEvaluationRepository struct{}

func (r *conversationEvaluationRepository) GetInTenant(db *gorm.DB, id, tenantID int64) *models.ConversationEvaluation {
	ret := &models.ConversationEvaluation{}
	if id <= 0 || tenantID <= 0 || db.Where("id = ? AND tenant_id = ?", id, tenantID).Take(ret).Error != nil {
		return nil
	}
	return ret
}

func (r *conversationEvaluationRepository) TakeByTokenHash(db *gorm.DB, tokenHash string) *models.ConversationEvaluation {
	ret := &models.ConversationEvaluation{}
	if tokenHash == "" || db.Where("token_hash = ?", tokenHash).Take(ret).Error != nil {
		return nil
	}
	return ret
}

func (r *conversationEvaluationRepository) TakePendingBySession(db *gorm.DB, tenantID, conversationID int64, sessionNo int) *models.ConversationEvaluation {
	ret := &models.ConversationEvaluation{}
	if db.Where("tenant_id = ? AND conversation_id = ? AND session_no = ? AND status = ?", tenantID, conversationID, sessionNo, enums.ConversationEvaluationStatusPending).
		Order("id DESC").Take(ret).Error != nil {
		return nil
	}
	return ret
}

func (r *conversationEvaluationRepository) Create(db *gorm.DB, item *models.ConversationEvaluation) error {
	return db.Create(item).Error
}

func (r *conversationEvaluationRepository) UpdatesInTenant(db *gorm.DB, id, tenantID int64, values map[string]any) error {
	return db.Model(&models.ConversationEvaluation{}).Where("id = ? AND tenant_id = ?", id, tenantID).Updates(values).Error
}

func (r *conversationEvaluationRepository) FindPageByParams(db *gorm.DB, query *params.QueryParams) (list []models.ConversationEvaluation, paging *sqls.Paging) {
	query.Cnd.Find(db, &list)
	paging = &sqls.Paging{Page: query.Cnd.Paging.Page, Limit: query.Cnd.Paging.Limit, Total: query.Cnd.Count(db, &models.ConversationEvaluation{})}
	return
}

func (r *conversationEvaluationRepository) FindPageByCnd(db *gorm.DB, cnd *sqls.Cnd) (list []models.ConversationEvaluation, paging *sqls.Paging) {
	cnd.Find(db, &list)
	paging = &sqls.Paging{Page: cnd.Paging.Page, Limit: cnd.Paging.Limit, Total: cnd.Count(db, &models.ConversationEvaluation{})}
	return
}

func (r *conversationEvaluationRepository) Find(db *gorm.DB, cnd *sqls.Cnd) (list []models.ConversationEvaluation) {
	cnd.Find(db, &list)
	return
}

var ReportViewPresetRepository = &reportViewPresetRepository{}

type reportViewPresetRepository struct{}

func (r *reportViewPresetRepository) GetOwned(db *gorm.DB, id, tenantID, userID int64) *models.ReportViewPreset {
	ret := &models.ReportViewPreset{}
	if id <= 0 || tenantID <= 0 || userID <= 0 || db.Where("id = ? AND tenant_id = ? AND user_id = ?", id, tenantID, userID).Take(ret).Error != nil {
		return nil
	}
	return ret
}

func (r *reportViewPresetRepository) FindOwned(db *gorm.DB, tenantID, userID int64, pageCode string) []models.ReportViewPreset {
	var list []models.ReportViewPreset
	db.Where("tenant_id = ? AND user_id = ? AND page_code = ? AND status = ?", tenantID, userID, pageCode, enums.StatusOk).
		Order("is_default DESC, id ASC").Find(&list)
	return list
}

func (r *reportViewPresetRepository) Create(db *gorm.DB, item *models.ReportViewPreset) error {
	return db.Create(item).Error
}

func (r *reportViewPresetRepository) UpdatesOwned(db *gorm.DB, id, tenantID, userID int64, values map[string]any) error {
	return db.Model(&models.ReportViewPreset{}).Where("id = ? AND tenant_id = ? AND user_id = ?", id, tenantID, userID).Updates(values).Error
}

func (r *reportViewPresetRepository) ClearDefault(db *gorm.DB, tenantID, userID int64, pageCode string) error {
	return db.Model(&models.ReportViewPreset{}).
		Where("tenant_id = ? AND user_id = ? AND page_code = ?", tenantID, userID, pageCode).
		Update("is_default", false).Error
}
