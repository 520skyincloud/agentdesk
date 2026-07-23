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
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
)

func TestInitializeCustomerTagRuntimePoliciesIsIdempotentAndPreservesExisting(t *testing.T) {
	db := openCustomerTagRuntimePolicyMigrationSQLite(t)
	policies := []models.TenantCustomerTagPolicy{
		{TenantID: 101, IntentProfileID: 11, EvolutionDefaultEnabled: true, Status: enums.StatusOk},
		{TenantID: 202, IntentProfileID: 22, ReplyTagContextDefaultEnabled: true, Status: enums.StatusOk},
	}
	if err := db.Create(&policies).Error; err != nil {
		t.Fatal(err)
	}
	stores := []models.Store{
		{TenantID: 101, StoreCode: "existing", Name: "Existing", Status: enums.StatusOk},
		{TenantID: 101, StoreCode: "disabled", Name: "Disabled", Status: enums.StatusDisabled},
		{TenantID: 202, StoreCode: "reply", Name: "Reply", Status: enums.StatusOk},
		{TenantID: 202, StoreCode: "deleted", Name: "Deleted", Status: enums.StatusDeleted},
		{TenantID: 0, StoreCode: "unscoped", Name: "Unscoped", Status: enums.StatusOk},
	}
	if err := db.Create(&stores).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.StoreCustomerTagRuntimePolicy{
		TenantID: 101, StoreID: stores[0].ID,
		CustomerTagEvolutionEnabled: false, ReplyTagContextEnabled: true, Status: enums.StatusOk,
	}).Error; err != nil {
		t.Fatal(err)
	}
	for run := 0; run < 2; run++ {
		if err := initializeCustomerTagRuntimePolicies(db); err != nil {
			t.Fatalf("run %d: %v", run+1, err)
		}
	}
	var list []models.StoreCustomerTagRuntimePolicy
	if err := db.Order("tenant_id ASC, store_id ASC").Find(&list).Error; err != nil {
		t.Fatal(err)
	}
	if len(list) != 3 {
		t.Fatalf("runtime policy count=%d want=3", len(list))
	}
	byStore := make(map[int64]models.StoreCustomerTagRuntimePolicy, len(list))
	for i := range list {
		byStore[list[i].StoreID] = list[i]
	}
	if current := byStore[stores[0].ID]; current.CustomerTagEvolutionEnabled || !current.ReplyTagContextEnabled {
		t.Fatalf("existing policy was overwritten: %#v", current)
	}
	if current := byStore[stores[1].ID]; !current.CustomerTagEvolutionEnabled || current.ReplyTagContextEnabled {
		t.Fatalf("tenant defaults were not projected: %#v", current)
	}
	if current := byStore[stores[2].ID]; current.CustomerTagEvolutionEnabled || !current.ReplyTagContextEnabled {
		t.Fatalf("reply defaults were not projected: %#v", current)
	}
}

func TestInitializeCustomerTagRuntimePoliciesRejectsMissingTenantPolicy(t *testing.T) {
	db := openCustomerTagRuntimePolicyMigrationSQLite(t)
	if err := db.Create(&models.Store{TenantID: 303, StoreCode: "orphan", Name: "Orphan", Status: enums.StatusOk}).Error; err != nil {
		t.Fatal(err)
	}
	if err := initializeCustomerTagRuntimePolicies(db); err == nil {
		t.Fatal("missing tenant policy must block migration")
	}
}

func TestInitializeCustomerTagRuntimePoliciesMySQL(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("TEST_MYSQL_DSN"))
	if dsn == "" {
		t.Skip("TEST_MYSQL_DSN is not configured")
	}
	db, err := gorm.Open(mysql.Open(dsn), customerTagRuntimePolicyMigrationGORMConfig())
	if err != nil {
		t.Fatal(err)
	}
	if err := migrateCustomerTagRuntimePolicyTables(db); err != nil {
		t.Fatal(err)
	}
	tenantID := time.Now().UnixNano()
	if err := db.Create(&models.TenantCustomerTagPolicy{
		TenantID: tenantID, IntentProfileID: tenantID, EvolutionDefaultEnabled: true, Status: enums.StatusOk,
	}).Error; err != nil {
		t.Fatal(err)
	}
	store := &models.Store{TenantID: tenantID, StoreCode: fmt.Sprintf("runtime-%d", tenantID), Name: "MySQL runtime", Status: enums.StatusOk}
	if err := db.Create(store).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = db.Where("tenant_id = ?", tenantID).Delete(&models.StoreCustomerTagRuntimePolicy{}).Error
		_ = db.Where("tenant_id = ?", tenantID).Delete(&models.Store{}).Error
		_ = db.Where("tenant_id = ?", tenantID).Delete(&models.TenantCustomerTagPolicy{}).Error
	})
	if err := initializeCustomerTagRuntimePolicies(db); err != nil {
		t.Fatal(err)
	}
	if err := initializeCustomerTagRuntimePolicies(db); err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := db.Model(&models.StoreCustomerTagRuntimePolicy{}).Where("tenant_id = ? AND store_id = ?", tenantID, store.ID).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("MySQL policy count=%d want=1", count)
	}
}

func openCustomerTagRuntimePolicyMigrationSQLite(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), customerTagRuntimePolicyMigrationGORMConfig())
	if err != nil {
		t.Fatal(err)
	}
	if err := migrateCustomerTagRuntimePolicyTables(db); err != nil {
		t.Fatal(err)
	}
	return db
}

func migrateCustomerTagRuntimePolicyTables(db *gorm.DB) error {
	return db.AutoMigrate(&models.Store{}, &models.TenantCustomerTagPolicy{}, &models.StoreCustomerTagRuntimePolicy{})
}

func customerTagRuntimePolicyMigrationGORMConfig() *gorm.Config {
	return &gorm.Config{
		Logger:         logger.Default.LogMode(logger.Silent),
		NamingStrategy: schema.NamingStrategy{TablePrefix: "t_", SingularTable: true},
	}
}
