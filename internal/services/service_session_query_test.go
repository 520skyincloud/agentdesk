package services

import (
	"strings"
	"testing"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/enums"
)

func TestServiceSessionListAndExportShareFilters(t *testing.T) {
	db := setupServiceAnalyticsTestDB(t)
	now := time.Date(2026, 7, 17, 14, 0, 0, 0, time.Local)
	tenantID := int64(801)
	intentProfileID := int64(8801)
	if err := db.Create(&models.Tenant{
		ID: tenantID, IntentProfileID: intentProfileID,
		TenantCode: "analytics-query-tenant", LegalName: "运营分析查询测试公司", ShortName: "分析测试",
		RegistrationType: "test", RegistrationNo: "analytics-query-tenant", Status: enums.StatusOk,
	}).Error; err != nil {
		t.Fatalf("create tenant: %v", err)
	}
	channel := &models.Channel{TenantID: tenantID, Name: "官网客服", ChannelType: "web", ChannelID: "query-web", Status: enums.StatusOk, AuditFields: testAnalyticsAudit(now)}
	if err := db.Create(channel).Error; err != nil {
		t.Fatalf("create channel: %v", err)
	}
	if err := db.Create(&models.ServiceAnalyticsPolicy{
		TenantID: tenantID, QueueTargetSeconds: 60, FirstResponseTargetSeconds: 120, ResponseTargetSeconds: 180,
		RepeatConsultationHours: 24, SatisfactionThreshold: 4, EvaluationExpiryHours: 72, DefaultSampleSize: 20,
		AuditFields: testAnalyticsAudit(now),
	}).Error; err != nil {
		t.Fatalf("create policy: %v", err)
	}

	startedAt := now.Add(-time.Hour)
	assignedAt := startedAt.Add(90 * time.Second)
	matching := &models.ConversationServiceSession{
		TenantID: tenantID, ConversationID: 80101, SessionNo: 1, ChannelID: channel.ID,
		Status: enums.ServiceSessionStatusClosed, StartedAt: startedAt, QueueEnteredAt: &startedAt, AssignedAt: &assignedAt,
		QueueSeconds: 90, HumanMessageCount: 2, ResolutionCode: "resolved", CategoryCode: "booking",
		FactOrigin: enums.AnalyticsFactOriginRuntime, DataQuality: enums.AnalyticsDataQualityExact,
		AuditFields: testAnalyticsAudit(startedAt),
	}
	nonMatchingCategory := &models.ConversationServiceSession{
		TenantID: tenantID, ConversationID: 80102, SessionNo: 1, ChannelID: channel.ID,
		Status: enums.ServiceSessionStatusClosed, StartedAt: startedAt.Add(time.Minute), QueueEnteredAt: &startedAt, AssignedAt: &assignedAt,
		QueueSeconds: 90, HumanMessageCount: 1, ResolutionCode: "resolved", CategoryCode: "billing",
		FactOrigin: enums.AnalyticsFactOriginRuntime, DataQuality: enums.AnalyticsDataQualityExact,
		AuditFields: testAnalyticsAudit(startedAt),
	}
	foreign := &models.ConversationServiceSession{
		TenantID: tenantID + 1, ConversationID: 80201, SessionNo: 1, ChannelID: channel.ID,
		Status: enums.ServiceSessionStatusClosed, StartedAt: startedAt, AssignedAt: &assignedAt,
		QueueSeconds: 90, ResolutionCode: "resolved", CategoryCode: "booking",
		FactOrigin: enums.AnalyticsFactOriginRuntime, DataQuality: enums.AnalyticsDataQualityExact,
		AuditFields: testAnalyticsAudit(startedAt),
	}
	for _, item := range []*models.ConversationServiceSession{matching, nonMatchingCategory, foreign} {
		if err := db.Create(item).Error; err != nil {
			t.Fatalf("create service session: %v", err)
		}
	}

	startRange := now.Add(-2 * time.Hour)
	endRange := now
	query := ServiceSessionQuery{
		Page: 1, Limit: 20, ChannelID: channel.ID, ResolutionCode: "resolved", CategoryCode: "booking",
		StartAt: &startRange, EndAt: &endRange, SLABreached: true, SLAReferenceTime: now,
	}
	operator := &dto.AuthPrincipal{UserID: 1, ActiveTenantID: tenantID, Roles: []string{constants.RoleCodeTenantAdmin}}
	list, paging, err := ServiceAnalyticsService.ListSessions(query, operator)
	if err != nil {
		t.Fatalf("list service sessions: %v", err)
	}
	exported, err := ServiceAnalyticsService.ExportSessions(query, operator, 10000)
	if err != nil {
		t.Fatalf("export service sessions: %v", err)
	}
	if paging.Total != 1 || len(list) != 1 || list[0].ID != matching.ID {
		t.Fatalf("list result total=%d items=%+v", paging.Total, list)
	}
	if len(exported) != 1 || exported[0].ID != matching.ID {
		t.Fatalf("export result=%+v", exported)
	}
	if _, err := ServiceAnalyticsService.ExportSessions(ServiceSessionQuery{}, operator, 1); err == nil || !strings.Contains(err.Error(), "超过单次上限1条") {
		t.Fatalf("export above limit should require narrower filters, err=%v", err)
	}
}

