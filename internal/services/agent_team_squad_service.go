package services

import (
	"slices"
	"strings"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/errorsx"
	"agent-desk/internal/pkg/utils"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

var AgentTeamSquadService = &agentTeamSquadService{}

type agentTeamSquadService struct{}

type AgentTeamSquadOverview struct {
	Squad            models.AgentTeamSquad
	LeaderName       string
	MemberProfileIDs []int64
	Manageable       bool
	ActiveSchedule   *models.AgentTeamSchedule
	NextSchedule     *models.AgentTeamSchedule
}

func (s *agentTeamSquadService) Get(id int64) *models.AgentTeamSquad {
	return repositories.AgentTeamSquadRepository.Get(sqls.DB(), id)
}

func (s *agentTeamSquadService) GetInTenant(id int64, operator *dto.AuthPrincipal) *models.AgentTeamSquad {
	return repositories.AgentTeamSquadRepository.GetInTenant(sqls.DB(), id, AgentTeamScopeService.ActiveTenantID(operator))
}

func (s *agentTeamSquadService) FindByTeamIDs(teamIDs []int64) []models.AgentTeamSquad {
	teamIDs = uniquePositive(teamIDs)
	if len(teamIDs) == 0 {
		return []models.AgentTeamSquad{}
	}
	return repositories.AgentTeamSquadRepository.Find(sqls.DB(), sqls.NewCnd().In("team_id", teamIDs).Where("status <> ?", enums.StatusDeleted).Asc("id"))
}

func (s *agentTeamSquadService) CountByTeamID(teamID int64) int {
	if teamID <= 0 {
		return 0
	}
	return len(repositories.AgentTeamSquadRepository.Find(sqls.DB(), sqls.NewCnd().Eq("team_id", teamID).Where("status <> ?", enums.StatusDeleted)))
}

func (s *agentTeamSquadService) CountByTeamIDInTenant(teamID, tenantID int64) int {
	if teamID <= 0 || tenantID <= 0 {
		return 0
	}
	return len(repositories.AgentTeamSquadRepository.Find(sqls.DB(), sqls.NewCnd().Eq("tenant_id", tenantID).Eq("team_id", teamID).Where("status <> ?", enums.StatusDeleted)))
}

func (s *agentTeamSquadService) ListByTeam(teamID int64, operator *dto.AuthPrincipal) ([]AgentTeamSquadOverview, error) {
	if !s.canViewTeam(teamID, operator) {
		return nil, errorsx.Forbidden("无权查看该客服组的小组")
	}
	tenantID := AgentTeamScopeService.ActiveTenantID(operator)
	squads := repositories.AgentTeamSquadRepository.Find(sqls.DB(), sqls.NewCnd().Eq("tenant_id", tenantID).Eq("team_id", teamID).Where("status <> ?", enums.StatusDeleted).Asc("id"))
	if len(squads) == 0 {
		return []AgentTeamSquadOverview{}, nil
	}
	squadIDs := make([]int64, 0, len(squads))
	leaderUserIDs := make([]int64, 0, len(squads))
	for i := range squads {
		squadIDs = append(squadIDs, squads[i].ID)
		leaderUserIDs = appendPositive(leaderUserIDs, squads[i].LeaderUserID)
	}
	members := repositories.AgentTeamSquadMemberRepository.Find(sqls.DB(), sqls.NewCnd().Eq("tenant_id", tenantID).In("squad_id", squadIDs).Eq("status", enums.StatusOk).Asc("agent_profile_id"))
	membersBySquad := make(map[int64][]int64, len(squads))
	for i := range members {
		membersBySquad[members[i].SquadID] = append(membersBySquad[members[i].SquadID], members[i].AgentProfileID)
	}
	leaders := UserService.FindByIdsInTenant(uniquePositive(leaderUserIDs), tenantID)
	leaderNames := make(map[int64]string, len(leaders))
	for i := range leaders {
		name := strings.TrimSpace(leaders[i].Nickname)
		if name == "" {
			name = leaders[i].Username
		}
		leaderNames[leaders[i].ID] = name
	}
	now := time.Now()
	schedules := AgentTeamScheduleService.Find(sqls.NewCnd().Eq("tenant_id", tenantID).In("squad_id", squadIDs).Eq("status", enums.StatusOk).Gt("end_at", now).Asc("start_at"))
	activeBySquad := make(map[int64]*models.AgentTeamSchedule, len(squads))
	nextBySquad := make(map[int64]*models.AgentTeamSchedule, len(squads))
	for i := range schedules {
		item := schedules[i]
		if !item.StartAt.After(now) && item.EndAt.After(now) {
			if activeBySquad[item.SquadID] == nil {
				activeBySquad[item.SquadID] = &item
			}
			continue
		}
		if item.StartAt.After(now) && nextBySquad[item.SquadID] == nil {
			nextBySquad[item.SquadID] = &item
		}
	}
	manageable := AgentTeamScopeService.CanManageTeam(operator, teamID)
	ret := make([]AgentTeamSquadOverview, 0, len(squads))
	for i := range squads {
		ret = append(ret, AgentTeamSquadOverview{
			Squad:            squads[i],
			LeaderName:       leaderNames[squads[i].LeaderUserID],
			MemberProfileIDs: membersBySquad[squads[i].ID],
			Manageable:       manageable,
			ActiveSchedule:   activeBySquad[squads[i].ID],
			NextSchedule:     nextBySquad[squads[i].ID],
		})
	}
	return ret, nil
}

func (s *agentTeamSquadService) Create(req request.CreateAgentTeamSquadRequest, operator *dto.AuthPrincipal) (*models.AgentTeamSquad, error) {
	var item *models.AgentTeamSquad
	err := sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		teams, err := AgentTeamScopeService.lockManageableTeamsDB(ctx.Tx, []int64{req.TeamID}, operator, "无权在该客服组增设小组")
		if err != nil {
			return err
		}
		team := teams[req.TeamID]
		var memberIDs []int64
		item, memberIDs, err = s.buildModelDB(ctx.Tx, team, 0, req.Name, req.LeaderUserID, req.MemberIDs, req.Status, req.Remark)
		if err != nil {
			return err
		}
		item.AuditFields = utils.BuildAuditFields(operator)
		if err := repositories.AgentTeamSquadRepository.Create(ctx.Tx, item); err != nil {
			return err
		}
		return s.replaceMembersDB(ctx.Tx, team, item, memberIDs, operator)
	})
	if err != nil {
		return nil, err
	}
	return item, nil
}

