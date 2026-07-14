package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"agent-desk/internal/models"

	"github.com/glebarez/sqlite"
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
	if err := db.AutoMigrate(&models.Company{}); err != nil {
		t.Fatalf("create company schema: %v", err)
	}
	if err := db.Migrator().DropIndex(&models.Company{}, "uk_company_tenant_name"); err != nil {
		t.Fatalf("drop company unique index: %v", err)
	}
	if err := db.Create(&[]models.Company{
		{TenantID: 101, Name: "duplicate"},
		{TenantID: 101, Name: "duplicate"},
	}).Error; err != nil {
		t.Fatalf("seed duplicate companies: %v", err)
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
	if err := verifyDB.Model(&models.Company{}).Where("tenant_id = ? AND name = ?", 101, "duplicate").Count(&count).Error; err != nil {
		t.Fatalf("count duplicate companies: %v", err)
	}
	if count != 2 {
		t.Fatalf("failed migration changed duplicate rows: %d", count)
	}
}

func TestMigrationMainUsesExplicitConfigAndExitsZero(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "success.db")
	configPath := writeMigrationCommandConfig(t, dir, dbPath)
	command := migrationMainHelperCommand(t, configPath)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("migration command failed: %v\n%s", err, output)
	}

	db := openMigrationCommandTestDB(t, dbPath)
	defer closeMigrationCommandTestDB(t, db)
	var successful int64
	if err := db.Model(&models.Migration{}).Where("success = ?", true).Count(&successful).Error; err != nil {
		t.Fatalf("count successful migrations: %v", err)
	}
	if successful == 0 {
		t.Fatal("explicit config database did not run migrations")
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
