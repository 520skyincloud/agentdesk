package services

import (
	"strings"

	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/errorsx"
	"agent-desk/internal/pkg/modelconfig"
	"agent-desk/internal/pkg/usagex"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
)

// ModelCallConfig is the non-persistent runtime result of the sole model
// resolver. B4 attaches decrypted credential material inside the runtime
// boundary; API responses must never serialize this type.
type ModelCallConfig struct {
	TenantID            int64
	StoreID             int64
	StoreStaffBindingID int64
	AssignmentID        int64
	ProfileID           int64
	ProfileCode         string
	ProfileRevision     int64
	SlotID              int64
	UsageCode           enums.ModelUsageSlot
	Provider            string
	GatewayBaseURL      string `json:"-"`
	APIMode             string
	ModelType           enums.AIModelType
	ModelName           string
	Dimension           int
	MaxContextTokens    int
	MaxOutputTokens     int
	TimeoutMS           int
	MaxRetryCount       int
	Temperature         float64
	SchemaVersion       string
	PromptTemplate      string `json:"-"`
	JSONSchema          string `json:"-"`
	CredentialID        int64
	CredentialRevision  int64
	APIKey              string `json:"-"`
	KeyFingerprint      string `json:"-"`
}

var ModelCallResolverService = &modelCallResolverService{}

type modelCallResolverService struct{}

func (c ModelCallConfig) RuntimeConfig() modelconfig.Config {
	return modelconfig.Config{
		Provider:         enums.AIProviderOpenAI,
		BaseURL:          c.GatewayBaseURL,
		APIKey:           c.APIKey,
		APIMode:          c.APIMode,
		ModelType:        c.ModelType,
		ModelName:        c.ModelName,
		Dimension:        c.Dimension,
		MaxContextTokens: c.MaxContextTokens,
		MaxOutputTokens:  c.MaxOutputTokens,
		TimeoutMS:        c.TimeoutMS,
		MaxRetryCount:    c.MaxRetryCount,
		Temperature:      c.Temperature,
	}
}

func modelCallUsageScope(resolved *ModelCallConfig, conversationID, messageID int64, requestID string) usagex.Scope {
	if resolved == nil {
		return usagex.Scope{ConversationID: conversationID, MessageID: messageID, RequestID: strings.TrimSpace(requestID)}
	}
	return usagex.Scope{
		TenantID: resolved.TenantID, StoreID: resolved.StoreID, StoreStaffBindingID: resolved.StoreStaffBindingID,
		ConversationID: conversationID, MessageID: messageID, RequestID: strings.TrimSpace(requestID),
		ModelProfileID: resolved.ProfileID, ProfileRevision: resolved.ProfileRevision,
		UsageSlot: string(resolved.UsageCode), CredentialRevision: resolved.CredentialRevision,
		KeyFingerprint: resolved.KeyFingerprint, ModelSource: AIModelSourceStoreProfile,
	}
}

// ModelCallUsageScope exposes the resolver attribution envelope to debug and
// operational callers without exposing credential material.
func ModelCallUsageScope(resolved *ModelCallConfig, conversationID, messageID int64, requestID string) usagex.Scope {
	return modelCallUsageScope(resolved, conversationID, messageID, requestID)
}

func (s *modelCallResolverService) ResolveForConversation(conversationID int64, usageCode enums.ModelUsageSlot) (*ModelCallConfig, error) {
	conversation := repositories.ConversationRepository.Get(sqls.DB(), conversationID)
	if conversation == nil || conversation.TenantID <= 0 {
		return nil, errorsx.InvalidParam("会话不存在或缺少接入公司归属")
	}
	if conversation.StoreID <= 0 || conversation.StoreStaffBindingID <= 0 {
		return nil, errorsx.BusinessError(5, "会话尚未绑定有效门店员工号，不能解析模型")
	}
	route := repositories.ConversationRouteStateRepository.TakeByConversationInTenant(sqls.DB(), conversation.ID, conversation.TenantID)
	if route == nil || route.TenantID != conversation.TenantID || route.StoreID <= 0 || route.StoreStaffBindingID <= 0 || route.WxWorkInstanceID <= 0 {
		return nil, errorsx.BusinessError(5, "会话尚未绑定门店员工号，不能解析模型")
	}
	if route.StoreID != conversation.StoreID || route.StoreStaffBindingID != conversation.StoreStaffBindingID {
		return nil, errorsx.BusinessError(5, "会话与当前门店员工号范围不一致，不能解析模型")
	}
	instance, err := WxWorkProtocolInstanceService.activeInstanceForBindingDB(
		sqls.DB(),
		conversation.TenantID,
		conversation.StoreStaffBindingID,
	)
	if err != nil {
		return nil, err
	}
	if instance == nil || instance.ID != route.WxWorkInstanceID ||
		instance.TenantID != conversation.TenantID || instance.StoreID != conversation.StoreID ||
		instance.StoreStaffBindingID != conversation.StoreStaffBindingID {
		return nil, errorsx.BusinessError(5, "会话未指向当前有效企微员工号实例，不能解析模型")
	}
	return s.ResolveForBinding(conversation.TenantID, route.StoreID, route.StoreStaffBindingID, usageCode)
}

