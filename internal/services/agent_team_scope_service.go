package services

import (
	"slices"
	"strings"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/errorsx"
	"agent-desk/internal/pkg/utils"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

var AgentTeamScopeService = newAgentTeamScopeService()

type agentTeamScopeService struct{}

func newAgentTeamScopeService() *agentTeamScopeService { return &agentTeamScopeService{} }

type ManagedDataScope struct {
	TenantID             int64
	Unrestricted         bool
	StoreStaffScoped     bool
	StoreIDs             []int64
	StoreStaffBindingIDs []int64
	StoreStaffStoreIDs   []int64
	WxWorkInstanceIDs    []int64
	KnowledgeBaseIDs     []int64
}

func (s *agentTeamScopeService) Resolve(operator *dto.AuthPrincipal) ManagedDataScope {
	tenantID := s.ActiveTenantID(operator)
	if tenantID <= 0 {
		return ManagedDataScope{}
	}
	if s.IsAdmin(operator) {
		return ManagedDataScope{TenantID: tenantID, Unrestricted: true}
	}
	scope := ManagedDataScope{TenantID: tenantID}
	if slices.Contains(operator.Roles, constants.RoleCodeCsTeamLeader) {
		teams := repositories.AgentTeamRepository.Find(sqls.DB(), sqls.NewCnd().Eq("tenant_id", tenantID).Eq("leader_user_id", operator.UserID).Eq("status", enums.StatusOk))
		for i := range teams {
			scope.StoreIDs = append(scope.StoreIDs, utils.SplitInt64s(teams[i].StoreScopeIDs)...)
			scope.WxWorkInstanceIDs = append(scope.WxWorkInstanceIDs, utils.SplitInt64s(teams[i].WxWorkInstanceScopeIDs)...)
		}
	}
	if slices.Contains(operator.Roles, constants.RoleCodeCsUser) {
		if profile := repositories.AgentProfileRepository.Take(sqls.DB(), "tenant_id = ? AND user_id = ?", tenantID, operator.UserID); profile != nil && profile.Status != enums.StatusDeleted {
			if team := repositories.AgentTeamRepository.GetInTenant(sqls.DB(), profile.TeamID, tenantID); team != nil && team.Status == enums.StatusOk {
				scope.StoreIDs = append(scope.StoreIDs, utils.SplitInt64s(team.StoreScopeIDs)...)
				scope.WxWorkInstanceIDs = append(scope.WxWorkInstanceIDs, utils.SplitInt64s(team.WxWorkInstanceScopeIDs)...)
			}
		}
	}
	if slices.Contains(operator.Roles, constants.RoleCodeStoreStaff) {
		scope.StoreStaffScoped = s.IsStoreStaffScoped(operator)
		bindings := repositories.StoreStaffBindingRepository.Find(sqls.DB(), sqls.NewCnd().
			Eq("tenant_id", tenantID).
			Eq("user_id", operator.UserID).
			Eq("active_user_id", operator.UserID).
			Eq("status", enums.StatusOk))
		for i := range bindings {
			scope.StoreStaffBindingIDs = appendPositive(scope.StoreStaffBindingIDs, bindings[i].ID)
			scope.StoreStaffStoreIDs = appendPositive(scope.StoreStaffStoreIDs, bindings[i].StoreID)
			if scope.StoreStaffScoped {
				scope.StoreIDs = appendPositive(scope.StoreIDs, bindings[i].StoreID)
			}
		}
	}
	scope.expand()
	return scope
}

func (s *agentTeamScopeService) IsAdmin(operator *dto.AuthPrincipal) bool {
	return operator != nil && (slices.Contains(operator.Roles, constants.RoleCodeSuperAdmin) ||
		slices.Contains(operator.Roles, constants.RoleCodeAdmin) ||
		slices.Contains(operator.Roles, constants.RoleCodeTenantAdmin))
}

func (s *agentTeamScopeService) IsStoreStaffScoped(operator *dto.AuthPrincipal) bool {
	return operator != nil &&
		slices.Contains(operator.Roles, constants.RoleCodeStoreStaff) &&
		!slices.Contains(operator.Roles, constants.RoleCodeCsTeamLeader) &&
		!slices.Contains(operator.Roles, constants.RoleCodeCsUser) &&
		!s.IsAdmin(operator)
}

func (s *agentTeamScopeService) ActiveTenantID(operator *dto.AuthPrincipal) int64 {
	if operator == nil || operator.ActiveTenantID <= 0 {
		return 0
	}
	return operator.ActiveTenantID
}

func (s *agentTeamScopeService) ApplyTenantFilter(cnd *sqls.Cnd, operator *dto.AuthPrincipal) *sqls.Cnd {
	if cnd == nil {
		cnd = sqls.NewCnd()
	}
	tenantID := s.ActiveTenantID(operator)
	if tenantID <= 0 {
		return cnd.Where("1 = 0")
	}
	return cnd.Eq("tenant_id", tenantID)
}

func (s *agentTeamScopeService) CanManageTeam(operator *dto.AuthPrincipal, teamID int64) bool {
	if operator == nil || teamID <= 0 {
		return false
	}
	tenantID := s.ActiveTenantID(operator)
	if tenantID <= 0 {
		return false
	}
	team := repositories.AgentTeamRepository.GetInTenant(sqls.DB(), teamID, tenantID)
	return s.canManageTeam(operator, team)
}

func (s *agentTeamScopeService) ManageableTeamIDs(operator *dto.AuthPrincipal) []int64 {
	tenantID := s.ActiveTenantID(operator)
	if tenantID <= 0 {
		return nil
	}
	cnd := sqls.NewCnd().
		Eq("tenant_id", tenantID).
		Where("status <> ?", enums.StatusDeleted)
	if !s.IsAdmin(operator) {
		if !slices.Contains(operator.Roles, constants.RoleCodeCsTeamLeader) {
			return nil
		}
		cnd.Eq("leader_user_id", operator.UserID)
	}
	teams := repositories.AgentTeamRepository.Find(sqls.DB(), cnd.Asc("id"))
	teamIDs := make([]int64, 0, len(teams))
	for i := range teams {
		teamIDs = append(teamIDs, teams[i].ID)
	}
	return teamIDs
}

func (s *agentTeamScopeService) canManageTeam(operator *dto.AuthPrincipal, team *models.AgentTeam) bool {
	if operator == nil || team == nil || operator.ActiveTenantID <= 0 || team.TenantID != operator.ActiveTenantID {
		return false
	}
	if slices.Contains(operator.Roles, constants.RoleCodeSuperAdmin) || slices.Contains(operator.Roles, constants.RoleCodeAdmin) {
		return true
	}
	if slices.Contains(operator.Roles, constants.RoleCodeTenantAdmin) {
		return true
	}
	if !slices.Contains(operator.Roles, constants.RoleCodeCsTeamLeader) {
		return false
	}
	return team.Status != enums.StatusDeleted && team.LeaderUserID == operator.UserID
}

func (s *agentTeamScopeService) lockManageableTeamsDB(db *gorm.DB, teamIDs []int64, operator *dto.AuthPrincipal, forbiddenMessage string) (map[int64]*models.AgentTeam, error) {
	tenantID := s.ActiveTenantID(operator)
	if tenantID <= 0 {
		return nil, errorsx.Forbidden("请先选择接入公司")
	}
	teamIDs = uniquePositiveInt64s(teamIDs)
	if len(teamIDs) == 0 {
		return nil, errorsx.InvalidParam("请选择客服组")
	}
	slices.Sort(teamIDs)
	teams := make(map[int64]*models.AgentTeam, len(teamIDs))
	for _, teamID := range teamIDs {
		team, err := repositories.AgentTeamRepository.GetForUpdateInTenant(db, teamID, tenantID)
		if err != nil {
			return nil, err
		}
		if team == nil || team.Status == enums.StatusDeleted {
			return nil, errorsx.InvalidParam("客服组不存在")
		}
		if !s.canManageTeam(operator, team) {
			return nil, errorsx.Forbidden(forbiddenMessage)
		}
		teams[teamID] = team
	}
	return teams, nil
}

func (s *agentTeamScopeService) ApplyKnowledgeBaseFilter(cnd *sqls.Cnd, operator *dto.AuthPrincipal) *sqls.Cnd {
	cnd = s.ApplyTenantFilter(cnd, operator)
	scope := s.Resolve(operator)
	if scope.Unrestricted {
		return cnd
	}
	if len(scope.KnowledgeBaseIDs) == 0 {
		return cnd.Eq("id", -1)
	}
	return cnd.In("id", scope.KnowledgeBaseIDs)
}

func (s *agentTeamScopeService) ApplyKnowledgeCandidateFilter(cnd *sqls.Cnd, operator *dto.AuthPrincipal) *sqls.Cnd {
	cnd = s.ApplyTenantFilter(cnd, operator)
	scope := s.Resolve(operator)
	if scope.Unrestricted {
		return cnd
	}
	if len(scope.KnowledgeBaseIDs) == 0 {
		return cnd.Eq("knowledge_base_id", -1)
	}
	return cnd.In("knowledge_base_id", scope.KnowledgeBaseIDs)
}

func (s *agentTeamScopeService) ApplyKnowledgeChildFilter(cnd *sqls.Cnd, operator *dto.AuthPrincipal) *sqls.Cnd {
	cnd = s.ApplyTenantFilter(cnd, operator)
	scope := s.Resolve(operator)
	if scope.Unrestricted {
		return cnd
	}
	if len(scope.KnowledgeBaseIDs) == 0 {
		return cnd.Eq("knowledge_base_id", -1)
	}
	return cnd.In("knowledge_base_id", scope.KnowledgeBaseIDs)
}

func (s *agentTeamScopeService) ApplyWxWorkInstanceFilter(cnd *sqls.Cnd, operator *dto.AuthPrincipal) *sqls.Cnd {
	cnd = s.ApplyTenantFilter(cnd, operator)
	scope := s.Resolve(operator)
	if scope.Unrestricted {
		return cnd
	}
	if scope.StoreStaffScoped {
		if len(scope.StoreStaffBindingIDs) == 0 || len(scope.StoreStaffStoreIDs) == 0 {
			return cnd.Eq("id", -1)
		}
		return cnd.
			In("store_staff_binding_id", scope.StoreStaffBindingIDs).
			In("store_id", scope.StoreStaffStoreIDs)
	}
	conditions := make([]string, 0, 2)
	args := make([]any, 0, 4)
	if len(scope.WxWorkInstanceIDs) > 0 {
		conditions = append(conditions, "id IN (?)")
		args = append(args, scope.WxWorkInstanceIDs)
	} else if len(scope.StoreIDs) > 0 {
		conditions = append(conditions, "store_id IN (?)")
		args = append(args, scope.StoreIDs)
	}
	if len(scope.StoreStaffBindingIDs) > 0 && len(scope.StoreStaffStoreIDs) > 0 {
		conditions = append(conditions, "(store_staff_binding_id IN (?) AND store_id IN (?))")
		args = append(args, scope.StoreStaffBindingIDs, scope.StoreStaffStoreIDs)
	}
	if len(conditions) == 0 {
		return cnd.Eq("id", -1)
	}
	return cnd.Where("("+strings.Join(conditions, " OR ")+")", args...)
}

func (s *agentTeamScopeService) ApplyConversationFilter(cnd *sqls.Cnd, operator *dto.AuthPrincipal) *sqls.Cnd {
	tenantID := s.ActiveTenantID(operator)
	if tenantID <= 0 {
		return cnd.Where("1 = 0")
	}
	cnd.Eq("tenant_id", tenantID)
	scope := s.Resolve(operator)
	if scope.Unrestricted {
		return cnd
	}
	conditions := []string{"current_assignee_id = ?"}
	args := []any{operator.UserID}
	if len(scope.WxWorkInstanceIDs) > 0 && !scope.StoreStaffScoped {
		conditions = append(conditions, "id IN (SELECT conversation_id FROM t_conversation_route_state WHERE tenant_id = ? AND wx_work_instance_id IN (?))")
		args = append(args, tenantID, scope.WxWorkInstanceIDs)
	} else if len(scope.StoreIDs) > 0 && !scope.StoreStaffScoped {
		conditions = append(conditions, "id IN (SELECT conversation_id FROM t_conversation_route_state WHERE tenant_id = ? AND store_id IN (?))")
		args = append(args, tenantID, scope.StoreIDs)
	}
	if len(scope.StoreStaffBindingIDs) > 0 && len(scope.StoreStaffStoreIDs) > 0 {
		conditions = append(conditions, `(
			store_staff_binding_id IN (?) AND EXISTS (
				SELECT 1 FROM t_conversation_route_state AS scoped_route
				WHERE scoped_route.tenant_id = ?
					AND scoped_route.conversation_id = t_conversation.id
					AND scoped_route.store_id = t_conversation.store_id
					AND scoped_route.store_staff_binding_id = t_conversation.store_staff_binding_id
			)
			AND store_id IN (?)
		)`)
		args = append(args, scope.StoreStaffBindingIDs, tenantID, scope.StoreStaffStoreIDs)
	}
	return cnd.Where("("+strings.Join(conditions, " OR ")+")", args...)
}

func (s *agentTeamScopeService) ApplyAssignedOrStoreStaffFilter(cnd *sqls.Cnd, operator *dto.AuthPrincipal) *sqls.Cnd {
	if cnd == nil {
		cnd = sqls.NewCnd()
	}
	scope := s.Resolve(operator)
	if operator == nil || operator.UserID <= 0 || scope.Unrestricted || len(scope.StoreStaffBindingIDs) == 0 || len(scope.StoreStaffStoreIDs) == 0 {
		if operator == nil || operator.UserID <= 0 {
			return cnd.Where("1 = 0")
		}
		return cnd.Eq("current_assignee_id", operator.UserID)
	}
	return cnd.Where(`(current_assignee_id = ? OR (
		store_staff_binding_id IN (?) AND store_id IN (?) AND EXISTS (
			SELECT 1 FROM t_conversation_route_state AS scoped_route
			WHERE scoped_route.tenant_id = ?
				AND scoped_route.conversation_id = t_conversation.id
				AND scoped_route.store_id = t_conversation.store_id
				AND scoped_route.store_staff_binding_id = t_conversation.store_staff_binding_id
		)
	))`, operator.UserID, scope.StoreStaffBindingIDs, scope.StoreStaffStoreIDs, scope.TenantID)
}

func (s *agentTeamScopeService) CanViewConversation(operator *dto.AuthPrincipal, conversationID int64) bool {
	if operator == nil || conversationID <= 0 {
		return false
	}
	tenantID := s.ActiveTenantID(operator)
	if tenantID <= 0 {
		return false
	}
	conversation := repositories.ConversationRepository.GetInTenant(sqls.DB(), conversationID, tenantID)
	if conversation == nil {
		return false
	}
	if s.IsAdmin(operator) {
		return true
	}
	if conversation.CurrentAssigneeID == operator.UserID {
		return true
	}
	scope := s.Resolve(operator)
	route := repositories.ConversationRouteStateRepository.TakeByConversationInTenant(sqls.DB(), conversationID, tenantID)
	if route == nil {
		return false
	}
	storeStaffAllowed := conversation.StoreID > 0 &&
		conversation.StoreStaffBindingID > 0 &&
		containsInt64(scope.StoreStaffBindingIDs, conversation.StoreStaffBindingID) &&
		containsInt64(scope.StoreStaffStoreIDs, conversation.StoreID) &&
		route.StoreID == conversation.StoreID &&
		route.StoreStaffBindingID == conversation.StoreStaffBindingID
	if scope.StoreStaffScoped {
		return storeStaffAllowed
	}
	if storeStaffAllowed {
		return true
	}
	if len(scope.WxWorkInstanceIDs) > 0 {
		return containsInt64(scope.WxWorkInstanceIDs, route.WxWorkInstanceID)
	}
	return containsInt64(scope.StoreIDs, route.StoreID)
}

func (s *agentTeamScopeService) CanViewWxWorkInstance(operator *dto.AuthPrincipal, instanceID int64) bool {
	if operator == nil || instanceID <= 0 {
		return false
	}
	tenantID := s.ActiveTenantID(operator)
	instance := repositories.WxWorkProtocolInstanceRepository.GetInTenant(sqls.DB(), instanceID, tenantID)
	if instance == nil {
		return false
	}
	scope := s.Resolve(operator)
	storeStaffAllowed := containsInt64(scope.StoreStaffBindingIDs, instance.StoreStaffBindingID) &&
		containsInt64(scope.StoreStaffStoreIDs, instance.StoreID)
	if scope.StoreStaffScoped {
		return storeStaffAllowed
	}
	return scope.Unrestricted || storeStaffAllowed || containsInt64(scope.WxWorkInstanceIDs, instanceID)
}

func (scope *ManagedDataScope) expand() {
	if scope.TenantID <= 0 {
		return
	}
	scope.StoreIDs = uniquePositive(scope.StoreIDs)
	scope.StoreStaffBindingIDs = uniquePositive(scope.StoreStaffBindingIDs)
	scope.StoreStaffStoreIDs = uniquePositive(scope.StoreStaffStoreIDs)
	scope.WxWorkInstanceIDs = uniquePositive(scope.WxWorkInstanceIDs)
	if scope.StoreStaffScoped && len(scope.StoreStaffBindingIDs) > 0 && len(scope.StoreStaffStoreIDs) > 0 {
		instances := repositories.WxWorkProtocolInstanceRepository.Find(sqls.DB(), sqls.NewCnd().
			Eq("tenant_id", scope.TenantID).
			In("store_staff_binding_id", scope.StoreStaffBindingIDs).
			In("store_id", scope.StoreStaffStoreIDs).
			Where("status <> ?", enums.StatusDeleted))
		for i := range instances {
			scope.WxWorkInstanceIDs = appendPositive(scope.WxWorkInstanceIDs, instances[i].ID)
		}
		scope.WxWorkInstanceIDs = uniquePositive(scope.WxWorkInstanceIDs)
	}
	if len(scope.WxWorkInstanceIDs) > 0 {
		instances := repositories.WxWorkProtocolInstanceRepository.Find(sqls.DB(), sqls.NewCnd().
			Eq("tenant_id", scope.TenantID).
			In("id", scope.WxWorkInstanceIDs).
			Where("status <> ?", enums.StatusDeleted))
		for i := range instances {
			scope.StoreIDs = appendPositive(scope.StoreIDs, instances[i].StoreID)
		}
		scope.StoreIDs = uniquePositive(scope.StoreIDs)
	}
	if len(scope.StoreIDs) > 0 {
		stores := repositories.StoreRepository.Find(sqls.DB(), sqls.NewCnd().
			Eq("tenant_id", scope.TenantID).
			In("id", scope.StoreIDs).
			Where("status <> ?", enums.StatusDeleted))
		for i := range stores {
			scope.KnowledgeBaseIDs = appendPositive(scope.KnowledgeBaseIDs, stores[i].KnowledgeBaseID)
		}
		if !scope.StoreStaffScoped && len(scope.WxWorkInstanceIDs) == 0 {
			instances := repositories.WxWorkProtocolInstanceRepository.Find(sqls.DB(), sqls.NewCnd().
				Eq("tenant_id", scope.TenantID).
				In("store_id", scope.StoreIDs).
				Where("status <> ?", enums.StatusDeleted))
			for i := range instances {
				scope.WxWorkInstanceIDs = appendPositive(scope.WxWorkInstanceIDs, instances[i].ID)
			}
		}
		knowledgeBases := repositories.KnowledgeBaseRepository.Find(sqls.DB(), sqls.NewCnd().
			Eq("tenant_id", scope.TenantID).
			In("store_id", scope.StoreIDs).
			Where("status <> ?", enums.StatusDeleted))
		for i := range knowledgeBases {
			scope.KnowledgeBaseIDs = appendPositive(scope.KnowledgeBaseIDs, knowledgeBases[i].ID)
		}
	}
	scope.StoreIDs = uniquePositive(scope.StoreIDs)
	scope.WxWorkInstanceIDs = uniquePositive(scope.WxWorkInstanceIDs)
	scope.KnowledgeBaseIDs = uniquePositive(scope.KnowledgeBaseIDs)
}

func appendPositive(values []int64, value int64) []int64 {
	if value > 0 {
		values = append(values, value)
	}
	return values
}

func uniquePositive(values []int64) []int64 {
	seen := map[int64]struct{}{}
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
