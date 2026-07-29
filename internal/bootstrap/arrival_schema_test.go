package bootstrap

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"agent-desk/internal/models"

	"github.com/glebarez/sqlite"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
)

func TestArrivalSchemaAutoMigrateSQLite(t *testing.T) {
	db, err := gorm.Open(
		sqlite.Open("file:"+strings.ReplaceAll(t.Name(), "/", "_")+"?mode=memory&cache=shared"),
		arrivalSchemaGORMConfig("t_"),
	)
	if err != nil {
		t.Fatalf("open SQLite: %v", err)
	}
	assertArrivalSchemaAutoMigrate(t, db)
}

func TestArrivalSchemaAutoMigrateMySQL(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("TEST_MYSQL_DSN"))
	if dsn == "" {
		t.Skip("TEST_MYSQL_DSN is not configured")
	}
	prefix := fmt.Sprintf("arrival_schema_%d_", time.Now().UnixNano())
	db, err := gorm.Open(mysql.Open(dsn), arrivalSchemaGORMConfig(prefix))
	if err != nil {
		t.Fatalf("open MySQL: %v", err)
	}
	modelsToMigrate := arrivalSchemaModels()
	t.Cleanup(func() {
		for i := len(modelsToMigrate) - 1; i >= 0; i-- {
			if err := db.Migrator().DropTable(modelsToMigrate[i]); err != nil {
				t.Errorf("drop MySQL arrival fixture %T: %v", modelsToMigrate[i], err)
			}
		}
	})
	assertArrivalSchemaAutoMigrate(t, db)
}

func assertArrivalSchemaAutoMigrate(t *testing.T, db *gorm.DB) {
	t.Helper()
	modelsToMigrate := arrivalSchemaModels()
	if err := db.AutoMigrate(modelsToMigrate...); err != nil {
		t.Fatalf("arrival AutoMigrate: %v", err)
	}
	if err := db.AutoMigrate(modelsToMigrate...); err != nil {
		t.Fatalf("arrival idempotent AutoMigrate: %v", err)
	}
	for _, model := range modelsToMigrate {
		if !db.Migrator().HasTable(model) {
			t.Errorf("arrival table for %T was not created", model)
		}
	}
	for _, index := range []struct {
		model any
		name  string
	}{
		{&models.MiniProgramIdentity{}, "uk_arrival_identity"},
		{&models.WeComTenantAuthorization{}, "uk_arrival_corp_auth"},
		{&models.ArrivalStoreBinding{}, "uk_arrival_store_binding"},
	} {
		if !db.Migrator().HasIndex(index.model, index.name) {
			t.Errorf("arrival unique index %s for %T was not created", index.name, index.model)
		}
	}
}

func arrivalSchemaModels() []any {
	return []any{
		&models.MiniProgramIdentity{},
		&models.WeComSuiteCredential{},
		&models.WeComTenantAuthorization{},
		&models.StoreArrivalConnection{},
		&models.StoreArrivalInvitation{},
		&models.WeComAuthorizationAttempt{},
		&models.ArrivalScanEvent{},
		&models.ArrivalSession{},
		&models.ArrivalContactWay{},
		&models.ArrivalStoreBinding{},
		&models.WeComProviderCallbackEvent{},
		&models.ArrivalAuditLog{},
	}
}

func arrivalSchemaGORMConfig(prefix string) *gorm.Config {
	return &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
		NamingStrategy: schema.NamingStrategy{
			TablePrefix:   prefix,
			SingularTable: true,
		},
	}
}
