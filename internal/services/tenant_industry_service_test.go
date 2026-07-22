package services

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/replyintent"

	"gorm.io/gorm"
)

func TestTenantIndustryChangeRequiresSafetyGateAndRestoresCatalogOnReturn(t *testing.T) {
	db, operator := setupTenantManagementTestDB(t)
	target := seedTenantIndustryProfile(t, db, 7002, "test-retail")
	created, err := TenantService.CreateTenant(tenantManagementCreateRequest("industry-change", "91350100MA8C1D2E3F"), operator)
	if err != nil {
		t.Fatalf("create tenant: %v", err)
	}

	originalTag := firstTenantIndustryLeafTag(t, db, created.Tenant.ID, created.Tenant.IntentProfileID)
	relation := &models.CustomerTagRelation{
		TenantID: created.Tenant.ID, StoreID: 501, CustomerID: 601, StoreCustomerRelationID: 701,
		TagID: originalTag.ID, Source: "manual", RelationStatus: "active", Confidence: 1,
		AuditFields: tenantManagementAuditFields(time.Now()),
	}
	if err := db.Create(relation).Error; err != nil {
		t.Fatalf("create active customer tag relation: %v", err)
	}
	instance := &models.WxWorkProtocolInstance{
		TenantID: created.Tenant.ID, Guid: "industry-change-ai-enabled", AIReplyEnabled: true,
		Status: enums.StatusOk, AuditFields: tenantManagementAuditFields(time.Now()),
	}
	if err := db.Create(instance).Error; err != nil {
		t.Fatalf("create AI-enabled instance: %v", err)
	}

	withoutConfirmation := tenantIndustryUpdateRequest(created.Tenant, target.ID, false, "")
	if err := TenantService.UpdateTenant(withoutConfirmation, operator); err == nil {
		t.Fatal("expected industry change without confirmation and reason to fail")
	}
	withAIEnabled := tenantIndustryUpdateRequest(created.Tenant, target.ID, true, "业务切换")
	if err := TenantService.UpdateTenant(withAIEnabled, operator); err == nil {
		t.Fatal("expected industry change while AI reply is enabled to fail")
	}
	assertTenantIndustryState(t, db, created.Tenant.ID, created.Tenant.IntentProfileID, 1)

	if err := db.Model(&models.WxWorkProtocolInstance{}).Where("id = ?", instance.ID).
		Update("ai_reply_enabled", false).Error; err != nil {
		t.Fatalf("disable AI reply for industry change: %v", err)
	}
	if err := TenantService.UpdateTenant(withAIEnabled, operator); err != nil {
		t.Fatalf("change tenant industry: %v", err)
	}
	assertTenantIndustryState(t, db, created.Tenant.ID, target.ID, 2)

	var retiredTag models.Tag
	if err := db.First(&retiredTag, originalTag.ID).Error; err != nil {
		t.Fatalf("reload retired tag: %v", err)
	}
	if retiredTag.Status != enums.StatusDisabled {
		t.Fatalf("old industry tag status = %d, want disabled", retiredTag.Status)
	}
	var retiredRelation models.CustomerTagRelation
	if err := db.First(&retiredRelation, relation.ID).Error; err != nil {
		t.Fatalf("reload retired customer tag relation: %v", err)
	}
	if retiredRelation.RelationStatus != "inactive" || retiredRelation.InactivatedAt == nil {
		t.Fatalf("old customer tag relation was not inactivated: %+v", retiredRelation)
	}
	firstTenantIndustryLeafTag(t, db, created.Tenant.ID, target.ID)

	current := loadTenantForIndustryTest(t, db, created.Tenant.ID)
	back := tenantIndustryUpdateRequest(current, created.Tenant.IntentProfileID, true, "恢复原行业")
	if err := TenantService.UpdateTenant(back, operator); err != nil {
		t.Fatalf("restore tenant industry: %v", err)
	}
	assertTenantIndustryState(t, db, created.Tenant.ID, created.Tenant.IntentProfileID, 3)
	if err := db.First(&retiredTag, originalTag.ID).Error; err != nil {
		t.Fatalf("reload restored tag: %v", err)
	}
	if retiredTag.Status != enums.StatusOk {
		t.Fatalf("restored industry tag status = %d, want enabled", retiredTag.Status)
	}
	if err := db.First(&retiredRelation, relation.ID).Error; err != nil {
		t.Fatalf("reload customer tag relation after return: %v", err)
	}
	if retiredRelation.RelationStatus != "inactive" {
		t.Fatalf("historical relation must remain inactive after returning to an industry: %+v", retiredRelation)
	}
}

