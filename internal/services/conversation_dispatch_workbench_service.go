package services

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"agent-desk/internal/events"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/dto/response"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/errorsx"
	"agent-desk/internal/pkg/eventbus"
	"agent-desk/internal/pkg/utils"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
)

var ConversationDispatchWorkbenchService = newConversationDispatchWorkbenchService()

const (
	ConversationDispatchStatusAll        = "all"
	ConversationDispatchStatusPending    = "pending"
	ConversationDispatchStatusAssigned   = "assigned"
	ConversationDispatchStatusProcessing = "processing"
	ConversationDispatchStatusWarning    = "warning"
	ConversationDispatchStatusTimeout    = "timeout"
	ConversationDispatchStatusClosed     = "closed"

	dispatchWarningWindow = 5 * time.Minute
)

type conversationDispatchWorkbenchService struct{}

type dispatchWorkbenchTask struct {
	conversation       models.Conversation
	route              *models.ConversationRouteState
	teamID             int64
	teamName           string
	manageable         bool
	status             string
	statusLabel        string
	waitingSeconds     int64
	assignedAt         *time.Time
	firstAgentReplyAt  *time.Time
	recommendedProfile *models.AgentProfile
	recommendation     string
}

type dispatchWorkbenchAgentLoad struct {
	profile           models.AgentProfile
	teamName          string
	username          string
	nickname          string
	activeCount       int
	pendingFirstReply int
	pendingReplyCount int
	processingCount   int
	available         bool
}

func newConversationDispatchWorkbenchService() *conversationDispatchWorkbenchService {
	return &conversationDispatchWorkbenchService{}
}

func (s *conversationDispatchWorkbenchService) ListTasks(req request.ConversationDispatchListRequest, operator *dto.AuthPrincipal, paging *sqls.Paging) ([]response.ConversationDispatchTaskResponse, *sqls.Paging, error) {
	if operator == nil {
		return nil, paging, errorsx.Unauthorized("未登录或登录已过期")
	}
	if paging == nil {
		paging = &sqls.Paging{Page: 1, Limit: 20}
	}
	if paging.Page <= 0 {
		paging.Page = 1
	}
	if paging.Limit <= 0 {
		paging.Limit = 20
	}
	if paging.Limit > 100 {
		paging.Limit = 100
	}

	tasks, err := s.collectTasks(req, operator)
	if err != nil {
		return nil, paging, err
	}
	paging.Total = int64(len(tasks))
	start := (paging.Page - 1) * paging.Limit
	if start >= len(tasks) {
		return []response.ConversationDispatchTaskResponse{}, paging, nil
	}
	end := start + paging.Limit
	if end > len(tasks) {
		end = len(tasks)
	}
	return s.buildTaskResponses(tasks[start:end]), paging, nil
}

func (s *conversationDispatchWorkbenchService) Stats(req request.ConversationDispatchListRequest, operator *dto.AuthPrincipal) (response.ConversationDispatchStatsResponse, error) {
	if operator == nil {
		return response.ConversationDispatchStatsResponse{}, errorsx.Unauthorized("未登录或登录已过期")
	}
	req.Status = ConversationDispatchStatusAll
	tasks, err := s.collectTasks(req, operator)
	if err != nil {
		return response.ConversationDispatchStatsResponse{}, err
	}
	ret := response.ConversationDispatchStatsResponse{Total: len(tasks)}
	for _, task := range tasks {
		switch task.status {
		case ConversationDispatchStatusPending:
			ret.Pending++
			if task.manageable {
				ret.ManageablePending++
			}
		case ConversationDispatchStatusAssigned:
			ret.Assigned++
			if task.manageable {
				ret.ManageableAssigned++
			}
		case ConversationDispatchStatusProcessing:
			ret.Processing++
		case ConversationDispatchStatusWarning:
			ret.Warning++
		case ConversationDispatchStatusTimeout:
			ret.Timeout++
		case ConversationDispatchStatusClosed:
			ret.Closed++
		}
	}
	agents, err := s.ListAgentLoads(req.TeamID, operator)
	if err != nil {
		return ret, err
	}
	for _, agent := range agents {
		if agent.Available {
			ret.AvailableAgents++
		}
	}
	return ret, nil
}

func (s *conversationDispatchWorkbenchService) ListAgentLoads(teamID int64, operator *dto.AuthPrincipal) ([]response.ConversationDispatchAgentLoadResponse, error) {
	if operator == nil {
		return nil, errorsx.Unauthorized("未登录或登录已过期")
	}
	profiles := s.listVisibleAgentProfiles(teamID, operator)
	loads, err := s.buildAgentLoads(profiles)
	if err != nil {
		return nil, err
	}
	ret := make([]response.ConversationDispatchAgentLoadResponse, 0, len(loads))
	for _, load := range loads {
		ret = append(ret, s.buildAgentLoadResponse(load))
	}
	return ret, nil
}

