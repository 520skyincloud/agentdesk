package services

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"agent-desk/internal/ai/rag"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/dto/response"
	"agent-desk/internal/pkg/enums"
	fastgptapi "agent-desk/internal/pkg/fastgpt"
	"agent-desk/internal/repositories"

	"github.com/glebarez/sqlite"
	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestKnowledgeRuntimeTenantIsolation(t *testing.T) {
	db, adminA, adminB := setupKnowledgeTenantRuntimeDB(t)
	storeA := &models.Store{TenantID: adminA.ActiveTenantID, StoreCode: "knowledge-runtime-store-a", Name: "A store", Status: enums.StatusOk}
	storeB := &models.Store{TenantID: adminB.ActiveTenantID, StoreCode: "knowledge-runtime-store-b", Name: "B store", Status: enums.StatusOk}
	for _, store := range []*models.Store{storeA, storeB} {
		if err := db.Create(store).Error; err != nil {
			t.Fatalf("create tenant store: %v", err)
		}
	}
	baseA := createTenantFastGPTKnowledgeBase(t, db, adminA.ActiveTenantID, storeA.ID, "A knowledge", "dataset-a")
	baseB := createTenantFastGPTKnowledgeBase(t, db, adminB.ActiveTenantID, storeB.ID, "B knowledge", "dataset-b")
	if err := db.Model(storeA).Update("knowledge_base_id", baseA.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(storeB).Update("knowledge_base_id", baseB.ID).Error; err != nil {
		t.Fatal(err)
	}
	if baseA.TenantID != adminA.ActiveTenantID || baseB.TenantID != adminB.ActiveTenantID {
		t.Fatalf("knowledge bases missing tenant ownership: A=%+v B=%+v", baseA, baseB)
	}
	if KnowledgeBaseService.GetForOperator(baseB.ID, adminA) != nil || KnowledgeBaseService.CanAccessKnowledgeBase(baseB.ID, adminA) {
		t.Fatal("tenant A can access tenant B knowledge base")
	}
	if err := KnowledgeBaseService.UpdateKnowledgeBase(request.UpdateKnowledgeBaseRequest{ID: baseB.ID, Name: "cross tenant update"}, adminA); err == nil {
		t.Fatal("tenant A updated tenant B knowledge base")
	}
	currentB := repositories.KnowledgeBaseRepository.GetInTenant(db, baseB.ID, adminB.ActiveTenantID)
	if currentB == nil || currentB.Name != "B knowledge" {
		t.Fatalf("tenant B knowledge base changed: %+v", currentB)
	}

	conversationA := &models.Conversation{TenantID: adminA.ActiveTenantID, CustomerName: "A customer", Status: enums.IMConversationStatusActive, LastActiveAt: time.Now(), LastMessageAt: time.Now(), AuditFields: models.AuditFields{CreatedAt: time.Now(), UpdatedAt: time.Now()}}
	if err := db.Create(conversationA).Error; err != nil {
		t.Fatalf("create tenant A conversation: %v", err)
	}
	candidate, err := KnowledgeCandidateService.UpsertCandidate(storeA.ID, baseA.ID, conversationA.ID, nil, enums.KnowledgeCandidateSourceAINoAnswer, "question", "answer", "", "", 0.8, "system")
	if err != nil {
		t.Fatalf("create tenant A candidate: %v", err)
	}
	if candidate.TenantID != adminA.ActiveTenantID {
		t.Fatalf("candidate tenant=%d want=%d", candidate.TenantID, adminA.ActiveTenantID)
	}
	if _, err := KnowledgeCandidateService.UpsertCandidate(storeA.ID, baseB.ID, conversationA.ID, nil, enums.KnowledgeCandidateSourceAINoAnswer, "cross tenant question", "answer", "", "", 0.8, "system"); err == nil {
		t.Fatal("cross-tenant candidate was created")
	}
	conversationAOther := &models.Conversation{TenantID: adminA.ActiveTenantID, CustomerName: "A other customer", Status: enums.IMConversationStatusActive, LastActiveAt: time.Now(), LastMessageAt: time.Now(), AuditFields: models.AuditFields{CreatedAt: time.Now(), UpdatedAt: time.Now()}}
	conversationB := &models.Conversation{TenantID: adminB.ActiveTenantID, CustomerName: "B customer", Status: enums.IMConversationStatusActive, LastActiveAt: time.Now(), LastMessageAt: time.Now(), AuditFields: models.AuditFields{CreatedAt: time.Now(), UpdatedAt: time.Now()}}
	if err := db.Create(conversationAOther).Error; err != nil {
		t.Fatalf("create other tenant A conversation: %v", err)
	}
	if err := db.Create(conversationB).Error; err != nil {
		t.Fatalf("create tenant B conversation: %v", err)
	}
	now := time.Now()
	messageA := &models.Message{TenantID: adminA.ActiveTenantID, ConversationID: conversationA.ID, SenderType: enums.IMSenderTypeCustomer, MessageType: enums.IMMessageTypeText, Content: "A evidence", SendStatus: enums.IMMessageStatusSent, SentAt: &now}
	messageAOther := &models.Message{TenantID: adminA.ActiveTenantID, ConversationID: conversationAOther.ID, SenderType: enums.IMSenderTypeCustomer, MessageType: enums.IMMessageTypeText, Content: "A other evidence", SendStatus: enums.IMMessageStatusSent, SentAt: &now}
	messageB := &models.Message{TenantID: adminB.ActiveTenantID, ConversationID: conversationB.ID, SenderType: enums.IMSenderTypeCustomer, MessageType: enums.IMMessageTypeText, Content: "B evidence", SendStatus: enums.IMMessageStatusSent, SentAt: &now}
	for label, message := range map[string]*models.Message{"tenant A": messageA, "other conversation": messageAOther, "tenant B": messageB} {
		if err := db.Create(message).Error; err != nil {
			t.Fatalf("create %s message: %v", label, err)
		}
	}
	evidenceCandidate, err := KnowledgeCandidateService.UpsertCandidate(storeA.ID, baseA.ID, conversationA.ID, []int64{messageA.ID, messageA.ID}, enums.KnowledgeCandidateSourceAINoAnswer, "message evidence question", "answer", "", "", 0.8, "system")
	if err != nil {
		t.Fatalf("create candidate with same-conversation evidence: %v", err)
	}
	if evidenceCandidate.MessageIDs != fmt.Sprint(messageA.ID) {
		t.Fatalf("candidate message ids=%q want=%d", evidenceCandidate.MessageIDs, messageA.ID)
	}
	if _, err := KnowledgeCandidateService.UpsertCandidate(storeA.ID, baseA.ID, conversationA.ID, []int64{messageB.ID}, enums.KnowledgeCandidateSourceAINoAnswer, "cross tenant evidence", "answer", "", "", 0.8, "system"); err == nil {
		t.Fatal("cross-tenant message evidence was accepted")
	}
	if _, err := KnowledgeCandidateService.UpsertCandidate(storeA.ID, baseA.ID, conversationA.ID, []int64{messageAOther.ID}, enums.KnowledgeCandidateSourceAINoAnswer, "cross conversation evidence", "answer", "", "", 0.8, "system"); err == nil {
		t.Fatal("same-tenant evidence from another conversation was accepted")
	}

	logItem, err := rag.RetrieveLog.CreateRetrieveLog(&rag.CreateRetrieveLogRequest{
		KnowledgeBaseID: baseA.ID,
		Question:        "tenant A retrieval",
		Hits:            []response.KnowledgeSearchResult{{KnowledgeBaseID: baseA.ID, SourceRecordID: "fastgpt-record-a", Content: "answer", Score: 0.9}},
	}, adminA)
	if err != nil {
		t.Fatalf("create tenant A retrieve log: %v", err)
	}
	if logItem.TenantID != adminA.ActiveTenantID {
		t.Fatalf("retrieve log tenant=%d want=%d", logItem.TenantID, adminA.ActiveTenantID)
	}
	if logItem.SourceType != "fastgpt" ||
		logItem.ChunkProvider != string(enums.KnowledgeChunkProviderFastGPT) ||
		logItem.ChunkTargetTokens != 0 ||
		logItem.ChunkMaxTokens != 0 ||
		logItem.ChunkOverlapTokens != 0 {
		t.Fatalf("new retrieve log kept legacy source or chunk settings: %#v", logItem)
	}
	if !strings.Contains(logItem.TraceData, `"sourceRecordId":"fastgpt-record-a"`) ||
		strings.Contains(logItem.TraceData, `"documentIds"`) ||
		strings.Contains(logItem.TraceData, `"usedChunkKeys"`) {
		t.Fatalf("new retrieve trace did not use FastGPT source identity: %s", logItem.TraceData)
	}
	if KnowledgeRetrieveLogService.GetInTenant(logItem.ID, adminB.ActiveTenantID) != nil {
		t.Fatal("tenant B can read tenant A retrieve log")
	}
	if _, err := rag.RetrieveLog.CreateRetrieveLog(&rag.CreateRetrieveLogRequest{KnowledgeBaseID: baseB.ID, Question: "cross tenant retrieval"}, adminA); err == nil {
		t.Fatal("tenant A created a tenant B retrieve log")
	}
}

func TestDashboardOverviewUsesActiveTenant(t *testing.T) {
	db, adminA, adminB := setupKnowledgeTenantRuntimeDB(t)
	now := time.Now()
	agentA := &models.AIAgent{TenantID: adminA.ActiveTenantID, Name: "A agent", Status: enums.StatusOk}
	agentB := &models.AIAgent{TenantID: adminB.ActiveTenantID, Name: "B agent", Status: enums.StatusOk}
	if err := db.Create(agentA).Error; err != nil {
		t.Fatalf("create agent A: %v", err)
	}
	if err := db.Create(agentB).Error; err != nil {
		t.Fatalf("create agent B: %v", err)
	}
	for _, item := range []any{
		&models.Channel{TenantID: adminA.ActiveTenantID, Name: "A channel", ChannelID: "dashboard-a", AIAgentID: agentA.ID, Status: enums.StatusOk},
		&models.Channel{TenantID: adminB.ActiveTenantID, Name: "B channel", ChannelID: "dashboard-b", AIAgentID: agentB.ID, Status: enums.StatusOk},
		&models.Conversation{TenantID: adminA.ActiveTenantID, CustomerName: "A customer", Status: enums.IMConversationStatusActive, LastActiveAt: now, LastMessageAt: now, AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now}},
		&models.Conversation{TenantID: adminB.ActiveTenantID, CustomerName: "B customer 1", Status: enums.IMConversationStatusPending, LastActiveAt: now, LastMessageAt: now, AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now}},
		&models.Conversation{TenantID: adminB.ActiveTenantID, CustomerName: "B customer 2", Status: enums.IMConversationStatusPending, LastActiveAt: now, LastMessageAt: now, AuditFields: models.AuditFields{CreatedAt: now, UpdatedAt: now}},
		&models.KnowledgeRetrieveLog{TenantID: adminA.ActiveTenantID, KnowledgeBaseID: 1, Question: "A", CreatedAt: now},
		&models.KnowledgeRetrieveLog{TenantID: adminB.ActiveTenantID, KnowledgeBaseID: 2, Question: "B1", CreatedAt: now},
		&models.KnowledgeRetrieveLog{TenantID: adminB.ActiveTenantID, KnowledgeBaseID: 2, Question: "B2", CreatedAt: now},
		&models.SkillRunLog{TenantID: adminA.ActiveTenantID, ErrorMessage: "A failure", CreatedAt: now},
		&models.SkillRunLog{TenantID: adminB.ActiveTenantID, ErrorMessage: "B failure 1", CreatedAt: now},
		&models.SkillRunLog{TenantID: adminB.ActiveTenantID, ErrorMessage: "B failure 2", CreatedAt: now},
	} {
		if err := db.Create(item).Error; err != nil {
			t.Fatalf("create dashboard fixture %T: %v", item, err)
		}
	}

	overviewA := DashboardService.GetOverview("7d", "zh-CN", adminA.ActiveTenantID)
	if overviewA.Summary.TodayNewConversations != 1 || overviewA.Summary.PendingDispatchConversations != 0 {
		t.Fatalf("tenant A conversation overview leaked: %+v", overviewA.Summary)
	}
	if overviewA.AIStats.EnabledChannels != 1 || overviewA.AIStats.EnabledAIAgents != 1 || overviewA.AIStats.TodayKnowledgeRetrieves != 1 || overviewA.AIStats.TodaySkillRunFailCount != 1 {
		t.Fatalf("tenant A AI overview leaked: %+v", overviewA.AIStats)
	}
	overviewB := DashboardService.GetOverview("7d", "zh-CN", adminB.ActiveTenantID)
	if overviewB.Summary.TodayNewConversations != 2 || overviewB.Summary.PendingDispatchConversations != 2 {
		t.Fatalf("tenant B conversation overview mismatch: %+v", overviewB.Summary)
	}
	if overviewB.AIStats.EnabledChannels != 1 || overviewB.AIStats.EnabledAIAgents != 1 || overviewB.AIStats.TodayKnowledgeRetrieves != 2 || overviewB.AIStats.TodaySkillRunFailCount != 2 {
		t.Fatalf("tenant B AI overview mismatch: %+v", overviewB.AIStats)
	}
}