func TestTenantIndustryChangeFailsClosedWhenAIEnabledCountCannotBeRead(t *testing.T) {
	db, operator := setupTenantManagementTestDB(t)
	target := seedTenantIndustryProfile(t, db, 7004, "test-finance")
	created, err := TenantService.CreateTenant(tenantManagementCreateRequest("industry-count-error", "91350100MA8F1A2B3C"), operator)
	if err != nil {
		t.Fatalf("create tenant: %v", err)
	}

	callbackName := "test:tenant-industry-ai-count-error"
	if err := db.Callback().Query().Before("gorm:query").Register(callbackName, func(tx *gorm.DB) {
		if tx.Statement.Schema != nil && tx.Statement.Schema.Name == "WxWorkProtocolInstance" {
			tx.AddError(errors.New("forced AI-enabled count failure"))
		}
	}); err != nil {
		t.Fatalf("register count failure callback: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Callback().Query().Remove(callbackName); err != nil {
			t.Errorf("remove count failure callback: %v", err)
		}
	})

	update := tenantIndustryUpdateRequest(created.Tenant, target.ID, true, "验证统计异常时阻止切换")
	if err := TenantService.UpdateTenant(update, operator); err == nil {
		t.Fatal("expected industry change to fail when AI-enabled count cannot be read")
	}
	assertTenantIndustryState(t, db, created.Tenant.ID, created.Tenant.IntentProfileID, 1)
}

func TestBoundIndustryCannotBeDisabledOrRenameStableCodes(t *testing.T) {
	db, operator := setupTenantManagementTestDB(t)
	created, err := TenantService.CreateTenant(tenantManagementCreateRequest("bound-industry", "91350100MA8B0U1N2D"), operator)
	if err != nil {
		t.Fatalf("create tenant: %v", err)
	}
	profile := loadTenantIndustryProfile(t, db, created.Tenant.IntentProfileID)

	disable := replyIntentProfileUpdateRequest(profile)
	disable.Status = enums.StatusDisabled
	if err := ReplyIntentProfileService.UpdateReplyIntentProfile(disable, operator); err == nil {
		t.Fatal("expected a bound industry profile not to be disabled")
	}
	rename := replyIntentProfileUpdateRequest(profile)
	rename.Code = profile.Code + "-renamed"
	if err := ReplyIntentProfileService.UpdateReplyIntentProfile(rename, operator); err == nil {
		t.Fatal("expected a bound industry stable code not to be changed")
	}
	reloaded := loadTenantIndustryProfile(t, db, profile.ID)
	if reloaded.Code != profile.Code || reloaded.Status != enums.StatusOk || reloaded.Revision != profile.Revision {
		t.Fatalf("rejected industry mutations changed persisted profile: before=%+v after=%+v", profile, reloaded)
	}
}

func TestTenantIndustryBindingLocksTargetProfile(t *testing.T) {
	db, operator := setupTenantManagementTestDB(t)
	seenProfileLock := false
	callbackName := "test:tenant-industry-profile-lock"
	if err := db.Callback().Query().Before("gorm:query").Register(callbackName, func(tx *gorm.DB) {
		if tx.Statement.Schema != nil && tx.Statement.Schema.Name == "ReplyIntentProfile" {
			if _, locked := tx.Statement.Clauses["FOR"]; locked {
				seenProfileLock = true
			}
		}
	}); err != nil {
		t.Fatalf("register profile locking callback: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Callback().Query().Remove(callbackName); err != nil {
			t.Errorf("remove profile locking callback: %v", err)
		}
	})

	created, err := TenantService.CreateTenant(tenantManagementCreateRequest("profile-lock", "91350100MA8P1R2T3U"), operator)
	if err != nil {
		t.Fatalf("create tenant: %v", err)
	}
	if !seenProfileLock {
		t.Fatal("tenant creation did not lock the selected industry profile")
	}

	seenProfileLock = false
	target := seedTenantIndustryProfile(t, db, 7003, "test-logistics")
	if err := TenantService.UpdateTenant(
		tenantIndustryUpdateRequest(created.Tenant, target.ID, true, "切换行业锁验证"),
		operator,
	); err != nil {
		t.Fatalf("change tenant industry: %v", err)
	}
	if !seenProfileLock {
		t.Fatal("tenant industry change did not lock the target profile")
	}
}