// ResolveForKnowledgeDebug keeps credential selection deterministic for both
// conversation-backed and standalone knowledge tests.
func (s *modelCallResolverService) ResolveForKnowledgeDebug(
	tenantID, storeID, conversationID, selectedBindingID int64,
	usageCode enums.ModelUsageSlot,
) (*ModelCallConfig, error) {
	if conversationID > 0 {
		resolved, err := s.ResolveForConversation(conversationID, usageCode)
		if err != nil {
			return nil, err
		}
		if tenantID > 0 && resolved.TenantID != tenantID {
			return nil, errorsx.InvalidParam("会话不属于当前接入公司")
		}
		if storeID > 0 && resolved.StoreID != storeID {
			return nil, errorsx.InvalidParam("会话与知识库不属于同一门店")
		}
		if selectedBindingID > 0 && resolved.StoreStaffBindingID != selectedBindingID {
			return nil, errorsx.InvalidParam("所选门店员工号与会话归属不一致")
		}
		return resolved, nil
	}
	if tenantID <= 0 || storeID <= 0 {
		return nil, errorsx.InvalidParam("知识调试缺少接入公司或门店范围")
	}
	if selectedBindingID <= 0 {
		return nil, errorsx.InvalidParam("请选择用于知识调试的门店员工号")
	}
	return s.ResolveForBinding(tenantID, storeID, selectedBindingID, usageCode)
}

func (s *modelCallResolverService) ResolveForBinding(tenantID, storeID, bindingID int64, usageCode enums.ModelUsageSlot) (*ModelCallConfig, error) {
	spec, known := modelUsageSlotSpecByCode(usageCode)
	if !known {
		return nil, errorsx.InvalidParam("模型用途不合法")
	}
	store := repositories.StoreRepository.GetInTenant(sqls.DB(), storeID, tenantID)
	if store == nil || store.Status != enums.StatusOk {
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
	binding, err := StoreModelCredentialService.requireStoreStaffCredentialScopeDB(sqls.DB(), tenantID, storeID, bindingID, true)
	if err != nil {
		return nil, err
	}
	credential := repositories.StoreModelCredentialRepository.GetByBinding(sqls.DB(), tenantID, storeID, binding.ID)
	if credential == nil || credential.Status != enums.StoreCredentialStatusActive || credential.CredentialRevision <= 0 ||
		strings.TrimSpace(credential.EncryptedKey) == "" || strings.TrimSpace(credential.KeyNonce) == "" {
		return nil, errorsx.BusinessError(5, "门店 NewAPI 凭据尚未激活")
	}
	resolvedCredential, err := StoreModelCredentialService.ResolveActiveForBinding(tenantID, storeID, binding.ID)
	if err != nil {
		return nil, err
	}
	if resolvedCredential.Revision != credential.CredentialRevision || resolvedCredential.Fingerprint != credential.KeyFingerprint {
		return nil, errorsx.BusinessError(5, "门店 NewAPI 凭据正在切换，请稍后重试")
	}
	return &ModelCallConfig{
		TenantID: tenantID, StoreID: storeID, StoreStaffBindingID: binding.ID, AssignmentID: assignment.ID,
		ProfileID: template.ID, ProfileCode: template.Code, ProfileRevision: template.Revision,
		SlotID: slot.ID, UsageCode: slot.UsageCode, Provider: slot.Provider,
		GatewayBaseURL: template.GatewayBaseURL, APIMode: slot.APIMode,
		ModelType: slot.ModelType, ModelName: slot.ModelName, Dimension: slot.Dimension,
		MaxContextTokens: slot.MaxContextTokens, MaxOutputTokens: slot.MaxOutputTokens,
		TimeoutMS: slot.TimeoutMS, MaxRetryCount: slot.MaxRetryCount, Temperature: slot.Temperature,
		SchemaVersion: slot.SchemaVersion, PromptTemplate: slot.PromptTemplate, JSONSchema: slot.JSONSchema,
		CredentialID: credential.ID, CredentialRevision: credential.CredentialRevision,
		APIKey: resolvedCredential.APIKey, KeyFingerprint: resolvedCredential.Fingerprint,
	}, nil
}
