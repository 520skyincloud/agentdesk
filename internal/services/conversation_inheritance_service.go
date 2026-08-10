package services

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/dto/response"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/errorsx"
	"agent-desk/internal/pkg/utils"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

type storeConversationInheritanceTarget struct {
	Binding  *models.StoreStaffBinding
	Instance *models.WxWorkProtocolInstance
	Channel  *models.Channel
}

const (
	storeConversationInheritanceModeCreate       = "create_successor"
	storeConversationInheritanceModeLinkExisting = "link_existing"
)

type storeConversationInheritanceResult struct {
	ConversationID int64
	SessionNo      int
	Mode           string
}

type storeConversationInheritanceSnapshot struct {
	Response      *response.StoreConversationInheritancePreviewResponse
	Conversations map[int64]*models.Conversation
	Target        *storeConversationInheritanceTarget
}

func (s *conversationService) PreviewStoreConversationInheritance(req request.PreviewStoreConversationInheritanceRequest, operator *dto.AuthPrincipal) (*response.StoreConversationInheritancePreviewResponse, error) {
	if err := s.validateInheritancePreviewRequest(req, operator); err != nil {
		return nil, err
	}
	snapshot, err := s.buildStoreConversationInheritanceSnapshot(sqls.DB(), req, operator, false)
	if err != nil {
		return nil, err
	}
	return snapshot.Response, nil
}

func (s *conversationService) ListRelatedStoreConversations(conversation *models.Conversation, operator *dto.AuthPrincipal) []models.Conversation {
	if conversation == nil || operator == nil || conversation.TenantID <= 0 || conversation.StoreID <= 0 || conversation.CustomerID <= 0 {
		return []models.Conversation{}
	}
	cnd := sqls.NewCnd().
		Eq("tenant_id", conversation.TenantID).
		Eq("store_id", conversation.StoreID).
		Eq("customer_id", conversation.CustomerID).
		NotEq("id", conversation.ID).
		Desc("last_active_at").Desc("id").Limit(50)
	cnd = AgentTeamScopeService.ApplyConversationFilter(cnd, operator)
	return repositories.ConversationRepository.Find(sqls.DB(), cnd)
}

func (s *conversationService) ListConversationContinuityLinks(conversation *models.Conversation, operator *dto.AuthPrincipal) []models.ConversationContinuityLink {
	if conversation == nil || operator == nil || conversation.TenantID <= 0 || conversation.ID <= 0 {
		return []models.ConversationContinuityLink{}
	}
	items := repositories.ConversationContinuityLinkRepository.FindByConversation(sqls.DB(), conversation.TenantID, conversation.ID)
	result := make([]models.ConversationContinuityLink, 0, len(items))
	for i := range items {
		otherConversationID := items[i].PredecessorConversationID
		if otherConversationID == conversation.ID {
			otherConversationID = items[i].SuccessorConversationID
		}
		if AgentTeamScopeService.CanViewConversation(operator, otherConversationID) {
			result = append(result, items[i])
		}
	}
	return result
}

