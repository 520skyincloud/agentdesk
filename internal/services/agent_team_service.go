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
	"slices"
	"strings"
	"time"

	"agent-desk/internal/pkg/httpx/params"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

var AgentTeamService = newAgentTeamService()

func newAgentTeamService() *agentTeamService {
	return &agentTeamService{}
}

type agentTeamService struct {
}

func (s *agentTeamService) Get(id int64) *models.AgentTeam {
	return repositories.AgentTeamRepository.Get(sqls.DB(), id)
}

func (s *agentTeamService) GetInTenant(id int64, operator *dto.AuthPrincipal) *models.AgentTeam {
	return repositories.AgentTeamRepository.GetInTenant(sqls.DB(), id, AgentTeamScopeService.ActiveTenantID(operator))
}

func (s *agentTeamService) GetByTenantID(id, tenantID int64) *models.AgentTeam {
	return repositories.AgentTeamRepository.GetInTenant(sqls.DB(), id, tenantID)
}

func (s *agentTeamService) Take(where ...interface{}) *models.AgentTeam {
	return repositories.AgentTeamRepository.Take(sqls.DB(), where...)
}

func (s *agentTeamService) Find(cnd *sqls.Cnd) []models.AgentTeam {
	return repositories.AgentTeamRepository.Find(sqls.DB(), cnd)
}

func (s *agentTeamService) FindInTenant(cnd *sqls.Cnd, operator *dto.AuthPrincipal) []models.AgentTeam {
	return repositories.AgentTeamRepository.Find(sqls.DB(), AgentTeamScopeService.ApplyTenantFilter(cnd, operator))
}

func (s *agentTeamService) FindOne(cnd *sqls.Cnd) *models.AgentTeam {
	return repositories.AgentTeamRepository.FindOne(sqls.DB(), cnd)
}

func (s *agentTeamService) FindPageByParams(params *params.QueryParams) (list []models.AgentTeam, paging *sqls.Paging) {
	return repositories.AgentTeamRepository.FindPageByParams(sqls.DB(), params)
}

func (s *agentTeamService) FindPageByCnd(cnd *sqls.Cnd) (list []models.AgentTeam, paging *sqls.Paging) {
	return repositories.AgentTeamRepository.FindPageByCnd(sqls.DB(), cnd)
}

func (s *agentTeamService) FindPageInTenant(cnd *sqls.Cnd, operator *dto.AuthPrincipal) (list []models.AgentTeam, paging *sqls.Paging) {
	return repositories.AgentTeamRepository.FindPageByCnd(sqls.DB(), AgentTeamScopeService.ApplyTenantFilter(cnd, operator))
}

func (s *agentTeamService) Count(cnd *sqls.Cnd) int64 {
	return repositories.AgentTeamRepository.Count(sqls.DB(), cnd)
}

func (s *agentTeamService) FindByIds(ids []int64) []models.AgentTeam {
	return repositories.AgentTeamRepository.FindByIds(sqls.DB(), ids)
}

func (s *agentTeamService) FindByIdsInTenant(ids []int64, tenantID int64) []models.AgentTeam {
	return repositories.AgentTeamRepository.FindByIdsInTenant(sqls.DB(), ids, tenantID)
}

