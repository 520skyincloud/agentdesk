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
	db         *gorm.DB
	adminA     *dto.AuthPrincipal
	adminB     *dto.AuthPrincipal
	aiConfig   models.AIConfig
	teamA      models.AgentTeam
	teamB      models.AgentTeam
	knowledgeA models.KnowledgeBase
	knowledgeB models.KnowledgeBase
}

func TestAIAgentServiceIsolatesCRUDAndAllowsTenantLocalNames(t *testing.T) {
	fixture := setupAIAgentTenantFixture(t)
	agentA, err := AIAgentService.CreateAIAgent(fixture.request("同名 AI 客服", fixture.teamA.ID, fixture.knowledgeA.ID), fixture.adminA)
	if err != nil {
		t.Fatalf("create tenant A AI Agent: %v", err)
	}
	agentB, err := AIAgentService.CreateAIAgent(fixture.request("同名 AI 客服", fixture.teamB.ID, fixture.knowledgeB.ID), fixture.adminB)
	if err != nil {
		t.Fatalf("create tenant B AI Agent with same name: %v", err)
	}
	if agentA.TenantID != fixture.adminA.ActiveTenantID || agentB.TenantID != fixture.adminB.ActiveTenantID {
		t.Fatalf("unexpected AI Agent tenants: A=%d B=%d", agentA.TenantID, agentB.TenantID)
	}
	if AIAgentService.GetInTenant(agentB.ID, fixture.adminA) != nil {
		t.Fatal("tenant A can read tenant B AI Agent")
	}
	cnd := sqls.NewCnd().Asc("id")
	cnd.Paging = &sqls.Paging{Page: 1, Limit: 20}
	list, paging := AIAgentService.FindPageInTenant(cnd, fixture.adminA)
	if len(list) != 1 || list[0].ID != agentA.ID || paging.Total != 1 {
		t.Fatalf("tenant A list=%+v paging=%+v", list, paging)
	}

	update := request.UpdateAIAgentRequest{ID: agentB.ID, CreateAIAgentRequest: fixture.request("越权更新", fixture.teamA.ID, fixture.knowledgeA.ID)}
	if err := AIAgentService.UpdateAIAgent(update, fixture.adminA); err == nil {
		t.Fatal("tenant A updated tenant B AI Agent")
	}
	if err := AIAgentService.UpdateStatus(agentB.ID, int(enums.StatusDisabled), fixture.adminA); err == nil {
		t.Fatal("tenant A changed tenant B AI Agent status")
	}
	if err := AIAgentService.UpdateSort([]int64{agentB.ID}, fixture.adminA); err == nil {
		t.Fatal("tenant A sorted tenant B AI Agent")
	}
	if err := AIAgentService.DeleteAIAgent(agentB.ID, fixture.adminA); err == nil {
		t.Fatal("tenant A deleted tenant B AI Agent")
	}
	currentB := repositories.AIAgentRepository.GetInTenant(fixture.db, agentB.ID, fixture.adminB.ActiveTenantID)
	if currentB == nil || currentB.Name != agentB.Name || currentB.Status != enums.StatusOk {
		t.Fatalf("tenant B AI Agent changed after rejected operations: %+v", currentB)
	}
}

func TestAIAgentServiceRejectsCrossTenantReferencesAndBindings(t *testing.T) {
	fixture := setupAIAgentTenantFixture(t)
	agentA, err := AIAgentService.CreateAIAgent(fixture.request("A AI 客服", fixture.teamA.ID, fixture.knowledgeA.ID), fixture.adminA)
	if err != nil {
		t.Fatalf("create tenant A AI Agent: %v", err)
	}
	agentB, err := AIAgentService.CreateAIAgent(fixture.request("B AI 客服", fixture.teamB.ID, fixture.knowledgeB.ID), fixture.adminB)
	if err != nil {
		t.Fatalf("create tenant B AI Agent: %v", err)
	}
	if _, err := AIAgentService.CreateAIAgent(fixture.request("跨租户客服组", fixture.teamB.ID, fixture.knowledgeA.ID), fixture.adminA); err == nil {
		t.Fatal("tenant A AI Agent accepted tenant B support team")
	}
	if _, err := AIAgentService.CreateAIAgent(fixture.request("跨租户知识库", fixture.teamA.ID, fixture.knowledgeB.ID), fixture.adminA); err == nil {
		t.Fatal("tenant A AI Agent accepted tenant B knowledge base")
	}
	if _, err := ChannelService.CreateChannel(request.CreateChannelRequest{
		Name: "A 跨租户渠道", ChannelType: enums.ChannelTypeWeb, AIAgentID: agentB.ID, Status: int(enums.StatusOk),
	}, fixture.adminA); err == nil {
		t.Fatal("tenant A channel accepted tenant B AI Agent")
	}
	channelA, err := ChannelService.CreateChannel(request.CreateChannelRequest{
		Name: "A 正常渠道", ChannelType: enums.ChannelTypeWeb, AIAgentID: agentA.ID, Status: int(enums.StatusOk),
	}, fixture.adminA)
	if err != nil {
		t.Fatalf("create tenant A channel: %v", err)
	}
	_, err = ConversationService.CreateWithoutWelcome(openidentity.ExternalUser{
		ExternalSource: enums.ExternalSourceGuest, ExternalID: "cross-agent", ExternalName: "cross-agent",
	}, channelA.ID, agentB.ID)
	if err == nil {
		t.Fatal("tenant A conversation accepted tenant B AI Agent")
	}
	conversationA := &models.Conversation{TenantID: fixture.adminA.ActiveTenantID, AIAgentID: agentA.ID, ChannelID: channelA.ID, Status: enums.IMConversationStatusAIServing}
	if err := fixture.db.Create(conversationA).Error; err != nil {
		t.Fatalf("create tenant A conversation fixture: %v", err)
	}
	if _, err := ConversationHumanDispatchService.HandoffByAI(conversationA.ID, *agentB, "跨租户转人工"); err == nil {
		t.Fatal("tenant A conversation accepted tenant B AI Agent for handoff")
	}
}