func (s *conversationService) BatchInheritStoreConversations(req request.BatchInheritStoreConversationsRequest, operator *dto.AuthPrincipal, requestID string) (*response.BatchStoreConversationInheritanceResponse, error) {
	if err := s.validateInheritancePreviewRequest(req.PreviewStoreConversationInheritanceRequest, operator); err != nil {
		return nil, err
	}
	reason := strings.TrimSpace(req.Reason)
	if reason == "" {
		return nil, errorsx.InvalidParam("请填写批量会话继承原因")
	}
	if len([]rune(reason)) > 255 {
		return nil, errorsx.InvalidParam("批量会话继承原因不能超过255个字符")
	}
	previewVersion := strings.TrimSpace(req.PreviewVersion)
	if len(previewVersion) != sha256.Size*2 {
		return nil, errorsx.InvalidParam("交接预览版本无效，请重新预览")
	}
	if _, err := hex.DecodeString(previewVersion); err != nil {
		return nil, errorsx.InvalidParam("交接预览版本无效，请重新预览")
	}
	conversationIDs := uniquePositiveInt64s(req.ConversationIDs)
	if len(conversationIDs) == 0 {
		return nil, errorsx.InvalidParam("请选择需要继承的会话")
	}
	slices.Sort(conversationIDs)

	tenantID := AgentTeamScopeService.ActiveTenantID(operator)
	result := &response.BatchStoreConversationInheritanceResponse{ConversationIDs: conversationIDs}
	affectedConversationIDs := make(map[int64]struct{}, len(conversationIDs)*2)
	err := sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		snapshot, err := s.buildStoreConversationInheritanceSnapshot(ctx.Tx, req.PreviewStoreConversationInheritanceRequest, operator, true)
		if err != nil {
			return err
		}
		if snapshot.Response.PreviewVersion != previewVersion {
			return errorsx.InvalidParam("交接范围或会话状态已变化，请重新预览后再执行")
		}
		eligible := make(map[int64]struct{}, snapshot.Response.EligibleCount)
		for _, item := range snapshot.Response.Items {
			if item.Eligible {
				eligible[item.ConversationID] = struct{}{}
			}
		}
		for _, conversationID := range conversationIDs {
			if _, ok := eligible[conversationID]; !ok {
				return errorsx.InvalidParam("所选会话包含冲突项，请重新预览")
			}
		}

		target := snapshot.Target
		if target == nil {
			return errorsx.InvalidParam("目标门店员工号继承范围不存在")
		}
		for _, conversationID := range conversationIDs {
			conversation := snapshot.Conversations[conversationID]
			if conversation == nil || conversation.StoreStaffBindingID != req.SourceStoreStaffBindingID {
				return errorsx.InvalidParam("所选会话归属已变化，请重新预览")
			}
			inheritance, err := s.inheritStoreConversationDB(ctx, conversation, target, reason, operator, requestID)
			if err != nil {
				return err
			}
			affectedConversationIDs[conversation.ID] = struct{}{}
			affectedConversationIDs[inheritance.ConversationID] = struct{}{}
			if inheritance.Mode == storeConversationInheritanceModeLinkExisting {
				result.LinkedCount++
			} else {
				result.CreatedCount++
			}
		}
		result.InheritedCount = int64(len(conversationIDs))
		return nil
	})
	if err != nil {
		return nil, err
	}
	for conversationID := range affectedConversationIDs {
		if conversation := repositories.ConversationRepository.GetInTenant(sqls.DB(), conversationID, tenantID); conversation != nil {
			WsService.PublishConversationChanged(conversation, enums.IMRealtimeEventConversationUpdated)
		}
	}
	return result, nil
}

func (s *conversationService) validateInheritancePreviewRequest(req request.PreviewStoreConversationInheritanceRequest, operator *dto.AuthPrincipal) error {
	if operator == nil || operator.UserID <= 0 {
		return errorsx.Unauthorized("未登录或登录已过期")
	}
	if req.SourceStoreStaffBindingID <= 0 || req.TargetStoreStaffBindingID <= 0 || req.TargetWxWorkInstanceID <= 0 {
		return errorsx.InvalidParam("请选择原门店员工号、目标门店员工号和目标企微实例")
	}
	if req.SourceStoreStaffBindingID == req.TargetStoreStaffBindingID {
		return errorsx.InvalidParam("原门店员工号与目标门店员工号不能相同")
	}
	if !AgentTeamScopeService.CanViewWxWorkInstance(operator, req.TargetWxWorkInstanceID) {
		return errorsx.Forbidden("目标企微实例超出当前数据范围")
	}
	return nil
}

