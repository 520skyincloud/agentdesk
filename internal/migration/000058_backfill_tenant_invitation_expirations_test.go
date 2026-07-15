package migration

import (
	"path/filepath"
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

func TestBackfillTenantInvitationExpirationsIsCompatibleAndIdempotent(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "tenant-invitation-expiration.db")), &gorm.Config{
		NamingStrategy: schema.NamingStrategy{TablePrefix: "t_", SingularTable: true},
		Logger:         logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.TenantInvitation{}); err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, time.July, 15, 9, 0, 0, 0, time.UTC)
	presetExpiry := now.Add(14 * 24 * time.Hour)
	items := []*models.TenantInvitation{
		newTenantInvitationBackfillFixture(1, enums.StatusOk, nil, now),
		newTenantInvitationBackfillFixture(2, enums.StatusDisabled, nil, now),
		newTenantInvitationBackfillFixture(3, enums.StatusOk, &presetExpiry, now),
	}
	for _, item := range items {
		if err := db.Create(item).Error; err != nil {
			t.Fatal(err)
		}
	}

	if err := backfillTenantInvitationExpirations(db, now); err != nil {
		t.Fatal(err)
	}
	wantExpiry := now.Add(time.Duration(constants.TenantInvitationValidityDays) * 24 * time.Hour)
	assertTenantInvitationExpiry(t, db, items[0].ID, &wantExpiry)
	assertTenantInvitationExpiry(t, db, items[1].ID, nil)
	assertTenantInvitationExpiry(t, db, items[2].ID, &presetExpiry)

	if err := backfillTenantInvitationExpirations(db, now.Add(24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	assertTenantInvitationExpiry(t, db, items[0].ID, &wantExpiry)
}

func newTenantInvitationBackfillFixture(version int, status enums.Status, expiresAt *time.Time, now time.Time) *models.TenantInvitation {
	return &models.TenantInvitation{
		TenantID:       1,
		CodeHash:       string(rune('a' + version)),
		CodeCiphertext: "ciphertext",
		CodeLast4:      "last",
		Version:        version,
		ExpiresAt:      expiresAt,
		Status:         status,
		AuditFields: models.AuditFields{
			CreatedAt: now, CreateUserName: "test", UpdatedAt: now, UpdateUserName: "test",
		},
	}
}

func assertTenantInvitationExpiry(t *testing.T, db *gorm.DB, id int64, want *time.Time) {
	t.Helper()
	var item models.TenantInvitation
	if err := db.First(&item, "id = ?", id).Error; err != nil {
		t.Fatal(err)
	}
	if want == nil {
		if item.ExpiresAt != nil {
			t.Fatalf("invitation %d expiry=%v want nil", id, item.ExpiresAt)
		}
		return
	}
	if item.ExpiresAt == nil || !item.ExpiresAt.Equal(*want) {
		t.Fatalf("invitation %d expiry=%v want=%v", id, item.ExpiresAt, want)
	}
}
