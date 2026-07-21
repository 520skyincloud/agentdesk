package services

import (
	"context"
	"errors"
	"log/slog"
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
	"gorm.io/gorm"
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

	dispatchSLATypeQueue         = "queue"
	dispatchSLATypeFirstResponse = "first_response"
	dispatchSLAStatusNormal      = "normal"
	dispatchSLAStatusWarning     = "warning"
	dispatchSLAStatusOverdue     = "overdue"

	dispatchAvailabilityAvailable          = "available"
	dispatchAvailabilityProfileDisabled    = "profile_disabled"
	dispatchAvailabilityAutoAssignDisabled = "auto_assign_disabled"
	dispatchAvailabilityCapacityMissing    = "capacity_missing"
	dispatchAvailabilityAccountDisabled    = "account_disabled"
	dispatchAvailabilityPermissionMissing  = "permission_missing"
	dispatchAvailabilityNoActiveSchedule   = "no_active_schedule"
	dispatchAvailabilityOutOfShift         = "out_of_shift"
	dispatchAvailabilityOffline            = "offline"
	dispatchAvailabilityBreak              = "break"
	dispatchAvailabilityBusy               = "busy"
	dispatchAvailabilityAtCapacity         = "at_capacity"

	dispatchWorkbenchCurrentScanLimit = 5000
	dispatchWorkbenchClosedScanLimit  = 1000
	dispatchWorkbenchClosedLookback   = 24 * time.Hour
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
	slaType            string
	slaStatus          string
	slaDeadlineAt      *time.Time
	slaRemaining       int64
	assignedAt         *time.Time
	firstAgentReplyAt  *time.Time
	recommendedProfile *models.AgentProfile
	recommendation     string
	assignment         *models.ConversationAssignment
	dispatchMode       enums.AgentTeamDispatchMode
	workloadWeight     int
	priority           int
}

type dispatchWorkbenchAgentLoad struct {
	profile             models.AgentProfile
	teamName            string
	username            string
	nickname            string
	activeCount         int
	pendingFirstReply   int
	pendingReplyCount   int
	processingCount     int
	available           bool
	manuallyAssignable  bool
	availabilityCode    string
	availabilityReason  string
	presenceStatus      enums.AgentPresenceStatus
	presenceLastSeenAt  time.Time
	weightedOpenLoad    int
	shiftWorkloadWeight int
	normalizedLoad      float64
}

type dispatchWorkbenchBatchContext struct {
	routeByConversationID      map[int64]*models.ConversationRouteState
	assignmentByConversationID map[int64]*models.ConversationAssignment
	firstReplyByConversationID map[int64]time.Time
	teamByID                   map[int64]models.AgentTeam
	instanceByID               map[int64]models.WxWorkProtocolInstance
	bindingByStoreID           map[int64]models.StoreStaffBinding
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
	loads, err := s.buildAgentLoads(profiles, operator)
	if err != nil {
		return nil, err
	}
	ret := make([]response.ConversationDispatchAgentLoadResponse, 0, len(loads))
	for _, load := range loads {
		ret = append(ret, s.buildAgentLoadResponse(load))
	}
	return ret, nil
}

