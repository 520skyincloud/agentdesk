package services

import (
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/errorsx"
	"agent-desk/internal/pkg/utils"
	"agent-desk/internal/repositories"
	"strings"
	"time"

	"agent-desk/internal/pkg/httpx/params"

	"github.com/mlogclub/simple/sqls"
)

var AgentProfileService = newAgentProfileService()

func newAgentProfileService() *agentProfileService {
	return &agentProfileService{}
}

type agentProfileService struct {
}

func (s *agentProfileService) Get(id int64) *models.AgentProfile {
	return repositories.AgentProfileRepository.Get(sqls.DB(), id)
}

func (s *agentProfileService) Take(where ...interface{}) *models.AgentProfile {
	return repositories.AgentProfileRepository.Take(sqls.DB(), where...)
}

func (s *agentProfileService) Find(cnd *sqls.Cnd) []models.AgentProfile {
	return repositories.AgentProfileRepository.Find(sqls.DB(), cnd)
}

func (s *agentProfileService) FindOne(cnd *sqls.Cnd) *models.AgentProfile {
	return repositories.AgentProfileRepository.FindOne(sqls.DB(), cnd)
}

func (s *agentProfileService) FindPageByParams(params *params.QueryParams) (list []models.AgentProfile, paging *sqls.Paging) {
	return repositories.AgentProfileRepository.FindPageByParams(sqls.DB(), params)
}

func (s *agentProfileService) FindPageByCnd(cnd *sqls.Cnd) (list []models.AgentProfile, paging *sqls.Paging) {
	return repositories.AgentProfileRepository.FindPageByCnd(sqls.DB(), cnd)
}

func (s *agentProfileService) Count(cnd *sqls.Cnd) int64 {
	return repositories.AgentProfileRepository.Count(sqls.DB(), cnd)
}

func (s *agentProfileService) GetByUserID(userID int64) *models.AgentProfile {
	if userID <= 0 {
		return nil
	}
	return repositories.AgentProfileRepository.FindOne(sqls.DB(), sqls.NewCnd().Eq("user_id", userID))
}

func (s *agentProfileService) MarkUserOnline(userID int64, username string, now time.Time) error {
	if userID <= 0 {
		return nil
	}
	profile := s.GetByUserID(userID)
	if profile == nil {
		return nil
	}
	columns := map[string]any{
		"last_online_at":   now,
		"update_user_id":   userID,
		"update_user_name": strings.TrimSpace(username),
		"updated_at":       now,
	}
	if profile.LastStatusAt == nil {
		columns["last_status_at"] = now
	}
	return repositories.AgentProfileRepository.Updates(sqls.DB(), profile.ID, columns)
}

func (s *agentProfileService) GetUserIDsByTeamID(teamID int64) []int64 {
	if teamID <= 0 {
		return nil
	}
	list := s.Find(sqls.NewCnd().Eq("team_id", teamID))
	if len(list) == 0 {
		return nil
	}
	result := make([]int64, 0, len(list))
	for _, item := range list {
		if item.UserID > 0 {
			result = append(result, item.UserID)
		}
	}
	return result
}

func (s *agentProfileService) GetActiveAgentUserIDs() []int64 {
	list := s.Find(sqls.NewCnd().Eq("status", enums.StatusOk))
	if len(list) == 0 {
		return nil
	}
	seen := make(map[int64]struct{}, len(list))
	result := make([]int64, 0, len(list))
	for _, item := range list {
		if item.UserID <= 0 {
			continue
		}
		if _, ok := seen[item.UserID]; ok {
			continue
		}
		seen[item.UserID] = struct{}{}
		result = append(result, item.UserID)
	}
	return result
}

// GetDispatchAgents 获取可用于分配会话的客服
func (s *agentProfileService) GetDispatchAgents(teamIds []int64) []models.AgentProfile {
	return AgentProfileService.Find(sqls.NewCnd().
		In("team_id", teamIds).
		Eq("status", enums.StatusOk).
		Eq("auto_assign_enabled", true).
		Eq("service_status", enums.ServiceStatusIdle))
}

func (s *agentProfileService) CanServeConversation(userID int64, conversationID int64) bool {
	if userID <= 0 || conversationID <= 0 {
		return false
	}
	profile := s.Take("user_id = ? AND status = ?", userID, enums.StatusOk)
	if profile == nil {
		return false
	}
	route := repositories.ConversationRouteStateRepository.Take(sqls.DB(), "conversation_id = ?", conversationID)
	if route == nil {
		return true
	}
	return s.ProfileCanServeRoute(profile, route)
}

func (s *agentProfileService) ProfileCanServeRoute(profile *models.AgentProfile, route *models.ConversationRouteState) bool {
	if profile == nil || route == nil {
		return false
	}
	team := AgentTeamService.Get(profile.TeamID)
	profileStoreIDs := utils.SplitInt64s(profile.StoreScopeIDs)
	profileInstanceIDs := utils.SplitInt64s(profile.WxWorkInstanceScopeIDs)
	teamStoreIDs := []int64{}
	teamInstanceIDs := []int64{}
	if team != nil {
		teamStoreIDs = utils.SplitInt64s(team.StoreScopeIDs)
		teamInstanceIDs = utils.SplitInt64s(team.WxWorkInstanceScopeIDs)
	}
	if len(profileStoreIDs) == 0 && len(profileInstanceIDs) == 0 && len(teamStoreIDs) == 0 && len(teamInstanceIDs) == 0 {
		return true
	}
	if route.StoreID > 0 && (containsInt64(profileStoreIDs, route.StoreID) || containsInt64(teamStoreIDs, route.StoreID)) {
		return true
	}
	if route.WxWorkInstanceID > 0 && (containsInt64(profileInstanceIDs, route.WxWorkInstanceID) || containsInt64(teamInstanceIDs, route.WxWorkInstanceID)) {
		return true
	}
	return false
}

