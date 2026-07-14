package services

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"agent-desk/internal/ai/rag"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/errorsx"
	"agent-desk/internal/pkg/utils"
	"agent-desk/internal/repositories"

	"agent-desk/internal/pkg/httpx/params"

	"github.com/mlogclub/simple/sqls"
)

var KnowledgeFAQService = newKnowledgeFAQService()

func newKnowledgeFAQService() *knowledgeFAQService {
	return &knowledgeFAQService{}
}

type knowledgeFAQService struct{}

func (s *knowledgeFAQService) Get(id int64) *models.KnowledgeFAQ {
	return repositories.KnowledgeFAQRepository.Get(sqls.DB(), id)
}

func (s *knowledgeFAQService) GetForOperator(id int64, operator *dto.AuthPrincipal) *models.KnowledgeFAQ {
	tenantID := AgentTeamScopeService.ActiveTenantID(operator)
	if tenantID <= 0 {
		return nil
	}
	item := repositories.KnowledgeFAQRepository.GetInTenant(sqls.DB(), id, tenantID)
	if item == nil || !KnowledgeBaseService.CanAccessKnowledgeBase(item.KnowledgeBaseID, operator) {
		return nil
	}
	return item
}

func (s *knowledgeFAQService) FindPageByCnd(cnd *sqls.Cnd) (list []models.KnowledgeFAQ, paging *sqls.Paging) {
	return repositories.KnowledgeFAQRepository.FindPageByCnd(sqls.DB(), cnd)
}

func (s *knowledgeFAQService) FindPageByParams(queryParams *params.QueryParams) (list []models.KnowledgeFAQ, paging *sqls.Paging) {
	return repositories.KnowledgeFAQRepository.FindPageByParams(sqls.DB(), queryParams)
}

func (s *knowledgeFAQService) CreateKnowledgeFAQ(req request.CreateKnowledgeFAQRequest, operator *dto.AuthPrincipal) (*models.KnowledgeFAQ, error) {
	tenantID, err := requireActiveTenantID(operator, "FAQ")
	if err != nil {
		return nil, err
	}
	kb, err := s.requireFAQKnowledgeBase(req.KnowledgeBaseID, tenantID, operator)
	if err != nil {
		return nil, err
	}
	item, err := s.buildKnowledgeFAQModel(req)
	if err != nil {
		return nil, err
	}
	item.Status = kb.Status
	item.TenantID = tenantID
	item.IndexStatus = enums.KnowledgeDocumentIndexStatusPending
	item.IndexError = ""
	item.IndexedAt = nil
	item.AuditFields = utils.BuildAuditFields(operator)
	if err := repositories.KnowledgeFAQRepository.Create(sqls.DB(), item); err != nil {
		return nil, err
	}
	if err := rag.Index.IndexFAQByID(context.Background(), item.ID); err != nil {
		slog.Error("failed to index created knowledge faq", "faq_id", item.ID, "error", err)
		return item, nil
	}
	return repositories.KnowledgeFAQRepository.GetInTenant(sqls.DB(), item.ID, tenantID), nil
}

