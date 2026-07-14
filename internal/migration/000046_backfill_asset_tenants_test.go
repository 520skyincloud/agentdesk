package migration

import (
	"fmt"
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

func TestBackfillAssetTenants(t *testing.T) {
	db := setupAssetTenantMigrationDB(t)
	legacy := createAssetMigrationTenant(t, db, constants.LegacyDefaultTenantCode)
	tenantA := createAssetMigrationTenant(t, db, "asset-a")
	tenantB := createAssetMigrationTenant(t, db, "asset-b")
	userA := createAssetMigrationUser(t, db, tenantA.ID, "asset-user-a")
	platformUser := createAssetMigrationUser(t, db, 0, "asset-platform")

	creatorAsset := createAssetMigrationAsset(t, db, 0, "asset-by-creator", userA.ID)
	messageAsset := createAssetMigrationAsset(t, db, 0, "asset-by-message", platformUser.ID)
	htmlAsset := createAssetMigrationAsset(t, db, 0, "asset-by-html", 0)
	explicitAsset := createAssetMigrationAsset(t, db, tenantB.ID, "asset-explicit", 0)
	legacyAsset := createAssetMigrationAsset(t, db, 0, "asset-legacy", 0)

	createAssetMigrationMessage(t, db, tenantA.ID, enums.IMMessageTypeImage, "", fmt.Sprintf(`{"assetId":%q}`, messageAsset.AssetID))
	createAssetMigrationMessage(t, db, tenantB.ID, enums.IMMessageTypeHTML,
		fmt.Sprintf(`<p><img data-asset-id=%q data-provider="local" data-storage-key=%q></p>`, htmlAsset.AssetID, htmlAsset.StorageKey), "")

	for run := 0; run < 2; run++ {
		if err := db.Transaction(backfillAssetTenants); err != nil {
			t.Fatalf("backfill asset tenants run %d: %v", run+1, err)
		}
	}

	assertAssetMigrationTenant(t, db, creatorAsset.ID, tenantA.ID)
	assertAssetMigrationTenant(t, db, messageAsset.ID, tenantA.ID)
	assertAssetMigrationTenant(t, db, htmlAsset.ID, tenantB.ID)
	assertAssetMigrationTenant(t, db, explicitAsset.ID, tenantB.ID)
	assertAssetMigrationTenant(t, db, legacyAsset.ID, legacy.ID)
}

func TestBackfillAssetTenantsRollsBackCrossTenantReference(t *testing.T) {
	db := setupAssetTenantMigrationDB(t)
	createAssetMigrationTenant(t, db, constants.LegacyDefaultTenantCode)
	tenantA := createAssetMigrationTenant(t, db, "asset-conflict-a")
	tenantB := createAssetMigrationTenant(t, db, "asset-conflict-b")
	userA := createAssetMigrationUser(t, db, tenantA.ID, "asset-conflict-user")

	updatedBeforeConflict := createAssetMigrationAsset(t, db, 0, "asset-before-conflict", userA.ID)
	conflictAsset := createAssetMigrationAsset(t, db, 0, "asset-conflict", 0)
	createAssetMigrationMessage(t, db, tenantA.ID, enums.IMMessageTypeImage, "", fmt.Sprintf(`{"assetId":%q}`, conflictAsset.AssetID))
	createAssetMigrationMessage(t, db, tenantB.ID, enums.IMMessageTypeGIF, "", fmt.Sprintf(`{"assetId":%q}`, conflictAsset.AssetID))

	err := db.Transaction(backfillAssetTenants)
	if err == nil || !strings.Contains(err.Error(), "conflicts") {
		t.Fatalf("backfill error=%v want cross-tenant conflict", err)
	}
	assertAssetMigrationTenant(t, db, updatedBeforeConflict.ID, 0)
	assertAssetMigrationTenant(t, db, conflictAsset.ID, 0)
}

func TestBackfillAssetTenantsRollsBackMissingCreator(t *testing.T) {
	db := setupAssetTenantMigrationDB(t)
	createAssetMigrationTenant(t, db, constants.LegacyDefaultTenantCode)
	tenant := createAssetMigrationTenant(t, db, "asset-missing-creator")
	user := createAssetMigrationUser(t, db, tenant.ID, "asset-valid-creator")

	updatedBeforeFailure := createAssetMigrationAsset(t, db, 0, "asset-before-missing-creator", user.ID)
	missingCreatorAsset := createAssetMigrationAsset(t, db, 0, "asset-missing-creator", user.ID+999999)

	err := db.Transaction(backfillAssetTenants)
	if err == nil || !strings.Contains(err.Error(), "missing creator user") {
		t.Fatalf("backfill error=%v want missing creator failure", err)
	}
	assertAssetMigrationTenant(t, db, updatedBeforeFailure.ID, 0)
	assertAssetMigrationTenant(t, db, missingCreatorAsset.ID, 0)
}

func setupAssetTenantMigrationDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "asset-tenant.db")), &gorm.Config{
		NamingStrategy: schema.NamingStrategy{TablePrefix: "t_", SingularTable: true},
		Logger:         logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&models.Tenant{}, &models.User{}, &models.Asset{}, &models.Message{}); err != nil {
		t.Fatalf("migrate asset tenant tables: %v", err)
	}
	return db
}