func (s *conversationDispatchWorkbenchService) PendingReplyCountsByTeam() map[int64]int {
	ret := make(map[int64]int)
	routes := repositories.ConversationRouteStateRepository.Find(sqls.DB(), sqls.NewCnd().Eq("need_human_follow_up", true))
	if len(routes) == 0 {
		return ret
	}
	conversationIDs := make([]int64, 0, len(routes))
	for i := range routes {
		conversationIDs = append(conversationIDs, routes[i].ConversationID)
	}
	conversations := ConversationService.Find(sqls.NewCnd().In("id", conversationIDs).In("status", []enums.IMConversationStatus{
		enums.IMConversationStatusPending,
		enums.IMConversationStatusActive,
	}))
	conversationByID := make(map[int64]*models.Conversation, len(conversations))
	assigneeIDs := make([]int64, 0, len(conversations))
	for i := range conversations {
		conversationByID[conversations[i].ID] = &conversations[i]
		if conversations[i].CurrentAssigneeID > 0 {
			assigneeIDs = append(assigneeIDs, conversations[i].CurrentAssigneeID)
		}
	}
	profiles := AgentProfileService.Find(sqls.NewCnd().In("user_id", uniquePositive(assigneeIDs)))
	teamByAssigneeID := make(map[int64]int64, len(profiles))
	for i := range profiles {
		teamByAssigneeID[profiles[i].UserID] = profiles[i].TeamID
	}
	teams := AgentTeamService.Find(sqls.NewCnd().Eq("status", enums.StatusOk))
	for i := range routes {
		route := routes[i]
		conversation := conversationByID[route.ConversationID]
		if conversation == nil {
			continue
		}
		teamID := conversation.CurrentTeamID
		if teamID <= 0 && conversation.CurrentAssigneeID > 0 {
			teamID = teamByAssigneeID[conversation.CurrentAssigneeID]
		}
		if teamID <= 0 {
			for j := range teams {
				if teamCanServeRoute(&teams[j], &route) {
					teamID = teams[j].ID
					break
				}
			}
		}
		if teamID > 0 {
			ret[teamID]++
		}
	}
	return ret
}

func (s *conversationDispatchWorkbenchService) AutoAssign(req request.ConversationDispatchAutoAssignRequest, operator *dto.AuthPrincipal) error {
	if operator == nil {
		return errorsx.Unauthorized("未登录或登录已过期")
	}
	conversation := ConversationService.Get(req.ConversationID)
	if conversation == nil {
		return errorsx.InvalidParam("会话不存在")
	}
	if conversation.Status != enums.IMConversationStatusPending || conversation.CurrentAssigneeID > 0 {
		return errorsx.InvalidParam("只有待派发会话允许自动派发")
	}
	route := ConversationRouteService.GetByConversationID(conversation.ID)
	teamID := req.TeamID
	if teamID <= 0 {
		teamID = s.resolveTaskTeamID(conversation, route)
	}
	if teamID <= 0 {
		return errorsx.InvalidParam("当前会话未匹配客服组，请手动选择客服组")
	}
	if !s.canManageTeam(operator, teamID) {
		return errorsx.Forbidden("无权管理该客服组任务")
	}
	candidates, err := s.pickRuleCandidates(teamID, route)
	if err != nil {
		return err
	}
	if len(candidates) == 0 {
		return errorsx.InvalidParam("当前客服组暂无可自动派发客服")
	}
	return s.assignToProfile(conversation, candidates[0].profile, candidates[0].squadID, "规则自动派发", operator, enums.IMAssignmentTypeAssign, true)
}

func (s *conversationDispatchWorkbenchService) Assign(req request.ConversationDispatchActionRequest, operator *dto.AuthPrincipal) error {
	if operator == nil {
		return errorsx.Unauthorized("未登录或登录已过期")
	}
	conversation := ConversationService.Get(req.ConversationID)
	if conversation == nil {
		return errorsx.InvalidParam("会话不存在")
	}
	if conversation.Status != enums.IMConversationStatusPending || conversation.CurrentAssigneeID > 0 {
		return errorsx.InvalidParam("只有待派发会话允许分配")
	}
	profile, err := s.requireManageableTargetProfile(req.AssigneeID, conversation, operator)
	if err != nil {
		return err
	}
	reason := strings.TrimSpace(req.Reason)
	if reason == "" {
		reason = "组长手动派发"
	}
	return s.assignToProfile(conversation, *profile, 0, reason, operator, enums.IMAssignmentTypeAssign, true)
}

