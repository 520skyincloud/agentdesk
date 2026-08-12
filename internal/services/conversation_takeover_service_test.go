package services

import (
	"strings"
	"testing"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
)

func TestConversationTakeoverRequestIsIdempotentWithoutAssignmentOrOutput(t *testing.T) {
	fixture := setupConversationSupervisorTakeoverFixture(t)
	conversation := fixture.createPendingConversation(t, fixture.teamA.ID, enums.ConversationRouteStatusHQAgentDeskPending, true)
	requester := fixture.agentPrincipal(fixture.offlineAgent)

	messagesBefore := countConversationRows[models.Message](t, fixture, conversation.ID)
	outboxesBefore := countConversationRows[models.ChannelMessageOutbox](t, fixture, conversation.ID)
	first, err := ConversationTakeoverService.Request(request.RequestConversationTakeoverRequest{
		ConversationID: conversation.ID,
		Reason:         "需要继续处理客户问题",
	}, requester)
	if err != nil {
		t.Fatalf("request takeover: %v", err)
	}
	second, err := ConversationTakeoverService.Request(request.RequestConversationTakeoverRequest{
		ConversationID: conversation.ID,
		Reason:         "重复提交应保持幂等",
	}, requester)
	if err != nil {
		t.Fatalf("repeat takeover request: %v", err)
	}
	if first == nil || second == nil || first.ID != second.ID {
		t.Fatalf("takeover request is not idempotent: first=%+v second=%+v", first, second)
	}

	current := ConversationService.GetByTenantID(conversation.ID, fixture.tenantID)
	if current == nil || current.Status != enums.IMConversationStatusPending || current.CurrentAssigneeID != 0 {
		t.Fatalf("request must not change assignment: %+v", current)
	}
	if assignments := countConversationRows[models.ConversationAssignment](t, fixture, conversation.ID); assignments != 0 {
		t.Fatalf("request created assignments=%d, want 0", assignments)
	}
	if messagesAfter := countConversationRows[models.Message](t, fixture, conversation.ID); messagesAfter != messagesBefore {
		t.Fatalf("request changed message count %d -> %d", messagesBefore, messagesAfter)
	}
	if outboxesAfter := countConversationRows[models.ChannelMessageOutbox](t, fixture, conversation.ID); outboxesAfter != outboxesBefore {
		t.Fatalf("request changed outbox count %d -> %d", outboxesBefore, outboxesAfter)
	}
	state := ConversationTakeoverService.ResolveState(current, requester)
	if !state.PendingForMe || state.CanReply || state.CanRequest {
		t.Fatalf("unexpected requester state: %+v", state)
	}
	reviewerState := ConversationTakeoverService.ResolveState(current, fixture.leaderA)
	if !reviewerState.PendingForAnother || !reviewerState.CanReview {
		t.Fatalf("unexpected reviewer state: %+v", reviewerState)
	}
}

func TestConversationTakeoverRequestAcceptsAnyValidOperationalRole(t *testing.T) {
	fixture := setupConversationSupervisorTakeoverFixture(t)
	conversation := fixture.createPendingConversation(t, fixture.teamA.ID, enums.ConversationRouteStatusHQAgentDeskPending, true)
	requester := *fixture.agentWithoutProfile
	requester.Roles = []string{constants.RoleCodeCsUser, constants.RoleCodeStoreStaff}
	requester.Permissions = []string{
		constants.PermissionConversationView.Code,
		constants.PermissionConversationSend.Code,
	}
	activeUserID := requester.UserID
	binding := &models.StoreStaffBinding{
		TenantID:     fixture.tenantID,
		UserID:       requester.UserID,
		ActiveUserID: &activeUserID,
		AgentTeamID:  fixture.teamA.ID,
		StoreID:      88,
		Status:       enums.StatusOk,
	}
	if err := fixture.db.Create(binding).Error; err != nil {
		t.Fatalf("create mixed-role store binding: %v", err)
	}
	if err := fixture.db.Model(&models.Conversation{}).
		Where("tenant_id = ? AND id = ?", fixture.tenantID, conversation.ID).
		Updates(map[string]any{"store_id": binding.StoreID, "store_staff_binding_id": binding.ID}).Error; err != nil {
		t.Fatalf("scope mixed-role conversation: %v", err)
	}
	if err := fixture.db.Model(&models.ConversationRouteState{}).
		Where("tenant_id = ? AND conversation_id = ?", fixture.tenantID, conversation.ID).
		Updates(map[string]any{"store_id": binding.StoreID, "store_staff_binding_id": binding.ID}).Error; err != nil {
		t.Fatalf("scope mixed-role route: %v", err)
	}

	item, err := ConversationTakeoverService.Request(request.RequestConversationTakeoverRequest{
		ConversationID: conversation.ID,
		Reason:         "通过门店员工身份申请接管",
	}, &requester)
	if err != nil {
		t.Fatalf("mixed-role takeover request: %v", err)
	}
	if item == nil || item.RequesterUserID != requester.UserID || item.TeamID != fixture.teamA.ID {
		t.Fatalf("unexpected mixed-role takeover request: %+v", item)
	}
}