func TestPublishedHotelIntentCatalogRejectsDestructiveMutationsAndAdvancesRevision(t *testing.T) {
	db, operator := setupTenantManagementTestDB(t)
	profile, configs := seedStrictHotelIndustryProfile(t, db, 7100)
	initialRevision := profile.Revision

	if _, err := ReplyIntentConfigService.CreateReplyIntentConfig(request.CreateReplyIntentConfigRequest{
		Code: "sixth_intent", Name: "第六类", IntentProfileID: profile.ID, Status: enums.StatusOk,
	}, operator); err == nil {
		t.Fatal("expected a sixth hotel intent to be rejected")
	}
	assertHotelIntentMutationRolledBack(t, db, profile.ID, initialRevision, 5)
	if _, err := ReplyIntentConfigService.CreateReplyIntentConfig(request.CreateReplyIntentConfigRequest{
		Code: "disabled_sixth_intent", Name: "停用第六类", IntentProfileID: profile.ID, Status: enums.StatusDisabled,
	}, operator); err == nil {
		t.Fatal("expected a disabled sixth hotel intent to be rejected")
	}
	assertHotelIntentMutationRolledBack(t, db, profile.ID, initialRevision, 5)

	disable := replyIntentConfigUpdateRequest(configs[0])
	disable.Status = enums.StatusDisabled
	if err := ReplyIntentConfigService.UpdateReplyIntentConfig(disable, operator); err == nil {
		t.Fatal("expected disabling a required hotel intent to be rejected")
	}
	assertHotelIntentMutationRolledBack(t, db, profile.ID, initialRevision, 5)

	if err := ReplyIntentConfigService.DeleteReplyIntentConfig(configs[1].ID, operator); err == nil {
		t.Fatal("expected deleting a required hotel intent to be rejected")
	}
	assertHotelIntentMutationRolledBack(t, db, profile.ID, initialRevision, 5)

	valid := replyIntentConfigUpdateRequest(configs[0])
	valid.Name = "酒店信息（已复核）"
	valid.PromptPack = "只按当前门店知识回答。"
	if err := ReplyIntentConfigService.UpdateReplyIntentConfig(valid, operator); err != nil {
		t.Fatalf("update valid hotel intent: %v", err)
	}
	reloaded := loadTenantIndustryProfile(t, db, profile.ID)
	if reloaded.Revision != initialRevision+1 || reloaded.PublishedAt == nil || reloaded.PublishedBy != operator.UserID {
		t.Fatalf("valid hotel intent update did not publish the next revision: %+v", reloaded)
	}
	var staleDefinitions int64
	if err := db.Model(&models.IndustryTagDefinition{}).
		Where("intent_profile_id = ? AND definition_revision <> ?", profile.ID, reloaded.Revision).
		Count(&staleDefinitions).Error; err != nil {
		t.Fatalf("count stale industry tag definitions: %v", err)
	}
	if staleDefinitions != 0 {
		t.Fatalf("industry tag definitions with stale revision = %d", staleDefinitions)
	}
}

