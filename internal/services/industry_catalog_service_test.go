package services

import (
	"strings"
	"testing"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/enums"

	"gorm.io/gorm"
)

func TestIndustryProfileLifecycleRequiresDraftValidationAndConfirmedRevision(t *testing.T) {
	db, operator := setupTenantManagementTestDB(t)
	profile, err := ReplyIntentProfileService.CreateReplyIntentProfile(request.CreateReplyIntentProfileRequest{
		Code:               "lifecycle-retail",
		Name:               "零售行业",
		IndustryCode:       "retail",
		IntentDetectPrompt: "识别用户意图",
		IntentJSONSchema:   `{"primaryIntent":"","confidence":0,"intentTasks":[],"reason":""}`,
		Status:             enums.StatusOk,
	}, operator)
	if err != nil {
		t.Fatalf("create industry profile: %v", err)
	}
	if profile.Status != enums.StatusDisabled || profile.Revision != 1 || profile.PublishedAt != nil || profile.PublishedBy != 0 {
		t.Fatalf("new industry profile must start as revision 1 draft: %+v", profile)
	}

	if _, err := ReplyIntentConfigService.CreateReplyIntentConfig(request.CreateReplyIntentConfigRequest{
		Code: "product_question", Name: "商品咨询", IntentProfileID: profile.ID, Status: enums.StatusOk,
	}, operator); err != nil {
		t.Fatalf("create industry intent: %v", err)
	}
	category, err := IndustryTagDefinitionService.Create(request.CreateIndustryTagDefinitionRequest{
		IntentProfileID: profile.ID, Name: "客户阶段", SemanticKey: "category.customer-stage", Status: enums.StatusOk,
	}, operator)
	if err != nil {
		t.Fatalf("create industry tag category: %v", err)
	}
	if _, err := IndustryTagDefinitionService.Create(request.CreateIndustryTagDefinitionRequest{
		IntentProfileID: profile.ID, ParentID: category.ID, Name: "新客",
		SemanticKey: "customer-stage.new", AIEnabled: true, Status: enums.StatusOk,
	}, operator); err != nil {
		t.Fatalf("create industry tag: %v", err)
	}

	draft := loadTenantIndustryProfile(t, db, profile.ID)
	validation, err := ReplyIntentProfileService.TestReplyIntentProfile(profile.ID)
	if err != nil {
		t.Fatalf("test industry profile: %v", err)
	}
	if !validation.Valid || validation.Revision != draft.Revision || validation.ActiveIntentCount != 1 ||
		validation.TagCategoryCount != 1 || validation.TagCount != 1 || len(validation.Errors) != 0 {
		t.Fatalf("unexpected valid industry profile result: %+v", validation)
	}

	if _, err := ReplyIntentProfileService.PublishReplyIntentProfile(request.PublishReplyIntentProfileRequest{
		ID: profile.ID, Revision: draft.Revision,
	}, operator); err == nil {
		t.Fatal("expected publish without revision confirmation to fail")
	}
	if _, err := ReplyIntentProfileService.PublishReplyIntentProfile(request.PublishReplyIntentProfileRequest{
		ID: profile.ID, Revision: draft.Revision - 1, ConfirmRevision: true,
	}, operator); err == nil {
		t.Fatal("expected publish with stale revision to fail")
	}
	published, err := ReplyIntentProfileService.PublishReplyIntentProfile(request.PublishReplyIntentProfileRequest{
		ID: profile.ID, Revision: draft.Revision, ConfirmRevision: true,
	}, operator)
	if err != nil {
		t.Fatalf("publish tested industry profile: %v", err)
	}
	if published.Status != enums.StatusOk || published.PublishedAt == nil || published.PublishedBy != operator.UserID {
		t.Fatalf("industry profile was not published with audit evidence: %+v", published)
	}
}

