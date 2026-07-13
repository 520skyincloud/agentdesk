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

func (s *agentTeamSquadService) ListByTeam(teamID int64, operator *dto.AuthPrincipal) ([]AgentTeamSquadOverview, error) {
	if !s.canViewTeam(teamID, operator) {
		return nil, errorsx.Forbidden("无权查看该客服组的小组")
	}
	squads := repositories.AgentTeamSquadRepository.Find(sqls.DB(), sqls.NewCnd().Eq("team_id", teamID).Where("status <> ?", enums.StatusDeleted).Asc("id"))
	if len(squads) == 0 {
		return []AgentTeamSquadOverview{}, nil
	}
	squadIDs := make([]int64, 0, len(squads))
	leaderUserIDs := make([]int64, 0, len(squads))
	for i := range squads {
		squadIDs = append(squadIDs, squads[i].ID)
		leaderUserIDs = appendPositive(leaderUserIDs, squads[i].LeaderUserID)
	}
	members := repositories.AgentTeamSquadMemberRepository.Find(sqls.DB(), sqls.NewCnd().In("squad_id", squadIDs).Eq("status", enums.StatusOk).Asc("agent_profile_id"))
	membersBySquad := make(map[int64][]int64, len(squads))
	for i := range members {
		membersBySquad[members[i].SquadID] = append(membersBySquad[members[i].SquadID], members[i].AgentProfileID)
	}
	leaders := UserService.FindByIds(uniquePositive(leaderUserIDs))
	leaderNames := make(map[int64]string, len(leaders))
	for i := range leaders {
		name := strings.TrimSpace(leaders[i].Nickname)
		if name == "" {
			name = leaders[i].Username
		}
		leaderNames[leaders[i].ID] = name
	}
	now := time.Now()
	schedules := AgentTeamScheduleService.Find(sqls.NewCnd().In("squad_id", squadIDs).Eq("status", enums.StatusOk).Gt("end_at", now).Asc("start_at"))
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
	if !AgentTeamScopeService.CanManageTeam(operator, req.TeamID) {
		return nil, errorsx.Forbidden("无权在该客服组增设小组")
	}
	item, memberIDs, err := s.buildModel(sqls.DB(), 0, req.TeamID, req.Name, req.LeaderUserID, req.MemberIDs, req.Status, req.Remark)
	if err != nil {
		return nil, err
	}
	item.AuditFields = utils.BuildAuditFields(operator)
	if err := sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		if err := repositories.AgentTeamSquadRepository.Create(ctx.Tx, item); err != nil {
			return err
		}
		return s.replaceMembersDB(ctx.Tx, item, memberIDs, operator)
	}); err != nil {
		return nil, err
	}
	return item, nil
}

func (s *agentTeamSquadService) Update(req request.UpdateAgentTeamSquadRequest, operator *dto.AuthPrincipal) error {
	current := s.Get(req.ID)
	if current == nil || current.Status == enums.StatusDeleted {
		return errorsx.InvalidParam("客服小组不存在")
	}
	if !AgentTeamScopeService.CanManageTeam(operator, current.TeamID) || !AgentTeamScopeService.CanManageTeam(operator, req.TeamID) {
		return errorsx.Forbidden("无权编辑该客服小组")
	}
	item, memberIDs, err := s.buildModel(sqls.DB(), req.ID, req.TeamID, req.Name, req.LeaderUserID, req.MemberIDs, req.Status, req.Remark)
	if err != nil {
		return err
	}
	return sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		if err := repositories.AgentTeamSquadRepository.Updates(ctx.Tx, req.ID, map[string]any{
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
		return s.replaceMembersDB(ctx.Tx, item, memberIDs, operator)
	})
}

func (s *agentTeamSquadService) ReplaceMembers(req request.ReplaceAgentTeamSquadMembersRequest, operator *dto.AuthPrincipal) error {
	item := s.Get(req.SquadID)
	if item == nil || item.Status == enums.StatusDeleted {
		return errorsx.InvalidParam("客服小组不存在")
	}
	if !AgentTeamScopeService.CanManageTeam(operator, item.TeamID) {
		return errorsx.Forbidden("无权调整该客服小组成员")
	}
	memberIDs, err := s.validateMemberProfilesDB(sqls.DB(), item.TeamID, item.LeaderUserID, req.AgentProfileIDs)
	if err != nil {
		return err
	}
	return sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		return s.replaceMembersDB(ctx.Tx, item, memberIDs, operator)
	})
}

func (s *agentTeamSquadService) Delete(id int64, operator *dto.AuthPrincipal) error {
	item := s.Get(id)
	if item == nil || item.Status == enums.StatusDeleted {
		return errorsx.InvalidParam("客服小组不存在")
	}
	if !AgentTeamScopeService.CanManageTeam(operator, item.TeamID) {
		return errorsx.Forbidden("无权删除该客服小组")
	}
	if AgentTeamScheduleService.FindOne(sqls.NewCnd().Eq("squad_id", id).Eq("status", enums.StatusOk).Gt("end_at", time.Now())) != nil {
		return errorsx.Forbidden("客服小组仍有当前或未来排班，无法删除")
	}
	return sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		now := time.Now()
		if err := repositories.AgentTeamSquadRepository.Updates(ctx.Tx, id, map[string]any{
			"status":           enums.StatusDeleted,
			"updated_at":       now,
			"update_user_id":   operator.UserID,
			"update_user_name": operator.Username,
		}); err != nil {
			return err
		}
		return ctx.Tx.Model(&models.AgentTeamSquadMember{}).
			Where("squad_id = ? AND status <> ?", id, enums.StatusDeleted).
			Updates(map[string]any{"status": enums.StatusDeleted, "updated_at": now, "update_user_id": operator.UserID, "update_user_name": operator.Username}).Error
	})
}

