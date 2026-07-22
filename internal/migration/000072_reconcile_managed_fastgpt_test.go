package migration

import (
	"os"
	"strings"
	"testing"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
	fastgptapi "agent-desk/internal/pkg/fastgpt"

	"github.com/glebarez/sqlite"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
)

func TestReconcileManagedFastGPTIsIdempotent(t *testing.T) {
	db := openManagedFastGPTMigrationSQLite(t)
	runManagedFastGPTMigrationScenario(t, db)
}

func TestReconcileManagedFastGPTRejectsConflictingStoreProjections(t *testing.T) {
	db := openManagedFastGPTMigrationSQLite(t)
	store := &models.Store{TenantID: 201, StoreCode: "conflicting-store", Name: "冲突门店", Status: enums.StatusOk}
	if err := db.Create(store).Error; err != nil {
		t.Fatal(err)
	}
	first := &models.KnowledgeBase{TenantID: store.TenantID, StoreID: store.ID, Name: "知识库一", Status: enums.StatusOk}
	second := &models.KnowledgeBase{TenantID: store.TenantID, StoreID: store.ID, Name: "知识库二", Status: enums.StatusOk}
	if err := db.Create(first).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(second).Error; err != nil {
		t.Fatal(err)
	}
	instances := []models.WxWorkProtocolInstance{
		{TenantID: store.TenantID, StoreID: store.ID, Guid: "conflict-instance-1", KnowledgeBaseID: first.ID, Status: enums.StatusOk},
		{TenantID: store.TenantID, StoreID: store.ID, Guid: "conflict-instance-2", KnowledgeBaseID: second.ID, Status: enums.StatusOk},
	}
	if err := db.Create(&instances).Error; err != nil {
		t.Fatal(err)
	}
	if err := reconcileManagedFastGPT(db); err == nil || !strings.Contains(err.Error(), "conflicting knowledge-base projections") {
		t.Fatalf("conflicting projections must block migration, err=%v", err)
	}
}

func TestReconcileManagedFastGPTMySQL(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("TEST_MYSQL_DSN"))
	if dsn == "" {
		t.Skip("TEST_MYSQL_DSN is not configured")
	}
	db, err := gorm.Open(mysql.Open(dsn), managedFastGPTMigrationGORMConfig())
	if err != nil {
		t.Fatal(err)
	}
	if err := migrateManagedFastGPTTables(db); err != nil {
		t.Fatal(err)
	}
	runManagedFastGPTMigrationScenario(t, db)
}

