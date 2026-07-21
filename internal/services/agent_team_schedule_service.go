package services

import (
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/errorsx"
	"agent-desk/internal/pkg/utils"
	"agent-desk/internal/repositories"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"agent-desk/internal/pkg/httpx/params"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

var AgentTeamScheduleService = newAgentTeamScheduleService()

func newAgentTeamScheduleService() *agentTeamScheduleService {
	return &agentTeamScheduleService{}
}

type agentTeamScheduleService struct {
	writeMu sync.Mutex
}

const (
	maxAgentTeamScheduleBatchItems     = 500
	maxAgentTeamScheduleDuration       = 24 * time.Hour
	maxAgentTeamScheduleOverrideAgents = 100
)

func agentTeamSchedulesConflict(leftSquadID, rightSquadID int64) bool {
	return leftSquadID <= 0 || rightSquadID <= 0 || leftSquadID == rightSquadID
}

type AgentTeamScheduleBatchPreviewResult struct {
	Total    int
	Conflict bool
	Items    []AgentTeamScheduleBatchPreviewItem
}

type AgentTeamScheduleBatchPreviewItem struct {
	TeamID             int64
	TeamName           string
	SquadID            int64
	SquadName          string
	Date               time.Time
	Weekday            int
	StartAt            time.Time
	EndAt              time.Time
	Remark             string
	EligibleAgentCount int
	TotalCapacity      int
	CoverageWarning    string
	Conflict           bool
	ConflictReason     string
}

type AgentTeamScheduleBatchGenerateResult struct {
	Created int
}

type batchScheduleCandidate struct {
	TenantID                int64
	TeamID                  int64
	TeamName                string
	SquadID                 int64
	SquadName               string
	IncludedAgentProfileIDs []int64
	ExcludedAgentProfileIDs []int64
	Date                    time.Time
	StartAt                 time.Time
	EndAt                   time.Time
	Remark                  string
}

func (s *agentTeamScheduleService) Get(id int64) *models.AgentTeamSchedule {
	return repositories.AgentTeamScheduleRepository.Get(sqls.DB(), id)
}

func (s *agentTeamScheduleService) GetInTenant(id int64, operator *dto.AuthPrincipal) *models.AgentTeamSchedule {
	return repositories.AgentTeamScheduleRepository.GetInTenant(sqls.DB(), id, AgentTeamScopeService.ActiveTenantID(operator))
}

func (s *agentTeamScheduleService) Take(where ...interface{}) *models.AgentTeamSchedule {
	return repositories.AgentTeamScheduleRepository.Take(sqls.DB(), where...)
}

func (s *agentTeamScheduleService) Find(cnd *sqls.Cnd) []models.AgentTeamSchedule {
	return repositories.AgentTeamScheduleRepository.Find(sqls.DB(), cnd)
}

func (s *agentTeamScheduleService) FindOne(cnd *sqls.Cnd) *models.AgentTeamSchedule {
	return repositories.AgentTeamScheduleRepository.FindOne(sqls.DB(), cnd)
}

func (s *agentTeamScheduleService) FindPageByParams(params *params.QueryParams) (list []models.AgentTeamSchedule, paging *sqls.Paging) {
	return repositories.AgentTeamScheduleRepository.FindPageByParams(sqls.DB(), params)
}

func (s *agentTeamScheduleService) FindPageByCnd(cnd *sqls.Cnd) (list []models.AgentTeamSchedule, paging *sqls.Paging) {
	return repositories.AgentTeamScheduleRepository.FindPageByCnd(sqls.DB(), cnd)
}

func (s *agentTeamScheduleService) FindPageInTenant(cnd *sqls.Cnd, operator *dto.AuthPrincipal) (list []models.AgentTeamSchedule, paging *sqls.Paging) {
	return repositories.AgentTeamScheduleRepository.FindPageByCnd(sqls.DB(), AgentTeamScopeService.ApplyTenantFilter(cnd, operator))
}

func (s *agentTeamScheduleService) Count(cnd *sqls.Cnd) int64 {
	return repositories.AgentTeamScheduleRepository.Count(sqls.DB(), cnd)
}

func (s *agentTeamScheduleService) FindCalendarSchedules(req request.AgentTeamScheduleCalendarRequest) ([]models.AgentTeamSchedule, error) {
	startAtValue, err := parseRequiredDateTime(req.StartAt, "开始时间格式错误")
	if err != nil {
		return nil, err
	}
	endAtValue, err := parseRequiredDateTime(req.EndAt, "结束时间格式错误")
	if err != nil {
		return nil, err
	}
	if !endAtValue.After(startAtValue) {
		return nil, errorsx.InvalidParam("结束时间必须晚于开始时间")
	}
	return repositories.AgentTeamScheduleRepository.FindByTimeRange(sqls.DB(), startAtValue, endAtValue, req.TeamID, req.SquadID), nil
}

func (s *agentTeamScheduleService) FindCalendarSchedulesInTenant(req request.AgentTeamScheduleCalendarRequest, operator *dto.AuthPrincipal) ([]models.AgentTeamSchedule, error) {
	startAtValue, err := parseRequiredDateTime(req.StartAt, "开始时间格式错误")
	if err != nil {
		return nil, err
	}
	endAtValue, err := parseRequiredDateTime(req.EndAt, "结束时间格式错误")
	if err != nil {
		return nil, err
	}
	if !endAtValue.After(startAtValue) {
		return nil, errorsx.InvalidParam("结束时间必须晚于开始时间")
	}
	tenantID := AgentTeamScopeService.ActiveTenantID(operator)
	if tenantID <= 0 {
		return nil, errorsx.Forbidden("请先选择接入公司")
	}
	return repositories.AgentTeamScheduleRepository.FindByTimeRangeInTenant(sqls.DB(), startAtValue, endAtValue, req.TeamID, req.SquadID, tenantID), nil
}

func (s *agentTeamScheduleService) CreateAgentTeamSchedule(req request.CreateAgentTeamScheduleRequest, operator *dto.AuthPrincipal) (*models.AgentTeamSchedule, error) {
	if operator == nil {
		return nil, errorsx.Unauthorized("未登录或登录已过期")
	}
	s.writeMu.Lock()
	var item *models.AgentTeamSchedule
	err := sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		teams, err := s.lockManageableScheduleTeamsDB(ctx.Tx, []int64{req.TeamID}, operator, "只能调整自己管理的客服组排班")
		if err != nil {
			return err
		}
		item, err = s.buildScheduleModelDB(ctx.Tx, teams[req.TeamID], 0, req.SquadID, req.IncludedAgentProfileIDs, req.ExcludedAgentProfileIDs, req.StartAt, req.EndAt, req.Remark)
		if err != nil {
			return err
		}
		item.AuditFields = utils.BuildAuditFields(operator)
		return repositories.AgentTeamScheduleRepository.Create(ctx.Tx, item)
	})
	s.writeMu.Unlock()
	if err != nil {
		return nil, err
	}
	s.reconcileDispatchAfterScheduleChange(nil, item)
	return item, nil
}

