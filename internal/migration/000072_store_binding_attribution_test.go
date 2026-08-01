package migration

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"

	"github.com/glebarez/sqlite"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

type storeBindingAttributionRecords struct {
	credentialID     int64
	auditID          int64
	modelTestRunID   int64
	usageEventID     int64
	gatewayCallID    int64
	knowledgeBaseID  int64
	fastGPTStateID   int64
	datasetJobID     int64
	usageSyncStateID int64
}

func TestStoreBindingAttributionMigrationSQLite(t *testing.T) {
	runStoreBindingAttributionScenarios(t, func(t *testing.T, prefix string) *gorm.DB {
		t.Helper()
		db, err := gorm.Open(sqlite.Open("file:"+prefix+"?mode=memory&cache=shared"), storeContinuityMigrationGORMConfig(prefix))
		if err != nil {
			t.Fatalf("open SQLite: %v", err)
		}
		return db
	})
}

func TestStoreBindingAttributionMigrationMySQL(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("TEST_MYSQL_DSN"))
	if dsn == "" {
		t.Skip("TEST_MYSQL_DSN is not configured")
	}
	runStoreBindingAttributionScenarios(t, func(t *testing.T, prefix string) *gorm.DB {
		t.Helper()
		db, err := gorm.Open(mysql.Open(dsn), storeContinuityMigrationGORMConfig(prefix))
		if err != nil {
			t.Fatalf("open MySQL: %v", err)
		}
		return db
	})
}

func runStoreBindingAttributionScenarios(t *testing.T, open func(*testing.T, string) *gorm.DB) {
	t.Helper()
	scenarios := []struct {
		name string
		run  func(*testing.T, *gorm.DB)
	}{
		{name: "single binding backfills every attribution", run: testSingleStoreBindingAttribution},
		{name: "configured credential without evidence aborts", run: testAmbiguousConfiguredCredentialAttribution},
		{name: "empty placeholder expands per binding", run: testEmptyCredentialExpansion},
		{name: "conflicting evidence rolls back", run: testConflictingStoreBindingEvidence},
	}
	for index, scenario := range scenarios {
		scenario := scenario
		t.Run(scenario.name, func(t *testing.T) {
			prefix := fmt.Sprintf("a72_%d_%d_", index, time.Now().UnixNano())
			db := open(t, prefix)
			prepareStoreContinuityMigrationSchema(t, db)
			scenario.run(t, db)
		})
	}
}

func prepareStoreContinuityMigrationSchema(t *testing.T, db *gorm.DB) {
	t.Helper()
	fixtures := storeContinuityMigrationModels()
	for i := len(fixtures) - 1; i >= 0; i-- {
		_ = db.Migrator().DropTable(fixtures[i])
	}
	if err := db.AutoMigrate(fixtures...); err != nil {
		t.Fatalf("migrate Store continuity fixtures: %v", err)
	}
	t.Cleanup(func() {
		for i := len(fixtures) - 1; i >= 0; i-- {
			if err := db.Migrator().DropTable(fixtures[i]); err != nil {
				t.Errorf("drop fixture %T: %v", fixtures[i], err)
			}
		}
	})
	if _, err := ensureRoles(db); err != nil {
		t.Fatalf("seed roles: %v", err)
	}
}

func testSingleStoreBindingAttribution(t *testing.T, db *gorm.DB) {
	now := time.Now().Truncate(time.Second)
	store, bindings := createMigrationStoreBindings(t, db, 201, 1, now)
	records := seedStoreBindingAttributionRecords(t, db, 201, store.ID, 0, 0, 0, now)
	for pass := 1; pass <= 2; pass++ {
		if err := migrateStoreConversationContinuity(db); err != nil {
			t.Fatalf("migration pass %d: %v", pass, err)
		}
	}
	assertStoreBindingAttributionRecords(t, db, records, bindings[0].ID)
}

