package services

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/errorsx"
	"agent-desk/internal/pkg/utils"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
)

var StoreIdentityLifecycleService = newStoreIdentityLifecycleService()

type storeIdentityLifecycleService struct{}

func newStoreIdentityLifecycleService() *storeIdentityLifecycleService {
	return &storeIdentityLifecycleService{}
}

func (s *storeIdentityLifecycleService) CompleteBindingSetup(instance *models.WxWorkProtocolInstance, req request.UpdateWxWorkProtocolRemoteSetupRequest) (*models.WxWorkProtocolInstance, error) {
	if instance == nil || instance.TenantID <= 0 {
		return nil, errorsx.InvalidParam("企微员工号绑定链接不存在或已失效")
	}
	email, err := normalizeVerificationEmail(req.Email)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(req.EmailVerificationToken) == "" {
		return nil, errorsx.InvalidParam("请先完成邮箱验证码验证")
	}

	var updated *models.WxWorkProtocolInstance
	err = sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		current := repositories.WxWorkProtocolInstanceRepository.GetForUpdateInTenant(ctx.Tx, instance.ID, instance.TenantID)
		if current == nil || current.Status == enums.StatusDeleted || current.RemoteSetupToken != req.Token {
			return errorsx.InvalidParam("企微员工号绑定链接不存在或已失效")
		}
		if current.RemoteSetupExpiresAt != nil && time.Now().After(*current.RemoteSetupExpiresAt) {
			return errorsx.InvalidParam("企微员工号绑定链接已过期，请联系公司主管重新生成")
		}
		if current.RemoteSetupSubmittedAt != nil {
			return errorsx.InvalidParam("企微员工号绑定已提交，请勿重复操作")
		}
		binding := repositories.StoreStaffBindingRepository.GetInTenant(ctx.Tx, current.StoreStaffBindingID, current.TenantID)
		if binding == nil || binding.Status != enums.StatusOk || binding.StoreID != current.StoreID {
			return errorsx.InvalidParam("绑定链接缺少有效系统账号，请联系公司主管重新生成")
		}
		if err := StoreStaffBindingService.validateBindingOwnerDB(ctx.Tx, binding); err != nil {
			return err
		}
		user := repositories.UserRepository.GetInTenant(ctx.Tx, binding.UserID, current.TenantID)
		if user == nil || user.Email == nil || !strings.EqualFold(strings.TrimSpace(*user.Email), email) {
			return errorsx.InvalidParam("请使用该门店员工系统账号登记的邮箱完成验证")
		}
		store := repositories.StoreRepository.GetInTenant(ctx.Tx, binding.StoreID, current.TenantID)
		if store == nil || store.Status == enums.StatusDeleted {
			return errorsx.InvalidParam("门店身份不存在或已删除")
		}
		if _, err := WxWorkProtocolInstanceService.resolveStoreKnowledgeBaseIDDB(ctx.Tx, current.TenantID, store.ID); err != nil {
			return err
		}
		storeName := utils.RepairMojibakeText(strings.TrimSpace(store.Name))
		if storeName == "" {
			return errorsx.InvalidParam("所选门店名称不能为空，请先由有权限的管理员完善门店资料")
		}
		var replaced *models.WxWorkProtocolInstance
		if current.ReplacesInstanceID > 0 {
			replaced = repositories.WxWorkProtocolInstanceRepository.GetForUpdateInTenant(ctx.Tx, current.ReplacesInstanceID, current.TenantID)
			if replaced == nil || replaced.Status == enums.StatusDeleted || replaced.StoreID <= 0 || replaced.StoreID != current.StoreID || replaced.StoreStaffBindingID != binding.ID {
				return errorsx.InvalidParam("待替换的原企微员工号状态不正确")
			}
			if strings.TrimSpace(current.EmployeeUserID) == "" {
				return errorsx.InvalidParam("请先使用新的企微员工号完成扫码登录")
			}
		}
		now := time.Now()
		if err := EmailVerificationService.ConsumeVerifiedToken(ctx.Tx, EmailVerificationPurposeRemoteSetup, email, req.Token, req.EmailVerificationToken); err != nil {
			return err
		}
		if err := repositories.UserRepository.UpdatesInTenant(ctx.Tx, user.ID, current.TenantID, map[string]any{
			"email_verified_at": now,
			"updated_at":        now,
			"update_user_id":    user.ID,
			"update_user_name":  user.Username,
		}); err != nil {
			return err
		}

		if err := repositories.StoreStaffBindingRepository.UpdatesInTenant(ctx.Tx, binding.ID, current.TenantID, map[string]any{
			"managed_mode":               normalizeStoreManagedMode(req.ManagedMode),
			"service_hours":              strings.TrimSpace(req.ServiceHours),
			"store_room_conversation_id": normalizeWxWorkRoomConversationID(req.StoreRoomConversationID),
			"store_room_notify_enabled":  req.StoreRoomNotifyEnabled,
			"store_room_at_list":         normalizeWxWorkAtList(req.StoreRoomAtList),
			"fallback_to_hq":             req.FallbackToHQ,
			"manual_timeout_minutes":     normalizeManualTimeoutMinutes(req.ManualTimeoutMinutes),
			"updated_at":                 now,
			"update_user_id":             user.ID,
			"update_user_name":           user.Username,
		}); err != nil {
			return err
		}

		guid := normalizeProtocolDeviceGUID(req.Guid)
		if guid != "" && guid != current.Guid {
			if existing := repositories.WxWorkProtocolInstanceRepository.Take(ctx.Tx, "guid = ? AND id <> ? AND status <> ?", guid, current.ID, enums.StatusDeleted); existing != nil {
				return errorsx.InvalidParam("该协议设备 GUID 已绑定到其他员工号")
			}
		}
		aiReplyEnabled := false
		if replaced != nil {
			aiReplyEnabled = replaced.AIReplyEnabled
		}
		updates := map[string]any{
			"employee_name":              utils.RepairMojibakeText(strings.TrimSpace(req.EmployeeName)),
			"store_id":                   store.ID,
			"store_staff_binding_id":     binding.ID,
			"front_desk_mode":            normalizeWxWorkFrontDeskMode(req.FrontDeskMode),
			"front_desk_hours":           normalizeWxWorkFrontDeskHours(req.FrontDeskMode, req.FrontDeskHours),
			"auto_accept_friend_request": req.AutoAcceptFriendRequest,
			"ai_reply_enabled":           aiReplyEnabled,
			"remote_setup_submitted_at":  now,
			"updated_at":                 now,
			"update_user_id":             user.ID,
			"update_user_name":           user.Username,
		}
		if guid != "" {
			updates["guid"] = guid
		}
		if err := repositories.WxWorkProtocolInstanceRepository.UpdatesInTenant(ctx.Tx, current.ID, current.TenantID, updates); err != nil {
			return err
		}
		if replaced != nil {
			if err := repositories.WxWorkProtocolInstanceRepository.UpdatesInTenant(ctx.Tx, replaced.ID, current.TenantID, map[string]any{
				"ai_reply_enabled":        false,
				"status":                  enums.StatusDisabled,
				"replaced_by_instance_id": current.ID,
				"replaced_at":             now,
				"updated_at":              now,
				"update_user_id":          user.ID,
				"update_user_name":        user.Username,
			}); err != nil {
				return err
			}
			if err := s.syncArrivalInstanceReplacementDB(ctx, replaced, current, binding, user, now); err != nil {
				return err
			}
		}
		updated = repositories.WxWorkProtocolInstanceRepository.GetInTenant(ctx.Tx, current.ID, current.TenantID)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return updated, nil
}

