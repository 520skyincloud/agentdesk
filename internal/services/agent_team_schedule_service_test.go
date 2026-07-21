package services_test

import (
	"slices"
	"strings"
	"testing"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/services"

	"github.com/glebarez/sqlite"
	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

func TestAgentTeamScheduleServiceFindCalendarSchedulesReturnsIntersectingSchedules(t *testing.T) {
	db := setupAgentTeamScheduleTestDB(t)
	createAgentTeamScheduleTestData(t, db)

	list, err := services.AgentTeamScheduleService.FindCalendarSchedules(request.AgentTeamScheduleCalendarRequest{
		StartAt: "2026-04-27 00:00:00",
		EndAt:   "2026-05-04 00:00:00",
	})
	if err != nil {
		t.Fatalf("FindCalendarSchedules() error = %v", err)
	}

	if len(list) != 3 {
		t.Fatalf("expected 3 intersecting schedules, got %d: %+v", len(list), list)
	}
	gotIDs := make([]int64, 0, len(list))
	for _, item := range list {
		gotIDs = append(gotIDs, item.ID)
	}
	wantIDs := []int64{1, 2, 3}
	for i, want := range wantIDs {
		if gotIDs[i] != want {
			t.Fatalf("expected ids %v, got %v", wantIDs, gotIDs)
		}
	}
}

func TestAgentTeamScheduleServiceFindCalendarSchedulesFiltersTeamID(t *testing.T) {
	db := setupAgentTeamScheduleTestDB(t)
	createAgentTeamScheduleTestData(t, db)

	list, err := services.AgentTeamScheduleService.FindCalendarSchedules(request.AgentTeamScheduleCalendarRequest{
		StartAt: "2026-04-27 00:00:00",
		EndAt:   "2026-05-04 00:00:00",
		TeamID:  2,
	})
	if err != nil {
		t.Fatalf("FindCalendarSchedules() error = %v", err)
	}

	if len(list) != 1 {
		t.Fatalf("expected 1 schedule for team 2, got %d: %+v", len(list), list)
	}
	if list[0].ID != 3 || list[0].TeamID != 2 {
		t.Fatalf("unexpected schedule: %+v", list[0])
	}
}

func TestAgentTeamScheduleServiceFindCalendarSchedulesValidatesTimeRange(t *testing.T) {
	setupAgentTeamScheduleTestDB(t)

	_, err := services.AgentTeamScheduleService.FindCalendarSchedules(request.AgentTeamScheduleCalendarRequest{
		StartAt: "2026-05-04 00:00:00",
		EndAt:   "2026-04-27 00:00:00",
	})
	if err == nil {
		t.Fatalf("expected invalid time range to fail")
	}
}

func TestAgentTeamScheduleServiceCreateAllowsOvernightSchedule(t *testing.T) {
	db := setupAgentTeamScheduleTestDB(t)
	createAgentTeamScheduleTestTeams(t, db)

	tomorrow := time.Now().AddDate(0, 0, 1)
	created, err := services.AgentTeamScheduleService.CreateAgentTeamSchedule(request.CreateAgentTeamScheduleRequest{
		TeamID:  1,
		StartAt: formatTestDateTime(tomorrow, "22:00:00"),
		EndAt:   formatTestDateTime(tomorrow.AddDate(0, 0, 1), "08:00:00"),
	}, testOperator())
	if err != nil {
		t.Fatalf("expected overnight schedule to succeed, got %v", err)
	}
	if created == nil || created.EndAt.Sub(created.StartAt) != 10*time.Hour {
		t.Fatalf("unexpected overnight schedule: %+v", created)
	}
}

func TestAgentTeamScheduleServiceCreateRejectsHistoricalScheduleByDay(t *testing.T) {
	setupAgentTeamScheduleTestDB(t)
	createAgentTeamScheduleTestTeams(t, sqls.DB())

	yesterday := time.Now().AddDate(0, 0, -1)
	_, err := services.AgentTeamScheduleService.CreateAgentTeamSchedule(request.CreateAgentTeamScheduleRequest{
		TeamID:  1,
		StartAt: formatTestDateTime(yesterday, "09:00:00"),
		EndAt:   formatTestDateTime(yesterday, "18:00:00"),
	}, testOperator())
	if err == nil {
		t.Fatalf("expected historical schedule to fail")
	}
	if !strings.Contains(err.Error(), "历史日期") {
		t.Fatalf("expected historical date error, got %v", err)
	}
}

func TestAgentTeamScheduleServiceCreateAllowsTodayEarlierThanCurrentTime(t *testing.T) {
	setupAgentTeamScheduleTestDB(t)
	createAgentTeamScheduleTestTeams(t, sqls.DB())

	today := time.Now()
	item, err := services.AgentTeamScheduleService.CreateAgentTeamSchedule(request.CreateAgentTeamScheduleRequest{
		TeamID:  1,
		StartAt: formatTestDateTime(today, "00:00:00"),
		EndAt:   formatTestDateTime(today, "01:00:00"),
	}, testOperator())
	if err != nil {
		t.Fatalf("expected today's schedule to pass, got %v", err)
	}
	if item == nil || item.ID == 0 {
		t.Fatalf("expected created schedule, got %+v", item)
	}
	if item.TenantID != 101 {
		t.Fatalf("schedule tenant = %d, want 101", item.TenantID)
	}
}

func TestAgentTeamScheduleServiceCreateSupportsSquadAndRejectsCrossTeam(t *testing.T) {
	db := setupAgentTeamScheduleTestDB(t)
	createAgentTeamScheduleTestTeams(t, db)
	squad := &models.AgentTeamSquad{TenantID: 101, TeamID: 1, Name: "白班一组", Status: enums.StatusOk}
	otherSquad := &models.AgentTeamSquad{TenantID: 101, TeamID: 2, Name: "晚班二组", Status: enums.StatusOk}
	if err := db.Create(squad).Error; err != nil {
		t.Fatalf("create squad: %v", err)
	}
	if err := db.Create(otherSquad).Error; err != nil {
		t.Fatalf("create other squad: %v", err)
	}
	if err := db.Create(&models.AgentTeamSquadMember{
		TenantID: 101, SquadID: squad.ID, AgentProfileID: 9201, Status: enums.StatusOk,
	}).Error; err != nil {
		t.Fatalf("create squad member: %v", err)
	}
	tomorrow := time.Now().AddDate(0, 0, 1)
	item, err := services.AgentTeamScheduleService.CreateAgentTeamSchedule(request.CreateAgentTeamScheduleRequest{
		TeamID: 1, SquadID: squad.ID, StartAt: formatTestDateTime(tomorrow, "09:00:00"), EndAt: formatTestDateTime(tomorrow, "18:00:00"),
	}, testOperator())
	if err != nil {
		t.Fatalf("create squad schedule: %v", err)
	}
	if item.SquadID != squad.ID {
		t.Fatalf("schedule squad = %d, want %d", item.SquadID, squad.ID)
	}
	_, err = services.AgentTeamScheduleService.CreateAgentTeamSchedule(request.CreateAgentTeamScheduleRequest{
		TeamID: 1, SquadID: otherSquad.ID, StartAt: formatTestDateTime(tomorrow, "19:00:00"), EndAt: formatTestDateTime(tomorrow, "20:00:00"),
	}, testOperator())
	if err == nil || !strings.Contains(err.Error(), "不属于") {
		t.Fatalf("expected cross-team squad error, got %v", err)
	}
}

func TestAgentTeamScheduleServiceAllowsOnlyDifferentSquadsToOverlap(t *testing.T) {
	db := setupAgentTeamScheduleTestDB(t)
	createAgentTeamScheduleTestTeams(t, db)
	secondUser := &models.User{ID: 9103, TenantID: 101, Username: "schedule-team-1-relief", Status: enums.StatusOk}
	if err := db.Create(secondUser).Error; err != nil {
		t.Fatalf("create second team agent: %v", err)
	}
	grantScheduleReplyPermissions(t, db, secondUser.ID)
	secondProfile := &models.AgentProfile{ID: 9203, TenantID: 101, UserID: secondUser.ID, TeamID: 1, AgentCode: "schedule-team-1-relief", AutoAssignEnabled: true, MaxConcurrentCount: 5, Status: enums.StatusOk}
	if err := db.Create(secondProfile).Error; err != nil {
		t.Fatalf("create second team profile: %v", err)
	}
	squads := []models.AgentTeamSquad{
		{TenantID: 101, TeamID: 1, Name: "白班交接组", Status: enums.StatusOk},
		{TenantID: 101, TeamID: 1, Name: "晚班交接组", Status: enums.StatusOk},
	}
	if err := db.Create(&squads).Error; err != nil {
		t.Fatalf("create handover squads: %v", err)
	}
	members := []models.AgentTeamSquadMember{
		{TenantID: 101, SquadID: squads[0].ID, AgentProfileID: 9201, Status: enums.StatusOk},
		{TenantID: 101, SquadID: squads[1].ID, AgentProfileID: secondProfile.ID, Status: enums.StatusOk},
	}
	if err := db.Create(&members).Error; err != nil {
		t.Fatalf("create handover squad members: %v", err)
	}
	tomorrow := time.Now().AddDate(0, 0, 1)
	first, err := services.AgentTeamScheduleService.CreateAgentTeamSchedule(request.CreateAgentTeamScheduleRequest{
		TeamID: 1, SquadID: squads[0].ID,
		StartAt: formatTestDateTime(tomorrow, "09:00:00"), EndAt: formatTestDateTime(tomorrow, "18:00:00"),
	}, testOperator())
	if err != nil || first == nil {
		t.Fatalf("create outgoing squad schedule: item=%+v err=%v", first, err)
	}
	second, err := services.AgentTeamScheduleService.CreateAgentTeamSchedule(request.CreateAgentTeamScheduleRequest{
		TeamID: 1, SquadID: squads[1].ID,
		StartAt: formatTestDateTime(tomorrow, "17:30:00"), EndAt: formatTestDateTime(tomorrow, "23:00:00"),
	}, testOperator())
	if err != nil || second == nil {
		t.Fatalf("different squads should overlap for handover: item=%+v err=%v", second, err)
	}
	if _, err := services.AgentTeamScheduleService.CreateAgentTeamSchedule(request.CreateAgentTeamScheduleRequest{
		TeamID: 1, SquadID: squads[0].ID,
		StartAt: formatTestDateTime(tomorrow, "17:00:00"), EndAt: formatTestDateTime(tomorrow, "19:00:00"),
	}, testOperator()); err == nil || !strings.Contains(err.Error(), "不能") {
		t.Fatalf("same squad overlap must fail, got %v", err)
	}
	if _, err := services.AgentTeamScheduleService.CreateAgentTeamSchedule(request.CreateAgentTeamScheduleRequest{
		TeamID:  1,
		StartAt: formatTestDateTime(tomorrow, "17:00:00"), EndAt: formatTestDateTime(tomorrow, "19:00:00"),
	}, testOperator()); err == nil || !strings.Contains(err.Error(), "不能") {
		t.Fatalf("full-team overlap must fail, got %v", err)
	}
}

func TestAgentTeamScheduleServiceBatchSquadRequiresSingleTeam(t *testing.T) {
	db := setupAgentTeamScheduleTestDB(t)
	createAgentTeamScheduleTestTeams(t, db)
	squad := &models.AgentTeamSquad{TenantID: 101, TeamID: 1, Name: "批量排班小组", Status: enums.StatusOk}
	if err := db.Create(squad).Error; err != nil {
		t.Fatalf("create squad: %v", err)
	}
	tomorrow := time.Now().AddDate(0, 0, 1)
	req := request.AgentTeamScheduleBatchRequest{
		TeamIDs: []int64{1, 2}, SquadID: squad.ID,
		StartDate: tomorrow.Format(time.DateOnly), EndDate: tomorrow.Format(time.DateOnly),
		Weekdays: []int{weekdayForRequest(tomorrow)}, StartTime: "09:00", EndTime: "18:00",
	}
	if _, err := services.AgentTeamScheduleService.BatchPreview(req, testOperator()); err == nil {
		t.Fatal("expected multi-team squad batch error")
	}
	req.TeamIDs = []int64{1}
	preview, err := services.AgentTeamScheduleService.BatchPreview(req, testOperator())
	if err != nil {
		t.Fatalf("preview squad batch: %v", err)
	}
	if len(preview.Items) != 1 || preview.Items[0].SquadID != squad.ID || preview.Items[0].SquadName != squad.Name {
		t.Fatalf("squad preview = %+v", preview.Items)
	}
}

func TestAgentTeamScheduleServiceBatchPreviewAppliesShiftOverridesAndCoverage(t *testing.T) {
	db := setupAgentTeamScheduleTestDB(t)
	createAgentTeamScheduleTestTeams(t, db)
	users := []models.User{
		{ID: 101, TenantID: 101, Username: "day-agent", Status: enums.StatusOk},
		{ID: 102, TenantID: 101, Username: "relief-agent", Status: enums.StatusOk},
	}
	if err := db.Create(&users).Error; err != nil {
		t.Fatalf("create schedule users: %v", err)
	}
	grantScheduleReplyPermissions(t, db, 101, 102)
	profiles := []models.AgentProfile{
		{ID: 11, TenantID: 101, UserID: 101, TeamID: 1, AgentCode: "schedule-day", Status: enums.StatusOk, AutoAssignEnabled: true, MaxConcurrentCount: 5},
		{ID: 12, TenantID: 101, UserID: 102, TeamID: 1, AgentCode: "schedule-relief", Status: enums.StatusOk, AutoAssignEnabled: true, MaxConcurrentCount: 3},
	}
	if err := db.Create(&profiles).Error; err != nil {
		t.Fatalf("create schedule profiles: %v", err)
	}
	squad := models.AgentTeamSquad{TenantID: 101, TeamID: 1, Name: "白班", Status: enums.StatusOk}
	if err := db.Create(&squad).Error; err != nil {
		t.Fatalf("create schedule squad: %v", err)
	}
	if err := db.Create(&models.AgentTeamSquadMember{
		TenantID: 101, SquadID: squad.ID, AgentProfileID: 11, Status: enums.StatusOk,
	}).Error; err != nil {
		t.Fatalf("create schedule squad member: %v", err)
	}
	tomorrow := time.Now().AddDate(0, 0, 1)
	req := request.AgentTeamScheduleBatchRequest{
		TeamIDs:                 []int64{1},
		SquadID:                 squad.ID,
		IncludedAgentProfileIDs: []int64{12},
		ExcludedAgentProfileIDs: []int64{11},
		StartDate:               tomorrow.Format(time.DateOnly),
		EndDate:                 tomorrow.Format(time.DateOnly),
		Weekdays:                []int{weekdayForRequest(tomorrow)},
		StartTime:               "09:00",
		EndTime:                 "18:00",
	}
	preview, err := services.AgentTeamScheduleService.BatchPreview(req, testOperator())
	if err != nil {
		t.Fatalf("preview schedule overrides: %v", err)
	}
	if len(preview.Items) != 1 || preview.Items[0].EligibleAgentCount != 1 || preview.Items[0].TotalCapacity != 3 {
		t.Fatalf("unexpected coverage after shift overrides: %+v", preview.Items)
	}
	if !strings.Contains(preview.Items[0].CoverageWarning, "单点") {
		t.Fatalf("expected single-agent coverage warning, got %+v", preview.Items[0])
	}
	result, err := services.AgentTeamScheduleService.BatchGenerate(req, testOperator())
	if err != nil || result.Created != 1 {
		t.Fatalf("generate schedule overrides result=%+v err=%v", result, err)
	}
	var stored models.AgentTeamSchedule
	if err := db.Where("team_id = ?", 1).Order("id DESC").Take(&stored).Error; err != nil {
		t.Fatalf("load generated override schedule: %v", err)
	}
	if stored.IncludedAgentProfileIDs != "12" || stored.ExcludedAgentProfileIDs != "11" {
		t.Fatalf("stored schedule overrides = include %q exclude %q", stored.IncludedAgentProfileIDs, stored.ExcludedAgentProfileIDs)
	}
}

func TestAgentTeamScheduleCoverageFollowsDispatchMode(t *testing.T) {
	db := setupAgentTeamScheduleTestDB(t)
	createAgentTeamScheduleTestTeams(t, db)
	if err := db.Model(&models.AgentProfile{}).Where("team_id = ?", 1).Updates(map[string]any{
		"auto_assign_enabled":  false,
		"max_concurrent_count": 0,
	}).Error; err != nil {
		t.Fatalf("disable automatic assignment capacity: %v", err)
	}
	if err := db.Model(&models.AgentTeam{}).Where("id = ?", 1).Update("dispatch_mode", enums.AgentTeamDispatchModeManual).Error; err != nil {
		t.Fatalf("set manual dispatch mode: %v", err)
	}
	tomorrow := time.Now().AddDate(0, 0, 1)
	req := request.AgentTeamScheduleBatchRequest{
		TeamIDs: []int64{1}, StartDate: tomorrow.Format(time.DateOnly), EndDate: tomorrow.Format(time.DateOnly),
		Weekdays: []int{weekdayForRequest(tomorrow)}, StartTime: "09:00", EndTime: "18:00",
	}
	preview, err := services.AgentTeamScheduleService.BatchPreview(req, testOperator())
	if err != nil || len(preview.Items) != 1 || preview.Items[0].EligibleAgentCount != 1 || preview.Items[0].TotalCapacity != 0 {
		t.Fatalf("manual coverage=%+v err=%v", preview, err)
	}
	if !strings.Contains(preview.Items[0].CoverageWarning, "人工接待") {
		t.Fatalf("manual warning=%q", preview.Items[0].CoverageWarning)
	}
	if err := db.Model(&models.AgentTeam{}).Where("id = ?", 1).Update("dispatch_mode", enums.AgentTeamDispatchModeRule).Error; err != nil {
		t.Fatalf("set rule dispatch mode: %v", err)
	}
	preview, err = services.AgentTeamScheduleService.BatchPreview(req, testOperator())
	if err != nil || len(preview.Items) != 1 || preview.Items[0].EligibleAgentCount != 0 {
		t.Fatalf("rule coverage=%+v err=%v", preview, err)
	}
	if !strings.Contains(preview.Items[0].CoverageWarning, "可自动接单") {
		t.Fatalf("rule warning=%q", preview.Items[0].CoverageWarning)
	}
}

func TestAgentTeamScheduleCoverageRejectsAgentWithoutReplyPermission(t *testing.T) {
	db := setupAgentTeamScheduleTestDB(t)
	createAgentTeamScheduleTestTeams(t, db)
	sendPermission := models.Permission{}
	if err := db.Where("code = ?", constants.PermissionConversationSend.Code).Take(&sendPermission).Error; err != nil {
		t.Fatalf("load send permission: %v", err)
	}
	if err := db.Where("permission_id = ?", sendPermission.ID).Delete(&models.RolePermission{}).Error; err != nil {
		t.Fatalf("remove send permission: %v", err)
	}

	tomorrow := time.Now().AddDate(0, 0, 1)
	preview, err := services.AgentTeamScheduleService.BatchPreview(request.AgentTeamScheduleBatchRequest{
		TeamIDs: []int64{1}, StartDate: tomorrow.Format(time.DateOnly), EndDate: tomorrow.Format(time.DateOnly),
		Weekdays: []int{weekdayForRequest(tomorrow)}, StartTime: "09:00", EndTime: "18:00",
	}, testOperator())
	if err != nil || len(preview.Items) != 1 || preview.Items[0].EligibleAgentCount != 0 {
		t.Fatalf("permission-gated coverage=%+v err=%v", preview, err)
	}
	if !strings.Contains(preview.Items[0].CoverageWarning, "回复权限") {
		t.Fatalf("permission warning=%q", preview.Items[0].CoverageWarning)
	}
}

func TestAgentTeamScheduleServiceUpdateAllowsOvernightSchedule(t *testing.T) {
	db := setupAgentTeamScheduleTestDB(t)
	createAgentTeamScheduleTestTeams(t, db)
	existingID := createFutureAgentTeamSchedule(t, db)
	tomorrow := time.Now().AddDate(0, 0, 1)

	err := services.AgentTeamScheduleService.UpdateAgentTeamSchedule(request.UpdateAgentTeamScheduleRequest{
		ID: existingID,
		CreateAgentTeamScheduleRequest: request.CreateAgentTeamScheduleRequest{
			TeamID:  1,
			StartAt: formatTestDateTime(tomorrow, "22:00:00"),
			EndAt:   formatTestDateTime(tomorrow.AddDate(0, 0, 1), "08:00:00"),
		},
	}, testOperator())
	if err != nil {
		t.Fatalf("expected overnight update to succeed, got %v", err)
	}
	updated := services.AgentTeamScheduleService.Get(existingID)
	if updated == nil || updated.EndAt.Sub(updated.StartAt) != 10*time.Hour {
		t.Fatalf("unexpected updated overnight schedule: %+v", updated)
	}
}

func TestAgentTeamScheduleServiceUpdateRejectsHistoricalScheduleByDay(t *testing.T) {
	db := setupAgentTeamScheduleTestDB(t)
	createAgentTeamScheduleTestTeams(t, db)
	existingID := createFutureAgentTeamSchedule(t, db)
	yesterday := time.Now().AddDate(0, 0, -1)

	err := services.AgentTeamScheduleService.UpdateAgentTeamSchedule(request.UpdateAgentTeamScheduleRequest{
		ID: existingID,
		CreateAgentTeamScheduleRequest: request.CreateAgentTeamScheduleRequest{
			TeamID:  1,
			StartAt: formatTestDateTime(yesterday, "09:00:00"),
			EndAt:   formatTestDateTime(yesterday, "18:00:00"),
		},
	}, testOperator())
	if err == nil {
		t.Fatalf("expected historical update to fail")
	}
	if !strings.Contains(err.Error(), "历史日期") {
		t.Fatalf("expected historical date error, got %v", err)
	}
}

func TestAgentTeamScheduleServiceBatchPreviewExpandsSharedRule(t *testing.T) {
	setupAgentTeamScheduleTestDB(t)
	createAgentTeamScheduleTestTeams(t, sqls.DB())
	nextMonday := nextTestWeekday(time.Monday)
	nextWednesday := nextMonday.AddDate(0, 0, 2)

	preview, err := services.AgentTeamScheduleService.BatchPreview(request.AgentTeamScheduleBatchRequest{
		TeamIDs:   []int64{1, 2},
		StartDate: nextMonday.Format(time.DateOnly),
		EndDate:   nextMonday.AddDate(0, 0, 6).Format(time.DateOnly),
		Weekdays:  []int{1, 3},
		StartTime: "09:00",
		EndTime:   "18:00",
		Remark:    "工作日白班",
	}, testOperator())
	if err != nil {
		t.Fatalf("BatchPreview() error = %v", err)
	}
	if preview.Total != 4 || len(preview.Items) != 4 {
		t.Fatalf("expected 4 preview items, got total=%d len=%d", preview.Total, len(preview.Items))
	}
	if preview.Conflict {
		t.Fatalf("expected no conflict, got %+v", preview.Items)
	}
	teamNames := make(map[int64]string)
	for _, item := range preview.Items {
		if item.TeamName == "" {
			t.Fatalf("expected all preview items to have team names: %+v", preview.Items)
		}
		teamNames[item.TeamID] = item.TeamName
	}
	if teamNames[1] == "" || teamNames[2] == "" {
		t.Fatalf("expected team ids 1 and 2 with names, got %v", teamNames)
	}
	type previewKey struct {
		teamID int64
		date   string
	}
	itemsByKey := make(map[previewKey]services.AgentTeamScheduleBatchPreviewItem)
	for _, item := range preview.Items {
		itemsByKey[previewKey{teamID: item.TeamID, date: item.Date.Format(time.DateOnly)}] = item
	}
	expected := []struct {
		teamID  int64
		date    time.Time
		weekday int
	}{
		{teamID: 1, date: nextMonday, weekday: 1},
		{teamID: 1, date: nextWednesday, weekday: 3},
		{teamID: 2, date: nextMonday, weekday: 1},
		{teamID: 2, date: nextWednesday, weekday: 3},
	}
	for _, want := range expected {
		wantDate := want.date.Format(time.DateOnly)
		item, ok := itemsByKey[previewKey{teamID: want.teamID, date: wantDate}]
		if !ok {
			t.Fatalf("expected preview item for teamID=%d date=%s, got %+v", want.teamID, wantDate, preview.Items)
		}
		if item.Weekday != want.weekday ||
			item.StartAt.Format(time.DateTime) != formatTestDateTime(want.date, "09:00:00") ||
			item.EndAt.Format(time.DateTime) != formatTestDateTime(want.date, "18:00:00") ||
			item.Remark != "工作日白班" {
			t.Fatalf("unexpected preview item for teamID=%d date=%s: %+v", want.teamID, wantDate, item)
		}
	}
}

func TestAgentTeamScheduleServiceBatchPreviewRejectsHistoricalDate(t *testing.T) {
	setupAgentTeamScheduleTestDB(t)
	createAgentTeamScheduleTestTeams(t, sqls.DB())
	yesterday := time.Now().AddDate(0, 0, -1)

	_, err := services.AgentTeamScheduleService.BatchPreview(request.AgentTeamScheduleBatchRequest{
		TeamIDs:   []int64{1},
		StartDate: yesterday.Format(time.DateOnly),
		EndDate:   yesterday.Format(time.DateOnly),
		Weekdays:  []int{weekdayForRequest(yesterday)},
		StartTime: "09:00",
		EndTime:   "18:00",
	}, testOperator())
	if err == nil {
		t.Fatalf("expected historical batch preview to fail")
	}
	if !strings.Contains(err.Error(), "历史日期") {
		t.Fatalf("expected historical date error, got %v", err)
	}
}

func TestAgentTeamScheduleServiceBatchPreviewSupportsOvernightTimeRange(t *testing.T) {
	setupAgentTeamScheduleTestDB(t)
	createAgentTeamScheduleTestTeams(t, sqls.DB())
	tomorrow := time.Now().AddDate(0, 0, 1)

	preview, err := services.AgentTeamScheduleService.BatchPreview(request.AgentTeamScheduleBatchRequest{
		TeamIDs:   []int64{1},
		StartDate: tomorrow.Format(time.DateOnly),
		EndDate:   tomorrow.Format(time.DateOnly),
		Weekdays:  []int{weekdayForRequest(tomorrow)},
		StartTime: "18:00",
		EndTime:   "09:00",
	}, testOperator())
	if err != nil {
		t.Fatalf("expected overnight batch preview to succeed, got %v", err)
	}
	if len(preview.Items) != 1 || preview.Items[0].EndAt.Sub(preview.Items[0].StartAt) != 15*time.Hour {
		t.Fatalf("unexpected overnight batch preview: %+v", preview)
	}
}

func TestAgentTeamScheduleServiceBatchPreviewExpandsMultipleTimeRanges(t *testing.T) {
	setupAgentTeamScheduleTestDB(t)
	createAgentTeamScheduleTestTeams(t, sqls.DB())
	tomorrow := time.Now().AddDate(0, 0, 1)

	preview, err := services.AgentTeamScheduleService.BatchPreview(request.AgentTeamScheduleBatchRequest{
		TeamIDs:   []int64{1},
		StartDate: tomorrow.Format(time.DateOnly),
		EndDate:   tomorrow.Format(time.DateOnly),
		Weekdays:  []int{weekdayForRequest(tomorrow)},
		TimeRanges: []request.AgentTeamScheduleTimeRange{
			{StartTime: "08:00", EndTime: "12:00"},
			{StartTime: "14:00", EndTime: "18:00"},
		},
	}, testOperator())
	if err != nil {
		t.Fatalf("preview multiple time ranges: %v", err)
	}
	if preview.Total != 2 || preview.Conflict {
		t.Fatalf("unexpected multiple range preview: %+v", preview)
	}
	if preview.Items[0].StartAt.Hour() != 8 || preview.Items[1].StartAt.Hour() != 14 {
		t.Fatalf("unexpected expanded ranges: %+v", preview.Items)
	}
}

func TestAgentTeamScheduleServiceBatchPreviewRejectsDuplicateTimeRanges(t *testing.T) {
	setupAgentTeamScheduleTestDB(t)
	createAgentTeamScheduleTestTeams(t, sqls.DB())
	tomorrow := time.Now().AddDate(0, 0, 1)

	_, err := services.AgentTeamScheduleService.BatchPreview(request.AgentTeamScheduleBatchRequest{
		TeamIDs:   []int64{1},
		StartDate: tomorrow.Format(time.DateOnly),
		EndDate:   tomorrow.Format(time.DateOnly),
		Weekdays:  []int{weekdayForRequest(tomorrow)},
		TimeRanges: []request.AgentTeamScheduleTimeRange{
			{StartTime: "09:00", EndTime: "18:00"},
			{StartTime: "09:00:00", EndTime: "18:00:00"},
		},
	}, testOperator())
	if err == nil || !strings.Contains(err.Error(), "重复") {
		t.Fatalf("expected duplicate time range error, got %v", err)
	}
}

func TestAgentTeamScheduleServiceBatchPreviewMarksCandidateOverlap(t *testing.T) {
	setupAgentTeamScheduleTestDB(t)
	createAgentTeamScheduleTestTeams(t, sqls.DB())
	tomorrow := time.Now().AddDate(0, 0, 1)

	preview, err := services.AgentTeamScheduleService.BatchPreview(request.AgentTeamScheduleBatchRequest{
		TeamIDs:   []int64{1},
		StartDate: tomorrow.Format(time.DateOnly),
		EndDate:   tomorrow.Format(time.DateOnly),
		Weekdays:  []int{weekdayForRequest(tomorrow)},
		TimeRanges: []request.AgentTeamScheduleTimeRange{
			{StartTime: "22:00", EndTime: "08:00"},
			{StartTime: "23:30", EndTime: "01:00"},
		},
	}, testOperator())
	if err != nil {
		t.Fatalf("preview overlapping candidate ranges: %v", err)
	}
	if !preview.Conflict || len(preview.Items) != 2 || !preview.Items[0].Conflict || !preview.Items[1].Conflict {
		t.Fatalf("expected both candidate ranges to be marked as conflicting: %+v", preview)
	}
	if !strings.Contains(preview.Items[0].ConflictReason, "待生成") || !strings.Contains(preview.Items[1].ConflictReason, "待生成") {
		t.Fatalf("expected candidate conflict reasons, got %+v", preview.Items)
	}

	result, err := services.AgentTeamScheduleService.BatchGenerate(request.AgentTeamScheduleBatchRequest{
		TeamIDs:   []int64{1},
		StartDate: tomorrow.Format(time.DateOnly),
		EndDate:   tomorrow.Format(time.DateOnly),
		Weekdays:  []int{weekdayForRequest(tomorrow)},
		TimeRanges: []request.AgentTeamScheduleTimeRange{
			{StartTime: "22:00", EndTime: "08:00"},
			{StartTime: "23:30", EndTime: "01:00"},
		},
	}, testOperator())
	if err == nil || result != nil {
		t.Fatalf("expected overlapping candidate generation to fail, result=%+v err=%v", result, err)
	}
}

func TestAgentTeamScheduleServiceBatchPreviewRejectsEqualStartAndEndTime(t *testing.T) {
	setupAgentTeamScheduleTestDB(t)
	createAgentTeamScheduleTestTeams(t, sqls.DB())
	tomorrow := time.Now().AddDate(0, 0, 1)

	_, err := services.AgentTeamScheduleService.BatchPreview(request.AgentTeamScheduleBatchRequest{
		TeamIDs:   []int64{1},
		StartDate: tomorrow.Format(time.DateOnly),
		EndDate:   tomorrow.Format(time.DateOnly),
		Weekdays:  []int{weekdayForRequest(tomorrow)},
		StartTime: "09:00",
		EndTime:   "09:00",
	}, testOperator())
	if err == nil || !strings.Contains(err.Error(), "不能相同") {
		t.Fatalf("expected zero-length shift rejection, got %v", err)
	}
}

func TestAgentTeamScheduleServiceBatchPreviewRejectsOverLimit(t *testing.T) {
	setupAgentTeamScheduleTestDB(t)
	createAgentTeamScheduleTestTeams(t, sqls.DB())
	today := time.Now()

	_, err := services.AgentTeamScheduleService.BatchPreview(request.AgentTeamScheduleBatchRequest{
		TeamIDs:   []int64{1, 2},
		StartDate: today.Format(time.DateOnly),
		EndDate:   today.AddDate(0, 0, 260).Format(time.DateOnly),
		Weekdays:  []int{1, 2, 3, 4, 5, 6, 7},
		StartTime: "09:00",
		EndTime:   "18:00",
	}, testOperator())
	if err == nil {
		t.Fatalf("expected over-limit preview to fail")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Fatalf("expected 500 limit error, got %v", err)
	}
}

func TestAgentTeamScheduleServiceBatchPreviewMarksConflicts(t *testing.T) {
	db := setupAgentTeamScheduleTestDB(t)
	createAgentTeamScheduleTestTeams(t, db)
	targetDay := time.Now().AddDate(0, 0, 2)
	existing := models.AgentTeamSchedule{
		TeamID:  1,
		StartAt: parseTestDateTime(t, formatTestDateTime(targetDay, "10:00:00")),
		EndAt:   parseTestDateTime(t, formatTestDateTime(targetDay, "12:00:00")),
		Status:  enums.StatusOk,
	}
	if err := db.Create(&existing).Error; err != nil {
		t.Fatalf("create existing schedule error = %v", err)
	}

	preview, err := services.AgentTeamScheduleService.BatchPreview(request.AgentTeamScheduleBatchRequest{
		TeamIDs:   []int64{1, 2},
		StartDate: targetDay.Format(time.DateOnly),
		EndDate:   targetDay.Format(time.DateOnly),
		Weekdays:  []int{weekdayForRequest(targetDay)},
		StartTime: "09:00",
		EndTime:   "18:00",
	}, testOperator())
	if err != nil {
		t.Fatalf("BatchPreview() error = %v", err)
	}
	if !preview.Conflict {
		t.Fatalf("expected preview conflict, got %+v", preview)
	}
	itemsByTeamID := make(map[int64]services.AgentTeamScheduleBatchPreviewItem)
	for _, item := range preview.Items {
		itemsByTeamID[item.TeamID] = item
	}
	team1Item, ok := itemsByTeamID[1]
	if !ok {
		t.Fatalf("expected team 1 preview item, got %+v", preview.Items)
	}
	team2Item, ok := itemsByTeamID[2]
	if !ok {
		t.Fatalf("expected team 2 preview item, got %+v", preview.Items)
	}
	if !team1Item.Conflict || team1Item.ConflictReason == "" {
		t.Fatalf("expected team 1 preview item to be marked as conflict: %+v", team1Item)
	}
	if team2Item.Conflict {
		t.Fatalf("expected team 2 preview item to have no conflict: %+v", team2Item)
	}
}

func TestAgentTeamScheduleServiceBatchPreviewIgnoresDisabledOverlappingSchedule(t *testing.T) {
	db := setupAgentTeamScheduleTestDB(t)
	createAgentTeamScheduleTestTeams(t, db)
	targetDay := time.Now().AddDate(0, 0, 2)
	existing := models.AgentTeamSchedule{
		TeamID:  1,
		StartAt: parseTestDateTime(t, formatTestDateTime(targetDay, "10:00:00")),
		EndAt:   parseTestDateTime(t, formatTestDateTime(targetDay, "12:00:00")),
		Status:  enums.StatusDisabled,
	}
	if err := db.Create(&existing).Error; err != nil {
		t.Fatalf("create existing schedule error = %v", err)
	}

	preview, err := services.AgentTeamScheduleService.BatchPreview(request.AgentTeamScheduleBatchRequest{
		TeamIDs:   []int64{1},
		StartDate: targetDay.Format(time.DateOnly),
		EndDate:   targetDay.Format(time.DateOnly),
		Weekdays:  []int{weekdayForRequest(targetDay)},
		StartTime: "09:00",
		EndTime:   "18:00",
	}, testOperator())
	if err != nil {
		t.Fatalf("BatchPreview() error = %v", err)
	}
	if preview.Conflict {
		t.Fatalf("expected disabled overlapping schedule to be ignored, got %+v", preview)
	}
	if len(preview.Items) != 1 || preview.Items[0].Conflict {
		t.Fatalf("expected one non-conflicting preview item, got %+v", preview.Items)
	}
}

func TestAgentTeamScheduleServiceBatchGenerateCreatesAllSchedules(t *testing.T) {
	db := setupAgentTeamScheduleTestDB(t)
	createAgentTeamScheduleTestTeams(t, db)
	nextMonday := nextTestWeekday(time.Monday)

	result, err := services.AgentTeamScheduleService.BatchGenerate(request.AgentTeamScheduleBatchRequest{
		TeamIDs:   []int64{1, 2},
		StartDate: nextMonday.Format(time.DateOnly),
		EndDate:   nextMonday.AddDate(0, 0, 2).Format(time.DateOnly),
		Weekdays:  []int{1, 3},
		StartTime: "09:00",
		EndTime:   "18:00",
		Remark:    "批量生成",
	}, testOperator())
	if err != nil {
		t.Fatalf("BatchGenerate() error = %v", err)
	}
	if result.Created != 4 {
		t.Fatalf("expected 4 created schedules, got %d", result.Created)
	}
	var schedules []models.AgentTeamSchedule
	if err := db.Where("remark = ?", "批量生成").
		Order("team_id ASC, start_at ASC").
		Find(&schedules).Error; err != nil {
		t.Fatalf("query generated schedules error = %v", err)
	}
	if len(schedules) != 4 {
		t.Fatalf("expected 4 stored schedules, got %d: %+v", len(schedules), schedules)
	}
	for i := range schedules {
		if schedules[i].TenantID != 101 {
			t.Fatalf("generated schedule tenant = %d, want 101", schedules[i].TenantID)
		}
	}
	expected := []struct {
		teamID  int64
		startAt string
		endAt   string
	}{
		{teamID: 1, startAt: formatTestDateTime(nextMonday, "09:00:00"), endAt: formatTestDateTime(nextMonday, "18:00:00")},
		{teamID: 1, startAt: formatTestDateTime(nextMonday.AddDate(0, 0, 2), "09:00:00"), endAt: formatTestDateTime(nextMonday.AddDate(0, 0, 2), "18:00:00")},
		{teamID: 2, startAt: formatTestDateTime(nextMonday, "09:00:00"), endAt: formatTestDateTime(nextMonday, "18:00:00")},
		{teamID: 2, startAt: formatTestDateTime(nextMonday.AddDate(0, 0, 2), "09:00:00"), endAt: formatTestDateTime(nextMonday.AddDate(0, 0, 2), "18:00:00")},
	}
	for i, want := range expected {
		got := schedules[i]
		if got.TeamID != want.teamID ||
			got.StartAt.Format(time.DateTime) != want.startAt ||
			got.EndAt.Format(time.DateTime) != want.endAt {
			t.Fatalf("unexpected schedule at index %d: got teamID=%d startAt=%s endAt=%s, want teamID=%d startAt=%s endAt=%s",
				i,
				got.TeamID,
				got.StartAt.Format(time.DateTime),
				got.EndAt.Format(time.DateTime),
				want.teamID,
				want.startAt,
				want.endAt,
			)
		}
	}
}

func TestAgentTeamScheduleServiceBatchGenerateRejectsConflictsWithoutPartialCreate(t *testing.T) {
	db := setupAgentTeamScheduleTestDB(t)
	createAgentTeamScheduleTestTeams(t, db)
	targetDay := time.Now().AddDate(0, 0, 2)
	existing := models.AgentTeamSchedule{
		TeamID:  1,
		StartAt: parseTestDateTime(t, formatTestDateTime(targetDay, "10:00:00")),
		EndAt:   parseTestDateTime(t, formatTestDateTime(targetDay, "12:00:00")),
		Status:  enums.StatusOk,
	}
	if err := db.Create(&existing).Error; err != nil {
		t.Fatalf("create existing schedule error = %v", err)
	}

	_, err := services.AgentTeamScheduleService.BatchGenerate(request.AgentTeamScheduleBatchRequest{
		TeamIDs:   []int64{1, 2},
		StartDate: targetDay.Format(time.DateOnly),
		EndDate:   targetDay.Format(time.DateOnly),
		Weekdays:  []int{weekdayForRequest(targetDay)},
		StartTime: "09:00",
		EndTime:   "18:00",
		Remark:    "不应创建",
	}, testOperator())
	if err == nil {
		t.Fatalf("expected conflict batch generate to fail")
	}
	var count int64
	db.Model(&models.AgentTeamSchedule{}).Where("remark = ?", "不应创建").Count(&count)
	if count != 0 {
		t.Fatalf("expected no partial creates, got %d", count)
	}
}

func TestAgentTeamScheduleMutationsUseDatabaseLocks(t *testing.T) {
	tests := []struct {
		name             string
		wantTeamIDs      []int64
		wantScheduleLock bool
		action           func(t *testing.T, db *gorm.DB) error
	}{
		{
			name:        "create",
			wantTeamIDs: []int64{1},
			action: func(t *testing.T, db *gorm.DB) error {
				tomorrow := time.Now().AddDate(0, 0, 1)
				_, err := services.AgentTeamScheduleService.CreateAgentTeamSchedule(request.CreateAgentTeamScheduleRequest{
					TeamID: 1, StartAt: formatTestDateTime(tomorrow, "06:00:00"), EndAt: formatTestDateTime(tomorrow, "07:00:00"),
				}, testOperator())
				return err
			},
		},
		{
			name:             "update",
			wantTeamIDs:      []int64{1, 2},
			wantScheduleLock: true,
			action: func(t *testing.T, db *gorm.DB) error {
				id := createFutureAgentTeamSchedule(t, db)
				tomorrow := time.Now().AddDate(0, 0, 1)
				return services.AgentTeamScheduleService.UpdateAgentTeamSchedule(request.UpdateAgentTeamScheduleRequest{
					ID: id,
					CreateAgentTeamScheduleRequest: request.CreateAgentTeamScheduleRequest{
						TeamID: 2, StartAt: formatTestDateTime(tomorrow, "06:00:00"), EndAt: formatTestDateTime(tomorrow, "07:00:00"),
					},
				}, testOperator())
			},
		},
		{
			name:             "delete",
			wantTeamIDs:      []int64{1},
			wantScheduleLock: true,
			action: func(t *testing.T, db *gorm.DB) error {
				return services.AgentTeamScheduleService.DeleteAgentTeamSchedule(createFutureAgentTeamSchedule(t, db), testOperator())
			},
		},
		{
			name:        "batch generate",
			wantTeamIDs: []int64{1, 2},
			action: func(t *testing.T, db *gorm.DB) error {
				date := nextTestWeekday(time.Monday)
				_, err := services.AgentTeamScheduleService.BatchGenerate(request.AgentTeamScheduleBatchRequest{
					TeamIDs: []int64{2, 1}, StartDate: date.Format(time.DateOnly), EndDate: date.Format(time.DateOnly),
					Weekdays: []int{weekdayForRequest(date)}, StartTime: "06:00", EndTime: "07:00",
				}, testOperator())
				return err
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := setupAgentTeamScheduleTestDB(t)
			createAgentTeamScheduleTestTeams(t, db)
			teamIDs := make([]int64, 0, len(tt.wantTeamIDs))
			scheduleLocked := false
			callbackName := "test:schedule-locks-" + strings.ReplaceAll(tt.name, " ", "-")
			if err := db.Callback().Query().After("gorm:query").Register(callbackName, func(tx *gorm.DB) {
				if _, locked := tx.Statement.Clauses["FOR"]; !locked || tx.Statement.Schema == nil {
					return
				}
				switch tx.Statement.Schema.Name {
				case "AgentTeam":
					if item, ok := tx.Statement.Dest.(*models.AgentTeam); ok {
						teamIDs = append(teamIDs, item.ID)
					}
				case "AgentTeamSchedule":
					scheduleLocked = true
				}
			}); err != nil {
				t.Fatalf("register lock callback: %v", err)
			}
			t.Cleanup(func() {
				if err := db.Callback().Query().Remove(callbackName); err != nil {
					t.Errorf("remove lock callback: %v", err)
				}
			})

			if err := tt.action(t, db); err != nil {
				t.Fatalf("%s schedule: %v", tt.name, err)
			}
			if !slices.Equal(teamIDs, tt.wantTeamIDs) {
				t.Fatalf("%s team lock order = %v, want %v", tt.name, teamIDs, tt.wantTeamIDs)
			}
			if scheduleLocked != tt.wantScheduleLock {
				t.Fatalf("%s schedule lock = %v, want %v", tt.name, scheduleLocked, tt.wantScheduleLock)
			}
		})
	}
}

func setupAgentTeamScheduleTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	dbName := strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())
	db, err := gorm.Open(sqlite.Open("file:"+dbName+"?mode=memory&cache=shared"), &gorm.Config{
		NamingStrategy: schema.NamingStrategy{
			TablePrefix:   "t_",
			SingularTable: true,
		},
	})
	if err != nil {
		t.Fatalf("open sqlite error = %v", err)
	}
	t.Cleanup(func() {
		sqlDB, err := db.DB()
		if err == nil {
			_ = sqlDB.Close()
		}
	})
	if err := db.AutoMigrate(
		&models.User{},
		&models.Role{},
		&models.Permission{},
		&models.UserRole{},
		&models.RolePermission{},
		&models.AgentTeam{},
		&models.AgentTeamSquad{},
		&models.AgentTeamSquadMember{},
		&models.AgentProfile{},
		&models.AgentTeamSchedule{},
	); err != nil {
		t.Fatalf("auto migrate error = %v", err)
	}
	sqls.SetDB(db)
	return db
}