func (s *conversationDispatchWorkbenchService) Transfer(req request.ConversationDispatchActionRequest, operator *dto.AuthPrincipal) error {
	if operator == nil {
		return errorsx.Unauthorized("未登录或登录已过期")
	}
	conversation := ConversationService.Get(req.ConversationID)
	if conversation == nil {
		return errorsx.InvalidParam("会话不存在")
	}
	if conversation.Status != enums.IMConversationStatusActive || conversation.CurrentAssigneeID <= 0 {
		return errorsx.InvalidParam("只有已派发会话允许转派")
	}
	currentTeamID := s.resolveTaskTeamID(conversation, ConversationRouteService.GetByConversationID(conversation.ID))
	if currentTeamID > 0 && !s.canManageTeam(operator, currentTeamID) {
		return errorsx.Forbidden("无权管理该客服组任务")
	}
	if conversation.CurrentAssigneeID == req.AssigneeID {
		return errorsx.InvalidParam("目标客服不能与当前指派人相同")
	}
	profile, err := s.requireManageableTargetProfile(req.AssigneeID, conversation, operator)
	if err != nil {
		return err
	}
	reason := strings.TrimSpace(req.Reason)
	if reason == "" {
		reason = "组长转派"
	}
	return s.assignToProfile(conversation, *profile, 0, reason, operator, enums.IMAssignmentTypeTransfer, true)
}

func (s *conversationDispatchWorkbenchService) Release(req request.ConversationDispatchActionRequest, operator *dto.AuthPrincipal) error {
	if operator == nil {
		return errorsx.Unauthorized("未登录或登录已过期")
	}
	conversation := ConversationService.Get(req.ConversationID)
	if conversation == nil {
		return errorsx.InvalidParam("会话不存在")
	}
	if conversation.Status != enums.IMConversationStatusActive || conversation.CurrentAssigneeID <= 0 {
		return errorsx.InvalidParam("只有已派发会话允许释放")
	}
	route := ConversationRouteService.GetByConversationID(conversation.ID)
	teamID := s.resolveTaskTeamID(conversation, route)
	if teamID <= 0 {
		return errorsx.InvalidParam("当前会话未匹配客服组")
	}
	if !s.canManageTeam(operator, teamID) {
		return errorsx.Forbidden("无权管理该客服组任务")
	}
	reason := strings.TrimSpace(req.Reason)
	if reason == "" {
		reason = "组长释放回待派发池"
	}
	now := time.Now()
	err := sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		current := repositories.ConversationRepository.Get(ctx.Tx, conversation.ID)
		if current == nil {
			return errorsx.InvalidParam("会话不存在")
		}
		if current.Status != enums.IMConversationStatusActive || current.CurrentAssigneeID <= 0 {
			return errorsx.InvalidParam("当前会话状态已变化")
		}
		if err := ConversationAssignmentService.FinishActiveAssignments(ctx, current.ID, now); err != nil {
			return err
		}
		if err := repositories.ConversationRepository.Updates(ctx.Tx, current.ID, map[string]any{
			"status":              enums.IMConversationStatusPending,
			"current_assignee_id": int64(0),
			"current_team_id":     teamID,
			"update_user_id":      operator.UserID,
			"update_user_name":    operator.Username,
			"updated_at":          now,
		}); err != nil {
			return err
		}
		return ConversationEventLogService.CreateEvent(ctx, current.ID, enums.IMEventTypeTransfer, enums.IMSenderTypeAgent, operator.UserID, "会话释放回客服组待派发池", ConversationService.buildEventPayload(map[string]any{
			"fromStatus":     current.Status,
			"toStatus":       enums.IMConversationStatusPending,
			"fromAssigneeId": current.CurrentAssigneeID,
			"toAssigneeId":   int64(0),
			"toTeamId":       teamID,
			"reason":         reason,
		}))
	})
	if err != nil {
		return err
	}
	if _, err := ConversationRouteService.EnterHQAgentDeskPending(conversation.ID, "释放回待派发池:"+reason, now); err != nil {
		return err
	}
	if updated := ConversationService.Get(conversation.ID); updated != nil {
		WsService.PublishConversationChanged(updated, enums.IMRealtimeEventConversationUpdated)
	}
	return nil
}