func (s *agentTeamService) CreateAgentTeam(req request.CreateAgentTeamRequest, operator *dto.AuthPrincipal) (*models.AgentTeam, error) {
	if operator == nil {
		return nil, errorsx.Unauthorized("未登录或登录已过期")
	}
	if !AgentTeamScopeService.IsAdmin(operator) {
		return nil, errorsx.Forbidden("只有管理员可以创建客服组")
	}
	if operator.ActiveTenantID <= 0 {
		return nil, errorsx.Forbidden("请先进入需要创建客服组的接入公司")
	}
	item, err := s.buildTeamModel(0, operator.ActiveTenantID, req.Name, req.LeaderUserID, req.Status, req.Description, req.Remark)
	if err != nil {
		return nil, err
	}
	storeStaffUserIDs, _, err := s.resolveRequestedStoreStaffUserIDsDB(sqls.DB(), operator.ActiveTenantID, req.StoreStaffUserIDs, req.WxWorkInstanceScopeIDs)
	if err != nil {
		return nil, err
	}
	item.AuditFields = utils.BuildAuditFields(operator)
	if err := sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		if err := repositories.AgentTeamRepository.Create(ctx.Tx, item); err != nil {
			return err
		}
		return s.replaceStoreStaffBindingsDB(ctx.Tx, item.ID, storeStaffUserIDs, operator)
	}); err != nil {
		return nil, err
	}
	item = repositories.AgentTeamRepository.GetInTenant(sqls.DB(), item.ID, operator.ActiveTenantID)
	if item == nil {
		return nil, errorsx.BusinessError(0, "客服组创建成功，但读取最新数据失败")
	}
	return item, nil
}

func (s *agentTeamService) UpdateAgentTeam(req request.UpdateAgentTeamRequest, operator *dto.AuthPrincipal) error {
	if operator == nil {
		return errorsx.Unauthorized("未登录或登录已过期")
	}
	return sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		current, err := repositories.AgentTeamRepository.GetForUpdateInTenant(ctx.Tx, req.ID, AgentTeamScopeService.ActiveTenantID(operator))
		if err != nil {
			return err
		}
		if current == nil || current.Status == enums.StatusDeleted {
			return errorsx.InvalidParam("客服组不存在")
		}
		if !AgentTeamScopeService.canManageTeam(operator, current) {
			return errorsx.Forbidden("只能管理自己绑定的客服组")
		}
		if !AgentTeamScopeService.IsAdmin(operator) && req.LeaderUserID != current.LeaderUserID {
			return errorsx.Forbidden("客服组长不能变更客服组负责人")
		}
		item, err := s.buildTeamModelDB(ctx.Tx, req.ID, current.TenantID, req.Name, req.LeaderUserID, req.Status, req.Description, req.Remark)
		if err != nil {
			return err
		}
		storeStaffUserIDs, scopeProvided, err := s.resolveRequestedStoreStaffUserIDsDB(ctx.Tx, current.TenantID, req.StoreStaffUserIDs, req.WxWorkInstanceScopeIDs)
		if err != nil {
			return err
		}
		if err := repositories.AgentTeamRepository.UpdatesInTenant(ctx.Tx, req.ID, current.TenantID, map[string]any{
			"tenant_id":        current.TenantID,
			"name":             item.Name,
			"leader_user_id":   item.LeaderUserID,
			"status":           item.Status,
			"description":      item.Description,
			"remark":           item.Remark,
			"update_user_id":   operator.UserID,
			"update_user_name": operator.Username,
			"updated_at":       time.Now(),
		}); err != nil {
			return err
		}
		if !scopeProvided {
			return nil
		}
		return s.replaceStoreStaffBindingsDB(ctx.Tx, req.ID, storeStaffUserIDs, operator)
	})
}

