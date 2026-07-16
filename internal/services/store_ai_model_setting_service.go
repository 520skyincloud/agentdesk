package services

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/errorsx"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

// Legacy aliases keep the reply-runtime call sites stable while the model
// selection rules live in one tenant-aware resolver.
const (
	StoreAIModelUsageReplyLLM           = constants.AIModelUsageReplyLLM
	StoreAIModelUsageIntentDetectLLM    = constants.AIModelUsageIntentDetectLLM
	StoreAIModelUsageMediaUnderstanding = constants.AIModelUsageMediaUnderstanding
	StoreAIModelUsageSpeechRecognition  = constants.AIModelUsageSpeechRecognition
	StoreAIModelSourceAccountOverride   = constants.AIModelSourceEmployeeOverride
	StoreAIModelSourceCompanyOverride   = constants.AIModelSourceTenantDefault
	StoreAIModelSourceGlobalDefault     = constants.AIModelSourcePlatformDefault
)

type StoreAIModelUsageMeta = constants.AIModelUsageSpec

type ResolvedAIConfig struct {
	Config         models.AIConfig
	Source         string
	ModelSettingID int64
}

type TenantAIModelAccessData struct {
	TenantID    int64
	Grants      []models.TenantAIModelGrant
	Assignments []models.StoreAIModelSetting
	Configs     map[int64]models.AIConfig
	UsageStats  map[int64]dto.AIModelUsageAggregate
}

var StoreAIModelSettingService = &storeAIModelSettingService{}

type storeAIModelSettingService struct{}

func StoreAIModelUsageMetas() []StoreAIModelUsageMeta {
	return constants.AIModelUsageSpecs()
}

func StoreAIModelUsageMetaByCode(code string) (StoreAIModelUsageMeta, bool) {
	return constants.AIModelUsageSpecByCode(strings.TrimSpace(code))
}

func (s *storeAIModelSettingService) LoadAccess(tenantID int64) (*TenantAIModelAccessData, error) {
	if tenantID <= 0 || repositories.TenantRepository.Get(sqls.DB(), tenantID) == nil {
		return nil, errorsx.InvalidParam("接入公司不存在")
	}
	db := sqls.DB()
	grants := repositories.TenantAIModelGrantRepository.Find(db, sqls.NewCnd().
		Eq("tenant_id", tenantID).
		Where("status <> ?", enums.StatusDeleted).
		Asc("id"))
	assignments := repositories.StoreAIModelSettingRepository.Find(db, sqls.NewCnd().
		Eq("tenant_id", tenantID).
		Where("status <> ?", enums.StatusDeleted).
		Asc("wx_work_instance_id").
		Asc("usage_code"))

	configIDs := make([]int64, 0, len(grants)+len(assignments))
	seen := make(map[int64]struct{})
	for _, grant := range grants {
		configIDs = appendUniquePositive(configIDs, seen, grant.AIConfigID)
	}
	for _, assignment := range assignments {
		configIDs = appendUniquePositive(configIDs, seen, assignment.AIConfigID)
	}
	configs := make(map[int64]models.AIConfig, len(configIDs))
	if len(configIDs) > 0 {
		for _, config := range repositories.AIConfigRepository.Find(db, sqls.NewCnd().In("id", configIDs).Desc("sort_no").Desc("id")) {
			configs[config.ID] = config
		}
	}
	stats := make(map[int64]dto.AIModelUsageAggregate)
	if db.Migrator().HasTable(&models.AIUsageEvent{}) {
		for _, item := range repositories.AIUsageEventRepository.AggregateByTenantAndAIConfig(db, tenantID) {
			stats[item.AIConfigID] = dto.AIModelUsageAggregate{
				RequestCount:       item.RequestCount,
				PromptTokens:       item.PromptTokens,
				CompletionTokens:   item.CompletionTokens,
				CachedPromptTokens: item.CachedPromptTokens,
			}
		}
	}
	return &TenantAIModelAccessData{TenantID: tenantID, Grants: grants, Assignments: assignments, Configs: configs, UsageStats: stats}, nil
}