func (s *conversationDispatchWorkbenchService) collectTasks(req request.ConversationDispatchListRequest, operator *dto.AuthPrincipal) ([]dispatchWorkbenchTask, error) {
	cnd := sqls.NewCnd().In("status", []enums.IMConversationStatus{
		enums.IMConversationStatusPending,
		enums.IMConversationStatusActive,
		enums.IMConversationStatusClosed,
	}).Desc("last_active_at").Desc("id")
	if req.AssigneeID > 0 {
		cnd.Eq("current_assignee_id", req.AssigneeID)
	}
	keyword := strings.TrimSpace(req.Keyword)
	if keyword != "" {
		keywordLike := "%" + keyword + "%"
		cnd.Where("customer_name LIKE ? OR last_message_summary LIKE ?", keywordLike, keywordLike)
	}
	conversations := ConversationService.Find(cnd)
	now := time.Now()
	tasks := make([]dispatchWorkbenchTask, 0, len(conversations))
	for i := range conversations {
		conversation := conversations[i]
		route := ConversationRouteService.GetByConversationID(conversation.ID)
		teamID := s.resolveTaskTeamID(&conversation, route)
		if req.TeamID > 0 && teamID != req.TeamID {
			continue
		}
		manageable := teamID > 0 && s.canManageTeam(operator, teamID)
		if req.OnlyManageable && !manageable {
			continue
		}
		task := s.buildTask(conversation, route, teamID, manageable, now)
		if !s.matchTaskStatus(task.status, req.Status) {
			continue
		}
		tasks = append(tasks, task)
	}
	return tasks, nil
}

func (s *conversationDispatchWorkbenchService) buildTask(conversation models.Conversation, route *models.ConversationRouteState, teamID int64, manageable bool, now time.Time) dispatchWorkbenchTask {
	var assignedAt *time.Time
	if assignment := s.activeAssignment(conversation.ID); assignment != nil {
		assignedAt = &assignment.CreatedAt
	}
	firstReplyAt := s.firstAgentReplyAt(conversation.ID, assignedAt)
	status, label := s.resolveTaskStatus(&conversation, route, assignedAt, firstReplyAt, now)
	baseAt := s.taskWaitingBaseAt(&conversation, route, assignedAt)
	waitingSeconds := int64(0)
	if baseAt != nil && now.After(*baseAt) {
		waitingSeconds = int64(now.Sub(*baseAt).Seconds())
	}
	task := dispatchWorkbenchTask{
		conversation:      conversation,
		route:             route,
		teamID:            teamID,
		teamName:          s.teamName(teamID),
		manageable:        manageable,
		status:            status,
		statusLabel:       label,
		waitingSeconds:    waitingSeconds,
		assignedAt:        assignedAt,
		firstAgentReplyAt: firstReplyAt,
	}
	if teamID > 0 && conversation.Status == enums.IMConversationStatusPending && conversation.CurrentAssigneeID == 0 {
		if candidates, err := s.pickRuleCandidates(teamID, route); err == nil && len(candidates) > 0 {
			profile := candidates[0].profile
			task.recommendedProfile = &profile
			task.recommendation = s.buildRuleRecommendation(candidates[0])
		}
	}
	return task
}

func (s *conversationDispatchWorkbenchService) buildTaskResponses(tasks []dispatchWorkbenchTask) []response.ConversationDispatchTaskResponse {
	ret := make([]response.ConversationDispatchTaskResponse, 0, len(tasks))
	for _, task := range tasks {
		item := task.conversation
		resp := response.ConversationDispatchTaskResponse{
			ConversationID:       item.ID,
			CustomerID:           item.CustomerID,
			CustomerName:         utils.RepairMojibakeText(item.CustomerName),
			Status:               task.status,
			StatusLabel:          task.statusLabel,
			ConversationStatus:   item.Status,
			ServiceMode:          item.ServiceMode,
			TeamID:               task.teamID,
			TeamName:             task.teamName,
			Manageable:           task.manageable,
			CurrentAssigneeID:    item.CurrentAssigneeID,
			LastMessageSummary:   utils.RepairMojibakeText(item.LastMessageSummary),
			LastMessageAt:        utils.FormatTime(item.LastMessageAt),
			WaitingSeconds:       task.waitingSeconds,
			AssignedAt:           utils.FormatTimePtr(task.assignedAt),
			FirstAgentReplyAt:    utils.FormatTimePtr(task.firstAgentReplyAt),
			RecommendationReason: task.recommendation,
		}
		if item.CurrentAssigneeID > 0 {
			resp.CurrentAssigneeName = s.userDisplayName(item.CurrentAssigneeID)
		}
		if task.route != nil {
			resp.RouteStatus = task.route.RouteStatus
			resp.RouteStatusLabel = enums.GetConversationRouteStatusLabel(task.route.RouteStatus)
			resp.StoreID = task.route.StoreID
			resp.WxWorkInstanceID = task.route.WxWorkInstanceID
			resp.HandoffReason = utils.RepairMojibakeText(task.route.HandoffReason)
			resp.ManualExpireAt = utils.FormatTimePtr(task.route.ManualExpireAt)
		}
		if resp.StoreID > 0 {
			if store := StoreService.Get(resp.StoreID); store != nil {
				resp.StoreName = utils.RepairMojibakeText(store.Name)
			}
		}
		if resp.WxWorkInstanceID > 0 {
			if instance := WxWorkProtocolInstanceService.Get(resp.WxWorkInstanceID); instance != nil {
				resp.WxWorkEmployeeName = utils.RepairMojibakeText(instance.EmployeeName)
				resp.WxWorkEmployeeUserID = instance.EmployeeUserID
				if resp.StoreID == 0 {
					resp.StoreID = instance.StoreID
				}
				if resp.StoreName == "" && instance.StoreID > 0 {
					if store := StoreService.Get(instance.StoreID); store != nil {
						resp.StoreName = utils.RepairMojibakeText(store.Name)
					}
				}
			}
		}
		if task.recommendedProfile != nil {
			resp.RecommendedAssigneeID = task.recommendedProfile.UserID
			resp.RecommendedAssigneeName = s.profileDisplayName(task.recommendedProfile)
		}
		ret = append(ret, resp)
	}
	return ret
}

