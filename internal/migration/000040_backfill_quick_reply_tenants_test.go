package migration

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/enums"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
)

func TestBackfillQuickReplyTenantsIsIdempotentAndPreservesExplicitTenant(t *testing.T) {
	db := setupQuickReplyTenantBackfillDB(t)
	legacy := createQuickReplyTenant(t, db, constants.LegacyDefaultTenantCode)
	other := createQuickReplyTenant(t, db, "quick-reply-other")
	historical := &models.QuickReply{GroupName: "历史", Title: "历史快捷回复", Content: "content", Status: enums.StatusOk, AuditFields: quickReplyAuditFields()}
	explicit := &models.QuickReply{TenantID: other.ID, GroupName: "显式", Title: "显式快捷回复", Content: "content", Status: enums.StatusOk, AuditFields: quickReplyAuditFields()}
	if err := db.Create(historical).Error; err != nil {
		t.Fatalf("create historical quick reply: %v", err)
	}
	if err := db.Create(explicit).Error; err != nil {
		t.Fatalf("create explicit quick reply: %v", err)
	}

	if err := db.Transaction(backfillQuickReplyTenants); err != nil {
		t.Fatalf("backfill quick replies: %v", err)
	}
	if err := db.Transaction(backfillQuickReplyTenants); err != nil {
		t.Fatalf("repeat quick reply backfill: %v", err)
	}

	assertQuickReplyTenant(t, db, historical.ID, legacy.ID)
	assertQuickReplyTenant(t, db, explicit.ID, other.ID)
}

func TestBackfillQuickReplyTenantsRejectsMissingExplicitTenantAndRollsBack(t *testing.T) {
	db := setupQuickReplyTenantBackfillDB(t)
	createQuickReplyTenant(t, db, constants.LegacyDefaultTenantCode)
	historical := &models.QuickReply{Title: "历史", Content: "content", Status: enums.StatusOk, AuditFields: quickReplyAuditFields()}
	invalid := &models.QuickReply{TenantID: 999, Title: "无效租户", Content: "content", Status: enums.StatusOk, AuditFields: quickReplyAuditFields()}
	if err := db.Create(historical).Error; err != nil {
		t.Fatalf("create historical quick reply: %v", err)
	}
	if err := db.Create(invalid).Error; err != nil {
		t.Fatalf("create invalid quick reply: %v", err)
	}

	err := db.Transaction(backfillQuickReplyTenants)
	if err == nil || !strings.Contains(err.Error(), "missing tenant") {
		t.Fatalf("backfill error=%v want missing tenant", err)
	}
	assertQuickReplyTenant(t, db, historical.ID, 0)
}

func setupQuickReplyTenantBackfillDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "quick-reply.db")), &gorm.Config{
		NamingStrategy: schema.NamingStrategy{TablePrefix: "t_", SingularTable: true},
		Logger:         logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&models.Tenant{}, &models.QuickReply{}); err != nil {
		t.Fatalf("migrate quick reply tables: %v", err)
	}
	return db
}

func createQuickReplyTenant(t *testing.T, db *gorm.DB, code string) *models.Tenant {
	t.Helper()
	tenant := &models.Tenant{
		TenantCode: code, LegalName: code, ShortName: code, RegistrationType: "test", RegistrationNo: "REG-" + code,
		VerificationStatus: enums.TenantVerificationStatusVerified, Status: enums.StatusOk, AuditFields: quickReplyAuditFields(),
	}
	if err := db.Create(tenant).Error; err != nil {
		t.Fatalf("create tenant %s: %v", code, err)
	}
	return tenant
}

func assertQuickReplyTenant(t *testing.T, db *gorm.DB, id, wantTenantID int64) {
	t.Helper()
	var item models.QuickReply
	if err := db.Take(&item, "id = ?", id).Error; err != nil {
		t.Fatalf("read quick reply %d: %v", id, err)
	}
	if item.TenantID != wantTenantID {
		t.Fatalf("quick reply %d tenant = %d, want %d", id, item.TenantID, wantTenantID)
	}
}

func quickReplyAuditFields() models.AuditFields {
	now := time.Now()
	return models.AuditFields{CreatedAt: now, UpdatedAt: now}
}