func (s *conversationService) buildStoreConversationInheritanceSnapshot(db *gorm.DB, req request.PreviewStoreConversationInheritanceRequest, operator *dto.AuthPrincipal, forUpdate bool) (*storeConversationInheritanceSnapshot, error) {
	tenantID := AgentTeamScopeService.ActiveTenantID(operator)
	if db == nil || tenantID <= 0 {
		return nil, errorsx.Forbidden("请先进入需要管理会话的接入公司")
	}
	var sourceBinding *models.StoreStaffBinding
	var target *storeConversationInheritanceTarget
	if forUpdate {
		item, lockedTarget, err := s.lockStoreConversationInheritanceBindingsDB(
			db,
			tenantID,
			req.SourceStoreStaffBindingID,
			req.TargetStoreStaffBindingID,
			req.TargetWxWorkInstanceID,
		)
		if err != nil {
			return nil, err
		}
		sourceBinding = item
		target = lockedTarget
	} else {
		sourceBinding = repositories.StoreStaffBindingRepository.GetInTenant(db, req.SourceStoreStaffBindingID, tenantID)
	}
	if sourceBinding == nil || sourceBinding.Status == enums.StatusDeleted || sourceBinding.StoreID <= 0 {
		return nil, errorsx.InvalidParam("原门店员工号不存在或已删除")
	}
	scope := AgentTeamScopeService.Resolve(operator)
	if !scope.Unrestricted && !containsInt64(scope.StoreIDs, sourceBinding.StoreID) {
		return nil, errorsx.Forbidden("原门店员工号超出当前数据范围")
	}

	var err error
	if !forUpdate {
		target, err = s.getStoreConversationInheritanceTargetDB(db, tenantID, sourceBinding.StoreID, req.TargetStoreStaffBindingID, req.TargetWxWorkInstanceID)
	}
	if err != nil {
		return nil, err
	}
	store := repositories.StoreRepository.GetInTenant(db, sourceBinding.StoreID, tenantID)
	if store == nil || store.Status == enums.StatusDeleted {
		return nil, errorsx.InvalidParam("门店不存在或已删除")
	}
	conversations, err := repositories.ConversationRepository.FindByStoreStaffBindingInTenant(db, tenantID, sourceBinding.StoreID, sourceBinding.ID, forUpdate)
	if err != nil {
		return nil, err
	}

	result := &response.StoreConversationInheritancePreviewResponse{
		SourceStoreStaffBindingID: sourceBinding.ID,
		TargetStoreStaffBindingID: target.Binding.ID,
		TargetWxWorkInstanceID:    target.Instance.ID,
		StoreID:                   store.ID,
		StoreName:                 utils.RepairMojibakeText(store.Name),
		Items:                     make([]response.StoreConversationInheritancePreviewItemResponse, 0, len(conversations)),
	}
	hasher := sha256.New()
	_, _ = fmt.Fprintf(hasher, "v1|%d|%d|%d|%d|", tenantID, store.ID, sourceBinding.ID, target.Binding.ID)
	_, _ = fmt.Fprintf(hasher, "%d|%d|%d|%d|%d|", target.Instance.ID, target.Instance.UpdatedAt.UnixNano(), sourceBinding.UpdatedAt.UnixNano(), target.Binding.UpdatedAt.UnixNano(), target.Channel.UpdatedAt.UnixNano())
	conversationMap := make(map[int64]*models.Conversation, len(conversations))
	for i := range conversations {
		conversation := &conversations[i]
		conversationMap[conversation.ID] = conversation
		item := response.StoreConversationInheritancePreviewItemResponse{
			ConversationID: conversation.ID,
			CustomerID:     conversation.CustomerID,
			CustomerName:   utils.RepairMojibakeText(conversation.CustomerName),
			LastMessageAt:  utils.FormatTime(conversation.LastMessageAt),
			Eligible:       true,
			ResolutionMode: storeConversationInheritanceModeCreate,
		}
		threadKey := ""
		if conversation.ThreadKey != nil {
			threadKey = strings.TrimSpace(*conversation.ThreadKey)
		}
		if conversation.CustomerID <= 0 || conversation.StoreID != store.ID || conversation.StoreStaffBindingID != sourceBinding.ID || threadKey == "" {
			item.Eligible = false
			item.ConflictReason = "会话缺少完整门店线程范围"
		}
		route := repositories.ConversationRouteStateRepository.TakeByConversationInTenant(db, conversation.ID, tenantID)
		if route != nil {
			item.CurrentSessionNo = route.SessionNo
		}
		targetThreadKey := buildStoreConversationThreadKey(tenantID, store.ID, conversation.CustomerID, target.Binding.ID)
		conflict := repositories.ConversationRepository.FindOne(db, sqls.NewCnd().
			Eq("tenant_id", tenantID).
			Eq("thread_key", targetThreadKey))
		if conflict != nil && conflict.ID != conversation.ID {
			item.ResolutionMode = storeConversationInheritanceModeLinkExisting
			item.TargetConversationID = conflict.ID
			result.LinkedExistingCount++
			incoming, incomingErr := repositories.ConversationContinuityLinkRepository.GetForUpdateBySuccessor(db, tenantID, conflict.ID)
			if incomingErr != nil {
				return nil, incomingErr
			}
			if incoming != nil && incoming.PredecessorConversationID != conversation.ID {
				item.Eligible = false
				item.ConflictReason = "目标员工号会话已经继承了其他前序会话"
			}
		}
		if existingLink, linkErr := repositories.ConversationContinuityLinkRepository.GetForUpdateByPredecessor(db, tenantID, conversation.ID); linkErr != nil {
			return nil, linkErr
		} else if existingLink != nil {
			item.Eligible = false
			item.ConflictReason = "该会话已经安排过员工号继承"
		}
		if item.Eligible {
			result.EligibleCount++
		} else {
			result.ConflictCount++
		}
		result.Items = append(result.Items, item)
		routeSessionNo := 0
		routeInstanceID := int64(0)
		routeUpdatedAt := int64(0)
		if route != nil {
			routeSessionNo = route.SessionNo
			routeInstanceID = route.WxWorkInstanceID
			routeUpdatedAt = route.UpdatedAt.UnixNano()
		}
		conflictID := int64(0)
		if conflict != nil {
			conflictID = conflict.ID
		}
		_, _ = fmt.Fprintf(hasher, "%d|%d|%d|%s|%d|%d|%d|%d|%t|%s|",
			conversation.ID, conversation.CustomerID, conversation.StoreStaffBindingID, threadKey,
			conversation.UpdatedAt.UnixNano(), routeSessionNo, routeInstanceID, routeUpdatedAt,
			item.Eligible, item.ConflictReason)
		_, _ = fmt.Fprintf(hasher, "%d|", conflictID)
	}
	result.PreviewVersion = hex.EncodeToString(hasher.Sum(nil))
	return &storeConversationInheritanceSnapshot{Response: result, Conversations: conversationMap, Target: target}, nil
}

