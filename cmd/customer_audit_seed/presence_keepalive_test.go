package main

import (
	"fmt"
	"testing"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

func TestCreateSimulationPresenceSessionsStartsEveryAgentOnline(t *testing.T) {
	db := openSeedTenantTestDB(t, "initial_presence", &models.AgentProfile{}, &models.AgentPresenceSession{})
	now := time.Date(2026, time.July, 22, 10, 0, 0, 0, time.UTC)
	ctx := &seedContext{
		db: db, tenant: &models.Tenant{ID: 77}, now: now,
		leaders: []*models.User{{ID: 801}, {ID: 802}, {ID: 803}},
	}
	for index := 1; index <= expectedSimulationPresenceCount; index++ {
		user := &models.User{ID: int64(100 + index), TenantID: ctx.tenant.ID}
		ctx.agents = append(ctx.agents, user)
		profile := &models.AgentProfile{
			TenantID: ctx.tenant.ID, UserID: user.ID, TeamID: int64(900 + (index-1)/4),
			AgentCode: fmt.Sprintf("%s%03d", agentCodePrefix, index), Status: enums.StatusOk,
			AuditFields: simulationAuditFields(now),
		}
		if err := db.Create(profile).Error; err != nil {
			t.Fatalf("create agent profile %d: %v", index, err)
		}
	}

	if err := ctx.createSimulationPresenceSessions(); err != nil {
		t.Fatalf("create simulation presence: %v", err)
	}
	var sessions []models.AgentPresenceSession
	if err := db.Order("user_id ASC").Find(&sessions).Error; err != nil {
		t.Fatalf("load presence sessions: %v", err)
	}
	if len(sessions) != expectedSimulationPresenceCount {
		t.Fatalf("presence session count = %d, want %d", len(sessions), expectedSimulationPresenceCount)
	}
	for _, session := range sessions {
		if session.Status != enums.AgentPresenceStatusOnline || session.Source != simulationPresenceSource || session.BreakReason != "" {
			t.Fatalf("unexpected initial presence for user %d: %+v", session.UserID, session)
		}
		if !session.LastSeenAt.Equal(now) || session.EndedAt != nil {
			t.Fatalf("initial presence is not active and fresh for user %d: %+v", session.UserID, session)
		}
	}
}

func TestRefreshSimulationPresenceKeepsOnlyMarkedTestAgentsOnline(t *testing.T) {
	db := openSeedTenantTestDB(t, "presence_keepalive",
		&models.Tenant{}, &models.User{}, &models.AgentProfile{}, &models.AgentPresenceSession{},
	)
	previousDB := sqls.DB()
	sqls.SetDB(db)
	t.Cleanup(func() { sqls.SetDB(previousDB) })

	batch := "presence-keepalive"
	now := time.Date(2026, time.July, 22, 11, 0, 0, 0, time.UTC)
	tenant := &models.Tenant{
		TenantCode: "simulation-presence", LegalName: tenantLegalName, ShortName: tenantShortName,
		RegistrationType: tenantRegistrationType, RegistrationNo: tenantRegistrationNo,
		Status: enums.StatusOk, Remark: marker(batch) + " 仿真测试租户",
		AuditFields: simulationAuditFields(now.Add(-time.Hour)),
	}
	if err := db.Create(tenant).Error; err != nil {
		t.Fatalf("create simulation tenant: %v", err)
	}

	userIDs := make([]int64, 0, expectedSimulationPresenceCount)
	for index, username := range simulationAgentUsernames() {
		user := &models.User{
			TenantID: tenant.ID, Username: username, Nickname: fmt.Sprintf("测试客服%02d", index+1),
			Status: enums.StatusOk, Remark: marker(batch) + " 仿真测试客服",
			AuditFields: simulationAuditFields(now.Add(-time.Hour)),
		}
		if err := db.Create(user).Error; err != nil {
			t.Fatalf("create simulation user %d: %v", index+1, err)
		}
		userIDs = append(userIDs, user.ID)
		profile := &models.AgentProfile{
			TenantID: tenant.ID, UserID: user.ID, TeamID: int64(201 + index/4),
			AgentCode: fmt.Sprintf("%s%03d", agentCodePrefix, index+1), DisplayName: user.Nickname,
			Status: enums.StatusOk, AutoAssignEnabled: true, MaxConcurrentCount: 8,
			Remark: marker(batch) + " 仿真测试客服档案", AuditFields: simulationAuditFields(now.Add(-time.Hour)),
		}
		if err := db.Create(profile).Error; err != nil {
			t.Fatalf("create simulation profile %d: %v", index+1, err)
		}
		if index == expectedSimulationPresenceCount-1 {
			continue
		}
		presence := &models.AgentPresenceSession{
			TenantID: tenant.ID, UserID: user.ID, AgentProfileID: profile.ID, TeamID: profile.TeamID,
			Status: enums.AgentPresenceStatusBreak, Source: simulationPresenceSource, BreakReason: "stale break",
			StartedAt: now.Add(-2 * time.Hour), LastSeenAt: now.Add(-30 * time.Minute),
			AuditFields: simulationAuditFields(now.Add(-2 * time.Hour)),
		}
		if err := db.Create(presence).Error; err != nil {
			t.Fatalf("create stale presence %d: %v", index+1, err)
		}
	}

	firstProfileID := profileIDForUser(t, db, tenant.ID, userIDs[0])
	duplicate := &models.AgentPresenceSession{
		TenantID: tenant.ID, UserID: userIDs[0], AgentProfileID: firstProfileID, TeamID: 201,
		Status: enums.AgentPresenceStatusBusy, Source: simulationPresenceSource,
		StartedAt: now.Add(-90 * time.Minute), LastSeenAt: now.Add(-20 * time.Minute),
		AuditFields: simulationAuditFields(now.Add(-90 * time.Minute)),
	}
	if err := db.Create(duplicate).Error; err != nil {
		t.Fatalf("create duplicate simulation presence: %v", err)
	}
	secondProfileID := profileIDForUser(t, db, tenant.ID, userIDs[1])
	realSession := &models.AgentPresenceSession{
		TenantID: tenant.ID, UserID: userIDs[1], AgentProfileID: secondProfileID, TeamID: 201,
		Status: enums.AgentPresenceStatusIdle, Source: "dashboard_websocket",
		StartedAt: now.Add(-time.Hour), LastSeenAt: now.Add(-time.Minute),
		AuditFields: simulationAuditFields(now.Add(-time.Hour)),
	}
	if err := db.Create(realSession).Error; err != nil {
		t.Fatalf("create second active presence: %v", err)
	}

	unrelated := &models.User{
		TenantID: tenant.ID, Username: usernamePrefix + "observer", Nickname: "unrelated",
		Status: enums.StatusOk, Remark: marker(batch), AuditFields: simulationAuditFields(now.Add(-time.Hour)),
	}
	if err := db.Create(unrelated).Error; err != nil {
		t.Fatalf("create unrelated user: %v", err)
	}
	unrelatedProfile := &models.AgentProfile{
		TenantID: tenant.ID, UserID: unrelated.ID, TeamID: 999, AgentCode: agentCodePrefix + "observer",
		Status: enums.StatusOk, Remark: marker(batch), AuditFields: simulationAuditFields(now.Add(-time.Hour)),
	}
	if err := db.Create(unrelatedProfile).Error; err != nil {
		t.Fatalf("create unrelated profile: %v", err)
	}
	unrelatedPresence := &models.AgentPresenceSession{
		TenantID: tenant.ID, UserID: unrelated.ID, AgentProfileID: unrelatedProfile.ID, TeamID: unrelatedProfile.TeamID,
		Status: enums.AgentPresenceStatusBreak, Source: "unrelated_test",
		StartedAt: now.Add(-time.Hour), LastSeenAt: now.Add(-30 * time.Minute),
		AuditFields: simulationAuditFields(now.Add(-time.Hour)),
	}
	if err := db.Create(unrelatedPresence).Error; err != nil {
		t.Fatalf("create unrelated presence: %v", err)
	}

	for run, at := range []time.Time{now, now.Add(time.Minute)} {
		refreshed, err := refreshSimulationPresence(db, batch, at)
		if err != nil {
			t.Fatalf("refresh run %d: %v", run+1, err)
		}
		if refreshed != expectedSimulationPresenceCount {
			t.Fatalf("refresh run %d count = %d, want %d", run+1, refreshed, expectedSimulationPresenceCount)
		}
		assertSimulationAgentsOnline(t, db, tenant.ID, userIDs, at)
	}

	var endedRealSession models.AgentPresenceSession
	if err := db.First(&endedRealSession, realSession.ID).Error; err != nil {
		t.Fatalf("load duplicate real session: %v", err)
	}
	if endedRealSession.EndedAt == nil {
		t.Fatal("duplicate active real session was not closed")
	}
	var unchanged models.AgentPresenceSession
	if err := db.First(&unchanged, unrelatedPresence.ID).Error; err != nil {
		t.Fatalf("load unrelated presence: %v", err)
	}
	if unchanged.Status != enums.AgentPresenceStatusBreak || unchanged.EndedAt != nil || !unchanged.LastSeenAt.Equal(unrelatedPresence.LastSeenAt) {
		t.Fatalf("unrelated presence changed: %+v", unchanged)
	}
}

func assertSimulationAgentsOnline(t *testing.T, db *gorm.DB, tenantID int64, userIDs []int64, at time.Time) {
	t.Helper()
	var sessions []models.AgentPresenceSession
	if err := db.Where("tenant_id = ? AND user_id IN ? AND ended_at IS NULL", tenantID, userIDs).
		Order("user_id ASC, id ASC").Find(&sessions).Error; err != nil {
		t.Fatalf("load active test presence: %v", err)
	}
	if len(sessions) != expectedSimulationPresenceCount {
		t.Fatalf("active test presence count = %d, want %d", len(sessions), expectedSimulationPresenceCount)
	}
	for _, session := range sessions {
		if session.Status != enums.AgentPresenceStatusOnline || session.Source != simulationPresenceSource || session.BreakReason != "" {
			t.Fatalf("test agent %d is not kept online: %+v", session.UserID, session)
		}
		if !session.LastSeenAt.Equal(at) {
			t.Fatalf("test agent %d last seen = %s, want %s", session.UserID, session.LastSeenAt, at)
		}
	}
	var syntheticCount int64
	if err := db.Model(&models.AgentPresenceSession{}).
		Where("tenant_id = ? AND user_id IN ? AND source = ?", tenantID, userIDs, simulationPresenceSource).
		Count(&syntheticCount).Error; err != nil {
		t.Fatalf("count synthetic presence: %v", err)
	}
	if syntheticCount != expectedSimulationPresenceCount {
		t.Fatalf("synthetic presence count = %d, want %d", syntheticCount, expectedSimulationPresenceCount)
	}
}

func profileIDForUser(t *testing.T, db *gorm.DB, tenantID, userID int64) int64 {
	t.Helper()
	profile := &models.AgentProfile{}
	if err := db.Where("tenant_id = ? AND user_id = ?", tenantID, userID).Take(profile).Error; err != nil {
		t.Fatalf("load profile for user %d: %v", userID, err)
	}
	return profile.ID
}
