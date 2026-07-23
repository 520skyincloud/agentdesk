package repositories

import (
	"os"
	"strings"
	"testing"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"

	"github.com/glebarez/sqlite"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
)

func TestStoreModelCredentialUsableTestTargetHasNoScanLimit(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{
		NamingStrategy: schema.NamingStrategy{TablePrefix: "t_", SingularTable: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	assertStoreModelCredentialUsableTestTargetHasNoScanLimit(t, db)
}

func TestStoreModelCredentialUsableTestTargetMySQL(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("TEST_MYSQL_DSN"))
	if dsn == "" {
		t.Skip("TEST_MYSQL_DSN is not configured")
	}
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{
		Logger:         logger.Default.LogMode(logger.Silent),
		NamingStrategy: schema.NamingStrategy{TablePrefix: "t_", SingularTable: true},
	})
	if err != nil {
		t.Fatalf("open MySQL test database: %v", err)
	}
	resetStoreModelCredentialRepositoryTestTables(t, db)
	t.Cleanup(func() {
		resetStoreModelCredentialRepositoryTestTables(t, db)
	})
	assertStoreModelCredentialUsableTestTargetHasNoScanLimit(t, db)
}

func assertStoreModelCredentialUsableTestTargetHasNoScanLimit(t *testing.T, db *gorm.DB) {
	t.Helper()
	if err := db.AutoMigrate(storeModelCredentialRepositoryTestModels()...); err != nil {
		t.Fatal(err)
	}

	invalidCredentials := make([]models.StoreModelCredential, 0, 1001)
	for index := int64(1); index <= 1001; index++ {
		invalidCredentials = append(invalidCredentials, models.StoreModelCredential{
			TenantID:           index,
			StoreID:            index,
			EncryptedKey:       "ciphertext",
			CredentialRevision: 1,
			Status:             enums.StoreCredentialStatusActive,
		})
	}
	if err := db.CreateInBatches(&invalidCredentials, 100).Error; err != nil {
		t.Fatal(err)
	}

	now := time.Now()
	tenant := &models.Tenant{
		ID: 2001, TenantCode: "usable-target", LegalName: "Usable Target",
		RegistrationNo: "USABLE-TARGET", Status: enums.StatusOk,
	}
	store := &models.Store{ID: 2001, TenantID: tenant.ID, StoreCode: "usable-store", Name: "Usable Store", Status: enums.StatusOk}
	template := &models.ModelProfileTemplate{
		Code: "usable-profile", Name: "Usable Profile", Revision: 1,
		GatewayBaseURL: "https://newapi.example.com/v1", Status: enums.ModelProfileStatusActive,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(tenant).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(store).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(template).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.StoreModelProfileAssignment{
		TenantID: tenant.ID, StoreID: store.ID,
		TemplateID: template.ID, TemplateRevision: template.Revision,
		Status: enums.StoreModelAssignmentStatusReady, ReadinessStatus: "ready",
		AssignedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.StoreModelCredential{
		TenantID: tenant.ID, StoreID: store.ID,
		EncryptedKey: "usable-ciphertext", CredentialRevision: 1,
		Status: enums.StoreCredentialStatusActive,
	}).Error; err != nil {
		t.Fatal(err)
	}

	exists, err := StoreModelCredentialRepository.HasUsableProfileTestTarget(db)
	if err != nil {
		t.Fatalf("HasUsableProfileTestTarget() error=%v", err)
	}
	if !exists {
		t.Fatalf("usable target after %d unrelated active credentials was not found", len(invalidCredentials))
	}

	targets, err := StoreModelCredentialRepository.FindUsableProfileTestTargets(db, 1)
	if err != nil {
		t.Fatalf("FindUsableProfileTestTargets() error=%v", err)
	}
	if len(targets) != 1 || targets[0].TenantID != tenant.ID ||
		targets[0].StoreID != store.ID || targets[0].CredentialRevision != 1 {
		t.Fatalf("unexpected projected target metadata: %#v", targets)
	}
}

func storeModelCredentialRepositoryTestModels() []any {
	return []any{
		&models.Tenant{},
		&models.Store{},
		&models.ModelProfileTemplate{},
		&models.StoreModelProfileAssignment{},
		&models.StoreModelCredential{},
	}
}

func resetStoreModelCredentialRepositoryTestTables(t *testing.T, db *gorm.DB) {
	t.Helper()
	if err := db.Exec("SET FOREIGN_KEY_CHECKS = 0").Error; err != nil {
		t.Fatalf("disable MySQL foreign key checks: %v", err)
	}
	modelsToDrop := storeModelCredentialRepositoryTestModels()
	for index := len(modelsToDrop) - 1; index >= 0; index-- {
		if err := db.Migrator().DropTable(modelsToDrop[index]); err != nil {
			t.Fatalf("drop MySQL test table: %v", err)
		}
	}
	if err := db.Exec("SET FOREIGN_KEY_CHECKS = 1").Error; err != nil {
		t.Fatalf("enable MySQL foreign key checks: %v", err)
	}
}