func (s *conversationService) getStoreConversationInheritanceTargetDB(db *gorm.DB, tenantID, storeID, bindingID, instanceID int64) (*storeConversationInheritanceTarget, error) {
	binding := repositories.StoreStaffBindingRepository.GetInTenant(db, bindingID, tenantID)
	instance := repositories.WxWorkProtocolInstanceRepository.GetInTenant(db, instanceID, tenantID)
	return s.validateStoreConversationInheritanceTargetDB(db, tenantID, storeID, binding, instance)
}

func (s *conversationService) lockStoreConversationInheritanceBindingsDB(
	db *gorm.DB,
	tenantID, sourceBindingID, targetBindingID, targetInstanceID int64,
) (*models.StoreStaffBinding, *storeConversationInheritanceTarget, error) {
	bindingIDs := []int64{sourceBindingID, targetBindingID}
	slices.Sort(bindingIDs)
	locked := make(map[int64]*models.StoreStaffBinding, len(bindingIDs))
	for _, bindingID := range bindingIDs {
		binding, err := repositories.StoreStaffBindingRepository.GetForUpdateInTenant(db, bindingID, tenantID)
		if err != nil {
			return nil, nil, err
		}
		locked[bindingID] = binding
	}
	sourceBinding := locked[sourceBindingID]
	if sourceBinding == nil || sourceBinding.Status == enums.StatusDeleted || sourceBinding.StoreID <= 0 {
		return nil, nil, errorsx.InvalidParam("原门店员工号不存在或已删除")
	}
	targetInstance := repositories.WxWorkProtocolInstanceRepository.GetForUpdateInTenant(db, targetInstanceID, tenantID)
	target, err := s.validateStoreConversationInheritanceTargetDB(
		db,
		tenantID,
		sourceBinding.StoreID,
		locked[targetBindingID],
		targetInstance,
	)
	if err != nil {
		return nil, nil, err
	}
	return sourceBinding, target, nil
}

func (s *conversationService) validateStoreConversationInheritanceTargetDB(db *gorm.DB, tenantID, storeID int64, binding *models.StoreStaffBinding, instance *models.WxWorkProtocolInstance) (*storeConversationInheritanceTarget, error) {
	if binding == nil || binding.Status != enums.StatusOk || binding.StoreID != storeID {
		return nil, errorsx.InvalidParam("目标门店员工号不存在、已停用或不属于当前门店")
	}
	if err := StoreStaffBindingService.validateBindingOwnerDB(db, binding); err != nil {
		return nil, err
	}
	if !isActivatedCurrentWxWorkProtocolInstance(instance) ||
		!strings.EqualFold(strings.TrimSpace(instance.HealthStatus), "online") {
		return nil, errorsx.InvalidParam("目标企微员工号实例不存在、已停用、已被替换或不在线")
	}
	if instance.StoreID != storeID || instance.StoreStaffBindingID != binding.ID {
		return nil, errorsx.InvalidParam("目标企微实例与门店员工号绑定不一致")
	}
	channel := repositories.ChannelRepository.GetInTenant(db, instance.ChannelID, tenantID)
	if channel == nil || channel.Status != enums.StatusOk || channel.ChannelType != enums.ChannelTypeWxWorkProtocol {
		return nil, errorsx.InvalidParam("目标企微实例缺少有效的企微员工号渠道")
	}
	return &storeConversationInheritanceTarget{Binding: binding, Instance: instance, Channel: channel}, nil
}