func (s *conversationDispatchWorkbenchService) PendingReplyCountsByTeam(operator *dto.AuthPrincipal) map[int64]int {
	ret := make(map[int64]int)
	tenantID := AgentTeamScopeService.ActiveTenantID(operator)
	if tenantID <= 0 {
		return ret
	}
	routes := repositories.ConversationRouteStateRepository.Find(sqls.DB(), sqls.NewCnd().Eq("tenant_id", tenantID).Eq("need_human_follow_up", true))
	if len(routes) == 0 {
		return ret
	}
	conversationIDs := make([]int64, 0, len(routes))
	for i := range routes {
		conversationIDs = append(conversationIDs, routes[i].ConversationID)
	}
	conversations := ConversationService.Find(sqls.NewCnd().Eq("tenant_id", tenantID).In("id", conversationIDs).In("status", []enums.IMConversationStatus{
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
	profiles := AgentProfileService.Find(sqls.NewCnd().Eq("tenant_id", tenantID).In("user_id", uniquePositive(assigneeIDs)))
	teamByAssigneeID := make(map[int64]int64, len(profiles))
	for i := range profiles {
		teamByAssigneeID[profiles[i].UserID] = profiles[i].TeamID
	}
	teams := AgentTeamService.Find(sqls.NewCnd().Eq("tenant_id", tenantID).Eq("status", enums.StatusOk))
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
	conversation, err := requireOperatorConversation(sqls.DB(), req.ConversationID, operator)
	if err != nil {
		return err
	}
	if conversation.Status != enums.IMConversationStatusPending || conversation.CurrentAssigneeID > 0 {
		return errorsx.InvalidParam("只有待派发会话允许自动派发")
	}
	route := ConversationRouteService.GetByConversationIDInTenant(conversation.ID, conversation.TenantID)
	teamIDs := ConversationDispatchService.resolveDispatchTeamIDs(conversation, route)
	if len(teamIDs) != 1 {
		return errorsx.InvalidParam("当前会话未匹配唯一客服组，请使用人工派发明确归属")
	}
	teamID := teamIDs[0]
	if req.TeamID > 0 && req.TeamID != teamID {
		return errorsx.InvalidParam("会话归属客服组已变化，请刷新后重试")
	}
	if !s.canManageTeam(operator, teamID) {
		return errorsx.Forbidden("无权管理该客服组任务")
	}
	team := repositories.AgentTeamRepository.GetInTenant(sqls.DB(), teamID, conversation.TenantID)
	if team == nil || team.Status != enums.StatusOk || normalizedDispatchMode(team.DispatchMode) != enums.AgentTeamDispatchModeRule {
		return errorsx.InvalidParam("当前客服组未启用规则派单")
	}
	dispatched, err := ConversationDispatchService.dispatchPendingConversationWithContext(conversation, ruleDispatchExecutionContext{
		operator:       operator,
		expectedTeamID: teamID,
		trigger:        dispatchTriggerOperatorRule,
		interactive:    true,
	})
	if errors.Is(err, errConversationDispatchTeamMismatch) {
		return errorsx.InvalidParam("会话归属客服组已变化，请刷新后重试")
	}
	if errors.Is(err, errConversationDispatchConflict) {
		return errorsx.InvalidParam("派单状态已变化，请刷新后重试")
	}
	if err != nil {
		return err
	}
	if dispatched == nil {
		if latest := ConversationService.Get(conversation.ID); latest != nil && latest.CurrentAssigneeID > 0 {
			return nil
		}
		return errorsx.InvalidParam("当前班次内没有同时满足在线、权限、服务范围和容量约束的客服，任务已保留在待派发池")
	}
	return nil
}

func (s *conversationDispatchWorkbenchService) Assign(req request.ConversationDispatchActionRequest, operator *dto.AuthPrincipal) error {
	if operator == nil {
		return errorsx.Unauthorized("未登录或登录已过期")
	}
	conversation, err := requireOperatorConversation(sqls.DB(), req.ConversationID, operator)
	if err != nil {
		return err
	}
	if conversation.Status != enums.IMConversationStatusPending || conversation.CurrentAssigneeID > 0 {
		return errorsx.InvalidParam("只有待派发会话允许分配")
	}
	profile, err := s.requireManageableTargetProfile(req.AssigneeID, conversation, operator)
	if err != nil {
		return err
	}
	reason, err := requiredManualDispatchReason(req.Reason)
	if err != nil {
		return err
	}
	return s.assignToProfile(conversation, *profile, 0, reason, operator, enums.IMAssignmentTypeAssign, enums.AgentTeamDispatchModeManual, true)
}

func (s *conversationDispatchWorkbenchService) Transfer(req request.ConversationDispatchActionRequest, operator *dto.AuthPrincipal) error {
	if operator == nil {
		return errorsx.Unauthorized("未登录或登录已过期")
	}
	conversation, err := requireOperatorConversation(sqls.DB(), req.ConversationID, operator)
	if err != nil {
		return err
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
	reason, err := requiredManualDispatchReason(req.Reason)
	if err != nil {
		return err
	}
	return s.assignToProfile(conversation, *profile, 0, reason, operator, enums.IMAssignmentTypeTransfer, enums.AgentTeamDispatchModeManual, true)
}

func (s *conversationDispatchWorkbenchService) Release(req request.ConversationDispatchActionRequest, operator *dto.AuthPrincipal) error {
	if operator == nil {
		return errorsx.Unauthorized("未登录或登录已过期")
	}
	conversation, err := requireOperatorConversation(sqls.DB(), req.ConversationID, operator)
	if err != nil {
		return err
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
	reason, err := requiredManualDispatchReason(req.Reason)
	if err != nil {
		return err
	}
	now := time.Now()
	err = sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		current, err := repositories.ConversationRepository.GetForUpdateInTenant(ctx.Tx, conversation.ID, conversation.TenantID)
		if err != nil {
			return err
		}
		if current == nil {
			return errorsx.InvalidParam("会话不存在")
		}
		if current.Status != enums.IMConversationStatusActive || current.CurrentAssigneeID <= 0 {
			return errorsx.InvalidParam("当前会话状态已变化")
		}
		if current.CurrentAssigneeID != conversation.CurrentAssigneeID || current.CurrentTeamID != teamID {
			return errorsx.InvalidParam("当前会话指派关系已变化，请刷新后重试")
		}
		if err := ConversationAssignmentService.FinishActiveAssignments(ctx, current.ID, now); err != nil {
			return err
		}
		if err := repositories.ConversationRepository.UpdatesInTenant(ctx.Tx, current.ID, current.TenantID, map[string]any{
			"status":              enums.IMConversationStatusPending,
			"current_assignee_id": int64(0),
			"current_team_id":     teamID,
			"update_user_id":      operator.UserID,
			"update_user_name":    operator.Username,
			"updated_at":          now,
		}); err != nil {
			return err
		}
		if err := ConversationEventLogService.CreateEvent(ctx, current.ID, enums.IMEventTypeTransfer, enums.IMSenderTypeAgent, operator.UserID, "会话释放回客服组待派发池", ConversationService.buildEventPayload(map[string]any{
			"fromStatus":     current.Status,
			"toStatus":       enums.IMConversationStatusPending,
			"fromAssigneeId": current.CurrentAssigneeID,
			"toAssigneeId":   int64(0),
			"toTeamId":       teamID,
			"reason":         reason,
		})); err != nil {
			return err
		}
		_, err = ConversationRouteService.enterHQAgentDeskPendingWithDB(ctx.Tx, current.ID, "释放回待派发池:"+reason, now)
		return err
	})
	if err != nil {
		return err
	}
	ConversationDispatchService.ScheduleDispatch(conversation.ID)
	if updated := ConversationService.Get(conversation.ID); updated != nil {
		WsService.PublishConversationChanged(updated, enums.IMRealtimeEventConversationUpdated)
	}
	return nil
}

func requiredManualDispatchReason(reason string) (string, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return "", errorsx.InvalidParam("请填写本次人工派单操作原因")
	}
	return reason, nil
}

func (s *conversationDispatchWorkbenchService) collectTasks(req request.ConversationDispatchListRequest, operator *dto.AuthPrincipal) ([]dispatchWorkbenchTask, error) {
	tenantID := AgentTeamScopeService.ActiveTenantID(operator)
	if tenantID <= 0 {
		return nil, errorsx.Forbidden("请先进入需要管理派单的接入公司")
	}
	unrestricted := AgentTeamScopeService.IsAdmin(operator)
	manageableTeamIDs := AgentTeamScopeService.ManageableTeamIDs(operator)
	if !unrestricted && req.TeamID > 0 && !slices.Contains(manageableTeamIDs, req.TeamID) {
		return []dispatchWorkbenchTask{}, nil
	}
	keyword := strings.TrimSpace(req.Keyword)
	requestedStatus := strings.TrimSpace(req.Status)
	includeCurrent := requestedStatus != ConversationDispatchStatusClosed
	includeClosed := requestedStatus == "" || requestedStatus == ConversationDispatchStatusAll || requestedStatus == ConversationDispatchStatusClosed
	conversations, truncated, err := repositories.ConversationRepository.FindDispatchWorkbenchCandidates(
		sqls.DB(), tenantID, req.AssigneeID, keyword, includeCurrent, includeClosed,
		time.Now().Add(-dispatchWorkbenchClosedLookback), dispatchWorkbenchCurrentScanLimit, dispatchWorkbenchClosedScanLimit,
	)
	if err != nil {
		return nil, err
	}
	if truncated {
		slog.Warn("conversation dispatch workbench scan truncated", "tenant_id", tenantID, "current_limit", dispatchWorkbenchCurrentScanLimit, "closed_limit", dispatchWorkbenchClosedScanLimit)
	}
	slices.SortStableFunc(conversations, func(a, b models.Conversation) int {
		switch {
		case a.LastActiveAt.After(b.LastActiveAt):
			return -1
		case a.LastActiveAt.Before(b.LastActiveAt):
			return 1
		case a.ID > b.ID:
			return -1
		case a.ID < b.ID:
			return 1
		default:
			return 0
		}
	})
	batch, err := s.loadTaskBatchContext(conversations, tenantID)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	policy := ServiceAnalyticsService.GetPolicy(tenantID)
	tasks := make([]dispatchWorkbenchTask, 0, len(conversations))
	for i := range conversations {
		conversation := conversations[i]
		route := batch.routeByConversationID[conversation.ID]
		teamID := s.resolveTaskTeamIDFromBatch(&conversation, route, batch)
		if !unrestricted && (teamID <= 0 || !slices.Contains(manageableTeamIDs, teamID)) {
			continue
		}
		if req.TeamID > 0 && teamID != req.TeamID {
			continue
		}
		manageable := teamID > 0 && s.canManageTeam(operator, teamID)
		if req.OnlyManageable && !manageable {
			continue
		}
		assignment := batch.assignmentByConversationID[conversation.ID]
		var firstReplyAt *time.Time
		if value, exists := batch.firstReplyByConversationID[conversation.ID]; exists {
			valueCopy := value
			firstReplyAt = &valueCopy
		}
		teamName := ""
		dispatchMode := enums.AgentTeamDispatchMode("")
		if team, exists := batch.teamByID[teamID]; exists {
			teamName = utils.RepairMojibakeText(team.Name)
			dispatchMode = normalizedDispatchMode(team.DispatchMode)
		}
		task := s.buildTask(conversation, route, teamID, teamName, dispatchMode, manageable, now, assignment, firstReplyAt, policy)
		if !s.matchTaskStatus(task.status, req.Status) {
			continue
		}
		tasks = append(tasks, task)
	}
	return tasks, nil
}

func (s *conversationDispatchWorkbenchService) loadTaskBatchContext(conversations []models.Conversation, tenantID int64) (*dispatchWorkbenchBatchContext, error) {
	ret := &dispatchWorkbenchBatchContext{
		routeByConversationID:      make(map[int64]*models.ConversationRouteState),
		assignmentByConversationID: make(map[int64]*models.ConversationAssignment),
		firstReplyByConversationID: make(map[int64]time.Time),
		teamByID:                   make(map[int64]models.AgentTeam),
		instanceByID:               make(map[int64]models.WxWorkProtocolInstance),
		bindingByStoreID:           make(map[int64]models.StoreStaffBinding),
	}
	if tenantID <= 0 || len(conversations) == 0 {
		return ret, nil
	}
	conversationIDs := make([]int64, 0, len(conversations))
	for i := range conversations {
		conversationIDs = append(conversationIDs, conversations[i].ID)
	}
	conversationIDs = uniquePositive(conversationIDs)
	routes := repositories.ConversationRouteStateRepository.Find(sqls.DB(), sqls.NewCnd().
		Eq("tenant_id", tenantID).
		In("conversation_id", conversationIDs))
	for i := range routes {
		copy := routes[i]
		ret.routeByConversationID[copy.ConversationID] = &copy
	}
	assignments := repositories.ConversationAssignmentRepository.Find(sqls.DB(), sqls.NewCnd().
		Eq("tenant_id", tenantID).
		In("conversation_id", conversationIDs).
		Eq("status", enums.IMAssignmentStatusActive).
		Desc("id"))
	for i := range assignments {
		if _, exists := ret.assignmentByConversationID[assignments[i].ConversationID]; exists {
			continue
		}
		copy := assignments[i]
		ret.assignmentByConversationID[copy.ConversationID] = &copy
	}
	firstReplies, err := repositories.MessageRepository.FindFirstAgentReplyAtForActiveAssignments(sqls.DB(), tenantID, conversationIDs)
	if err != nil {
		return nil, err
	}
	for _, row := range firstReplies {
		ret.firstReplyByConversationID[row.ConversationID] = row.FirstReplyAt
	}
	teams := repositories.AgentTeamRepository.Find(sqls.DB(), sqls.NewCnd().
		Eq("tenant_id", tenantID).
		Eq("status", enums.StatusOk).
		Asc("id"))
	for i := range teams {
		ret.teamByID[teams[i].ID] = teams[i]
	}
	storeIDs := make([]int64, 0)
	instanceIDs := make([]int64, 0)
	for _, route := range ret.routeByConversationID {
		storeIDs = append(storeIDs, route.StoreID)
		instanceIDs = append(instanceIDs, route.WxWorkInstanceID)
	}
	instanceIDs = uniquePositive(instanceIDs)
	if len(instanceIDs) > 0 {
		instances := repositories.WxWorkProtocolInstanceRepository.Find(sqls.DB(), sqls.NewCnd().
			Eq("tenant_id", tenantID).
			In("id", instanceIDs))
		for i := range instances {
			ret.instanceByID[instances[i].ID] = instances[i]
			storeIDs = append(storeIDs, instances[i].StoreID)
		}
	}
	storeIDs = uniquePositive(storeIDs)
	if len(storeIDs) > 0 {
		bindings := repositories.StoreStaffBindingRepository.Find(sqls.DB(), sqls.NewCnd().
			Eq("tenant_id", tenantID).
			In("store_id", storeIDs).
			Eq("status", enums.StatusOk).
			Asc("id"))
		for i := range bindings {
			if _, exists := ret.bindingByStoreID[bindings[i].StoreID]; !exists {
				ret.bindingByStoreID[bindings[i].StoreID] = bindings[i]
			}
		}
	}
	return ret, nil
}

func (s *conversationDispatchWorkbenchService) resolveTaskTeamIDFromBatch(conversation *models.Conversation, route *models.ConversationRouteState, batch *dispatchWorkbenchBatchContext) int64 {
	if conversation == nil || batch == nil || conversation.TenantID <= 0 {
		return 0
	}
	teamIDs := make([]int64, 0, 2)
	appendTeam := func(teamID int64) {
		if teamID <= 0 || slices.Contains(teamIDs, teamID) {
			return
		}
		if _, enabled := batch.teamByID[teamID]; enabled {
			teamIDs = append(teamIDs, teamID)
		}
	}
	if route != nil && route.TenantID == conversation.TenantID {
		if instance, exists := batch.instanceByID[route.WxWorkInstanceID]; exists {
			appendTeam(instance.AgentTeamID)
		}
		if binding, exists := batch.bindingByStoreID[route.StoreID]; exists {
			appendTeam(binding.AgentTeamID)
		}
		if len(teamIDs) == 0 {
			matched := make([]int64, 0, 2)
			for _, team := range batch.teamByID {
				if (route.StoreID > 0 && slices.Contains(utils.SplitInt64s(team.StoreScopeIDs), route.StoreID)) ||
					(route.WxWorkInstanceID > 0 && slices.Contains(utils.SplitInt64s(team.WxWorkInstanceScopeIDs), route.WxWorkInstanceID)) {
					matched = append(matched, team.ID)
				}
			}
			if len(matched) == 1 {
				appendTeam(matched[0])
			}
		}
	}
	if len(teamIDs) > 1 {
		return 0
	}
	if len(teamIDs) == 0 {
		appendTeam(conversation.CurrentTeamID)
	}
	if len(teamIDs) == 0 {
		defaultTeamID := int64(0)
		for _, team := range batch.teamByID {
			if !team.IsDefault {
				continue
			}
			if defaultTeamID > 0 {
				return 0
			}
			defaultTeamID = team.ID
		}
		appendTeam(defaultTeamID)
	}
	if len(teamIDs) == 1 {
		return teamIDs[0]
	}
	return 0
}

func (s *conversationDispatchWorkbenchService) buildTask(conversation models.Conversation, route *models.ConversationRouteState, teamID int64, teamName string, dispatchMode enums.AgentTeamDispatchMode, manageable bool, now time.Time, assignment *models.ConversationAssignment, firstReplyAt *time.Time, policy models.ServiceAnalyticsPolicy) dispatchWorkbenchTask {
	var assignedAt *time.Time
	if assignment != nil {
		assignedAt = &assignment.CreatedAt
	}
	sla := s.resolveTaskSLAWithPolicy(&conversation, route, assignedAt, firstReplyAt, now, policy)
	status, label := s.resolveTaskStatus(&conversation, assignedAt, firstReplyAt, sla)
	workloadWeight, priority := ConversationDispatchService.ruleDispatchAssessmentAt(&conversation, route, now)
	waitingSeconds := int64(0)
	if sla.startedAt != nil && now.After(*sla.startedAt) {
		waitingSeconds = int64(now.Sub(*sla.startedAt).Seconds())
	}
	task := dispatchWorkbenchTask{
		conversation:      conversation,
		route:             route,
		teamID:            teamID,
		teamName:          teamName,
		manageable:        manageable,
		status:            status,
		statusLabel:       label,
		waitingSeconds:    waitingSeconds,
		slaType:           sla.slaType,
		slaStatus:         sla.status,
		slaDeadlineAt:     sla.deadlineAt,
		slaRemaining:      sla.remainingSeconds,
		assignedAt:        assignedAt,
		firstAgentReplyAt: firstReplyAt,
		assignment:        assignment,
		dispatchMode:      dispatchMode,
		workloadWeight:    workloadWeight,
		priority:          priority,
	}
	return task
}

func (s *conversationDispatchWorkbenchService) buildTaskResponses(tasks []dispatchWorkbenchTask) []response.ConversationDispatchTaskResponse {
	if len(tasks) == 0 {
		return []response.ConversationDispatchTaskResponse{}
	}
	for i := range tasks {
		item := &tasks[i].conversation
		if tasks[i].teamID <= 0 || item.Status != enums.IMConversationStatusPending || item.CurrentAssigneeID > 0 {
			continue
		}
		if candidates, err := s.pickRuleCandidates(tasks[i].teamID, item.TenantID, tasks[i].route); err == nil && len(candidates) > 0 {
			decision := ConversationDispatchService.selectDispatchDecision(item, tasks[i].route, candidates)
			profile := decision.candidate.profile
			tasks[i].recommendedProfile = &profile
			tasks[i].recommendation = decision.reason
		}
	}
	tenantID := tasks[0].conversation.TenantID
	userIDs := make([]int64, 0, len(tasks)*2)
	storeIDs := make([]int64, 0, len(tasks))
	instanceIDs := make([]int64, 0, len(tasks))
	for i := range tasks {
		userIDs = append(userIDs, tasks[i].conversation.CurrentAssigneeID)
		if tasks[i].recommendedProfile != nil {
			userIDs = append(userIDs, tasks[i].recommendedProfile.UserID)
		}
		if tasks[i].route != nil {
			storeIDs = append(storeIDs, tasks[i].route.StoreID)
			instanceIDs = append(instanceIDs, tasks[i].route.WxWorkInstanceID)
		}
	}
	instanceByID := make(map[int64]models.WxWorkProtocolInstance)
	instanceIDs = uniquePositive(instanceIDs)
	if len(instanceIDs) > 0 {
		instances := repositories.WxWorkProtocolInstanceRepository.Find(sqls.DB(), sqls.NewCnd().
			Eq("tenant_id", tenantID).
			In("id", instanceIDs))
		for i := range instances {
			instanceByID[instances[i].ID] = instances[i]
			storeIDs = append(storeIDs, instances[i].StoreID)
		}
	}
	storeByID := make(map[int64]models.Store)
	storeIDs = uniquePositive(storeIDs)
	if len(storeIDs) > 0 {
		stores := repositories.StoreRepository.Find(sqls.DB(), sqls.NewCnd().
			Eq("tenant_id", tenantID).
			In("id", storeIDs))
		for i := range stores {
			storeByID[stores[i].ID] = stores[i]
		}
	}
	userByID := make(map[int64]models.User)
	userIDs = uniquePositive(userIDs)
	if len(userIDs) > 0 {
		users := repositories.UserRepository.Find(sqls.DB(), sqls.NewCnd().
			Eq("tenant_id", tenantID).
			In("id", userIDs))
		for i := range users {
			userByID[users[i].ID] = users[i]
		}
	}
	displayUser := func(userID int64) string {
		user, exists := userByID[userID]
		if !exists {
			return ""
		}
		if name := strings.TrimSpace(user.Nickname); name != "" {
			return utils.RepairMojibakeText(name)
		}
		return utils.RepairMojibakeText(user.Username)
	}

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
			SLAType:              task.slaType,
			SLAStatus:            task.slaStatus,
			SLADeadlineAt:        utils.FormatTimePtr(task.slaDeadlineAt),
			SLARemainingSeconds:  task.slaRemaining,
			AssignedAt:           utils.FormatTimePtr(task.assignedAt),
			FirstAgentReplyAt:    utils.FormatTimePtr(task.firstAgentReplyAt),
			RecommendationReason: task.recommendation,
			DispatchMode:         task.dispatchMode,
			DispatchModeLabel:    enums.GetAgentTeamDispatchModeLabel(task.dispatchMode),
			WorkloadWeight:       task.workloadWeight,
			Priority:             task.priority,
		}
		if task.assignment != nil {
			resp.DispatchMode = task.assignment.DispatchMode
			resp.DispatchModeLabel = enums.GetAgentTeamDispatchModeLabel(task.assignment.DispatchMode)
			resp.WorkloadWeight = task.assignment.WorkloadWeight
			resp.AssignmentReason = utils.RepairMojibakeText(task.assignment.Reason)
		}
		if item.CurrentAssigneeID > 0 {
			resp.CurrentAssigneeName = displayUser(item.CurrentAssigneeID)
		}
		if task.route != nil {
			resp.RouteStatus = task.route.RouteStatus
			resp.RouteStatusLabel = enums.GetConversationRouteStatusLabel(task.route.RouteStatus)
			resp.StoreID = task.route.StoreID
			resp.WxWorkInstanceID = task.route.WxWorkInstanceID
			resp.HandoffReason = utils.RepairMojibakeText(task.route.HandoffReason)
		}
		if resp.StoreID > 0 {
			if store, exists := storeByID[resp.StoreID]; exists {
				resp.StoreName = utils.RepairMojibakeText(store.Name)
			}
		}
		if resp.WxWorkInstanceID > 0 {
			if instance, exists := instanceByID[resp.WxWorkInstanceID]; exists {
				resp.WxWorkEmployeeName = utils.RepairMojibakeText(instance.EmployeeName)
				resp.WxWorkEmployeeUserID = instance.EmployeeUserID
				if resp.StoreID == 0 {
					resp.StoreID = instance.StoreID
				}
				if resp.StoreName == "" && instance.StoreID > 0 {
					if store, exists := storeByID[instance.StoreID]; exists {
						resp.StoreName = utils.RepairMojibakeText(store.Name)
					}
				}
			}
		}
		if task.recommendedProfile != nil {
			resp.RecommendedAssigneeID = task.recommendedProfile.UserID
			resp.RecommendedAssigneeName = utils.RepairMojibakeText(strings.TrimSpace(task.recommendedProfile.DisplayName))
			if resp.RecommendedAssigneeName == "" {
				resp.RecommendedAssigneeName = displayUser(task.recommendedProfile.UserID)
			}
		}
		ret = append(ret, resp)
	}
	return ret
}

