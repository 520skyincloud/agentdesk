package services

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
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

type conversationRuntimeTenantFixture struct {
	db       *gorm.DB
	adminA   *dto.AuthPrincipal
	adminB   *dto.AuthPrincipal
	aiAgentA models.AIAgent
	aiAgentB models.AIAgent
	channelA models.Channel
	channelB models.Channel
	teamA    models.AgentTeam
	teamB    models.AgentTeam
	userA    models.User
	userB    models.User
	profileA models.AgentProfile
	profileB models.AgentProfile
}

func TestConversationRuntimeChildrenInheritTenant(t *testing.T) {
	fixture := setupConversationRuntimeTenantFixture(t)
	previousHook := TriggerAIReplyAsyncHook
	TriggerAIReplyAsyncHook = nil
	t.Cleanup(func() { TriggerAIReplyAsyncHook = previousHook })

	external := openidentity.ExternalUser{ExternalSource: enums.ExternalSourceGuest, ExternalID: "runtime-tenant-a", ExternalName: "A租户访客"}
	conversation, err := ConversationService.CreateWithoutWelcome(external, fixture.channelA.ID, fixture.aiAgentA.ID)
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	message, err := MessageService.SendCustomerMessageWithRequestID(conversation.ID, "tenant-a-message", enums.IMMessageTypeText, "需要人工协助", "", external, "tenant-a-request")
	if err != nil {
		t.Fatalf("send customer message: %v", err)
	}
	if conversation.TenantID != fixture.adminA.ActiveTenantID || message.TenantID != conversation.TenantID {
		t.Fatalf("conversation/message tenant mismatch: conversation=%d message=%d", conversation.TenantID, message.TenantID)
	}

	assertConversationChildrenTenant(t, fixture.db, conversation.ID, conversation.TenantID)

	if err := ConversationInterruptService.SaveCheckpoint("tenant-a-checkpoint", []byte("checkpoint")); err != nil {
		t.Fatalf("save detached checkpoint: %v", err)
	}
	detachedInterrupt := ConversationInterruptService.GetByCheckPointID("tenant-a-checkpoint")
	if detachedInterrupt == nil || detachedInterrupt.TenantID != 0 || detachedInterrupt.ConversationID != 0 {
		t.Fatalf("detached checkpoint should remain quarantined: %+v", detachedInterrupt)
	}
	if err := ConversationInterruptService.CreateOrUpdatePending(&models.ConversationInterrupt{
		ConversationID:  conversation.ID,
		AIAgentID:       fixture.aiAgentA.ID,
		SourceMessageID: message.ID,
		CheckPointID:    "tenant-a-checkpoint",
		InterruptID:     "confirm-handoff",
		InterruptType:   "approval",
		Status:          "pending",
	}); err != nil {
		t.Fatalf("attach pending interrupt: %v", err)
	}
	interrupt := ConversationInterruptService.GetByCheckPointID("tenant-a-checkpoint")
	if interrupt == nil || interrupt.TenantID != conversation.TenantID || interrupt.ConversationID != conversation.ID {
		t.Fatalf("pending interrupt did not inherit conversation tenant: %+v", interrupt)
	}

	if err := MessageSyncLogService.Create(0, 0, enums.MessageSyncDirectionWecomToAgentDesk, "wxwork_protocol", "agentdesk", "detached-runtime-log", enums.MessageSyncStatusSkipped, "{}", "pre-conversation"); err != nil {
		t.Fatalf("create detached sync log: %v", err)
	}
	detachedLog := repositories.MessageSyncLogRepository.Take(fixture.db, "external_msg_id = ?", "detached-runtime-log")
	if detachedLog == nil || detachedLog.TenantID != 0 || detachedLog.ConversationID != 0 || detachedLog.MessageID != 0 {
		t.Fatalf("detached sync log should remain quarantined: %+v", detachedLog)
	}
}