func (s *conversationService) inheritStoreConversationDB(ctx *sqls.TxContext, conversation *models.Conversation, target *storeConversationInheritanceTarget, reason string, operator *dto.AuthPrincipal, requestID string) (*storeConversationInheritanceResult, error) {
	if ctx == nil || ctx.Tx == nil || conversation == nil || target == nil || target.Binding == nil || target.Instance == nil || target.Channel == nil {
		return nil, errorsx.InvalidParam("会话继承上下文不完整")
	}
	if conversation.StoreID <= 0 || conversation.StoreStaffBindingID <= 0 || conversation.CustomerID <= 0 {
		return nil, errorsx.InvalidParam("该会话缺少完整门店线程范围，不能继承")
	}
	if conversation.StoreID != target.Binding.StoreID {
		return nil, errorsx.InvalidParam("目标门店员工号不属于当前会话门店")
	}
	if conversation.StoreStaffBindingID == target.Binding.ID {
		return nil, errorsx.InvalidParam("企微账号更换但门店员工号未变时会自动继承，无需人工安排")
	}
	targetThreadKey := buildStoreConversationThreadKey(conversation.TenantID, conversation.StoreID, conversation.CustomerID, target.Binding.ID)
	successor, err := repositories.ConversationRepository.GetForUpdateByThreadKeyInTenant(ctx.Tx, conversation.TenantID, targetThreadKey)
	if err != nil {
		return nil, err
	}
	mode := storeConversationInheritanceModeLinkExisting
	if successor == nil {
		successor, err = s.createStoreConversationSuccessorDB(ctx, conversation, target, targetThreadKey, operator, requestID)
		if err != nil {
			return nil, err
		}
		mode = storeConversationInheritanceModeCreate
	} else if successor.ID == conversation.ID {
		return nil, errorsx.InvalidParam("目标员工号会话与原会话冲突")
	}
	sessionNo, err := s.ensureStoreConversationSuccessorRouteDB(ctx, successor, target, operator)
	if err != nil {
		return nil, err
	}
	if err := s.touchStoreCustomerRelationDB(ctx.Tx, successor, successor.CustomerID, target.Instance.ID, time.Now(), operator); err != nil {
		return nil, err
	}
	result, err := s.linkExistingStoreConversationDB(ctx, conversation, successor, target, reason, operator, requestID, mode)
	if result != nil {
		result.SessionNo = sessionNo
	}
	return result, err
}

func (s *conversationService) createStoreConversationSuccessorDB(
	ctx *sqls.TxContext,
	predecessor *models.Conversation,
	target *storeConversationInheritanceTarget,
	threadKey string,
	operator *dto.AuthPrincipal,
	requestID string,
) (*models.Conversation, error) {
	if ctx == nil || ctx.Tx == nil || predecessor == nil || target == nil || target.Binding == nil || target.Channel == nil {
		return nil, errorsx.InvalidParam("创建接续会话上下文不完整")
	}
	participant, err := repositories.ConversationParticipantRepository.GetCustomerForUpdateInTenant(ctx.Tx, predecessor.TenantID, predecessor.ID)
	if err != nil {
		return nil, err
	}
	if participant == nil {
		return nil, errorsx.InvalidParam("原会话缺少有效客户参与人，不能安排继承")
	}
	now := time.Now()
	successor := &models.Conversation{
		TenantID: predecessor.TenantID, StoreID: predecessor.StoreID, StoreStaffBindingID: target.Binding.ID,
		ThreadKey: &threadKey, AIAgentID: predecessor.AIAgentID, ChannelID: target.Channel.ID,
		CustomerID: predecessor.CustomerID, CustomerName: predecessor.CustomerName,
		Status: s.resolveInitialStatus(predecessor.ServiceMode), ServiceMode: predecessor.ServiceMode,
		Priority: predecessor.Priority, DispatchWeight: predecessor.DispatchWeight,
		LastMessageAt: predecessor.LastMessageAt, LastActiveAt: now, LastMessageSummary: predecessor.LastMessageSummary,
		AuditFields: utils.BuildAuditFields(operator),
	}
	if successor.DispatchWeight <= 0 {
		successor.DispatchWeight = 1
	}
	if err := repositories.ConversationRepository.Create(ctx.Tx, successor); err != nil {
		return nil, err
	}
	joinedAt := now
	if err := repositories.ConversationParticipantRepository.Create(ctx.Tx, &models.ConversationParticipant{
		TenantID: successor.TenantID, ConversationID: successor.ID,
		ParticipantType: participant.ParticipantType, ParticipantID: participant.ParticipantID,
		ExternalParticipantID: participant.ExternalParticipantID, JoinedAt: &joinedAt,
		Status: enums.StatusOk, AuditFields: utils.BuildAuditFields(operator),
	}); err != nil {
		return nil, err
	}
	if err := ConversationEventLogService.CreateEventWithRequestID(
		ctx, successor.ID, requestID+":created", enums.IMEventTypeCreate,
		enums.IMSenderTypeAgent, operator.UserID, "授权人员创建接续会话",
		s.buildEventPayload(map[string]any{"mappingMode": "operator_confirmed_cross_namespace"}),
	); err != nil {
		return nil, err
	}
	return successor, nil
}