func createAgentTeamScheduleTestData(t *testing.T, db *gorm.DB) {
	t.Helper()

	createAgentTeamScheduleTestTeams(t, db)

	parse := func(value string) time.Time {
		t.Helper()
		ret, err := time.ParseInLocation(time.DateTime, value, time.Local)
		if err != nil {
			t.Fatalf("parse time %q error = %v", value, err)
		}
		return ret
	}
	schedules := []models.AgentTeamSchedule{
		{ID: 1, TenantID: 101, TeamID: 1, StartAt: parse("2026-04-26 20:00:00"), EndAt: parse("2026-04-27 10:00:00"), Status: enums.StatusOk},
		{ID: 2, TenantID: 101, TeamID: 1, StartAt: parse("2026-04-28 09:00:00"), EndAt: parse("2026-04-28 18:00:00"), Status: enums.StatusOk},
		{ID: 3, TenantID: 101, TeamID: 2, StartAt: parse("2026-05-03 20:00:00"), EndAt: parse("2026-05-04 08:00:00"), Status: enums.StatusOk},
		{ID: 4, TenantID: 101, TeamID: 1, StartAt: parse("2026-04-20 09:00:00"), EndAt: parse("2026-04-20 18:00:00"), Status: enums.StatusOk},
		{ID: 5, TenantID: 101, TeamID: 2, StartAt: parse("2026-05-04 09:00:00"), EndAt: parse("2026-05-04 18:00:00"), Status: enums.StatusOk},
	}
	if err := db.Create(&schedules).Error; err != nil {
		t.Fatalf("create schedules error = %v", err)
	}
}