func TestDraftIndustryIntentCatalogCanBeBuiltIncrementally(t *testing.T) {
	db, operator := setupTenantManagementTestDB(t)
	profile, err := ReplyIntentProfileService.CreateReplyIntentProfile(request.CreateReplyIntentProfileRequest{
		Code: "draft-retail", Name: "零售行业草稿", IndustryCode: "retail", Status: enums.StatusDisabled,
	}, operator)
	if err != nil {
		t.Fatalf("create draft industry profile: %v", err)
	}

	for _, code := range []string{"product_question", "after_sales"} {
		if _, err := ReplyIntentConfigService.CreateReplyIntentConfig(request.CreateReplyIntentConfigRequest{
			Code: code, Name: code, IntentProfileID: profile.ID, Status: enums.StatusOk,
		}, operator); err != nil {
			t.Fatalf("append %s to draft industry catalog: %v", code, err)
		}
	}

	reloaded := loadTenantIndustryProfile(t, db, profile.ID)
	if reloaded.Status != enums.StatusDisabled || reloaded.Revision != 3 || reloaded.PublishedAt != nil {
		t.Fatalf("draft catalog mutation changed publication state: %+v", reloaded)
	}
}

func TestIndustryProfileWithTagDefinitionsCannotBeDeleted(t *testing.T) {
	db, _ := setupTenantManagementTestDB(t)
	profile := &models.ReplyIntentProfile{
		ID: 7200, Code: "tagged-draft", Name: "已有标签的草稿行业", IndustryCode: "tagged-draft",
		Revision: 1, Status: enums.StatusDisabled, AuditFields: tenantManagementAuditFields(time.Now()),
	}
	if err := db.Create(profile).Error; err != nil {
		t.Fatalf("create tagged draft profile: %v", err)
	}
	if err := db.Create(&models.IndustryTagDefinition{
		IntentProfileID: profile.ID, Name: "固定分类", SemanticKey: "category.tagged-draft",
		DefinitionRevision: 1, Status: enums.StatusOk, AuditFields: tenantManagementAuditFields(time.Now()),
	}).Error; err != nil {
		t.Fatalf("create industry tag definition: %v", err)
	}

	if err := ReplyIntentProfileService.DeleteReplyIntentProfile(profile.ID); err == nil {
		t.Fatal("expected an industry profile with tag definitions not to be deleted")
	}
	loadTenantIndustryProfile(t, db, profile.ID)
}

func seedTenantIndustryProfile(t *testing.T, db *gorm.DB, id int64, code string) *models.ReplyIntentProfile {
	t.Helper()
	now := time.Now()
	profile := &models.ReplyIntentProfile{
		ID: id, Code: code, Name: code, IndustryCode: code, IntentDetectPrompt: "detect " + code,
		IntentJSONSchema: `{"type":"object"}`, Revision: 1, PublishedAt: &now, Status: enums.StatusOk,
		AuditFields: tenantManagementAuditFields(now),
	}
	if err := db.Create(profile).Error; err != nil {
		t.Fatalf("create industry profile %s: %v", code, err)
	}
	if err := db.Create(&models.ReplyIntentConfig{
		Code: "general", Name: "通用", IntentProfileID: profile.ID, ScopeType: "global", Status: enums.StatusOk,
		AuditFields: tenantManagementAuditFields(now),
	}).Error; err != nil {
		t.Fatalf("create industry intent %s: %v", code, err)
	}
	parent := &models.IndustryTagDefinition{
		IntentProfileID: profile.ID, Name: "分类", SemanticKey: "category." + code,
		DefinitionRevision: profile.Revision, Status: enums.StatusOk, AuditFields: tenantManagementAuditFields(now),
	}
	if err := db.Create(parent).Error; err != nil {
		t.Fatalf("create industry tag category %s: %v", code, err)
	}
	if err := db.Create(&models.IndustryTagDefinition{
		IntentProfileID: profile.ID, ParentID: parent.ID, Name: "标签", SemanticKey: code + ".tag",
		AIEnabled: true, DefinitionRevision: profile.Revision, Status: enums.StatusOk,
		AuditFields: tenantManagementAuditFields(now),
	}).Error; err != nil {
		t.Fatalf("create industry tag %s: %v", code, err)
	}
	return profile
}