func runManagedFastGPTMigrationScenario(t *testing.T, db *gorm.DB) {
	t.Helper()
	now := time.Now()
	store := &models.Store{TenantID: 101, StoreCode: "managed-fastgpt-store", Name: "托管知识门店", Status: enums.StatusOk}
	if err := db.Create(store).Error; err != nil {
		t.Fatal(err)
	}
	knowledgeBase := &models.KnowledgeBase{
		TenantID: store.TenantID, StoreID: store.ID, Name: "门店知识库", DatasetID: "dataset-managed",
		ConnectionID: fastgptapi.ManagedConnectionID, FastGPTProfileID: "remote-profile-old",
		FastGPTProfileRevision: "5", FastGPTProfileStatus: "ready", Status: enums.StatusOk,
	}
	if err := db.Create(knowledgeBase).Error; err != nil {
		t.Fatal(err)
	}
	instances := []models.WxWorkProtocolInstance{
		{TenantID: store.TenantID, StoreID: store.ID, Guid: "managed-instance-1", KnowledgeBaseID: knowledgeBase.ID, Status: enums.StatusOk},
		{TenantID: store.TenantID, StoreID: store.ID, Guid: "managed-instance-2", KnowledgeBaseID: 0, Status: enums.StatusOk},
	}
	if err := db.Create(&instances).Error; err != nil {
		t.Fatal(err)
	}
	route := &models.ConversationRouteState{TenantID: store.TenantID, ConversationID: 501, StoreID: store.ID, WxWorkInstanceID: instances[1].ID}
	if err := db.Create(route).Error; err != nil {
		t.Fatal(err)
	}
	template := &models.ModelProfileTemplate{
		Code: "managed-fastgpt", Name: "托管模型", Revision: 2, GatewayBaseURL: "https://newapi.example/v1",
		Status: enums.ModelProfileStatusActive, PublishedAt: &now,
	}
	if err := db.Create(template).Error; err != nil {
		t.Fatal(err)
	}
	assignment := &models.StoreModelProfileAssignment{
		TenantID: store.TenantID, StoreID: store.ID, TemplateID: template.ID, TemplateRevision: template.Revision,
		Status: enums.StoreModelAssignmentStatusReady, ReadinessStatus: "ready", AssignedAt: now,
	}
	if err := db.Create(assignment).Error; err != nil {
		t.Fatal(err)
	}
	credential := &models.StoreModelCredential{
		TenantID: store.TenantID, StoreID: store.ID, EncryptedKey: "encrypted-key", KeyFingerprint: "fingerprint-current",
		CredentialRevision: 3, Status: enums.StoreCredentialStatusActive,
	}
	if err := db.Create(credential).Error; err != nil {
		t.Fatal(err)
	}
	binding := &models.FastGPTStoreTenant{
		TenantID: store.TenantID, StoreID: store.ID, TenantTeamID: "team-managed", TenantTeamName: store.Name,
		Status: "active", ReadinessStatus: "ready", AppliedProfileID: template.ID + 1000, AppliedProfileRevision: 1,
		AppliedCredentialRevision: 2, AppliedKeyFingerprint: "fingerprint-old",
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(binding).Error; err != nil {
		t.Fatal(err)
	}
	usageState := &models.FastGPTUsageSyncState{
		TenantID: store.TenantID, StoreID: store.ID, KnowledgeBaseID: knowledgeBase.ID,
		TenantTeamID: binding.TenantTeamID, Cursor: "cursor-before-migration", CreatedAt: now, UpdatedAt: now,
	}
	if err := db.Create(usageState).Error; err != nil {
		t.Fatal(err)
	}
	missingTargetJob := &models.FastGPTDatasetJob{
		TenantID: store.TenantID, StoreID: store.ID, KnowledgeBaseID: knowledgeBase.ID,
		TaskKey: "migration-missing-target", Action: "upload_file", Status: "pending", CreatedAt: now, UpdatedAt: now,
	}
	staleTargetJob := &models.FastGPTDatasetJob{
		TenantID: store.TenantID, StoreID: store.ID, KnowledgeBaseID: knowledgeBase.ID,
		TaskKey: "migration-stale-target", Action: "upload_file", Status: "pending",
		TargetProfileID: template.ID + 1000, TargetProfileRevision: 1, TargetCredentialRevision: 2,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := db.Create(missingTargetJob).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(staleTargetJob).Error; err != nil {
		t.Fatal(err)
	}

	for run := 0; run < 2; run++ {
		if err := reconcileManagedFastGPT(db); err != nil {
			t.Fatalf("migration run %d: %v", run+1, err)
		}
	}

	var updatedStore models.Store
	if err := db.First(&updatedStore, store.ID).Error; err != nil || updatedStore.KnowledgeBaseID != knowledgeBase.ID {
		t.Fatalf("Store knowledge authority=%#v err=%v", updatedStore, err)
	}
	var updatedInstances []models.WxWorkProtocolInstance
	if err := db.Where("store_id = ?", store.ID).Order("id ASC").Find(&updatedInstances).Error; err != nil {
		t.Fatal(err)
	}
	for _, instance := range updatedInstances {
		if instance.KnowledgeBaseID != knowledgeBase.ID {
			t.Fatalf("instance projection=%#v", instance)
		}
	}
	var updatedRoute models.ConversationRouteState
	if err := db.First(&updatedRoute, route.ID).Error; err != nil || updatedRoute.KnowledgeBaseID != knowledgeBase.ID {
		t.Fatalf("route projection=%#v err=%v", updatedRoute, err)
	}
	if err := db.First(binding, binding.ID).Error; err != nil {
		t.Fatal(err)
	}
	if binding.TargetProfileID != template.ID || binding.TargetProfileRevision != template.Revision || binding.TargetCredentialRevision != credential.CredentialRevision ||
		binding.AppliedProfileRevision != 1 || binding.AppliedCredentialRevision != 2 || binding.ReadinessStatus != "ready" {
		t.Fatalf("binding reconciliation=%#v", binding)
	}
	if err := db.First(usageState, usageState.ID).Error; err != nil {
		t.Fatal(err)
	}
	if usageState.ModelProfileID != binding.AppliedProfileID || usageState.ProfileRevision != 1 || usageState.CredentialRevision != 2 ||
		usageState.KeyFingerprint != "fingerprint-old" || usageState.FastGPTProfileID != "remote-profile-old" || usageState.FastGPTRevision != "5" {
		t.Fatalf("usage cursor attribution=%#v", usageState)
	}
	if err := db.First(knowledgeBase, knowledgeBase.ID).Error; err != nil {
		t.Fatal(err)
	}
	if knowledgeBase.FastGPTAppliedProfileID != binding.AppliedProfileID || knowledgeBase.FastGPTAppliedProfileRevision != 1 ||
		knowledgeBase.FastGPTAppliedCredentialRevision != 2 {
		t.Fatalf("knowledge-base applied target=%#v", knowledgeBase)
	}
	if err := db.First(missingTargetJob, missingTargetJob.ID).Error; err != nil {
		t.Fatal(err)
	}
	if missingTargetJob.Status != "failed" || missingTargetJob.LastErrorClass != "legacy_remote_resource_reprovisioned" {
		t.Fatalf("missing target job=%#v", missingTargetJob)
	}
	if err := db.First(staleTargetJob, staleTargetJob.ID).Error; err != nil {
		t.Fatal(err)
	}
	if staleTargetJob.Status != "failed" || staleTargetJob.LastErrorClass != "target_revision_changed" {
		t.Fatalf("stale target job=%#v", staleTargetJob)
	}
	var retirement models.FastGPTRemoteResourceRetirement
	if err := db.Take(&retirement, "tenant_id = ? AND store_id = ? AND legacy_dataset_id = ?", store.TenantID, store.ID, knowledgeBase.DatasetID).Error; err != nil {
		t.Fatal(err)
	}
	if retirement.LegacyKnowledgeBaseID != knowledgeBase.ID || retirement.LegacyTeamID != binding.TenantTeamID ||
		retirement.Status != enums.FastGPTRemoteRetirementAwaitingReplacement || retirement.ReplacementKnowledgeBaseID != 0 {
		t.Fatalf("remote retirement=%#v", retirement)
	}
	var reprovisionJobs []models.FastGPTDatasetJob
	if err := db.Model(&models.FastGPTDatasetJob{}).
		Where("tenant_id = ? AND store_id = ? AND action = ? AND target_profile_id = ? AND target_profile_revision = ? AND target_credential_revision = ?",
			store.TenantID, store.ID, "create_dataset", template.ID, template.Revision, credential.CredentialRevision).
		Find(&reprovisionJobs).Error; err != nil || len(reprovisionJobs) != 1 {
		t.Fatalf("reprovision jobs=%#v err=%v", reprovisionJobs, err)
	}
	if reprovisionJobs[0].KnowledgeBaseID != 0 || reprovisionJobs[0].DatasetID != "" || reprovisionJobs[0].Status != "pending" || reprovisionJobs[0].Filename != knowledgeBase.Name {
		t.Fatalf("reprovision job=%#v", reprovisionJobs[0])
	}
}

func openManagedFastGPTMigrationSQLite(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), managedFastGPTMigrationGORMConfig())
	if err != nil {
		t.Fatal(err)
	}
	if err := migrateManagedFastGPTTables(db); err != nil {
		t.Fatal(err)
	}
	return db
}

func migrateManagedFastGPTTables(db *gorm.DB) error {
	return db.AutoMigrate(
		&models.Store{}, &models.KnowledgeBase{}, &models.WxWorkProtocolInstance{}, &models.ConversationRouteState{},
		&models.ModelProfileTemplate{}, &models.StoreModelProfileAssignment{}, &models.StoreModelCredential{},
		&models.FastGPTStoreTenant{}, &models.FastGPTUsageSyncState{}, &models.FastGPTDatasetJob{}, &models.FastGPTRemoteResourceRetirement{},
	)
}

func managedFastGPTMigrationGORMConfig() *gorm.Config {
	return &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
		NamingStrategy: schema.NamingStrategy{
			TablePrefix: "t_", SingularTable: true,
		},
	}
}