func (s *conversationDispatchWorkbenchService) resolveTaskStatus(conversation *models.Conversation, route *models.ConversationRouteState, assignedAt *time.Time, firstReplyAt *time.Time, now time.Time) (string, string) {
	if conversation == nil {
		return ConversationDispatchStatusClosed, "未知"
	}
	if conversation.Status == enums.IMConversationStatusClosed {
		return ConversationDispatchStatusClosed, "已完成"
	}
	if route != nil && route.ManualExpireAt != nil && now.After(*route.ManualExpireAt) {
		return ConversationDispatchStatusTimeout, "已超时"
	}
	if route != nil && route.ManualExpireAt != nil && route.ManualExpireAt.Sub(now) <= dispatchWarningWindow {
		if conversation.Status == enums.IMConversationStatusPending || firstReplyAt == nil {
			return ConversationDispatchStatusWarning, "即将超时"
		}
	}
	if conversation.Status == enums.IMConversationStatusPending || conversation.CurrentAssigneeID == 0 {
		return ConversationDispatchStatusPending, "待派发"
	}
	if assignedAt != nil && firstReplyAt == nil {
		return ConversationDispatchStatusAssigned, "已派发待首响"
	}
	return ConversationDispatchStatusProcessing, "处理中"
}

func (s *conversationDispatchWorkbenchService) taskWaitingBaseAt(conversation *models.Conversation, route *models.ConversationRouteState, assignedAt *time.Time) *time.Time {
	if conversation == nil {
		return nil
	}
	if conversation.Status == enums.IMConversationStatusActive && assignedAt != nil {
		return assignedAt
	}
	if route != nil && route.LastManualHandoffAt != nil {
		return route.LastManualHandoffAt
	}
	if conversation.HandoffAt != nil {
		return conversation.HandoffAt
	}
	if !conversation.LastActiveAt.IsZero() {
		return &conversation.LastActiveAt
	}
	return &conversation.UpdatedAt
}

func (s *conversationDispatchWorkbenchService) matchTaskStatus(status string, requested string) bool {
	requested = strings.TrimSpace(requested)
	if requested == "" || requested == ConversationDispatchStatusAll {
		return true
	}
	return status == requested
}

func (s *conversationDispatchWorkbenchService) activeAssignment(conversationID int64) *models.ConversationAssignment {
	if conversationID <= 0 {
		return nil
	}
	return ConversationAssignmentService.FindOne(sqls.NewCnd().
		Eq("conversation_id", conversationID).
		Eq("status", enums.IMAssignmentStatusActive).
		Desc("id"))
}

func (s *conversationDispatchWorkbenchService) firstAgentReplyAt(conversationID int64, since *time.Time) *time.Time {
	if conversationID <= 0 {
		return nil
	}
	cnd := sqls.NewCnd().Eq("conversation_id", conversationID).Eq("sender_type", enums.IMSenderTypeAgent).Asc("created_at")
	if since != nil {
		cnd.Where("created_at >= ?", *since)
	}
	message := MessageService.FindOne(cnd)
	if message == nil {
		return nil
	}
	if message.SentAt != nil {
		return message.SentAt
	}
	return &message.CreatedAt
}

func (s *conversationDispatchWorkbenchService) resolveTaskTeamID(conversation *models.Conversation, route *models.ConversationRouteState) int64 {
	if conversation == nil {
		return 0
	}
	if conversation.CurrentTeamID > 0 {
		return conversation.CurrentTeamID
	}
	if conversation.CurrentAssigneeID > 0 {
		if profile := AgentProfileService.GetByUserID(conversation.CurrentAssigneeID); profile != nil && profile.TeamID > 0 {
			return profile.TeamID
		}
	}
	if route != nil {
		if teamID := s.findTeamIDByRoute(route); teamID > 0 {
			return teamID
		}
	}
	if aiAgent := AIAgentService.Get(conversation.AIAgentID); aiAgent != nil {
		teamIDs := utils.SplitInt64s(aiAgent.TeamIDs)
		if len(teamIDs) > 0 {
			return teamIDs[0]
		}
	}
	return 0
}

