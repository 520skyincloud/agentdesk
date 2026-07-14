package migration

import (
	"path/filepath"
	"strconv"
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

func TestBackfillNotificationTenantsUsesRecipientAndIsIdempotent(t *testing.T) {
	db := setupNotificationTenantBackfillDB(t)
	tenantUser := createNotificationTenantUser(t, db, 11, 101)
	platformUser := createNotificationTenantUser(t, db, 12, 0)
	tenantNotification := createNotificationForBackfill(t, db, tenantUser.ID, 0)
	platformNotification := createNotificationForBackfill(t, db, platformUser.ID, 0)

	if err := db.Transaction(backfillNotificationTenants); err != nil {
		t.Fatalf("backfill notification tenants: %v", err)
	}
	if err := db.Transaction(backfillNotificationTenants); err != nil {
		t.Fatalf("repeat notification tenant backfill: %v", err)
	}

	assertNotificationTenant(t, db, tenantNotification.ID, tenantUser.TenantID)
	assertNotificationTenant(t, db, platformNotification.ID, 0)
}

func TestBackfillNotificationTenantsRejectsMismatchAndRollsBack(t *testing.T) {
	db := setupNotificationTenantBackfillDB(t)
	validUser := createNotificationTenantUser(t, db, 21, 201)
	invalidUser := createNotificationTenantUser(t, db, 22, 202)
	valid := createNotificationForBackfill(t, db, validUser.ID, 0)
	createNotificationForBackfill(t, db, invalidUser.ID, 999)

	err := db.Transaction(backfillNotificationTenants)
	if err == nil || !strings.Contains(err.Error(), "conflicts with recipient") {
		t.Fatalf("backfill error=%v want recipient tenant conflict", err)
	}
	assertNotificationTenant(t, db, valid.ID, 0)
}

func TestBackfillNotificationTenantsRejectsMissingRecipient(t *testing.T) {
	db := setupNotificationTenantBackfillDB(t)
	createNotificationForBackfill(t, db, 404, 0)

	err := db.Transaction(backfillNotificationTenants)
	if err == nil || !strings.Contains(err.Error(), "missing recipient") {
		t.Fatalf("backfill error=%v want missing recipient", err)
	}
}

func setupNotificationTenantBackfillDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "notification.db")), &gorm.Config{
		NamingStrategy: schema.NamingStrategy{TablePrefix: "t_", SingularTable: true},
		Logger:         logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&models.User{}, &models.Notification{}); err != nil {
		t.Fatalf("migrate notification tenant tables: %v", err)
	}
	return db
}

func createNotificationTenantUser(t *testing.T, db *gorm.DB, id, tenantID int64) *models.User {
	t.Helper()
	now := time.Now()
	user := &models.User{
		ID: id, TenantID: tenantID, Username: fmtNotificationTestValue("user", id), Nickname: "test",
		Status: enums.StatusOk, AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(user).Error; err != nil {
		t.Fatalf("create user %d: %v", id, err)
	}
	return user
}

func createNotificationForBackfill(t *testing.T, db *gorm.DB, recipientUserID, tenantID int64) *models.Notification {
	t.Helper()
	item := &models.Notification{
		TenantID: tenantID, RecipientUserID: recipientUserID, Title: "test", Status: enums.StatusOk, CreatedAt: time.Now(),
	}
	if err := db.Create(item).Error; err != nil {
		t.Fatalf("create notification: %v", err)
	}
	return item
}

func assertNotificationTenant(t *testing.T, db *gorm.DB, id, wantTenantID int64) {
	t.Helper()
	var item models.Notification
	if err := db.Take(&item, "id = ?", id).Error; err != nil {
		t.Fatalf("read notification %d: %v", id, err)
	}
	if item.TenantID != wantTenantID {
		t.Fatalf("notification %d tenant=%d want %d", id, item.TenantID, wantTenantID)
	}
}

func fmtNotificationTestValue(prefix string, id int64) string {
	return prefix + "-" + strconv.FormatInt(id, 10)
}