func (s *agentProfileService) CreateAgentProfile(req request.CreateAgentProfileRequest, operator *dto.AuthPrincipal) (*models.AgentProfile, error) {
	if operator == nil {
		return nil, errorsx.Unauthorized("未登录或登录已过期")
	}
	item, err := s.buildProfileModel(0, req)
	if err != nil {
		return nil, err
	}
	item.AuditFields = utils.BuildAuditFields(operator)
	if err := repositories.AgentProfileRepository.Create(sqls.DB(), item); err != nil {
		return nil, err
	}
	s.dispatchPendingConversationsIfEligible(item)
	return item, nil
}

func containsInt64(values []int64, target int64) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func (s *agentProfileService) UpdateAgentProfile(req request.UpdateAgentProfileRequest, operator *dto.AuthPrincipal) error {
	if operator == nil {
		return errorsx.Unauthorized("未登录或登录已过期")
	}
	current := s.Get(req.ID)
	if current == nil {
		return errorsx.InvalidParam("客服档案不存在")
	}
	item, err := s.buildProfileModel(req.ID, req.CreateAgentProfileRequest)
	if err != nil {
		return err
	}
	if err := repositories.AgentProfileRepository.Updates(sqls.DB(), req.ID, map[string]any{
		"user_id":                    item.UserID,
		"team_id":                    item.TeamID,
		"store_scope_ids":            item.StoreScopeIDs,
		"wx_work_instance_scope_ids": item.WxWorkInstanceScopeIDs,
		"agent_code":                 item.AgentCode,
		"display_name":               item.DisplayName,
		"avatar":                     item.Avatar,
		"service_status":             item.ServiceStatus,
		"max_concurrent_count":       item.MaxConcurrentCount,
		"priority_level":             item.PriorityLevel,
		"auto_assign_enabled":        item.AutoAssignEnabled,
		"receive_offline_message":    item.ReceiveOfflineMessage,
		"remark":                     item.Remark,
		"update_user_id":             operator.UserID,
		"update_user_name":           operator.Username,
		"updated_at":                 time.Now(),
	}); err != nil {
		return err
	}
	s.dispatchPendingConversationsIfEligible(item)
	return nil
}

func (s *agentProfileService) DeleteAgentProfile(id int64) error {
	current := s.Get(id)
	if current == nil {
		return errorsx.InvalidParam("客服档案不存在")
	}
	repositories.AgentProfileRepository.Delete(sqls.DB(), id)
	return nil
}

func (s *agentProfileService) buildProfileModel(id int64, req request.CreateAgentProfileRequest) (*models.AgentProfile, error) {
	if req.UserID <= 0 {
		return nil, errorsx.InvalidParam("请选择关联用户")
	}
	if UserService.Get(req.UserID) == nil {
		return nil, errorsx.InvalidParam("关联用户不存在")
	}
	if req.TeamID <= 0 {
		return nil, errorsx.InvalidParam("请选择所属客服组")
	}
	if AgentTeamService.Get(req.TeamID) == nil {
		return nil, errorsx.InvalidParam("所属客服组不存在")
	}
	req.AgentCode = strings.TrimSpace(req.AgentCode)
	req.DisplayName = strings.TrimSpace(req.DisplayName)
	if req.AgentCode == "" || req.DisplayName == "" {
		return nil, errorsx.InvalidParam("客服工号和展示名不能为空")
	}
	if exists := s.Take("user_id = ? AND id <> ?", req.UserID, id); exists != nil {
		return nil, errorsx.InvalidParam("该用户已存在客服档案")
	}
	if exists := s.Take("agent_code = ? AND id <> ?", req.AgentCode, id); exists != nil {
		return nil, errorsx.InvalidParam("客服工号已存在")
	}
	if !enums.IsValidServiceStatus(req.ServiceStatus) {
		return nil, errorsx.InvalidParam("客服状态不合法")
	}
	if req.MaxConcurrentCount < 0 {
		return nil, errorsx.InvalidParam("最大并发接待数不能小于 0")
	}
	return &models.AgentProfile{
		UserID:                 req.UserID,
		TeamID:                 req.TeamID,
		StoreScopeIDs:          utils.JoinInt64s(req.StoreScopeIDs),
		WxWorkInstanceScopeIDs: utils.JoinInt64s(req.WxWorkInstanceScopeIDs),
		AgentCode:              req.AgentCode,
		DisplayName:            req.DisplayName,
		Avatar:                 strings.TrimSpace(req.Avatar),
		ServiceStatus:          req.ServiceStatus,
		MaxConcurrentCount:     req.MaxConcurrentCount,
		PriorityLevel:          req.PriorityLevel,
		AutoAssignEnabled:      req.AutoAssignEnabled,
		ReceiveOfflineMessage:  req.ReceiveOfflineMessage,
		Remark:                 strings.TrimSpace(req.Remark),
	}, nil
}

func (s *agentProfileService) dispatchPendingConversationsIfEligible(item *models.AgentProfile) {
	if item == nil {
		return
	}
	if item.Status != enums.StatusOk {
		return
	}
	if !item.AutoAssignEnabled || item.MaxConcurrentCount <= 0 {
		return
	}
	if item.ServiceStatus != enums.ServiceStatusIdle {
		return
	}
	_, _ = ConversationDispatchService.DispatchPendingConversations(0)
}