func (s *conversationService) ensureStoreConversationSuccessorRouteDB(
	ctx *sqls.TxContext,
	successor *models.Conversation,
	target *storeConversationInheritanceTarget,
	operator *dto.AuthPrincipal,
) (int, error) {
	if ctx == nil || ctx.Tx == nil || successor == nil || target == nil || target.Binding == nil || target.Instance == nil {
		return 0, errorsx.InvalidParam("接续会话路由上下文不完整")
	}
	if successor.TenantID != target.Binding.TenantID || successor.StoreID != target.Binding.StoreID || successor.StoreStaffBindingID != target.Binding.ID {
		return 0, errorsx.InvalidParam("接续会话与目标门店员工号范围不一致")
	}
	state, err := ConversationRouteService.ensureWithDB(ctx.Tx, successor.ID)
	if err != nil {
		return 0, err
	}
	state, err = repositories.ConversationRouteStateRepository.GetForUpdateByConversationInTenant(ctx.Tx, successor.ID, successor.TenantID)
	if err != nil {
		return 0, err
	}
	activeSession, err := repositories.ConversationChannelSessionRepository.FindActiveForUpdate(ctx.Tx, successor.TenantID, successor.ID)
	if err != nil {
		return 0, err
	}
	if state != nil && successor.Status != enums.IMConversationStatusClosed &&
		state.StoreID == successor.StoreID && state.StoreStaffBindingID == target.Binding.ID &&
		state.WxWorkInstanceID == target.Instance.ID && activeSession != nil &&
		activeSession.WxWorkInstanceID == target.Instance.ID {
		return activeSession.SessionNo, nil
	}
	now := time.Now()
	if err := ConversationAssignmentService.FinishActiveAssignments(ctx, successor.ID, now); err != nil {
		return 0, err
	}
	sessionNo, err := ConversationChannelSessionService.StartManualInheritanceDB(
		ctx, successor, target.Binding, target.Instance, now, operator.UserID, operator.Username,
	)
	if err != nil {
		return 0, err
	}
	if err := ConversationChannelSessionService.reopenConversationDB(ctx.Tx, successor, now); err != nil {
		return 0, err
	}
	if successor.ChannelID != target.Channel.ID {
		if err := repositories.ConversationRepository.UpdatesInTenant(ctx.Tx, successor.ID, successor.TenantID, map[string]any{
			"channel_id": target.Channel.ID, "updated_at": now,
			"update_user_id": operator.UserID, "update_user_name": operator.Username,
		}); err != nil {
			return 0, err
		}
		successor.ChannelID = target.Channel.ID
	}
	successor.Status = enums.IMConversationStatusAIServing
	successor.ClosedAt = nil
	successor.ClosedBy = 0
	successor.CloseReason = ""
	return sessionNo, nil
}