func testAmbiguousConfiguredCredentialAttribution(t *testing.T, db *gorm.DB) {
	now := time.Now().Truncate(time.Second)
	store, _ := createMigrationStoreBindings(t, db, 202, 2, now)
	credential := &models.StoreModelCredential{
		TenantID: 202, StoreID: store.ID, EncryptedKey: "legacy-ciphertext", KeyNonce: "legacy-nonce",
		KeyFingerprint: "legacy-fingerprint", CipherVersion: "legacy-v1", MasterKeyID: "legacy-key",
		CredentialRevision: 3, Status: enums.StoreCredentialStatusActive, AuditFields: migration72TestAudit(now),
	}
	if err := db.Create(credential).Error; err != nil {
		t.Fatalf("create configured credential: %v", err)
	}
	err := migrateStoreConversationContinuity(db)
	if err == nil || !strings.Contains(err.Error(), "cannot deterministically choose one of 2 Store staff bindings") {
		t.Fatalf("ambiguous credential migration error=%v", err)
	}
	var current models.StoreModelCredential
	if err := db.First(&current, credential.ID).Error; err != nil {
		t.Fatalf("reload configured credential: %v", err)
	}
	if current.StoreStaffBindingID != 0 || current.EncryptedKey != credential.EncryptedKey || current.CredentialRevision != credential.CredentialRevision {
		t.Fatalf("failed migration changed configured credential: %+v", current)
	}
}

func testEmptyCredentialExpansion(t *testing.T, db *gorm.DB) {
	now := time.Now().Truncate(time.Second)
	store, bindings := createMigrationStoreBindings(t, db, 203, 2, now)
	credential := &models.StoreModelCredential{
		TenantID: 203, StoreID: store.ID, Status: enums.StoreCredentialStatusUnconfigured, AuditFields: migration72TestAudit(now),
	}
	if err := db.Create(credential).Error; err != nil {
		t.Fatalf("create empty credential: %v", err)
	}
	for pass := 1; pass <= 2; pass++ {
		if err := migrateStoreConversationContinuity(db); err != nil {
			t.Fatalf("migration pass %d: %v", pass, err)
		}
	}
	var credentials []models.StoreModelCredential
	if err := db.Where("tenant_id = ? AND store_id = ?", 203, store.ID).Order("store_staff_binding_id ASC").Find(&credentials).Error; err != nil {
		t.Fatalf("load expanded credentials: %v", err)
	}
	if len(credentials) != len(bindings) {
		t.Fatalf("expanded credentials=%d want=%d", len(credentials), len(bindings))
	}
	for index := range credentials {
		if credentials[index].StoreStaffBindingID != bindings[index].ID || legacyStoreCredentialConfigured(&credentials[index]) {
			t.Fatalf("unexpected expanded credential: %+v", credentials[index])
		}
	}
}

func testConflictingStoreBindingEvidence(t *testing.T, db *gorm.DB) {
	now := time.Now().Truncate(time.Second)
	store, bindings := createMigrationStoreBindings(t, db, 204, 2, now)
	credential := &models.StoreModelCredential{
		TenantID: 204, StoreID: store.ID, EncryptedKey: "legacy-ciphertext", KeyNonce: "legacy-nonce",
		KeyFingerprint: "legacy-fingerprint", CipherVersion: "legacy-v1", MasterKeyID: "legacy-key",
		CredentialRevision: 9, Status: enums.StoreCredentialStatusActive, AuditFields: migration72TestAudit(now),
	}
	if err := db.Create(credential).Error; err != nil {
		t.Fatalf("create configured credential: %v", err)
	}
	modelRun := &models.ModelProfileTestRun{
		TenantID: 204, StoreID: store.ID, StoreStaffBindingID: bindings[0].ID, CredentialRevision: 9,
		Status: enums.ModelProfileTestStatusPassed, CredentialSource: enums.ModelProfileTestCredentialSourceActive, CreatedAt: now,
	}
	usage := &models.AIUsageEvent{
		TenantID: 204, EventKey: "conflicting-attribution", StoreID: store.ID, StoreStaffBindingID: bindings[1].ID,
		CredentialRevision: 9, KeyFingerprint: "legacy-fingerprint", CreatedAt: now,
	}
	for _, item := range []any{modelRun, usage} {
		if err := db.Create(item).Error; err != nil {
			t.Fatalf("create conflicting evidence %T: %v", item, err)
		}
	}
	err := migrateStoreConversationContinuity(db)
	if err == nil || !strings.Contains(err.Error(), "ambiguous Store staff binding evidence") {
		t.Fatalf("conflicting evidence migration error=%v", err)
	}
	var current models.StoreModelCredential
	if err := db.First(&current, credential.ID).Error; err != nil {
		t.Fatalf("reload configured credential: %v", err)
	}
	if current.StoreStaffBindingID != 0 {
		t.Fatalf("failed migration attributed configured credential to binding %d", current.StoreStaffBindingID)
	}
}