func (s *conversationDispatchWorkbenchService) findTeamIDByRoute(route *models.ConversationRouteState) int64 {
	if route == nil {
		return 0
	}
	teams := AgentTeamService.Find(sqls.NewCnd().Eq("status", enums.StatusOk).Asc("id"))
	for _, team := range teams {
		storeIDs := utils.SplitInt64s(team.StoreScopeIDs)
		instanceIDs := utils.SplitInt64s(team.WxWorkInstanceScopeIDs)
		if route.StoreID > 0 && containsInt64(storeIDs, route.StoreID) {
			return team.ID
		}
		if route.WxWorkInstanceID > 0 && containsInt64(instanceIDs, route.WxWorkInstanceID) {
			return team.ID
		}
	}
	return 0
}

func (s *conversationDispatchWorkbenchService) listManageableTeamIDs(operator *dto.AuthPrincipal) []int64 {
	if operator == nil {
		return nil
	}
	if s.isAdmin(operator) {
		teams := AgentTeamService.Find(sqls.NewCnd().Eq("status", enums.StatusOk))
		ret := make([]int64, 0, len(teams))
		for _, team := range teams {
			ret = append(ret, team.ID)
		}
		return ret
	}
	if !slices.Contains(operator.Roles, constants.RoleCodeCsTeamLeader) {
		return nil
	}
	teams := AgentTeamService.Find(sqls.NewCnd().Eq("leader_user_id", operator.UserID).Eq("status", enums.StatusOk))
	ret := make([]int64, 0, len(teams))
	for _, team := range teams {
		ret = append(ret, team.ID)
	}
	return ret
}

func (s *conversationDispatchWorkbenchService) canManageTeam(operator *dto.AuthPrincipal, teamID int64) bool {
	if operator == nil || teamID <= 0 {
		return false
	}
	if s.isAdmin(operator) {
		return true
	}
	return containsInt64(s.listManageableTeamIDs(operator), teamID)
}

func (s *conversationDispatchWorkbenchService) isAdmin(operator *dto.AuthPrincipal) bool {
	return operator != nil && (slices.Contains(operator.Roles, constants.RoleCodeSuperAdmin) || slices.Contains(operator.Roles, constants.RoleCodeAdmin))
}

func (s *conversationDispatchWorkbenchService) listVisibleAgentProfiles(teamID int64, operator *dto.AuthPrincipal) []models.AgentProfile {
	cnd := sqls.NewCnd().Where("status <> ?", enums.StatusDeleted)
	if teamID > 0 {
		cnd.Eq("team_id", teamID)
	}
	return AgentProfileService.Find(cnd.Asc("team_id").Desc("priority_level").Asc("id"))
}

func (s *conversationDispatchWorkbenchService) buildAgentLoads(profiles []models.AgentProfile) ([]dispatchWorkbenchAgentLoad, error) {
	userIDs := make([]int64, 0, len(profiles))
	for _, profile := range profiles {
		if profile.UserID > 0 {
			userIDs = append(userIDs, profile.UserID)
		}
	}
	activeCounts, err := ConversationDispatchService.findActiveConversationCountMap(userIDs)
	if err != nil {
		return nil, err
	}
	ret := make([]dispatchWorkbenchAgentLoad, 0, len(profiles))
	for _, profile := range profiles {
		load := dispatchWorkbenchAgentLoad{
			profile:     profile,
			teamName:    s.teamName(profile.TeamID),
			activeCount: activeCounts[profile.UserID],
		}
		if user := UserService.Get(profile.UserID); user != nil {
			load.username = user.Username
			load.nickname = user.Nickname
		}
		load.pendingFirstReply, load.processingCount = s.countAgentTaskPhases(profile.UserID)
		load.pendingReplyCount = s.countAgentPendingReplies(profile.UserID)
		load.available = s.profileAvailable(profile, load.activeCount)
		ret = append(ret, load)
	}
	return ret, nil
}

func (s *conversationDispatchWorkbenchService) countAgentPendingReplies(userID int64) int {
	if userID <= 0 {
		return 0
	}
	conversations := ConversationService.Find(sqls.NewCnd().
		Eq("status", enums.IMConversationStatusActive).
		Eq("current_assignee_id", userID))
	count := 0
	for i := range conversations {
		if route := ConversationRouteService.GetByConversationID(conversations[i].ID); route != nil && route.NeedHumanFollowUp {
			count++
		}
	}
	return count
}