func (s *agentTeamSquadService) Update(req request.UpdateAgentTeamSquadRequest, operator *dto.AuthPrincipal) error {
	return sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		current, err := repositories.AgentTeamSquadRepository.GetForUpdateInTenant(ctx.Tx, req.ID, AgentTeamScopeService.ActiveTenantID(operator))
		if err != nil {
			return err
		}
		if current == nil || current.Status == enums.StatusDeleted {
			return errorsx.InvalidParam("客服小组不存在")
		}
		if req.TeamID != current.TeamID {
			return errorsx.InvalidParam("客服小组不支持变更所属综合客服组")
		}
		teams, err := AgentTeamScopeService.lockManageableTeamsDB(ctx.Tx, []int64{current.TeamID}, operator, "无权编辑该客服小组")
		if err != nil {
			return err
		}
		team := teams[current.TeamID]
		item, memberIDs, err := s.buildModelDB(ctx.Tx, team, req.ID, req.Name, req.LeaderUserID, req.MemberIDs, req.Status, req.Remark)
		if err != nil {
			return err
		}
		if current.Status != enums.StatusDisabled && item.Status == enums.StatusDisabled && s.hasCurrentOrFutureScheduleDB(ctx.Tx, item.ID, current.TenantID) {
			return errorsx.Forbidden("客服小组仍有当前或未来排班，无法停用")
		}
		if err := repositories.AgentTeamSquadRepository.UpdatesInTenant(ctx.Tx, req.ID, current.TenantID, map[string]any{
			"tenant_id":        item.TenantID,
			"team_id":          item.TeamID,
			"name":             item.Name,
			"leader_user_id":   item.LeaderUserID,
			"status":           item.Status,
			"remark":           item.Remark,
			"updated_at":       time.Now(),
			"update_user_id":   operator.UserID,
			"update_user_name": operator.Username,
		}); err != nil {
			return err
		}
		return s.replaceMembersDB(ctx.Tx, team, item, memberIDs, operator)
	})
}

func (s *agentTeamSquadService) ReplaceMembers(req request.ReplaceAgentTeamSquadMembersRequest, operator *dto.AuthPrincipal) error {
	return sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		item, err := repositories.AgentTeamSquadRepository.GetForUpdateInTenant(ctx.Tx, req.SquadID, AgentTeamScopeService.ActiveTenantID(operator))
		if err != nil {
			return err
		}
		if item == nil || item.Status == enums.StatusDeleted {
			return errorsx.InvalidParam("客服小组不存在")
		}
		teams, err := AgentTeamScopeService.lockManageableTeamsDB(ctx.Tx, []int64{item.TeamID}, operator, "无权调整该客服小组成员")
		if err != nil {
			return err
		}
		return s.replaceMembersDB(ctx.Tx, teams[item.TeamID], item, req.AgentProfileIDs, operator)
	})
}