func TestConversationRuntimeRejectsCrossTenantOperations(t *testing.T) {
	fixture := setupConversationRuntimeTenantFixture(t)
	previousHook := TriggerAIReplyAsyncHook
	TriggerAIReplyAsyncHook = nil
	t.Cleanup(func() { TriggerAIReplyAsyncHook = previousHook })

	externalB := openidentity.ExternalUser{ExternalSource: enums.ExternalSourceGuest, ExternalID: "runtime-tenant-b", ExternalName: "B租户访客"}
	conversationB, err := ConversationService.CreateWithoutWelcome(externalB, fixture.channelB.ID, fixture.aiAgentB.ID)
	if err != nil {
		t.Fatalf("create tenant B conversation: %v", err)
	}
	messageB, err := MessageService.SendCustomerMessageWithRequestID(conversationB.ID, "tenant-b-message", enums.IMMessageTypeText, "B租户消息", "", externalB, "tenant-b-request")
	if err != nil {
		t.Fatalf("send tenant B message: %v", err)
	}
	if err := repositories.ConversationRepository.UpdatesInTenant(fixture.db, conversationB.ID, fixture.adminB.ActiveTenantID, map[string]any{
		"status":              enums.IMConversationStatusPending,
		"current_team_id":     fixture.teamB.ID,
		"current_assignee_id": int64(0),
	}); err != nil {
		t.Fatalf("prepare tenant B pending conversation: %v", err)
	}

	if AgentTeamScopeService.CanViewConversation(fixture.adminA, conversationB.ID) {
		t.Fatal("tenant A must not view tenant B conversation")
	}
	if err := ConversationService.MarkAgentConversationReadToMessage(conversationB.ID, messageB.ID, fixture.adminA); err == nil {
		t.Fatal("tenant A must not mark tenant B conversation as read")
	}
	if _, err := MessageService.SendAgentMessage(conversationB.ID, fixture.userA.ID, "cross-tenant-send", enums.IMMessageTypeText, "越权回复", "", fixture.adminA); err == nil {
		t.Fatal("tenant A must not send to tenant B conversation")
	}
	if err := ConversationService.AssignConversation(request.AssignConversationRequest{ConversationID: conversationB.ID, AssigneeID: fixture.userA.ID, Reason: "越权派发"}, fixture.adminA); err == nil {
		t.Fatal("tenant A must not assign tenant B conversation")
	}
	if err := ConversationDispatchWorkbenchService.Assign(request.ConversationDispatchActionRequest{ConversationID: conversationB.ID, AssigneeID: fixture.userA.ID, Reason: "越权派单"}, fixture.adminA); err == nil {
		t.Fatal("tenant A workbench must not assign tenant B conversation")
	}

	if err := repositories.ConversationRepository.UpdatesInTenant(fixture.db, conversationB.ID, fixture.adminB.ActiveTenantID, map[string]any{
		"status":              enums.IMConversationStatusActive,
		"current_team_id":     fixture.teamB.ID,
		"current_assignee_id": fixture.userB.ID,
	}); err != nil {
		t.Fatalf("prepare tenant B active conversation: %v", err)
	}
	if err := ConversationService.TransferConversation(conversationB.ID, fixture.userA.ID, "越权转接", fixture.adminA); err == nil {
		t.Fatal("tenant A must not transfer tenant B conversation")
	}
	if err := ConversationDispatchWorkbenchService.Transfer(request.ConversationDispatchActionRequest{ConversationID: conversationB.ID, AssigneeID: fixture.userA.ID, Reason: "越权转派"}, fixture.adminA); err == nil {
		t.Fatal("tenant A workbench must not transfer tenant B conversation")
	}
	if err := ConversationDispatchWorkbenchService.Release(request.ConversationDispatchActionRequest{ConversationID: conversationB.ID, Reason: "越权释放"}, fixture.adminA); err == nil {
		t.Fatal("tenant A workbench must not release tenant B conversation")
	}
	if err := ConversationService.CloseConversation(conversationB.ID, "越权关闭", fixture.adminA); err == nil {
		t.Fatal("tenant A must not close tenant B conversation")
	}

	current := repositories.ConversationRepository.GetInTenant(fixture.db, conversationB.ID, fixture.adminB.ActiveTenantID)
	if current == nil || current.Status != enums.IMConversationStatusActive || current.CurrentAssigneeID != fixture.userB.ID || current.CurrentTeamID != fixture.teamB.ID {
		t.Fatalf("tenant B conversation changed after rejected operations: %+v", current)
	}
	if count := repositories.MessageRepository.Count(fixture.db, sqls.NewCnd().Eq("tenant_id", fixture.adminA.ActiveTenantID).Eq("conversation_id", conversationB.ID)); count != 0 {
		t.Fatalf("cross-tenant message was created: count=%d", count)
	}
	if count := repositories.ConversationAssignmentRepository.Count(fixture.db, sqls.NewCnd().Eq("tenant_id", fixture.adminA.ActiveTenantID).Eq("conversation_id", conversationB.ID)); count != 0 {
		t.Fatalf("cross-tenant assignment was created: count=%d", count)
	}
}