func (s *agentTeamScheduleService) UpdateAgentTeamSchedule(req request.UpdateAgentTeamScheduleRequest, operator *dto.AuthPrincipal) error {
	if operator == nil {
		return errorsx.Unauthorized("未登录或登录已过期")
	}
	s.writeMu.Lock()
	var item *models.AgentTeamSchedule
	var previous *models.AgentTeamSchedule
	err := sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		tenantID := AgentTeamScopeService.ActiveTenantID(operator)
		current, err := repositories.AgentTeamScheduleRepository.GetForUpdateInTenant(ctx.Tx, req.ID, tenantID)
		if err != nil {
			return err
		}
		if current == nil {
			return errorsx.InvalidParam("客服组排班不存在")
		}
		copy := *current
		previous = &copy
		teams, err := s.lockManageableScheduleTeamsDB(ctx.Tx, []int64{current.TeamID, req.TeamID}, operator, "排班原客服组和目标组都必须在你的管理范围内")
		if err != nil {
			return err
		}
		item, err = s.buildScheduleModelDB(ctx.Tx, teams[req.TeamID], req.ID, req.SquadID, req.IncludedAgentProfileIDs, req.ExcludedAgentProfileIDs, req.StartAt, req.EndAt, req.Remark)
		if err != nil {
			return err
		}
		return repositories.AgentTeamScheduleRepository.UpdatesInTenant(ctx.Tx, req.ID, current.TenantID, map[string]any{
			"tenant_id":                  item.TenantID,
			"team_id":                    item.TeamID,
			"squad_id":                   item.SquadID,
			"included_agent_profile_ids": item.IncludedAgentProfileIDs,
			"excluded_agent_profile_ids": item.ExcludedAgentProfileIDs,
			"start_at":                   item.StartAt,
			"end_at":                     item.EndAt,
			"remark":                     item.Remark,
			"update_user_id":             operator.UserID,
			"update_user_name":           operator.Username,
			"updated_at":                 time.Now(),
		})
	})
	s.writeMu.Unlock()
	if err != nil {
		return err
	}
	s.reconcileDispatchAfterScheduleChange(previous, item)
	return nil
}

func (s *agentTeamScheduleService) DeleteAgentTeamSchedule(id int64, operator *dto.AuthPrincipal) error {
	if operator == nil {
		return errorsx.Unauthorized("未登录或登录已过期")
	}
	s.writeMu.Lock()
	var deleted *models.AgentTeamSchedule
	err := sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		tenantID := AgentTeamScopeService.ActiveTenantID(operator)
		current, err := repositories.AgentTeamScheduleRepository.GetForUpdateInTenant(ctx.Tx, id, tenantID)
		if err != nil {
			return err
		}
		if current == nil {
			return errorsx.InvalidParam("客服组排班不存在")
		}
		if _, err := s.lockManageableScheduleTeamsDB(ctx.Tx, []int64{current.TeamID}, operator, "只能删除自己管理的客服组排班"); err != nil {
			return err
		}
		copy := *current
		deleted = &copy
		return repositories.AgentTeamScheduleRepository.DeleteInTenant(ctx.Tx, id, current.TenantID)
	})
	s.writeMu.Unlock()
	if err != nil {
		return err
	}
	s.reconcileDispatchAfterScheduleChange(deleted, nil)
	return nil
}