func (s *storeAIModelSettingService) UpdateTenantAccess(req request.UpdateTenantAIModelAccessRequest, operator *dto.AuthPrincipal) error {
	if err := requirePlatformModelOperator(operator); err != nil {
		return err
	}
	tenant := repositories.TenantRepository.Get(sqls.DB(), req.TenantID)
	if tenant == nil || tenant.Status == enums.StatusDeleted {
		return errorsx.InvalidParam("接入公司不存在")
	}
	grantIDs, configs, err := s.validateGrantIDs(req.GrantedAIConfigIDs)
	if err != nil {
		return err
	}
	defaults, err := validateModelSelections(req.Defaults, grantIDs, configs)
	if err != nil {
		return err
	}
	now := time.Now()
	return sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		if err := s.replaceAssignments(ctx.Tx, req.TenantID, 0, defaults, grantIDs, configs, operator, now); err != nil {
			return err
		}
		if err := s.ensureGrantRevocationsAreUnused(ctx.Tx, req.TenantID, grantIDs); err != nil {
			return err
		}
		return s.replaceGrants(ctx.Tx, req.TenantID, grantIDs, operator, now)
	})
}

func (s *storeAIModelSettingService) UpdateEmployeeAssignments(req request.UpdateTenantAIModelAssignmentsRequest, operator *dto.AuthPrincipal) error {
	if err := requirePlatformModelOperator(operator); err != nil {
		return err
	}
	if req.TenantID <= 0 || req.WxWorkInstanceID <= 0 {
		return errorsx.InvalidParam("接入公司和企微员工号不能为空")
	}
	instance := repositories.WxWorkProtocolInstanceRepository.GetInTenant(sqls.DB(), req.WxWorkInstanceID, req.TenantID)
	if instance == nil || instance.Status == enums.StatusDeleted {
		return errorsx.InvalidParam("企微员工号不存在或不属于该接入公司")
	}
	grantIDs, configs := s.activeGrantConfigMap(sqls.DB(), req.TenantID)
	selections, err := validateModelSelections(req.Assignments, grantIDs, configs)
	if err != nil {
		return err
	}
	now := time.Now()
	return sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		return s.replaceAssignments(ctx.Tx, req.TenantID, instance.ID, selections, grantIDs, configs, operator, now)
	})
}

func (s *storeAIModelSettingService) Resolve(storeID int64, usageCode string) (*ResolvedAIConfig, error) {
	if storeID > 0 {
		store := repositories.StoreRepository.Get(sqls.DB(), storeID)
		if store != nil {
			return s.ResolveForTenant(store.TenantID, 0, usageCode)
		}
	}
	return s.ResolveForTenant(0, 0, usageCode)
}

func (s *storeAIModelSettingService) ResolveForTenant(tenantID int64, wxWorkInstanceID int64, usageCode string) (*ResolvedAIConfig, error) {
	spec, ok := constants.AIModelUsageSpecByCode(strings.TrimSpace(usageCode))
	if !ok {
		return nil, errorsx.InvalidParam("模型用途不合法")
	}
	if tenantID <= 0 {
		return s.resolvePlatformDefault(spec)
	}
	db := sqls.DB()
	if wxWorkInstanceID > 0 {
		if resolved := s.resolveAssignment(db, tenantID, wxWorkInstanceID, spec, constants.AIModelSourceEmployeeOverride); resolved != nil {
			return resolved, nil
		}
	}
	if resolved := s.resolveAssignment(db, tenantID, 0, spec, constants.AIModelSourceTenantDefault); resolved != nil {
		return resolved, nil
	}
	if resolved := s.resolveAuthorizedFallback(db, tenantID, spec); resolved != nil {
		return resolved, nil
	}
	return nil, errorsx.BusinessError(2005, "当前接入公司未获授权可用的模型")
}

func (s *storeAIModelSettingService) ResolveForConversation(conversationID int64, usageCode string) (*ResolvedAIConfig, error) {
	conversation := repositories.ConversationRepository.Get(sqls.DB(), conversationID)
	if conversation == nil || conversation.TenantID <= 0 {
		return nil, errorsx.InvalidParam("会话不存在或缺少接入公司归属")
	}
	wxWorkInstanceID := int64(0)
	if route := ConversationRouteService.GetByConversationIDInTenant(conversationID, conversation.TenantID); route != nil {
		wxWorkInstanceID = route.WxWorkInstanceID
	}
	return s.ResolveForTenant(conversation.TenantID, wxWorkInstanceID, usageCode)
}

