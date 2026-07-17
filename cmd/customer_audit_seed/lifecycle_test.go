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
	firstUsage := createLifecycleUsageEvidence(t, db, batch, "repeat")

	if err := seed(db, batch, defaultPassword); err != nil {
		t.Fatalf("repeat seed: %v", err)
	}
	assertLifecycleUsageEvidence(t, db, firstUsage)
	second := buildReport(db, batch)
	assertCompleteSimulationReport(t, second)
	if second != first {
		t.Fatalf("repeated seed changed report:\nfirst=%+v\nsecond=%+v", first, second)
	}
	secondUsage := createLifecycleUsageEvidence(t, db, batch, "cleanup")

	if err := cleanup(db, batch); err != nil {
		t.Fatalf("cleanup seed data: %v", err)
	}
	assertLifecycleUsageEvidence(t, db, secondUsage)
	empty := buildReport(db, batch)
	wantEmpty := report{Batch: batch, Marker: marker(batch)}
	if empty != wantEmpty {
		t.Fatalf("cleanup left report data:\ngot=%+v\nwant=%+v", empty, wantEmpty)
	}

	for name, model := range map[string]any{
		"invitations":              &models.TenantInvitation{},
		"companies":                &models.Company{},
		"agent teams":              &models.AgentTeam{},
		"team schedules":           &models.AgentTeamSchedule{},
		"agent profiles":           &models.AgentProfile{},
		"stores":                   &models.Store{},
		"customers":                &models.Customer{},
		"conversations":            &models.Conversation{},
		"route states":             &models.ConversationRouteState{},
		"participants":             &models.ConversationParticipant{},
		"messages":                 &models.Message{},
		"assignments":              &models.ConversationAssignment{},
		"event logs":               &models.ConversationEventLog{},
		"service sessions":         &models.ConversationServiceSession{},
		"response spans":           &models.ConversationResponseSpan{},
		"presence sessions":        &models.AgentPresenceSession{},
		"quality inspections":      &models.QualityInspection{},
		"quality inspection items": &models.QualityInspectionItem{},
		"evaluations":              &models.ConversationEvaluation{},
		"dispatch decisions":       &models.DispatchDecisionLog{},
	} {
		assertLifecycleRowCount(t, db, name, model, 0)
	}
	assertLifecycleScopedRowCount(t, db, "AI agents", &models.AIAgent{}, tenant.ID, 0)
	assertLifecycleScopedRowCount(t, db, "model grants", &models.TenantAIModelGrant{}, tenant.ID, 0)
	assertLifecycleScopedRowCount(t, db, "model assignments", &models.StoreAIModelSetting{}, tenant.ID, 0)
	assertLifecycleScopedRowCount(t, db, "quality templates", &models.QualityTemplate{}, tenant.ID, 0)
	assertLifecycleScopedRowCount(t, db, "quality template items", &models.QualityTemplateItem{}, tenant.ID, 0)
	assertLifecycleScopedRowCount(t, db, "analytics policies", &models.ServiceAnalyticsPolicy{}, tenant.ID, 0)
	if got := count(db, &models.Tenant{}, "registration_type = ? AND registration_no = ?", tenantRegistrationType, tenantRegistrationNo); got != 0 {
		t.Fatalf("simulation tenant count after cleanup=%d want=0", got)
	}

	assertLifecycleSystemData(t, db)
}

type lifecycleUsageEvidence struct {
	simulationEventKey string
	simulationCallKey  string
	orphanEventKey     string
	orphanCallKey      string
	platformEventKey   string
	platformCallKey    string
}