func (s *conversationDispatchWorkbenchService) countAgentTaskPhases(userID int64) (int, int) {
	if userID <= 0 {
		return 0, 0
	}
	conversations := ConversationService.Find(sqls.NewCnd().
		Eq("status", enums.IMConversationStatusActive).
		Eq("current_assignee_id", userID))
	pendingFirstReply := 0
	processing := 0
	for _, conversation := range conversations {
		assignment := s.activeAssignment(conversation.ID)
		var assignedAt *time.Time
		if assignment != nil {
			assignedAt = &assignment.CreatedAt
		}
		if s.firstAgentReplyAt(conversation.ID, assignedAt) == nil {
			pendingFirstReply++
		} else {
			processing++
		}
	}
	return pendingFirstReply, processing
}

func (s *conversationDispatchWorkbenchService) buildAgentLoadResponse(load dispatchWorkbenchAgentLoad) response.ConversationDispatchAgentLoadResponse {
	return response.ConversationDispatchAgentLoadResponse{
		UserID:             load.profile.UserID,
		ProfileID:          load.profile.ID,
		TeamID:             load.profile.TeamID,
		TeamName:           load.teamName,
		Username:           load.username,
		Nickname:           utils.RepairMojibakeText(load.nickname),
		DisplayName:        utils.RepairMojibakeText(load.profile.DisplayName),
		ServiceStatus:      load.profile.ServiceStatus,
		MaxConcurrentCount: load.profile.MaxConcurrentCount,
		ActiveCount:        load.activeCount,
		PendingFirstReply:  load.pendingFirstReply,
		PendingReplyCount:  load.pendingReplyCount,
		ProcessingCount:    load.processingCount,
		AutoAssignEnabled:  load.profile.AutoAssignEnabled,
		Available:          load.available,
		PriorityLevel:      load.profile.PriorityLevel,
		LastOnlineAt:       utils.FormatTimePtr(load.profile.LastOnlineAt),
		LastStatusAt:       utils.FormatTimePtr(load.profile.LastStatusAt),
	}
}

func (s *conversationDispatchWorkbenchService) pickRuleCandidates(teamID int64, route *models.ConversationRouteState) ([]dispatchCandidate, error) {
	candidates, _, err := ConversationDispatchService.pickDispatchCandidates([]int64{teamID}, route, time.Now())
	return candidates, err
}

func (s *conversationDispatchWorkbenchService) profileAvailable(profile models.AgentProfile, activeCount int) bool {
	if profile.Status != enums.StatusOk || !profile.AutoAssignEnabled || profile.ServiceStatus != enums.ServiceStatusIdle {
		return false
	}
	return profile.MaxConcurrentCount <= 0 || activeCount < profile.MaxConcurrentCount
}

func (s *conversationDispatchWorkbenchService) requireManageableTargetProfile(userID int64, conversation *models.Conversation, operator *dto.AuthPrincipal) (*models.AgentProfile, error) {
	if userID <= 0 {
		return nil, errorsx.InvalidParam("请选择目标客服")
	}
	profile := AgentProfileService.GetByUserID(userID)
	if profile == nil || profile.Status != enums.StatusOk {
		return nil, errorsx.InvalidParam("目标客服不存在")
	}
	ownerTeamID := conversation.CurrentTeamID
	if ownerTeamID <= 0 && conversation.CurrentAssigneeID > 0 {
		if currentProfile := AgentProfileService.GetByUserID(conversation.CurrentAssigneeID); currentProfile != nil {
			ownerTeamID = currentProfile.TeamID
		}
	}
	if ownerTeamID > 0 && profile.TeamID != ownerTeamID {
		return nil, errorsx.Forbidden("目标客服不属于当前会话综合客服组")
	}
	if !s.canManageTeam(operator, profile.TeamID) {
		return nil, errorsx.Forbidden("无权派发到该客服组")
	}
	route := ConversationRouteService.GetByConversationID(conversation.ID)
	if route != nil && !AgentProfileService.ProfileCanServeRoute(profile, route) {
		return nil, errorsx.Forbidden("目标客服不在该会话门店或员工号服务范围内")
	}
	return profile, nil
}