func (s *agentTeamService) DeleteAgentTeam(id int64, operator *dto.AuthPrincipal) error {
	if operator == nil {
		return errorsx.Unauthorized("未登录或登录已过期")
	}
	return sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		current, err := repositories.AgentTeamRepository.GetForUpdateInTenant(ctx.Tx, id, AgentTeamScopeService.ActiveTenantID(operator))
		if err != nil {
			return err
		}
		if current == nil || current.Status == enums.StatusDeleted {
			return errorsx.InvalidParam("客服组不存在")
		}
		if !AgentTeamScopeService.canManageTeam(operator, current) {
			return errorsx.Forbidden("只能管理自己绑定的客服组")
		}
		if repositories.AgentProfileRepository.Take(ctx.Tx, "tenant_id = ? AND team_id = ?", current.TenantID, id) != nil {
			return errorsx.Forbidden("客服组下仍有关联客服档案，无法删除")
		}
		if repositories.AgentTeamSquadRepository.Take(ctx.Tx, "tenant_id = ? AND team_id = ? AND status <> ?", current.TenantID, id, enums.StatusDeleted) != nil {
			return errorsx.Forbidden("客服组下仍有关联客服小组，无法删除")
		}
		if repositories.WxWorkProtocolInstanceRepository.Take(ctx.Tx, "tenant_id = ? AND agent_team_id = ? AND status <> ?", current.TenantID, id, enums.StatusDeleted) != nil {
			return errorsx.Forbidden("客服组下仍有关联企微员工号，无法删除")
		}
		if repositories.StoreStaffBindingRepository.TakeInTenant(ctx.Tx, current.TenantID, "agent_team_id = ? AND status <> ?", id, enums.StatusDeleted) != nil {
			return errorsx.Forbidden("客服组下仍有关联门店员工，无法删除")
		}
		if repositories.AgentTeamScheduleRepository.Take(ctx.Tx, "tenant_id = ? AND team_id = ?", current.TenantID, id) != nil {
			return errorsx.Forbidden("客服组下仍有关联组排班，无法删除")
		}
		if repositories.AIAgentRepository.Take(
			ctx.Tx,
			"tenant_id = ? AND (team_ids = ? OR team_ids LIKE ? OR team_ids LIKE ? OR team_ids LIKE ?) AND status <> ?",
			current.TenantID,
			utils.JoinInt64s([]int64{id}),
			utils.JoinInt64s([]int64{id})+",%",
			"%,"+utils.JoinInt64s([]int64{id}),
			"%,"+utils.JoinInt64s([]int64{id})+",%",
			enums.StatusDeleted,
		) != nil {
			return errorsx.Forbidden("客服组下仍有关联 AI Agent，无法删除")
		}
		return repositories.AgentTeamRepository.UpdatesInTenant(ctx.Tx, id, current.TenantID, map[string]any{
			"status":           enums.StatusDeleted,
			"update_user_id":   operator.UserID,
			"update_user_name": operator.Username,
			"updated_at":       time.Now(),
		})
	})
}

func (s *agentTeamService) buildTeamModel(id, tenantID int64, name string, leaderUserID int64, status int, description, remark string) (*models.AgentTeam, error) {
	return s.buildTeamModelDB(sqls.DB(), id, tenantID, name, leaderUserID, status, description, remark)
}

func (s *agentTeamService) buildTeamModelDB(db *gorm.DB, id, tenantID int64, name string, leaderUserID int64, status int, description, remark string) (*models.AgentTeam, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, errorsx.InvalidParam("客服组名称不能为空")
	}
	if exists := repositories.AgentTeamRepository.Take(db, "tenant_id = ? AND name = ? AND status <> ? AND id <> ?", tenantID, name, enums.StatusDeleted, id); exists != nil {
		return nil, errorsx.InvalidParam("客服组名称已存在")
	}
	if leaderUserID > 0 {
		leader := repositories.UserRepository.Get(db, leaderUserID)
		if leader == nil || leader.Status != enums.StatusOk {
			return nil, errorsx.InvalidParam("组长用户不存在或已停用")
		}
		role := repositories.RoleRepository.GetByCode(db, constants.RoleCodeCsTeamLeader)
		if role == nil || role.Status != enums.StatusOk || repositories.UserRoleRepository.FindOne(db, sqls.NewCnd().Eq("user_id", leaderUserID).Eq("role_id", role.ID)) == nil {
			return nil, errorsx.InvalidParam("所选用户不是客服组长账号")
		}
		if tenantID > 0 && leader.TenantID != tenantID {
			return nil, errorsx.InvalidParam("客服组长账号与客服组必须属于同一接入公司")
		}
	}
	if status != 0 && status != 1 {
		return nil, errorsx.InvalidParam("客服组状态不合法")
	}
	return &models.AgentTeam{
		TenantID:               tenantID,
		Name:                   name,
		LeaderUserID:           leaderUserID,
		CompanyScopeIDs:        "",
		StoreScopeIDs:          "",
		WxWorkInstanceScopeIDs: "",
		Status:                 enums.Status(status),
		Description:            strings.TrimSpace(description),
		Remark:                 strings.TrimSpace(remark),
	}, nil
}