func createMigrationStoreBindings(t *testing.T, db *gorm.DB, tenantID int64, count int, now time.Time) (*models.Store, []models.StoreStaffBinding) {
	t.Helper()
	store := &models.Store{
		TenantID: tenantID, StoreCode: fmt.Sprintf("store-%d", tenantID), Name: fmt.Sprintf("Store %d", tenantID),
		Status: enums.StatusOk, AuditFields: migration72TestAudit(now),
	}
	if err := db.Create(store).Error; err != nil {
		t.Fatalf("create Store: %v", err)
	}
	bindings := make([]models.StoreStaffBinding, 0, count)
	for index := 0; index < count; index++ {
		user := &models.User{
			TenantID: tenantID, Username: fmt.Sprintf("store-%d-staff-%d", tenantID, index+1),
			Status: enums.StatusOk, AuditFields: migration72TestAudit(now),
		}
		if err := db.Create(user).Error; err != nil {
			t.Fatalf("create Store staff user: %v", err)
		}
		activeUserID := user.ID
		binding := models.StoreStaffBinding{
			TenantID: tenantID, UserID: user.ID, ActiveUserID: &activeUserID, StoreID: store.ID,
			Status: enums.StatusOk, AuditFields: migration72TestAudit(now),
		}
		if err := db.Create(&binding).Error; err != nil {
			t.Fatalf("create Store staff binding: %v", err)
		}
		bindings = append(bindings, binding)
	}
	return store, bindings
}

func seedStoreBindingAttributionRecords(t *testing.T, db *gorm.DB, tenantID, storeID, instanceID, conversationID, messageID int64, now time.Time) storeBindingAttributionRecords {
	t.Helper()
	credential := &models.StoreModelCredential{
		TenantID: tenantID, StoreID: storeID, EncryptedKey: "legacy-ciphertext", KeyNonce: "legacy-nonce",
		KeyFingerprint: "legacy-fingerprint", CipherVersion: "legacy-v1", MasterKeyID: "legacy-key",
		CredentialRevision: 7, Status: enums.StoreCredentialStatusActive, AuditFields: migration72TestAudit(now),
	}
	if err := db.Create(credential).Error; err != nil {
		t.Fatalf("create legacy Store credential: %v", err)
	}
	audit := &models.StoreModelCredentialAuditLog{
		TenantID: tenantID, StoreID: storeID, CredentialID: credential.ID, Action: enums.CredentialAuditActionActivate,
		Result: enums.CredentialAuditResultSuccess, FromRevision: 6, ToRevision: 7, CreatedAt: now,
	}
	modelRun := &models.ModelProfileTestRun{
		TenantID: tenantID, StoreID: storeID, CredentialRevision: 7,
		Status: enums.ModelProfileTestStatusPassed, CredentialSource: enums.ModelProfileTestCredentialSourceActive, CreatedAt: now,
	}
	knowledgeBase := &models.KnowledgeBase{
		TenantID: tenantID, StoreID: storeID, Name: "Migration knowledge", DatasetID: fmt.Sprintf("dataset-%d-%d", tenantID, storeID),
		FastGPTProfileStatus: "ready", FastGPTAppliedProfileID: 11, FastGPTAppliedProfileRevision: 2,
		FastGPTAppliedCredentialRevision: 7, Status: enums.StatusOk, AuditFields: migration72TestAudit(now),
	}
	for _, item := range []any{audit, modelRun, knowledgeBase} {
		if err := db.Create(item).Error; err != nil {
			t.Fatalf("create Store attribution record %T: %v", item, err)
		}
	}
	usage := &models.AIUsageEvent{
		TenantID: tenantID, EventKey: fmt.Sprintf("usage-%d-%d", tenantID, storeID), StoreID: storeID,
		WxWorkInstanceID: instanceID, ConversationID: conversationID, MessageID: messageID, KnowledgeBaseID: knowledgeBase.ID,
		CredentialRevision: 7, KeyFingerprint: "legacy-fingerprint", CreatedAt: now,
	}
	gatewayCall := &models.AIUsageGatewayCall{
		TenantID: tenantID, CallKey: fmt.Sprintf("gateway-%d-%d", tenantID, storeID), StoreID: storeID,
		WxWorkInstanceID: instanceID, ConversationID: conversationID, MessageID: messageID,
		CredentialRevision: 7, KeyFingerprint: "legacy-fingerprint", StartedAt: now, FinishedAt: now, CreatedAt: now, UpdatedAt: now,
	}
	fastGPTState := &models.FastGPTStoreTenant{
		TenantID: tenantID, StoreID: storeID, TenantTeamID: fmt.Sprintf("team-%d-%d", tenantID, storeID),
		TargetProfileID: 11, TargetProfileRevision: 2, AppliedProfileID: 11, AppliedProfileRevision: 2,
		TargetCredentialRevision: 7, AppliedCredentialRevision: 7, AppliedKeyFingerprint: "legacy-fingerprint",
		Status: "ready", ReadinessStatus: "ready", AuditFields: migration72TestAudit(now),
	}
	datasetJob := &models.FastGPTDatasetJob{
		TenantID: tenantID, TaskKey: fmt.Sprintf("job-%d-%d", tenantID, storeID), StoreID: storeID,
		KnowledgeBaseID: knowledgeBase.ID, Action: "sync_profile", Status: "pending",
		TargetProfileID: 11, TargetProfileRevision: 2, TargetCredentialRevision: 7, CreatedAt: now, UpdatedAt: now,
	}
	usageSync := &models.FastGPTUsageSyncState{
		TenantID: tenantID, StoreID: storeID, KnowledgeBaseID: knowledgeBase.ID,
		ModelProfileID: 11, ProfileRevision: 2, CredentialRevision: 7, KeyFingerprint: "legacy-fingerprint",
		CreatedAt: now, UpdatedAt: now,
	}
	for _, item := range []any{usage, gatewayCall, fastGPTState, datasetJob, usageSync} {
		if err := db.Create(item).Error; err != nil {
			t.Fatalf("create Store attribution record %T: %v", item, err)
		}
	}
	return storeBindingAttributionRecords{
		credentialID: credential.ID, auditID: audit.ID, modelTestRunID: modelRun.ID,
		usageEventID: usage.ID, gatewayCallID: gatewayCall.ID, knowledgeBaseID: knowledgeBase.ID,
		fastGPTStateID: fastGPTState.ID, datasetJobID: datasetJob.ID, usageSyncStateID: usageSync.ID,
	}
}