func (s *storeAIModelSettingService) ResolveForMessage(message *models.Message, usageCode string) (*ResolvedAIConfig, error) {
	if message == nil {
		return nil, errorsx.InvalidParam("消息不能为空")
	}
	return s.ResolveForConversation(message.ConversationID, usageCode)
}

func (s *storeAIModelSettingService) resolveAssignment(db *gorm.DB, tenantID, wxWorkInstanceID int64, spec constants.AIModelUsageSpec, source string) *ResolvedAIConfig {
	assignment := repositories.StoreAIModelSettingRepository.Take(db,
		"tenant_id = ? AND wx_work_instance_id = ? AND usage_code = ? AND status = ?",
		tenantID, wxWorkInstanceID, spec.Code, enums.StatusOk)
	if assignment == nil || !s.isGranted(db, tenantID, assignment.AIConfigID) {
		return nil
	}
	config := repositories.AIConfigRepository.Get(db, assignment.AIConfigID)
	if !isUsableAIConfigForUsage(config, spec.ExpectedType) {
		return nil
	}
	return &ResolvedAIConfig{Config: *config, Source: source, ModelSettingID: assignment.ID}
}

func (s *storeAIModelSettingService) resolveAuthorizedFallback(db *gorm.DB, tenantID int64, spec constants.AIModelUsageSpec) *ResolvedAIConfig {
	_, configs := s.activeGrantConfigMap(db, tenantID)
	list := make([]models.AIConfig, 0, len(configs))
	for _, config := range configs {
		if isUsableAIConfigForUsage(&config, spec.ExpectedType) {
			list = append(list, config)
		}
	}
	sort.Slice(list, func(i, j int) bool {
		if spec.Code == constants.AIModelUsageIntentDetectLLM && list[i].IntentDetectEnabled != list[j].IntentDetectEnabled {
			return list[i].IntentDetectEnabled
		}
		if list[i].SortNo != list[j].SortNo {
			return list[i].SortNo > list[j].SortNo
		}
		return list[i].ID > list[j].ID
	})
	if len(list) == 0 {
		return nil
	}
	return &ResolvedAIConfig{Config: list[0], Source: constants.AIModelSourceTenantFallback}
}

func (s *storeAIModelSettingService) resolvePlatformDefault(spec constants.AIModelUsageSpec) (*ResolvedAIConfig, error) {
	cnd := sqls.NewCnd().Eq("status", enums.StatusOk).Eq("model_type", spec.ExpectedType)
	if spec.Code == constants.AIModelUsageIntentDetectLLM {
		if config := repositories.AIConfigRepository.FindOne(sqls.DB(), cnd.Eq("intent_detect_enabled", true).Desc("sort_no").Desc("id")); config != nil && isUsableAIConfigForUsage(config, spec.ExpectedType) {
			return &ResolvedAIConfig{Config: *config, Source: constants.AIModelSourcePlatformDefault}, nil
		}
	}
	config := repositories.AIConfigRepository.GetEnabled(sqls.DB(), spec.ExpectedType)
	if !isUsableAIConfigForUsage(config, spec.ExpectedType) {
		return nil, errorsx.BusinessError(2005, "平台未配置可用的模型")
	}
	return &ResolvedAIConfig{Config: *config, Source: constants.AIModelSourcePlatformDefault}, nil
}

func (s *storeAIModelSettingService) replaceAssignments(db *gorm.DB, tenantID, wxWorkInstanceID int64, selections map[string]int64, grants map[int64]struct{}, configs map[int64]models.AIConfig, operator *dto.AuthPrincipal, now time.Time) error {
	for _, spec := range constants.AIModelUsageSpecs() {
		existing := repositories.StoreAIModelSettingRepository.Take(db,
			"tenant_id = ? AND wx_work_instance_id = ? AND usage_code = ?",
			tenantID, wxWorkInstanceID, spec.Code)
		aiConfigID := selections[spec.Code]
		status := enums.StatusDisabled
		if aiConfigID > 0 {
			if _, ok := grants[aiConfigID]; !ok {
				return errorsx.InvalidParam("所选模型未授权给该接入公司")
			}
			if config := configs[aiConfigID]; !isUsableAIConfigForUsage(&config, spec.ExpectedType) {
				return errorsx.InvalidParam("所选模型不可用或类型不匹配")
			}
			status = enums.StatusOk
		}
		columns := map[string]any{
			"tenant_id": tenantID, "company_id": 0, "store_id": 0, "wx_work_instance_id": wxWorkInstanceID,
			"usage_code": spec.Code, "ai_config_id": aiConfigID, "status": status,
			"updated_at": now, "update_user_id": operator.UserID, "update_user_name": operator.Username,
		}
		if existing != nil {
			if err := repositories.StoreAIModelSettingRepository.Updates(db, existing.ID, columns); err != nil {
				return err
			}
			continue
		}
		if aiConfigID == 0 {
			continue
		}
		item := &models.StoreAIModelSetting{TenantID: tenantID, WxWorkInstanceID: wxWorkInstanceID, UsageCode: spec.Code, AIConfigID: aiConfigID, Status: status, AuditFields: auditFields(operator, now)}
		if err := repositories.StoreAIModelSettingRepository.Create(db, item); err != nil {
			return err
		}
	}
	return nil
}

