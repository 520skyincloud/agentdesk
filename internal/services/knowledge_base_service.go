package services

import (
	"encoding/json"
	"strings"
	"time"

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

var KnowledgeBaseService = newKnowledgeBaseService()

func newKnowledgeBaseService() *knowledgeBaseService {
	return &knowledgeBaseService{}
}

type knowledgeBaseService struct {
}

func (s *knowledgeBaseService) Get(id int64) *models.KnowledgeBase {
	return repositories.KnowledgeBaseRepository.Get(sqls.DB(), id)
}

func (s *knowledgeBaseService) GetInTenant(id, tenantID int64) *models.KnowledgeBase {
	return repositories.KnowledgeBaseRepository.GetInTenant(sqls.DB(), id, tenantID)
}

func (s *knowledgeBaseService) GetForOperator(id int64, operator *dto.AuthPrincipal) *models.KnowledgeBase {
	tenantID := AgentTeamScopeService.ActiveTenantID(operator)
	if tenantID <= 0 {
		return nil
	}
	item := repositories.KnowledgeBaseRepository.GetInTenant(sqls.DB(), id, tenantID)
	if item == nil || !s.CanAccessKnowledgeBase(item.ID, operator) {
		return nil
	}
	return item
}

func (s *knowledgeBaseService) Take(where ...interface{}) *models.KnowledgeBase {
	return repositories.KnowledgeBaseRepository.Take(sqls.DB(), where...)
}

func (s *knowledgeBaseService) Find(cnd *sqls.Cnd) []models.KnowledgeBase {
	return repositories.KnowledgeBaseRepository.Find(sqls.DB(), cnd)
}

func (s *knowledgeBaseService) FindOne(cnd *sqls.Cnd) *models.KnowledgeBase {
	return repositories.KnowledgeBaseRepository.FindOne(sqls.DB(), cnd)
}

func (s *knowledgeBaseService) FindPageByParams(params *params.QueryParams) (list []models.KnowledgeBase, paging *sqls.Paging) {
	return repositories.KnowledgeBaseRepository.FindPageByParams(sqls.DB(), params)
}

func (s *knowledgeBaseService) FindPageByCnd(cnd *sqls.Cnd) (list []models.KnowledgeBase, paging *sqls.Paging) {
	return repositories.KnowledgeBaseRepository.FindPageByCnd(sqls.DB(), cnd)
}

func (s *knowledgeBaseService) Count(cnd *sqls.Cnd) int64 {
	return repositories.KnowledgeBaseRepository.Count(sqls.DB(), cnd)
}

func (s *knowledgeBaseService) Create(t *models.KnowledgeBase) error {
	return repositories.KnowledgeBaseRepository.Create(sqls.DB(), t)
}

func (s *knowledgeBaseService) Update(t *models.KnowledgeBase) error {
	return repositories.KnowledgeBaseRepository.Update(sqls.DB(), t)
}

func (s *knowledgeBaseService) Updates(id int64, columns map[string]interface{}) error {
	return repositories.KnowledgeBaseRepository.Updates(sqls.DB(), id, columns)
}

func (s *knowledgeBaseService) UpdateColumn(id int64, name string, value interface{}) error {
	return repositories.KnowledgeBaseRepository.UpdateColumn(sqls.DB(), id, name, value)
}

func (s *knowledgeBaseService) Delete(id int64) {
	repositories.KnowledgeBaseRepository.Delete(sqls.DB(), id)
}

func (s *knowledgeBaseService) CreateKnowledgeBase(req request.CreateKnowledgeBaseRequest, operator *dto.AuthPrincipal) (*models.KnowledgeBase, error) {
	tenantID, err := requireActiveTenantID(operator, "知识库")
	if err != nil {
		return nil, err
	}
	item, err := s.buildKnowledgeBaseModel(req, tenantID)
	if err != nil {
		return nil, err
	}
	if item.KnowledgeType == string(enums.KnowledgeBaseTypeFastGPTCloud) {
		return nil, errorsx.InvalidParam("FastGPT 知识库只能通过门店知识库开通流程创建")
	}
	item.Status = enums.StatusOk
	item.TenantID = tenantID
	item.AuditFields = utils.BuildAuditFields(operator)
	if err := repositories.KnowledgeBaseRepository.Create(sqls.DB(), item); err != nil {
		return nil, err
	}
	return item, nil
}

func (s *knowledgeBaseService) UpdateKnowledgeBase(req request.UpdateKnowledgeBaseRequest, operator *dto.AuthPrincipal) error {
	if operator == nil {
		return errorsx.Unauthorized("未登录或登录已过期")
	}
	tenantID, err := requireActiveTenantID(operator, "知识库")
	if err != nil {
		return err
	}
	current := repositories.KnowledgeBaseRepository.GetInTenant(sqls.DB(), req.ID, tenantID)
	if current == nil {
		return errorsx.InvalidParam("知识库不存在")
	}
	if !s.CanAccessKnowledgeBase(current.ID, operator) {
		return errorsx.Forbidden("无权限维护该知识库")
	}
	currentIsFastGPT := current.KnowledgeType == string(enums.KnowledgeBaseTypeFastGPTCloud)
	if currentIsFastGPT {
		remark, err := buildFastGPTKnowledgeRemark(req.ResourceAllowedHosts)
		if err != nil {
			return err
		}
		req.StoreID = current.StoreID
		req.DatasetID = current.DatasetID
		req.DatasetName = current.DatasetName
		req.ConnectionID = current.ConnectionID
		req.RetrievalMode = current.RetrievalMode
		req.KnowledgeType = current.KnowledgeType
		req.ChunkProvider = current.ChunkProvider
		req.ChunkTargetTokens = current.ChunkTargetTokens
		req.ChunkMaxTokens = current.ChunkMaxTokens
		req.ChunkOverlapTokens = current.ChunkOverlapTokens
		req.Remark = remark
	} else if req.KnowledgeType == string(enums.KnowledgeBaseTypeFastGPTCloud) {
		return errorsx.InvalidParam("普通知识库不能转换为 FastGPT 知识库，请使用门店知识库开通流程")
	}
	item, err := s.buildKnowledgeBaseModel(req.CreateKnowledgeBaseRequest, tenantID)
	if err != nil {
		return err
	}
	return repositories.KnowledgeBaseRepository.UpdatesInTenant(sqls.DB(), req.ID, tenantID, map[string]any{
		"intent_profile_id":       0,
		"company_id":              item.CompanyID,
		"store_id":                item.StoreID,
		"dataset_id":              item.DatasetID,
		"dataset_name":            item.DatasetName,
		"connection_id":           item.ConnectionID,
		"retrieval_mode":          item.RetrievalMode,
		"name":                    item.Name,
		"description":             item.Description,
		"knowledge_type":          item.KnowledgeType,
		"default_top_k":           item.DefaultTopK,
		"default_score_threshold": item.DefaultScoreThreshold,
		"default_rerank_limit":    item.DefaultRerankLimit,
		"chunk_provider":          item.ChunkProvider,
		"chunk_target_tokens":     item.ChunkTargetTokens,
		"chunk_max_tokens":        item.ChunkMaxTokens,
		"chunk_overlap_tokens":    item.ChunkOverlapTokens,
		"answer_mode":             item.AnswerMode,
		"remark":                  item.Remark,
		"update_user_id":          operator.UserID,
		"update_user_name":        operator.Username,
		"updated_at":              time.Now(),
	})
}

func (s *knowledgeBaseService) DeleteKnowledgeBase(id int64, operator *dto.AuthPrincipal) error {
	tenantID, err := requireActiveTenantID(operator, "知识库")
	if err != nil {
		return err
	}
	current := repositories.KnowledgeBaseRepository.GetInTenant(sqls.DB(), id, tenantID)
	if current == nil {
		return errorsx.InvalidParam("知识库不存在")
	}
	if !s.CanAccessKnowledgeBase(current.ID, operator) {
		return errorsx.Forbidden("无权限删除该知识库")
	}
	if current.KnowledgeType == string(enums.KnowledgeBaseTypeFastGPTCloud) {
		return errorsx.InvalidParam("FastGPT 知识库必须在知识库工作区中确认并删除远端数据集")
	}
	docCount := repositories.KnowledgeDocumentRepository.CountByKnowledgeBaseIDInTenant(sqls.DB(), id, tenantID)
	if docCount > 0 {
		return errorsx.InvalidParam("知识库下存在文档，无法删除")
	}
	faqCount := repositories.KnowledgeFAQRepository.CountByKnowledgeBaseIDInTenant(sqls.DB(), id, tenantID)
	if faqCount > 0 {
		return errorsx.InvalidParam("知识库下存在FAQ，无法删除")
	}
	return repositories.KnowledgeBaseRepository.DeleteInTenant(sqls.DB(), id, tenantID)
}

func buildFastGPTKnowledgeRemark(values []string) (string, error) {
	hosts := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		host := strings.ToLower(strings.TrimSpace(value))
		host = strings.TrimPrefix(host, "https://")
		host = strings.TrimPrefix(host, "http://")
		host = strings.TrimSuffix(host, "/")
		if host == "" {
			continue
		}
		if strings.Contains(host, "/") {
			return "", errorsx.InvalidParam("图片可信域名只能填写域名，不能包含路径")
		}
		if !seen[host] {
			seen[host] = true
			hosts = append(hosts, host)
		}
	}
	if len(hosts) == 0 {
		return "", nil
	}
	payload, err := json.Marshal(struct {
		ResourceAllowedHosts []string `json:"resourceAllowedHosts"`
	}{ResourceAllowedHosts: hosts})
	if err != nil {
		return "", err
	}
	return string(payload), nil
}

