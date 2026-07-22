package services

import (
	"fmt"
	"strings"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/errorsx"
	"agent-desk/internal/pkg/replyintent"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

const (
	tenantIndustryActionCreate    = "create"
	tenantIndustryActionChange    = "change"
	tenantIndustryActionMigration = "migration"
)

var TenantIndustryService = &tenantIndustryService{}

type tenantIndustryService struct{}

func (s *tenantIndustryService) FindBindableProfiles() ([]models.ReplyIntentProfile, error) {
	db := sqls.DB()
	items := repositories.ReplyIntentProfileRepository.Find(db, sqls.NewCnd().
		Eq("status", enums.StatusOk).Asc("sort_no").Asc("id"))
	ret := make([]models.ReplyIntentProfile, 0, len(items))
	for i := range items {
		if _, err := s.ValidateBindingProfileDB(db, items[i].ID); err == nil {
			ret = append(ret, items[i])
		}
	}
	return ret, nil
}

func (s *tenantIndustryService) FindProfilesByIDs(ids []int64) map[int64]*models.ReplyIntentProfile {
	ret := make(map[int64]*models.ReplyIntentProfile)
	if len(ids) == 0 || sqls.DB() == nil {
		return ret
	}
	items := repositories.ReplyIntentProfileRepository.Find(sqls.DB(), sqls.NewCnd().In("id", uniqueTenantIndustryProfileIDs(ids)))
	for i := range items {
		item := items[i]
		ret[item.ID] = &item
	}
	return ret
}

func (s *tenantIndustryService) ValidateBindingProfileDB(db *gorm.DB, profileID int64) (*models.ReplyIntentProfile, error) {
	if db == nil || profileID <= 0 {
		return nil, errorsx.InvalidParam("接入公司必须选择行业")
	}
	profile := repositories.ReplyIntentProfileRepository.Get(db, profileID)
	if profile == nil || profile.Status != enums.StatusOk {
		return nil, errorsx.InvalidParam("所选行业未发布或已停用")
	}
	if profile.Revision <= 0 || profile.PublishedAt == nil {
		return nil, errorsx.InvalidParam("所选行业尚未发布有效版本")
	}
	if strings.TrimSpace(profile.IndustryCode) == "" || strings.TrimSpace(profile.IntentDetectPrompt) == "" || strings.TrimSpace(profile.IntentJSONSchema) == "" {
		return nil, errorsx.InvalidParam("所选行业的意图提示词或输出 Schema 尚未配置完整")
	}
	configs := repositories.ReplyIntentConfigRepository.Find(db, sqls.NewCnd().
		Eq("intent_profile_id", profile.ID).
		Eq("scope_type", "global").Eq("company_id", 0).Eq("store_id", 0).Eq("wx_work_instance_id", 0))
	if err := validateIndustryIntentConfigs(profile, configs); err != nil {
		return nil, err
	}
	definitions, err := repositories.IndustryTagDefinitionRepository.FindActiveByProfile(db, profile.ID)
	if err != nil {
		return nil, err
	}
	if err := validateIndustryTagDefinitions(profile, definitions); err != nil {
		return nil, err
	}
	return profile, nil
}

func (s *tenantIndustryService) ResolveTenantProfileDB(db *gorm.DB, tenantID int64) (*models.ReplyIntentProfile, error) {
	if db == nil || tenantID <= 0 {
		return nil, errorsx.InvalidParam("接入公司范围无效")
	}
	tenant := repositories.TenantRepository.Get(db, tenantID)
	if tenant == nil || tenant.Status != enums.StatusOk || tenant.IntentProfileID <= 0 {
		return nil, errorsx.InvalidParam("接入公司尚未绑定可用行业")
	}
	return s.ValidateBindingProfileDB(db, tenant.IntentProfileID)
}

func (s *tenantIndustryService) InitializeTenantDB(db *gorm.DB, tenant *models.Tenant, operator *dto.AuthPrincipal) error {
	if tenant == nil || tenant.ID <= 0 {
		return errorsx.InvalidParam("接入公司不存在")
	}
	profile, err := s.ValidateBindingProfileDB(db, tenant.IntentProfileID)
	if err != nil {
		return err
	}
	if err := s.syncTenantCatalogDB(db, tenant.ID, 0, profile.ID, operator); err != nil {
		return err
	}
	return repositories.TenantIndustryChangeLogRepository.Create(db, &models.TenantIndustryChangeLog{
		TenantID: tenant.ID, AfterIntentProfileID: profile.ID, AfterRevision: profile.Revision,
		Action: tenantIndustryActionCreate, Reason: "接入公司创建时绑定行业",
		OperatorID: auditUserID(operator), OperatorName: auditUsername(operator), CreatedAt: time.Now(),
	})
}

func (s *tenantIndustryService) ChangeTenantDB(db *gorm.DB, tenant *models.Tenant, profileID int64, confirmed bool, reason string, operator *dto.AuthPrincipal) error {
	if tenant == nil || tenant.ID <= 0 {
		return errorsx.InvalidParam("接入公司不存在")
	}
	if target, err := repositories.ReplyIntentProfileRepository.GetForUpdate(db, profileID); err != nil {
		return err
	} else if target == nil {
		return errorsx.InvalidParam("所选行业不存在")
	}
	target, err := s.ValidateBindingProfileDB(db, profileID)
	if err != nil {
		return err
	}
	if tenant.IntentProfileID == target.ID {
		return nil
	}
	reason = strings.TrimSpace(reason)
	if !confirmed || reason == "" {
		return errorsx.InvalidParam("切换行业需要二次确认并填写变更原因")
	}
	aiEnabledCount, err := repositories.WxWorkProtocolInstanceRepository.CountAIEnabledInTenant(db, tenant.ID)
	if err != nil {
		return err
	}
	if aiEnabledCount > 0 {
		return errorsx.InvalidParam("请先关闭该公司全部门店的 AI 回复，再切换行业")
	}
	beforeRevision := int64(0)
	if current := repositories.ReplyIntentProfileRepository.Get(db, tenant.IntentProfileID); current != nil {
		beforeRevision = current.Revision
	}
	if err := s.syncTenantCatalogDB(db, tenant.ID, tenant.IntentProfileID, target.ID, operator); err != nil {
		return err
	}
	return repositories.TenantIndustryChangeLogRepository.Create(db, &models.TenantIndustryChangeLog{
		TenantID: tenant.ID, BeforeIntentProfileID: tenant.IntentProfileID, AfterIntentProfileID: target.ID,
		BeforeRevision: beforeRevision, AfterRevision: target.Revision, Action: tenantIndustryActionChange,
		Reason: reason, OperatorID: operator.UserID, OperatorName: operator.Username, CreatedAt: time.Now(),
	})
}

// InitializeTenantForMigrationDB binds a historical Tenant after the catalog
// has been seeded. It is only called by the numbered DML migration.
func (s *tenantIndustryService) InitializeTenantForMigrationDB(db *gorm.DB, tenant *models.Tenant, profileID int64) error {
	if tenant == nil || tenant.ID <= 0 {
		return fmt.Errorf("historical tenant is missing")
	}
	profile, err := s.ValidateBindingProfileDB(db, profileID)
	if err != nil {
		return err
	}
	beforeID := tenant.IntentProfileID
	if err := s.syncTenantCatalogDB(db, tenant.ID, beforeID, profile.ID, nil); err != nil {
		return err
	}
	if beforeID == profile.ID {
		return nil
	}
	return repositories.TenantIndustryChangeLogRepository.Create(db, &models.TenantIndustryChangeLog{
		TenantID: tenant.ID, BeforeIntentProfileID: beforeID, AfterIntentProfileID: profile.ID,
		AfterRevision: profile.Revision, Action: tenantIndustryActionMigration,
		Reason: "统一行业事实源迁移", OperatorID: constants.SystemAuditUserID,
		OperatorName: constants.SystemAuditUserName, CreatedAt: time.Now(),
	})
}

func (s *tenantIndustryService) syncTenantCatalogDB(db *gorm.DB, tenantID, previousProfileID, profileID int64, operator *dto.AuthPrincipal) error {
	if previousProfileID > 0 && previousProfileID != profileID {
		if err := s.retireTenantCatalogDB(db, tenantID, previousProfileID, operator); err != nil {
			return err
		}
	}
	definitions, err := repositories.IndustryTagDefinitionRepository.FindActiveByProfile(db, profileID)
	if err != nil {
		return err
	}
	now := time.Now()
	definitionTagIDs := make(map[int64]int64, len(definitions))
	for i := range definitions {
		definition := definitions[i]
		parentID := int64(0)
		if definition.ParentID > 0 {
			var ok bool
			parentID, ok = definitionTagIDs[definition.ParentID]
			if !ok {
				return fmt.Errorf("industry tag %s references unavailable parent %d", definition.SemanticKey, definition.ParentID)
			}
		}
		item := repositories.TagRepository.TakeByTemplateInTenant(db, tenantID, definition.ID)
		if item == nil {
			legacy := repositories.TagRepository.TakeBySemanticKeyInTenant(db, tenantID, definition.SemanticKey)
			if legacy != nil && legacy.TemplateDefinitionID == nil {
				item = legacy
			}
		}
		if item == nil {
			templateID := definition.ID
			item = &models.Tag{
				TenantID: tenantID, IntentProfileID: profileID, TemplateDefinitionID: &templateID,
				ParentID: parentID, Name: definition.Name, SemanticKey: definition.SemanticKey,
				Aliases: definition.Aliases, ConflictGroup: definition.ConflictGroup,
				ApplicableScene: definition.ApplicableScene, AIEnabled: definition.AIEnabled,
				ReplyEnabled: definition.ReplyEnabled, SystemDefined: true, SortNo: definition.SortNo,
				Status: enums.StatusOk, AuditFields: systemOrOperatorAuditFields(now, operator),
			}
			if err := repositories.TagRepository.Create(db, item); err != nil {
				return err
			}
		} else {
			templateID := definition.ID
			if err := repositories.TagRepository.UpdatesInTenant(db, item.ID, tenantID, map[string]any{
				"intent_profile_id": profileID, "template_definition_id": templateID, "parent_id": parentID,
				"name": definition.Name, "semantic_key": definition.SemanticKey, "aliases": definition.Aliases,
				"conflict_group": definition.ConflictGroup, "applicable_scene": definition.ApplicableScene,
				"ai_enabled": definition.AIEnabled, "reply_enabled": definition.ReplyEnabled,
				"system_defined": true, "sort_no": definition.SortNo, "status": enums.StatusOk,
				"update_user_id": auditUserID(operator), "update_user_name": auditUsername(operator), "updated_at": now,
			}); err != nil {
				return err
			}
		}
		definitionTagIDs[definition.ID] = item.ID
	}
	policy := repositories.TenantCustomerTagPolicyRepository.GetByTenant(db, tenantID)
	if policy == nil {
		return repositories.TenantCustomerTagPolicyRepository.Create(db, &models.TenantCustomerTagPolicy{
			TenantID: tenantID, IntentProfileID: profileID, QuietPeriodMinutes: 1440,
			MinimumConfidence: 0.8, MaxOperationsPerRun: 6, Status: enums.StatusOk,
			AuditFields: systemOrOperatorAuditFields(now, operator),
		})
	}
	return repositories.TenantCustomerTagPolicyRepository.UpdatesByTenant(db, tenantID, map[string]any{
		"intent_profile_id": profileID, "status": enums.StatusOk, "update_user_id": auditUserID(operator),
		"update_user_name": auditUsername(operator), "updated_at": now,
	})
}

func (s *tenantIndustryService) retireTenantCatalogDB(db *gorm.DB, tenantID, profileID int64, operator *dto.AuthPrincipal) error {
	tags, err := repositories.TagRepository.FindByProfileInTenant(db, tenantID, profileID)
	if err != nil {
		return err
	}
	tagIDs := make([]int64, 0, len(tags))
	for i := range tags {
		tagIDs = append(tagIDs, tags[i].ID)
	}
	relations, err := repositories.CustomerTagRelationRepository.FindActiveByTenantAndTagIDs(db, tenantID, tagIDs)
	if err != nil {
		return err
	}
	now := time.Now()
	for i := range relations {
		relation := relations[i]
		if err := repositories.CustomerTagRelationRepository.Inactivate(db, relation.ID, tenantID, map[string]any{
			"relation_status": "inactive", "inactivated_at": now,
			"update_user_id": auditUserID(operator), "update_user_name": auditUsername(operator), "updated_at": now,
		}); err != nil {
			return err
		}
		if err := repositories.CustomerTagChangeLogRepository.Create(db, &models.CustomerTagChangeLog{
			TenantID: tenantID, StoreID: relation.StoreID, CustomerID: relation.CustomerID,
			StoreCustomerRelationID: relation.StoreCustomerRelationID, Action: "remove", OldTagID: relation.TagID,
			EvidenceMessageIDs: "[]", Source: "system", OperatorType: operatorType(operator),
			OperatorID: auditUserID(operator), OperatorName: auditUsername(operator), CreatedAt: now,
		}); err != nil {
			return err
		}
	}
	for i := range tags {
		if err := repositories.TagRepository.UpdatesInTenant(db, tags[i].ID, tenantID, map[string]any{
			"status": enums.StatusDisabled, "update_user_id": auditUserID(operator),
			"update_user_name": auditUsername(operator), "updated_at": now,
		}); err != nil {
			return err
		}
	}
	return nil
}

func validateIndustryIntentConfigs(profile *models.ReplyIntentProfile, configs []models.ReplyIntentConfig) error {
	activeCount := 0
	for i := range configs {
		if configs[i].Status == enums.StatusOk {
			activeCount++
		}
	}
	if strings.TrimSpace(profile.IndustryCode) != replyintent.DefaultHotelIndustryCode {
		if activeCount == 0 {
			return errorsx.InvalidParam("所选行业尚未配置可用意图分类")
		}
		return nil
	}
	required := map[string]bool{
		"hotel_info": false, "hotel_variable": false, "service_request": false,
		"human_complaint_risk": false, "interaction": false,
	}
	for i := range configs {
		if configs[i].Status != enums.StatusOk {
			return errorsx.InvalidParam("酒店行业五个固定意图分类必须全部启用")
		}
		if _, ok := required[strings.TrimSpace(configs[i].Code)]; ok {
			required[strings.TrimSpace(configs[i].Code)] = true
		}
	}
	if len(configs) != len(required) {
		return errorsx.InvalidParam("酒店行业只能启用五个固定意图分类")
	}
	for code, found := range required {
		if !found {
			return errorsx.InvalidParam("酒店行业缺少必需意图分类：" + code)
		}
	}
	return nil
}

func validateIndustryTagDefinitions(profile *models.ReplyIntentProfile, definitions []models.IndustryTagDefinition) error {
	parents := make(map[int64]struct{})
	leafCount := 0
	conflictGroups := make(map[string]struct{})
	for i := range definitions {
		if definitions[i].ParentID == 0 {
			parents[definitions[i].ID] = struct{}{}
			continue
		}
		leafCount++
		if strings.TrimSpace(definitions[i].ConflictGroup) != "" {
			conflictGroups[definitions[i].ConflictGroup] = struct{}{}
		}
	}
	if len(parents) == 0 || leafCount == 0 {
		return errorsx.InvalidParam("所选行业尚未配置完整客户标签目录")
	}
	for i := range definitions {
		if definitions[i].ParentID > 0 {
			if _, ok := parents[definitions[i].ParentID]; !ok {
				return errorsx.InvalidParam("行业标签目录存在无效父级")
			}
		}
	}
	if strings.TrimSpace(profile.IndustryCode) == replyintent.DefaultHotelIndustryCode && (len(parents) != 4 || leafCount != 31 || len(conflictGroups) != 8) {
		return errorsx.InvalidParam("酒店行业标签目录必须严格包含 4 个分类、31 个标签和 8 个互斥组")
	}
	return nil
}

func systemOrOperatorAuditFields(now time.Time, operator *dto.AuthPrincipal) models.AuditFields {
	return models.AuditFields{
		CreatedAt: now, UpdatedAt: now, CreateUserID: auditUserID(operator),
		CreateUserName: auditUsername(operator), UpdateUserID: auditUserID(operator), UpdateUserName: auditUsername(operator),
	}
}

func operatorType(operator *dto.AuthPrincipal) string {
	if operator == nil {
		return "system"
	}
	return "user"
}

func uniqueTenantIndustryProfileIDs(values []int64) []int64 {
	seen := make(map[int64]struct{}, len(values))
	ret := make([]int64, 0, len(values))
	for _, value := range values {
		if value <= 0 {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		ret = append(ret, value)
	}
	return ret
}