func assertStoreBindingAttributionRecords(t *testing.T, db *gorm.DB, records storeBindingAttributionRecords, bindingID int64) {
	t.Helper()
	var credential models.StoreModelCredential
	if err := db.First(&credential, records.credentialID).Error; err != nil {
		t.Fatalf("load migrated credential: %v", err)
	}
	if credential.StoreStaffBindingID != bindingID || credential.EncryptedKey != "legacy-ciphertext" ||
		credential.KeyNonce != "legacy-nonce" || credential.CipherVersion != "legacy-v1" {
		t.Fatalf("unexpected migrated credential: %+v", credential)
	}
	assertBinding := func(model any, id int64, column string) {
		t.Helper()
		var got int64
		if err := db.Model(model).Select(column).Where("id = ?", id).Scan(&got).Error; err != nil {
			t.Fatalf("load %T binding: %v", model, err)
		}
		if got != bindingID {
			t.Fatalf("%T binding=%d want=%d", model, got, bindingID)
		}
	}
	assertBinding(&models.StoreModelCredentialAuditLog{}, records.auditID, "store_staff_binding_id")
	assertBinding(&models.ModelProfileTestRun{}, records.modelTestRunID, "store_staff_binding_id")
	assertBinding(&models.AIUsageEvent{}, records.usageEventID, "store_staff_binding_id")
	assertBinding(&models.AIUsageGatewayCall{}, records.gatewayCallID, "store_staff_binding_id")
	assertBinding(&models.KnowledgeBase{}, records.knowledgeBaseID, "fast_gpt_applied_store_staff_binding_id")
	assertBinding(&models.FastGPTStoreTenant{}, records.fastGPTStateID, "target_store_staff_binding_id")
	assertBinding(&models.FastGPTStoreTenant{}, records.fastGPTStateID, "applied_store_staff_binding_id")
	assertBinding(&models.FastGPTDatasetJob{}, records.datasetJobID, "target_store_staff_binding_id")
	assertBinding(&models.FastGPTUsageSyncState{}, records.usageSyncStateID, "store_staff_binding_id")
}