func createAssetMigrationTenant(t *testing.T, db *gorm.DB, code string) *models.Tenant {
	t.Helper()
	item := &models.Tenant{
		TenantCode:         code,
		LegalName:          code,
		ShortName:          code,
		RegistrationType:   "test",
		RegistrationNo:     code,
		VerificationStatus: enums.TenantVerificationStatusVerified,
		Status:             enums.StatusOk,
		AuditFields:        assetMigrationAuditFields(),
	}
	if err := db.Create(item).Error; err != nil {
		t.Fatalf("create tenant %s: %v", code, err)
	}
	return item
}

func createAssetMigrationUser(t *testing.T, db *gorm.DB, tenantID int64, username string) *models.User {
	t.Helper()
	item := &models.User{TenantID: tenantID, Username: username, Status: enums.StatusOk, AuditFields: assetMigrationAuditFields()}
	if err := db.Create(item).Error; err != nil {
		t.Fatalf("create user %s: %v", username, err)
	}
	return item
}

func createAssetMigrationAsset(t *testing.T, db *gorm.DB, tenantID int64, assetID string, createUserID int64) *models.Asset {
	t.Helper()
	item := &models.Asset{
		TenantID:   tenantID,
		AssetID:    assetID,
		Provider:   enums.AssetProviderLocal,
		StorageKey: "asset-tests/" + assetID,
		Filename:   assetID + ".png",
		MimeType:   "image/png",
		Status:     enums.AssetStatusSuccess,
		AuditFields: models.AuditFields{
			CreatedAt:      time.Now(),
			CreateUserID:   createUserID,
			CreateUserName: "test",
			UpdatedAt:      time.Now(),
			UpdateUserID:   createUserID,
			UpdateUserName: "test",
		},
	}
	if err := db.Create(item).Error; err != nil {
		t.Fatalf("create asset %s: %v", assetID, err)
	}
	return item
}

func createAssetMigrationMessage(t *testing.T, db *gorm.DB, tenantID int64, messageType enums.IMMessageType, content, payload string) *models.Message {
	t.Helper()
	now := time.Now()
	item := &models.Message{
		TenantID:       tenantID,
		ConversationID: tenantID,
		SenderType:     enums.IMSenderTypeCustomer,
		MessageType:    messageType,
		Content:        content,
		Payload:        payload,
		SendStatus:     enums.IMMessageStatusSent,
		SentAt:         &now,
		AuditFields:    assetMigrationAuditFields(),
	}
	if err := db.Create(item).Error; err != nil {
		t.Fatalf("create %s message: %v", messageType, err)
	}
	return item
}

func assertAssetMigrationTenant(t *testing.T, db *gorm.DB, assetID, wantTenantID int64) {
	t.Helper()
	var item models.Asset
	if err := db.First(&item, "id = ?", assetID).Error; err != nil {
		t.Fatalf("read asset %d: %v", assetID, err)
	}
	if item.TenantID != wantTenantID {
		t.Fatalf("asset %d tenant=%d want=%d", assetID, item.TenantID, wantTenantID)
	}
}

func assetMigrationAuditFields() models.AuditFields {
	now := time.Now()
	return models.AuditFields{CreatedAt: now, CreateUserName: "test", UpdatedAt: now, UpdateUserName: "test"}
}
