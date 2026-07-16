package services

import (
	"path/filepath"
	"testing"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/openidentity"
	"agent-desk/internal/repositories"

	"github.com/glebarez/sqlite"
	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
)

type aiAgentTenantFixture struct {
	db     *gorm.DB
	adminA *dto.AuthPrincipal
	adminB *dto.AuthPrincipal
	agentA models.AIAgent
	agentB models.AIAgent
}

func TestAIAgentServiceExposesOnlyTenantRuntimeStrategies(t *testing.T) {
	fixture := setupAIAgentTenantFixture(t)
	if AIAgentService.GetInTenant(fixture.agentB.ID, fixture.adminA) != nil {
		t.Fatal("tenant A can read tenant B runtime strategy")
	}
	cnd := sqls.NewCnd().Asc("id")
	cnd.Paging = &sqls.Paging{Page: 1, Limit: 20}
	list, paging := AIAgentService.FindPageInTenant(cnd, fixture.adminA)
	if len(list) != 1 || list[0].ID != fixture.agentA.ID || paging.Total != 1 {
		t.Fatalf("tenant A list=%+v paging=%+v", list, paging)
	}
}

func TestAIAgentRuntimeReferencesRejectCrossTenantBindings(t *testing.T) {
	fixture := setupAIAgentTenantFixture(t)
	if _, err := ChannelService.CreateChannel(request.CreateChannelRequest{
		Name: "A cross-tenant channel", ChannelType: enums.ChannelTypeWeb,
		AIAgentID: fixture.agentB.ID, Status: int(enums.StatusOk),
	}, fixture.adminA); err == nil {
		t.Fatal("tenant A channel accepted tenant B runtime strategy")
	}
	channelA, err := ChannelService.CreateChannel(request.CreateChannelRequest{
		Name: "A channel", ChannelType: enums.ChannelTypeWeb,
		AIAgentID: fixture.agentA.ID, Status: int(enums.StatusOk),
	}, fixture.adminA)
	if err != nil {
		t.Fatalf("create tenant A channel: %v", err)
	}
	_, err = ConversationService.CreateWithoutWelcome(openidentity.ExternalUser{
		ExternalSource: enums.ExternalSourceGuest, ExternalID: "cross-agent", ExternalName: "cross-agent",
	}, channelA.ID, fixture.agentB.ID)
	if err == nil {
		t.Fatal("tenant A conversation accepted tenant B runtime strategy")
	}
	conversationA := &models.Conversation{
		TenantID: fixture.adminA.ActiveTenantID, AIAgentID: fixture.agentA.ID,
		ChannelID: channelA.ID, Status: enums.IMConversationStatusAIServing,
	}
	if err := fixture.db.Create(conversationA).Error; err != nil {
		t.Fatalf("create tenant A conversation fixture: %v", err)
	}
	if _, err := ConversationHumanDispatchService.HandoffByAI(conversationA.ID, fixture.agentB, "cross-tenant handoff"); err == nil {
		t.Fatal("tenant A conversation accepted tenant B runtime strategy for handoff")
	}
}

func TestAIAgentRepositoryFinalWritePredicateIncludesTenant(t *testing.T) {
	fixture := setupAIAgentTenantFixture(t)
	if err := repositories.AIAgentRepository.UpdatesInTenant(
		fixture.db, fixture.agentB.ID, fixture.adminA.ActiveTenantID, map[string]any{"name": "cross-tenant update"},
	); err != nil {
		t.Fatalf("scoped update: %v", err)
	}
	if err := repositories.AIAgentRepository.UpdateColumnInTenant(
		fixture.db, fixture.agentB.ID, fixture.adminA.ActiveTenantID, "sort_no", 99,
	); err != nil {
		t.Fatalf("scoped column update: %v", err)
	}
	current := repositories.AIAgentRepository.GetInTenant(fixture.db, fixture.agentB.ID, fixture.adminB.ActiveTenantID)
	if current == nil || current.Name != fixture.agentB.Name || current.SortNo != fixture.agentB.SortNo {
		t.Fatalf("tenant B runtime strategy changed through wrong tenant predicate: %+v", current)
	}
}

func setupAIAgentTenantFixture(t *testing.T) aiAgentTenantFixture {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "ai-agent-tenant-service.db")), &gorm.Config{
		NamingStrategy: schema.NamingStrategy{TablePrefix: "t_", SingularTable: true},
		Logger:         logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(
		&models.AIAgent{}, &models.Channel{}, &models.Customer{}, &models.CustomerIdentity{},
		&models.Conversation{}, &models.ConversationParticipant{}, &models.ConversationEventLog{}, &models.Message{},
	); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	sqls.SetDB(db)
	t.Cleanup(func() {
		sqls.SetDB(nil)
		if sqlDB, dbErr := db.DB(); dbErr == nil {
			_ = sqlDB.Close()
		}
	})
	fixture := aiAgentTenantFixture{
		db:     db,
		adminA: &dto.AuthPrincipal{UserID: 1001, TenantID: 101, ActiveTenantID: 101, Username: "tenant-a-admin"},
		adminB: &dto.AuthPrincipal{UserID: 2001, TenantID: 202, ActiveTenantID: 202, Username: "tenant-b-admin"},
		agentA: models.AIAgent{TenantID: 101, Name: "A default runtime strategy", Status: enums.StatusOk, ServiceMode: enums.IMConversationServiceModeAIFirst},
		agentB: models.AIAgent{TenantID: 202, Name: "B default runtime strategy", Status: enums.StatusOk, ServiceMode: enums.IMConversationServiceModeAIFirst},
	}
	for _, item := range []*models.AIAgent{&fixture.agentA, &fixture.agentB} {
		if err := db.Create(item).Error; err != nil {
			t.Fatalf("create runtime strategy: %v", err)
		}
	}
	return fixture
}