func TestConversationTakeoverReviewApprovalAndResumeAIAuthorization(t *testing.T) {
	fixture := setupConversationSupervisorTakeoverFixture(t)
	conversation := fixture.createPendingConversation(t, fixture.teamA.ID, enums.ConversationRouteStatusHQAgentDeskPending, true)
	requester := fixture.agentPrincipal(fixture.offlineAgent)
	item := fixture.requestTakeover(t, conversation.ID, requester)

	if err := ConversationTakeoverService.Review(request.ReviewConversationTakeoverRequest{
		RequestID: item.ID,
		Approved:  true,
		Remark:    "同意接管",
	}, fixture.leaderA); err != nil {
		t.Fatalf("approve takeover request: %v", err)
	}
	stored := repositories.ConversationTakeoverRequestRepository.FindOne(fixture.db, sqls.NewCnd().Eq("id", item.ID))
	if stored == nil || stored.Status != enums.ConversationTakeoverRequestStatusApproved || stored.ActiveKey != nil {
		t.Fatalf("unexpected approved request: %+v", stored)
	}
	current := ConversationService.GetByTenantID(conversation.ID, fixture.tenantID)
	if current == nil || current.Status != enums.IMConversationStatusActive || current.CurrentAssigneeID != requester.UserID {
		t.Fatalf("unexpected approved conversation: %+v", current)
	}
	route := ConversationRouteService.GetByConversationIDInTenant(conversation.ID, fixture.tenantID)
	if route == nil || route.RouteStatus != enums.ConversationRouteStatusHQAgentDeskServing {
		t.Fatalf("unexpected approved route: %+v", route)
	}
	if err := ConversationTakeoverService.EnsureCanReply(conversation.ID, requester); err != nil {
		t.Fatalf("approved requester cannot reply: %v", err)
	}
	if err := ConversationTakeoverService.ResumeAI(conversation.ID, fixture.leaderA); err == nil || !strings.Contains(err.Error(), "当前接待人") {
		t.Fatalf("non-assignee unexpectedly resumed AI: %v", err)
	}
	if err := ConversationTakeoverService.ResumeAI(conversation.ID, requester); err != nil {
		t.Fatalf("current assignee resume AI: %v", err)
	}
	current = ConversationService.GetByTenantID(conversation.ID, fixture.tenantID)
	route = ConversationRouteService.GetByConversationIDInTenant(conversation.ID, fixture.tenantID)
	if current == nil || current.Status != enums.IMConversationStatusAIServing || current.CurrentAssigneeID != 0 || route == nil || route.RouteStatus != enums.ConversationRouteStatusAIServing {
		t.Fatalf("unexpected AI resumed state: conversation=%+v route=%+v", current, route)
	}
}

func TestConversationTakeoverReviewRejectsCrossTeamAndCanReject(t *testing.T) {
	fixture := setupConversationSupervisorTakeoverFixture(t)
	conversation := fixture.createPendingConversation(t, fixture.teamA.ID, enums.ConversationRouteStatusHQAgentDeskPending, true)
	requester := fixture.agentPrincipal(fixture.offlineAgent)
	item := fixture.requestTakeover(t, conversation.ID, requester)

	if err := ConversationTakeoverService.Review(request.ReviewConversationTakeoverRequest{
		RequestID: item.ID,
		Approved:  true,
	}, fixture.leaderB); err == nil || !strings.Contains(err.Error(), "自己负责客服组") {
		t.Fatalf("cross-team reviewer unexpectedly approved request: %v", err)
	}
	pending := repositories.ConversationTakeoverRequestRepository.FindOne(fixture.db, sqls.NewCnd().Eq("id", item.ID))
	if pending == nil || pending.Status != enums.ConversationTakeoverRequestStatusPending {
		t.Fatalf("cross-team review changed request: %+v", pending)
	}
	if err := ConversationTakeoverService.Review(request.ReviewConversationTakeoverRequest{
		RequestID: item.ID,
		Approved:  false,
		Remark:    "暂不接管",
	}, fixture.leaderA); err != nil {
		t.Fatalf("reject takeover request: %v", err)
	}
	rejected := repositories.ConversationTakeoverRequestRepository.FindOne(fixture.db, sqls.NewCnd().Eq("id", item.ID))
	if rejected == nil || rejected.Status != enums.ConversationTakeoverRequestStatusRejected || rejected.ActiveKey != nil {
		t.Fatalf("unexpected rejected request: %+v", rejected)
	}
	current := ConversationService.GetByTenantID(conversation.ID, fixture.tenantID)
	if current == nil || current.Status != enums.IMConversationStatusPending || current.CurrentAssigneeID != 0 {
		t.Fatalf("rejection changed assignment: %+v", current)
	}
}