func seedStrictHotelIndustryProfile(t *testing.T, db *gorm.DB, id int64) (*models.ReplyIntentProfile, []models.ReplyIntentConfig) {
	t.Helper()
	now := time.Now()
	profile := &models.ReplyIntentProfile{
		ID: id, Code: "hotel-strict-test", Name: "酒店行业测试", IndustryCode: replyintent.DefaultHotelIndustryCode,
		IntentDetectPrompt: "hotel detect", IntentJSONSchema: `{"type":"object"}`, Revision: 5,
		PublishedAt: &now, Status: enums.StatusOk, AuditFields: tenantManagementAuditFields(now),
	}
	if err := db.Create(profile).Error; err != nil {
		t.Fatalf("create strict hotel profile: %v", err)
	}
	codes := []string{"hotel_info", "hotel_variable", "service_request", "human_complaint_risk", "interaction"}
	configs := make([]models.ReplyIntentConfig, 0, len(codes))
	for i, code := range codes {
		item := models.ReplyIntentConfig{
			Code: code, Name: code, IntentProfileID: profile.ID, ScopeType: "global", Priority: 100 - i,
			MatchMode: "hybrid", Status: enums.StatusOk, AuditFields: tenantManagementAuditFields(now),
		}
		if err := db.Create(&item).Error; err != nil {
			t.Fatalf("create strict hotel intent %s: %v", code, err)
		}
		configs = append(configs, item)
	}
	leafIndex := 0
	for categoryIndex, leafCount := range []int{8, 8, 8, 7} {
		parent := &models.IndustryTagDefinition{
			IntentProfileID: profile.ID, Name: fmt.Sprintf("分类%d", categoryIndex+1),
			SemanticKey:        fmt.Sprintf("category.hotel.test.%d", categoryIndex+1),
			DefinitionRevision: profile.Revision, Status: enums.StatusOk, AuditFields: tenantManagementAuditFields(now),
		}
		if err := db.Create(parent).Error; err != nil {
			t.Fatalf("create strict hotel category: %v", err)
		}
		for i := 0; i < leafCount; i++ {
			leafIndex++
			conflictGroup := ""
			if leafIndex <= 8 {
				conflictGroup = fmt.Sprintf("hotel.test.conflict.%d", leafIndex)
			}
			leaf := &models.IndustryTagDefinition{
				IntentProfileID: profile.ID, ParentID: parent.ID, Name: fmt.Sprintf("标签%d", leafIndex),
				SemanticKey: fmt.Sprintf("hotel.test.tag.%d", leafIndex), ConflictGroup: conflictGroup,
				AIEnabled: true, DefinitionRevision: profile.Revision, Status: enums.StatusOk,
				AuditFields: tenantManagementAuditFields(now),
			}
			if err := db.Create(leaf).Error; err != nil {
				t.Fatalf("create strict hotel tag %d: %v", leafIndex, err)
			}
		}
	}
	return profile, configs
}

func tenantIndustryUpdateRequest(tenant *models.Tenant, profileID int64, confirmed bool, reason string) request.UpdateTenantRequest {
	return request.UpdateTenantRequest{
		ID: tenant.ID, IntentProfileID: profileID, ConfirmIndustryChange: confirmed, IndustryChangeReason: reason,
		LegalName: tenant.LegalName, ShortName: tenant.ShortName, RegistrationType: tenant.RegistrationType,
		RegistrationNo: tenant.RegistrationNo, ContactName: tenant.ContactName, ContactMobile: tenant.ContactMobile,
		ContactEmail: tenant.ContactEmail, Address: tenant.Address, Remark: tenant.Remark,
	}
}

func replyIntentProfileUpdateRequest(profile *models.ReplyIntentProfile) request.UpdateReplyIntentProfileRequest {
	return request.UpdateReplyIntentProfileRequest{
		ID: profile.ID,
		CreateReplyIntentProfileRequest: request.CreateReplyIntentProfileRequest{
			Code: profile.Code, Name: profile.Name, IndustryCode: profile.IndustryCode,
			Description: profile.Description, IntentDetectPrompt: profile.IntentDetectPrompt,
			IntentJSONSchema: profile.IntentJSONSchema, Status: profile.Status,
			SortNo: profile.SortNo, Remark: profile.Remark,
		},
	}
}