func (s *conversationService) linkExistingStoreConversationDB(
	ctx *sqls.TxContext,
	predecessor *models.Conversation,
	successor *models.Conversation,
	target *storeConversationInheritanceTarget,
	reason string,
	operator *dto.AuthPrincipal,
	requestID string,
	mode string,
) (*storeConversationInheritanceResult, error) {
	if ctx == nil || ctx.Tx == nil || predecessor == nil || successor == nil || target == nil || target.Binding == nil || target.Instance == nil {
		return nil, errorsx.InvalidParam("会话继承关系上下文不完整")
	}
	if predecessor.ID == successor.ID || predecessor.TenantID != successor.TenantID || predecessor.StoreID != successor.StoreID || predecessor.CustomerID != successor.CustomerID {
		return nil, errorsx.InvalidParam("前序与目标会话范围不一致")
	}
	if successor.StoreStaffBindingID != target.Binding.ID {
		return nil, errorsx.InvalidParam("目标会话员工号归属已变化，请刷新后重试")
	}
	if existing, err := repositories.ConversationContinuityLinkRepository.GetForUpdateByPredecessor(ctx.Tx, predecessor.TenantID, predecessor.ID); err != nil {
		return nil, err
	} else if existing != nil {
		if existing.SuccessorConversationID == successor.ID {
			if err := s.moveArrivalBindingsToSuccessorDB(ctx, predecessor, successor, target, mode, operator, requestID); err != nil {
				return nil, err
			}
			return &storeConversationInheritanceResult{ConversationID: successor.ID, Mode: mode}, nil
		}
		return nil, errorsx.InvalidParam("该会话已经继承到其他目标会话")
	}
	incoming, err := repositories.ConversationContinuityLinkRepository.GetForUpdateBySuccessor(ctx.Tx, predecessor.TenantID, successor.ID)
	if err != nil {
		return nil, errorsx.BusinessError(1, "目标会话继承关系异常，请联系管理员修复")
	}
	if incoming != nil {
		return nil, errorsx.InvalidParam("目标员工号会话已经继承了其他前序会话")
	}
	predecessorChain, err := repositories.ConversationContinuityLinkRepository.FindPredecessorChain(ctx.Tx, predecessor.TenantID, predecessor.ID, 50)
	if err != nil {
		return nil, errorsx.BusinessError(1, "原会话继承关系异常，请联系管理员修复")
	}
	for _, link := range predecessorChain {
		if link.PredecessorConversationID == successor.ID {
			return nil, errorsx.InvalidParam("会话继承关系会形成循环，请检查历史交接")
		}
	}

	now := time.Now()
	if err := s.disableStoreConversationProtocolMappingDB(ctx.Tx, predecessor, operator, now); err != nil {
		return nil, err
	}
	if err := ConversationAssignmentService.FinishActiveAssignments(ctx, predecessor.ID, now); err != nil {
		return nil, err
	}
	activeSession, err := repositories.ConversationChannelSessionRepository.FindActiveForUpdate(ctx.Tx, predecessor.TenantID, predecessor.ID)
	if err != nil {
		return nil, err
	}
	if activeSession != nil {
		if err := repositories.ConversationChannelSessionRepository.UpdatesInTenant(ctx.Tx, activeSession.ID, predecessor.TenantID, map[string]any{
			"ended_at":         now,
			"status":           enums.StatusDisabled,
			"updated_at":       now,
			"update_user_id":   operator.UserID,
			"update_user_name": operator.Username,
		}); err != nil {
			return nil, err
		}
	}
	closeReason := "已由授权人员继承至同门店目标员工号会话"
	if err := AIReplyTurnService.InterruptCurrentDB(ctx.Tx, predecessor, 0, "conversation_inherited"); err != nil {
		return nil, err
	}
	if err := repositories.ConversationRepository.UpdatesInTenant(ctx.Tx, predecessor.ID, predecessor.TenantID, map[string]any{
		"status":              enums.IMConversationStatusClosed,
		"current_assignee_id": int64(0),
		"current_team_id":     int64(0),
		"closed_at":           now,
		"closed_by":           operator.UserID,
		"close_reason":        closeReason,
		"updated_at":          now,
		"update_user_id":      operator.UserID,
		"update_user_name":    operator.Username,
	}); err != nil {
		return nil, err
	}
	if route := repositories.ConversationRouteStateRepository.TakeByConversationInTenant(ctx.Tx, predecessor.ID, predecessor.TenantID); route != nil {
		if err := repositories.ConversationRouteStateRepository.UpdatesInTenant(ctx.Tx, route.ID, predecessor.TenantID, map[string]any{
			"route_status":         enums.ConversationRouteStatusClosed,
			"route_target":         "closed",
			"manual_expire_at":     nil,
			"need_human_follow_up": false,
			"handoff_reason":       closeReason,
			"updated_at":           now,
			"update_user_id":       operator.UserID,
			"update_user_name":     operator.Username,
		}); err != nil {
			return nil, err
		}
	}
	link := &models.ConversationContinuityLink{
		TenantID: predecessor.TenantID, StoreID: predecessor.StoreID, CustomerID: predecessor.CustomerID,
		PredecessorConversationID: predecessor.ID, SuccessorConversationID: successor.ID,
		Reason: reason, Status: enums.StatusOk, AuditFields: utils.BuildAuditFields(operator),
	}
	link.CreatedAt = now
	link.UpdatedAt = now
	if err := repositories.ConversationContinuityLinkRepository.Create(ctx.Tx, link); err != nil {
		return nil, err
	}
	if err := s.moveArrivalBindingsToSuccessorDB(ctx, predecessor, successor, target, mode, operator, requestID); err != nil {
		return nil, err
	}
	eventPayload := s.buildEventPayload(map[string]any{
		"mappingMode":                 "operator_confirmed_cross_namespace",
		"continuityMode":              mode,
		"previousStoreStaffBindingId": predecessor.StoreStaffBindingID,
		"targetStoreStaffBindingId":   target.Binding.ID,
		"predecessorConversationId":   predecessor.ID,
		"successorConversationId":     successor.ID,
	})
	if err := ConversationEventLogService.CreateEventWithRequestID(
		ctx, predecessor.ID, requestID+":predecessor", enums.IMEventTypeRouteChange,
		enums.IMSenderTypeAgent, operator.UserID, "人工确认前序会话继承："+reason, eventPayload,
	); err != nil {
		return nil, err
	}
	if err := ConversationEventLogService.CreateEventWithRequestID(
		ctx, successor.ID, requestID+":successor", enums.IMEventTypeRouteChange,
		enums.IMSenderTypeAgent, operator.UserID, "人工确认接续历史会话："+reason, eventPayload,
	); err != nil {
		return nil, err
	}
	predecessor.Status = enums.IMConversationStatusClosed
	predecessor.CurrentAssigneeID = 0
	predecessor.CurrentTeamID = 0
	predecessor.ClosedAt = &now
	predecessor.ClosedBy = operator.UserID
	predecessor.CloseReason = closeReason
	return &storeConversationInheritanceResult{ConversationID: successor.ID, Mode: mode}, nil
}

