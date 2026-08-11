package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"

	"github.com/glebarez/sqlite"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
)

const migrationMainHelperEnv = "GO_WANT_MIGRATION_MAIN_HELPER"

func TestMigrationMainHelper(t *testing.T) {
	if os.Getenv(migrationMainHelperEnv) != "1" {
		return
	}
	os.Args = []string{"migration", "-config", os.Getenv("MIGRATION_TEST_CONFIG")}
	main()
}

func TestMigrationMainExitsNonzeroWhenAutoMigrateFails(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "duplicate.db")
	db := openMigrationCommandTestDB(t, dbPath)
	if err := db.AutoMigrate(&models.Store{}); err != nil {
		t.Fatalf("create store schema: %v", err)
	}
	if err := db.Migrator().DropIndex(&models.Store{}, "uk_store_tenant_code"); err != nil {
		t.Fatalf("drop store unique index: %v", err)
	}
	if err := db.Create(&[]models.Store{
		{TenantID: 101, StoreCode: "duplicate"},
		{TenantID: 101, StoreCode: "duplicate"},
	}).Error; err != nil {
		t.Fatalf("seed duplicate stores: %v", err)
	}
	closeMigrationCommandTestDB(t, db)
	configPath := writeMigrationCommandConfig(t, dir, dbPath)

	command := migrationMainHelperCommand(t, configPath)
	err := command.Run()
	exitError, ok := err.(*exec.ExitError)
	if !ok || exitError.ExitCode() == 0 {
		t.Fatalf("migration failure exit error = %v, want nonzero exit", err)
	}

	verifyDB := openMigrationCommandTestDB(t, dbPath)
	defer closeMigrationCommandTestDB(t, verifyDB)
	var count int64
	if err := verifyDB.Model(&models.Store{}).Where("tenant_id = ? AND store_code = ?", 101, "duplicate").Count(&count).Error; err != nil {
		t.Fatalf("count duplicate stores: %v", err)
	}
	if count != 2 {
		t.Fatalf("failed migration changed duplicate rows: %d", count)
	}
}

func TestMigrationMainUsesExplicitConfigAndExitsZero(t *testing.T) {
	const (
		bootstrapUsername = "deployment-bootstrap-admin"
		bootstrapPassword = "DeploymentPassword123!"
	)
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "success.db")
	configPath := writeMigrationCommandConfig(t, dir, dbPath)
	command := migrationMainHelperCommand(t, configPath)
	command.Env = append(command.Env,
		"AGENT_DESK_BOOTSTRAP_ADMIN_USERNAME="+bootstrapUsername,
		"AGENT_DESK_BOOTSTRAP_ADMIN_PASSWORD="+bootstrapPassword,
	)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("migration command failed: %v\n%s", err, output)
	}

	db := openMigrationCommandTestDB(t, dbPath)
	defer closeMigrationCommandTestDB(t, db)
	var successful int64
	if err := db.Model(&models.Migration{}).Where("success = ?", true).Count(&successful).Error; err != nil {
		t.Fatalf("count successful migrations: %v", err)
	}
	if successful != 10 {
		t.Fatalf("successful fresh initializers=%d want=10", successful)
	}
	var migrations []models.Migration
	if err := db.Order("version ASC").Find(&migrations).Error; err != nil {
		t.Fatalf("load fresh initializer identities: %v", err)
	}
	versions := make([]int64, 0, len(migrations))
	for i := range migrations {
		versions = append(versions, migrations[i].Version)
		if !migrations[i].Success {
			t.Fatalf("initializer %d failed: %+v", migrations[i].Version, migrations[i])
		}
	}
	if want := []int64{2, 15, 35, 68, 69, 70, 71, 72, 74, 75}; !reflect.DeepEqual(versions, want) {
		t.Fatalf("fresh initializer versions=%v want=%v", versions, want)
	}

	var admin models.User
	if err := db.Where("username = ?", bootstrapUsername).Take(&admin).Error; err != nil {
		t.Fatalf("load bootstrap administrator: %v", err)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(admin.Password), []byte(bootstrapPassword)); err != nil {
		t.Fatalf("bootstrap administrator password mismatch: %v", err)
	}
	var fallback models.Tenant
	if err := db.Where("tenant_code = ?", constants.LegacyDefaultTenantCode).Take(&fallback).Error; err != nil {
		t.Fatalf("load OIDC fallback tenant: %v", err)
	}
	if fallback.IntentProfileID <= 0 {
		t.Fatal("OIDC fallback tenant has no initialized industry")
	}
	var tenantTagCount int64
	if err := db.Model(&models.Tag{}).Where("tenant_id = ?", fallback.ID).Count(&tenantTagCount).Error; err != nil {
		t.Fatalf("count fallback tenant tags: %v", err)
	}
	if tenantTagCount != 35 {
		t.Fatalf("fallback tenant tag count=%d want=35", tenantTagCount)
	}

	var profile models.ModelProfileTemplate
	if err := db.Where("code = ? AND revision = ?", "standard", 1).Take(&profile).Error; err != nil {
		t.Fatalf("load default model profile: %v", err)
	}
	var slotCount int64
	if err := db.Model(&models.ModelProfileSlot{}).Where("template_id = ?", profile.ID).Count(&slotCount).Error; err != nil {
		t.Fatalf("count default model slots: %v", err)
	}
	if slotCount != 9 {
		t.Fatalf("default model slot count=%d want=9", slotCount)
	}
}

func migrationMainHelperCommand(t *testing.T, configPath string) *exec.Cmd {
	t.Helper()
	command := exec.Command(os.Args[0], "-test.run=^TestMigrationMainHelper$")
	command.Env = append(os.Environ(), migrationMainHelperEnv+"=1", "MIGRATION_TEST_CONFIG="+configPath)
	return command
}

func writeMigrationCommandConfig(t *testing.T, dir, dbPath string) string {
	t.Helper()
	configPath := filepath.Join(dir, "config.yaml")
	content := fmt.Sprintf("db:\n  type: sqlite\n  dsn: file:%s?_busy_timeout=5000\nlogger:\n  level: warn\n  format: text\n", dbPath)
	if err := os.WriteFile(configPath, []byte(content), 0o600); err != nil {
		t.Fatalf("write migration config: %v", err)
	}
	return configPath
}

func openMigrationCommandTestDB(t *testing.T, dbPath string) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+dbPath+"?_busy_timeout=5000"), &gorm.Config{
		Logger:         logger.Default.LogMode(logger.Silent),
		NamingStrategy: schema.NamingStrategy{TablePrefix: "t_", SingularTable: true},
	})
	if err != nil {
		t.Fatalf("open migration command db: %v", err)
	}
	return db
}

func closeMigrationCommandTestDB(t *testing.T, db *gorm.DB) {
	t.Helper()
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("access migration command db: %v", err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatalf("close migration command db: %v", err)
	}
}
