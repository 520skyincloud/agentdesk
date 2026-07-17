package services

import (
	"os"
	"sync"
	"testing"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/utils"
	"agent-desk/internal/repositories"

	"github.com/glebarez/sqlite"
	"github.com/mlogclub/simple/sqls"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
)

func TestServiceAnalyticsCaptureAndHumanOnlyQuality(t *testing.T) {
	db := setupServiceAnalyticsTestDB(t)
	tenantID := int64(101)
	t0 := time.Date(2026, 7, 17, 9, 0, 0, 0, time.Local)
	conversation := &models.Conversation{
		TenantID: tenantID, CustomerID: 801, CustomerName: "测试客户", Status: enums.IMConversationStatusAIServing,
		ServiceMode: enums.IMConversationServiceModeAIFirst, LastActiveAt: t0, AuditFields: testAnalyticsAudit(t0),
	}
	if err := db.Create(conversation).Error; err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	route := &models.ConversationRouteState{
		TenantID: tenantID, ConversationID: conversation.ID, SessionNo: 1, SessionStartedAt: &t0,
		RouteStatus: enums.ConversationRouteStatusAIServing, RouteTarget: "ai", AuditFields: testAnalyticsAudit(t0),
	}
	if err := db.Create(route).Error; err != nil {
		t.Fatalf("create route: %v", err)
	}
	customerAt := t0.Add(time.Second)
	customer := createAnalyticsMessage(t, db, tenantID, conversation.ID, 1, 1, enums.IMSenderTypeCustomer, 0, "customer-1", customerAt)
	if err := ServiceAnalyticsCaptureService.RecordMessage(customer); err != nil {
		t.Fatalf("capture customer message: %v", err)
	}
	queueAt := t0.Add(5 * time.Second)
	if err := db.Model(route).Updates(map[string]any{"route_status": enums.ConversationRouteStatusHQAgentDeskPending, "updated_at": queueAt}).Error; err != nil {
		t.Fatalf("enter queue: %v", err)
	}
	if err := ServiceAnalyticsCaptureService.RecordQueueEntry(conversation.ID, queueAt); err != nil {
		t.Fatalf("capture queue: %v", err)
	}
	user := &models.User{TenantID: tenantID, Username: "agent-a", Nickname: "客服A", Status: enums.StatusOk, AuditFields: testAnalyticsAudit(t0)}
	if err := db.Create(user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	team := &models.AgentTeam{TenantID: tenantID, Name: "白班组", Status: enums.StatusOk, AuditFields: testAnalyticsAudit(t0)}
	if err := db.Create(team).Error; err != nil {
		t.Fatalf("create team: %v", err)
	}
	profile := &models.AgentProfile{TenantID: tenantID, UserID: user.ID, TeamID: team.ID, AgentCode: "A-1", DisplayName: "客服A", Status: enums.StatusOk, AuditFields: testAnalyticsAudit(t0)}
	if err := db.Create(profile).Error; err != nil {
		t.Fatalf("create profile: %v", err)
	}
	assignedAt := t0.Add(10 * time.Second)
	assignment := &models.ConversationAssignment{
		TenantID: tenantID, ConversationID: conversation.ID, SessionNo: 1, ToUserID: user.ID,
		AssignType: string(enums.IMAssignmentTypeAssign), Status: enums.IMAssignmentStatusActive, CreatedAt: assignedAt,
	}
	if err := db.Create(assignment).Error; err != nil {
		t.Fatalf("create assignment: %v", err)
	}
	if err := db.Model(conversation).Updates(map[string]any{"status": enums.IMConversationStatusActive, "current_assignee_id": user.ID, "current_team_id": team.ID}).Error; err != nil {
		t.Fatalf("update conversation assignment: %v", err)
	}
	if err := ServiceAnalyticsCaptureService.RecordCurrentAssignment(conversation.ID); err != nil {
		t.Fatalf("capture assignment: %v", err)
	}
	if err := ServiceAnalyticsCaptureService.RecordDispatchDecision(conversation.ID, user.ID, 0, "auto_assign", "自动分配"); err != nil {
		t.Fatalf("capture dispatch decision: %v", err)
	}
	if err := ServiceAnalyticsCaptureService.RecordDispatchDecision(conversation.ID, user.ID, 0, "auto_assign", "自动分配"); err != nil {
		t.Fatalf("capture duplicate dispatch decision: %v", err)
	}
	decisionLogs := repositories.DispatchDecisionLogRepository.Find(db, sqls.NewCnd().Eq("tenant_id", tenantID).Eq("assignment_id", assignment.ID))
	if len(decisionLogs) != 1 || decisionLogs[0].DecisionMode != "rule" || decisionLogs[0].SelectedTeamID != team.ID {
		t.Fatalf("dispatch decision logs=%+v", decisionLogs)
	}
	aiAt := t0.Add(15 * time.Second)
	ai := createAnalyticsMessage(t, db, tenantID, conversation.ID, 1, 2, enums.IMSenderTypeAI, 0, "ai-1", aiAt)
	if err := ServiceAnalyticsCaptureService.RecordMessage(ai); err != nil {
		t.Fatalf("capture ai message: %v", err)
	}
	if waiting := repositories.ConversationResponseSpanRepository.FindLastWaiting(db, tenantID, conversation.ID, 1); waiting == nil {
		t.Fatal("AI reply must not close human response span")
	}
	replyAt := t0.Add(25 * time.Second)
	reply := createAnalyticsMessage(t, db, tenantID, conversation.ID, 1, 3, enums.IMSenderTypeAgent, user.ID, "agent-1", replyAt)
	if err := ServiceAnalyticsCaptureService.RecordMessage(reply); err != nil {
		t.Fatalf("capture agent message: %v", err)
	}
	session := repositories.ConversationServiceSessionRepository.TakeByKey(db, tenantID, conversation.ID, 1)
	if session == nil {
		t.Fatal("service session missing")
	}
	if session.FirstResponseSeconds != 15 {
		t.Fatalf("first response seconds=%d want 15", session.FirstResponseSeconds)
	}
	if session.TotalHumanWaitSeconds != 20 {
		t.Fatalf("total human wait seconds=%d want 20", session.TotalHumanWaitSeconds)
	}
	if session.CustomerMessageCount != 1 || session.AIMessageCount != 1 || session.HumanMessageCount != 1 {
		t.Fatalf("message counts customer=%d ai=%d human=%d", session.CustomerMessageCount, session.AIMessageCount, session.HumanMessageCount)
	}
	spans := repositories.ConversationResponseSpanRepository.Find(db, sqls.NewCnd().Eq("tenant_id", tenantID).Eq("conversation_id", conversation.ID))
	if len(spans) != 1 || spans[0].Status != enums.ResponseSpanStatusReplied || spans[0].ReplyMessageID != reply.ID {
		t.Fatalf("response spans=%+v", spans)
	}
	reportOperator := &dto.AuthPrincipal{UserID: 991, TenantID: tenantID, ActiveTenantID: tenantID, Username: "report-admin", Roles: []string{constants.RoleCodeTenantAdmin}}
	overview, err := ServiceAnalyticsService.GetOverview(ServiceAnalyticsQuery{StartAt: t0, EndAt: t0.Add(time.Hour)}, reportOperator)
	if err != nil {
		t.Fatalf("get analytics overview: %v", err)
	}
	if overview.Summary.SessionCount != 1 || overview.Summary.UniqueCustomerCount != 1 || overview.Summary.TotalMessageCount != 3 {
		t.Fatalf("overview volume summary=%+v", overview.Summary)
	}
	if overview.Summary.AverageFirstReplySeconds != 15 || overview.Summary.AverageResponseSeconds != 20 || overview.Summary.ResponseSLARate != 100 {
		t.Fatalf("overview response summary=%+v", overview.Summary)
	}
	if len(overview.ResponseDistribution) == 0 || overview.ResponseDistribution[0].Count != 1 {
		t.Fatalf("response distribution=%+v", overview.ResponseDistribution)
	}
	if len(overview.Agents) != 1 || overview.Agents[0].AssignedCount != 1 || overview.Agents[0].RepliedCount != 1 || overview.Agents[0].HumanMessageCount != 1 {
		t.Fatalf("agent analytics=%+v", overview.Agents)
	}

	operator := &dto.AuthPrincipal{UserID: 990, TenantID: tenantID, ActiveTenantID: tenantID, Username: "qa", Roles: []string{constants.RoleCodeTenantAdmin}}
	template, err := QualityInspectionService.EnsureDefaultTemplate(tenantID)
	if err != nil {
		t.Fatalf("ensure template: %v", err)
	}
	items := make([]request.QualityInspectionItemRequest, 0, len(template.Items))
	for _, item := range template.Items {
		items = append(items, request.QualityInspectionItemRequest{TemplateItemID: item.ID, Score: item.MaxScore, MessageIDs: []int64{reply.ID}})
	}
	inspection, err := QualityInspectionService.SaveInspection(request.SaveQualityInspectionRequest{
		AssignmentID: assignment.ID, TemplateID: template.Template.ID, Status: string(enums.QualityInspectionStatusCompleted), Items: items,
	}, operator)
	if err != nil {
		t.Fatalf("save human quality inspection: %v", err)
	}
	if inspection.Inspection.AgentID != user.ID || inspection.Inspection.TotalScore != 100 {
		t.Fatalf("inspection=%+v", inspection.Inspection)
	}
	items[0].MessageIDs = []int64{ai.ID}
	if _, err := QualityInspectionService.SaveInspection(request.SaveQualityInspectionRequest{
		ID: inspection.Inspection.ID, AssignmentID: assignment.ID, TemplateID: template.Template.ID, Status: string(enums.QualityInspectionStatusCompleted), Items: items,
	}, operator); err == nil {
		t.Fatal("AI message must not be accepted as human quality evidence")
	}
}

func TestQualityInspectionRejectsAssignedButUnansweredSession(t *testing.T) {
	db := setupServiceAnalyticsTestDB(t)
	tenantID := int64(202)
	now := time.Now()
	conversation := &models.Conversation{TenantID: tenantID, CustomerName: "未回复客户", Status: enums.IMConversationStatusActive, AuditFields: testAnalyticsAudit(now)}
	if err := db.Create(conversation).Error; err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	assignment := &models.ConversationAssignment{TenantID: tenantID, ConversationID: conversation.ID, SessionNo: 1, ToUserID: 55, AssignType: "assign", Status: enums.IMAssignmentStatusActive, CreatedAt: now}
	if err := db.Create(assignment).Error; err != nil {
		t.Fatalf("create assignment: %v", err)
	}
	operator := &dto.AuthPrincipal{UserID: 1, ActiveTenantID: tenantID, Roles: []string{constants.RoleCodeTenantAdmin}}
	template, err := QualityInspectionService.EnsureDefaultTemplate(tenantID)
	if err != nil {
		t.Fatalf("ensure template: %v", err)
	}
	if _, err := QualityInspectionService.SaveInspection(request.SaveQualityInspectionRequest{
		AssignmentID: assignment.ID, TemplateID: template.Template.ID, Status: string(enums.QualityInspectionStatusDraft),
		Items: []request.QualityInspectionItemRequest{{TemplateItemID: template.Items[0].ID, Score: 0}},
	}, operator); err == nil {
		t.Fatal("assigned but unanswered segment must not enter content quality inspection")
	}
}

func TestQueueEntryCapturesPendingTeamScope(t *testing.T) {
	db := setupServiceAnalyticsTestDB(t)
	tenantID := int64(301)
	now := time.Date(2026, 7, 17, 11, 0, 0, 0, time.Local)
	teamA := &models.AgentTeam{TenantID: tenantID, Name: "A客服组", LeaderUserID: 3101, Status: enums.StatusOk, AuditFields: testAnalyticsAudit(now)}
	teamB := &models.AgentTeam{TenantID: tenantID, Name: "B客服组", LeaderUserID: 3201, Status: enums.StatusOk, AuditFields: testAnalyticsAudit(now)}
	for _, team := range []*models.AgentTeam{teamA, teamB} {
		if err := db.Create(team).Error; err != nil {
			t.Fatalf("create team: %v", err)
		}
	}

	createPending := func(customerName string, teamID int64) *models.Conversation {
		t.Helper()
		conversation := &models.Conversation{
			TenantID: tenantID, CustomerName: customerName, Status: enums.IMConversationStatusPending,
			CurrentTeamID: teamID, LastActiveAt: now, AuditFields: testAnalyticsAudit(now),
		}
		if err := db.Create(conversation).Error; err != nil {
			t.Fatalf("create conversation: %v", err)
		}
		if err := db.Create(&models.ConversationRouteState{
			TenantID: tenantID, ConversationID: conversation.ID, SessionNo: 1,
			RouteStatus: enums.ConversationRouteStatusHQAgentDeskPending, AuditFields: testAnalyticsAudit(now),
		}).Error; err != nil {
			t.Fatalf("create route: %v", err)
		}
		if err := ServiceAnalyticsCaptureService.RecordQueueEntry(conversation.ID, now); err != nil {
			t.Fatalf("capture queue entry: %v", err)
		}
		return conversation
	}

	conversationA := createPending("A组待派客户", teamA.ID)
	conversationB := createPending("B组待派客户", teamB.ID)
	for _, expected := range []struct {
		conversationID int64
		teamID         int64
	}{{conversationA.ID, teamA.ID}, {conversationB.ID, teamB.ID}} {
		session := repositories.ConversationServiceSessionRepository.TakeByKey(db, tenantID, expected.conversationID, 1)
		if session == nil {
			t.Fatalf("pending session missing for conversation=%d", expected.conversationID)
		}
		if session.AssignedTeamID != expected.teamID {
			t.Fatalf("pending session conversation=%d team=%d want=%d", expected.conversationID, session.AssignedTeamID, expected.teamID)
		}
	}

	leaderA := &dto.AuthPrincipal{UserID: teamA.LeaderUserID, ActiveTenantID: tenantID, Roles: []string{constants.RoleCodeCsTeamLeader}}
	list, paging, err := ServiceAnalyticsService.ListSessions(ServiceSessionQuery{Page: 1, Limit: 20}, leaderA)
	if err != nil {
		t.Fatalf("list leader sessions: %v", err)
	}
	if paging.Total != 1 || len(list) != 1 || list[0].ConversationID != conversationA.ID {
		t.Fatalf("leader A pending scope total=%d sessions=%+v", paging.Total, list)
	}

	if err := db.Model(conversationA).Update("current_team_id", 0).Error; err != nil {
		t.Fatalf("release conversation to global pool: %v", err)
	}
	if err := ServiceAnalyticsCaptureService.RecordQueueEntry(conversationA.ID, now.Add(time.Minute)); err != nil {
		t.Fatalf("capture global queue entry: %v", err)
	}
	sessionA := repositories.ConversationServiceSessionRepository.TakeByKey(db, tenantID, conversationA.ID, 1)
	if sessionA == nil || sessionA.AssignedTeamID != 0 {
		t.Fatalf("global pending session retained team scope: %+v", sessionA)
	}
	list, paging, err = ServiceAnalyticsService.ListSessions(ServiceSessionQuery{Page: 1, Limit: 20}, leaderA)
	if err != nil {
		t.Fatalf("list leader sessions after release: %v", err)
	}
	if paging.Total != 0 || len(list) != 0 {
		t.Fatalf("released global session remained visible to team leader total=%d sessions=%+v", paging.Total, list)
	}
}

func TestAgentPresenceStartsNewSessionAfterStaleHeartbeat(t *testing.T) {
	db := setupServiceAnalyticsTestDB(t)
	t0 := time.Date(2026, 7, 17, 8, 0, 0, 0, time.Local)
	tenantID := int64(801)
	user := &models.User{TenantID: tenantID, Username: "presence-agent", Status: enums.StatusOk, AuditFields: testAnalyticsAudit(t0)}
	if err := db.Create(user).Error; err != nil {
		t.Fatalf("create presence user: %v", err)
	}
	profile := &models.AgentProfile{TenantID: tenantID, UserID: user.ID, AgentCode: "P-1", DisplayName: "在线客服", Status: enums.StatusOk, AuditFields: testAnalyticsAudit(t0)}
	if err := db.Create(profile).Error; err != nil {
		t.Fatalf("create presence profile: %v", err)
	}
	operator := &dto.AuthPrincipal{UserID: user.ID, ActiveTenantID: tenantID, Username: user.Username}
	if err := AgentPresenceService.Touch(operator, "test", t0); err != nil {
		t.Fatalf("start presence: %v", err)
	}
	lastSeenAt := t0.Add(time.Minute)
	if err := AgentPresenceService.Touch(operator, "test", lastSeenAt); err != nil {
		t.Fatalf("refresh presence: %v", err)
	}
	reconnectedAt := t0.Add(10 * time.Minute)
	if err := AgentPresenceService.Touch(operator, "test", reconnectedAt); err != nil {
		t.Fatalf("restart stale presence: %v", err)
	}
	presences := repositories.AgentPresenceSessionRepository.Find(db, sqls.NewCnd().Eq("tenant_id", tenantID).Eq("user_id", user.ID).Asc("id"))
	if len(presences) != 2 || presences[0].EndedAt == nil || !presences[0].EndedAt.Equal(lastSeenAt) || presences[1].EndedAt != nil || !presences[1].StartedAt.Equal(reconnectedAt) {
		t.Fatalf("presence rollover=%+v", presences)
	}
}

func TestAgentPresenceConcurrentHeartbeatAndBreakKeepSingleActiveSession(t *testing.T) {
	db := setupServiceAnalyticsTestDB(t)
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sql db: %v", err)
	}
	if db.Dialector.Name() == "sqlite" {
		sqlDB.SetMaxOpenConns(1)
	} else {
		sqlDB.SetMaxOpenConns(16)
	}
	t0 := time.Date(2026, 7, 17, 13, 0, 0, 0, time.Local)
	tenantID := int64(802)
	user := &models.User{TenantID: tenantID, Username: "presence-concurrent-agent", Status: enums.StatusOk, AuditFields: testAnalyticsAudit(t0)}
	if err := db.Create(user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	profile := &models.AgentProfile{TenantID: tenantID, UserID: user.ID, AgentCode: "P-2", DisplayName: "并发客服", Status: enums.StatusOk, AuditFields: testAnalyticsAudit(t0)}
	if err := db.Create(profile).Error; err != nil {
		t.Fatalf("create profile: %v", err)
	}
	operator := &dto.AuthPrincipal{UserID: user.ID, ActiveTenantID: tenantID, Username: user.Username}
	if err := AgentPresenceService.Touch(operator, "test", t0); err != nil {
		t.Fatalf("start presence: %v", err)
	}

	errCh := make(chan error, 9)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errCh <- AgentPresenceService.Touch(operator, "dashboard_ws", t0.Add(time.Minute))
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, err := AgentPresenceService.SetStatus(operator, enums.AgentPresenceStatusBreak, "会议", t0.Add(2*time.Minute))
		errCh <- err
	}()
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatalf("concurrent presence update: %v", err)
		}
	}

	presences := repositories.AgentPresenceSessionRepository.Find(db, sqls.NewCnd().Eq("tenant_id", tenantID).Eq("user_id", user.ID).Asc("id"))
	activeCount := 0
	var active *models.AgentPresenceSession
	for i := range presences {
		if presences[i].EndedAt == nil {
			activeCount++
			active = &presences[i]
		}
	}
	if activeCount != 1 || active == nil || active.Status != enums.AgentPresenceStatusBreak || active.BreakReason != "会议" {
		t.Fatalf("presence sessions=%+v", presences)
	}
	if active.LastSeenAt.Before(t0.Add(2 * time.Minute)) {
		t.Fatalf("presence time moved backwards: %+v", active)
	}
}

func setupServiceAnalyticsTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	testModels := []any{
		&models.User{}, &models.Channel{}, &models.Tag{}, &models.Conversation{}, &models.ConversationRouteState{}, &models.Message{}, &models.ConversationAssignment{},
		&models.AgentProfile{}, &models.AgentTeam{}, &models.AgentTeamSquad{}, &models.AgentTeamSquadMember{},
		&models.Store{}, &models.WxWorkProtocolInstance{}, &models.ConversationServiceSession{}, &models.ConversationResponseSpan{},
		&models.AgentPresenceSession{}, &models.ServiceAnalyticsPolicy{},
		&models.QualityTemplate{}, &models.QualityTemplateItem{}, &models.QualityInspection{}, &models.QualityInspectionItem{},
		&models.QualitySamplingBatch{}, &models.QualitySamplingItem{}, &models.DispatchDecisionLog{},
		&models.ConversationEvaluation{}, &models.ReportViewPreset{},
	}
	mysqlDSN := os.Getenv("AGENT_DESK_SERVICE_ANALYTICS_TEST_MYSQL_DSN")
	var dialector gorm.Dialector = sqlite.Open("file:" + stringsForServiceAnalyticsTest(t.Name()) + "?mode=memory&cache=shared")
	if mysqlDSN != "" {
		dialector = mysql.Open(mysqlDSN)
	}
	db, err := gorm.Open(dialector, &gorm.Config{
		Logger:         logger.Default.LogMode(logger.Silent),
		NamingStrategy: schema.NamingStrategy{TablePrefix: "t_", SingularTable: true},
	})
	if err != nil {
		t.Fatalf("open service analytics test database: %v", err)
	}
	if mysqlDSN != "" {
		if err := db.Exec("SET FOREIGN_KEY_CHECKS = 0").Error; err != nil {
			t.Fatalf("disable mysql foreign key checks: %v", err)
		}
		for i := len(testModels) - 1; i >= 0; i-- {
			if err := db.Migrator().DropTable(testModels[i]); err != nil {
				t.Fatalf("reset mysql test table: %v", err)
			}
		}
		if err := db.Exec("SET FOREIGN_KEY_CHECKS = 1").Error; err != nil {
			t.Fatalf("enable mysql foreign key checks: %v", err)
		}
	}
	if err := db.AutoMigrate(testModels...); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	sqls.SetDB(db)
	t.Cleanup(func() { sqls.SetDB(nil) })
	return db
}

func createAnalyticsMessage(t *testing.T, db *gorm.DB, tenantID, conversationID int64, sessionNo int, seq int64, senderType enums.IMSenderType, senderID int64, clientMsgID string, at time.Time) *models.Message {
	t.Helper()
	item := &models.Message{
		TenantID: tenantID, ConversationID: conversationID, SessionNo: sessionNo, SenderType: senderType, SenderID: senderID,
		ClientMsgID: clientMsgID, MessageType: enums.IMMessageTypeText, Content: clientMsgID, SeqNo: seq, SendStatus: enums.IMMessageStatusSent,
		SentAt: &at, AuditFields: testAnalyticsAudit(at),
	}
	if err := db.Create(item).Error; err != nil {
		t.Fatalf("create message: %v", err)
	}
	return item
}

func testAnalyticsAudit(at time.Time) models.AuditFields {
	audit := utils.BuildAuditFields(nil)
	audit.CreatedAt = at
	audit.UpdatedAt = at
	return audit
}

func stringsForServiceAnalyticsTest(value string) string {
	ret := make([]rune, 0, len(value))
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') {
			ret = append(ret, char)
		} else {
			ret = append(ret, '_')
		}
	}
	return string(ret)
}
