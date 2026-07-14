package services

import (
	"slices"

	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/utils"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
)

var AgentTeamScopeService = newAgentTeamScopeService()

type agentTeamScopeService struct{}

func newAgentTeamScopeService() *agentTeamScopeService { return &agentTeamScopeService{} }

type ManagedDataScope struct {
	TenantID          int64
	Unrestricted      bool
	CompanyIDs        []int64
	StoreIDs          []int64
	WxWorkInstanceIDs []int64
	KnowledgeBaseIDs  []int64
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
			scope.CompanyIDs = append(scope.CompanyIDs, utils.SplitInt64s(teams[i].CompanyScopeIDs)...)
			scope.StoreIDs = append(scope.StoreIDs, utils.SplitInt64s(teams[i].StoreScopeIDs)...)
			scope.WxWorkInstanceIDs = append(scope.WxWorkInstanceIDs, utils.SplitInt64s(teams[i].WxWorkInstanceScopeIDs)...)
		}
	}
	if slices.Contains(operator.Roles, constants.RoleCodeCsUser) {
		if profile := repositories.AgentProfileRepository.Take(sqls.DB(), "tenant_id = ? AND user_id = ?", tenantID, operator.UserID); profile != nil && profile.Status != enums.StatusDeleted {
			if team := repositories.AgentTeamRepository.GetInTenant(sqls.DB(), profile.TeamID, tenantID); team != nil && team.Status == enums.StatusOk {
				scope.CompanyIDs = append(scope.CompanyIDs, utils.SplitInt64s(team.CompanyScopeIDs)...)
				scope.StoreIDs = append(scope.StoreIDs, utils.SplitInt64s(team.StoreScopeIDs)...)
				scope.WxWorkInstanceIDs = append(scope.WxWorkInstanceIDs, utils.SplitInt64s(team.WxWorkInstanceScopeIDs)...)
			}
		}
	}
	if slices.Contains(operator.Roles, constants.RoleCodeStoreStaff) {
		bindings := repositories.StoreStaffBindingRepository.Find(sqls.DB(), sqls.NewCnd().Eq("tenant_id", tenantID).Eq("user_id", operator.UserID).Where("status <> ?", enums.StatusDeleted))
		for i := range bindings {
			scope.CompanyIDs = appendPositive(scope.CompanyIDs, bindings[i].CompanyID)
			scope.StoreIDs = appendPositive(scope.StoreIDs, bindings[i].StoreID)
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
	if slices.Contains(operator.Roles, constants.RoleCodeSuperAdmin) || slices.Contains(operator.Roles, constants.RoleCodeAdmin) {
		return team != nil
	}
	if slices.Contains(operator.Roles, constants.RoleCodeTenantAdmin) {
		return team != nil
	}
	if !slices.Contains(operator.Roles, constants.RoleCodeCsTeamLeader) {
		return false
	}
	return team != nil && team.Status != enums.StatusDeleted && team.LeaderUserID == operator.UserID
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
	if len(scope.WxWorkInstanceIDs) > 0 {
		return cnd.In("id", scope.WxWorkInstanceIDs)
	}
	if len(scope.StoreIDs) > 0 {
		return cnd.In("store_id", scope.StoreIDs)
	}
	return cnd.Eq("id", -1)
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
	if len(scope.WxWorkInstanceIDs) > 0 {
		return cnd.Where("id IN (SELECT conversation_id FROM t_conversation_route_state WHERE tenant_id = ? AND wx_work_instance_id IN (?))", tenantID, scope.WxWorkInstanceIDs)
	}
	if len(scope.StoreIDs) > 0 {
		return cnd.Where("id IN (SELECT conversation_id FROM t_conversation_route_state WHERE tenant_id = ? AND store_id IN (?))", tenantID, scope.StoreIDs)
	}
	return cnd.Eq("id", -1)
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
	route := repositories.ConversationRouteStateRepository.TakeByConversationInTenant(sqls.DB(), conversationID, tenantID)
	if route == nil {
		return false
	}
	scope := s.Resolve(operator)
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
	if repositories.WxWorkProtocolInstanceRepository.GetInTenant(sqls.DB(), instanceID, tenantID) == nil {
		return false
	}
	scope := s.Resolve(operator)
	return scope.Unrestricted || containsInt64(scope.WxWorkInstanceIDs, instanceID)
}

func (scope *ManagedDataScope) expand() {
	if scope.TenantID <= 0 {
		return
	}
	scope.CompanyIDs = uniquePositive(scope.CompanyIDs)
	scope.StoreIDs = uniquePositive(scope.StoreIDs)
	scope.WxWorkInstanceIDs = uniquePositive(scope.WxWorkInstanceIDs)
	if len(scope.CompanyIDs) > 0 && len(scope.StoreIDs) == 0 && len(scope.WxWorkInstanceIDs) == 0 {
		stores := repositories.StoreRepository.Find(sqls.DB(), sqls.NewCnd().Eq("tenant_id", scope.TenantID).In("company_id", scope.CompanyIDs).Where("status <> ?", enums.StatusDeleted))
		for i := range stores {
			scope.StoreIDs = appendPositive(scope.StoreIDs, stores[i].ID)
		}
	}
	scope.StoreIDs = uniquePositive(scope.StoreIDs)
	if len(scope.StoreIDs) > 0 {
		stores := repositories.StoreRepository.Find(sqls.DB(), sqls.NewCnd().Eq("tenant_id", scope.TenantID).In("id", scope.StoreIDs).Where("status <> ?", enums.StatusDeleted))
		for i := range stores {
			scope.CompanyIDs = appendPositive(scope.CompanyIDs, stores[i].CompanyID)
			scope.KnowledgeBaseIDs = appendPositive(scope.KnowledgeBaseIDs, stores[i].KnowledgeBaseID)
		}
		if len(scope.WxWorkInstanceIDs) == 0 {
			instances := repositories.WxWorkProtocolInstanceRepository.Find(sqls.DB(), sqls.NewCnd().Eq("tenant_id", scope.TenantID).In("store_id", scope.StoreIDs).Where("status <> ?", enums.StatusDeleted))
			for i := range instances {
				scope.WxWorkInstanceIDs = appendPositive(scope.WxWorkInstanceIDs, instances[i].ID)
				scope.KnowledgeBaseIDs = appendPositive(scope.KnowledgeBaseIDs, instances[i].KnowledgeBaseID)
			}
		}
	}
	if len(scope.WxWorkInstanceIDs) > 0 {
		instances := repositories.WxWorkProtocolInstanceRepository.Find(sqls.DB(), sqls.NewCnd().Eq("tenant_id", scope.TenantID).In("id", scope.WxWorkInstanceIDs).Where("status <> ?", enums.StatusDeleted))
		for i := range instances {
			scope.StoreIDs = appendPositive(scope.StoreIDs, instances[i].StoreID)
			scope.KnowledgeBaseIDs = appendPositive(scope.KnowledgeBaseIDs, instances[i].KnowledgeBaseID)
		}
	}
	scope.CompanyIDs = uniquePositive(scope.CompanyIDs)
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