func TestConversationDispatchAndFinalWritesStayInTenant(t *testing.T) {
	fixture := setupConversationRuntimeTenantFixture(t)
	now := time.Now()
	candidates, report, err := ConversationDispatchService.pickDispatchCandidates([]int64{fixture.teamA.ID, fixture.teamB.ID}, fixture.adminA.ActiveTenantID, nil, now)
	if err != nil {
		t.Fatalf("pick tenant A candidates: %v", err)
	}
	if len(candidates) != 1 || candidates[0].profile.ID != fixture.profileA.ID || candidates[0].profile.TenantID != fixture.adminA.ActiveTenantID || report.Reason != "ok" {
		t.Fatalf("unexpected tenant A candidates=%+v report=%+v", candidates, report)
	}

	conversationB := &models.Conversation{TenantID: fixture.adminB.ActiveTenantID, ChannelID: fixture.channelB.ID, CustomerName: "B受保护会话", Status: enums.IMConversationStatusPending, CurrentTeamID: fixture.teamB.ID, LastActiveAt: now, LastMessageAt: now}
	if err := fixture.db.Create(conversationB).Error; err != nil {
		t.Fatalf("create protected conversation: %v", err)
	}
	routeB, err := ConversationRouteService.Ensure(conversationB.ID)
	if err != nil {
		t.Fatalf("ensure protected route: %v", err)
	}
	messageB := &models.Message{TenantID: fixture.adminB.ActiveTenantID, ConversationID: conversationB.ID, ClientMsgID: "protected-message", SenderType: enums.IMSenderTypeCustomer, MessageType: enums.IMMessageTypeText, Content: "原始内容", SeqNo: 1, SendStatus: enums.IMMessageStatusSent, SentAt: &now}
	if err := fixture.db.Create(messageB).Error; err != nil {
		t.Fatalf("create protected message: %v", err)
	}

	if err := repositories.ConversationRepository.UpdatesInTenant(fixture.db, conversationB.ID, fixture.adminA.ActiveTenantID, map[string]any{"customer_name": "越权会话"}); err != nil {
		t.Fatalf("scoped conversation update: %v", err)
	}
	if err := repositories.ConversationRouteStateRepository.UpdatesInTenant(fixture.db, routeB.ID, fixture.adminA.ActiveTenantID, map[string]any{"route_target": "cross-tenant"}); err != nil {
		t.Fatalf("scoped route update: %v", err)
	}
	if err := repositories.MessageRepository.UpdatesInTenant(fixture.db, messageB.ID, fixture.adminA.ActiveTenantID, map[string]any{"content": "越权消息"}); err != nil {
		t.Fatalf("scoped message update: %v", err)
	}

	currentConversation := repositories.ConversationRepository.GetInTenant(fixture.db, conversationB.ID, fixture.adminB.ActiveTenantID)
	currentRoute := repositories.ConversationRouteStateRepository.GetInTenant(fixture.db, routeB.ID, fixture.adminB.ActiveTenantID)
	currentMessage := repositories.MessageRepository.GetInTenant(fixture.db, messageB.ID, fixture.adminB.ActiveTenantID)
	if currentConversation == nil || currentConversation.CustomerName != conversationB.CustomerName {
		t.Fatalf("tenant B conversation changed by tenant A final write: %+v", currentConversation)
	}
	if currentRoute == nil || currentRoute.RouteTarget != "ai" {
		t.Fatalf("tenant B route changed by tenant A final write: %+v", currentRoute)
	}
	if currentMessage == nil || currentMessage.Content != messageB.Content {
		t.Fatalf("tenant B message changed by tenant A final write: %+v", currentMessage)
	}
}