func (s *agentTeamScheduleService) BatchPreview(req request.AgentTeamScheduleBatchRequest, operator *dto.AuthPrincipal) (*AgentTeamScheduleBatchPreviewResult, error) {
	if operator == nil {
		return nil, errorsx.Unauthorized("未登录或登录已过期")
	}
	candidates, err := s.buildBatchScheduleCandidates(req, operator)
	if err != nil {
		return nil, err
	}
	if err := s.requireManageableBatchTeams(candidates, operator); err != nil {
		return nil, err
	}
	conflicts := s.findBatchConflict(candidates)
	coverage := s.buildBatchCoverageDB(sqls.DB(), candidates)
	return buildBatchPreviewResult(candidates, conflicts, coverage), nil
}

func (s *agentTeamScheduleService) BatchGenerate(req request.AgentTeamScheduleBatchRequest, operator *dto.AuthPrincipal) (*AgentTeamScheduleBatchGenerateResult, error) {
	if operator == nil {
		return nil, errorsx.Unauthorized("未登录或登录已过期")
	}
	s.writeMu.Lock()
	var schedules []models.AgentTeamSchedule
	err := sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		if _, err := s.lockManageableScheduleTeamsDB(ctx.Tx, req.TeamIDs, operator, "批量排班只能包含自己管理的客服组"); err != nil {
			return err
		}
		candidates, err := s.buildBatchScheduleCandidatesDB(ctx.Tx, req, operator)
		if err != nil {
			return err
		}
		conflicts := s.findBatchConflictByDB(ctx.Tx, candidates)
		for _, conflict := range conflicts {
			if conflict != "" {
				return errorsx.InvalidParam("存在冲突排班，请先处理冲突")
			}
		}
		coverage := s.buildBatchCoverageDB(ctx.Tx, candidates)
		for i := range candidates {
			if coverage[i].EligibleAgentCount == 0 {
				return errorsx.InvalidParam(fmt.Sprintf("%s %s 至 %s：%s", candidates[i].TeamName, candidates[i].StartAt.Format(time.DateTime), candidates[i].EndAt.Format(time.DateTime), coverage[i].Warning))
			}
		}
		schedules = make([]models.AgentTeamSchedule, 0, len(candidates))
		for _, candidate := range candidates {
			schedules = append(schedules, models.AgentTeamSchedule{
				TenantID:                candidate.TenantID,
				TeamID:                  candidate.TeamID,
				SquadID:                 candidate.SquadID,
				IncludedAgentProfileIDs: utils.JoinInt64s(candidate.IncludedAgentProfileIDs),
				ExcludedAgentProfileIDs: utils.JoinInt64s(candidate.ExcludedAgentProfileIDs),
				StartAt:                 candidate.StartAt,
				EndAt:                   candidate.EndAt,
				Remark:                  candidate.Remark,
				Status:                  enums.StatusOk,
				AuditFields:             utils.BuildAuditFields(operator),
			})
		}
		return repositories.AgentTeamScheduleRepository.CreateBatch(ctx.Tx, schedules)
	})
	s.writeMu.Unlock()
	if err != nil {
		return nil, err
	}
	for i := range schedules {
		s.reconcileDispatchAfterScheduleChange(nil, &schedules[i])
	}
	return &AgentTeamScheduleBatchGenerateResult{Created: len(schedules)}, nil
}

func (s *agentTeamScheduleService) requireManageableBatchTeams(candidates []batchScheduleCandidate, operator *dto.AuthPrincipal) error {
	seen := make(map[int64]struct{})
	for _, candidate := range candidates {
		if _, ok := seen[candidate.TeamID]; ok {
			continue
		}
		seen[candidate.TeamID] = struct{}{}
		if !AgentTeamScopeService.CanManageTeam(operator, candidate.TeamID) {
			return errorsx.Forbidden("批量排班只能包含自己管理的客服组")
		}
	}
	return nil
}