func createAgentTeamScheduleTestTeams(t *testing.T, db *gorm.DB) {
	t.Helper()
	teams := []models.AgentTeam{
		{ID: 1, TenantID: 101, Name: "售前组", DispatchMode: enums.AgentTeamDispatchModeRule, Status: enums.StatusOk},
		{ID: 2, TenantID: 101, Name: "售后组", DispatchMode: enums.AgentTeamDispatchModeRule, Status: enums.StatusOk},
	}
	if err := db.Create(&teams).Error; err != nil {
		t.Fatalf("create teams error = %v", err)
	}
	users := []models.User{
		{ID: 9101, TenantID: 101, Username: "schedule-team-1", Status: enums.StatusOk},
		{ID: 9102, TenantID: 101, Username: "schedule-team-2", Status: enums.StatusOk},
	}
	if err := db.Create(&users).Error; err != nil {
		t.Fatalf("create schedule coverage users error = %v", err)
	}
	grantScheduleReplyPermissions(t, db, 9101, 9102)
	profiles := []models.AgentProfile{
		{ID: 9201, TenantID: 101, UserID: 9101, TeamID: 1, AgentCode: "schedule-team-1", AutoAssignEnabled: true, MaxConcurrentCount: 5, Status: enums.StatusOk},
		{ID: 9202, TenantID: 101, UserID: 9102, TeamID: 2, AgentCode: "schedule-team-2", AutoAssignEnabled: true, MaxConcurrentCount: 5, Status: enums.StatusOk},
	}
	if err := db.Create(&profiles).Error; err != nil {
		t.Fatalf("create schedule coverage profiles error = %v", err)
	}
}