func setupKnowledgeTenantRuntimeDB(t *testing.T) (*gorm.DB, *dto.AuthPrincipal, *dto.AuthPrincipal) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+strings.ReplaceAll(t.Name(), "/", "_")+"?mode=memory&cache=shared"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(
		&models.User{}, &models.Store{}, &models.Channel{}, &models.AIAgent{},
		&models.AgentTeam{}, &models.AgentProfile{}, &models.AgentTeamSchedule{},
		&models.Conversation{}, &models.Message{}, &models.ConversationRouteState{},
		&models.KnowledgeBase{},
		&models.KnowledgeCandidate{}, &models.KnowledgeRetrieveLog{}, &models.KnowledgeRetrieveHit{}, &models.KnowledgeFeedback{},
		&models.SkillRunLog{},
	); err != nil {
		t.Fatalf("auto migrate knowledge tenant runtime: %v", err)
	}
	sqls.SetDB(db)
	t.Cleanup(func() {
		sqls.SetDB(nil)
		if sqlDB, dbErr := db.DB(); dbErr == nil {
			_ = sqlDB.Close()
		}
	})
	adminA := &dto.AuthPrincipal{UserID: 101, TenantID: 101, ActiveTenantID: 101, Username: "tenant-a-admin", Roles: []string{constants.RoleCodeAdmin}}
	adminB := &dto.AuthPrincipal{UserID: 202, TenantID: 202, ActiveTenantID: 202, Username: "tenant-b-admin", Roles: []string{constants.RoleCodeAdmin}}
	return db, adminA, adminB
}

func createTenantFastGPTKnowledgeBase(t *testing.T, db *gorm.DB, tenantID, storeID int64, name, datasetID string) *models.KnowledgeBase {
	t.Helper()
	item := &models.KnowledgeBase{
		TenantID: tenantID, StoreID: storeID, Name: name,
		KnowledgeType: string(enums.KnowledgeBaseTypeFastGPTCloud), DatasetID: datasetID, DatasetName: name,
		ConnectionID: fastgptapi.ManagedConnectionID, RetrievalMode: enums.KnowledgeRetrievalModeFastGPT,
		ChunkProvider: string(enums.KnowledgeChunkProviderFastGPT), DefaultTopK: 10, DefaultScoreThreshold: 0.2,
		DefaultRerankLimit: 10, AnswerMode: int(enums.KnowledgeAnswerModeStrict), Status: enums.StatusOk,
	}
	if err := db.Create(item).Error; err != nil {
		t.Fatalf("create FastGPT knowledge base: %v", err)
	}
	return item
}