func (s *agentTeamScheduleService) buildScheduleModelDB(db *gorm.DB, team *models.AgentTeam, id, squadID int64, includedProfileIDs, excludedProfileIDs []int64, startAt, endAt, remark string) (*models.AgentTeamSchedule, error) {
	if team == nil || team.ID <= 0 {
		return nil, errorsx.InvalidParam("请选择客服组")
	}
	if team.TenantID <= 0 {
		return nil, errorsx.InvalidParam("客服组尚未归属接入公司")
	}
	if team.Status != enums.StatusOk {
		return nil, errorsx.InvalidParam("客服组已停用，不能安排班次")
	}
	if err := s.validateScheduleSquadDB(db, team.ID, squadID); err != nil {
		return nil, err
	}
	includedProfileIDs, excludedProfileIDs, err := s.normalizeScheduleOverridesDB(db, team, includedProfileIDs, excludedProfileIDs)
	if err != nil {
		return nil, err
	}
	startAtValue, err := parseRequiredDateTime(startAt, "开始时间格式错误")
	if err != nil {
		return nil, err
	}
	endAtValue, err := parseRequiredDateTime(endAt, "结束时间格式错误")
	if err != nil {
		return nil, err
	}
	if !endAtValue.After(startAtValue) {
		return nil, errorsx.InvalidParam("结束时间必须晚于开始时间")
	}
	if endAtValue.Sub(startAtValue) > maxAgentTeamScheduleDuration {
		return nil, errorsx.InvalidParam("单条班次时长不能超过 24 小时")
	}
	if startAtValue.Before(startOfLocalDay(time.Now())) {
		return nil, errorsx.InvalidParam("不能添加或修改历史日期的排班")
	}
	overlapping := repositories.AgentTeamScheduleRepository.FindOverlappingByTeamIDsAndTimeRange(db, []int64{team.ID}, startAtValue, endAtValue)
	for _, item := range overlapping {
		if item.ID != id && agentTeamSchedulesConflict(item.SquadID, squadID) {
			return nil, errorsx.InvalidParam("同一客服小组或全组班次不能在所选时间段重复排班")
		}
	}
	item := &models.AgentTeamSchedule{
		TenantID:                team.TenantID,
		TeamID:                  team.ID,
		SquadID:                 squadID,
		IncludedAgentProfileIDs: utils.JoinInt64s(includedProfileIDs),
		ExcludedAgentProfileIDs: utils.JoinInt64s(excludedProfileIDs),
		StartAt:                 startAtValue,
		EndAt:                   endAtValue,
		Remark:                  strings.TrimSpace(remark),
	}
	coverage := s.buildBatchCoverageDB(db, []batchScheduleCandidate{{
		TenantID:                item.TenantID,
		TeamID:                  item.TeamID,
		TeamName:                team.Name,
		SquadID:                 item.SquadID,
		IncludedAgentProfileIDs: includedProfileIDs,
		ExcludedAgentProfileIDs: excludedProfileIDs,
		StartAt:                 item.StartAt,
		EndAt:                   item.EndAt,
	}})
	if coverage[0].EligibleAgentCount == 0 {
		return nil, errorsx.InvalidParam(coverage[0].Warning)
	}
	return item, nil
}

func (s *agentTeamScheduleService) buildBatchScheduleCandidates(req request.AgentTeamScheduleBatchRequest, operator *dto.AuthPrincipal) ([]batchScheduleCandidate, error) {
	return s.buildBatchScheduleCandidatesDB(sqls.DB(), req, operator)
}

