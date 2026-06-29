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
	Unrestricted      bool
	CompanyIDs        []int64
	StoreIDs          []int64
	WxWorkInstanceIDs []int64
	KnowledgeBaseIDs  []int64
}

func (s *agentTeamScopeService) Resolve(operator *dto.AuthPrincipal) ManagedDataScope {
	if operator == nil {
		return ManagedDataScope{}
	}
	if slices.Contains(operator.Roles, constants.RoleCodeSuperAdmin) || slices.Contains(operator.Roles, constants.RoleCodeAdmin) {
		return ManagedDataScope{Unrestricted: true}
	}
	scope := ManagedDataScope{}
	if slices.Contains(operator.Roles, constants.RoleCodeCsTeamLeader) {
		teams := repositories.AgentTeamRepository.Find(sqls.DB(), sqls.NewCnd().Eq("leader_user_id", operator.UserID).Eq("status", enums.StatusOk))
		for i := range teams {
			scope.CompanyIDs = append(scope.CompanyIDs, utils.SplitInt64s(teams[i].CompanyScopeIDs)...)
			scope.StoreIDs = append(scope.StoreIDs, utils.SplitInt64s(teams[i].StoreScopeIDs)...)
			scope.WxWorkInstanceIDs = append(scope.WxWorkInstanceIDs, utils.SplitInt64s(teams[i].WxWorkInstanceScopeIDs)...)
		}
	}
	if slices.Contains(operator.Roles, constants.RoleCodeStoreStaff) {
		bindings := repositories.StoreStaffBindingRepository.Find(sqls.DB(), sqls.NewCnd().Eq("user_id", operator.UserID).Where("status <> ?", enums.StatusDeleted))
		for i := range bindings {
			scope.CompanyIDs = appendPositive(scope.CompanyIDs, bindings[i].CompanyID)
			scope.StoreIDs = appendPositive(scope.StoreIDs, bindings[i].StoreID)
		}
	}
	scope.expand()
	return scope
}

func (s *agentTeamScopeService) ApplyKnowledgeBaseFilter(cnd *sqls.Cnd, operator *dto.AuthPrincipal) *sqls.Cnd {
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

func (scope *ManagedDataScope) expand() {
	scope.CompanyIDs = uniquePositive(scope.CompanyIDs)
	scope.StoreIDs = uniquePositive(scope.StoreIDs)
	scope.WxWorkInstanceIDs = uniquePositive(scope.WxWorkInstanceIDs)
	if len(scope.CompanyIDs) > 0 {
		stores := repositories.StoreRepository.Find(sqls.DB(), sqls.NewCnd().In("company_id", scope.CompanyIDs).Where("status <> ?", enums.StatusDeleted))
		for i := range stores {
			scope.StoreIDs = appendPositive(scope.StoreIDs, stores[i].ID)
		}
	}
	scope.StoreIDs = uniquePositive(scope.StoreIDs)
	if len(scope.StoreIDs) > 0 {
		stores := repositories.StoreRepository.Find(sqls.DB(), sqls.NewCnd().In("id", scope.StoreIDs).Where("status <> ?", enums.StatusDeleted))
		for i := range stores {
			scope.CompanyIDs = appendPositive(scope.CompanyIDs, stores[i].CompanyID)
			scope.KnowledgeBaseIDs = appendPositive(scope.KnowledgeBaseIDs, stores[i].KnowledgeBaseID)
		}
		instances := repositories.WxWorkProtocolInstanceRepository.Find(sqls.DB(), sqls.NewCnd().In("store_id", scope.StoreIDs).Where("status <> ?", enums.StatusDeleted))
		for i := range instances {
			scope.WxWorkInstanceIDs = appendPositive(scope.WxWorkInstanceIDs, instances[i].ID)
			scope.KnowledgeBaseIDs = appendPositive(scope.KnowledgeBaseIDs, instances[i].KnowledgeBaseID)
		}
	}
	if len(scope.WxWorkInstanceIDs) > 0 {
		instances := repositories.WxWorkProtocolInstanceRepository.Find(sqls.DB(), sqls.NewCnd().In("id", scope.WxWorkInstanceIDs).Where("status <> ?", enums.StatusDeleted))
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