func TestIndustryProfileValidationReportsErrorsAndSchemaWarnings(t *testing.T) {
	_, operator := setupTenantManagementTestDB(t)
	profile, err := ReplyIntentProfileService.CreateReplyIntentProfile(request.CreateReplyIntentProfileRequest{
		Code: "invalid-profile", Name: "不完整行业", IndustryCode: "incomplete", IntentJSONSchema: `{}`,
	}, operator)
	if err != nil {
		t.Fatalf("create incomplete industry profile: %v", err)
	}

	validation, err := ReplyIntentProfileService.TestReplyIntentProfile(profile.ID)
	if err != nil {
		t.Fatalf("test incomplete industry profile: %v", err)
	}
	if validation.Valid {
		t.Fatalf("incomplete industry profile unexpectedly passed: %+v", validation)
	}
	for _, want := range []string{
		"IntentDetect 提示词不能为空",
		"所选行业尚未配置可用意图分类",
		"所选行业尚未配置完整客户标签目录",
	} {
		if !containsStringFragment(validation.Errors, want) {
			t.Fatalf("validation errors %v do not contain %q", validation.Errors, want)
		}
	}
	for _, field := range []string{"primaryIntent", "confidence", "intentTasks", "reason"} {
		if !containsStringFragment(validation.Warnings, field) {
			t.Fatalf("validation warnings %v do not contain schema field %q", validation.Warnings, field)
		}
	}
}

func TestIndustryTagDefinitionIdentityIsImmutable(t *testing.T) {
	db, operator := setupTenantManagementTestDB(t)
	profile := createDraftIndustryProfileForTagTest(t, operator, "immutable-primary")
	otherProfile := createDraftIndustryProfileForTagTest(t, operator, "immutable-secondary")
	category := createIndustryTagCategoryForTest(t, operator, profile.ID, "category.immutable-primary")
	otherCategory := createIndustryTagCategoryForTest(t, operator, profile.ID, "category.immutable-other")
	tag, err := IndustryTagDefinitionService.Create(request.CreateIndustryTagDefinitionRequest{
		IntentProfileID: profile.ID, ParentID: category.ID, Name: "稳定标签",
		SemanticKey: "immutable-primary.stable", AIEnabled: true, Status: enums.StatusOk,
	}, operator)
	if err != nil {
		t.Fatalf("create immutable industry tag: %v", err)
	}

	cases := []struct {
		name   string
		mutate func(*request.UpdateIndustryTagDefinitionRequest)
	}{
		{
			name: "semantic key",
			mutate: func(req *request.UpdateIndustryTagDefinitionRequest) {
				req.SemanticKey = "immutable-primary.changed"
			},
		},
		{
			name: "parent",
			mutate: func(req *request.UpdateIndustryTagDefinitionRequest) {
				req.ParentID = otherCategory.ID
			},
		},
		{
			name: "industry profile",
			mutate: func(req *request.UpdateIndustryTagDefinitionRequest) {
				req.IntentProfileID = otherProfile.ID
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			update := industryTagDefinitionUpdateRequest(tag)
			tc.mutate(&update)
			if err := IndustryTagDefinitionService.Update(update, operator); err == nil {
				t.Fatalf("expected changing industry tag %s to fail", tc.name)
			}
		})
	}

	reloaded := loadIndustryTagDefinitionForTest(t, db, tag.ID)
	if reloaded.IntentProfileID != tag.IntentProfileID || reloaded.ParentID != tag.ParentID ||
		reloaded.SemanticKey != tag.SemanticKey || reloaded.Name != tag.Name {
		t.Fatalf("rejected identity mutation changed industry tag: before=%+v after=%+v", tag, reloaded)
	}
}