func TestConversationTakeoverRequestCancelsWhenSessionChanges(t *testing.T) {
	fixture := setupConversationSupervisorTakeoverFixture(t)
	conversation := fixture.createPendingConversation(t, fixture.teamA.ID, enums.ConversationRouteStatusHQAgentDeskPending, true)
	requester := fixture.agentPrincipal(fixture.offlineAgent)
	item := fixture.requestTakeover(t, conversation.ID, requester)

	if err := fixture.db.Model(&models.ConversationRouteState{}).
		Where("tenant_id = ? AND conversation_id = ?", fixture.tenantID, conversation.ID).
		Update("session_no", 2).Error; err != nil {
		t.Fatalf("advance conversation session: %v", err)
	}
	err := ConversationTakeoverService.Review(request.ReviewConversationTakeoverRequest{
		RequestID: item.ID,
		Approved:  true,
	}, fixture.leaderA)
	if err == nil || !strings.Contains(err.Error(), "服务段已变化") {
		t.Fatalf("session change did not reject review: %v", err)
	}
	cancelled := repositories.ConversationTakeoverRequestRepository.FindOne(fixture.db, sqls.NewCnd().Eq("id", item.ID))
	if cancelled == nil || cancelled.Status != enums.ConversationTakeoverRequestStatusCancelled || cancelled.TerminalReason != "session_changed" || cancelled.ActiveKey != nil {
		t.Fatalf("unexpected cancelled request: %+v", cancelled)
	}
}

func TestConversationAssignmentAndTransferCancelPendingTakeoverRequest(t *testing.T) {
	fixture := setupConversationSupervisorTakeoverFixture(t)
	t.Run("assignment", func(t *testing.T) {
		conversation := fixture.createPendingConversation(t, fixture.teamA.ID, enums.ConversationRouteStatusHQAgentDeskPending, true)
		item := fixture.requestTakeover(t, conversation.ID, fixture.agentPrincipal(fixture.offlineAgent))
		if err := ConversationService.AssignConversation(request.AssignConversationRequest{
			ConversationID: conversation.ID,
			AssigneeID:     fixture.breakAgent.ID,
			Reason:         "主管派单",
		}, fixture.adminA); err != nil {
			t.Fatalf("assign conversation: %v", err)
		}
		fixture.assertTakeoverCancelled(t, item.ID)
	})

	t.Run("transfer", func(t *testing.T) {
		conversation := fixture.createPendingConversation(t, fixture.teamA.ID, enums.ConversationRouteStatusHQAgentDeskPending, true)
		if err := ConversationService.AssignConversation(request.AssignConversationRequest{
			ConversationID: conversation.ID,
			AssigneeID:     fixture.offlineAgent.ID,
			Reason:         "首次派单",
		}, fixture.adminA); err != nil {
			t.Fatalf("initial assignment: %v", err)
		}
		route := ConversationRouteService.GetByConversationIDInTenant(conversation.ID, fixture.tenantID)
		if route == nil {
			t.Fatal("assigned conversation route not found")
		}
		activeKey := "stale-transfer-request"
		item := &models.ConversationTakeoverRequest{
			TenantID: fixture.tenantID, ConversationID: conversation.ID, SessionNo: route.SessionNo,
			TeamID: fixture.teamA.ID, RequesterUserID: fixture.breakAgent.ID, RequesterName: fixture.breakAgent.Username,
			SourceAssigneeID: fixture.offlineAgent.ID, SourceRouteStatus: route.RouteStatus,
			Reason: "遗留申请", Status: enums.ConversationTakeoverRequestStatusPending, ActiveKey: &activeKey,
			AuditFields: models.AuditFields{CreatedAt: time.Now(), UpdatedAt: time.Now()},
		}
		if err := fixture.db.Create(item).Error; err != nil {
			t.Fatalf("create stale takeover request: %v", err)
		}
		if err := ConversationService.TransferConversation(conversation.ID, fixture.breakAgent.ID, "主管转派", fixture.adminA); err != nil {
			t.Fatalf("transfer conversation: %v", err)
		}
		fixture.assertTakeoverCancelled(t, item.ID)
	})
}

