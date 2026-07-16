package main

import (
	"encoding/base64"
	"path/filepath"
	"testing"
	"time"

	"agent-desk/internal/bootstrap"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/config"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/enums"

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
	config.SetCurrent(&config.Config{Auth: config.AuthConfig{
		InvitationEncryptionKey: base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef")),
	}})
	if err := db.Create(&models.AIConfig{
		Name:        "仿真测试复用模型",
		Provider:    enums.AIProviderOpenAI,
		BaseURL:     "https://example.invalid/v1",
		APIKey:      "test-only-key",
		ModelType:   enums.AIModelTypeLLM,
		ModelName:   "test-only-model",
		Status:      enums.StatusOk,
		Remark:      "仿真测试模型配置，不用于生产",
		AuditFields: models.AuditFields{CreatedAt: time.Now(), UpdatedAt: time.Now()},
	}).Error; err != nil {
		t.Fatalf("create reusable test AI config: %v", err)
	}

	batch := "fresh-lifecycle"
	if err := seed(db, batch, defaultPassword); err != nil {
		t.Fatalf("seed fresh database: %v", err)
	}
	first := buildReport(db, batch)
	assertCompleteSimulationReport(t, first)
	tenant := models.Tenant{}
	if err := db.Where("registration_type = ? AND registration_no = ?", tenantRegistrationType, tenantRegistrationNo).Take(&tenant).Error; err != nil {
		t.Fatalf("load simulation tenant: %v", err)
	}

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
		"invitations":    &models.TenantInvitation{},
		"companies":      &models.Company{},
		"agent teams":    &models.AgentTeam{},
		"agent profiles": &models.AgentProfile{},
		"stores":         &models.Store{},
		"customers":      &models.Customer{},
		"conversations":  &models.Conversation{},
		"route states":   &models.ConversationRouteState{},
		"participants":   &models.ConversationParticipant{},
		"messages":       &models.Message{},
		"assignments":    &models.ConversationAssignment{},
		"event logs":     &models.ConversationEventLog{},
	} {
		assertLifecycleRowCount(t, db, name, model, 0)
	}
	assertLifecycleScopedRowCount(t, db, "AI agents", &models.AIAgent{}, tenant.ID, 0)
	assertLifecycleScopedRowCount(t, db, "model grants", &models.TenantAIModelGrant{}, tenant.ID, 0)
	assertLifecycleScopedRowCount(t, db, "model assignments", &models.StoreAIModelSetting{}, tenant.ID, 0)
	if got := count(db, &models.Tenant{}, "registration_type = ? AND registration_no = ?", tenantRegistrationType, tenantRegistrationNo); got != 0 {
		t.Fatalf("simulation tenant count after cleanup=%d want=0", got)
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
	if got.Tenant != 1 || got.TenantSupervisor != 1 || got.TenantInvitation != 1 || got.DefaultAgentTeam != 1 ||
		got.AIAgent != 1 || !got.ModelConfigReused || got.TenantDefaultConfigName != "仿真测试复用模型" {
		t.Fatalf("tenant/model foundation baseline changed: %+v", got)
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

func assertLifecycleScopedRowCount(t *testing.T, db *gorm.DB, name string, model any, tenantID, want int64) {
	t.Helper()
	var count int64
	if err := db.Model(model).Where("tenant_id = ?", tenantID).Count(&count).Error; err != nil {
		t.Fatalf("count tenant-scoped %s: %v", name, err)
	}
	if count != want {
		t.Fatalf("tenant-scoped %s count=%d want=%d", name, count, want)
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
