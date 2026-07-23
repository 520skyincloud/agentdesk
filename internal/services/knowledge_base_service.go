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
	if current.KnowledgeType != string(enums.KnowledgeBaseTypeFastGPTCloud) {
		return errorsx.InvalidParam("历史本地知识库已退出运行链，请使用门店 FastGPT 知识库开通流程")
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return errorsx.InvalidParam("知识库名称不能为空")
	}
	topK := req.DefaultTopK
	if topK <= 0 {
		topK = current.DefaultTopK
	}
	if topK <= 0 || topK > 100 {
		return errorsx.InvalidParam("召回数量必须在 1 到 100 之间")
	}
	scoreThreshold := req.DefaultScoreThreshold
	if scoreThreshold <= 0 {
		scoreThreshold = current.DefaultScoreThreshold
	}
	if scoreThreshold <= 0 || scoreThreshold > 1 {
		return errorsx.InvalidParam("相似度阈值必须大于 0 且不超过 1")
	}
	if req.DefaultRerankLimit < 0 || req.DefaultRerankLimit > 100 {
		return errorsx.InvalidParam("重排保留数量必须在 0 到 100 之间")
	}
	answerMode := req.AnswerMode
	if answerMode == 0 {
		answerMode = current.AnswerMode
	}
	if answerMode != int(enums.KnowledgeAnswerModeStrict) && answerMode != int(enums.KnowledgeAnswerModeAssist) {
		return errorsx.InvalidParam("回答模式不支持")
	}
	remark, err := buildFastGPTKnowledgeRemark(req.ResourceAllowedHosts)
	if err != nil {
		return err
	}
	return repositories.KnowledgeBaseRepository.UpdatesInTenant(sqls.DB(), req.ID, tenantID, map[string]any{
		"name":                    name,
		"description":             strings.TrimSpace(req.Description),
		"default_top_k":           topK,
		"default_score_threshold": scoreThreshold,
		"default_rerank_limit":    req.DefaultRerankLimit,
		"answer_mode":             answerMode,
		"remark":                  remark,
		"update_user_id":          operator.UserID,
		"update_user_name":        operator.Username,
		"updated_at":              time.Now(),
	})
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