func (s *agentTeamService) deriveScopeFromWxWorkInstances(instanceIDs []int64) ([]int64, []int64, []int64, error) {
	return s.deriveScopeFromWxWorkInstancesDB(sqls.DB(), instanceIDs)
}

func (s *agentTeamService) deriveScopeFromWxWorkInstancesDB(db *gorm.DB, instanceIDs []int64) ([]int64, []int64, []int64, error) {
	instanceIDs = uniquePositive(instanceIDs)
	if len(instanceIDs) == 0 {
		return nil, nil, nil, nil
	}
	instances := repositories.WxWorkProtocolInstanceRepository.Find(db, sqls.NewCnd().In("id", instanceIDs).Where("status <> ?", enums.StatusDeleted))
	if len(instances) != len(instanceIDs) {
		return nil, nil, nil, errorsx.InvalidParam("部分企微员工号不存在或已删除，请重新选择")
	}
	companyIDs := make([]int64, 0, len(instances))
	storeIDs := make([]int64, 0, len(instances))
	for i := range instances {
		instance := instances[i]
		if instance.StoreID <= 0 {
			return nil, nil, nil, errorsx.InvalidParam("企微员工号未绑定门店，不能加入客服组")
		}
		store := repositories.StoreRepository.Get(db, instance.StoreID)
		if store == nil || store.Status == enums.StatusDeleted {
			return nil, nil, nil, errorsx.InvalidParam("企微员工号绑定的门店不存在或已删除")
		}
		storeIDs = append(storeIDs, store.ID)
		companyIDs = appendPositive(companyIDs, store.CompanyID)
		companyIDs = appendPositive(companyIDs, instance.CompanyID)
	}
	slices.Sort(companyIDs)
	slices.Sort(storeIDs)
	slices.Sort(instanceIDs)
	return uniquePositive(companyIDs), uniquePositive(storeIDs), instanceIDs, nil
}

func (s *agentTeamService) resolveRequestedStoreStaffUserIDsDB(db *gorm.DB, tenantID int64, storeStaffUserIDs, legacyInstanceIDs []int64) ([]int64, bool, error) {
	if tenantID <= 0 {
		return nil, false, errorsx.Forbidden("请先进入需要管理的接入公司")
	}
	if storeStaffUserIDs != nil {
		return uniquePositive(storeStaffUserIDs), true, nil
	}
	if legacyInstanceIDs == nil {
		return nil, false, nil
	}
	legacyInstanceIDs = uniquePositive(legacyInstanceIDs)
	if len(legacyInstanceIDs) == 0 {
		return []int64{}, true, nil
	}
	instances := repositories.WxWorkProtocolInstanceRepository.Find(db, sqls.NewCnd().Eq("tenant_id", tenantID).In("id", legacyInstanceIDs).Where("status <> ?", enums.StatusDeleted).Asc("id"))
	if len(instances) != len(legacyInstanceIDs) {
		return nil, false, errorsx.InvalidParam("部分企微员工号不存在或已删除，请重新选择")
	}
	userIDs := make([]int64, 0, len(instances))
	for i := range instances {
		instance := &instances[i]
		var binding *models.StoreStaffBinding
		if instance.StoreStaffBindingID > 0 {
			binding = repositories.StoreStaffBindingRepository.GetInTenant(db, instance.StoreStaffBindingID, tenantID)
		}
		if (binding == nil || binding.Status == enums.StatusDeleted) && instance.StoreID > 0 {
			binding = repositories.StoreStaffBindingRepository.TakeInTenant(db, tenantID, "store_id = ? AND status <> ?", instance.StoreID, enums.StatusDeleted)
		}
		if binding == nil || binding.UserID <= 0 {
			return nil, false, errorsx.InvalidParam("所选企微员工号未关联门店员工，无法加入客服组")
		}
		userIDs = append(userIDs, binding.UserID)
	}
	slices.Sort(userIDs)
	return uniquePositive(userIDs), true, nil
}