func (s *storeIdentityLifecycleService) syncArrivalInstanceReplacementDB(
	ctx *sqls.TxContext,
	replaced, replacement *models.WxWorkProtocolInstance,
	binding *models.StoreStaffBinding,
	operator *models.User,
	now time.Time,
) error {
	if ctx == nil || ctx.Tx == nil || replaced == nil || replacement == nil || binding == nil || operator == nil {
		return errorsx.InvalidParam("企微员工号替换上下文不完整")
	}
	if replaced.TenantID != replacement.TenantID || replaced.StoreID != replacement.StoreID ||
		replaced.StoreStaffBindingID != binding.ID || replacement.StoreStaffBindingID != binding.ID ||
		binding.StoreID != replacement.StoreID {
		return errorsx.InvalidParam("企微员工号替换范围不一致")
	}
	requestID := "wxwork_instance_replacement_" + strconv.FormatInt(replacement.ID, 10)
	detail, err := json.Marshal(map[string]any{
		"mappingMode":           "same_store_staff_binding_instance_replacement",
		"storeStaffBindingId":   binding.ID,
		"previousInstanceId":    replaced.ID,
		"replacementInstanceId": replacement.ID,
		"protocolMappingReset":  true,
	})
	if err != nil {
		return err
	}
	connections, err := repositories.ArrivalRepository.FindConnectionsByBindingInstanceForUpdate(
		ctx.Tx,
		replacement.TenantID,
		replacement.StoreID,
		binding.ID,
		replaced.ID,
	)
	if err != nil {
		return err
	}
	for i := range connections {
		if err := repositories.ArrivalRepository.UpdateConnection(ctx.Tx, connections[i].ID, connections[i].TenantID, map[string]any{
			"wx_work_protocol_instance_id": replacement.ID,
			"updated_at":                   now,
			"update_user_id":               operator.ID,
			"update_user_name":             operator.Username,
		}); err != nil {
			return err
		}
		if err := repositories.ArrivalRepository.CreateAuditLog(ctx.Tx, &models.ArrivalAuditLog{
			TenantID: replacement.TenantID, StoreID: replacement.StoreID,
			Action: "wxwork_instance_replacement", EntityType: "store_arrival_connection", EntityID: connections[i].ID,
			Result: "success", RequestID: requestID, DetailJSON: string(detail),
			OperatorID: operator.ID, OperatorName: operator.Username, CreatedAt: now,
		}); err != nil {
			return err
		}
	}
	arrivalBindings, err := repositories.ArrivalRepository.FindBindingsByBindingInstanceForUpdate(
		ctx.Tx,
		replacement.TenantID,
		replacement.StoreID,
		binding.ID,
		replaced.ID,
	)
	if err != nil {
		return err
	}
	for i := range arrivalBindings {
		item := &arrivalBindings[i]
		updates := map[string]any{
			"wx_work_protocol_instance_id":      replacement.ID,
			"protocol_conversation_ciphertext":  "",
			"protocol_conversation_nonce":       "",
			"protocol_conversation_fingerprint": "",
			"protocol_mapped_at":                nil,
			"evidence_hash":                     arrivalSafeEvidenceHash(item.EvidenceHash, "wxwork_instance_replacement", strconv.FormatInt(replaced.ID, 10), strconv.FormatInt(replacement.ID, 10)),
			"updated_at":                        now,
			"update_user_id":                    operator.ID,
			"update_user_name":                  operator.Username,
		}
		if item.BindingProofType == enums.ArrivalBindingProofTypeProviderCallback {
			updates["binding_status"] = enums.ArrivalBindingStatusLegacyUnmapped
		}
		if err := repositories.ArrivalRepository.UpdateBinding(ctx.Tx, item.ID, item.TenantID, updates); err != nil {
			return err
		}
		if err := repositories.ArrivalRepository.CreateAuditLog(ctx.Tx, &models.ArrivalAuditLog{
			TenantID: replacement.TenantID, StoreID: replacement.StoreID,
			Action: "wxwork_instance_replacement", EntityType: "arrival_store_binding", EntityID: item.ID,
			Result: "success", RequestID: requestID, DetailJSON: string(detail),
			OperatorID: operator.ID, OperatorName: operator.Username, CreatedAt: now,
		}); err != nil {
			return err
		}
	}
	return nil
}
