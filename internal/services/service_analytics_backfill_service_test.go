package services

import (
	"testing"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
)

func TestServiceAnalyticsBackfillIsIdempotentAndTenantScoped(t *testing.T) {
	db := setupServiceAnalyticsTestDB(t)
	tenantID := int64(301)
	otherTenantID := int64(302)
	t0 := time.Date(2026, 7, 17, 8, 0, 0, 0, time.Local)
	queueAt := t0.Add(5 * time.Second)
	assignedAt := t0.Add(10 * time.Second)
	repliedAt := t0.Add(25 * time.Second)

	conversation := &models.Conversation{
		TenantID: tenantID, CustomerID: 901, CustomerName: "历史客户", Status: enums.IMConversationStatusActive,
		ServiceMode: enums.IMConversationServiceModeAIFirst, HandoffAt: &queueAt, LastMessageAt: repliedAt, LastActiveAt: repliedAt,
		AuditFields: testAnalyticsAudit(t0),
	}
	if err := db.Create(conversation).Error; err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	route := &models.ConversationRouteState{
		TenantID: tenantID, ConversationID: conversation.ID, SessionNo: 1, SessionStartedAt: &t0,
		LastManualHandoffAt: &queueAt, RouteStatus: enums.ConversationRouteStatusHQAgentDeskServing,
		RouteTarget: "hq_agentdesk", AuditFields: testAnalyticsAudit(t0),
	}
	if err := db.Create(route).Error; err != nil {
		t.Fatalf("create route: %v", err)
	}
	agent := &models.User{TenantID: tenantID, Username: "history-agent", Nickname: "历史客服", Status: enums.StatusOk, AuditFields: testAnalyticsAudit(t0)}
	if err := db.Create(agent).Error; err != nil {
		t.Fatalf("create agent: %v", err)
	}
	assignment := &models.ConversationAssignment{
		TenantID: tenantID, ConversationID: conversation.ID, SessionNo: 1, ToUserID: agent.ID,
		AssignType: string(enums.IMAssignmentTypeAssign), Reason: "自动分配", Status: enums.IMAssignmentStatusActive,
		CreatedAt: assignedAt,
	}
	if err := db.Create(assignment).Error; err != nil {
		t.Fatalf("create assignment: %v", err)
	}
	customer := createAnalyticsMessage(t, db, tenantID, conversation.ID, 1, 1, enums.IMSenderTypeCustomer, 0, "history-customer", t0.Add(time.Second))
	ai := createAnalyticsMessage(t, db, tenantID, conversation.ID, 1, 2, enums.IMSenderTypeAI, 0, "history-ai", t0.Add(15*time.Second))
	human := createAnalyticsMessage(t, db, tenantID, conversation.ID, 1, 3, enums.IMSenderTypeAgent, agent.ID, "history-human", repliedAt)
	_ = customer
	_ = ai

	createAnalyticsMessage(t, db, otherTenantID, conversation.ID, 1, 4, enums.IMSenderTypeCustomer, 0, "wrong-tenant-message", t0.Add(2*time.Second))
	orphan := &models.Conversation{
		TenantID: 0, CustomerName: "平台遗留数据", Status: enums.IMConversationStatusClosed,
		LastMessageAt: t0, LastActiveAt: t0, AuditFields: testAnalyticsAudit(t0),
	}
	if err := db.Create(orphan).Error; err != nil {
		t.Fatalf("create unscoped conversation: %v", err)
	}

	first, err := ServiceAnalyticsCaptureService.BackfillMissingFacts()
	if err != nil {
		t.Fatalf("first backfill: %v", err)
	}
	if first.Sessions != 1 || first.ResponseSpans != 1 || first.DispatchDecisions != 1 {
		t.Fatalf("first backfill result=%+v", first)
	}
	session := repositories.ConversationServiceSessionRepository.TakeByKey(db, tenantID, conversation.ID, 1)
	if session == nil {
		t.Fatal("historical service session missing")
	}
	if session.CustomerMessageCount != 1 || session.AIMessageCount != 1 || session.HumanMessageCount != 1 {
		t.Fatalf("tenant-scoped message counts customer=%d ai=%d human=%d", session.CustomerMessageCount, session.AIMessageCount, session.HumanMessageCount)
	}
	if session.AssignedAt == nil || !session.AssignedAt.Equal(assignedAt) || session.FirstResponseSeconds != 15 {
		t.Fatalf("historical first response assignedAt=%v seconds=%d", session.AssignedAt, session.FirstResponseSeconds)
	}
	spans := repositories.ConversationResponseSpanRepository.Find(db, sqls.NewCnd().
		Eq("tenant_id", tenantID).
		Eq("conversation_id", conversation.ID).
		Eq("session_no", 1))
	if len(spans) != 1 || spans[0].ReplyMessageID != human.ID || spans[0].Status != enums.ResponseSpanStatusReplied {
		t.Fatalf("AI must not close historical human response span: %+v", spans)
	}
	if repositories.ConversationServiceSessionRepository.TakeByKey(db, otherTenantID, conversation.ID, 1) != nil {
		t.Fatal("cross-tenant message must not create another tenant session")
	}
	if repositories.ConversationServiceSessionRepository.TakeByKey(db, 0, orphan.ID, 1) != nil {
		t.Fatal("unscoped legacy conversation must not be backfilled")
	}

	second, err := ServiceAnalyticsCaptureService.BackfillMissingFacts()
	if err != nil {
		t.Fatalf("second backfill: %v", err)
	}
	if second != (ServiceAnalyticsBackfillResult{}) {
		t.Fatalf("backfill must be idempotent, second result=%+v", second)
	}
}