func (s *agentTeamService) BindStoreStaffUser(userID, teamID int64, operator *dto.AuthPrincipal) error {
	if operator == nil {
		return errorsx.Unauthorized("未登录或登录已过期")
	}
	tenantID := AgentTeamScopeService.ActiveTenantID(operator)
	if tenantID <= 0 {
		return errorsx.Forbidden("请先进入需要管理门店员工的接入公司")
	}
	user := repositories.UserRepository.GetInTenant(sqls.DB(), userID, tenantID)
	if user == nil || user.Status == enums.StatusDeleted {
		return errorsx.InvalidParam("门店员工账号不存在")
	}
	if !UserService.HasRole(userID, constants.RoleCodeStoreStaff) {
		return errorsx.InvalidParam("只有门店员工账号可以分配客服组")
	}
	bindings := repositories.StoreStaffBindingRepository.Find(sqls.DB(), sqls.NewCnd().Eq("tenant_id", tenantID).Eq("user_id", userID).Where("status <> ?", enums.StatusDeleted).Asc("id"))
	if len(bindings) == 0 {
		return errorsx.InvalidParam("该门店员工尚未绑定门店，当前保持暂未分配客服组")
	}
	if teamID > 0 {
		team := repositories.AgentTeamRepository.GetInTenant(sqls.DB(), teamID, tenantID)
		if team == nil || team.Status != enums.StatusOk {
			return errorsx.InvalidParam("客服组不存在或已停用")
		}
		if !AgentTeamScopeService.CanManageTeam(operator, teamID) {
			return errorsx.Forbidden("无权管理目标客服组")
		}
		if team.TenantID > 0 && user.TenantID != team.TenantID {
			return errorsx.InvalidParam("门店员工账号与客服组必须属于同一接入公司")
		}
	}
	affectedTeamIDs := map[int64]struct{}{}
	allUnchanged := true
	for i := range bindings {
		oldTeamID := bindings[i].AgentTeamID
		if oldTeamID != teamID {
			allUnchanged = false
		}
		if oldTeamID > 0 && oldTeamID != teamID {
			if !AgentTeamScopeService.CanManageTeam(operator, oldTeamID) {
				return errorsx.Forbidden("无权从原客服组移出该门店员工")
			}
			affectedTeamIDs[oldTeamID] = struct{}{}
		}
	}
	if allUnchanged {
		return nil
	}
	return sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		now := time.Now()
		bindingIDs := make([]int64, 0, len(bindings))
		for i := range bindings {
			bindingIDs = append(bindingIDs, bindings[i].ID)
			if err := repositories.StoreStaffBindingRepository.UpdatesInTenant(ctx.Tx, bindings[i].ID, tenantID, map[string]any{
				"agent_team_id":    teamID,
				"updated_at":       now,
				"update_user_id":   operator.UserID,
				"update_user_name": operator.Username,
			}); err != nil {
				return err
			}
		}
		if err := repositories.WxWorkProtocolInstanceRepository.UpdatesByStoreStaffBindingIDsInTenant(ctx.Tx, bindingIDs, tenantID, map[string]any{
			"agent_team_id":    teamID,
			"updated_at":       now,
			"update_user_id":   operator.UserID,
			"update_user_name": operator.Username,
		}); err != nil {
			return err
		}
		for affectedTeamID := range affectedTeamIDs {
			if err := s.syncTeamScopeFromAssignments(ctx.Tx, affectedTeamID, operator); err != nil {
				return err
			}
		}
		if teamID > 0 {
			return s.syncTeamScopeFromAssignments(ctx.Tx, teamID, operator)
		}
		return nil
	})
}

func (s *agentTeamService) FindStoreStaffUserIDs(teamID int64) []int64 {
	if teamID <= 0 {
		return []int64{}
	}
	bindings := repositories.StoreStaffBindingRepository.Find(sqls.DB(), sqls.NewCnd().Eq("agent_team_id", teamID).Where("status <> ?", enums.StatusDeleted).Asc("user_id"))
	userIDs := make([]int64, 0, len(bindings))
	for i := range bindings {
		userIDs = appendPositive(userIDs, bindings[i].UserID)
	}
	slices.Sort(userIDs)
	return uniquePositive(userIDs)
}

