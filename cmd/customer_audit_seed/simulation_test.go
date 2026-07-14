package main

import (
	"fmt"
	"testing"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
)

func TestBuildSimulationScenarios(t *testing.T) {
	now := time.Date(2026, time.July, 10, 12, 0, 0, 0, time.Local)
	scenarios := buildSimulationScenarios(now)
	if len(scenarios) != expectedSimulationConversationCount {
		t.Fatalf("scenario count = %d, want %d", len(scenarios), expectedSimulationConversationCount)
	}

	kindCounts := map[simulationKind]int{}
	teamCounts := map[int]int{}
	seenStores := map[int]bool{}
	messageCount := 0
	assignmentCount := 0
	needReplyCount := 0
	for _, scenario := range scenarios {
		kindCounts[scenario.Kind]++
		teamCounts[scenario.TeamIndex]++
		messageCount += len(scenario.Messages)
		if seenStores[scenario.StoreIndex] {
			t.Fatalf("store %d is used by more than one scenario", scenario.StoreIndex)
		}
		seenStores[scenario.StoreIndex] = true

		if simulationNeedsReply(scenario.Kind) {
			needReplyCount++
			if scenario.Messages[len(scenario.Messages)-1].SenderType != enums.IMSenderTypeCustomer {
				t.Fatalf("need-reply scenario %s must end with a customer message", scenario.Key)
			}
		}
		if scenario.AssignmentAt != nil {
			assignmentCount++
			if scenario.AssigneeIndex <= 0 {
				t.Fatalf("assigned scenario %s has no assignee", scenario.Key)
			}
		}
	}

	wantKinds := map[simulationKind]int{
		simulationKindAI:         6,
		simulationKindPending:    9,
		simulationKindAssigned:   6,
		simulationKindProcessing: 6,
		simulationKindPriority:   3,
		simulationKindUrgent:     3,
		simulationKindClosed:     3,
	}
	for kind, want := range wantKinds {
		if kindCounts[kind] != want {
			t.Fatalf("kind %s count = %d, want %d", kind, kindCounts[kind], want)
		}
	}
	for teamIndex := 1; teamIndex <= 3; teamIndex++ {
		if teamCounts[teamIndex] != 12 {
			t.Fatalf("team %d scenario count = %d, want 12", teamIndex, teamCounts[teamIndex])
		}
	}
	if messageCount != expectedSimulationMessageCount {
		t.Fatalf("message count = %d, want %d", messageCount, expectedSimulationMessageCount)
	}
	if assignmentCount != expectedSimulationAssignmentCount {
		t.Fatalf("assignment count = %d, want %d", assignmentCount, expectedSimulationAssignmentCount)
	}
	if needReplyCount != expectedSimulationNeedReplyCount {
		t.Fatalf("need reply count = %d, want %d", needReplyCount, expectedSimulationNeedReplyCount)
	}
}

func TestSimulationManualTasksRemainAvailableForDispatchTesting(t *testing.T) {
	now := time.Date(2026, time.July, 10, 12, 0, 0, 0, time.Local)
	for _, scenario := range buildSimulationScenarios(now) {
		if simulationNeedsReply(scenario.Kind) {
			if scenario.ManualExpireAt == nil || scenario.ManualExpireAt.Sub(now) < 12*time.Hour {
				t.Fatalf("manual scenario %s does not remain available for a test session", scenario.Key)
			}
		}
		if scenario.Kind == simulationKindClosed {
			if scenario.ClosedAt == nil || scenario.AssignmentAt == nil || !scenario.ClosedAt.After(*scenario.AssignmentAt) {
				t.Fatalf("closed scenario %s has invalid lifecycle", scenario.Key)
			}
		}
	}
}