func (s *agentTeamSquadService) Delete(id int64, operator *dto.AuthPrincipal) error {
	return sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		item, err := repositories.AgentTeamSquadRepository.GetForUpdateInTenant(ctx.Tx, id, AgentTeamScopeService.ActiveTenantID(operator))
		if err != nil {
			return err
		}
		if item == nil || item.Status == enums.StatusDeleted {
			return errorsx.InvalidParam("客服小组不存在")
		}
		if _, err := AgentTeamScopeService.lockManageableTeamsDB(ctx.Tx, []int64{item.TeamID}, operator, "无权删除该客服小组"); err != nil {
			return err
		}
		if s.hasCurrentOrFutureScheduleDB(ctx.Tx, id, item.TenantID) {
			return errorsx.Forbidden("客服小组仍有当前或未来排班，无法删除")
		}
		now := time.Now()
		if err := repositories.AgentTeamSquadRepository.UpdatesInTenant(ctx.Tx, id, item.TenantID, map[string]any{
			"status":           enums.StatusDeleted,
			"updated_at":       now,
			"update_user_id":   operator.UserID,
			"update_user_name": operator.Username,
		}); err != nil {
			return err
		}
		return repositories.AgentTeamSquadMemberRepository.UpdatesActiveBySquadInTenant(ctx.Tx, id, item.TenantID,
			map[string]any{"status": enums.StatusDeleted, "updated_at": now, "update_user_id": operator.UserID, "update_user_name": operator.Username})
	})
}

func (s *agentTeamSquadService) ActiveMemberProfileSet(squadIDs []int64, tenantID int64) (map[int64]map[int64]struct{}, map[int64]int64) {
	squadIDs = uniquePositive(squadIDs)
	ret := make(map[int64]map[int64]struct{}, len(squadIDs))
	teamBySquad := make(map[int64]int64, len(squadIDs))
	if len(squadIDs) == 0 || tenantID <= 0 {
		return ret, teamBySquad
	}
	activeSquads := repositories.AgentTeamSquadRepository.Find(sqls.DB(), sqls.NewCnd().Eq("tenant_id", tenantID).In("id", squadIDs).Eq("status", enums.StatusOk))
	activeSquadIDs := make([]int64, 0, len(activeSquads))
	for i := range activeSquads {
		activeSquadIDs = append(activeSquadIDs, activeSquads[i].ID)
		ret[activeSquads[i].ID] = make(map[int64]struct{})
		teamBySquad[activeSquads[i].ID] = activeSquads[i].TeamID
	}
	if len(activeSquadIDs) == 0 {
		return ret, teamBySquad
	}
	members := repositories.AgentTeamSquadMemberRepository.Find(sqls.DB(), sqls.NewCnd().Eq("tenant_id", tenantID).In("squad_id", activeSquadIDs).Eq("status", enums.StatusOk))
	for i := range members {
		ret[members[i].SquadID][members[i].AgentProfileID] = struct{}{}
	}
	return ret, teamBySquad
}

func (s *agentTeamSquadService) hasCurrentOrFutureScheduleDB(db *gorm.DB, squadID, tenantID int64) bool {
	return repositories.AgentTeamScheduleRepository.Take(db, "tenant_id = ? AND squad_id = ? AND status = ? AND end_at > ?", tenantID, squadID, enums.StatusOk, time.Now()) != nil
}

func (s *agentTeamSquadService) canViewTeam(teamID int64, operator *dto.AuthPrincipal) bool {
	tenantID := AgentTeamScopeService.ActiveTenantID(operator)
	team := repositories.AgentTeamRepository.GetInTenant(sqls.DB(), teamID, tenantID)
	if team == nil || team.Status == enums.StatusDeleted {
		return false
	}
	if AgentTeamScopeService.CanManageTeam(operator, teamID) {
		return true
	}
	if operator == nil {
		return false
	}
	profile := AgentProfileService.GetByUserID(operator.UserID)
	return profile != nil && profile.TenantID == tenantID && profile.Status != enums.StatusDeleted && profile.TeamID == teamID
}