func (s *conversationService) moveArrivalBindingsToSuccessorDB(
	ctx *sqls.TxContext,
	predecessor, successor *models.Conversation,
	target *storeConversationInheritanceTarget,
	mode string,
	operator *dto.AuthPrincipal,
	requestID string,
) error {
	if ctx == nil || ctx.Tx == nil || predecessor == nil || successor == nil || target == nil || target.Binding == nil || target.Instance == nil || operator == nil {
		return errorsx.InvalidParam("到店绑定继承上下文不完整")
	}
	items, err := repositories.ArrivalRepository.FindBindingsByConversationForUpdate(
		ctx.Tx,
		predecessor.TenantID,
		predecessor.StoreID,
		predecessor.CustomerID,
		predecessor.StoreStaffBindingID,
		predecessor.ID,
	)
	if err != nil {
		return err
	}
	if len(items) == 0 {
		return nil
	}
	now := time.Now()
	for i := range items {
		item := &items[i]
		updates := map[string]any{
			"store_staff_binding_id":            target.Binding.ID,
			"wx_work_protocol_instance_id":      target.Instance.ID,
			"conversation_id":                   successor.ID,
			"protocol_conversation_ciphertext":  "",
			"protocol_conversation_nonce":       "",
			"protocol_conversation_fingerprint": "",
			"protocol_mapped_at":                nil,
			"evidence_hash":                     arrivalSafeEvidenceHash(item.EvidenceHash, "conversation_inheritance", mode),
			"updated_at":                        now,
			"update_user_id":                    operator.UserID,
			"update_user_name":                  operator.Username,
		}
		if item.BindingProofType == enums.ArrivalBindingProofTypeProviderCallback {
			updates["binding_status"] = enums.ArrivalBindingStatusLegacyUnmapped
		}
		if err := repositories.ArrivalRepository.UpdateBinding(ctx.Tx, item.ID, item.TenantID, updates); err != nil {
			return err
		}
		detail, err := json.Marshal(map[string]any{
			"mappingMode":               "operator_confirmed_cross_namespace",
			"continuityMode":            mode,
			"sourceConversationId":      predecessor.ID,
			"successorConversationId":   successor.ID,
			"sourceStoreStaffBindingId": predecessor.StoreStaffBindingID,
			"targetStoreStaffBindingId": target.Binding.ID,
			"protocolMappingReset":      true,
		})
		if err != nil {
			return err
		}
		if err := repositories.ArrivalRepository.CreateAuditLog(ctx.Tx, &models.ArrivalAuditLog{
			TenantID: predecessor.TenantID, StoreID: predecessor.StoreID,
			Action: "conversation_inheritance", EntityType: "arrival_store_binding", EntityID: item.ID,
			Result: "success", RequestID: requestID, DetailJSON: string(detail),
			OperatorID: operator.UserID, OperatorName: operator.Username, CreatedAt: now,
		}); err != nil {
			return err
		}
	}
	return nil
}

func (s *conversationService) disableStoreConversationProtocolMappingDB(db *gorm.DB, conversation *models.Conversation, operator *dto.AuthPrincipal, now time.Time) error {
	if db == nil || conversation == nil || operator == nil {
		return errorsx.InvalidParam("会话协议映射停用上下文不完整")
	}
	mapping := repositories.WxWorkKFConversationRepository.FindOne(db, sqls.NewCnd().
		Eq("tenant_id", conversation.TenantID).
		Eq("conversation_id", conversation.ID))
	if mapping == nil {
		return nil
	}
	return repositories.WxWorkKFConversationRepository.UpdatesInTenant(db, mapping.ID, conversation.TenantID, map[string]any{
		"status":           enums.StatusDisabled,
		"updated_at":       now,
		"update_user_id":   operator.UserID,
		"update_user_name": operator.Username,
	})
}