func TestPublishedIndustryTagMutationCreatesDraftButBoundMutationRollsBack(t *testing.T) {
	db, operator := setupTenantManagementTestDB(t)
	profile := createValidPublishedIndustryForTagTest(t, db, operator, "published-tag-edit")
	tag := firstIndustryTagDefinitionForTest(t, db, profile.ID)
	publishedRevision := profile.Revision

	update := industryTagDefinitionUpdateRequest(tag)
	update.Name = "允许编辑后的名称"
	update.Aliases = "别名一,别名二"
	if err := IndustryTagDefinitionService.Update(update, operator); err != nil {
		t.Fatalf("update unbound published industry tag: %v", err)
	}
	draft := loadTenantIndustryProfile(t, db, profile.ID)
	if draft.Status != enums.StatusDisabled || draft.Revision != publishedRevision+1 ||
		draft.PublishedAt != nil || draft.PublishedBy != 0 {
		t.Fatalf("published industry mutation did not create a new draft revision: %+v", draft)
	}
	edited := loadIndustryTagDefinitionForTest(t, db, tag.ID)
	if edited.Name != update.Name || edited.Aliases != update.Aliases || edited.DefinitionRevision != draft.Revision {
		t.Fatalf("industry tag edit or revision synchronization missing: %+v", edited)
	}

	published, err := ReplyIntentProfileService.PublishReplyIntentProfile(request.PublishReplyIntentProfileRequest{
		ID: draft.ID, Revision: draft.Revision, ConfirmRevision: true,
	}, operator)
	if err != nil {
		t.Fatalf("republish edited industry profile: %v", err)
	}
	tenantReq := tenantManagementCreateRequest("bound-tag-catalog", "91350100MA8T4A6G89")
	tenantReq.IntentProfileID = published.ID
	if _, err := TenantService.CreateTenant(tenantReq, operator); err != nil {
		t.Fatalf("bind tenant to published industry profile: %v", err)
	}

	boundUpdate := industryTagDefinitionUpdateRequest(edited)
	boundUpdate.Name = "不应落库的名称"
	if err := IndustryTagDefinitionService.Update(boundUpdate, operator); err == nil {
		t.Fatal("expected mutation of a bound published industry tag to fail")
	}
	if _, err := IndustryTagDefinitionService.Create(request.CreateIndustryTagDefinitionRequest{
		IntentProfileID: published.ID, ParentID: edited.ParentID, Name: "不应新增",
		SemanticKey: "published-tag-edit.rejected", AIEnabled: true, Status: enums.StatusOk,
	}, operator); err == nil {
		t.Fatal("expected adding a tag to a bound published industry to fail")
	}

	reloadedProfile := loadTenantIndustryProfile(t, db, published.ID)
	reloadedTag := loadIndustryTagDefinitionForTest(t, db, tag.ID)
	if reloadedProfile.Status != enums.StatusOk || reloadedProfile.Revision != published.Revision ||
		reloadedTag.Name != edited.Name {
		t.Fatalf("rejected bound mutation changed persisted catalog: profile=%+v tag=%+v", reloadedProfile, reloadedTag)
	}
	var rejectedTagCount int64
	if err := db.Model(&models.IndustryTagDefinition{}).
		Where("intent_profile_id = ? AND semantic_key = ?", published.ID, "published-tag-edit.rejected").
		Count(&rejectedTagCount).Error; err != nil {
		t.Fatalf("count rolled back industry tag: %v", err)
	}
	if rejectedTagCount != 0 {
		t.Fatalf("bound industry tag create was not rolled back: count=%d", rejectedTagCount)
	}
}

func TestIndustryTagCategoryCannotBeDisabledWithActiveChildren(t *testing.T) {
	db, operator := setupTenantManagementTestDB(t)
	profile := createDraftIndustryProfileForTagTest(t, operator, "category-disable")
	category := createIndustryTagCategoryForTest(t, operator, profile.ID, "category.category-disable")
	child, err := IndustryTagDefinitionService.Create(request.CreateIndustryTagDefinitionRequest{
		IntentProfileID: profile.ID, ParentID: category.ID, Name: "启用子标签",
		SemanticKey: "category-disable.active-child", AIEnabled: true, Status: enums.StatusOk,
	}, operator)
	if err != nil {
		t.Fatalf("create active child tag: %v", err)
	}

	disableCategory := industryTagDefinitionUpdateRequest(category)
	disableCategory.Status = enums.StatusDisabled
	if err := IndustryTagDefinitionService.Update(disableCategory, operator); err == nil {
		t.Fatal("expected category disable with active child to fail")
	}
	if loadIndustryTagDefinitionForTest(t, db, category.ID).Status != enums.StatusOk {
		t.Fatal("rejected category disable changed persisted status")
	}

	disableChild := industryTagDefinitionUpdateRequest(child)
	disableChild.Status = enums.StatusDisabled
	if err := IndustryTagDefinitionService.Update(disableChild, operator); err != nil {
		t.Fatalf("disable child tag: %v", err)
	}
	category = loadIndustryTagDefinitionForTest(t, db, category.ID)
	disableCategory = industryTagDefinitionUpdateRequest(category)
	disableCategory.Status = enums.StatusDisabled
	if err := IndustryTagDefinitionService.Update(disableCategory, operator); err != nil {
		t.Fatalf("disable category after all children were disabled: %v", err)
	}
	if loadIndustryTagDefinitionForTest(t, db, category.ID).Status != enums.StatusDisabled {
		t.Fatal("category did not persist disabled status")
	}
}