func (s *agentTeamService) FindStoreStaffUserIDsInTenant(teamID, tenantID int64) []int64 {
	if repositories.AgentTeamRepository.GetInTenant(sqls.DB(), teamID, tenantID) == nil {
		return []int64{}
	}
	bindings := repositories.StoreStaffBindingRepository.Find(sqls.DB(), sqls.NewCnd().Eq("tenant_id", tenantID).Eq("agent_team_id", teamID).Where("status <> ?", enums.StatusDeleted).Asc("user_id"))
	userIDs := make([]int64, 0, len(bindings))
	for i := range bindings {
		userIDs = appendPositive(userIDs, bindings[i].UserID)
	}
	slices.Sort(userIDs)
	return uniquePositive(userIDs)
}

func (s *agentTeamService) replaceStoreStaffBindingsDB(db *gorm.DB, teamID int64, selectedUserIDs []int64, operator *dto.AuthPrincipal) error {
	tenantID := AgentTeamScopeService.ActiveTenantID(operator)
	team := repositories.AgentTeamRepository.GetInTenant(db, teamID, tenantID)
	if team == nil {
		return errorsx.InvalidParam("客服组不存在或不属于当前接入公司")
	}
	selectedUserIDs = uniquePositive(selectedUserIDs)
	selected := make(map[int64]struct{}, len(selectedUserIDs))
	for _, userID := range selectedUserIDs {
		selected[userID] = struct{}{}
	}

	role := repositories.RoleRepository.GetByCode(db, constants.RoleCodeStoreStaff)
	if len(selectedUserIDs) > 0 && (role == nil || role.Status != enums.StatusOk) {
		return errorsx.InvalidParam("门店员工角色不存在或已停用")
	}
	selectedBindings := make([]models.StoreStaffBinding, 0)
	if len(selectedUserIDs) > 0 {
		users := repositories.UserRepository.Find(db, sqls.NewCnd().In("id", selectedUserIDs).Eq("tenant_id", tenantID).Where("status <> ?", enums.StatusDeleted))
		if len(users) != len(selectedUserIDs) {
			return errorsx.InvalidParam("部分门店员工账号不存在或已删除，请重新选择")
		}
		for _, userID := range selectedUserIDs {
			if repositories.UserRoleRepository.FindOne(db, sqls.NewCnd().Eq("user_id", userID).Eq("role_id", role.ID)) == nil {
				return errorsx.InvalidParam("所选账号中包含非门店员工账号")
			}
		}
		if team.TenantID > 0 {
			for i := range users {
				if users[i].TenantID != team.TenantID {
					return errorsx.InvalidParam("门店员工账号与客服组必须属于同一接入公司")
				}
			}
		}
		selectedBindings = repositories.StoreStaffBindingRepository.Find(db, sqls.NewCnd().Eq("tenant_id", tenantID).In("user_id", selectedUserIDs).Where("status <> ?", enums.StatusDeleted).Asc("id"))
		boundUsers := make(map[int64]struct{}, len(selectedBindings))
		for i := range selectedBindings {
			boundUsers[selectedBindings[i].UserID] = struct{}{}
		}
		if len(boundUsers) != len(selectedUserIDs) {
			return errorsx.InvalidParam("部分门店员工尚未绑定门店，不能加入客服组")
		}
	}

	currentBindings := repositories.StoreStaffBindingRepository.Find(db, sqls.NewCnd().Eq("tenant_id", tenantID).Eq("agent_team_id", teamID).Where("status <> ?", enums.StatusDeleted).Asc("id"))
	changes := make(map[int64]int64)
	affectedTeamIDs := map[int64]struct{}{teamID: {}}
	for i := range currentBindings {
		if _, keep := selected[currentBindings[i].UserID]; !keep {
			changes[currentBindings[i].ID] = 0
		}
	}
	for i := range selectedBindings {
		binding := selectedBindings[i]
		if binding.AgentTeamID == teamID {
			continue
		}
		if binding.AgentTeamID > 0 {
			if !s.canManageTeamDB(db, operator, binding.AgentTeamID) {
				return errorsx.Forbidden("无权从原客服组移出所选门店员工")
			}
			affectedTeamIDs[binding.AgentTeamID] = struct{}{}
		}
		changes[binding.ID] = teamID
	}
	if len(changes) == 0 {
		return s.syncTeamScopeFromAssignments(db, teamID, operator)
	}

	now := time.Now()
	for bindingID, nextTeamID := range changes {
		if err := repositories.StoreStaffBindingRepository.UpdatesInTenant(db, bindingID, tenantID, map[string]any{
			"agent_team_id":    nextTeamID,
			"updated_at":       now,
			"update_user_id":   operator.UserID,
			"update_user_name": operator.Username,
		}); err != nil {
			return err
		}
		if err := repositories.WxWorkProtocolInstanceRepository.UpdatesByStoreStaffBindingIDsInTenant(db, []int64{bindingID}, tenantID, map[string]any{
			"agent_team_id":    nextTeamID,
			"updated_at":       now,
			"update_user_id":   operator.UserID,
			"update_user_name": operator.Username,
		}); err != nil {
			return err
		}
	}
	for affectedTeamID := range affectedTeamIDs {
		if err := s.syncTeamScopeFromAssignments(db, affectedTeamID, operator); err != nil {
			return err
		}
	}
	return nil
}