func TestAIAgentRepositoryFinalWritePredicateIncludesTenant(t *testing.T) {
	fixture := setupAIAgentTenantFixture(t)
	agentB, err := AIAgentService.CreateAIAgent(fixture.request("B 受保护 AI 客服", fixture.teamB.ID, fixture.knowledgeB.ID), fixture.adminB)
	if err != nil {
		t.Fatalf("create tenant B AI Agent: %v", err)
	}
	if err := repositories.AIAgentRepository.UpdatesInTenant(fixture.db, agentB.ID, fixture.adminA.ActiveTenantID, map[string]any{"name": "越权更新"}); err != nil {
		t.Fatalf("scoped update: %v", err)
	}
	if err := repositories.AIAgentRepository.UpdateColumnInTenant(fixture.db, agentB.ID, fixture.adminA.ActiveTenantID, "sort_no", 99); err != nil {
		t.Fatalf("scoped column update: %v", err)
	}
	current := repositories.AIAgentRepository.GetInTenant(fixture.db, agentB.ID, fixture.adminB.ActiveTenantID)
	if current == nil || current.Name != agentB.Name || current.SortNo != agentB.SortNo {
		t.Fatalf("tenant B AI Agent changed through wrong tenant predicate: %+v", current)
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
		&models.AIConfig{}, &models.AgentTeam{}, &models.KnowledgeBase{}, &models.AIAgent{}, &models.Channel{},
		&models.Customer{}, &models.CustomerIdentity{}, &models.Conversation{}, &models.ConversationParticipant{}, &models.ConversationEventLog{}, &models.Message{},
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
		db:         db,
		adminA:     &dto.AuthPrincipal{UserID: 1001, TenantID: 101, ActiveTenantID: 101, Username: "tenant-a-admin"},
		adminB:     &dto.AuthPrincipal{UserID: 2001, TenantID: 202, ActiveTenantID: 202, Username: "tenant-b-admin"},
		aiConfig:   models.AIConfig{Name: "平台模型", Provider: enums.AIProviderOpenAI, ModelType: enums.AIModelTypeLLM, ModelName: "test-model", Status: enums.StatusOk},
		teamA:      models.AgentTeam{TenantID: 101, Name: "A 客服组", Status: enums.StatusOk},
		teamB:      models.AgentTeam{TenantID: 202, Name: "B 客服组", Status: enums.StatusOk},
		knowledgeA: models.KnowledgeBase{TenantID: 101, Name: "A 知识库", Status: enums.StatusOk},
		knowledgeB: models.KnowledgeBase{TenantID: 202, Name: "B 知识库", Status: enums.StatusOk},
	}
	for _, item := range []any{&fixture.aiConfig, &fixture.teamA, &fixture.teamB, &fixture.knowledgeA, &fixture.knowledgeB} {
		if err := db.Create(item).Error; err != nil {
			t.Fatalf("create fixture %T: %v", item, err)
		}
	}
	return fixture
}

func (f aiAgentTenantFixture) request(name string, teamID, knowledgeID int64) request.CreateAIAgentRequest {
	return request.CreateAIAgentRequest{
		Name: name, AIConfigID: f.aiConfig.ID, ServiceMode: enums.IMConversationServiceModeAIFirst,
		ReplyTimeoutSeconds: 180, TeamIDs: []int64{teamID}, HandoffMode: enums.AIAgentHandoffModeWaitPool,
		FallbackMode: enums.AIAgentFallbackModeNoAnswer, KnowledgeIDs: []int64{knowledgeID},
	}
}