func TestConversationTakeoverAssignedConversationRequiresTransfer(t *testing.T) {
	fixture := setupConversationSupervisorTakeoverFixture(t)
	conversation := fixture.createPendingConversation(t, fixture.teamA.ID, enums.ConversationRouteStatusHQAgentDeskPending, true)
	if err := ConversationService.AssignConversation(request.AssignConversationRequest{
		ConversationID: conversation.ID,
		AssigneeID:     fixture.offlineAgent.ID,
		Reason:         "首次派单",
	}, fixture.adminA); err != nil {
		t.Fatalf("assign conversation: %v", err)
	}
	requester := fixture.agentPrincipal(fixture.breakAgent)
	current := ConversationService.GetByTenantID(conversation.ID, fixture.tenantID)
	state := ConversationTakeoverService.ResolveState(current, requester)
	if state.CanRequest || state.CanDirectTakeover {
		t.Fatalf("assigned conversation exposed takeover action: %+v", state)
	}
	if _, err := ConversationTakeoverService.Request(request.RequestConversationTakeoverRequest{
		ConversationID: conversation.ID,
	}, requester); err == nil || !strings.Contains(err.Error(), "转派流程") {
		t.Fatalf("assigned conversation unexpectedly accepted takeover request: %v", err)
	}
}

func TestConversationTakeoverHumanOnlyCannotResumeAI(t *testing.T) {
	fixture := setupConversationSupervisorTakeoverFixture(t)
	conversation := fixture.createPendingConversation(t, fixture.teamA.ID, enums.ConversationRouteStatusHQAgentDeskPending, true)
	if err := fixture.db.Model(&models.Conversation{}).
		Where("tenant_id = ? AND id = ?", fixture.tenantID, conversation.ID).
		Update("service_mode", enums.IMConversationServiceModeHumanOnly).Error; err != nil {
		t.Fatalf("set human-only mode: %v", err)
	}
	if err := ConversationTakeoverService.DirectTakeover(request.RequestConversationTakeoverRequest{
		ConversationID: conversation.ID,
		Reason:         "组长接管仅人工会话",
	}, fixture.leaderA); err != nil {
		t.Fatalf("direct takeover human-only conversation: %v", err)
	}
	current := ConversationService.GetByTenantID(conversation.ID, fixture.tenantID)
	state := ConversationTakeoverService.ResolveState(current, fixture.leaderA)
	if state.CanResumeAI {
		t.Fatalf("human-only conversation exposed resume AI action: %+v", state)
	}
	if err := ConversationTakeoverService.ResumeAI(conversation.ID, fixture.leaderA); err == nil || !strings.Contains(err.Error(), "仅人工") {
		t.Fatalf("human-only conversation unexpectedly resumed AI: %v", err)
	}
}

