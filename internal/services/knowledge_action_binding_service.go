package services

import (
	"strings"
	"time"

	"agent-desk/internal/ai/runtime/actions"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/errorsx"
	"agent-desk/internal/pkg/utils"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
)

var KnowledgeActionBindingService = newKnowledgeActionBindingService()

func newKnowledgeActionBindingService() *knowledgeActionBindingService {
	return &knowledgeActionBindingService{}
}

type knowledgeActionBindingService struct{}

func (s *knowledgeActionBindingService) List() []models.KnowledgeActionBinding {
	return repositories.KnowledgeActionBindingRepository.Find(sqls.DB(), sqls.NewCnd().Desc("id"))
}

func (s *knowledgeActionBindingService) Get(id int64) *models.KnowledgeActionBinding {
	return repositories.KnowledgeActionBindingRepository.Get(sqls.DB(), id)
}

// Set 以 (tenant, store, knowledge_base, source_record) 为键写绑定。
func (s *knowledgeActionBindingService) Set(
	tenantID, storeID, knowledgeBaseID int64,
	sourceRecordID, actionCode string,
	enabled bool,
	operator *dto.AuthPrincipal,
) (*models.KnowledgeActionBinding, error) {
	if operator == nil {
		return nil, errorsx.Unauthorized("未登录或登录已过期")
	}
	if tenantID <= 0 || storeID <= 0 || knowledgeBaseID <= 0 || strings.TrimSpace(sourceRecordID) == "" {
		return nil, errorsx.InvalidParam("知识动作绑定缺少门店或来源记录")
	}
	actionCode = strings.TrimSpace(actionCode)
	if actionCode == "" {
		return nil, errorsx.InvalidParam("请选择动作")
	}
	if _, ok := actions.Get(actionCode); !ok {
		return nil, errorsx.InvalidParam("动作不存在")
	}
	if enabled && actions.GetDefinitionKind(actionCode) == actions.KindExternal && !actions.Provisioned(actionCode) {
		return nil, errorsx.InvalidParam("该动作依赖的外部系统尚未接入，暂不能启用")
	}
	now := time.Now()
	item := &models.KnowledgeActionBinding{
		TenantID: tenantID, StoreID: storeID, KnowledgeBaseID: knowledgeBaseID,
		SourceRecordID: strings.TrimSpace(sourceRecordID), ActionCode: actionCode, Enabled: enabled,
		AuditFields: models.AuditFields{
			CreatedAt: now, CreateUserID: operator.UserID, CreateUserName: operator.Username,
			UpdatedAt: now, UpdateUserID: operator.UserID, UpdateUserName: operator.Username,
		},
	}
	if err := repositories.KnowledgeActionBindingRepository.UpsertByScope(sqls.DB(), item); err != nil {
		return nil, err
	}
	return repositories.KnowledgeActionBindingRepository.Take(sqls.DB(),
		"tenant_id = ? AND store_id = ? AND knowledge_base_id = ? AND source_record_id = ?",
		tenantID, storeID, knowledgeBaseID, strings.TrimSpace(sourceRecordID)), nil
}

func (s *knowledgeActionBindingService) Delete(id int64, operator *dto.AuthPrincipal) error {
	if operator == nil {
		return errorsx.Unauthorized("未登录或登录已过期")
	}
	if repositories.KnowledgeActionBindingRepository.Get(sqls.DB(), id) == nil {
		return errorsx.InvalidParam("知识动作绑定不存在")
	}
	return repositories.KnowledgeActionBindingRepository.Delete(sqls.DB(), id)
}

// ActionCodeForHit 检索命中后按 SourceRecordID 查启用绑定；未命中返回空。
func (s *knowledgeActionBindingService) ActionCodeForHit(tenantID, storeID, knowledgeBaseID int64, sourceRecordID string) string {
	if tenantID <= 0 || storeID <= 0 || knowledgeBaseID <= 0 || strings.TrimSpace(sourceRecordID) == "" {
		return ""
	}
	bound := repositories.KnowledgeActionBindingRepository.FindEnabledBySourceRecords(
		sqls.DB(), tenantID, storeID, knowledgeBaseID, []string{strings.TrimSpace(sourceRecordID)},
	)
	return bound[strings.TrimSpace(sourceRecordID)]
}

var _ = utils.BuildAuditFields