func (s *agentTeamScheduleService) buildBatchScheduleCandidatesDB(db *gorm.DB, req request.AgentTeamScheduleBatchRequest, operator *dto.AuthPrincipal) ([]batchScheduleCandidate, error) {
	teamIDs := uniquePositiveInt64s(req.TeamIDs)
	if len(teamIDs) == 0 {
		return nil, errorsx.InvalidParam("请选择客服组")
	}
	if req.SquadID > 0 && len(teamIDs) != 1 {
		return nil, errorsx.InvalidParam("按客服小组批量排班时只能选择一个综合客服组")
	}
	if (len(req.IncludedAgentProfileIDs) > 0 || len(req.ExcludedAgentProfileIDs) > 0) && len(teamIDs) != 1 {
		return nil, errorsx.InvalidParam("设置临时替班或请假客服时只能选择一个综合客服组")
	}
	weekdays, err := normalizeBatchWeekdays(req.Weekdays)
	if err != nil {
		return nil, err
	}
	startDate, err := parseRequiredDate(req.StartDate, "开始日期格式错误")
	if err != nil {
		return nil, err
	}
	endDate, err := parseRequiredDate(req.EndDate, "结束日期格式错误")
	if err != nil {
		return nil, err
	}
	if endDate.Before(startDate) {
		return nil, errorsx.InvalidParam("结束日期必须晚于或等于开始日期")
	}
	if startDate.Before(startOfLocalDay(time.Now())) {
		return nil, errorsx.InvalidParam("不能添加或修改历史日期的排班")
	}
	timeRanges, err := normalizeScheduleBatchTimeRanges(req)
	if err != nil {
		return nil, err
	}

	tenantID := AgentTeamScopeService.ActiveTenantID(operator)
	if tenantID <= 0 {
		return nil, errorsx.Forbidden("请先选择接入公司")
	}
	teams := repositories.AgentTeamRepository.FindByIdsInTenant(db, teamIDs, tenantID)
	teamsByID := make(map[int64]models.AgentTeam, len(teams))
	for _, team := range teams {
		teamsByID[team.ID] = team
	}
	for _, teamID := range teamIDs {
		team, ok := teamsByID[teamID]
		if !ok || team.Status == enums.StatusDeleted {
			return nil, errorsx.InvalidParam("客服组不存在")
		}
		if team.TenantID <= 0 {
			return nil, errorsx.InvalidParam("客服组尚未归属接入公司")
		}
		if !slices.Contains(enums.StatusValues, team.Status) {
			return nil, errorsx.InvalidParam("客服组状态不合法")
		}
	}
	squadName := ""
	if req.SquadID > 0 {
		if err := s.validateScheduleSquadDB(db, teamIDs[0], req.SquadID); err != nil {
			return nil, err
		}
		if squad := repositories.AgentTeamSquadRepository.Get(db, req.SquadID); squad != nil {
			squadName = squad.Name
		}
	}
	includedProfileIDs := []int64(nil)
	excludedProfileIDs := []int64(nil)
	if len(teamIDs) == 1 {
		team := teamsByID[teamIDs[0]]
		includedProfileIDs, excludedProfileIDs, err = s.normalizeScheduleOverridesDB(db, &team, req.IncludedAgentProfileIDs, req.ExcludedAgentProfileIDs)
		if err != nil {
			return nil, err
		}
	}

	weekdaySet := make(map[int]struct{}, len(weekdays))
	for _, weekday := range weekdays {
		weekdaySet[weekday] = struct{}{}
	}
	candidates := make([]batchScheduleCandidate, 0)
	remark := strings.TrimSpace(req.Remark)
	for _, teamID := range teamIDs {
		team := teamsByID[teamID]
		for date := startDate; !date.After(endDate); date = date.AddDate(0, 0, 1) {
			if _, ok := weekdaySet[weekdayForBatchRequest(date)]; !ok {
				continue
			}
			for _, tr := range timeRanges {
				if len(candidates) >= maxAgentTeamScheduleBatchItems {
					return nil, errorsx.InvalidParam(fmt.Sprintf("单次最多生成 %d 条排班", maxAgentTeamScheduleBatchItems))
				}
				startAt := combineDateAndClock(date, tr.startClock)
				endAt := combineDateAndClock(date, tr.endClock)
				if tr.overnight {
					endAt = endAt.AddDate(0, 0, 1)
				}
				candidates = append(candidates, batchScheduleCandidate{
					TenantID:                team.TenantID,
					TeamID:                  teamID,
					TeamName:                team.Name,
					SquadID:                 req.SquadID,
					SquadName:               squadName,
					IncludedAgentProfileIDs: includedProfileIDs,
					ExcludedAgentProfileIDs: excludedProfileIDs,
					Date:                    date,
					StartAt:                 startAt,
					EndAt:                   endAt,
					Remark:                  remark,
				})
			}
		}
	}
	if len(candidates) == 0 {
		return nil, errorsx.InvalidParam("未生成任何排班")
	}
	return candidates, nil
}

func (s *agentTeamScheduleService) lockManageableScheduleTeamsDB(db *gorm.DB, teamIDs []int64, operator *dto.AuthPrincipal, forbiddenMessage string) (map[int64]*models.AgentTeam, error) {
	return AgentTeamScopeService.lockManageableTeamsDB(db, teamIDs, operator, forbiddenMessage)
}

type scheduleBatchClockRange struct {
	startClock time.Time
	endClock   time.Time
	overnight  bool
}

func normalizeScheduleBatchTimeRanges(req request.AgentTeamScheduleBatchRequest) ([]scheduleBatchClockRange, error) {
	raw := req.TimeRanges
	if len(raw) == 0 {
		raw = []request.AgentTeamScheduleTimeRange{{StartTime: req.StartTime, EndTime: req.EndTime}}
	}
	ret := make([]scheduleBatchClockRange, 0, len(raw))
	seen := map[string]struct{}{}
	for _, item := range raw {
		startClock, err := parseRequiredClock(item.StartTime, "开始时间格式错误")
		if err != nil {
			return nil, err
		}
		endClock, err := parseRequiredClock(item.EndTime, "结束时间格式错误")
		if err != nil {
			return nil, err
		}
		firstStartAt := combineDateAndClock(time.Now(), startClock)
		firstEndAt := combineDateAndClock(time.Now(), endClock)
		if firstEndAt.Equal(firstStartAt) {
			return nil, errorsx.InvalidParam("班次开始和结束时间不能相同")
		}
		key := startClock.Format("15:04:05") + "-" + endClock.Format("15:04:05")
		if _, ok := seen[key]; ok {
			return nil, errorsx.InvalidParam("存在重复班次时段")
		}
		seen[key] = struct{}{}
		ret = append(ret, scheduleBatchClockRange{startClock: startClock, endClock: endClock, overnight: firstEndAt.Before(firstStartAt)})
	}
	if len(ret) == 0 {
		return nil, errorsx.InvalidParam("请至少填写一个排班时间段")
	}
	return ret, nil
}

func parseRequiredDate(value, message string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, errorsx.InvalidParam(message)
	}
	ret, err := time.ParseInLocation(time.DateOnly, value, time.Local)
	if err != nil {
		return time.Time{}, errorsx.InvalidParam(message + "，请使用 yyyy-MM-dd")
	}
	return startOfLocalDay(ret), nil
}