func (s *storeAIModelSettingService) validateGrantIDs(input []int64) (map[int64]struct{}, map[int64]models.AIConfig, error) {
	ids := make(map[int64]struct{})
	for _, id := range input {
		if id > 0 {
			ids[id] = struct{}{}
		}
	}
	configs := make(map[int64]models.AIConfig, len(ids))
	for id := range ids {
		config := repositories.AIConfigRepository.Get(sqls.DB(), id)
		if config == nil || config.Status != enums.StatusOk {
			return nil, nil, errorsx.InvalidParam(fmt.Sprintf("模型配置 %d 不存在或未启用", id))
		}
		configs[id] = *config
	}
	return ids, configs, nil
}

func validateModelSelections(input []request.TenantAIModelDefaultRequest, grants map[int64]struct{}, configs map[int64]models.AIConfig) (map[string]int64, error) {
	result := make(map[string]int64, len(input))
	for _, item := range input {
		code := strings.TrimSpace(item.UsageCode)
		spec, ok := constants.AIModelUsageSpecByCode(code)
		if !ok {
			return nil, errorsx.InvalidParam("模型用途不合法")
		}
		if _, exists := result[code]; exists {
			return nil, errorsx.InvalidParam("模型用途不能重复")
		}
		if item.AIConfigID > 0 {
			if _, ok := grants[item.AIConfigID]; !ok {
				return nil, errorsx.InvalidParam("默认模型必须先授权给该接入公司")
			}
			config := configs[item.AIConfigID]
			if config.Status != enums.StatusOk || config.ModelType != spec.ExpectedType {
				return nil, errorsx.InvalidParam("默认模型类型与用途不匹配")
			}
		}
		result[code] = item.AIConfigID
	}
	return result, nil
}

func (s *storeAIModelSettingService) replaceGrants(db *gorm.DB, tenantID int64, selected map[int64]struct{}, operator *dto.AuthPrincipal, now time.Time) error {
	existing := repositories.TenantAIModelGrantRepository.Find(db, sqls.NewCnd().Eq("tenant_id", tenantID))
	byConfig := make(map[int64]models.TenantAIModelGrant, len(existing))
	for _, grant := range existing {
		byConfig[grant.AIConfigID] = grant
	}
	for configID, grant := range byConfig {
		status := enums.StatusDisabled
		if _, ok := selected[configID]; ok {
			status = enums.StatusOk
		}
		if err := repositories.TenantAIModelGrantRepository.Updates(db, grant.ID, map[string]any{
			"status": status, "updated_at": now, "update_user_id": operator.UserID, "update_user_name": operator.Username,
		}); err != nil {
			return err
		}
	}
	for configID := range selected {
		if _, ok := byConfig[configID]; ok {
			continue
		}
		if err := repositories.TenantAIModelGrantRepository.Create(db, &models.TenantAIModelGrant{
			TenantID: tenantID, AIConfigID: configID, Status: enums.StatusOk, AuditFields: auditFields(operator, now),
		}); err != nil {
			return err
		}
	}
	return nil
}