func setupConversationRuntimeTenantFixture(t *testing.T) conversationRuntimeTenantFixture {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "conversation-runtime-tenant.db")), &gorm.Config{
		NamingStrategy: schema.NamingStrategy{TablePrefix: "t_", SingularTable: true},
		Logger:         logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(
		&models.User{}, &models.Customer{}, &models.CustomerIdentity{}, &models.Channel{}, &models.AIAgent{},
		&models.AgentTeam{}, &models.AgentTeamSchedule{}, &models.AgentProfile{}, &models.AgentTeamSquad{}, &models.AgentTeamSquadMember{},
		&models.Conversation{}, &models.ConversationParticipant{}, &models.ConversationRouteState{}, &models.ConversationReadState{},
		&models.Message{}, &models.ConversationAssignment{}, &models.ConversationEventLog{}, &models.ConversationInterrupt{}, &models.MessageSyncLog{},
		&models.ConversationServiceSession{}, &models.ConversationResponseSpan{}, &models.DispatchDecisionLog{},
	); err != nil {
		t.Fatalf("migrate conversation runtime tenant tables: %v", err)
	}
	sqls.SetDB(db)
	t.Cleanup(func() {
		sqls.SetDB(nil)
		if sqlDB, dbErr := db.DB(); dbErr == nil {
			_ = sqlDB.Close()
		}
	})

	fixture := conversationRuntimeTenantFixture{
		db:       db,
		adminA:   &dto.AuthPrincipal{UserID: 1001, TenantID: 101, ActiveTenantID: 101, Username: "tenant-a-admin", Roles: []string{constants.RoleCodeAdmin}},
		adminB:   &dto.AuthPrincipal{UserID: 2001, TenantID: 202, ActiveTenantID: 202, Username: "tenant-b-admin", Roles: []string{constants.RoleCodeAdmin}},
		aiAgentA: models.AIAgent{TenantID: 101, Name: "A 租户运行测试 Agent", Status: enums.StatusOk, ServiceMode: enums.IMConversationServiceModeAIOnly},
		aiAgentB: models.AIAgent{TenantID: 202, Name: "B 租户运行测试 Agent", Status: enums.StatusOk, ServiceMode: enums.IMConversationServiceModeAIOnly},
		channelA: models.Channel{TenantID: 101, Name: "A租户渠道", ChannelType: enums.ChannelTypeWeb, ChannelID: "conversation-runtime-a", Status: enums.StatusOk},
		channelB: models.Channel{TenantID: 202, Name: "B租户渠道", ChannelType: enums.ChannelTypeWeb, ChannelID: "conversation-runtime-b", Status: enums.StatusOk},
		teamA:    models.AgentTeam{TenantID: 101, Name: "A租户客服组", Status: enums.StatusOk},
		teamB:    models.AgentTeam{TenantID: 202, Name: "B租户客服组", Status: enums.StatusOk},
		userA:    models.User{ID: 1001, TenantID: 101, Username: "tenant-a-admin", Status: enums.StatusOk},
		userB:    models.User{ID: 2001, TenantID: 202, Username: "tenant-b-admin", Status: enums.StatusOk},
	}
	for label, item := range map[string]any{
		"ai agent A": &fixture.aiAgentA,
		"ai agent B": &fixture.aiAgentB,
		"channel A":  &fixture.channelA,
		"channel B":  &fixture.channelB,
		"team A":     &fixture.teamA,
		"team B":     &fixture.teamB,
		"user A":     &fixture.userA,
		"user B":     &fixture.userB,
	} {
		if err := db.Create(item).Error; err != nil {
			t.Fatalf("create %s: %v", label, err)
		}
	}
	if err := db.Model(&models.Channel{}).Where("id = ?", fixture.channelA.ID).Update("ai_agent_id", fixture.aiAgentA.ID).Error; err != nil {
		t.Fatalf("bind tenant A channel to AI Agent: %v", err)
	}
	if err := db.Model(&models.Channel{}).Where("id = ?", fixture.channelB.ID).Update("ai_agent_id", fixture.aiAgentB.ID).Error; err != nil {
		t.Fatalf("bind tenant B channel to AI Agent: %v", err)
	}
	fixture.channelA.AIAgentID = fixture.aiAgentA.ID
	fixture.channelB.AIAgentID = fixture.aiAgentB.ID
	fixture.profileA = models.AgentProfile{TenantID: 101, UserID: fixture.userA.ID, TeamID: fixture.teamA.ID, AgentCode: "tenant-a-agent", DisplayName: "A租户客服", ServiceStatus: enums.ServiceStatusIdle, MaxConcurrentCount: 10, AutoAssignEnabled: true, Status: enums.StatusOk}
	fixture.profileB = models.AgentProfile{TenantID: 202, UserID: fixture.userB.ID, TeamID: fixture.teamB.ID, AgentCode: "tenant-b-agent", DisplayName: "B租户客服", ServiceStatus: enums.ServiceStatusIdle, MaxConcurrentCount: 10, AutoAssignEnabled: true, Status: enums.StatusOk}
	if err := db.Create(&fixture.profileA).Error; err != nil {
		t.Fatalf("create tenant A profile: %v", err)
	}
	if err := db.Create(&fixture.profileB).Error; err != nil {
		t.Fatalf("create tenant B profile: %v", err)
	}
	now := time.Now()
	schedules := []models.AgentTeamSchedule{
		{TenantID: 101, TeamID: fixture.teamA.ID, StartAt: now.Add(-time.Hour), EndAt: now.Add(time.Hour), Status: enums.StatusOk},
		{TenantID: 202, TeamID: fixture.teamB.ID, StartAt: now.Add(-time.Hour), EndAt: now.Add(time.Hour), Status: enums.StatusOk},
	}
	if err := db.Create(&schedules).Error; err != nil {
		t.Fatalf("create schedules: %v", err)
	}
	return fixture
}

