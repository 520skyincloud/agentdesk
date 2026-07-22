package services

import (
	"strings"
	"testing"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/enums"

	"github.com/glebarez/sqlite"
	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

func TestModelProfileRequiresExactlyNineCompatibleNewAPISlots(t *testing.T) {
	template := &models.ModelProfileTemplate{Code: "standard", Name: "Standard", GatewayBaseURL: "https://newapi.example.com/v1"}
	slots := completeModelProfileSlotsForTest(0)
	if issues := ValidateModelProfileForPublication(template, slots); len(issues) != 0 {
		t.Fatalf("complete profile issues=%#v", issues)
	}

	missing := append([]models.ModelProfileSlot(nil), slots[:len(slots)-1]...)
	if issues := ValidateModelProfileForPublication(template, missing); !hasModelProfileIssue(issues, "缺少必需模型槽") {
		t.Fatalf("missing slot issues=%#v", issues)
	}

	wrongType := append([]models.ModelProfileSlot(nil), slots...)
	wrongType[0].ModelType = enums.AIModelTypeEmbedding
	if issues := ValidateModelProfileForPublication(template, wrongType); !hasModelProfileIssue(issues, "必须使用 llm 类型") {
		t.Fatalf("wrong type issues=%#v", issues)
	}

	wrongProvider := append([]models.ModelProfileSlot(nil), slots...)
	wrongProvider[0].Provider = "openai"
	if issues := ValidateModelProfileForPublication(template, wrongProvider); !hasModelProfileIssue(issues, "只允许统一 NewAPI") {
		t.Fatalf("wrong provider issues=%#v", issues)
	}

	wrongAPIMode := append([]models.ModelProfileSlot(nil), slots...)
	for i := range wrongAPIMode {
		if wrongAPIMode[i].UsageCode == enums.ModelUsageSlotASR {
			wrongAPIMode[i].APIMode = "chat_completions"
		}
	}
	if issues := ValidateModelProfileForPublication(template, wrongAPIMode); !hasModelProfileIssue(issues, "API 模式与模型用途不匹配") {
		t.Fatalf("wrong API mode issues=%#v", issues)
	}
}

func TestModelProfileRevisionBecomesImmutableCandidateAfterConfirmedPublish(t *testing.T) {
	setupModelProfileTestDB(t)
	operator := modelProfilePlatformOperator()
	input := completeModelProfileSlotRequestsForTest()
	created, err := ModelProfileService.Create(request.CreateModelProfileRequest{
		Code: "hotel-standard", Name: "酒店标准", GatewayBaseURL: "https://newapi.example.com/v1", Slots: input,
	}, operator)
	if err != nil {
		t.Fatalf("Create() error=%v", err)
	}
	if created.Template.Status != enums.ModelProfileStatusDraft || created.Template.Revision != 1 || len(created.Slots) != 9 {
		t.Fatalf("created profile=%#v slots=%d", created.Template, len(created.Slots))
	}
	if _, err := ModelProfileService.Publish(request.ModelProfileRevisionActionRequest{ID: created.Template.ID, ConfirmRevision: 2}, operator); err == nil {
		t.Fatal("publish with a stale confirmation revision must fail")
	}
	published, err := ModelProfileService.Publish(request.ModelProfileRevisionActionRequest{ID: created.Template.ID, ConfirmRevision: 1}, operator)
	if err != nil {
		t.Fatalf("Publish() error=%v", err)
	}
	if published.Template.Status != enums.ModelProfileStatusCandidate {
		t.Fatalf("published status=%q", published.Template.Status)
	}
	if _, err := ModelProfileService.Update(request.UpdateModelProfileRequest{
		ID: created.Template.ID, Name: "changed", GatewayBaseURL: created.Template.GatewayBaseURL, Slots: input,
	}, operator); err == nil {
		t.Fatal("candidate revision must be immutable")
	}
	next, err := ModelProfileService.Create(request.CreateModelProfileRequest{SourceTemplateID: created.Template.ID}, operator)
	if err != nil {
		t.Fatalf("Create(next revision) error=%v", err)
	}
	if next.Template.Code != created.Template.Code || next.Template.Revision != 2 || next.Template.Status != enums.ModelProfileStatusDraft {
		t.Fatalf("next revision=%#v", next.Template)
	}
}

func TestStoreProfilePendingAssignmentPreservesActiveRevision(t *testing.T) {
	db := setupModelProfileTestDB(t)
	operator := modelProfilePlatformOperator()
	tenant, store := createModelProfileTenantAndStore(t, db)
	active := createPersistedModelProfileForTest(t, db, "stable", 1, enums.ModelProfileStatusActive)
	candidate := createPersistedModelProfileForTest(t, db, "next", 1, enums.ModelProfileStatusCandidate)
	now := time.Now()
	assignment := &models.StoreModelProfileAssignment{
		TenantID: tenant.ID, StoreID: store.ID, TemplateID: active.ID, TemplateRevision: active.Revision,
		Status: enums.StoreModelAssignmentStatusReady, ReadinessStatus: "ready", AssignedAt: now,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(assignment).Error; err != nil {
		t.Fatalf("create active assignment: %v", err)
	}
	if err := StoreModelProfileAssignmentService.Assign(request.AssignStoreModelProfileRequest{
		TenantID: tenant.ID, StoreID: store.ID, TemplateID: candidate.ID, ConfirmRevision: candidate.Revision,
	}, operator); err != nil {
		t.Fatalf("Assign() error=%v", err)
	}
	var saved models.StoreModelProfileAssignment
	if err := db.First(&saved, "id = ?", assignment.ID).Error; err != nil {
		t.Fatal(err)
	}
	if saved.TemplateID != active.ID || saved.TemplateRevision != active.Revision || saved.Status != enums.StoreModelAssignmentStatusReady {
		t.Fatalf("active revision was overwritten: %#v", saved)
	}
	if saved.PendingTemplateID != candidate.ID || saved.PendingTemplateRevision != candidate.Revision || saved.ReadinessStatus != "pending" {
		t.Fatalf("pending revision was not recorded: %#v", saved)
	}
	if err := StoreModelProfileAssignmentService.Assign(request.AssignStoreModelProfileRequest{
		TenantID: tenant.ID, StoreID: store.ID, TemplateID: active.ID, ConfirmRevision: active.Revision,
	}, operator); err != nil {
		t.Fatalf("cancel pending assignment: %v", err)
	}
	if err := db.First(&saved, "id = ?", assignment.ID).Error; err != nil {
		t.Fatal(err)
	}
	if saved.TemplateID != active.ID || saved.PendingTemplateID != 0 || saved.Status != enums.StoreModelAssignmentStatusReady || saved.ReadinessStatus != "ready" {
		t.Fatalf("selecting active revision must only cancel pending: %#v", saved)
	}
	if err := db.Model(&saved).Updates(map[string]any{
		"status": enums.StoreModelAssignmentStatusBlocked, "readiness_status": "blocked", "last_error_message": "active credential failed",
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := StoreModelProfileAssignmentService.Assign(request.AssignStoreModelProfileRequest{
		TenantID: tenant.ID, StoreID: store.ID, TemplateID: candidate.ID, ConfirmRevision: candidate.Revision,
	}, operator); err != nil {
		t.Fatalf("assign candidate while active is blocked: %v", err)
	}
	if err := db.First(&saved, "id = ?", assignment.ID).Error; err != nil {
		t.Fatal(err)
	}
	if saved.TemplateID != active.ID || saved.Status != enums.StoreModelAssignmentStatusBlocked || saved.PendingTemplateID != candidate.ID {
		t.Fatalf("pending assignment changed blocked active state: %#v", saved)
	}

	tenantOperator := &dto.AuthPrincipal{UserID: 2, TenantID: tenant.ID, ActiveTenantID: tenant.ID, Username: "tenant-admin"}
	otherTenant := &models.Tenant{TenantCode: "other", LegalName: "Other", ShortName: "Other", RegistrationType: "credit_code", RegistrationNo: "OTHER", Status: enums.StatusOk}
	if err := db.Create(otherTenant).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := StoreModelProfileAssignmentService.List(request.GetStoreModelProfileAssignmentsRequest{TenantID: otherTenant.ID}, tenantOperator); err == nil {
		t.Fatal("tenant operator must not cross tenant scope")
	}
}

func TestModelCallResolverNeverFallsBackToLegacyAIConfig(t *testing.T) {
	db := setupModelProfileTestDB(t)
	tenant, store := createModelProfileTenantAndStore(t, db)
	legacy := &models.AIConfig{
		Name: "legacy", Provider: enums.AIProviderOpenAI, BaseURL: "https://legacy.example.com/v1", APIKey: "legacy-key",
		ModelType: enums.AIModelTypeLLM, ModelName: "legacy-model", Status: enums.StatusOk,
	}
	if err := db.Create(legacy).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.TenantAIModelGrant{TenantID: tenant.ID, AIConfigID: legacy.ID, Status: enums.StatusOk}).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := ModelCallResolverService.Resolve(tenant.ID, store.ID, enums.ModelUsageSlotReplyLLM); err == nil {
		t.Fatal("resolver must fail without the sole Store Profile assignment")
	}

	profile := createPersistedModelProfileForTest(t, db, "runtime", 1, enums.ModelProfileStatusActive)
	now := time.Now()
	assignment := &models.StoreModelProfileAssignment{
		TenantID: tenant.ID, StoreID: store.ID, TemplateID: profile.ID, TemplateRevision: profile.Revision,
		Status: enums.StoreModelAssignmentStatusReady, ReadinessStatus: "ready", AssignedAt: now,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(assignment).Error; err != nil {
		t.Fatal(err)
	}
	credential := &models.StoreModelCredential{
		TenantID: tenant.ID, StoreID: store.ID, EncryptedKey: "ciphertext", KeyNonce: "nonce",
		CredentialRevision: 4, Status: enums.StoreCredentialStatusActive,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(credential).Error; err != nil {
		t.Fatal(err)
	}
	resolved, err := ModelCallResolverService.Resolve(tenant.ID, store.ID, enums.ModelUsageSlotReplyLLM)
	if err != nil {
		t.Fatalf("Resolve() error=%v", err)
	}
	if resolved.ProfileID != profile.ID || resolved.ModelName != "reply_llm-model" || resolved.CredentialRevision != 4 {
		t.Fatalf("resolved=%#v", resolved)
	}
	if resolved.ModelName == legacy.ModelName || resolved.GatewayBaseURL == legacy.BaseURL {
		t.Fatalf("legacy configuration leaked into resolver: %#v", resolved)
	}
}

func setupModelProfileTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{
		NamingStrategy: schema.NamingStrategy{TablePrefix: "t_", SingularTable: true},
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(
		&models.Tenant{}, &models.Store{}, &models.ModelProfileTemplate{}, &models.ModelProfileSlot{},
		&models.StoreModelProfileAssignment{}, &models.StoreModelCredential{}, &models.AIConfig{},
		&models.TenantAIModelGrant{}, &models.StoreAIModelSetting{}, &models.Conversation{}, &models.ConversationRouteState{},
	); err != nil {
		t.Fatalf("AutoMigrate() error=%v", err)
	}
	sqls.SetDB(db)
	t.Cleanup(func() {
		sqls.SetDB(nil)
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}

func modelProfilePlatformOperator() *dto.AuthPrincipal {
	return &dto.AuthPrincipal{UserID: 1, Username: "platform-admin", IsPlatformAccount: true}
}

func createModelProfileTenantAndStore(t *testing.T, db *gorm.DB) (*models.Tenant, *models.Store) {
	t.Helper()
	tenant := &models.Tenant{
		TenantCode: "tenant-" + strings.ToLower(t.Name()), LegalName: "Tenant", ShortName: "Tenant",
		RegistrationType: "credit_code", RegistrationNo: "REG-" + t.Name(), Status: enums.StatusOk,
	}
	if err := db.Create(tenant).Error; err != nil {
		t.Fatal(err)
	}
	store := &models.Store{TenantID: tenant.ID, StoreCode: "store-1", Name: "Store 1", Status: enums.StatusOk}
	if err := db.Create(store).Error; err != nil {
		t.Fatal(err)
	}
	return tenant, store
}

func createPersistedModelProfileForTest(t *testing.T, db *gorm.DB, code string, revision int64, status enums.ModelProfileStatus) *models.ModelProfileTemplate {
	t.Helper()
	now := time.Now()
	template := &models.ModelProfileTemplate{
		Code: code, Name: code, Revision: revision, GatewayBaseURL: "https://newapi.example.com/v1", Status: status,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(template).Error; err != nil {
		t.Fatal(err)
	}
	slots := completeModelProfileSlotsForTest(template.ID)
	for i := range slots {
		slots[i].AuditFields = models.AuditFields{CreatedAt: now, UpdatedAt: now}
	}
	if err := db.Create(&slots).Error; err != nil {
		t.Fatal(err)
	}
	return template
}

func completeModelProfileSlotRequestsForTest() []request.ModelProfileSlotRequest {
	requests := defaultModelProfileSlotRequests()
	for i := range requests {
		requests[i].ModelName = requests[i].UsageCode + "-model"
		requests[i].TimeoutMS = 30000
		if requests[i].ModelType == string(enums.AIModelTypeLLM) || requests[i].ModelType == string(enums.AIModelTypeVision) {
			requests[i].MaxContextTokens = 8192
			requests[i].MaxOutputTokens = 1024
		}
		if requests[i].ModelType == string(enums.AIModelTypeEmbedding) {
			requests[i].Dimension = 1024
		}
		if requests[i].UsageCode == string(enums.ModelUsageSlotCustomerTag) {
			requests[i].SchemaVersion = "customer_tag_evolution.v1"
			requests[i].PromptTemplate = "Return only allowed tags."
			requests[i].JSONSchema = `{"type":"object"}`
		}
	}
	return requests
}

func completeModelProfileSlotsForTest(templateID int64) []models.ModelProfileSlot {
	return buildModelProfileSlots(completeModelProfileSlotRequestsForTest(), templateID, modelProfilePlatformOperator(), time.Now())
}

func hasModelProfileIssue(items []ModelProfileValidationIssue, contains string) bool {
	for _, item := range items {
		if strings.Contains(item.Message, contains) {
			return true
		}
	}
	return false
}
