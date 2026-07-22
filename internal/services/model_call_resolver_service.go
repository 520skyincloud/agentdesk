package services

import (
	"strings"

	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/errorsx"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
)

// ModelCallConfig is the non-persistent runtime result of the sole model
// resolver. B4 attaches decrypted credential material inside the runtime
// boundary; API responses must never serialize this type.
type ModelCallConfig struct {
	TenantID           int64
	StoreID            int64
	AssignmentID       int64
	ProfileID          int64
	ProfileCode        string
	ProfileRevision    int64
	SlotID             int64
	UsageCode          enums.ModelUsageSlot
	Provider           string
	GatewayBaseURL     string
	APIMode            string
	ModelType          enums.AIModelType
	ModelName          string
	Dimension          int
	MaxContextTokens   int
	MaxOutputTokens    int
	TimeoutMS          int
	MaxRetryCount      int
	Temperature        float64
	SchemaVersion      string
	PromptTemplate     string
	JSONSchema         string
	CredentialID       int64
	CredentialRevision int64
}

var ModelCallResolverService = &modelCallResolverService{}

type modelCallResolverService struct{}

func (s *modelCallResolverService) ResolveForStore(storeID int64, usageCode enums.ModelUsageSlot) (*ModelCallConfig, error) {
	store := repositories.StoreRepository.Get(sqls.DB(), storeID)
	if store == nil || store.Status == enums.StatusDeleted || store.TenantID <= 0 {
		return nil, errorsx.InvalidParam("门店不存在或缺少接入公司归属")
	}
	return s.Resolve(store.TenantID, store.ID, usageCode)
}

func (s *modelCallResolverService) ResolveForConversation(conversationID int64, usageCode enums.ModelUsageSlot) (*ModelCallConfig, error) {
	conversation := repositories.ConversationRepository.Get(sqls.DB(), conversationID)
	if conversation == nil || conversation.TenantID <= 0 {
		return nil, errorsx.InvalidParam("会话不存在或缺少接入公司归属")
	}
	route := repositories.ConversationRouteStateRepository.TakeByConversationInTenant(sqls.DB(), conversation.ID, conversation.TenantID)
	if route == nil || route.StoreID <= 0 {
		return nil, errorsx.BusinessError(5, "会话尚未绑定门店，不能解析模型")
	}
	return s.Resolve(conversation.TenantID, route.StoreID, usageCode)
}

func (s *modelCallResolverService) Resolve(tenantID, storeID int64, usageCode enums.ModelUsageSlot) (*ModelCallConfig, error) {
	spec, known := modelUsageSlotSpecByCode(usageCode)
	if !known {
		return nil, errorsx.InvalidParam("模型用途不合法")
	}
	store := repositories.StoreRepository.GetInTenant(sqls.DB(), storeID, tenantID)
	if store == nil || store.Status == enums.StatusDeleted {
		return nil, errorsx.InvalidParam("门店不存在或不属于当前接入公司")
	}
	assignment := repositories.StoreModelProfileAssignmentRepository.GetByStore(sqls.DB(), tenantID, storeID)
	if assignment == nil || assignment.Status != enums.StoreModelAssignmentStatusReady || assignment.TemplateID <= 0 || assignment.TemplateRevision <= 0 {
		return nil, errorsx.BusinessError(5, "门店模型方案尚未就绪")
	}
	template := repositories.ModelProfileTemplateRepository.Get(sqls.DB(), assignment.TemplateID)
	if template == nil || template.Status != enums.ModelProfileStatusActive || template.Revision != assignment.TemplateRevision {
		return nil, errorsx.BusinessError(5, "门店当前模型方案 revision 无效")
	}
	slots := repositories.ModelProfileSlotRepository.FindByTemplateID(sqls.DB(), template.ID)
	if issues := ValidateModelProfileForPublication(template, slots); len(issues) > 0 {
		return nil, errorsx.BusinessError(5, "门店当前模型方案九槽不完整")
	}
	slot := repositories.ModelProfileSlotRepository.GetByUsage(sqls.DB(), template.ID, usageCode)
	if slot == nil || !slot.Enabled || slot.ModelType != spec.ExpectedModelType || !strings.EqualFold(slot.Provider, modelProfileProviderNewAPI) {
		return nil, errorsx.BusinessError(5, "门店当前模型用途不可用")
	}
	credential := repositories.StoreModelCredentialRepository.GetByStore(sqls.DB(), tenantID, storeID)
	if credential == nil || credential.Status != enums.StoreCredentialStatusActive || credential.CredentialRevision <= 0 ||
		strings.TrimSpace(credential.EncryptedKey) == "" || strings.TrimSpace(credential.KeyNonce) == "" {
		return nil, errorsx.BusinessError(5, "门店 NewAPI 凭据尚未激活")
	}
	return &ModelCallConfig{
		TenantID: tenantID, StoreID: storeID, AssignmentID: assignment.ID,
		ProfileID: template.ID, ProfileCode: template.Code, ProfileRevision: template.Revision,
		SlotID: slot.ID, UsageCode: slot.UsageCode, Provider: slot.Provider,
		GatewayBaseURL: template.GatewayBaseURL, APIMode: slot.APIMode,
		ModelType: slot.ModelType, ModelName: slot.ModelName, Dimension: slot.Dimension,
		MaxContextTokens: slot.MaxContextTokens, MaxOutputTokens: slot.MaxOutputTokens,
		TimeoutMS: slot.TimeoutMS, MaxRetryCount: slot.MaxRetryCount, Temperature: slot.Temperature,
		SchemaVersion: slot.SchemaVersion, PromptTemplate: slot.PromptTemplate, JSONSchema: slot.JSONSchema,
		CredentialID: credential.ID, CredentialRevision: credential.CredentialRevision,
	}, nil
}