func TestRecordCurrentAssignmentPreservesFirstAssignmentMetrics(t *testing.T) {
	db := setupServiceAnalyticsTestDB(t)
	tenantID := int64(401)
	t0 := time.Date(2026, 7, 17, 10, 0, 0, 0, time.Local)
	queueAt := t0.Add(5 * time.Second)
	firstAssignedAt := t0.Add(10 * time.Second)
	transferAt := t0.Add(40 * time.Second)

	conversation := &models.Conversation{
		TenantID: tenantID, CustomerName: "转派客户", Status: enums.IMConversationStatusPending,
		ServiceMode: enums.IMConversationServiceModeHumanOnly, LastMessageAt: t0, LastActiveAt: t0,
		AuditFields: testAnalyticsAudit(t0),
	}
	if err := db.Create(conversation).Error; err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	route := &models.ConversationRouteState{
		TenantID: tenantID, ConversationID: conversation.ID, SessionNo: 1, SessionStartedAt: &t0,
		RouteStatus: enums.ConversationRouteStatusHQAgentDeskPending, RouteTarget: "hq_agentdesk",
		AuditFields: testAnalyticsAudit(t0),
	}
	if err := db.Create(route).Error; err != nil {
		t.Fatalf("create route: %v", err)
	}
	if err := ServiceAnalyticsCaptureService.RecordQueueEntry(conversation.ID, queueAt); err != nil {
		t.Fatalf("record queue entry: %v", err)
	}
	first := &models.ConversationAssignment{
		TenantID: tenantID, ConversationID: conversation.ID, SessionNo: 1, ToUserID: 1001,
		AssignType: string(enums.IMAssignmentTypeAssign), Status: enums.IMAssignmentStatusActive, CreatedAt: firstAssignedAt,
	}
	if err := db.Create(first).Error; err != nil {
		t.Fatalf("create first assignment: %v", err)
	}
	if err := ServiceAnalyticsCaptureService.RecordCurrentAssignment(conversation.ID); err != nil {
		t.Fatalf("record first assignment: %v", err)
	}
	if err := db.Model(first).Updates(map[string]any{"status": enums.IMAssignmentStatusInactive, "finished_at": transferAt}).Error; err != nil {
		t.Fatalf("finish first assignment: %v", err)
	}
	transfer := &models.ConversationAssignment{
		TenantID: tenantID, ConversationID: conversation.ID, SessionNo: 1, FromUserID: 1001, ToUserID: 1002,
		AssignType: string(enums.IMAssignmentTypeTransfer), Status: enums.IMAssignmentStatusActive, CreatedAt: transferAt,
	}
	if err := db.Create(transfer).Error; err != nil {
		t.Fatalf("create transfer: %v", err)
	}
	if err := ServiceAnalyticsCaptureService.RecordCurrentAssignment(conversation.ID); err != nil {
		t.Fatalf("record transfer: %v", err)
	}
	session := repositories.ConversationServiceSessionRepository.TakeByKey(db, tenantID, conversation.ID, 1)
	if session == nil {
		t.Fatal("service session missing")
	}
	if session.FirstAssignmentID != first.ID || session.LastAssignmentID != transfer.ID {
		t.Fatalf("assignment ids first=%d last=%d", session.FirstAssignmentID, session.LastAssignmentID)
	}
	if session.AssignedAt == nil || !session.AssignedAt.Equal(firstAssignedAt) {
		t.Fatalf("assignedAt=%v want first assignment %v", session.AssignedAt, firstAssignedAt)
	}
	if session.QueueSeconds != 5 {
		t.Fatalf("queue seconds=%d want 5", session.QueueSeconds)
	}
	if session.AssignedAgentID != transfer.ToUserID || session.TransferCount != 1 || session.AssignmentCount != 2 {
		t.Fatalf("current assignment agent=%d transfers=%d assignments=%d", session.AssignedAgentID, session.TransferCount, session.AssignmentCount)
	}
}