func (s *agentTeamSquadService) buildModelDB(db *gorm.DB, team *models.AgentTeam, id int64, name string, leaderUserID int64, memberIDs []int64, status int, remark string) (*models.AgentTeamSquad, []int64, error) {
	if team == nil || team.Status == enums.StatusDeleted {
		return nil, nil, errorsx.InvalidParam("综合客服组不存在")
	}
	if team.TenantID <= 0 {
		return nil, nil, errorsx.InvalidParam("综合客服组尚未归属接入公司")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, nil, errorsx.InvalidParam("客服小组名称不能为空")
	}
	if status != int(enums.StatusOk) && status != int(enums.StatusDisabled) {
		return nil, nil, errorsx.InvalidParam("客服小组状态不合法")
	}
	duplicate := repositories.AgentTeamSquadRepository.Take(db, "tenant_id = ? AND team_id = ? AND name = ? AND status <> ? AND id <> ?", team.TenantID, team.ID, name, enums.StatusDeleted, id)
	if duplicate != nil {
		return nil, nil, errorsx.InvalidParam("同一综合客服组内小组名称不能重复")
	}
	memberIDs, err := s.validateMemberProfilesDB(db, team, leaderUserID, memberIDs)
	if err != nil {
		return nil, nil, err
	}
	return &models.AgentTeamSquad{ID: id, TenantID: team.TenantID, TeamID: team.ID, Name: name, LeaderUserID: leaderUserID, Status: enums.Status(status), Remark: strings.TrimSpace(remark)}, memberIDs, nil
}

func (s *agentTeamSquadService) validateMemberProfilesDB(db *gorm.DB, team *models.AgentTeam, leaderUserID int64, memberIDs []int64) ([]int64, error) {
	if team == nil || team.TenantID <= 0 || team.Status == enums.StatusDeleted {
		return nil, errorsx.InvalidParam("综合客服组不存在或尚未归属接入公司")
	}
	memberIDs = uniquePositive(memberIDs)
	if leaderUserID > 0 {
		leaderProfile := repositories.AgentProfileRepository.Take(db, "tenant_id = ? AND user_id = ? AND status <> ?", team.TenantID, leaderUserID, enums.StatusDeleted)
		if leaderProfile == nil || leaderProfile.TeamID != team.ID || leaderProfile.TenantID != team.TenantID {
			return nil, errorsx.InvalidParam("小组负责人必须是综合客服组内客服")
		}
		memberIDs = append(memberIDs, leaderProfile.ID)
	}
	memberIDs = uniquePositive(memberIDs)
	if len(memberIDs) == 0 {
		return []int64{}, nil
	}
	profiles := repositories.AgentProfileRepository.Find(db, sqls.NewCnd().Eq("tenant_id", team.TenantID).In("id", memberIDs).Where("status <> ?", enums.StatusDeleted))
	if len(profiles) != len(memberIDs) {
		return nil, errorsx.InvalidParam("部分客服档案不存在或已删除")
	}
	for i := range profiles {
		if profiles[i].TeamID != team.ID || profiles[i].TenantID != team.TenantID {
			return nil, errorsx.InvalidParam("客服小组只能包含所属综合客服组内客服")
		}
	}
	slices.Sort(memberIDs)
	return memberIDs, nil
}

func (s *agentTeamSquadService) replaceMembersDB(db *gorm.DB, team *models.AgentTeam, squad *models.AgentTeamSquad, memberIDs []int64, operator *dto.AuthPrincipal) error {
	if team == nil || team.TenantID <= 0 || squad.TeamID != team.ID || squad.TenantID != team.TenantID {
		return errorsx.InvalidParam("客服小组与综合客服组的接入公司不一致")
	}
	memberIDs, err := s.validateMemberProfilesDB(db, team, squad.LeaderUserID, memberIDs)
	if err != nil {
		return err
	}
	now := time.Now()
	if err := repositories.AgentTeamSquadMemberRepository.UpdatesActiveBySquadInTenant(db, squad.ID, squad.TenantID,
		map[string]any{"status": enums.StatusDeleted, "updated_at": now, "update_user_id": operator.UserID, "update_user_name": operator.Username}); err != nil {
		return err
	}
	for _, profileID := range memberIDs {
		current := repositories.AgentTeamSquadMemberRepository.Take(db, "tenant_id = ? AND squad_id = ? AND agent_profile_id = ?", squad.TenantID, squad.ID, profileID)
		if current != nil {
			if err := repositories.AgentTeamSquadMemberRepository.UpdatesInTenant(db, current.ID, squad.TenantID, map[string]any{
				"tenant_id": squad.TenantID, "status": enums.StatusOk, "updated_at": now, "update_user_id": operator.UserID, "update_user_name": operator.Username,
			}); err != nil {
				return err
			}
			continue
		}
		item := &models.AgentTeamSquadMember{TenantID: squad.TenantID, SquadID: squad.ID, AgentProfileID: profileID, Status: enums.StatusOk, AuditFields: utils.BuildAuditFields(operator)}
		if err := repositories.AgentTeamSquadMemberRepository.Create(db, item); err != nil {
			return err
		}
	}
	return nil
}