func parseRequiredClock(value, message string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, errorsx.InvalidParam(message)
	}
	layouts := []string{"15:04", "15:04:05"}
	for _, layout := range layouts {
		if ret, err := time.ParseInLocation(layout, value, time.Local); err == nil {
			return ret, nil
		}
	}
	return time.Time{}, errorsx.InvalidParam(message + "，请使用 HH:mm 或 HH:mm:ss")
}

func combineDateAndClock(date, clock time.Time) time.Time {
	year, month, day := date.In(time.Local).Date()
	hour, minute, second := clock.In(time.Local).Clock()
	return time.Date(year, month, day, hour, minute, second, 0, time.Local)
}

type scheduleCoverageSnapshot struct {
	EligibleAgentCount int
	TotalCapacity      int
	Warning            string
}

func buildBatchPreviewResult(candidates []batchScheduleCandidate, conflicts map[int]string, coverage map[int]scheduleCoverageSnapshot) *AgentTeamScheduleBatchPreviewResult {
	items := make([]AgentTeamScheduleBatchPreviewItem, 0, len(candidates))
	hasConflict := false
	for i, candidate := range candidates {
		conflictReason := conflicts[i]
		conflict := conflictReason != ""
		if conflict {
			hasConflict = true
		}
		items = append(items, AgentTeamScheduleBatchPreviewItem{
			TeamID:             candidate.TeamID,
			TeamName:           candidate.TeamName,
			SquadID:            candidate.SquadID,
			SquadName:          candidate.SquadName,
			Date:               candidate.Date,
			Weekday:            weekdayForBatchRequest(candidate.Date),
			StartAt:            candidate.StartAt,
			EndAt:              candidate.EndAt,
			Remark:             candidate.Remark,
			EligibleAgentCount: coverage[i].EligibleAgentCount,
			TotalCapacity:      coverage[i].TotalCapacity,
			CoverageWarning:    coverage[i].Warning,
			Conflict:           conflict,
			ConflictReason:     conflictReason,
		})
	}
	return &AgentTeamScheduleBatchPreviewResult{
		Total:    len(items),
		Conflict: hasConflict,
		Items:    items,
	}
}

func (s *agentTeamScheduleService) normalizeScheduleOverridesDB(db *gorm.DB, team *models.AgentTeam, includedProfileIDs, excludedProfileIDs []int64) ([]int64, []int64, error) {
	if db == nil || team == nil || team.ID <= 0 || team.TenantID <= 0 {
		return nil, nil, errorsx.InvalidParam("客服组不存在")
	}
	includedProfileIDs = uniquePositiveInt64s(includedProfileIDs)
	excludedProfileIDs = uniquePositiveInt64s(excludedProfileIDs)
	if len(includedProfileIDs) > maxAgentTeamScheduleOverrideAgents || len(excludedProfileIDs) > maxAgentTeamScheduleOverrideAgents {
		return nil, nil, errorsx.InvalidParam(fmt.Sprintf("单个班次临时加入或排除的客服均不能超过 %d 人", maxAgentTeamScheduleOverrideAgents))
	}
	for _, profileID := range includedProfileIDs {
		if slices.Contains(excludedProfileIDs, profileID) {
			return nil, nil, errorsx.InvalidParam("同一客服不能同时设为临时替班和请假排除")
		}
	}
	profileIDs := append(append([]int64(nil), includedProfileIDs...), excludedProfileIDs...)
	if len(profileIDs) > 0 {
		profiles := repositories.AgentProfileRepository.Find(db, sqls.NewCnd().
			Eq("tenant_id", team.TenantID).
			Eq("team_id", team.ID).
			In("id", profileIDs).
			Where("status <> ?", enums.StatusDeleted))
		if len(profiles) != len(profileIDs) {
			return nil, nil, errorsx.InvalidParam("临时替班或请假客服必须属于当前综合客服组")
		}
	}
	slices.Sort(includedProfileIDs)
	slices.Sort(excludedProfileIDs)
	return includedProfileIDs, excludedProfileIDs, nil
}

