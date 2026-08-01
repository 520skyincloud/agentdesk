package services

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/config"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/securex"
	"agent-desk/internal/repositories"

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

func TestModelProfileBootstrapExceptionRequiresNoActiveCredential(t *testing.T) {
	db := setupModelProfileTestDB(t)
	operator := modelProfilePlatformOperator()
	created, err := ModelProfileService.Create(request.CreateModelProfileRequest{
		Code: "bootstrap-guard", Name: "Bootstrap Guard",
		GatewayBaseURL: "https://newapi.example.com/v1",
		Slots:          completeModelProfileSlotRequestsForTest(),
	}, operator)
	if err != nil {
		t.Fatalf("Create() error=%v", err)
	}
	tenant, store := createModelProfileTenantAndStore(t, db)
	if err := db.Create(&models.StoreModelCredential{
		TenantID: tenant.ID, StoreID: store.ID,
		EncryptedKey: "existing-ciphertext", CredentialRevision: 1,
		Status: enums.StoreCredentialStatusActive,
	}).Error; err != nil {
		t.Fatal(err)
	}

	catalog, err := ModelProfileService.GetCatalog(request.GetModelProfileCatalogRequest{}, operator)
	if err != nil {
		t.Fatalf("GetCatalog() error=%v", err)
	}
	if !catalog.TestRequired || len(catalog.TestTargets) != 0 {
		t.Fatalf("active but unusable credential gate=%t targets=%d", catalog.TestRequired, len(catalog.TestTargets))
	}
	if _, err := ModelProfileService.Publish(
		request.ModelProfileRevisionActionRequest{ID: created.Template.ID, ConfirmRevision: created.Template.Revision},
		operator,
	); err == nil || !strings.Contains(err.Error(), "真实九槽测试") {
		t.Fatalf("active credential incorrectly used bootstrap exception, error=%v", err)
	}
}

func TestModelProfileConfigurationEditInvalidatesPreviousTestEvidence(t *testing.T) {
	fixture := setupStoreCredentialFixture(t)
	seedActiveStoreCredential(t, fixture, "sk-active-profile-test", 3)
	draft, _ := createStoreCredentialProfile(t, fixture.db, "profile-under-test", 1, enums.ModelProfileStatusDraft)
	validator := &storeCredentialValidatorStub{}
	service := &modelProfileService{validator: validator}

	tested, err := service.Test(
		context.Background(),
		request.TestModelProfileRequest{ID: draft.ID, TenantID: fixture.tenant.ID, StoreID: fixture.store.ID, StoreStaffBindingID: fixture.binding.ID},
		modelProfilePlatformOperator(),
		StoreCredentialRequestMeta{RequestID: "profile-test-before-edit"},
	)
	if err != nil {
		t.Fatalf("Test() error=%v", err)
	}
	if tested.TestRun == nil || tested.TestRun.Status != enums.ModelProfileTestStatusPassed || validator.callCount() != 1 {
		t.Fatalf("unexpected test evidence=%#v validator calls=%d", tested.TestRun, validator.callCount())
	}
	if err := repositories.ModelProfileTemplateRepository.Updates(fixture.db, draft.ID, map[string]any{
		"description": "configuration changed after the passing test",
	}); err != nil {
		t.Fatal(err)
	}
	updated := repositories.ModelProfileTemplateRepository.Get(fixture.db, draft.ID)
	updatedSlots := repositories.ModelProfileSlotRepository.FindByTemplateID(fixture.db, draft.ID)
	if currentDigest := modelProfileConfigurationDigest(updated, updatedSlots); currentDigest == tested.TestRun.ConfigDigest {
		t.Fatal("configuration edit did not change the evidence digest")
	}
	if _, err := service.Publish(
		request.ModelProfileRevisionActionRequest{ID: draft.ID, ConfirmRevision: draft.Revision},
		modelProfilePlatformOperator(),
	); err == nil || !strings.Contains(err.Error(), "真实九槽测试") {
		t.Fatalf("edited profile reused stale test evidence, error=%v", err)
	}
}

