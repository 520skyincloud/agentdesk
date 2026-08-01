package main

import (
	"encoding/base64"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"agent-desk/internal/bootstrap"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/config"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/services"

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
	createLifecycleModelProfile(t, db)

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
	simulationUser := models.User{}
	if err := db.Where("tenant_id = ?", tenant.ID).Order("id ASC").Take(&simulationUser).Error; err != nil {
		t.Fatalf("load simulation user for report view fixture: %v", err)
	}
	createLifecycleReportViewPreset(t, db, tenant.ID, simulationUser.ID, "simulation-view")
	otherTenant, otherUser := createLifecycleUnrelatedTenant(t, db)
	otherPreset := createLifecycleReportViewPreset(t, db, otherTenant.ID, otherUser.ID, "unrelated-view")
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
	assertLifecycleScopedRowCount(t, db, "model profile assignments", &models.StoreModelProfileAssignment{}, tenant.ID, 0)
	assertLifecycleScopedRowCount(t, db, "model credentials", &models.StoreModelCredential{}, tenant.ID, 0)
	assertLifecycleScopedRowCount(t, db, "credential policies", &models.StoreCredentialPolicy{}, tenant.ID, 0)
	assertLifecycleScopedRowCount(t, db, "credential audit logs", &models.StoreModelCredentialAuditLog{}, tenant.ID, 0)
	assertLifecycleScopedRowCount(t, db, "quality templates", &models.QualityTemplate{}, tenant.ID, 0)
	assertLifecycleScopedRowCount(t, db, "quality template items", &models.QualityTemplateItem{}, tenant.ID, 0)
	assertLifecycleScopedRowCount(t, db, "analytics policies", &models.ServiceAnalyticsPolicy{}, tenant.ID, 0)
	assertLifecycleScopedRowCount(t, db, "report view presets", &models.ReportViewPreset{}, tenant.ID, 0)
	if got := count(db, &models.ReportViewPreset{}, "id = ? AND tenant_id = ? AND user_id = ?", otherPreset.ID, otherTenant.ID, otherUser.ID); got != 1 {
		t.Fatalf("cleanup changed unrelated tenant report view preset count=%d want=1", got)
	}
	if got := count(db, &models.Tenant{}, "registration_type = ? AND registration_no = ?", tenantRegistrationType, tenantRegistrationNo); got != 0 {
		t.Fatalf("simulation tenant count after cleanup=%d want=0", got)
	}
	if err := db.Delete(&otherPreset).Error; err != nil {
		t.Fatalf("delete unrelated report view fixture: %v", err)
	}
	if err := db.Delete(&otherUser).Error; err != nil {
		t.Fatalf("delete unrelated user fixture: %v", err)
	}
	if err := db.Delete(&otherTenant).Error; err != nil {
		t.Fatalf("delete unrelated tenant fixture: %v", err)
	}

	assertLifecycleSystemData(t, db)
}

func createLifecycleModelProfile(t *testing.T, db *gorm.DB) {
	t.Helper()
	now := time.Now()
	profile := &models.ModelProfileTemplate{
		Code: "simulation", Name: "仿真测试九槽模型方案", Revision: 1,
		GatewayBaseURL: "https://example.invalid/v1", Status: enums.ModelProfileStatusCandidate,
		PublishedAt: &now, AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(profile).Error; err != nil {
		t.Fatalf("create simulation model profile: %v", err)
	}
	slots := make([]models.ModelProfileSlot, 0, len(services.RequiredModelUsageSlotSpecs()))
	for index, spec := range services.RequiredModelUsageSlotSpecs() {
		slot := models.ModelProfileSlot{
			TemplateID: profile.ID, UsageCode: spec.UsageCode, DisplayName: spec.DisplayName,
			ModelType: spec.ExpectedModelType, Provider: "newapi", ModelName: "test-" + string(spec.UsageCode),
			APIMode: spec.DefaultAPIMode, TimeoutMS: 30000, Enabled: true, SortNo: index + 1,
			AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now},
		}
		if slot.ModelType == enums.AIModelTypeLLM || slot.ModelType == enums.AIModelTypeVision {
			slot.MaxContextTokens = 8192
			slot.MaxOutputTokens = 1024
		}
		if slot.ModelType == enums.AIModelTypeEmbedding {
			slot.Dimension = 1536
		}
		if slot.UsageCode == enums.ModelUsageSlotCustomerTag {
			slot.SchemaVersion = "customer_tag_evolution.v1"
			slot.PromptTemplate = "仅从允许的固定行业标签中提取长期客户偏好。"
			slot.JSONSchema = `{"type":"object"}`
		}
		slots = append(slots, slot)
	}
	if err := db.Create(&slots).Error; err != nil {
		t.Fatalf("create simulation model slots: %v", err)
	}
}

func createLifecycleReportViewPreset(t *testing.T, db *gorm.DB, tenantID, userID int64, name string) models.ReportViewPreset {
	t.Helper()
	now := time.Now()
	item := models.ReportViewPreset{
		TenantID: tenantID,
		UserID:   userID,
		PageCode: "conversation-records",
		Name:     name,
		Status:   enums.StatusOk,
		AuditFields: models.AuditFields{
			CreatedAt: now,
			UpdatedAt: now,
		},
	}
	if err := db.Create(&item).Error; err != nil {
		t.Fatalf("create %s report view fixture: %v", name, err)
	}
	return item
}

func createLifecycleUnrelatedTenant(t *testing.T, db *gorm.DB) (models.Tenant, models.User) {
	t.Helper()
	now := time.Now()
	tenant := models.Tenant{
		TenantCode:       "T_LIFECYCLE_UNRELATED",
		LegalName:        "生命周期无关租户",
		ShortName:        "无关租户",
		RegistrationType: "test",
		RegistrationNo:   "LIFECYCLE-UNRELATED",
		Status:           enums.StatusOk,
		AuditFields: models.AuditFields{
			CreatedAt: now,
			UpdatedAt: now,
		},
	}
	if err := db.Create(&tenant).Error; err != nil {
		t.Fatalf("create unrelated tenant fixture: %v", err)
	}
	user := models.User{
		TenantID:       tenant.ID,
		Username:       "lifecycle_unrelated_user",
		Nickname:       "生命周期无关用户",
		ApprovalStatus: enums.UserApprovalStatusApproved,
		Status:         enums.StatusOk,
		AuditFields: models.AuditFields{
			CreatedAt: now,
			UpdatedAt: now,
		},
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("create unrelated user fixture: %v", err)
	}
	return tenant, user
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
		got.AIAgent != 1 || !got.ModelProfileReady || got.ModelProfileName != "仿真测试九槽模型方案" ||
		got.StoreModelAssignments != 100 || got.StoreModelCredentials != 100 || got.UnconfiguredCredentials != 100 || got.StoreCredentialPolicies != 100 ||
		got.RuleAgentTeams != 3 || got.AgentTeamSchedules != 3 {
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
	var migrations []models.Migration
	if err := db.Order("version ASC").Find(&migrations).Error; err != nil {
		t.Fatalf("load fresh initializers after cleanup: %v", err)
	}
	versions := make([]int64, 0, len(migrations))
	for i := range migrations {
		versions = append(versions, migrations[i].Version)
		if !migrations[i].Success {
			t.Fatalf("fresh initializer changed to failed after seed lifecycle: %+v", migrations[i])
		}
	}
	if want := []int64{2, 15, 35, 68, 69, 70, 71, 72}; !reflect.DeepEqual(versions, want) {
		t.Fatalf("fresh initializer versions after seed lifecycle=%v want=%v", versions, want)
	}
}