func (s *agentTeamService) canManageTeamDB(db *gorm.DB, operator *dto.AuthPrincipal, teamID int64) bool {
	if operator == nil || teamID <= 0 {
		return false
	}
	tenantID := AgentTeamScopeService.ActiveTenantID(operator)
	if tenantID <= 0 {
		return false
	}
	team := repositories.AgentTeamRepository.GetInTenant(db, teamID, tenantID)
	return AgentTeamScopeService.canManageTeam(operator, team)
}

func (s *agentTeamService) syncTeamScopeFromAssignments(db *gorm.DB, teamID int64, operator *dto.AuthPrincipal) error {
	if teamID <= 0 {
		return nil
	}
	team := repositories.AgentTeamRepository.Get(db, teamID)
	if team == nil || team.Status == enums.StatusDeleted {
		return errorsx.InvalidParam("客服组不存在")
	}
	tenantID := team.TenantID
	bindingCnd := sqls.NewCnd().Eq("agent_team_id", teamID).Where("status <> ?", enums.StatusDeleted).Asc("id")
	if operator != nil {
		activeTenantID := AgentTeamScopeService.ActiveTenantID(operator)
		if tenantID <= 0 || activeTenantID != tenantID {
			return errorsx.Forbidden("客服组不属于当前接入公司")
		}
		bindingCnd.Eq("tenant_id", tenantID)
	}
	bindings := repositories.StoreStaffBindingRepository.Find(db, bindingCnd)
	bindingIDs := make([]int64, 0, len(bindings))
	companyIDs := make([]int64, 0, len(bindings))
	storeIDs := make([]int64, 0, len(bindings))
	for i := range bindings {
		bindingIDs = append(bindingIDs, bindings[i].ID)
		companyIDs = appendPositive(companyIDs, bindings[i].CompanyID)
		storeIDs = appendPositive(storeIDs, bindings[i].StoreID)
	}
	instanceCnd := sqls.NewCnd().Where("status <> ?", enums.StatusDeleted).Asc("id")
	if operator != nil {
		instanceCnd.Eq("tenant_id", tenantID)
	}
	if len(bindingIDs) > 0 {
		instanceCnd.Where("(store_staff_binding_id IN ? OR agent_team_id = ?)", bindingIDs, teamID)
	} else {
		instanceCnd.Eq("agent_team_id", teamID)
	}
	instances := repositories.WxWorkProtocolInstanceRepository.Find(db, instanceCnd)
	instanceIDs := make([]int64, 0, len(instances))
	for i := range instances {
		instanceIDs = append(instanceIDs, instances[i].ID)
		companyIDs = appendPositive(companyIDs, instances[i].CompanyID)
		storeIDs = appendPositive(storeIDs, instances[i].StoreID)
	}
	slices.Sort(companyIDs)
	slices.Sort(storeIDs)
	slices.Sort(instanceIDs)
	userID, username := int64(0), "system"
	if operator != nil {
		userID, username = operator.UserID, operator.Username
	}
	columns := map[string]any{
		"company_scope_ids":          utils.JoinInt64s(uniquePositive(companyIDs)),
		"store_scope_ids":            utils.JoinInt64s(uniquePositive(storeIDs)),
		"wx_work_instance_scope_ids": utils.JoinInt64s(uniquePositive(instanceIDs)),
		"updated_at":                 time.Now(),
		"update_user_id":             userID,
		"update_user_name":           username,
	}
	if operator == nil {
		return repositories.AgentTeamRepository.Updates(db, teamID, columns)
	}
	return repositories.AgentTeamRepository.UpdatesInTenant(db, teamID, tenantID, columns)
}