func createDraftIndustryProfileForTagTest(
	t *testing.T,
	operator *dto.AuthPrincipal,
	code string,
) *models.ReplyIntentProfile {
	t.Helper()
	profile, err := ReplyIntentProfileService.CreateReplyIntentProfile(request.CreateReplyIntentProfileRequest{
		Code:               code,
		Name:               code,
		IndustryCode:       code,
		IntentDetectPrompt: "识别用户意图",
		IntentJSONSchema:   `{"primaryIntent":"","confidence":0,"intentTasks":[],"reason":""}`,
		Status:             enums.StatusDisabled,
	}, operator)
	if err != nil {
		t.Fatalf("create draft industry profile %s: %v", code, err)
	}
	return profile
}

func createValidPublishedIndustryForTagTest(
	t *testing.T,
	db *gorm.DB,
	operator *dto.AuthPrincipal,
	code string,
) *models.ReplyIntentProfile {
	t.Helper()
	profile := createDraftIndustryProfileForTagTest(t, operator, code)
	if _, err := ReplyIntentConfigService.CreateReplyIntentConfig(request.CreateReplyIntentConfigRequest{
		Code: "general", Name: "通用咨询", IntentProfileID: profile.ID, Status: enums.StatusOk,
	}, operator); err != nil {
		t.Fatalf("create industry intent for %s: %v", code, err)
	}
	category := createIndustryTagCategoryForTest(t, operator, profile.ID, "category."+code)
	if _, err := IndustryTagDefinitionService.Create(request.CreateIndustryTagDefinitionRequest{
		IntentProfileID: profile.ID, ParentID: category.ID, Name: "通用标签",
		SemanticKey: code + ".general", AIEnabled: true, Status: enums.StatusOk,
	}, operator); err != nil {
		t.Fatalf("create industry tag for %s: %v", code, err)
	}
	draft := loadTenantIndustryProfile(t, db, profile.ID)
	published, err := ReplyIntentProfileService.PublishReplyIntentProfile(request.PublishReplyIntentProfileRequest{
		ID: draft.ID, Revision: draft.Revision, ConfirmRevision: true,
	}, operator)
	if err != nil {
		t.Fatalf("publish industry profile %s: %v", code, err)
	}
	return published
}

func createIndustryTagCategoryForTest(
	t *testing.T,
	operator *dto.AuthPrincipal,
	profileID int64,
	semanticKey string,
) *models.IndustryTagDefinition {
	t.Helper()
	item, err := IndustryTagDefinitionService.Create(request.CreateIndustryTagDefinitionRequest{
		IntentProfileID: profileID,
		Name:            "标签分类",
		SemanticKey:     semanticKey,
		Status:          enums.StatusOk,
	}, operator)
	if err != nil {
		t.Fatalf("create industry tag category %s: %v", semanticKey, err)
	}
	return item
}

func industryTagDefinitionUpdateRequest(item *models.IndustryTagDefinition) request.UpdateIndustryTagDefinitionRequest {
	return request.UpdateIndustryTagDefinitionRequest{
		ID: item.ID, IntentProfileID: item.IntentProfileID, ParentID: item.ParentID,
		Name: item.Name, SemanticKey: item.SemanticKey, Aliases: item.Aliases,
		ConflictGroup: item.ConflictGroup, ApplicableScene: item.ApplicableScene,
		AIEnabled: item.AIEnabled, ReplyEnabled: item.ReplyEnabled,
		SortNo: item.SortNo, Status: item.Status,
	}
}

func firstIndustryTagDefinitionForTest(t *testing.T, db *gorm.DB, profileID int64) *models.IndustryTagDefinition {
	t.Helper()
	item := &models.IndustryTagDefinition{}
	if err := db.Where("intent_profile_id = ? AND parent_id > 0 AND status = ?", profileID, enums.StatusOk).
		Order("id ASC").First(item).Error; err != nil {
		t.Fatalf("load active industry tag: %v", err)
	}
	return item
}

func loadIndustryTagDefinitionForTest(t *testing.T, db *gorm.DB, id int64) *models.IndustryTagDefinition {
	t.Helper()
	item := &models.IndustryTagDefinition{}
	if err := db.First(item, id).Error; err != nil {
		t.Fatalf("load industry tag definition %d: %v", id, err)
	}
	return item
}

func containsStringFragment(values []string, fragment string) bool {
	for _, value := range values {
		if strings.Contains(value, fragment) {
			return true
		}
	}
	return false
}