func TestConversationRoomInviteRequiresCurrentAssigneeAndMatchingScope(t *testing.T) {
	fixture := setupConversationSupervisorTakeoverFixture(t)
	conversation := fixture.createPendingConversation(t, fixture.teamA.ID, enums.ConversationRouteStatusHQAgentDeskPending, true)
	if err := ConversationTakeoverService.DirectTakeover(request.RequestConversationTakeoverRequest{
		ConversationID: conversation.ID,
		Reason:         "组长接管群聊",
	}, fixture.leaderA); err != nil {
		t.Fatalf("direct takeover room conversation: %v", err)
	}
	channel := &models.Channel{
		TenantID: fixture.tenantID, Name: "企微群聊", ChannelType: enums.ChannelTypeWxWorkProtocol,
		ChannelID: "takeover-room-channel", Status: enums.StatusOk,
	}
	if err := fixture.db.Create(channel).Error; err != nil {
		t.Fatalf("create room channel: %v", err)
	}
	instance := &models.WxWorkProtocolInstance{
		TenantID: fixture.tenantID, AgentTeamID: fixture.teamA.ID, Guid: "takeover-room-instance",
		ChannelID: channel.ID, Status: enums.StatusOk,
	}
	if err := fixture.db.Create(instance).Error; err != nil {
		t.Fatalf("create room instance: %v", err)
	}
	if err := fixture.db.Model(&models.ConversationRouteState{}).
		Where("tenant_id = ? AND conversation_id = ?", fixture.tenantID, conversation.ID).
		Update("wx_work_instance_id", instance.ID).Error; err != nil {
		t.Fatalf("bind room instance to route: %v", err)
	}
	mapping := &models.WxWorkKFConversation{
		TenantID: fixture.tenantID, ConversationID: conversation.ID, ChannelID: channel.ID,
		OpenKfID: "wx_protocol:takeover-room-instance:room", ExternalUserID: "R:room-101", Status: enums.StatusOk,
	}
	if err := fixture.db.Create(mapping).Error; err != nil {
		t.Fatalf("create room conversation mapping: %v", err)
	}

	if err := ConversationTakeoverService.EnsureCanInviteRoomMember(conversation.ID, instance.ID, "room-101", fixture.leaderA); err != nil {
		t.Fatalf("current assignee cannot invite room member: %v", err)
	}
	if err := ConversationTakeoverService.EnsureCanInviteRoomMember(conversation.ID, instance.ID, "room-202", fixture.leaderA); err == nil || !strings.Contains(err.Error(), "群ID") {
		t.Fatalf("mismatched room unexpectedly allowed: %v", err)
	}
	otherInstance := &models.WxWorkProtocolInstance{
		TenantID: fixture.tenantID, AgentTeamID: fixture.teamA.ID, Guid: "takeover-other-instance",
		ChannelID: channel.ID, Status: enums.StatusOk,
	}
	if err := fixture.db.Create(otherInstance).Error; err != nil {
		t.Fatalf("create other room instance: %v", err)
	}
	if err := ConversationTakeoverService.EnsureCanInviteRoomMember(conversation.ID, otherInstance.ID, "room-101", fixture.leaderA); err == nil || !strings.Contains(err.Error(), "绑定的企微员工号") {
		t.Fatalf("mismatched instance unexpectedly allowed: %v", err)
	}
	if err := ConversationTakeoverService.EnsureCanInviteRoomMember(conversation.ID, instance.ID, "room-101", fixture.leaderB); err == nil {
		t.Fatal("non-assignee unexpectedly allowed to invite room member")
	}
}

func (f conversationSupervisorTakeoverFixture) agentPrincipal(user models.User) *dto.AuthPrincipal {
	return &dto.AuthPrincipal{
		UserID: user.ID, TenantID: f.tenantID, ActiveTenantID: f.tenantID,
		Username: user.Username, Nickname: user.Nickname,
		Roles: []string{constants.RoleCodeCsUser},
		Permissions: []string{
			constants.PermissionConversationView.Code,
			constants.PermissionConversationSend.Code,
		},
	}
}

func (f conversationSupervisorTakeoverFixture) requestTakeover(t *testing.T, conversationID int64, requester *dto.AuthPrincipal) *models.ConversationTakeoverRequest {
	t.Helper()
	item, err := ConversationTakeoverService.Request(request.RequestConversationTakeoverRequest{
		ConversationID: conversationID,
		Reason:         "申请主动接管",
	}, requester)
	if err != nil {
		t.Fatalf("request takeover: %v", err)
	}
	if item == nil {
		t.Fatal("takeover request not returned")
	}
	return item
}

func (f conversationSupervisorTakeoverFixture) assertTakeoverCancelled(t *testing.T, requestID int64) {
	t.Helper()
	item := repositories.ConversationTakeoverRequestRepository.FindOne(f.db, sqls.NewCnd().Eq("id", requestID))
	if item == nil || item.Status != enums.ConversationTakeoverRequestStatusCancelled || item.ActiveKey != nil || item.TerminalReason != "conversation_assigned" {
		t.Fatalf("unexpected cancelled takeover request: %+v", item)
	}
}

func countConversationRows[T any](t *testing.T, fixture conversationSupervisorTakeoverFixture, conversationID int64) int64 {
	t.Helper()
	var count int64
	if err := fixture.db.Model(new(T)).Where("tenant_id = ? AND conversation_id = ?", fixture.tenantID, conversationID).Count(&count).Error; err != nil {
		t.Fatalf("count conversation rows: %v", err)
	}
	return count
}