func (s *conversationDispatchWorkbenchService) assignToProfile(conversation *models.Conversation, profile models.AgentProfile, squadID int64, reason string, operator *dto.AuthPrincipal, assignType enums.IMAssignmentType, publish bool) error {
	if conversation == nil {
		return errorsx.InvalidParam("会话不存在")
	}
	if operator == nil {
		return errorsx.Unauthorized("未登录或登录已过期")
	}
	reason = strings.TrimSpace(reason)
	now := time.Now()
	var assignedEvent events.ConversationAssignedEvent
	err := sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		current := repositories.ConversationRepository.Get(ctx.Tx, conversation.ID)
		if current == nil {
			return errorsx.InvalidParam("会话不存在")
		}
		if current.Status != enums.IMConversationStatusPending && current.Status != enums.IMConversationStatusActive {
			return errorsx.InvalidParam("当前会话状态不允许派发")
		}
		if current.Status == enums.IMConversationStatusPending && current.CurrentAssigneeID > 0 {
			return errorsx.InvalidParam("当前会话已分配客服")
		}
		if err := ConversationAssignmentService.FinishActiveAssignments(ctx, current.ID, now); err != nil {
			return err
		}
		if err := ConversationAssignmentService.CreateAssignmentWithSquad(ctx, current.ID, squadID, current.CurrentAssigneeID, profile.UserID, assignType, reason, operator, now); err != nil {
			return err
		}
		if err := repositories.ConversationRepository.Updates(ctx.Tx, current.ID, map[string]any{
			"current_assignee_id": profile.UserID,
			"current_team_id":     profile.TeamID,
			"status":              enums.IMConversationStatusActive,
			"update_user_id":      operator.UserID,
			"update_user_name":    operator.Username,
			"updated_at":          now,
		}); err != nil {
			return err
		}
		eventType := enums.IMEventTypeAssign
		eventTitle := "会话已派发"
		if assignType == enums.IMAssignmentTypeTransfer {
			eventType = enums.IMEventTypeTransfer
			eventTitle = "会话已转派"
		}
		if err := ConversationEventLogService.CreateEvent(ctx, current.ID, eventType, enums.IMSenderTypeAgent, operator.UserID, eventTitle, ConversationService.buildEventPayload(map[string]any{
			"fromStatus":     current.Status,
			"toStatus":       enums.IMConversationStatusActive,
			"fromAssigneeId": current.CurrentAssigneeID,
			"toAssigneeId":   profile.UserID,
			"toTeamId":       profile.TeamID,
			"reason":         reason,
			"dispatchMode":   "rule_or_manual",
		})); err != nil {
			return err
		}
		assignedEvent = events.ConversationAssignedEvent{
			ConversationID: current.ID,
			FromUserID:     current.CurrentAssigneeID,
			ToUserID:       profile.UserID,
			OperatorID:     operator.UserID,
			Reason:         reason,
			AssignType:     events.ConversationAssignTypeAssign,
		}
		if assignType == enums.IMAssignmentTypeTransfer {
			assignedEvent.AssignType = events.ConversationAssignTypeTransfer
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, errConversationDispatchConflict) {
			return errorsx.InvalidParam("当前会话状态已变化")
		}
		return err
	}
	if _, err := ConversationRouteService.EnterHQAgentDeskServing(conversation.ID, "客服组派单:"+reason, now); err != nil {
		return err
	}
	if updated := ConversationService.Get(conversation.ID); updated != nil && publish {
		eventName := enums.IMRealtimeEventConversationAssigned
		if assignType == enums.IMAssignmentTypeTransfer {
			eventName = enums.IMRealtimeEventConversationTransferred
		}
		WsService.PublishConversationChanged(updated, eventName)
	}
	if assignedEvent.ConversationID > 0 {
		eventbus.PublishAsync(context.Background(), assignedEvent)
	}
	return nil
}

func (s *conversationDispatchWorkbenchService) buildRuleRecommendation(candidate dispatchCandidate) string {
	name := s.profileDisplayName(&candidate.profile)
	parts := []string{"规则推荐"}
	if name != "" {
		parts = append(parts, name)
	}
	parts = append(parts, "当前负载较低")
	if candidate.profile.PriorityLevel > 0 {
		parts = append(parts, "优先级较高")
	}
	return strings.Join(parts, "，")
}

func (s *conversationDispatchWorkbenchService) teamName(teamID int64) string {
	if teamID <= 0 {
		return ""
	}
	if team := AgentTeamService.Get(teamID); team != nil {
		return utils.RepairMojibakeText(team.Name)
	}
	return ""
}

func (s *conversationDispatchWorkbenchService) userDisplayName(userID int64) string {
	if userID <= 0 {
		return ""
	}
	if user := UserService.Get(userID); user != nil {
		if name := strings.TrimSpace(user.Nickname); name != "" {
			return utils.RepairMojibakeText(name)
		}
		return utils.RepairMojibakeText(user.Username)
	}
	return ""
}

func (s *conversationDispatchWorkbenchService) profileDisplayName(profile *models.AgentProfile) string {
	if profile == nil {
		return ""
	}
	if name := strings.TrimSpace(profile.DisplayName); name != "" {
		return utils.RepairMojibakeText(name)
	}
	return s.userDisplayName(profile.UserID)
}