func (s *agentTeamSquadService) ActiveMemberProfileSet(squadIDs []int64) map[int64]map[int64]struct{} {
	squadIDs = uniquePositive(squadIDs)
	ret := make(map[int64]map[int64]struct{}, len(squadIDs))
	if len(squadIDs) == 0 {
		return ret
	}
	members := repositories.AgentTeamSquadMemberRepository.Find(sqls.DB(), sqls.NewCnd().In("squad_id", squadIDs).Eq("status", enums.StatusOk))
	for i := range members {
		if ret[members[i].SquadID] == nil {
			ret[members[i].SquadID] = make(map[int64]struct{})
		}
		ret[members[i].SquadID][members[i].AgentProfileID] = struct{}{}
	}
	return ret
}

func (s *agentTeamSquadService) canViewTeam(teamID int64, operator *dto.AuthPrincipal) bool {
	if AgentTeamScopeService.CanManageTeam(operator, teamID) {
		return true
	}
	if operator == nil {
		return false
	}
	profile := AgentProfileService.GetByUserID(operator.UserID)
	return profile != nil && profile.Status != enums.StatusDeleted && profile.TeamID == teamID
}

func (s *agentTeamSquadService) buildModel(db *gorm.DB, id, teamID int64, name string, leaderUserID int64, memberIDs []int64, status int, remark string) (*models.AgentTeamSquad, []int64, error) {
	team := repositories.AgentTeamRepository.Get(db, teamID)
	if team == nil || team.Status == enums.StatusDeleted {
		return nil, nil, errorsx.InvalidParam("综合客服组不存在")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, nil, errorsx.InvalidParam("客服小组名称不能为空")
	}
	if status != int(enums.StatusOk) && status != int(enums.StatusDisabled) {
		return nil, nil, errorsx.InvalidParam("客服小组状态不合法")
	}
	duplicate := repositories.AgentTeamSquadRepository.Take(db, "team_id = ? AND name = ? AND status <> ? AND id <> ?", teamID, name, enums.StatusDeleted, id)
	if duplicate != nil {
		return nil, nil, errorsx.InvalidParam("同一综合客服组内小组名称不能重复")
	}
	memberIDs, err := s.validateMemberProfilesDB(db, teamID, leaderUserID, memberIDs)
	if err != nil {
		return nil, nil, err
	}
	return &models.AgentTeamSquad{ID: id, TeamID: teamID, Name: name, LeaderUserID: leaderUserID, Status: enums.Status(status), Remark: strings.TrimSpace(remark)}, memberIDs, nil
}

func (s *agentTeamSquadService) validateMemberProfilesDB(db *gorm.DB, teamID, leaderUserID int64, memberIDs []int64) ([]int64, error) {
	memberIDs = uniquePositive(memberIDs)
	if leaderUserID > 0 {
		leaderProfile := repositories.AgentProfileRepository.Take(db, "user_id = ? AND status <> ?", leaderUserID, enums.StatusDeleted)
		if leaderProfile == nil || leaderProfile.TeamID != teamID {
			return nil, errorsx.InvalidParam("小组负责人必须是综合客服组内客服")
		}
		memberIDs = append(memberIDs, leaderProfile.ID)
	}
	memberIDs = uniquePositive(memberIDs)
	if len(memberIDs) == 0 {
		return []int64{}, nil
	}
	profiles := repositories.AgentProfileRepository.Find(db, sqls.NewCnd().In("id", memberIDs).Where("status <> ?", enums.StatusDeleted))
	if len(profiles) != len(memberIDs) {
		return nil, errorsx.InvalidParam("部分客服档案不存在或已删除")
	}
	for i := range profiles {
		if profiles[i].TeamID != teamID {
			return nil, errorsx.InvalidParam("客服小组只能包含所属综合客服组内客服")
		}
	}
	slices.Sort(memberIDs)
	return memberIDs, nil
}

func (s *agentTeamSquadService) replaceMembersDB(db *gorm.DB, squad *models.AgentTeamSquad, memberIDs []int64, operator *dto.AuthPrincipal) error {
	memberIDs, err := s.validateMemberProfilesDB(db, squad.TeamID, squad.LeaderUserID, memberIDs)
	if err != nil {
		return err
	}
	now := time.Now()
	if err := db.Model(&models.AgentTeamSquadMember{}).
		Where("squad_id = ? AND status <> ?", squad.ID, enums.StatusDeleted).
		Updates(map[string]any{"status": enums.StatusDeleted, "updated_at": now, "update_user_id": operator.UserID, "update_user_name": operator.Username}).Error; err != nil {
		return err
	}
	for _, profileID := range memberIDs {
		current := repositories.AgentTeamSquadMemberRepository.Take(db, "squad_id = ? AND agent_profile_id = ?", squad.ID, profileID)
		if current != nil {
			if err := repositories.AgentTeamSquadMemberRepository.Updates(db, current.ID, map[string]any{
				"status": enums.StatusOk, "updated_at": now, "update_user_id": operator.UserID, "update_user_name": operator.Username,
			}); err != nil {
				return err
			}
			continue
		}
		item := &models.AgentTeamSquadMember{SquadID: squad.ID, AgentProfileID: profileID, Status: enums.StatusOk, AuditFields: utils.BuildAuditFields(operator)}
		if err := repositories.AgentTeamSquadMemberRepository.Create(db, item); err != nil {
			return err
		}
	}
	return nil
}