func TestServiceSessionSLABreachedCoversQueueFirstReplyAndResponse(t *testing.T) {
	db := setupServiceAnalyticsTestDB(t)
	now := time.Date(2026, 7, 17, 15, 0, 0, 0, time.Local)
	tenantID := int64(811)
	if err := db.Create(&models.ServiceAnalyticsPolicy{
		TenantID: tenantID, QueueTargetSeconds: 60, FirstResponseTargetSeconds: 120, ResponseTargetSeconds: 180,
		RepeatConsultationHours: 24, SatisfactionThreshold: 4, EvaluationExpiryHours: 72, DefaultSampleSize: 20,
		AuditFields: testAnalyticsAudit(now),
	}).Error; err != nil {
		t.Fatalf("create policy: %v", err)
	}

	createSession := func(conversationID int64, status enums.ServiceSessionStatus) *models.ConversationServiceSession {
		t.Helper()
		item := &models.ConversationServiceSession{
			TenantID: tenantID, ConversationID: conversationID, SessionNo: 1, Status: status, StartedAt: now.Add(-time.Hour),
			FactOrigin: enums.AnalyticsFactOriginRuntime, DataQuality: enums.AnalyticsDataQualityExact, AuditFields: testAnalyticsAudit(now),
		}
		if err := db.Create(item).Error; err != nil {
			t.Fatalf("create service session: %v", err)
		}
		return item
	}
	queue := createSession(81101, enums.ServiceSessionStatusClosed)
	queueEnteredAt := now.Add(-10 * time.Minute)
	assignedAt := queueEnteredAt.Add(90 * time.Second)
	if err := db.Model(queue).Updates(map[string]any{"queue_entered_at": queueEnteredAt, "assigned_at": assignedAt, "queue_seconds": 90}).Error; err != nil {
		t.Fatalf("update queue session: %v", err)
	}
	firstReply := createSession(81102, enums.ServiceSessionStatusClosed)
	firstAssignedAt := now.Add(-10 * time.Minute)
	firstHumanReplyAt := firstAssignedAt.Add(150 * time.Second)
	if err := db.Model(firstReply).Updates(map[string]any{"assigned_at": firstAssignedAt, "first_human_reply_at": firstHumanReplyAt, "first_response_seconds": 150}).Error; err != nil {
		t.Fatalf("update first reply session: %v", err)
	}
	repliedResponse := createSession(81103, enums.ServiceSessionStatusClosed)
	repliedAt := now.Add(-time.Minute)
	waitingResponse := createSession(81104, enums.ServiceSessionStatusOpen)
	notBreached := createSession(81105, enums.ServiceSessionStatusClosed)
	spans := []*models.ConversationResponseSpan{
		{TenantID: tenantID, ConversationID: repliedResponse.ConversationID, SessionNo: 1, CustomerStartMessageID: 1, StartedAt: now.Add(-10 * time.Minute), RepliedAt: &repliedAt, WaitSeconds: 240, Status: enums.ResponseSpanStatusReplied, FactOrigin: enums.AnalyticsFactOriginRuntime, DataQuality: enums.AnalyticsDataQualityExact, AuditFields: testAnalyticsAudit(now)},
		{TenantID: tenantID, ConversationID: waitingResponse.ConversationID, SessionNo: 1, CustomerStartMessageID: 2, StartedAt: now.Add(-4 * time.Minute), Status: enums.ResponseSpanStatusWaiting, FactOrigin: enums.AnalyticsFactOriginRuntime, DataQuality: enums.AnalyticsDataQualityExact, AuditFields: testAnalyticsAudit(now)},
		{TenantID: tenantID, ConversationID: notBreached.ConversationID, SessionNo: 1, CustomerStartMessageID: 3, StartedAt: now.Add(-time.Minute), RepliedAt: &repliedAt, WaitSeconds: 60, Status: enums.ResponseSpanStatusReplied, FactOrigin: enums.AnalyticsFactOriginRuntime, DataQuality: enums.AnalyticsDataQualityExact, AuditFields: testAnalyticsAudit(now)},
	}
	for _, span := range spans {
		if err := db.Create(span).Error; err != nil {
			t.Fatalf("create response span: %v", err)
		}
	}
	startRange := now.Add(-2 * time.Hour)
	endRange := now
	operator := &dto.AuthPrincipal{UserID: 1, ActiveTenantID: tenantID, Roles: []string{constants.RoleCodeTenantAdmin}}
	list, paging, err := ServiceAnalyticsService.ListSessions(ServiceSessionQuery{
		Page: 1, Limit: 20, StartAt: &startRange, EndAt: &endRange, SLABreached: true, SLAReferenceTime: now,
	}, operator)
	if err != nil {
		t.Fatalf("list breached sessions: %v", err)
	}
	if paging.Total != 4 || len(list) != 4 {
		t.Fatalf("breached total=%d sessions=%+v", paging.Total, list)
	}
	seen := map[int64]bool{}
	for _, item := range list {
		seen[item.ConversationID] = true
	}
	for _, conversationID := range []int64{queue.ConversationID, firstReply.ConversationID, repliedResponse.ConversationID, waitingResponse.ConversationID} {
		if !seen[conversationID] {
			t.Fatalf("missing SLA breach conversation %d in %+v", conversationID, seen)
		}
	}
	if seen[notBreached.ConversationID] {
		t.Fatalf("non-breached conversation %d was included", notBreached.ConversationID)
	}
}
