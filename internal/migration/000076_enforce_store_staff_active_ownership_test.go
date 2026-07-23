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

func TestEnforceStoreStaffActiveOwnershipArchivesDuplicatesAndAddsUniqueGuard(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), storeStaffOwnershipMigrationGORMConfig())
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	runEnforceStoreStaffActiveOwnershipScenario(t, db, 101, fmt.Sprintf("%d", time.Now().UnixNano()))
}

func TestEnforceStoreStaffActiveOwnershipMySQL(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("TEST_MYSQL_DSN"))
	if dsn == "" {
		t.Skip("TEST_MYSQL_DSN is not configured")
	}
	db, err := gorm.Open(mysql.Open(dsn), storeStaffOwnershipMigrationGORMConfig())
	if err != nil {
		t.Fatalf("open mysql: %v", err)
	}
	runEnforceStoreStaffActiveOwnershipScenario(t, db, time.Now().UnixNano(), fmt.Sprintf("%d", time.Now().UnixNano()))
}

func runEnforceStoreStaffActiveOwnershipScenario(t *testing.T, db *gorm.DB, tenantID int64, suffix string) {
	t.Helper()
	if err := db.AutoMigrate(
		&models.User{},
		&models.Store{},
		&models.StoreStaffBinding{},
		&models.WxWorkProtocolInstance{},
	); err != nil {
		t.Fatalf("migrate store ownership fixtures: %v", err)
	}

	account := models.User{TenantID: tenantID, Username: "store-owner-" + suffix, Status: enums.StatusOk}
	if err := db.Create(&account).Error; err != nil {
		t.Fatalf("create Store account: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Where("tenant_id = ?", tenantID).Delete(&models.WxWorkProtocolInstance{}).Error
		_ = db.Where("tenant_id = ?", tenantID).Delete(&models.StoreStaffBinding{}).Error
		_ = db.Where("tenant_id = ?", tenantID).Delete(&models.Store{}).Error
		_ = db.Delete(&models.User{}, account.ID).Error
	})
	stores := make([]models.Store, 4)
	for index := range stores {
		stores[index] = models.Store{
			TenantID:  tenantID,
			StoreCode: fmt.Sprintf("STORE-%s-%d", suffix, index+1),
			Name:      fmt.Sprintf("Store %d", index+1),
			Status:    enums.StatusOk,
		}
		if err := db.Create(&stores[index]).Error; err != nil {
			t.Fatalf("create Store %d: %v", index+1, err)
		}
	}
	bindings := []models.StoreStaffBinding{
		{TenantID: tenantID, UserID: account.ID, StoreID: stores[0].ID, Status: enums.StatusOk},
		{TenantID: tenantID, UserID: account.ID, StoreID: stores[1].ID, Status: enums.StatusOk},
		{TenantID: tenantID, UserID: account.ID, StoreID: stores[2].ID, Status: enums.StatusDisabled},
	}
	if err := db.Create(&bindings).Error; err != nil {
		t.Fatalf("create duplicate historical bindings: %v", err)
	}
	instance := models.WxWorkProtocolInstance{
		TenantID: tenantID, Guid: "duplicate-binding-instance-" + suffix,
		StoreID: stores[1].ID, StoreStaffBindingID: bindings[1].ID,
		AIReplyEnabled: true, Status: enums.StatusOk,
	}
	if err := db.Create(&instance).Error; err != nil {
		t.Fatalf("create duplicate binding instance: %v", err)
	}

	if err := enforceStoreStaffActiveOwnership(db); err != nil {
		t.Fatalf("enforce ownership: %v", err)
	}
	if err := enforceStoreStaffActiveOwnership(db); err != nil {
		t.Fatalf("enforce ownership idempotently: %v", err)
	}

	var current []models.StoreStaffBinding
	if err := db.Where("user_id = ?", account.ID).Order("id ASC").Find(&current).Error; err != nil {
		t.Fatalf("load normalized bindings: %v", err)
	}
	if len(current) != 3 {
		t.Fatalf("binding count=%d", len(current))
	}
	if current[0].Status != enums.StatusOk || current[0].ActiveUserID == nil || *current[0].ActiveUserID != account.ID {
		t.Fatalf("canonical binding not active: %#v", current[0])
	}
	for index := 1; index < len(current); index++ {
		if current[index].Status != enums.StatusDeleted || current[index].ActiveUserID != nil ||
			!strings.Contains(current[index].Remark, "重复历史门店绑定已软归档") {
			t.Fatalf("duplicate binding %d was not archived: %#v", index, current[index])
		}
	}
	var currentInstance models.WxWorkProtocolInstance
	if err := db.First(&currentInstance, instance.ID).Error; err != nil {
		t.Fatalf("load duplicate instance: %v", err)
	}
	if currentInstance.Status != enums.StatusDisabled || currentInstance.AIReplyEnabled || currentInstance.HealthStatus != "pending_binding" {
		t.Fatalf("duplicate instance remained active: %#v", currentInstance)
	}

	conflictingUserID := account.ID
	conflict := models.StoreStaffBinding{
		TenantID: tenantID, UserID: account.ID, ActiveUserID: &conflictingUserID,
		StoreID: stores[3].ID, Status: enums.StatusOk,
	}
	if err := db.Create(&conflict).Error; err == nil {
		t.Fatal("database unique guard accepted a second active Store for the same account")
	}
}

func storeStaffOwnershipMigrationGORMConfig() *gorm.Config {
	return &gorm.Config{
		Logger:         logger.Default.LogMode(logger.Silent),
		NamingStrategy: schema.NamingStrategy{TablePrefix: "t_", SingularTable: true},
	}
}

func TestEnforceStoreStaffActiveOwnershipDisablesInvalidAccount(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s-%d?mode=memory&cache=shared", t.Name(), time.Now().UnixNano())), storeStaffOwnershipMigrationGORMConfig())
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&models.User{}, &models.StoreStaffBinding{}, &models.WxWorkProtocolInstance{}); err != nil {
		t.Fatalf("migrate invalid account fixtures: %v", err)
	}
	binding := models.StoreStaffBinding{TenantID: 101, UserID: 404, StoreID: 7, Status: enums.StatusOk}
	if err := db.Create(&binding).Error; err != nil {
		t.Fatalf("create invalid binding: %v", err)
	}
	if err := enforceStoreStaffActiveOwnership(db); err != nil {
		t.Fatalf("enforce invalid ownership: %v", err)
	}
	var current models.StoreStaffBinding
	if err := db.First(&current, binding.ID).Error; err != nil {
		t.Fatalf("load invalid binding: %v", err)
	}
	if current.Status != enums.StatusDisabled || current.ActiveUserID != nil {
		t.Fatalf("invalid account still owns an active Store: %#v", current)
	}
}
