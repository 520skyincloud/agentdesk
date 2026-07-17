package services

import (
	"testing"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

func TestDashboardPresenceStaysActiveUntilLastUserSessionCloses(t *testing.T) {
	db := setupServiceAnalyticsTestDB(t)
	now := time.Now().Truncate(time.Second)
	operator := createDashboardPresenceFixture(t, db, 803, now)
	service := newWsService()
	first := newDashboardPresenceTestSession("presence-first", operator)
	second := newDashboardPresenceTestSession("presence-second", operator)

	if got := service.manager.Register(first, nil); got != 1 {
		t.Fatalf("register first session count=%d want=1", got)
	}
	if got := service.manager.Register(second, nil); got != 2 {
		t.Fatalf("register second session count=%d want=2", got)
	}
	service.touchDashboardPresence(first, now)
	service.touchDashboardPresence(second, now.Add(time.Second))

	if got := service.manager.CountUserSessions(operator.ActiveTenantID, operator.UserID); got != 2 {
		t.Fatalf("user session count=%d want=2", got)
	}
	requireSingleActivePresence(t, db, operator.ActiveTenantID, operator.UserID, enums.AgentPresenceStatusOnline)
	overview, err := ServiceAnalyticsService.GetOverview(ServiceAnalyticsQuery{
		StartAt:                   now.Add(-time.Hour),
		EndAt:                     now.Add(time.Minute),
		IncludeCurrentAgentRoster: true,
	}, operator)
	if err != nil {
		t.Fatalf("get overview with two sessions: %v", err)
	}
	if overview.Realtime.OnlineAgentCount != 1 || overview.Realtime.OfflineAgentCount != 0 {
		t.Fatalf("realtime counts with two sessions=%+v", overview.Realtime)
	}

	service.closeSession(first)
	if got := service.manager.CountUserSessions(operator.ActiveTenantID, operator.UserID); got != 1 {
		t.Fatalf("user session count after partial disconnect=%d want=1", got)
	}
	if repositories.AgentPresenceSessionRepository.FindActive(db, operator.ActiveTenantID, operator.UserID) == nil {
		t.Fatal("partial disconnect ended active presence")
	}

	service.closeSession(second)
	if got := service.manager.CountUserSessions(operator.ActiveTenantID, operator.UserID); got != 0 {
		t.Fatalf("user session count after final disconnect=%d want=0", got)
	}
	if active := repositories.AgentPresenceSessionRepository.FindActive(db, operator.ActiveTenantID, operator.UserID); active != nil {
		t.Fatalf("final disconnect left active presence=%+v", active)
	}

	rows := repositories.AgentPresenceSessionRepository.Find(db, sqls.NewCnd().
		Eq("tenant_id", operator.ActiveTenantID).
		Eq("user_id", operator.UserID).
		Asc("id"))
	if len(rows) != 1 || rows[0].EndedAt == nil {
		t.Fatalf("presence rows after final disconnect=%+v", rows)
	}

	service.closeSession(first)
	rows = repositories.AgentPresenceSessionRepository.Find(db, sqls.NewCnd().
		Eq("tenant_id", operator.ActiveTenantID).
		Eq("user_id", operator.UserID))
	if len(rows) != 1 {
		t.Fatalf("idempotent close created presence rows=%+v", rows)
	}
}

func TestDashboardPresenceBreakSurvivesHeartbeatsAndCanRecover(t *testing.T) {
	db := setupServiceAnalyticsTestDB(t)
	t0 := time.Now().Truncate(time.Second)
	operator := createDashboardPresenceFixture(t, db, 804, t0)
	service := newWsService()
	first := newDashboardPresenceTestSession("break-first", operator)
	second := newDashboardPresenceTestSession("break-second", operator)
	service.manager.Register(first, nil)
	service.manager.Register(second, nil)

	if err := AgentPresenceService.Touch(operator, "dashboard_ws", t0); err != nil {
		t.Fatalf("start dashboard presence: %v", err)
	}
	_, err := AgentPresenceService.SetStatus(operator, enums.AgentPresenceStatusBreak, "会议", t0.Add(time.Minute))
	if err != nil {
		t.Fatalf("set break status: %v", err)
	}
	service.touchDashboardPresence(first, t0.Add(90*time.Second))
	service.touchDashboardPresence(second, t0.Add(2*time.Minute))

	active := requireSingleActivePresence(t, db, operator.ActiveTenantID, operator.UserID, enums.AgentPresenceStatusBreak)
	if active.BreakReason != "会议" || active.LastSeenAt.Before(t0.Add(2*time.Minute)) {
		t.Fatalf("break heartbeat changed presence=%+v", active)
	}

	_, err = AgentPresenceService.SetStatus(operator, enums.AgentPresenceStatusIdle, "", t0.Add(3*time.Minute))
	if err != nil {
		t.Fatalf("recover from break: %v", err)
	}
	requireSingleActivePresence(t, db, operator.ActiveTenantID, operator.UserID, enums.AgentPresenceStatusIdle)

	overview, err := ServiceAnalyticsService.GetOverview(ServiceAnalyticsQuery{
		StartAt:                   t0.Add(-time.Hour),
		EndAt:                     t0.Add(4 * time.Minute),
		IncludeCurrentAgentRoster: true,
	}, operator)
	if err != nil {
		t.Fatalf("get overview after break recovery: %v", err)
	}
	if overview.Realtime.OnlineAgentCount != 1 || overview.Realtime.IdleAgentCount != 1 || overview.Realtime.BreakAgentCount != 0 {
		t.Fatalf("realtime counts after break recovery=%+v", overview.Realtime)
	}

	service.closeSession(first)
	if repositories.AgentPresenceSessionRepository.FindActive(db, operator.ActiveTenantID, operator.UserID) == nil {
		t.Fatal("partial disconnect ended recovered presence")
	}
	service.closeSession(second)
	if active := repositories.AgentPresenceSessionRepository.FindActive(db, operator.ActiveTenantID, operator.UserID); active != nil {
		t.Fatalf("final disconnect left recovered presence active=%+v", active)
	}

	rows := repositories.AgentPresenceSessionRepository.Find(db, sqls.NewCnd().
		Eq("tenant_id", operator.ActiveTenantID).
		Eq("user_id", operator.UserID).
		Asc("id"))
	if len(rows) != 3 || rows[0].Status != enums.AgentPresenceStatusOnline || rows[0].EndedAt == nil || rows[1].Status != enums.AgentPresenceStatusBreak || rows[1].EndedAt == nil || rows[2].Status != enums.AgentPresenceStatusIdle || rows[2].EndedAt == nil {
		t.Fatalf("break recovery rows=%+v", rows)
	}
}

func TestDashboardPresenceHeartbeatTimeoutKeepsOneRealtimeAgent(t *testing.T) {
	db := setupServiceAnalyticsTestDB(t)
	t0 := time.Now().Truncate(time.Second)
	operator := createDashboardPresenceFixture(t, db, 805, t0)
	service := newWsService()
	session := newDashboardPresenceTestSession("timeout-session", operator)
	service.manager.Register(session, nil)

	service.touchDashboardPresence(session, t0)
	service.touchDashboardPresence(session, t0.Add(time.Minute))
	reconnectedAt := t0.Add(10 * time.Minute)
	service.touchDashboardPresence(session, reconnectedAt)

	rows := repositories.AgentPresenceSessionRepository.Find(db, sqls.NewCnd().
		Eq("tenant_id", operator.ActiveTenantID).
		Eq("user_id", operator.UserID).
		Asc("id"))
	if len(rows) != 2 || rows[0].EndedAt == nil || !rows[0].EndedAt.Equal(t0.Add(time.Minute)) || rows[1].EndedAt != nil || !rows[1].StartedAt.Equal(reconnectedAt) {
		t.Fatalf("heartbeat timeout rows=%+v", rows)
	}
	requireSingleActivePresence(t, db, operator.ActiveTenantID, operator.UserID, enums.AgentPresenceStatusOnline)

	overview, err := ServiceAnalyticsService.GetOverview(ServiceAnalyticsQuery{
		StartAt:                   t0,
		EndAt:                     reconnectedAt.Add(time.Minute),
		IncludeCurrentAgentRoster: true,
	}, operator)
	if err != nil {
		t.Fatalf("get overview after heartbeat timeout: %v", err)
	}
	if overview.Realtime.OnlineAgentCount != 1 || overview.Realtime.OfflineAgentCount != 0 {
		t.Fatalf("realtime counts after heartbeat timeout=%+v", overview.Realtime)
	}

	service.closeSession(session)
}

func createDashboardPresenceFixture(t *testing.T, db *gorm.DB, tenantID int64, at time.Time) *dto.AuthPrincipal {
	t.Helper()
	team := &models.AgentTeam{
		TenantID:    tenantID,
		Name:        "Presence 测试客服组",
		Status:      enums.StatusOk,
		AuditFields: testAnalyticsAudit(at),
	}
	if err := db.Create(team).Error; err != nil {
		t.Fatalf("create presence team: %v", err)
	}
	user := &models.User{
		TenantID:    tenantID,
		Username:    "presence-ws-agent",
		Nickname:    "Presence WebSocket 客服",
		Status:      enums.StatusOk,
		AuditFields: testAnalyticsAudit(at),
	}
	if err := db.Create(user).Error; err != nil {
		t.Fatalf("create presence user: %v", err)
	}
	profile := &models.AgentProfile{
		TenantID:           tenantID,
		UserID:             user.ID,
		TeamID:             team.ID,
		AgentCode:          "PRESENCE-WS",
		DisplayName:        user.Nickname,
		MaxConcurrentCount: 5,
		Status:             enums.StatusOk,
		AuditFields:        testAnalyticsAudit(at),
	}
	if err := db.Create(profile).Error; err != nil {
		t.Fatalf("create presence profile: %v", err)
	}
	return &dto.AuthPrincipal{
		UserID:         user.ID,
		TenantID:       tenantID,
		ActiveTenantID: tenantID,
		Username:       user.Username,
		Roles:          []string{constants.RoleCodeTenantAdmin},
	}
}

func newDashboardPresenceTestSession(id string, operator *dto.AuthPrincipal) *ClientSession {
	return &ClientSession{
		ID:        id,
		TenantID:  operator.ActiveTenantID,
		Principal: operator,
		Role:      realtimeRoleAdmin,
		Topics:    make(map[string]struct{}),
		Send:      make(chan []byte, 1),
	}
}

func requireSingleActivePresence(t *testing.T, db *gorm.DB, tenantID, userID int64, status enums.AgentPresenceStatus) *models.AgentPresenceSession {
	t.Helper()
	rows := repositories.AgentPresenceSessionRepository.Find(db, sqls.NewCnd().
		Eq("tenant_id", tenantID).
		Eq("user_id", userID).
		Asc("id"))
	activeCount := 0
	var active *models.AgentPresenceSession
	for i := range rows {
		if rows[i].EndedAt == nil {
			activeCount++
			active = &rows[i]
		}
	}
	if activeCount != 1 || active == nil {
		t.Fatalf("active presence count=%d rows=%+v", activeCount, rows)
	}
	if active.Status != status {
		t.Fatalf("active presence status=%q want=%q rows=%+v", active.Status, status, rows)
	}
	return active
}