func createLifecycleUsageEvidence(t *testing.T, db *gorm.DB, batch, suffix string) lifecycleUsageEvidence {
	t.Helper()
	routeState := models.ConversationRouteState{}
	if err := db.Where("remark LIKE ?", likeMarker(marker(batch))).Order("id ASC").Take(&routeState).Error; err != nil {
		t.Fatalf("load simulation route state for usage fixture: %v", err)
	}
	message := models.Message{}
	if err := db.Where("conversation_id = ?", routeState.ConversationID).Order("id ASC").Take(&message).Error; err != nil {
		t.Fatalf("load simulation message for usage fixture: %v", err)
	}
	now := time.Now()
	evidence := lifecycleUsageEvidence{
		simulationEventKey: "simulation-usage-event-" + suffix,
		simulationCallKey:  "simulation-usage-call-" + suffix,
		orphanEventKey:     "simulation-orphan-event-" + suffix,
		orphanCallKey:      "simulation-orphan-call-" + suffix,
		platformEventKey:   "platform-usage-event-" + suffix,
		platformCallKey:    "platform-usage-call-" + suffix,
	}
	if err := db.Create(&models.AIUsageEvent{
		TenantID: routeState.TenantID, EventKey: evidence.simulationEventKey,
		StoreID: routeState.StoreID, WxWorkInstanceID: routeState.WxWorkInstanceID,
		ConversationID: routeState.ConversationID, MessageID: message.ID,
		Stage: "simulation_lifecycle_fixture", CreatedAt: now,
	}).Error; err != nil {
		t.Fatalf("create simulation usage event fixture: %v", err)
	}
	if err := db.Create(&models.AIUsageGatewayCall{
		TenantID: routeState.TenantID, CallKey: evidence.simulationCallKey, EventKey: evidence.simulationEventKey,
		StoreID: routeState.StoreID, WxWorkInstanceID: routeState.WxWorkInstanceID,
		ConversationID: routeState.ConversationID, MessageID: message.ID,
		Stage: "simulation_lifecycle_fixture", StartedAt: now, FinishedAt: now, CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatalf("create simulation gateway call fixture: %v", err)
	}
	if err := db.Create(&models.AIUsageEvent{
		TenantID: routeState.TenantID, EventKey: evidence.orphanEventKey,
		ConversationID: 999999001, MessageID: 999999002,
		Stage: "simulation_orphan_fixture", CreatedAt: now,
	}).Error; err != nil {
		t.Fatalf("create simulation orphan usage event fixture: %v", err)
	}
	if err := db.Create(&models.AIUsageGatewayCall{
		TenantID: routeState.TenantID, CallKey: evidence.orphanCallKey, EventKey: evidence.orphanEventKey,
		ConversationID: 999999001, MessageID: 999999002,
		Stage: "simulation_orphan_fixture", StartedAt: now, FinishedAt: now, CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatalf("create simulation orphan gateway call fixture: %v", err)
	}
	if err := db.Create(&models.AIUsageEvent{
		EventKey: evidence.platformEventKey, Stage: "platform_lifecycle_fixture", CreatedAt: now,
	}).Error; err != nil {
		t.Fatalf("create platform usage event fixture: %v", err)
	}
	if err := db.Create(&models.AIUsageGatewayCall{
		CallKey: evidence.platformCallKey, EventKey: evidence.platformEventKey,
		Stage: "platform_lifecycle_fixture", StartedAt: now, FinishedAt: now, CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatalf("create platform gateway call fixture: %v", err)
	}
	return evidence
}

func assertLifecycleUsageEvidence(t *testing.T, db *gorm.DB, evidence lifecycleUsageEvidence) {
	t.Helper()
	if got := count(db, &models.AIUsageEvent{}, "event_key = ?", evidence.simulationEventKey); got != 0 {
		t.Fatalf("simulation usage event count=%d want=0", got)
	}
	if got := count(db, &models.AIUsageGatewayCall{}, "call_key = ?", evidence.simulationCallKey); got != 0 {
		t.Fatalf("simulation gateway call count=%d want=0", got)
	}
	if got := count(db, &models.AIUsageEvent{}, "event_key = ?", evidence.orphanEventKey); got != 0 {
		t.Fatalf("simulation orphan usage event count=%d want=0", got)
	}
	if got := count(db, &models.AIUsageGatewayCall{}, "call_key = ?", evidence.orphanCallKey); got != 0 {
		t.Fatalf("simulation orphan gateway call count=%d want=0", got)
	}
	if got := count(db, &models.AIUsageEvent{}, "event_key = ?", evidence.platformEventKey); got != 1 {
		t.Fatalf("platform usage event count=%d want=1", got)
	}
	if got := count(db, &models.AIUsageGatewayCall{}, "call_key = ?", evidence.platformCallKey); got != 1 {
		t.Fatalf("platform gateway call count=%d want=1", got)
	}
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
		got.AIAgent != 1 || !got.ModelConfigReused || got.TenantDefaultConfigName != "仿真测试复用模型" ||
		got.IntelligentAgentTeams != 3 || got.AgentTeamSchedules != 3 || !got.DispatchModelAssigned {
		t.Fatalf("tenant/model foundation baseline changed: %+v", got)
	}
	if got.SimulatedConversations != 36 || got.SimulatedMessages != 135 || got.SimulatedAssignments != 21 ||
		got.SimulatedCurrentlyAssigned != 18 || got.SimulatedAssignedAgents != 12 || got.SimulatedNeedReply != 27 {
		t.Fatalf("dispatch simulation baseline changed: %+v", got)
	}
	if got.ServiceSessions != expectedSimulationServiceSessionCount || got.ResponseSpans != expectedSimulationResponseSpanCount ||
		got.WaitingResponseSpans != expectedSimulationWaitingResponseSpanCount || got.RepliedResponseSpans != expectedSimulationRepliedResponseSpanCount ||
		got.PresenceSessions != expectedSimulationPresenceCount || got.QualityInspections != expectedSimulationQualityInspectionCount ||
		got.CompletedInspections != expectedSimulationCompletedInspectionCount || got.QualityInspectionItems != expectedSimulationQualityItemCount ||
		got.Evaluations != expectedSimulationEvaluationCount || got.SubmittedEvaluations != expectedSimulationSubmittedEvaluationCount ||
		got.DispatchDecisionLogs != expectedSimulationDispatchDecisionCount || got.AnalyticsPolicies != 1 {
		t.Fatalf("analytics simulation baseline changed: %+v", got)
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