func (s *agentTeamService) BackfillWxWorkInstanceBindings() error {
	return sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		teams := repositories.AgentTeamRepository.Find(ctx.Tx, sqls.NewCnd().Where("status <> ?", enums.StatusDeleted).Asc("id"))
		for i := range teams {
			ids := utils.SplitInt64s(teams[i].WxWorkInstanceScopeIDs)
			if len(ids) == 0 {
				continue
			}
			if err := ctx.Tx.Model(&models.WxWorkProtocolInstance{}).
				Where("id IN ? AND agent_team_id = ?", ids, 0).
				Updates(map[string]any{"agent_team_id": teams[i].ID}).Error; err != nil {
				return err
			}
		}
		for i := range teams {
			if err := s.syncTeamScopeFromAssignments(ctx.Tx, teams[i].ID, nil); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *agentTeamService) BackfillStoreStaffAgentTeamBindings() error {
	return sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		instances := repositories.WxWorkProtocolInstanceRepository.Find(ctx.Tx, sqls.NewCnd().Where("status <> ?", enums.StatusDeleted).Asc("id"))
		for i := range instances {
			instance := &instances[i]
			var binding *models.StoreStaffBinding
			if instance.StoreStaffBindingID > 0 {
				binding = repositories.StoreStaffBindingRepository.Get(ctx.Tx, instance.StoreStaffBindingID)
			}
			if binding == nil && instance.StoreID > 0 {
				binding = repositories.StoreStaffBindingRepository.Take(ctx.Tx, "store_id = ? AND status <> ?", instance.StoreID, enums.StatusDeleted)
			}
			if binding == nil {
				continue
			}
			teamID := binding.AgentTeamID
			if teamID <= 0 {
				teamID = instance.AgentTeamID
				if teamID > 0 {
					if err := repositories.StoreStaffBindingRepository.Updates(ctx.Tx, binding.ID, map[string]any{"agent_team_id": teamID, "updated_at": time.Now()}); err != nil {
						return err
					}
				}
			}
			if instance.StoreStaffBindingID != binding.ID || instance.AgentTeamID != teamID {
				if err := repositories.WxWorkProtocolInstanceRepository.Updates(ctx.Tx, instance.ID, map[string]any{
					"store_staff_binding_id": binding.ID,
					"agent_team_id":          teamID,
					"updated_at":             time.Now(),
				}); err != nil {
					return err
				}
			}
		}
		teams := repositories.AgentTeamRepository.Find(ctx.Tx, sqls.NewCnd().Where("status <> ?", enums.StatusDeleted).Asc("id"))
		for i := range teams {
			if err := s.syncTeamScopeFromAssignments(ctx.Tx, teams[i].ID, nil); err != nil {
				return err
			}
		}
		return nil
	})
}