func (s *agentTeamScheduleService) buildBatchCoverageDB(db *gorm.DB, candidates []batchScheduleCandidate) map[int]scheduleCoverageSnapshot {
	ret := make(map[int]scheduleCoverageSnapshot, len(candidates))
	if db == nil || len(candidates) == 0 {
		return ret
	}
	teamIDs := make([]int64, 0, len(candidates))
	squadIDs := make([]int64, 0, len(candidates))
	tenantID := candidates[0].TenantID
	for _, candidate := range candidates {
		teamIDs = append(teamIDs, candidate.TeamID)
		if candidate.SquadID > 0 {
			squadIDs = append(squadIDs, candidate.SquadID)
		}
	}
	teams := repositories.AgentTeamRepository.Find(db, sqls.NewCnd().
		Eq("tenant_id", tenantID).
		In("id", uniquePositiveInt64s(teamIDs)).
		Eq("status", enums.StatusOk))
	teamByID := make(map[int64]models.AgentTeam, len(teams))
	for _, team := range teams {
		teamByID[team.ID] = team
	}
	profiles := repositories.AgentProfileRepository.Find(db, sqls.NewCnd().
		Eq("tenant_id", tenantID).
		In("team_id", uniquePositiveInt64s(teamIDs)).
		Eq("status", enums.StatusOk))
	userIDs := make([]int64, 0, len(profiles))
	for _, profile := range profiles {
		userIDs = append(userIDs, profile.UserID)
	}
	enabledUsers := []models.User(nil)
	if userIDs = uniquePositiveInt64s(userIDs); len(userIDs) > 0 {
		enabledUsers = repositories.UserRepository.Find(db, sqls.NewCnd().
			Eq("tenant_id", tenantID).
			In("id", userIDs).
			Eq("status", enums.StatusOk))
	}
	enabledUserSet := make(map[int64]struct{}, len(enabledUsers))
	for _, user := range enabledUsers {
		if user.DeletedAt == nil {
			enabledUserSet[user.ID] = struct{}{}
		}
	}
	permittedUserIDs, err := repositories.PermissionRepository.FindUserIDsWithAllCodes(db, userIDs, []string{
		constants.PermissionConversationView.Code,
		constants.PermissionConversationSend.Code,
	})
	if err != nil {
		for i := range candidates {
			ret[i] = scheduleCoverageSnapshot{Warning: "无法校验客服会话回复权限"}
		}
		return ret
	}
	permittedUserSet := int64Set(permittedUserIDs)
	membersBySquad, teamBySquad := AgentTeamSquadService.ActiveMemberProfileSet(uniquePositiveInt64s(squadIDs), tenantID)
	for i, candidate := range candidates {
		team, teamExists := teamByID[candidate.TeamID]
		automatic := teamExists && normalizedDispatchMode(team.DispatchMode) == enums.AgentTeamDispatchModeRule
		includedSet := int64Set(candidate.IncludedAgentProfileIDs)
		excludedSet := int64Set(candidate.ExcludedAgentProfileIDs)
		snapshot := scheduleCoverageSnapshot{}
		for _, profile := range profiles {
			if profile.TeamID != candidate.TeamID || (automatic && (!profile.AutoAssignEnabled || profile.MaxConcurrentCount <= 0)) {
				continue
			}
			if _, enabled := enabledUserSet[profile.UserID]; !enabled {
				continue
			}
			if _, permitted := permittedUserSet[profile.UserID]; !permitted {
				continue
			}
			if _, excluded := excludedSet[profile.ID]; excluded {
				continue
			}
			if candidate.SquadID > 0 {
				_, included := includedSet[profile.ID]
				_, member := membersBySquad[candidate.SquadID][profile.ID]
				if teamBySquad[candidate.SquadID] != candidate.TeamID || (!member && !included) {
					continue
				}
			}
			snapshot.EligibleAgentCount++
			if profile.MaxConcurrentCount > 0 {
				snapshot.TotalCapacity += profile.MaxConcurrentCount
			}
		}
		switch {
		case !teamExists:
			snapshot.Warning = "客服组不存在或已停用"
		case snapshot.EligibleAgentCount == 0 && automatic:
			snapshot.Warning = "该班次没有具备会话回复权限且可自动接单的客服"
		case snapshot.EligibleAgentCount == 0:
			snapshot.Warning = "该班次没有具备会话回复权限且可人工接待的客服"
		case snapshot.EligibleAgentCount == 1 && automatic:
			snapshot.Warning = "该班次仅一名可自动接单客服，存在单点值班风险"
		case snapshot.EligibleAgentCount == 1:
			snapshot.Warning = "该班次仅一名可人工接待客服，存在单点值班风险"
		}
		ret[i] = snapshot
	}
	return ret
}

func int64Set(values []int64) map[int64]struct{} {
	ret := make(map[int64]struct{}, len(values))
	for _, value := range values {
		if value > 0 {
			ret[value] = struct{}{}
		}
	}
	return ret
}

func (s *agentTeamScheduleService) validateScheduleSquadDB(db *gorm.DB, teamID, squadID int64) error {
	if squadID <= 0 {
		return nil
	}
	squad := repositories.AgentTeamSquadRepository.Get(db, squadID)
	if squad == nil || squad.Status != enums.StatusOk {
		return errorsx.InvalidParam("客服小组不存在或已停用")
	}
	if squad.TeamID != teamID {
		return errorsx.InvalidParam("客服小组不属于所选综合客服组")
	}
	team := repositories.AgentTeamRepository.Get(db, teamID)
	if team == nil || team.TenantID <= 0 || squad.TenantID != team.TenantID {
		return errorsx.InvalidParam("客服小组与综合客服组的接入公司不一致")
	}
	return nil
}

func (s *agentTeamScheduleService) findBatchConflict(candidates []batchScheduleCandidate) map[int]string {
	return s.findBatchConflictByDB(sqls.DB(), candidates)
}