func TestModelProfileCrossGatewayRejectedBeforeCredentialValidation(t *testing.T) {
	fixture := setupStoreCredentialFixture(t)
	seedActiveStoreCredential(t, fixture, "sk-cross-gateway", 2)
	draft, _ := createStoreCredentialProfile(t, fixture.db, "cross-gateway", 1, enums.ModelProfileStatusDraft)
	if err := repositories.ModelProfileTemplateRepository.Updates(fixture.db, draft.ID, map[string]any{
		"gateway_base_url": "https://different-newapi.example.com/v1",
	}); err != nil {
		t.Fatal(err)
	}
	validator := &storeCredentialValidatorStub{}
	service := &modelProfileService{validator: validator}

	if _, err := service.Test(
		context.Background(),
		request.TestModelProfileRequest{ID: draft.ID, TenantID: fixture.tenant.ID, StoreID: fixture.store.ID, StoreStaffBindingID: fixture.binding.ID},
		modelProfilePlatformOperator(),
		StoreCredentialRequestMeta{RequestID: "profile-test-cross-gateway"},
	); err == nil || !strings.Contains(err.Error(), "相同的统一 NewAPI 网关") {
		t.Fatalf("cross-gateway test error=%v", err)
	}
	if validator.callCount() != 0 {
		t.Fatalf("credential reached validator before gateway rejection, calls=%d", validator.callCount())
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
	if saved.PendingTemplateID != candidate.ID || saved.PendingTemplateRevision != candidate.Revision || saved.ReadinessStatus != "ready" {
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

func TestStoreModelProfileListIncludesAllActiveCredentialBindings(t *testing.T) {
	db := setupModelProfileTestDB(t)
	tenant, store := createModelProfileTenantAndStore(t, db)
	secondUser := &models.User{
		TenantID: tenant.ID, Username: "second-staff-" + strings.ToLower(t.Name()),
		Nickname: "Second Store Staff", Status: enums.StatusOk,
	}
	if err := db.Create(secondUser).Error; err != nil {
		t.Fatal(err)
	}
	secondBinding := &models.StoreStaffBinding{
		TenantID: tenant.ID, UserID: secondUser.ID, ActiveUserID: positiveInt64Pointer(secondUser.ID),
		StoreID: store.ID, Status: enums.StatusOk,
	}
	if err := db.Create(secondBinding).Error; err != nil {
		t.Fatal(err)
	}

	data, err := StoreModelProfileAssignmentService.List(
		request.GetStoreModelProfileAssignmentsRequest{TenantID: tenant.ID},
		modelProfilePlatformOperator(),
	)
	if err != nil {
		t.Fatalf("List() error=%v", err)
	}
	if len(data.Stores) != 1 || len(data.Stores[0].CredentialBindings) != 2 {
		t.Fatalf("credential bindings=%#v", data.Stores)
	}
	if data.Stores[0].CredentialBindings[0].AccountName != "Store Staff" ||
		data.Stores[0].CredentialBindings[1].AccountName != "Second Store Staff" {
		t.Fatalf("credential binding labels=%#v", data.Stores[0].CredentialBindings)
	}
}

func TestModelCallResolverRequiresStoreProfileAndCredential(t *testing.T) {
	db := setupModelProfileTestDB(t)
	tenant, store := createModelProfileTenantAndStore(t, db)
	binding := modelProfileTestBinding(t, db, tenant.ID, store.ID)
	if _, err := ModelCallResolverService.ResolveForBinding(tenant.ID, store.ID, binding.ID, enums.ModelUsageSlotReplyLLM); err == nil {
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
		TenantID: tenant.ID, StoreID: store.ID, StoreStaffBindingID: binding.ID, CredentialRevision: 4, Status: enums.StoreCredentialStatusActive,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	cipher, cipherErr := securex.NewAESGCM(config.Current().StoreCredential.MasterKey)
	if cipherErr != nil {
		t.Fatal(cipherErr)
	}
	credential.EncryptedKey, credential.KeyNonce, cipherErr = cipher.Encrypt("new-runtime-key", storeBindingCredentialAAD(tenant.ID, store.ID, binding.ID, credential.CredentialRevision))
	if cipherErr != nil {
		t.Fatal(cipherErr)
	}
	credential.KeyFingerprint = securex.Fingerprint("new-runtime-key")
	credential.CipherVersion = storeBindingCredentialCipherVersion
	credential.MasterKeyID = config.Current().StoreCredential.MasterKeyID
	if err := db.Create(credential).Error; err != nil {
		t.Fatal(err)
	}
	resolved, err := ModelCallResolverService.ResolveForBinding(tenant.ID, store.ID, binding.ID, enums.ModelUsageSlotReplyLLM)
	if err != nil {
		t.Fatalf("Resolve() error=%v", err)
	}
	if resolved.ProfileID != profile.ID || resolved.ModelName != "reply_llm-model" || resolved.CredentialRevision != 4 {
		t.Fatalf("resolved=%#v", resolved)
	}
	if err := db.Model(store).Update("status", enums.StatusDisabled).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := ModelCallResolverService.ResolveForBinding(tenant.ID, store.ID, binding.ID, enums.ModelUsageSlotReplyLLM); err == nil {
		t.Fatal("disabled Store must not resolve an active model credential")
	}
}

func TestModelCallResolverKnowledgeDebugRequiresExactBinding(t *testing.T) {
	db := setupModelProfileTestDB(t)
	tenant, store := createModelProfileTenantAndStore(t, db)
	binding := modelProfileTestBinding(t, db, tenant.ID, store.ID)
	now := time.Now()
	profile := createPersistedModelProfileForTest(t, db, "knowledge-debug", 1, enums.ModelProfileStatusActive)
	if err := db.Create(&models.StoreModelProfileAssignment{
		TenantID: tenant.ID, StoreID: store.ID, TemplateID: profile.ID, TemplateRevision: profile.Revision,
		Status: enums.StoreModelAssignmentStatusReady, ReadinessStatus: "ready", AssignedAt: now,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}).Error; err != nil {
		t.Fatal(err)
	}
	credential := &models.StoreModelCredential{
		TenantID: tenant.ID, StoreID: store.ID, StoreStaffBindingID: binding.ID,
		CredentialRevision: 7, Status: enums.StoreCredentialStatusActive,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	cipher, err := securex.NewAESGCM(config.Current().StoreCredential.MasterKey)
	if err != nil {
		t.Fatal(err)
	}
	credential.EncryptedKey, credential.KeyNonce, err = cipher.Encrypt(
		"knowledge-debug-key",
		storeBindingCredentialAAD(tenant.ID, store.ID, binding.ID, credential.CredentialRevision),
	)
	if err != nil {
		t.Fatal(err)
	}
	credential.KeyFingerprint = securex.Fingerprint("knowledge-debug-key")
	credential.CipherVersion = storeBindingCredentialCipherVersion
	credential.MasterKeyID = config.Current().StoreCredential.MasterKeyID
	if err := db.Create(credential).Error; err != nil {
		t.Fatal(err)
	}

	secondUser := &models.User{
		TenantID: tenant.ID, Username: "knowledge-debug-second-" + strings.ToLower(t.Name()),
		Nickname: "Second Store Staff", Status: enums.StatusOk,
	}
	if err := db.Create(secondUser).Error; err != nil {
		t.Fatal(err)
	}
	secondBinding := &models.StoreStaffBinding{
		TenantID: tenant.ID, UserID: secondUser.ID, ActiveUserID: positiveInt64Pointer(secondUser.ID),
		StoreID: store.ID, Status: enums.StatusOk,
	}
	if err := db.Create(secondBinding).Error; err != nil {
		t.Fatal(err)
	}
	conversation := &models.Conversation{
		TenantID: tenant.ID, StoreID: store.ID, StoreStaffBindingID: binding.ID,
		CustomerID: 991, CustomerName: "Knowledge Debug Customer", Status: enums.IMConversationStatusPending,
	}
	if err := db.Create(conversation).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.ConversationRouteState{
		TenantID: tenant.ID, ConversationID: conversation.ID, StoreID: store.ID,
		StoreStaffBindingID: binding.ID,
	}).Error; err != nil {
		t.Fatal(err)
	}

	if _, err := ModelCallResolverService.ResolveForKnowledgeDebug(
		tenant.ID, store.ID, 0, 0, enums.ModelUsageSlotReplyLLM,
	); err == nil {
		t.Fatal("standalone knowledge debug without a Store staff binding must fail")
	}
	if _, err := ModelCallResolverService.ResolveForKnowledgeDebug(
		tenant.ID, store.ID, conversation.ID, secondBinding.ID, enums.ModelUsageSlotReplyLLM,
	); err == nil {
		t.Fatal("conversation and selected Store staff binding mismatch must fail")
	}
	resolved, err := ModelCallResolverService.ResolveForKnowledgeDebug(
		tenant.ID, store.ID, conversation.ID, binding.ID, enums.ModelUsageSlotReplyLLM,
	)
	if err != nil {
		t.Fatalf("matching knowledge debug binding rejected: %v", err)
	}
	if resolved.StoreStaffBindingID != binding.ID || resolved.CredentialID != credential.ID || resolved.CredentialRevision != 7 {
		t.Fatalf("knowledge debug resolved wrong credential: %#v", resolved)
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
		&models.Tenant{}, &models.User{}, &models.Store{}, &models.StoreStaffBinding{}, &models.ModelProfileTemplate{}, &models.ModelProfileSlot{}, &models.ModelProfileTestRun{},
		&models.StoreModelProfileAssignment{}, &models.StoreModelCredential{}, &models.Conversation{}, &models.ConversationRouteState{},
	); err != nil {
		t.Fatalf("AutoMigrate() error=%v", err)
	}
	sqls.SetDB(db)
	masterKey := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	config.SetCurrent(&config.Config{StoreCredential: config.StoreCredentialConfig{MasterKey: masterKey, MasterKeyID: "model-profile-test-key"}})
	t.Cleanup(func() {
		config.SetCurrent(&config.Config{})
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
	user := &models.User{TenantID: tenant.ID, Username: "staff-" + strings.ToLower(t.Name()), Nickname: "Store Staff", Status: enums.StatusOk}
	if err := db.Create(user).Error; err != nil {
		t.Fatal(err)
	}
	binding := &models.StoreStaffBinding{
		TenantID: tenant.ID, UserID: user.ID, ActiveUserID: positiveInt64Pointer(user.ID), StoreID: store.ID, Status: enums.StatusOk,
	}
	if err := db.Create(binding).Error; err != nil {
		t.Fatal(err)
	}
	return tenant, store
}

func modelProfileTestBinding(t *testing.T, db *gorm.DB, tenantID, storeID int64) *models.StoreStaffBinding {
	t.Helper()
	binding := repositories.StoreStaffBindingRepository.TakeInTenant(db, tenantID, "store_id = ? AND status = ?", storeID, enums.StatusOk)
	if binding == nil {
		t.Fatal("model profile test binding is missing")
	}
	return binding
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