func (s *knowledgeBaseService) CanAccessKnowledgeBase(id int64, operator *dto.AuthPrincipal) bool {
	if id <= 0 || operator == nil {
		return false
	}
	tenantID := AgentTeamScopeService.ActiveTenantID(operator)
	if tenantID <= 0 || repositories.KnowledgeBaseRepository.GetInTenant(sqls.DB(), id, tenantID) == nil {
		return false
	}
	return s.canAccessKnowledgeBaseScope(id, operator)
}

func (s *knowledgeBaseService) canAccessKnowledgeBaseScope(id int64, operator *dto.AuthPrincipal) bool {
	return knowledgeBaseIDInScope(id, AgentTeamScopeService.Resolve(operator))
}

func knowledgeBaseIDInScope(id int64, scope ManagedDataScope) bool {
	if scope.Unrestricted {
		return true
	}
	for _, allowedID := range scope.KnowledgeBaseIDs {
		if allowedID == id {
			return true
		}
	}
	return false
}

func (s *knowledgeBaseService) UpdateSort(ids []int64, operator *dto.AuthPrincipal) error {
	tenantID, err := requireActiveTenantID(operator, "知识库")
	if err != nil {
		return err
	}
	scope := AgentTeamScopeService.Resolve(operator)
	return sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		for i, id := range ids {
			if repositories.KnowledgeBaseRepository.GetInTenant(ctx.Tx, id, tenantID) == nil || !knowledgeBaseIDInScope(id, scope) {
				return errorsx.Forbidden("无权限调整该知识库排序")
			}
			if err := repositories.KnowledgeBaseRepository.UpdateColumnInTenant(ctx.Tx, id, tenantID, "sort_no", i); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *knowledgeBaseService) ValidateAccessibleIDs(ids []int64, operator *dto.AuthPrincipal) error {
	if _, err := requireActiveTenantID(operator, "知识库"); err != nil {
		return err
	}
	for _, id := range ids {
		if id <= 0 || s.GetForOperator(id, operator) == nil {
			return errorsx.Forbidden("请求包含无权访问的知识库")
		}
	}
	return nil
}

func (s *knowledgeBaseService) CountContents(id int64, operator *dto.AuthPrincipal) (int64, int64) {
	item := s.GetForOperator(id, operator)
	if item == nil {
		return 0, 0
	}
	documentCount := repositories.KnowledgeDocumentRepository.CountByKnowledgeBaseIDInTenant(sqls.DB(), item.ID, item.TenantID)
	faqCount := repositories.KnowledgeFAQRepository.CountByKnowledgeBaseIDInTenant(sqls.DB(), item.ID, item.TenantID)
	return documentCount, faqCount
}

func (s *knowledgeBaseService) buildKnowledgeBaseModel(req request.CreateKnowledgeBaseRequest, tenantID int64) (*models.KnowledgeBase, error) {
	item := &models.KnowledgeBase{
		IntentProfileID:       0,
		CompanyID:             0,
		StoreID:               req.StoreID,
		DatasetID:             strings.TrimSpace(req.DatasetID),
		DatasetName:           strings.TrimSpace(req.DatasetName),
		ConnectionID:          strings.TrimSpace(req.ConnectionID),
		RetrievalMode:         enums.KnowledgeRetrievalModeFastGPT,
		Name:                  req.Name,
		Description:           req.Description,
		KnowledgeType:         req.KnowledgeType,
		DefaultTopK:           req.DefaultTopK,
		DefaultScoreThreshold: req.DefaultScoreThreshold,
		DefaultRerankLimit:    req.DefaultRerankLimit,
		ChunkProvider:         req.ChunkProvider,
		ChunkTargetTokens:     req.ChunkTargetTokens,
		ChunkMaxTokens:        req.ChunkMaxTokens,
		ChunkOverlapTokens:    req.ChunkOverlapTokens,
		AnswerMode:            req.AnswerMode,
		Remark:                req.Remark,
	}
	if item.StoreID > 0 {
		store := StoreService.GetInTenant(item.StoreID, tenantID)
		if store == nil || store.Status == enums.StatusDeleted {
			return nil, errorsx.InvalidParam("门店不存在")
		}
	}
	if item.ConnectionID == "" {
		item.ConnectionID = "platform"
	}
	if item.DefaultTopK == 0 {
		item.DefaultTopK = 10
	}
	if item.KnowledgeType == "" {
		item.KnowledgeType = string(enums.KnowledgeBaseTypeDocument)
	}
	if !isValidKnowledgeType(item.KnowledgeType) {
		return nil, errorsx.InvalidParam("知识库类型不支持")
	}
	if item.DefaultScoreThreshold == 0 {
		item.DefaultScoreThreshold = 0.2
	}
	if item.DefaultRerankLimit == 0 {
		item.DefaultRerankLimit = 5
	}
	if item.KnowledgeType == string(enums.KnowledgeBaseTypeFastGPTCloud) {
		item.ChunkProvider = string(enums.KnowledgeChunkProviderFastGPT)
		item.ChunkTargetTokens = 0
		item.ChunkMaxTokens = 0
		item.ChunkOverlapTokens = 0
		if item.DefaultTopK == 0 {
			item.DefaultTopK = 2
		}
		if item.DefaultRerankLimit == 0 {
			item.DefaultRerankLimit = 0
		}
		if item.AnswerMode == 0 {
			item.AnswerMode = int(enums.KnowledgeAnswerModeStrict)
		}
		return item, nil
	}
	if item.ChunkProvider == "" {
		item.ChunkProvider = string(enums.KnowledgeChunkProviderStructured)
	}
	if item.KnowledgeType == string(enums.KnowledgeBaseTypeFAQ) {
		item.ChunkProvider = string(enums.KnowledgeChunkProviderFAQ)
		item.ChunkTargetTokens = 0
		item.ChunkMaxTokens = 0
		item.ChunkOverlapTokens = 0
	} else if item.ChunkProvider == string(enums.KnowledgeChunkProviderFAQ) {
		return nil, errorsx.InvalidParam("文档知识库不能使用FAQ分块策略")
	}
	if !isValidChunkProvider(item.ChunkProvider) {
		return nil, errorsx.InvalidParam("分块策略不支持")
	}
	if item.KnowledgeType != string(enums.KnowledgeBaseTypeFAQ) && item.ChunkTargetTokens == 0 {
		item.ChunkTargetTokens = 300
	}
	if item.KnowledgeType != string(enums.KnowledgeBaseTypeFAQ) && item.ChunkMaxTokens == 0 {
		item.ChunkMaxTokens = 400
	}
	if item.KnowledgeType != string(enums.KnowledgeBaseTypeFAQ) && item.ChunkMaxTokens < item.ChunkTargetTokens {
		item.ChunkMaxTokens = item.ChunkTargetTokens
	}
	if item.KnowledgeType != string(enums.KnowledgeBaseTypeFAQ) && item.ChunkOverlapTokens == 0 {
		item.ChunkOverlapTokens = 40
	}
	if item.AnswerMode == 0 {
		item.AnswerMode = 1
	}
	return item, nil
}

func isValidChunkProvider(provider string) bool {
	switch provider {
	case string(enums.KnowledgeChunkProviderFixed),
		string(enums.KnowledgeChunkProviderStructured),
		string(enums.KnowledgeChunkProviderFAQ),
		string(enums.KnowledgeChunkProviderSemantic),
		string(enums.KnowledgeChunkProviderFastGPT):
		return true
	default:
		return false
	}
}

func isValidKnowledgeType(knowledgeType string) bool {
	switch knowledgeType {
	case string(enums.KnowledgeBaseTypeDocument),
		string(enums.KnowledgeBaseTypeFAQ),
		string(enums.KnowledgeBaseTypeFastGPTCloud):
		return true
	default:
		return false
	}
}