func (s *agentTeamScheduleService) findBatchConflictByDB(db *gorm.DB, candidates []batchScheduleCandidate) map[int]string {
	conflicts := make(map[int]string)
	if len(candidates) == 0 {
		return conflicts
	}
	teamIDs := make([]int64, 0, len(candidates))
	startAt := candidates[0].StartAt
	endAt := candidates[0].EndAt
	for _, candidate := range candidates {
		teamIDs = append(teamIDs, candidate.TeamID)
		if candidate.StartAt.Before(startAt) {
			startAt = candidate.StartAt
		}
		if candidate.EndAt.After(endAt) {
			endAt = candidate.EndAt
		}
	}
	existing := repositories.AgentTeamScheduleRepository.FindOverlappingByTeamIDsAndTimeRange(db, uniquePositiveInt64s(teamIDs), startAt, endAt)
	for i, candidate := range candidates {
		for _, item := range existing {
			if item.TeamID != candidate.TeamID {
				continue
			}
			if item.StartAt.Before(candidate.EndAt) && item.EndAt.After(candidate.StartAt) && agentTeamSchedulesConflict(item.SquadID, candidate.SquadID) {
				conflicts[i] = fmt.Sprintf("该客服组在 %s 至 %s 已存在排班", item.StartAt.Format(time.DateTime), item.EndAt.Format(time.DateTime))
				break
			}
		}
	}
	for i := 0; i < len(candidates); i++ {
		for j := i + 1; j < len(candidates); j++ {
			left := candidates[i]
			right := candidates[j]
			if left.TeamID != right.TeamID || !left.StartAt.Before(right.EndAt) || !left.EndAt.After(right.StartAt) || !agentTeamSchedulesConflict(left.SquadID, right.SquadID) {
				continue
			}
			if conflicts[i] == "" {
				conflicts[i] = fmt.Sprintf("本次批量排班与 %s 至 %s 的待生成班次重叠", right.StartAt.Format(time.DateTime), right.EndAt.Format(time.DateTime))
			}
			if conflicts[j] == "" {
				conflicts[j] = fmt.Sprintf("本次批量排班与 %s 至 %s 的待生成班次重叠", left.StartAt.Format(time.DateTime), left.EndAt.Format(time.DateTime))
			}
		}
	}
	return conflicts
}

func normalizeBatchWeekdays(values []int) ([]int, error) {
	seen := make(map[int]struct{}, len(values))
	ret := make([]int, 0, len(values))
	for _, value := range values {
		if value < 1 || value > 7 {
			return nil, errorsx.InvalidParam("星期必须在 1 到 7 之间")
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		ret = append(ret, value)
	}
	if len(ret) == 0 {
		return nil, errorsx.InvalidParam("请选择星期")
	}
	return ret, nil
}

func weekdayForBatchRequest(value time.Time) int {
	if value.Weekday() == time.Sunday {
		return 7
	}
	return int(value.Weekday())
}

func uniquePositiveInt64s(values []int64) []int64 {
	seen := make(map[int64]struct{}, len(values))
	ret := make([]int64, 0, len(values))
	for _, value := range values {
		if value <= 0 {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		ret = append(ret, value)
	}
	return ret
}

func parseRequiredDateTime(value, message string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, errorsx.InvalidParam(message)
	}
	ret, err := parseDateTimeValue(value)
	if err != nil {
		return time.Time{}, errorsx.InvalidParam(message + "，请使用 yyyy-MM-dd HH:mm:ss 或 RFC3339")
	}
	return ret, nil
}

func parseDateTimeValue(value string) (time.Time, error) {
	layouts := []string{
		time.DateTime,
		time.RFC3339,
		"2006-01-02T15:04",
		"2006-01-02T15:04:05",
	}
	for _, layout := range layouts {
		if ret, err := time.ParseInLocation(layout, value, time.Local); err == nil {
			return ret, nil
		}
	}
	return time.Time{}, errorsx.InvalidParam("时间格式错误")
}

func startOfLocalDay(value time.Time) time.Time {
	year, month, day := value.In(time.Local).Date()
	return time.Date(year, month, day, 0, 0, 0, 0, time.Local)
}

func (s *agentTeamScheduleService) reconcileDispatchAfterScheduleChange(previous, current *models.AgentTeamSchedule) {
	now := time.Now()
	active := func(item *models.AgentTeamSchedule) bool {
		return item != nil && item.Status == enums.StatusOk && !item.StartAt.After(now) && item.EndAt.After(now)
	}
	teamIDsByTenant := make(map[int64][]int64)
	recoverTenant := make(map[int64]bool)
	for _, item := range []*models.AgentTeamSchedule{previous, current} {
		if item == nil || item.TenantID <= 0 || item.TeamID <= 0 {
			continue
		}
		teamIDsByTenant[item.TenantID] = append(teamIDsByTenant[item.TenantID], item.TeamID)
		if active(item) {
			recoverTenant[item.TenantID] = true
		}
	}
	for tenantID, teamIDs := range teamIDsByTenant {
		teamIDs = uniquePositiveInt64s(teamIDs)
		if recoverTenant[tenantID] {
			ConversationDispatchService.ReconcileConfigurationChange(tenantID, teamIDs...)
			continue
		}
		for _, teamID := range teamIDs {
			ConversationDispatchService.ScheduleTeamDispatch(tenantID, teamID)
		}
	}
}