func (s *knowledgeFAQService) UpdateKnowledgeFAQ(req request.UpdateKnowledgeFAQRequest, operator *dto.AuthPrincipal) error {
	if operator == nil {
		return errorsx.Unauthorized("未登录或登录已过期")
	}
	tenantID, err := requireActiveTenantID(operator, "FAQ")
	if err != nil {
		return err
	}
	current := repositories.KnowledgeFAQRepository.GetInTenant(sqls.DB(), req.ID, tenantID)
	if current == nil {
		return errorsx.InvalidParam("FAQ不存在")
	}
	if !KnowledgeBaseService.CanAccessKnowledgeBase(current.KnowledgeBaseID, operator) {
		return errorsx.Forbidden("无权限维护该FAQ")
	}
	if _, err := s.requireFAQKnowledgeBase(req.KnowledgeBaseID, tenantID, operator); err != nil {
		return err
	}
	item, err := s.buildKnowledgeFAQModel(req.CreateKnowledgeFAQRequest)
	if err != nil {
		return err
	}
	if err := repositories.KnowledgeFAQRepository.UpdatesInTenant(sqls.DB(), req.ID, tenantID, map[string]any{
		"knowledge_base_id": item.KnowledgeBaseID,
		"question":          item.Question,
		"answer":            item.Answer,
		"similar_questions": item.SimilarQuestions,
		"index_status":      enums.KnowledgeDocumentIndexStatusPending,
		"indexed_at":        nil,
		"index_error":       "",
		"remark":            item.Remark,
		"update_user_id":    operator.UserID,
		"update_user_name":  operator.Username,
		"updated_at":        time.Now(),
	}); err != nil {
		return err
	}
	return rag.Index.IndexFAQByID(context.Background(), req.ID)
}

func (s *knowledgeFAQService) DeleteKnowledgeFAQ(id int64, operator *dto.AuthPrincipal) error {
	tenantID, err := requireActiveTenantID(operator, "FAQ")
	if err != nil {
		return err
	}
	current := repositories.KnowledgeFAQRepository.GetInTenant(sqls.DB(), id, tenantID)
	if current == nil {
		return errorsx.InvalidParam("FAQ不存在")
	}
	if !KnowledgeBaseService.CanAccessKnowledgeBase(current.KnowledgeBaseID, operator) {
		return errorsx.Forbidden("无权限删除该FAQ")
	}
	chunks := repositories.KnowledgeChunkRepository.FindByFaqIDInTenant(sqls.DB(), id, tenantID)
	if err := sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		if err := repositories.KnowledgeFAQRepository.DeleteInTenant(ctx.Tx, id, tenantID); err != nil {
			return err
		}
		return ctx.Tx.Delete(&models.KnowledgeChunk{}, "faq_id = ? AND tenant_id = ?", id, tenantID).Error
	}); err != nil {
		return err
	}
	return rag.Index.RemoveFAQIndexByChunkModels(context.Background(), current.KnowledgeBaseID, id, chunks)
}

func (s *knowledgeFAQService) buildKnowledgeFAQModel(req request.CreateKnowledgeFAQRequest) (*models.KnowledgeFAQ, error) {
	if req.KnowledgeBaseID <= 0 {
		return nil, errorsx.InvalidParam("知识库不存在")
	}
	if req.Question == "" {
		return nil, errorsx.InvalidParam("问题不能为空")
	}
	if req.Answer == "" {
		return nil, errorsx.InvalidParam("答案不能为空")
	}
	similarQuestions, err := json.Marshal(normalizeSimilarQuestions(req.SimilarQuestions))
	if err != nil {
		return nil, errorsx.InvalidParam("相似问格式不合法")
	}
	return &models.KnowledgeFAQ{
		KnowledgeBaseID:  req.KnowledgeBaseID,
		Question:         req.Question,
		Answer:           req.Answer,
		SimilarQuestions: string(similarQuestions),
		Remark:           req.Remark,
	}, nil
}

func (s *knowledgeFAQService) requireFAQKnowledgeBase(knowledgeBaseID, tenantID int64, operator *dto.AuthPrincipal) (*models.KnowledgeBase, error) {
	kb := KnowledgeBaseService.GetInTenant(knowledgeBaseID, tenantID)
	if kb == nil {
		return nil, errorsx.InvalidParam("知识库不存在")
	}
	if kb.KnowledgeType != "faq" {
		return nil, errorsx.InvalidParam("当前知识库不是FAQ知识库")
	}
	if !KnowledgeBaseService.CanAccessKnowledgeBase(kb.ID, operator) {
		return nil, errorsx.Forbidden("无权限维护该知识库")
	}
	return kb, nil
}

func normalizeSimilarQuestions(values []string) []string {
	items := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, item := range values {
		value := item
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		items = append(items, value)
	}
	return items
}
