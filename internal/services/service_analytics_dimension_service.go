package services

import (
	"slices"
	"sort"
	"strings"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/errorsx"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
)

type ServiceAnalyticsDimensionItem struct {
	ID       int64
	Name     string
	ParentID int64
}

type ServiceAnalyticsDimensions struct {
	Teams           []ServiceAnalyticsDimensionItem
	Squads          []ServiceAnalyticsDimensionItem
	Agents          []ServiceAnalyticsDimensionItem
	Channels        []ServiceAnalyticsDimensionItem
	Stores          []ServiceAnalyticsDimensionItem
	WxWorkInstances []ServiceAnalyticsDimensionItem
}

func (s *serviceAnalyticsService) GetDimensions(operator *dto.AuthPrincipal) (*ServiceAnalyticsDimensions, error) {
	tenantID := AgentTeamScopeService.ActiveTenantID(operator)
	if tenantID <= 0 {
		return nil, errorsx.Forbidden("请先进入需要查看统计的接入公司")
	}
	ret := &ServiceAnalyticsDimensions{}
	teams := s.analyticsTeams(tenantID, operator)
	teamIDs := make([]int64, 0, len(teams))
	for _, team := range teams {
		teamIDs = append(teamIDs, team.ID)
		ret.Teams = append(ret.Teams, ServiceAnalyticsDimensionItem{ID: team.ID, Name: team.Name})
	}
	if !AgentTeamScopeService.IsAdmin(operator) && len(teamIDs) == 0 {
		return ret, nil
	}
	if len(teamIDs) > 0 {
		for _, squad := range repositories.AgentTeamSquadRepository.Find(sqls.DB(), sqls.NewCnd().
			Eq("tenant_id", tenantID).
			In("team_id", teamIDs).
			Where("status <> ?", enums.StatusDeleted).
			Asc("team_id").Asc("name").Asc("id")) {
			ret.Squads = append(ret.Squads, ServiceAnalyticsDimensionItem{ID: squad.ID, Name: squad.Name, ParentID: squad.TeamID})
		}
		for _, profile := range repositories.AgentProfileRepository.Find(sqls.DB(), sqls.NewCnd().
			Eq("tenant_id", tenantID).
			In("team_id", teamIDs).
			Where("status <> ?", enums.StatusDeleted).
			Asc("display_name").Asc("id")) {
			if !s.canViewAgent(operator, profile.UserID, profile.TeamID) {
				continue
			}
			name := strings.TrimSpace(profile.DisplayName)
			if name == "" {
				if user := UserService.GetInTenant(profile.UserID, tenantID); user != nil {
					name = strings.TrimSpace(user.Nickname)
					if name == "" {
						name = strings.TrimSpace(user.Username)
					}
				}
			}
			ret.Agents = append(ret.Agents, ServiceAnalyticsDimensionItem{ID: profile.UserID, Name: name, ParentID: profile.TeamID})
		}
	}

	scope := AgentTeamScopeService.Resolve(operator)
	instanceCnd := sqls.NewCnd().Eq("tenant_id", tenantID).Where("status <> ?", enums.StatusDeleted).Asc("employee_name").Asc("id")
	if !scope.Unrestricted {
		if len(scope.WxWorkInstanceIDs) > 0 {
			instanceCnd.In("id", scope.WxWorkInstanceIDs)
		} else if len(scope.StoreIDs) > 0 {
			instanceCnd.In("store_id", scope.StoreIDs)
		}
	}
	instances := repositories.WxWorkProtocolInstanceRepository.Find(sqls.DB(), instanceCnd)
	visibleStoreIDs := append([]int64(nil), scope.StoreIDs...)
	for _, instance := range instances {
		ret.WxWorkInstances = append(ret.WxWorkInstances, ServiceAnalyticsDimensionItem{ID: instance.ID, Name: instance.EmployeeName, ParentID: instance.StoreID})
		if instance.StoreID > 0 && !slices.Contains(visibleStoreIDs, instance.StoreID) {
			visibleStoreIDs = append(visibleStoreIDs, instance.StoreID)
		}
	}
	storeCnd := sqls.NewCnd().Eq("tenant_id", tenantID).Where("status <> ?", enums.StatusDeleted).Asc("name").Asc("id")
	if !scope.Unrestricted && len(visibleStoreIDs) > 0 {
		storeCnd.In("id", visibleStoreIDs)
	}
	for _, store := range repositories.StoreRepository.Find(sqls.DB(), storeCnd) {
		ret.Stores = append(ret.Stores, ServiceAnalyticsDimensionItem{ID: store.ID, Name: store.Name})
	}
	for _, channel := range repositories.ChannelRepository.Find(sqls.DB(), sqls.NewCnd().
		Eq("tenant_id", tenantID).
		Where("status <> ?", enums.StatusDeleted).
		Asc("name").Asc("id")) {
		name := strings.TrimSpace(channel.Name)
		if name == "" {
			name = strings.TrimSpace(channel.ChannelType)
		}
		ret.Channels = append(ret.Channels, ServiceAnalyticsDimensionItem{ID: channel.ID, Name: name})
	}
	return ret, nil
}

func (s *serviceAnalyticsService) analyticsTeams(tenantID int64, operator *dto.AuthPrincipal) []models.AgentTeam {
	if AgentTeamScopeService.IsAdmin(operator) {
		return repositories.AgentTeamRepository.Find(sqls.DB(), sqls.NewCnd().
			Eq("tenant_id", tenantID).
			Where("status <> ?", enums.StatusDeleted).
			Asc("name").Asc("id"))
	}
	ids := make([]int64, 0)
	if slices.Contains(operator.Roles, constants.RoleCodeCsTeamLeader) {
		for _, team := range repositories.AgentTeamRepository.Find(sqls.DB(), sqls.NewCnd().
			Eq("tenant_id", tenantID).
			Eq("leader_user_id", operator.UserID).
			Where("status <> ?", enums.StatusDeleted)) {
			ids = append(ids, team.ID)
		}
	}
	if slices.Contains(operator.Roles, constants.RoleCodeCsUser) {
		if profile := repositories.AgentProfileRepository.Take(sqls.DB(), "tenant_id = ? AND user_id = ? AND status <> ?", tenantID, operator.UserID, enums.StatusDeleted); profile != nil {
			ids = append(ids, profile.TeamID)
		}
	}
	ids = uniqueAnalyticsIDs(ids)
	if len(ids) == 0 {
		return nil
	}
	list := repositories.AgentTeamRepository.Find(sqls.DB(), sqls.NewCnd().
		Eq("tenant_id", tenantID).
		In("id", ids).
		Where("status <> ?", enums.StatusDeleted))
	sort.Slice(list, func(i, j int) bool {
		if list[i].Name == list[j].Name {
			return list[i].ID < list[j].ID
		}
		return list[i].Name < list[j].Name
	})
	return list
}

func uniqueAnalyticsIDs(values []int64) []int64 {
	ret := make([]int64, 0, len(values))
	seen := map[int64]struct{}{}
	for _, value := range values {
		if value <= 0 {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		ret = append(ret, value)
	}
	return ret
}