func assertConversationChildrenTenant(t *testing.T, db *gorm.DB, conversationID, tenantID int64) {
	t.Helper()
	checks := []struct {
		name  string
		model any
	}{
		{name: "participant", model: &models.ConversationParticipant{}},
		{name: "route", model: &models.ConversationRouteState{}},
		{name: "read state", model: &models.ConversationReadState{}},
		{name: "message", model: &models.Message{}},
		{name: "event log", model: &models.ConversationEventLog{}},
	}
	for _, check := range checks {
		var count int64
		if err := db.Model(check.model).Where("conversation_id = ?", conversationID).Count(&count).Error; err != nil {
			t.Fatalf("count %s: %v", check.name, err)
		}
		if count == 0 {
			t.Fatalf("expected at least one %s", check.name)
		}
		var mismatched int64
		if err := db.Model(check.model).Where("conversation_id = ? AND tenant_id <> ?", conversationID, tenantID).Count(&mismatched).Error; err != nil {
			t.Fatalf("count mismatched %s: %v", check.name, err)
		}
		if mismatched != 0 {
			t.Fatalf("%s contains %d rows outside tenant %d", check.name, mismatched, tenantID)
		}
	}
}

func TestConversationInterruptRejectsCrossTenantCheckpointReuse(t *testing.T) {
	fixture := setupConversationRuntimeTenantFixture(t)
	conversationA := &models.Conversation{TenantID: 101, ChannelID: fixture.channelA.ID, Status: enums.IMConversationStatusAIServing, LastActiveAt: time.Now(), LastMessageAt: time.Now()}
	conversationB := &models.Conversation{TenantID: 202, ChannelID: fixture.channelB.ID, Status: enums.IMConversationStatusAIServing, LastActiveAt: time.Now(), LastMessageAt: time.Now()}
	if err := fixture.db.Create(conversationA).Error; err != nil {
		t.Fatalf("create tenant A conversation: %v", err)
	}
	if err := fixture.db.Create(conversationB).Error; err != nil {
		t.Fatalf("create tenant B conversation: %v", err)
	}
	if err := ConversationInterruptService.CreateOrUpdatePending(&models.ConversationInterrupt{ConversationID: conversationA.ID, CheckPointID: "shared-checkpoint", Status: "pending"}); err != nil {
		t.Fatalf("create tenant A interrupt: %v", err)
	}
	err := ConversationInterruptService.CreateOrUpdatePending(&models.ConversationInterrupt{ConversationID: conversationB.ID, CheckPointID: "shared-checkpoint", Status: "pending"})
	if err == nil || !strings.Contains(err.Error(), "其他接入公司") {
		t.Fatalf("expected cross-tenant checkpoint conflict, got %v", err)
	}
	current := ConversationInterruptService.GetByCheckPointID("shared-checkpoint")
	if current == nil || current.TenantID != 101 || current.ConversationID != conversationA.ID {
		t.Fatalf("tenant A interrupt changed after conflict: %+v", current)
	}
}
