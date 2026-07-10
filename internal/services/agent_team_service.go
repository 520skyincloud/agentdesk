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

func (s *agentTeamService) Take(where ...interface{}) *models.AgentTeam {
	return repositories.AgentTeamRepository.Take(sqls.DB(), where...)
}

func (s *agentTeamService) Find(cnd *sqls.Cnd) []models.AgentTeam {
	return repositories.AgentTeamRepository.Find(sqls.DB(), cnd)
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

func (s *agentTeamService) Count(cnd *sqls.Cnd) int64 {
	return repositories.AgentTeamRepository.Count(sqls.DB(), cnd)
}

func (s *agentTeamService) FindByIds(ids []int64) []models.AgentTeam {
	return repositories.AgentTeamRepository.FindByIds(sqls.DB(), ids)
}

func (s *agentTeamService) Create(t *models.AgentTeam) error {
	return repositories.AgentTeamRepository.Create(sqls.DB(), t)
}

func (s *agentTeamService) Update(t *models.AgentTeam) error {
	return repositories.AgentTeamRepository.Update(sqls.DB(), t)
}

func (s *agentTeamService) Updates(id int64, columns map[string]interface{}) error {
	return repositories.AgentTeamRepository.Updates(sqls.DB(), id, columns)
}

func (s *agentTeamService) UpdateColumn(id int64, name string, value interface{}) error {
	return repositories.AgentTeamRepository.UpdateColumn(sqls.DB(), id, name, value)
}

func (s *agentTeamService) Delete(id int64) {
	repositories.AgentTeamRepository.Delete(sqls.DB(), id)
}

func (s *agentTeamService) CreateAgentTeam(req request.CreateAgentTeamRequest, operator *dto.AuthPrincipal) (*models.AgentTeam, error) {
	if operator == nil {
		return nil, errorsx.Unauthorized("未登录或登录已过期")
	}
	if !AgentTeamScopeService.IsAdmin(operator) {
		return nil, errorsx.Forbidden("只有管理员可以创建客服组")
	}
	item, err := s.buildTeamModel(0, req.Name, req.LeaderUserID, req.WxWorkInstanceScopeIDs, req.Status, req.Description, req.Remark)
	if err != nil {
		return nil, err
	}
	item.AuditFields = utils.BuildAuditFields(operator)
	if err := repositories.AgentTeamRepository.Create(sqls.DB(), item); err != nil {
		return nil, err
	}
	return item, nil
}

func (s *agentTeamService) UpdateAgentTeam(req request.UpdateAgentTeamRequest, operator *dto.AuthPrincipal) error {
	if operator == nil {
		return errorsx.Unauthorized("未登录或登录已过期")
	}
	current := s.Get(req.ID)
	if current == nil || current.Status == enums.StatusDeleted {
		return errorsx.InvalidParam("客服组不存在")
	}
	if !AgentTeamScopeService.CanManageTeam(operator, current.ID) {
		return errorsx.Forbidden("只能管理自己绑定的客服组")
	}
	if !AgentTeamScopeService.IsAdmin(operator) && req.LeaderUserID != current.LeaderUserID {
		return errorsx.Forbidden("客服组长不能变更客服组负责人")
	}
	item, err := s.buildTeamModel(req.ID, req.Name, req.LeaderUserID, req.WxWorkInstanceScopeIDs, req.Status, req.Description, req.Remark)
	if err != nil {
		return err
	}
	now := time.Now()
	return repositories.AgentTeamRepository.Updates(sqls.DB(), req.ID, map[string]any{
		"name":                       item.Name,
		"leader_user_id":             item.LeaderUserID,
		"company_scope_ids":          item.CompanyScopeIDs,
		"store_scope_ids":            item.StoreScopeIDs,
		"wx_work_instance_scope_ids": item.WxWorkInstanceScopeIDs,
		"status":                     item.Status,
		"description":                item.Description,
		"remark":                     item.Remark,
		"update_user_id":             operator.UserID,
		"update_user_name":           operator.Username,
		"updated_at":                 now,
	})
}

func (s *agentTeamService) DeleteAgentTeam(id int64, operator *dto.AuthPrincipal) error {
	if operator == nil {
		return errorsx.Unauthorized("未登录或登录已过期")
	}
	current := s.Get(id)
	if current == nil || current.Status == enums.StatusDeleted {
		return errorsx.InvalidParam("客服组不存在")
	}
	if !AgentTeamScopeService.CanManageTeam(operator, current.ID) {
		return errorsx.Forbidden("只能管理自己绑定的客服组")
	}
	if AgentProfileService.Take("team_id = ?", id) != nil {
		return errorsx.Forbidden("客服组下仍有关联客服档案，无法删除")
	}
	if AgentTeamScheduleService.Take("team_id = ?", id) != nil {
		return errorsx.Forbidden("客服组下仍有关联组排班，无法删除")
	}
	if AIAgentService.Take(
		"(team_ids = ? OR team_ids LIKE ? OR team_ids LIKE ? OR team_ids LIKE ?) AND status <> ?",
		utils.JoinInt64s([]int64{id}),
		utils.JoinInt64s([]int64{id})+",%",
		"%,"+utils.JoinInt64s([]int64{id}),
		"%,"+utils.JoinInt64s([]int64{id})+",%",
		enums.StatusDeleted,
	) != nil {
		return errorsx.Forbidden("客服组下仍有关联 AI Agent，无法删除")
	}
	return repositories.AgentTeamRepository.Updates(sqls.DB(), id, map[string]any{
		"status":           enums.StatusDeleted,
		"update_user_id":   operator.UserID,
		"update_user_name": operator.Username,
		"updated_at":       time.Now(),
	})
}

func (s *agentTeamService) buildTeamModel(id int64, name string, leaderUserID int64, wxWorkInstanceScopeIDs []int64, status int, description, remark string) (*models.AgentTeam, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, errorsx.InvalidParam("客服组名称不能为空")
	}
	if exists := s.Take("name = ? AND status <> ? AND id <> ?", name, enums.StatusDeleted, id); exists != nil {
		return nil, errorsx.InvalidParam("客服组名称已存在")
	}
	if leaderUserID > 0 {
		leader := UserService.Get(leaderUserID)
		if leader == nil || leader.Status != enums.StatusOk {
			return nil, errorsx.InvalidParam("组长用户不存在或已停用")
		}
		if !UserService.HasRole(leaderUserID, constants.RoleCodeCsTeamLeader) {
			return nil, errorsx.InvalidParam("所选用户不是客服组长账号")
		}
	}
	if status != 0 && status != 1 {
		return nil, errorsx.InvalidParam("客服组状态不合法")
	}
	derivedCompanyIDs, derivedStoreIDs, derivedInstanceIDs, err := s.deriveScopeFromWxWorkInstances(wxWorkInstanceScopeIDs)
	if err != nil {
		return nil, err
	}
	return &models.AgentTeam{
		Name:                   name,
		LeaderUserID:           leaderUserID,
		CompanyScopeIDs:        utils.JoinInt64s(derivedCompanyIDs),
		StoreScopeIDs:          utils.JoinInt64s(derivedStoreIDs),
		WxWorkInstanceScopeIDs: utils.JoinInt64s(derivedInstanceIDs),
		Status:                 enums.Status(status),
		Description:            strings.TrimSpace(description),
		Remark:                 strings.TrimSpace(remark),
	}, nil
}

func (s *agentTeamService) deriveScopeFromWxWorkInstances(instanceIDs []int64) ([]int64, []int64, []int64, error) {
	instanceIDs = uniquePositive(instanceIDs)
	if len(instanceIDs) == 0 {
		return nil, nil, nil, nil
	}
	instances := repositories.WxWorkProtocolInstanceRepository.Find(sqls.DB(), sqls.NewCnd().In("id", instanceIDs).Where("status <> ?", enums.StatusDeleted))
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
		store := repositories.StoreRepository.Get(sqls.DB(), instance.StoreID)
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