func replyIntentConfigUpdateRequest(item models.ReplyIntentConfig) request.UpdateReplyIntentConfigRequest {
	return request.UpdateReplyIntentConfigRequest{
		ID: item.ID,
		CreateReplyIntentConfigRequest: request.CreateReplyIntentConfigRequest{
			Code: item.Code, Name: item.Name, Description: item.Description, IntentProfileID: item.IntentProfileID,
			Priority: item.Priority, MatchMode: item.MatchMode, Keywords: item.Keywords,
			PositiveExamples: item.PositiveExamples, NegativeExamples: item.NegativeExamples,
			RequiredContext: item.RequiredContext, NeedsKnowledge: item.NeedsKnowledge,
			NeedsResource: item.NeedsResource, ResourceType: item.ResourceType, NeedsTool: item.NeedsTool,
			ToolCodes: item.ToolCodes, NeedsHumanRoute: item.NeedsHumanRoute,
			HumanRoutePolicy: item.HumanRoutePolicy, PromptPack: item.PromptPack,
			ReplyPlanTemplate: item.ReplyPlanTemplate, ValidationRules: item.ValidationRules,
			NoReplyWhenMatched: item.NoReplyWhenMatched, Status: item.Status, SortNo: item.SortNo, Remark: item.Remark,
		},
	}
}

func assertTenantIndustryState(t *testing.T, db *gorm.DB, tenantID, profileID, wantLogs int64) {
	t.Helper()
	tenant := loadTenantForIndustryTest(t, db, tenantID)
	if tenant.IntentProfileID != profileID {
		t.Fatalf("tenant industry profile = %d, want %d", tenant.IntentProfileID, profileID)
	}
	policy := &models.TenantCustomerTagPolicy{}
	if err := db.Where("tenant_id = ?", tenantID).First(policy).Error; err != nil {
		t.Fatalf("load tenant tag policy: %v", err)
	}
	if policy.IntentProfileID != profileID || policy.Status != enums.StatusOk {
		t.Fatalf("tenant tag policy does not follow industry: %+v", policy)
	}
	var logs int64
	if err := db.Model(&models.TenantIndustryChangeLog{}).Where("tenant_id = ?", tenantID).Count(&logs).Error; err != nil {
		t.Fatalf("count tenant industry logs: %v", err)
	}
	if logs != wantLogs {
		t.Fatalf("tenant industry log count = %d, want %d", logs, wantLogs)
	}
}

func assertHotelIntentMutationRolledBack(t *testing.T, db *gorm.DB, profileID, revision, wantConfigs int64) {
	t.Helper()
	profile := loadTenantIndustryProfile(t, db, profileID)
	if profile.Revision != revision {
		t.Fatalf("rejected hotel intent mutation changed revision to %d, want %d", profile.Revision, revision)
	}
	var configs int64
	if err := db.Model(&models.ReplyIntentConfig{}).
		Where("intent_profile_id = ? AND status = ?", profileID, enums.StatusOk).Count(&configs).Error; err != nil {
		t.Fatalf("count active hotel intents: %v", err)
	}
	if configs != wantConfigs {
		t.Fatalf("active hotel intent count = %d, want %d", configs, wantConfigs)
	}
}

func firstTenantIndustryLeafTag(t *testing.T, db *gorm.DB, tenantID, profileID int64) *models.Tag {
	t.Helper()
	item := &models.Tag{}
	if err := db.Where("tenant_id = ? AND intent_profile_id = ? AND parent_id > 0 AND status = ?", tenantID, profileID, enums.StatusOk).
		Order("id ASC").First(item).Error; err != nil {
		t.Fatalf("load tenant industry leaf tag: %v", err)
	}
	return item
}

func loadTenantForIndustryTest(t *testing.T, db *gorm.DB, tenantID int64) *models.Tenant {
	t.Helper()
	item := &models.Tenant{}
	if err := db.First(item, tenantID).Error; err != nil {
		t.Fatalf("load tenant: %v", err)
	}
	return item
}

func loadTenantIndustryProfile(t *testing.T, db *gorm.DB, profileID int64) *models.ReplyIntentProfile {
	t.Helper()
	item := &models.ReplyIntentProfile{}
	if err := db.First(item, profileID).Error; err != nil {
		t.Fatalf("load industry profile: %v", err)
	}
	return item
}
