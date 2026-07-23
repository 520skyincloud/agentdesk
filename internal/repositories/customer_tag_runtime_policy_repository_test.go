package repositories

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

func TestStoreCustomerTagRuntimePolicyRepositorySQLite(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), customerTagRuntimePolicyRepositoryGORMConfig())
	if err != nil {
		t.Fatal(err)
	}
	testStoreCustomerTagRuntimePolicyRepository(t, db, time.Now().UnixNano())
}

func TestStoreCustomerTagRuntimePolicyRepositoryMySQL(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("TEST_MYSQL_DSN"))
	if dsn == "" {
		t.Skip("TEST_MYSQL_DSN is not configured")
	}
	db, err := gorm.Open(mysql.Open(dsn), customerTagRuntimePolicyRepositoryGORMConfig())
	if err != nil {
		t.Fatal(err)
	}
	testStoreCustomerTagRuntimePolicyRepository(t, db, time.Now().UnixNano())
}

func testStoreCustomerTagRuntimePolicyRepository(t *testing.T, db *gorm.DB, tenantID int64) {
	t.Helper()
	if err := db.AutoMigrate(&models.Store{}, &models.StoreCustomerTagRuntimePolicy{}); err != nil {
		t.Fatal(err)
	}
	stores := []models.Store{
		{TenantID: tenantID, StoreCode: fmt.Sprintf("active-%d", tenantID), Name: "Active Store", Status: enums.StatusOk},
		{TenantID: tenantID, StoreCode: fmt.Sprintf("disabled-%d", tenantID), Name: "Disabled Store", Status: enums.StatusDisabled},
		{TenantID: tenantID, StoreCode: fmt.Sprintf("deleted-%d", tenantID), Name: "Deleted Store", Status: enums.StatusDeleted},
	}
	if err := db.Create(&stores).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = db.Where("tenant_id = ?", tenantID).Delete(&models.StoreCustomerTagRuntimePolicy{}).Error
		_ = db.Where("tenant_id = ?", tenantID).Delete(&models.Store{}).Error
	})
	activePolicy := models.StoreCustomerTagRuntimePolicy{
		TenantID: tenantID, StoreID: stores[0].ID,
		CustomerTagEvolutionEnabled: true, Status: enums.StatusOk,
	}
	if err := db.Create(&activePolicy).Error; err != nil {
		t.Fatal(err)
	}

	rows, total, err := StoreCustomerTagRuntimePolicyRepository.FindStorePage(
		db, tenantID, StoreCustomerTagRuntimePolicyListFilter{Page: 1, Limit: 20},
	)
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 || len(rows) != 2 || rows[0].StoreID != stores[0].ID || rows[0].PolicyID != activePolicy.ID || !rows[0].CustomerTagEvolutionEnabled {
		t.Fatalf("unfiltered rows=%#v total=%d", rows, total)
	}
	disabled := false
	rows, total, err = StoreCustomerTagRuntimePolicyRepository.FindStorePage(
		db, tenantID, StoreCustomerTagRuntimePolicyListFilter{Page: 1, Limit: 20, EvolutionEnabled: &disabled},
	)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(rows) != 1 || rows[0].StoreID != stores[1].ID || rows[0].PolicyID != 0 {
		t.Fatalf("disabled rows=%#v total=%d", rows, total)
	}

	now := time.Now()
	if err := StoreCustomerTagRuntimePolicyRepository.UpsertBatch(db, []models.StoreCustomerTagRuntimePolicy{{
		TenantID: tenantID, StoreID: stores[1].ID,
		ReplyTagContextEnabled: true, Status: enums.StatusOk,
		AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}}, []string{"reply_tag_context_enabled", "status", "updated_at"}); err != nil {
		t.Fatal(err)
	}
	enabled := true
	rows, total, err = StoreCustomerTagRuntimePolicyRepository.FindStorePage(
		db, tenantID, StoreCustomerTagRuntimePolicyListFilter{Page: 1, Limit: 20, ReplyEnabled: &enabled},
	)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(rows) != 1 || rows[0].StoreID != stores[1].ID || !rows[0].ReplyTagContextEnabled {
		t.Fatalf("reply-enabled rows=%#v total=%d", rows, total)
	}
}

func customerTagRuntimePolicyRepositoryGORMConfig() *gorm.Config {
	return &gorm.Config{
		Logger:         logger.Default.LogMode(logger.Silent),
		NamingStrategy: schema.NamingStrategy{TablePrefix: "t_", SingularTable: true},
	}
}