type dispatchTaskSLA struct {
	slaType          string
	status           string
	startedAt        *time.Time
	deadlineAt       *time.Time
	remainingSeconds int64
}

func (s *conversationDispatchWorkbenchService) resolveTaskStatus(conversation *models.Conversation, assignedAt *time.Time, firstReplyAt *time.Time, sla dispatchTaskSLA) (string, string) {
	if conversation == nil {
		return ConversationDispatchStatusClosed, "未知"
	}
	if conversation.Status == enums.IMConversationStatusClosed {
		return ConversationDispatchStatusClosed, "已完成"
	}
	if sla.status == dispatchSLAStatusOverdue {
		return ConversationDispatchStatusTimeout, "已超时"
	}
	if sla.status == dispatchSLAStatusWarning {
		return ConversationDispatchStatusWarning, "即将超时"
	}
	if conversation.Status == enums.IMConversationStatusPending || conversation.CurrentAssigneeID == 0 {
		return ConversationDispatchStatusPending, "待派发"
	}
	if assignedAt != nil && firstReplyAt == nil {
		return ConversationDispatchStatusAssigned, "已派发待首响"
	}
	return ConversationDispatchStatusProcessing, "处理中"
}

func (s *conversationDispatchWorkbenchService) resolveTaskSLA(conversation *models.Conversation, route *models.ConversationRouteState, assignedAt *time.Time, firstReplyAt *time.Time, now time.Time) dispatchTaskSLA {
	if conversation == nil {
		return dispatchTaskSLA{}
	}
	return s.resolveTaskSLAWithPolicy(conversation, route, assignedAt, firstReplyAt, now, ServiceAnalyticsService.GetPolicy(conversation.TenantID))
}

