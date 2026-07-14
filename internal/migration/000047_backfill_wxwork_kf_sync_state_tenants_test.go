package migration

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
)

func TestBackfillWxWorkKFSyncStateTenants(t *testing.T) {
	db := setupWxWorkKFSyncStateMigrationDB(t)
	tenantA := createWxWorkKFSyncMigrationTenant(t, db, "kf-sync-a")
	tenantB := createWxWorkKFSyncMigrationTenant(t, db, "kf-sync-b")
	createWxWorkKFSyncMigrationChannel(t, db, tenantA.ID, "kf-open-a")
	createWxWorkKFSyncMigrationChannel(t, db, tenantB.ID, "kf-open-b")
	stateA := createWxWorkKFSyncMigrationState(t, db, 0, "kf-open-a")
	stateB := createWxWorkKFSyncMigrationState(t, db, tenantB.ID, "kf-open-b")

	for run := 0; run < 2; run++ {
		if err := db.Transaction(backfillWxWorkKFSyncStateTenants); err != nil {
			t.Fatalf("backfill wxwork kf sync state tenants run %d: %v", run+1, err)
		}
	}

	assertWxWorkKFSyncMigrationTenant(t, db, stateA.ID, tenantA.ID)
	assertWxWorkKFSyncMigrationTenant(t, db, stateB.ID, tenantB.ID)
}

func TestBackfillWxWorkKFSyncStateTenantsRollsBackConflict(t *testing.T) {
	db := setupWxWorkKFSyncStateMigrationDB(t)
	tenantA := createWxWorkKFSyncMigrationTenant(t, db, "kf-conflict-a")
	tenantB := createWxWorkKFSyncMigrationTenant(t, db, "kf-conflict-b")
	createWxWorkKFSyncMigrationChannel(t, db, tenantA.ID, "kf-before-conflict")
	createWxWorkKFSyncMigrationChannel(t, db, tenantB.ID, "kf-conflict")
	updatedBeforeConflict := createWxWorkKFSyncMigrationState(t, db, 0, "kf-before-conflict")
	conflict := createWxWorkKFSyncMigrationState(t, db, tenantA.ID, "kf-conflict")

	err := db.Transaction(backfillWxWorkKFSyncStateTenants)
	if err == nil || !strings.Contains(err.Error(), "conflicts") {
		t.Fatalf("backfill error=%v want tenant conflict", err)
	}
	assertWxWorkKFSyncMigrationTenant(t, db, updatedBeforeConflict.ID, 0)
	assertWxWorkKFSyncMigrationTenant(t, db, conflict.ID, tenantA.ID)
}

func TestBackfillWxWorkKFSyncStateTenantsRejectsOrphan(t *testing.T) {
	db := setupWxWorkKFSyncStateMigrationDB(t)
	createWxWorkKFSyncMigrationTenant(t, db, "kf-orphan-tenant")
	orphan := createWxWorkKFSyncMigrationState(t, db, 0, "kf-orphan")

	err := db.Transaction(backfillWxWorkKFSyncStateTenants)
	if err == nil || !strings.Contains(err.Error(), "no channel tenant evidence") {
		t.Fatalf("backfill error=%v want orphan rejection", err)
	}
	assertWxWorkKFSyncMigrationTenant(t, db, orphan.ID, 0)
}

func setupWxWorkKFSyncStateMigrationDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "wxwork-kf-sync-tenant.db")), &gorm.Config{
		NamingStrategy: schema.NamingStrategy{TablePrefix: "t_", SingularTable: true},
		Logger:         logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&models.Tenant{}, &models.Channel{}, &models.WxWorkKFSyncState{}); err != nil {
		t.Fatalf("migrate wxwork kf sync tenant tables: %v", err)
	}
	return db
}

func createWxWorkKFSyncMigrationTenant(t *testing.T, db *gorm.DB, code string) *models.Tenant {
	t.Helper()
	item := &models.Tenant{
		TenantCode:         code,
		LegalName:          code,
		ShortName:          code,
		RegistrationType:   "test",
		RegistrationNo:     code,
		VerificationStatus: enums.TenantVerificationStatusVerified,
		Status:             enums.StatusOk,
		AuditFields:        wxWorkKFSyncMigrationAuditFields(),
	}
	if err := db.Create(item).Error; err != nil {
		t.Fatalf("create tenant %s: %v", code, err)
	}
	return item
}

func createWxWorkKFSyncMigrationChannel(t *testing.T, db *gorm.DB, tenantID int64, openKfID string) *models.Channel {
	t.Helper()
	item := &models.Channel{
		TenantID:    tenantID,
		Name:        openKfID,
		ChannelType: enums.ChannelTypeWxWorkKF,
		ChannelID:   "channel-" + openKfID,
		ConfigJSON:  fmt.Sprintf(`{"openKfId":%q}`, openKfID),
		Status:      enums.StatusOk,
		AuditFields: wxWorkKFSyncMigrationAuditFields(),
	}
	if err := db.Create(item).Error; err != nil {
		t.Fatalf("create channel %s: %v", openKfID, err)
	}
	return item
}

func createWxWorkKFSyncMigrationState(t *testing.T, db *gorm.DB, tenantID int64, openKfID string) *models.WxWorkKFSyncState {
	t.Helper()
	item := &models.WxWorkKFSyncState{
		TenantID:    tenantID,
		OpenKfID:    openKfID,
		NextCursor:  "cursor-" + openKfID,
		Status:      enums.StatusOk,
		AuditFields: wxWorkKFSyncMigrationAuditFields(),
	}
	if err := db.Create(item).Error; err != nil {
		t.Fatalf("create sync state %s: %v", openKfID, err)
	}
	return item
}

func assertWxWorkKFSyncMigrationTenant(t *testing.T, db *gorm.DB, stateID, wantTenantID int64) {
	t.Helper()
	var item models.WxWorkKFSyncState
	if err := db.First(&item, "id = ?", stateID).Error; err != nil {
		t.Fatalf("read sync state %d: %v", stateID, err)
	}
	if item.TenantID != wantTenantID {
		t.Fatalf("sync state %d tenant=%d want=%d", stateID, item.TenantID, wantTenantID)
	}
}

func wxWorkKFSyncMigrationAuditFields() models.AuditFields {
	now := time.Now()
	return models.AuditFields{CreatedAt: now, CreateUserName: "test", UpdatedAt: now, UpdateUserName: "test"}
}
