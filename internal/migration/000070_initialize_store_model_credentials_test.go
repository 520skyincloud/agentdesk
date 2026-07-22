package migration

import (
	"os"
	"strings"
	"testing"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"

	"github.com/glebarez/sqlite"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
)

func TestInitializeStoreModelCredentialsIsIdempotentAndNeverMigratesLegacyKeys(t *testing.T) {
	db := openStoreModelCredentialMigrationSQLite(t)
	stores := []models.Store{
		{TenantID: 101, StoreCode: "store-active", Name: "启用门店", Status: enums.StatusOk},
		{TenantID: 101, StoreCode: "store-disabled", Name: "停用门店", Status: enums.StatusDisabled},
		{TenantID: 101, StoreCode: "store-deleted", Name: "已删除门店", Status: enums.StatusDeleted},
		{TenantID: 0, StoreCode: "store-unscoped", Name: "无租户门店", Status: enums.StatusOk},
	}
	if err := db.Create(&stores).Error; err != nil {
		t.Fatal(err)
	}
	legacySecret := "legacy-key-must-not-migrate"
	if err := db.Create(&models.AIConfig{Name: "legacy", APIKey: legacySecret, Status: enums.StatusOk}).Error; err != nil {
		t.Fatal(err)
	}

	for run := 0; run < 2; run++ {
		if err := initializeStoreModelCredentials(db); err != nil {
			t.Fatalf("run %d: %v", run+1, err)
		}
	}

	var credentials []models.StoreModelCredential
	if err := db.Order("store_id ASC").Find(&credentials).Error; err != nil {
		t.Fatal(err)
	}
	if len(credentials) != 2 {
		t.Fatalf("credential count=%d want=2", len(credentials))
	}
	for _, credential := range credentials {
		if credential.Status != enums.StoreCredentialStatusUnconfigured || credential.CredentialRevision != 0 {
			t.Fatalf("unexpected credential state: %#v", credential)
		}
		if credential.EncryptedKey != "" || credential.CandidateEncryptedKey != "" ||
			strings.Contains(credential.EncryptedKey, legacySecret) || strings.Contains(credential.CandidateEncryptedKey, legacySecret) {
			t.Fatalf("legacy secret entered new credential: %#v", credential)
		}
	}

	var policies []models.StoreCredentialPolicy
	if err := db.Order("store_id ASC").Find(&policies).Error; err != nil {
		t.Fatal(err)
	}
	if len(policies) != 2 {
		t.Fatalf("policy count=%d want=2", len(policies))
	}
	for _, policy := range policies {
		if policy.AllowCredentialSelfService || policy.RequireSupervisorApproval || policy.Status != enums.StatusOk {
			t.Fatalf("policy must default closed: %#v", policy)
		}
	}
}

func TestInitializeStoreModelCredentialsMySQL(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("TEST_MYSQL_DSN"))
	if dsn == "" {
		t.Skip("TEST_MYSQL_DSN is not configured")
	}
	db, err := gorm.Open(mysql.Open(dsn), storeModelCredentialMigrationGORMConfig())
	if err != nil {
		t.Fatal(err)
	}
	if err = migrateStoreModelCredentialTables(db); err != nil {
		t.Fatal(err)
	}
	store := &models.Store{TenantID: 901, StoreCode: "mysql-store-credential", Name: "MySQL 门店", Status: enums.StatusOk}
	if err = db.Create(store).Error; err != nil {
		t.Fatal(err)
	}
	if err = initializeStoreModelCredentials(db); err != nil {
		t.Fatal(err)
	}
	if err = initializeStoreModelCredentials(db); err != nil {
		t.Fatal(err)
	}
	var credentialCount, policyCount int64
	if err = db.Model(&models.StoreModelCredential{}).Where("tenant_id = ? AND store_id = ?", store.TenantID, store.ID).Count(&credentialCount).Error; err != nil {
		t.Fatal(err)
	}
	if err = db.Model(&models.StoreCredentialPolicy{}).Where("tenant_id = ? AND store_id = ?", store.TenantID, store.ID).Count(&policyCount).Error; err != nil {
		t.Fatal(err)
	}
	if credentialCount != 1 || policyCount != 1 {
		t.Fatalf("MySQL counts credential=%d policy=%d", credentialCount, policyCount)
	}
}

func openStoreModelCredentialMigrationSQLite(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), storeModelCredentialMigrationGORMConfig())
	if err != nil {
		t.Fatal(err)
	}
	if err = migrateStoreModelCredentialTables(db); err != nil {
		t.Fatal(err)
	}
	return db
}

func migrateStoreModelCredentialTables(db *gorm.DB) error {
	return db.AutoMigrate(
		&models.Store{}, &models.AIConfig{}, &models.StoreModelCredential{},
		&models.StoreCredentialPolicy{}, &models.StoreModelCredentialAuditLog{},
	)
}

func storeModelCredentialMigrationGORMConfig() *gorm.Config {
	return &gorm.Config{
		Logger:         logger.Default.LogMode(logger.Silent),
		NamingStrategy: schema.NamingStrategy{TablePrefix: "t_", SingularTable: true},
	}
}
