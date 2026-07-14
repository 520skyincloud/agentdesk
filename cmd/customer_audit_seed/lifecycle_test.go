package main

import (
	"path/filepath"
	"testing"

	"agent-desk/internal/bootstrap"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/config"
	"agent-desk/internal/pkg/constants"

	"gorm.io/gorm"
)

func TestFreshDatabaseSeedLifecycle(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "customer-audit-lifecycle.db")
	db, err := bootstrap.InitDB(config.DBConfig{
		Type:         "sqlite",
		DSN:          "file:" + databasePath + "?_busy_timeout=5000",
		MaxIdleConns: 1,
		MaxOpenConns: 1,
	})
	if err != nil {
		t.Fatalf("initialize fresh database: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get fresh database connection: %v", err)
	}
	t.Cleanup(func() {
		_ = sqlDB.Close()
	})
	if err := bootstrap.InitMigrations(); err != nil {
		t.Fatalf("migrate fresh database: %v", err)
	}

	batch := "fresh-lifecycle"
	if err := seed(db, batch, defaultPassword); err != nil {
		t.Fatalf("seed fresh database: %v", err)
	}
	first := buildReport(db, batch)
	assertCompleteSimulationReport(t, first)

	if err := seed(db, batch, defaultPassword); err != nil {
		t.Fatalf("repeat seed: %v", err)
	}
	second := buildReport(db, batch)
	assertCompleteSimulationReport(t, second)
	if second != first {
		t.Fatalf("repeated seed changed report:\nfirst=%+v\nsecond=%+v", first, second)
	}

	if err := cleanup(db, batch); err != nil {
		t.Fatalf("cleanup seed data: %v", err)
	}
	empty := buildReport(db, batch)
	wantEmpty := report{Batch: batch, Marker: marker(batch)}
	if empty != wantEmpty {
		t.Fatalf("cleanup left report data:\ngot=%+v\nwant=%+v", empty, wantEmpty)
	}

	for name, model := range map[string]any{
		"conversations": &models.Conversation{},
		"route states":  &models.ConversationRouteState{},
		"participants":  &models.ConversationParticipant{},
		"messages":      &models.Message{},
		"assignments":   &models.ConversationAssignment{},
		"event logs":    &models.ConversationEventLog{},
	} {
		assertLifecycleRowCount(t, db, name, model, 0)
	}

	assertLifecycleSystemData(t, db)
}

func assertCompleteSimulationReport(t *testing.T, got report) {
	t.Helper()
	if !got.ExpectedCoreComplete || !got.ExpectedSimulationComplete || !got.SimulationBaselineIntact {
		t.Fatalf("simulation report is incomplete: %+v", got)
	}
	if got.CustomerContacts != 500 || got.CustomerIdentities != 500 || got.StoreCustomerRels != 801 {
		t.Fatalf("customer relationship baseline changed: %+v", got)
	}
	if got.SimulatedConversations != 36 || got.SimulatedMessages != 135 || got.SimulatedAssignments != 21 ||
		got.SimulatedCurrentlyAssigned != 18 || got.SimulatedAssignedAgents != 12 || got.SimulatedNeedReply != 27 {
		t.Fatalf("dispatch simulation baseline changed: %+v", got)
	}
}

func assertLifecycleRowCount(t *testing.T, db *gorm.DB, name string, model any, want int64) {
	t.Helper()
	var count int64
	if err := db.Model(model).Count(&count).Error; err != nil {
		t.Fatalf("count %s: %v", name, err)
	}
	if count != want {
		t.Fatalf("%s count=%d want=%d", name, count, want)
	}
}

func assertLifecycleSystemData(t *testing.T, db *gorm.DB) {
	t.Helper()
	var count int64
	if err := db.Model(&models.User{}).Count(&count).Error; err != nil {
		t.Fatalf("count users after cleanup: %v", err)
	}
	if count != 1 {
		t.Fatalf("user count after cleanup=%d want=1", count)
	}
	if err := db.Model(&models.User{}).Where("username = ?", constants.BootstrapAdminUsername).Count(&count).Error; err != nil {
		t.Fatalf("count bootstrap admin: %v", err)
	}
	if count != 1 {
		t.Fatalf("bootstrap admin count=%d want=1", count)
	}
	if err := db.Model(&models.Permission{}).Where("code = ?", constants.PermissionDashboardView.Code).Count(&count).Error; err != nil {
		t.Fatalf("count dashboard permission: %v", err)
	}
	if count != 1 {
		t.Fatalf("dashboard permission count=%d want=1", count)
	}
	roleCodes := []string{
		constants.RoleCodeSuperAdmin,
		constants.RoleCodeAdmin,
		constants.RoleCodeTenantAdmin,
		constants.RoleCodeCsTeamLeader,
		constants.RoleCodeCsUser,
		constants.RoleCodeStoreStaff,
	}
	if err := db.Model(&models.Role{}).Where("code IN ?", roleCodes).Count(&count).Error; err != nil {
		t.Fatalf("count builtin roles: %v", err)
	}
	if count != int64(len(roleCodes)) {
		t.Fatalf("builtin role count=%d want=%d", count, len(roleCodes))
	}
	var migration models.Migration
	if err := db.Where("version = ? AND success = ?", 54, true).Take(&migration).Error; err != nil {
		t.Fatalf("migration 54 was not preserved: %v", err)
	}
}