func grantScheduleReplyPermissions(t *testing.T, db *gorm.DB, userIDs ...int64) {
	t.Helper()
	role := models.Role{Name: "排班测试客服", Code: "schedule-test-agent", Scope: "tenant", Status: enums.StatusOk}
	if err := db.Where("code = ?", role.Code).FirstOrCreate(&role).Error; err != nil {
		t.Fatalf("create schedule reply role: %v", err)
	}
	for index, definition := range []constants.Permission{
		constants.PermissionConversationView,
		constants.PermissionConversationSend,
	} {
		permission := models.Permission{
			Name: definition.Name, Code: definition.Code, Type: definition.Type,
			Scope: "tenant", Status: enums.StatusOk, SortNo: index + 1,
		}
		if err := db.Where("code = ?", permission.Code).FirstOrCreate(&permission).Error; err != nil {
			t.Fatalf("create schedule reply permission: %v", err)
		}
		binding := models.RolePermission{RoleID: role.ID, PermissionID: permission.ID}
		if err := db.Where("role_id = ? AND permission_id = ?", role.ID, permission.ID).FirstOrCreate(&binding).Error; err != nil {
			t.Fatalf("bind schedule reply permission: %v", err)
		}
	}
	for _, userID := range userIDs {
		binding := models.UserRole{UserID: userID, RoleID: role.ID}
		if err := db.Where("user_id = ? AND role_id = ?", userID, role.ID).FirstOrCreate(&binding).Error; err != nil {
			t.Fatalf("bind schedule reply role: %v", err)
		}
	}
}