func TestSimulationSenderIDMatchesRealMessageSemantics(t *testing.T) {
	agent := &models.User{}
	agent.ID = 42

	tests := []struct {
		name       string
		senderType enums.IMSenderType
		assignee   *models.User
		want       int64
		wantErr    bool
	}{
		{name: "customer", senderType: enums.IMSenderTypeCustomer, assignee: agent, want: 0},
		{name: "ai", senderType: enums.IMSenderTypeAI, assignee: agent, want: 0},
		{name: "assigned agent", senderType: enums.IMSenderTypeAgent, assignee: agent, want: 42},
		{name: "missing agent", senderType: enums.IMSenderTypeAgent, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := simulationSenderID(tt.senderType, tt.assignee)
			if (err != nil) != tt.wantErr {
				t.Fatalf("simulationSenderID() error = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("simulationSenderID() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestSeedResourceUpsertsInheritTenantID(t *testing.T) {
	db := openSeedTenantTestDB(t, "resource", &models.Store{}, &models.StoreStaffBinding{}, &models.WxWorkProtocolInstance{})
	now := time.Date(2026, time.July, 14, 12, 0, 0, 0, time.UTC)
	ctx := &seedContext{
		db:      db,
		tenant:  &models.Tenant{ID: 77},
		company: &models.Company{ID: 88},
		channel: &models.Channel{ID: 99},
		teams: []*models.AgentTeam{
			{ID: 101},
			{ID: 102},
			{ID: 103},
		},
		batch:  "tenant-scope",
		marker: marker("tenant-scope"),
		now:    now,
		audit:  simulationAuditFields(now),
	}
	if err := ctx.upsertStores(); err != nil {
		t.Fatalf("upsert stores: %v", err)
	}
	assertSeedTenantRows(t, db, ctx.tenant.ID, 100, &models.Store{})

	store := ctx.stores[0]
	staff := &models.User{ID: 201}
	binding, err := ctx.upsertStoreStaffBinding(1, store, staff)
	if err != nil {
		t.Fatalf("upsert store staff binding: %v", err)
	}
	instance, err := ctx.upsertWxWorkInstance(1, store, binding)
	if err != nil {
		t.Fatalf("upsert wxwork instance: %v", err)
	}
	assertSeedTenantRows(t, db, ctx.tenant.ID, 1, &models.StoreStaffBinding{})
	assertSeedTenantRows(t, db, ctx.tenant.ID, 1, &models.WxWorkProtocolInstance{})

	for _, item := range []any{store, binding, instance} {
		if err := db.Model(item).Update("tenant_id", 0).Error; err != nil {
			t.Fatalf("reset %T tenant: %v", item, err)
		}
	}
	if err := ctx.upsertStores(); err != nil {
		t.Fatalf("repair stores: %v", err)
	}
	store = ctx.stores[0]
	binding, err = ctx.upsertStoreStaffBinding(1, store, staff)
	if err != nil {
		t.Fatalf("repair store staff binding: %v", err)
	}
	if _, err = ctx.upsertWxWorkInstance(1, store, binding); err != nil {
		t.Fatalf("repair wxwork instance: %v", err)
	}
	assertSeedTenantRows(t, db, ctx.tenant.ID, 100, &models.Store{})
	assertSeedTenantRows(t, db, ctx.tenant.ID, 1, &models.StoreStaffBinding{})
	assertSeedTenantRows(t, db, ctx.tenant.ID, 1, &models.WxWorkProtocolInstance{})
}

func TestSimulationRecordsInheritTenantID(t *testing.T) {
	db := openSeedTenantTestDB(t, "simulation",
		&models.Conversation{},
		&models.ConversationRouteState{},
		&models.ConversationParticipant{},
		&models.Message{},
		&models.ConversationAssignment{},
		&models.ConversationEventLog{},
		&models.StoreCustomerRelation{},
	)
	now := time.Date(2026, time.July, 14, 12, 0, 0, 0, time.UTC)
	var scenario simulationScenario
	for _, candidate := range buildSimulationScenarios(now) {
		if candidate.AssignmentAt != nil {
			scenario = candidate
			break
		}
	}
	if scenario.AssignmentAt == nil {
		t.Fatal("expected an assigned simulation scenario")
	}

	ctx := &seedContext{
		db:      db,
		tenant:  &models.Tenant{ID: 77},
		channel: &models.Channel{ID: 88},
		batch:   "tenant-scope",
		marker:  marker("tenant-scope"),
	}
	ctx.customers = make([]*models.Customer, scenario.CustomerIndex)
	ctx.customers[scenario.CustomerIndex-1] = &models.Customer{ID: 101, TenantID: ctx.tenant.ID, Name: "Tenant Customer"}
	ctx.stores = make([]*models.Store, scenario.StoreIndex)
	ctx.stores[scenario.StoreIndex-1] = &models.Store{ID: 102, TenantID: ctx.tenant.ID}
	ctx.wxInstances = make([]*models.WxWorkProtocolInstance, scenario.StoreIndex)
	ctx.wxInstances[scenario.StoreIndex-1] = &models.WxWorkProtocolInstance{ID: 103, TenantID: ctx.tenant.ID}
	ctx.teams = make([]*models.AgentTeam, scenario.TeamIndex)
	ctx.teams[scenario.TeamIndex-1] = &models.AgentTeam{ID: 104, TenantID: ctx.tenant.ID}
	ctx.leaders = make([]*models.User, scenario.TeamIndex)
	ctx.leaders[scenario.TeamIndex-1] = &models.User{ID: 105, TenantID: ctx.tenant.ID}
	ctx.agents = make([]*models.User, scenario.AssigneeIndex)
	ctx.agents[scenario.AssigneeIndex-1] = &models.User{ID: 106, TenantID: ctx.tenant.ID}

	if err := ctx.createSimulationScenario(scenario); err != nil {
		t.Fatalf("create simulation scenario: %v", err)
	}
	for _, model := range []any{
		&models.Conversation{},
		&models.ConversationRouteState{},
		&models.ConversationParticipant{},
		&models.Message{},
		&models.ConversationAssignment{},
		&models.ConversationEventLog{},
	} {
		assertSeedTenantRows(t, db, ctx.tenant.ID, -1, model)
	}
}

func openSeedTenantTestDB(t *testing.T, name string, modelsToMigrate ...any) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:customer_audit_seed_%s?mode=memory&cache=shared", name)), &gorm.Config{
		NamingStrategy: schema.NamingStrategy{TablePrefix: "t_", SingularTable: true},
		Logger:         logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(modelsToMigrate...); err != nil {
		t.Fatalf("migrate seed models: %v", err)
	}
	return db
}

func assertSeedTenantRows(t *testing.T, db *gorm.DB, tenantID, expected int64, model any) {
	t.Helper()
	var total int64
	if err := db.Model(model).Count(&total).Error; err != nil {
		t.Fatalf("count %T rows: %v", model, err)
	}
	if expected >= 0 && total != expected {
		t.Fatalf("%T row count = %d, want %d", model, total, expected)
	}
	if total == 0 {
		t.Fatalf("expected %T rows", model)
	}
	var scoped int64
	if err := db.Model(model).Where("tenant_id = ?", tenantID).Count(&scoped).Error; err != nil {
		t.Fatalf("count %T tenant rows: %v", model, err)
	}
	if scoped != total {
		t.Fatalf("%T tenant rows = %d, total = %d", model, scoped, total)
	}
}