func (s *conversationDispatchWorkbenchService) resolveTaskSLAWithPolicy(conversation *models.Conversation, route *models.ConversationRouteState, assignedAt *time.Time, firstReplyAt *time.Time, now time.Time, policy models.ServiceAnalyticsPolicy) dispatchTaskSLA {
	if conversation == nil {
		return dispatchTaskSLA{}
	}
	targetSeconds := 0
	ret := dispatchTaskSLA{status: dispatchSLAStatusNormal}
	if conversation.Status == enums.IMConversationStatusPending || conversation.CurrentAssigneeID == 0 {
		ret.slaType = dispatchSLATypeQueue
		targetSeconds = policy.QueueTargetSeconds
		ret.startedAt = pendingTaskStartedAt(conversation, route)
	} else if assignedAt != nil && firstReplyAt == nil {
		ret.slaType = dispatchSLATypeFirstResponse
		targetSeconds = policy.FirstResponseTargetSeconds
		ret.startedAt = assignedAt
	} else {
		return dispatchTaskSLA{}
	}
	if targetSeconds <= 0 {
		if ret.slaType == dispatchSLATypeQueue {
			targetSeconds = 60
		} else {
			targetSeconds = 180
		}
	}
	if ret.startedAt == nil || ret.startedAt.IsZero() {
		return dispatchTaskSLA{}
	}
	deadline := ret.startedAt.Add(time.Duration(targetSeconds) * time.Second)
	ret.deadlineAt = &deadline
	ret.remainingSeconds = int64(deadline.Sub(now).Seconds())
	elapsed := now.Sub(*ret.startedAt)
	switch {
	case !now.Before(deadline):
		ret.status = dispatchSLAStatusOverdue
	case elapsed > 0 && elapsed*100 >= time.Duration(targetSeconds)*time.Second*80:
		ret.status = dispatchSLAStatusWarning
	}
	return ret
}