func formatTestDateTime(date time.Time, clock string) string {
	return date.Format(time.DateOnly) + " " + clock
}

func nextTestWeekday(target time.Weekday) time.Time {
	ret := startOfTestDay(time.Now()).AddDate(0, 0, 1)
	for ret.Weekday() != target {
		ret = ret.AddDate(0, 0, 1)
	}
	return ret
}

func startOfTestDay(value time.Time) time.Time {
	year, month, day := value.In(time.Local).Date()
	return time.Date(year, month, day, 0, 0, 0, 0, time.Local)
}

func weekdayForRequest(value time.Time) int {
	if value.Weekday() == time.Sunday {
		return 7
	}
	return int(value.Weekday())
}

func createFutureAgentTeamSchedule(t *testing.T, db *gorm.DB) int64 {
	t.Helper()
	tomorrow := time.Now().AddDate(0, 0, 1)
	item := models.AgentTeamSchedule{
		TenantID: 101,
		TeamID:   1,
		StartAt:  parseTestDateTime(t, formatTestDateTime(tomorrow, "09:00:00")),
		EndAt:    parseTestDateTime(t, formatTestDateTime(tomorrow, "18:00:00")),
		Status:   enums.StatusOk,
	}
	if err := db.Create(&item).Error; err != nil {
		t.Fatalf("create future schedule error = %v", err)
	}
	return item.ID
}

func parseTestDateTime(t *testing.T, value string) time.Time {
	t.Helper()
	ret, err := time.ParseInLocation(time.DateTime, value, time.Local)
	if err != nil {
		t.Fatalf("parse time %q error = %v", value, err)
	}
	return ret
}

func testOperator() *dto.AuthPrincipal {
	return &dto.AuthPrincipal{UserID: 1, Username: "tester", ActiveTenantID: 101, Status: enums.StatusOk, Roles: []string{constants.RoleCodeAdmin}}
}