func (s *storeAIModelSettingService) ensureGrantRevocationsAreUnused(db *gorm.DB, tenantID int64, selected map[int64]struct{}) error {
	grants := repositories.TenantAIModelGrantRepository.Find(db, sqls.NewCnd().Eq("tenant_id", tenantID).Eq("status", enums.StatusOk))
	for _, grant := range grants {
		if _, keep := selected[grant.AIConfigID]; keep {
			continue
		}
		assignments := repositories.StoreAIModelSettingRepository.Find(db, sqls.NewCnd().
			Eq("tenant_id", tenantID).
			Eq("ai_config_id", grant.AIConfigID).
			Eq("status", enums.StatusOk).
			Asc("wx_work_instance_id").Asc("usage_code"))
		if len(assignments) == 0 {
			continue
		}
		dependencies := make([]string, 0, len(assignments))
		for _, assignment := range assignments {
			scope := "租户默认"
			if assignment.WxWorkInstanceID > 0 {
				scope = fmt.Sprintf("员工号#%d", assignment.WxWorkInstanceID)
			}
			dependencies = append(dependencies, scope+"/"+assignment.UsageCode)
		}
		return errorsx.Forbidden("模型仍被使用，先调整以下分配后再撤销授权：" + strings.Join(dependencies, "、"))
	}
	return nil
}

func (s *storeAIModelSettingService) activeGrantConfigMap(db *gorm.DB, tenantID int64) (map[int64]struct{}, map[int64]models.AIConfig) {
	ids := make(map[int64]struct{})
	configs := make(map[int64]models.AIConfig)
	for _, grant := range repositories.TenantAIModelGrantRepository.Find(db, sqls.NewCnd().Eq("tenant_id", tenantID).Eq("status", enums.StatusOk).Asc("id")) {
		config := repositories.AIConfigRepository.Get(db, grant.AIConfigID)
		if config == nil || config.Status != enums.StatusOk {
			continue
		}
		ids[config.ID] = struct{}{}
		configs[config.ID] = *config
	}
	return ids, configs
}

func (s *storeAIModelSettingService) isGranted(db *gorm.DB, tenantID, aiConfigID int64) bool {
	return repositories.TenantAIModelGrantRepository.Take(db,
		"tenant_id = ? AND ai_config_id = ? AND status = ?", tenantID, aiConfigID, enums.StatusOk) != nil
}

func isUsableAIConfigForUsage(config *models.AIConfig, expectedType enums.AIModelType) bool {
	return config != nil && config.Status == enums.StatusOk && config.ModelType == expectedType && strings.TrimSpace(config.ModelName) != "" && strings.TrimSpace(config.APIKey) != ""
}

func requirePlatformModelOperator(operator *dto.AuthPrincipal) error {
	if operator == nil || !operator.IsPlatformAccount {
		return errorsx.Forbidden("只有平台账号可以管理租户模型授权与分配")
	}
	return nil
}

func appendUniquePositive(result []int64, seen map[int64]struct{}, id int64) []int64 {
	if id <= 0 {
		return result
	}
	if _, ok := seen[id]; ok {
		return result
	}
	seen[id] = struct{}{}
	return append(result, id)
}

func auditFields(operator *dto.AuthPrincipal, now time.Time) models.AuditFields {
	return models.AuditFields{CreatedAt: now, CreateUserID: operator.UserID, CreateUserName: operator.Username, UpdatedAt: now, UpdateUserID: operator.UserID, UpdateUserName: operator.Username}
}

// Historical migrations still call these methods on a fresh database. The
// authoritative tenant conversion is migration 59.
func (s *storeAIModelSettingService) BackfillStoreAIModelSettings() error { return nil }

func (s *storeAIModelSettingService) RebuildStoreAIModelSettingScopeIndex() error { return nil }

func (s *storeAIModelSettingService) DisableLegacyWxWorkDedicatedAgents() error {
	now := time.Now()
	return sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		if ctx.Tx.Migrator().HasColumn("t_wx_work_protocol_instance", "ai_agent_id") {
			if err := ctx.Tx.Table("t_wx_work_protocol_instance").Where("ai_agent_id > 0").Updates(map[string]any{
				"ai_agent_id": 0, "updated_at": now, "update_user_id": constants.SystemAuditUserID, "update_user_name": constants.SystemAuditUserName,
			}).Error; err != nil {
				return err
			}
		}
		return ctx.Tx.Model(&models.AIAgent{}).Where("name LIKE ?", "%独立配置%").Updates(map[string]any{
			"status": enums.StatusDisabled, "updated_at": now, "update_user_id": constants.SystemAuditUserID, "update_user_name": constants.SystemAuditUserName,
		}).Error
	})
}