func pendingTaskStartedAt(conversation *models.Conversation, route *models.ConversationRouteState) *time.Time {
	if conversation == nil {
		return nil
	}
	if route != nil && route.LastManualHandoffAt != nil {
		return route.LastManualHandoffAt
	}
	if conversation.HandoffAt != nil {
		return conversation.HandoffAt
	}
	if !conversation.CreatedAt.IsZero() {
		return &conversation.CreatedAt
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

func (s *conversationDispatchWorkbenchService) resolveTaskTeamID(conversation *models.Conversation, route *models.ConversationRouteState) int64 {
	if conversation == nil {
		return 0
	}
	teamIDs := ConversationDispatchService.resolveDispatchTeamIDs(conversation, route)
	if len(teamIDs) == 1 {
		return teamIDs[0]
	}
	return 0
}

func (s *conversationDispatchWorkbenchService) canManageTeam(operator *dto.AuthPrincipal, teamID int64) bool {
	return AgentTeamScopeService.CanManageTeam(operator, teamID)
}

func (s *conversationDispatchWorkbenchService) listVisibleAgentProfiles(teamID int64, operator *dto.AuthPrincipal) []models.AgentProfile {
	cnd := sqls.NewCnd().Where("status <> ?", enums.StatusDeleted)
	manageableTeamIDs := AgentTeamScopeService.ManageableTeamIDs(operator)
	if teamID > 0 {
		if !slices.Contains(manageableTeamIDs, teamID) {
			return []models.AgentProfile{}
		}
		cnd.Eq("team_id", teamID)
	} else {
		if len(manageableTeamIDs) == 0 {
			return []models.AgentProfile{}
		}
		cnd.In("team_id", manageableTeamIDs)
	}
	return AgentProfileService.FindInTenant(cnd.Asc("team_id").Desc("priority_level").Asc("id"), operator)
}

func (s *conversationDispatchWorkbenchService) buildAgentLoads(profiles []models.AgentProfile, operator *dto.AuthPrincipal) ([]dispatchWorkbenchAgentLoad, error) {
	tenantID := AgentTeamScopeService.ActiveTenantID(operator)
	now := time.Now()
	teamIDs := make([]int64, 0, len(profiles))
	userIDs := make([]int64, 0, len(profiles))
	for _, profile := range profiles {
		if profile.TeamID > 0 && !slices.Contains(teamIDs, profile.TeamID) {
			teamIDs = append(teamIDs, profile.TeamID)
		}
		if profile.UserID > 0 {
			userIDs = append(userIDs, profile.UserID)
		}
	}
	userIDs = uniquePositiveInt64s(userIDs)
	schedules := ConversationDispatchService.findActiveScheduleDetails(teamIDs, tenantID, now)
	dispatchLoads, err := ConversationDispatchService.buildDispatchLoadMap(profiles, schedules, tenantID)
	if err != nil {
		return nil, err
	}
	enabledUserSet := make(map[int64]struct{}, len(userIDs))
	for _, user := range UserService.Find(sqls.NewCnd().Eq("tenant_id", tenantID).In("id", userIDs).Eq("status", enums.StatusOk)) {
		if user.DeletedAt == nil {
			enabledUserSet[user.ID] = struct{}{}
		}
	}
	permittedUserIDs, err := repositories.PermissionRepository.FindUserIDsWithAllCodes(sqls.DB(), userIDs, []string{
		constants.PermissionConversationView.Code,
		constants.PermissionConversationSend.Code,
	})
	if err != nil {
		return nil, err
	}
	permittedUserSet := int64Set(permittedUserIDs)
	presenceByUserID := ConversationDispatchService.loadDispatchPresenceMapDB(sqls.DB(), tenantID, userIDs, now)
	squadIDs := activeScheduleSquadIDs(schedules)
	membersBySquad, teamBySquad := AgentTeamSquadService.ActiveMemberProfileSet(squadIDs, tenantID)
	ret := make([]dispatchWorkbenchAgentLoad, 0, len(profiles))
	for _, profile := range profiles {
		presence := presenceByUserID[profile.UserID]
		load := dispatchWorkbenchAgentLoad{
			profile:             profile,
			activeCount:         dispatchLoads[profile.UserID].activeCount,
			weightedOpenLoad:    dispatchLoads[profile.UserID].weightedOpenLoad,
			shiftWorkloadWeight: dispatchLoads[profile.UserID].shiftWorkloadWeight,
			presenceStatus:      presence.Status,
			presenceLastSeenAt:  presence.LastSeenAt,
		}
		if team := AgentTeamService.GetInTenant(profile.TeamID, operator); team != nil {
			load.teamName = utils.RepairMojibakeText(team.Name)
			load.manuallyAssignable = team.Status == enums.StatusOk
		}
		if user := UserService.GetInTenant(profile.UserID, AgentTeamScopeService.ActiveTenantID(operator)); user != nil {
			load.username = user.Username
			load.nickname = user.Nickname
		}
		load.pendingFirstReply = dispatchLoads[profile.UserID].pendingFirstReply
		load.processingCount = load.activeCount - load.pendingFirstReply
		load.pendingReplyCount = dispatchLoads[profile.UserID].pendingReplyCount
		load.normalizedLoad = normalizedDispatchPressure(dispatchLoads[profile.UserID], profile.MaxConcurrentCount)
		selection, scheduled := schedules[profile.TeamID]
		_, userEnabled := enabledUserSet[profile.UserID]
		_, permitted := permittedUserSet[profile.UserID]
		load.manuallyAssignable = load.manuallyAssignable && profile.Status == enums.StatusOk && userEnabled && permitted
		onShift := scheduled && profileMatchesActiveScheduleSnapshot(&profile, selection, membersBySquad, teamBySquad)
		load.available, load.availabilityCode, load.availabilityReason = dispatchProfileAvailability(profile, load.activeCount, userEnabled, permitted, scheduled, onShift, presence, now)
		ret = append(ret, load)
	}
	return ret, nil
}

func (s *conversationDispatchWorkbenchService) buildAgentLoadResponse(load dispatchWorkbenchAgentLoad) response.ConversationDispatchAgentLoadResponse {
	return response.ConversationDispatchAgentLoadResponse{
		UserID:              load.profile.UserID,
		ProfileID:           load.profile.ID,
		TeamID:              load.profile.TeamID,
		TeamName:            load.teamName,
		Username:            load.username,
		Nickname:            utils.RepairMojibakeText(load.nickname),
		DisplayName:         utils.RepairMojibakeText(load.profile.DisplayName),
		MaxConcurrentCount:  load.profile.MaxConcurrentCount,
		ActiveCount:         load.activeCount,
		PendingFirstReply:   load.pendingFirstReply,
		PendingReplyCount:   load.pendingReplyCount,
		ProcessingCount:     load.processingCount,
		AutoAssignEnabled:   load.profile.AutoAssignEnabled,
		Available:           load.available,
		ManuallyAssignable:  load.manuallyAssignable,
		AvailabilityCode:    load.availabilityCode,
		AvailabilityReason:  load.availabilityReason,
		PresenceStatus:      load.presenceStatus,
		PresenceLastSeenAt:  utils.FormatTime(load.presenceLastSeenAt),
		PriorityLevel:       load.profile.PriorityLevel,
		LastOnlineAt:        utils.FormatTimePtr(load.profile.LastOnlineAt),
		WeightedOpenLoad:    load.weightedOpenLoad,
		ShiftWorkloadWeight: load.shiftWorkloadWeight,
		NormalizedLoad:      load.normalizedLoad,
	}
}

func dispatchProfileAvailability(profile models.AgentProfile, activeCount int, userEnabled, permitted, scheduled, onShift bool, presence dispatchPresenceSnapshot, now time.Time) (bool, string, string) {
	switch {
	case profile.Status != enums.StatusOk:
		return false, dispatchAvailabilityProfileDisabled, "客服档案已停用"
	case !profile.AutoAssignEnabled:
		return false, dispatchAvailabilityAutoAssignDisabled, "未开启自动接单"
	case profile.MaxConcurrentCount <= 0:
		return false, dispatchAvailabilityCapacityMissing, "未配置有效并发上限"
	case !userEnabled:
		return false, dispatchAvailabilityAccountDisabled, "客服账号已停用"
	case !permitted:
		return false, dispatchAvailabilityPermissionMissing, "缺少会话查看或回复权限"
	case !scheduled:
		return false, dispatchAvailabilityNoActiveSchedule, "客服组当前无有效班次"
	case !onShift:
		return false, dispatchAvailabilityOutOfShift, "不在当前值班小组或已被排除"
	case presence.LastSeenAt.IsZero() || now.Sub(presence.LastSeenAt) > dispatchPresenceFreshness:
		return false, dispatchAvailabilityOffline, "当前未在线"
	case presence.Status == enums.AgentPresenceStatusBreak:
		return false, dispatchAvailabilityBreak, "当前处于休息状态"
	case presence.Status == enums.AgentPresenceStatusBusy:
		return false, dispatchAvailabilityBusy, "当前处于忙碌状态"
	case !isDispatchPresenceEligible(presence, now):
		return false, dispatchAvailabilityOffline, "当前未在线"
	case activeCount >= profile.MaxConcurrentCount:
		return false, dispatchAvailabilityAtCapacity, "已达到最大并发接待数"
	default:
		return true, dispatchAvailabilityAvailable, "可自动接单"
	}
}

func (s *conversationDispatchWorkbenchService) pickRuleCandidates(teamID, tenantID int64, route *models.ConversationRouteState) ([]dispatchCandidate, error) {
	if route != nil && route.TenantID != tenantID {
		return nil, errorsx.InvalidParam("会话路由与接入公司不一致")
	}
	candidates, _, err := ConversationDispatchService.pickDispatchCandidates([]int64{teamID}, tenantID, route, time.Now())
	return candidates, err
}

func (s *conversationDispatchWorkbenchService) requireManageableTargetProfile(userID int64, conversation *models.Conversation, operator *dto.AuthPrincipal) (*models.AgentProfile, error) {
	if userID <= 0 {
		return nil, errorsx.InvalidParam("请选择目标客服")
	}
	profile := AgentProfileService.GetEnabledForAssignment(sqls.DB(), conversation.TenantID, userID)
	if profile == nil {
		return nil, errorsx.InvalidParam("目标客服不存在或账号已停用")
	}
	ownerTeamID := conversation.CurrentTeamID
	if ownerTeamID <= 0 && conversation.CurrentAssigneeID > 0 {
		if currentProfile := repositories.AgentProfileRepository.Take(sqls.DB(), "tenant_id = ? AND user_id = ?", conversation.TenantID, conversation.CurrentAssigneeID); currentProfile != nil {
			ownerTeamID = currentProfile.TeamID
		}
	}
	if ownerTeamID > 0 && profile.TeamID != ownerTeamID {
		return nil, errorsx.Forbidden("目标客服不属于当前会话综合客服组")
	}
	if !s.canManageTeam(operator, profile.TeamID) {
		return nil, errorsx.Forbidden("无权派发到该客服组")
	}
	team := repositories.AgentTeamRepository.GetInTenant(sqls.DB(), profile.TeamID, conversation.TenantID)
	if team == nil || team.Status != enums.StatusOk {
		return nil, errorsx.InvalidParam("目标客服所属客服组已停用")
	}
	permittedUserIDs, err := repositories.PermissionRepository.FindUserIDsWithAllCodes(sqls.DB(), []int64{profile.UserID}, []string{
		constants.PermissionConversationView.Code,
		constants.PermissionConversationSend.Code,
	})
	if err != nil {
		return nil, err
	}
	if len(permittedUserIDs) != 1 || permittedUserIDs[0] != profile.UserID {
		return nil, errorsx.Forbidden("目标客服缺少会话查看或回复权限")
	}
	route := repositories.ConversationRouteStateRepository.TakeByConversationInTenant(sqls.DB(), conversation.ID, conversation.TenantID)
	if route != nil && !AgentProfileService.ProfileCanServeRoute(profile, route) {
		return nil, errorsx.Forbidden("目标客服不在该会话门店或员工号服务范围内")
	}
	return profile, nil
}

func (s *conversationDispatchWorkbenchService) assignToProfile(conversation *models.Conversation, profile models.AgentProfile, squadID int64, reason string, operator *dto.AuthPrincipal, assignType enums.IMAssignmentType, dispatchMode enums.AgentTeamDispatchMode, publish bool) error {
	if conversation == nil {
		return errorsx.InvalidParam("会话不存在")
	}
	if operator == nil {
		return errorsx.Unauthorized("未登录或登录已过期")
	}
	if profile.TenantID != conversation.TenantID || operator.ActiveTenantID != conversation.TenantID {
		return errorsx.Forbidden("会话、目标客服与当前接入公司不一致")
	}
	reason = strings.TrimSpace(reason)
	now := time.Now()
	var assignedEvent events.ConversationAssignedEvent
	err := sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		current, err := repositories.ConversationRepository.GetForUpdateInTenant(ctx.Tx, conversation.ID, conversation.TenantID)
		if err != nil {
			return err
		}
		if current == nil {
			return errorsx.InvalidParam("会话不存在")
		}
		if current.Status != enums.IMConversationStatusPending && current.Status != enums.IMConversationStatusActive {
			return errorsx.InvalidParam("当前会话状态不允许派发")
		}
		if current.Status != conversation.Status || current.CurrentAssigneeID != conversation.CurrentAssigneeID {
			return errorsx.InvalidParam("当前会话指派关系已变化，请刷新后重试")
		}
		if current.Status == enums.IMConversationStatusPending && current.CurrentAssigneeID > 0 {
			return errorsx.InvalidParam("当前会话已分配客服")
		}
		lockedProfile, err := s.validateAssignmentTargetDB(ctx.Tx, current, profile, squadID, dispatchMode, operator, now)
		if err != nil {
			return err
		}
		if err := ConversationAssignmentService.FinishActiveAssignments(ctx, current.ID, now); err != nil {
			return err
		}
		workloadWeight := normalizedWorkloadWeight(current)
		priority := normalizedConversationPriority(current)
		if dispatchMode == enums.AgentTeamDispatchModeRule {
			route := repositories.ConversationRouteStateRepository.TakeByConversationInTenant(ctx.Tx, current.ID, current.TenantID)
			workloadWeight, priority = ConversationDispatchService.ruleDispatchAssessmentAt(current, route, now)
		}
		if err := ConversationAssignmentService.CreateAssignmentWithOptions(ctx, current.ID, current.CurrentAssigneeID, lockedProfile.UserID, assignType, reason, operator, now, ConversationAssignmentOptions{
			SquadID:        squadID,
			DispatchMode:   dispatchMode,
			WorkloadWeight: workloadWeight,
		}); err != nil {
			return err
		}
		if err := repositories.ConversationRepository.UpdatesInTenant(ctx.Tx, current.ID, current.TenantID, map[string]any{
			"current_assignee_id": lockedProfile.UserID,
			"current_team_id":     lockedProfile.TeamID,
			"status":              enums.IMConversationStatusActive,
			"priority":            priority,
			"dispatch_weight":     workloadWeight,
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
			"toAssigneeId":   lockedProfile.UserID,
			"toTeamId":       lockedProfile.TeamID,
			"reason":         reason,
			"dispatchMode":   dispatchMode,
			"workloadWeight": workloadWeight,
			"priority":       priority,
		})); err != nil {
			return err
		}
		if _, err := ConversationRouteService.enterHQAgentDeskServingWithDB(ctx.Tx, current.ID, "客服组派单:"+reason, now); err != nil {
			return err
		}
		assignedEvent = events.ConversationAssignedEvent{
			ConversationID: current.ID,
			FromUserID:     current.CurrentAssigneeID,
			ToUserID:       lockedProfile.UserID,
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

func (s *conversationDispatchWorkbenchService) validateAssignmentTargetDB(db *gorm.DB, conversation *models.Conversation, expected models.AgentProfile, squadID int64, dispatchMode enums.AgentTeamDispatchMode, operator *dto.AuthPrincipal, now time.Time) (*models.AgentProfile, error) {
	if db == nil || conversation == nil || operator == nil {
		return nil, errorsx.InvalidParam("派单上下文无效")
	}
	profile, err := repositories.AgentProfileRepository.GetForUpdateInTenant(db, expected.ID, conversation.TenantID)
	if err != nil {
		return nil, err
	}
	if profile == nil || profile.UserID != expected.UserID || profile.Status != enums.StatusOk {
		return nil, errorsx.InvalidParam("目标客服状态已变化")
	}
	user := repositories.UserRepository.GetInTenant(db, profile.UserID, conversation.TenantID)
	if user == nil || user.Status != enums.StatusOk || user.DeletedAt != nil {
		return nil, errorsx.InvalidParam("目标客服账号已停用")
	}
	if !s.canManageTeam(operator, profile.TeamID) {
		return nil, errorsx.Forbidden("无权派发到该客服组")
	}
	team, err := repositories.AgentTeamRepository.GetForUpdateInTenant(db, profile.TeamID, conversation.TenantID)
	if err != nil {
		return nil, err
	}
	if team == nil || team.Status != enums.StatusOk {
		return nil, errorsx.InvalidParam("目标客服所属客服组已停用")
	}
	ownerTeamID := conversation.CurrentTeamID
	if ownerTeamID <= 0 && conversation.CurrentAssigneeID > 0 {
		if currentProfile := repositories.AgentProfileRepository.Take(db, "tenant_id = ? AND user_id = ?", conversation.TenantID, conversation.CurrentAssigneeID); currentProfile != nil {
			ownerTeamID = currentProfile.TeamID
		}
	}
	if ownerTeamID > 0 && profile.TeamID != ownerTeamID {
		return nil, errorsx.Forbidden("目标客服不属于当前会话综合客服组")
	}
	permittedUserIDs, err := repositories.PermissionRepository.FindUserIDsWithAllCodes(db, []int64{profile.UserID}, []string{
		constants.PermissionConversationView.Code,
		constants.PermissionConversationSend.Code,
	})
	if err != nil {
		return nil, err
	}
	if len(permittedUserIDs) != 1 || permittedUserIDs[0] != profile.UserID {
		return nil, errorsx.Forbidden("目标客服缺少会话查看或回复权限")
	}
	route := repositories.ConversationRouteStateRepository.TakeByConversationInTenant(db, conversation.ID, conversation.TenantID)
	if route != nil && (!teamCanServeRoute(team, route) || !AgentProfileService.ProfileCanServeRoute(profile, route)) {
		return nil, errorsx.Forbidden("目标客服不在该会话门店或员工号服务范围内")
	}
	if dispatchMode != enums.AgentTeamDispatchModeRule {
		return profile, nil
	}
	if normalizedDispatchMode(team.DispatchMode) != enums.AgentTeamDispatchModeRule || !profile.AutoAssignEnabled || profile.MaxConcurrentCount <= 0 {
		return nil, errorsx.InvalidParam("目标客服已不符合规则派单条件")
	}
	activeSchedules := ConversationDispatchService.findActiveScheduleDetailsDB(db, []int64{profile.TeamID}, conversation.TenantID, now)
	selection, scheduled := activeSchedules[profile.TeamID]
	matchedWindow, matched := matchingActiveScheduleDB(db, profile, selection)
	if !scheduled || !matched || matchedWindow.SquadID != squadID {
		return nil, errorsx.InvalidParam("目标客服已不在当前值班范围")
	}
	presence := ConversationDispatchService.loadDispatchPresenceMapDB(db, conversation.TenantID, []int64{profile.UserID}, now)[profile.UserID]
	if !isDispatchPresenceEligible(presence, now) {
		return nil, errorsx.InvalidParam("目标客服当前不可自动接单")
	}
	activeCounts, err := ConversationDispatchService.findActiveConversationCountMapDB(db, []int64{profile.UserID}, conversation.TenantID)
	if err != nil {
		return nil, err
	}
	if activeCounts[profile.UserID] >= profile.MaxConcurrentCount {
		return nil, errorsx.InvalidParam("目标客服已达到最大并发接待数")
	}
	return profile, nil
}
