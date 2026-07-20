package services

import (
	"strings"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/errorsx"
	"agent-desk/internal/pkg/utils"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
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
		storeName := utils.RepairMojibakeText(strings.TrimSpace(firstNonBlank(req.StoreName, store.Name)))
		if storeName == "" {
			return errorsx.InvalidParam("请填写门店名称")
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

		if err := repositories.StoreRepository.UpdatesInTenant(ctx.Tx, store.ID, current.TenantID, map[string]any{
			"name":             storeName,
			"company_id":       0,
			"updated_at":       now,
			"update_user_id":   user.ID,
			"update_user_name": user.Username,
		}); err != nil {
			return err
		}
		if err := repositories.StoreStaffBindingRepository.UpdatesInTenant(ctx.Tx, binding.ID, current.TenantID, map[string]any{
			"company_id":                 0,
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
			"company_id":                 0,
			"store_id":                   store.ID,
			"store_staff_binding_id":     binding.ID,
			"store_address":              utils.RepairMojibakeText(strings.TrimSpace(req.StoreAddress)),
			"store_navigation_name":      utils.RepairMojibakeText(firstNonBlank(strings.TrimSpace(req.StoreNavigationName), storeName)),
			"store_longitude":            strings.TrimSpace(req.StoreLongitude),
			"store_latitude":             strings.TrimSpace(req.StoreLatitude),
			"store_map_provider":         strings.TrimSpace(req.StoreMapProvider),
			"store_contact_phone":        utils.RepairMojibakeText(strings.TrimSpace(req.StoreContactPhone)),
			"knowledge_base_id":          req.KnowledgeBaseID,
			"service_hours":              strings.TrimSpace(req.ServiceHours),
			"front_desk_mode":            normalizeWxWorkFrontDeskMode(req.FrontDeskMode),
			"front_desk_hours":           normalizeWxWorkFrontDeskHours(req.FrontDeskMode, req.FrontDeskHours),
			"store_room_conversation_id": normalizeWxWorkRoomConversationID(req.StoreRoomConversationID),
			"store_room_notify_enabled":  req.StoreRoomNotifyEnabled,
			"store_room_at_list":         normalizeWxWorkAtList(req.StoreRoomAtList),
			"fallback_to_hq":             req.FallbackToHQ,
			"manual_timeout_minutes":     normalizeManualTimeoutMinutes(req.ManualTimeoutMinutes),
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
			if err := s.copyInstanceModelSettings(ctx.Tx, replaced, current, user, now); err != nil {
				return err
			}
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
		}
		updated = repositories.WxWorkProtocolInstanceRepository.GetInTenant(ctx.Tx, current.ID, current.TenantID)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return updated, nil
}

func (s *storeIdentityLifecycleService) copyInstanceModelSettings(db *gorm.DB, oldInstance, newInstance *models.WxWorkProtocolInstance, user *models.User, now time.Time) error {
	if oldInstance == nil || newInstance == nil || oldInstance.TenantID <= 0 || oldInstance.TenantID != newInstance.TenantID {
		return errorsx.InvalidParam("替换员工号的租户归属不一致")
	}
	settings := repositories.StoreAIModelSettingRepository.Find(db, sqls.NewCnd().Eq("wx_work_instance_id", oldInstance.ID).Where("status <> ?", enums.StatusDeleted))
	for i := range settings {
		setting := settings[i]
		setting.ID = 0
		setting.CompanyID = 0
		setting.StoreID = newInstance.StoreID
		setting.WxWorkInstanceID = newInstance.ID
		setting.LastTestStatus = ""
		setting.LastTestedAt = nil
		setting.LastTestLatencyMS = 0
		setting.CreatedAt = now
		setting.CreateUserID = user.ID
		setting.CreateUserName = user.Username
		setting.UpdatedAt = now
		setting.UpdateUserID = user.ID
		setting.UpdateUserName = user.Username
		if err := repositories.StoreAIModelSettingRepository.Create(db, &setting); err != nil {
			return err
		}
	}
	return nil
}
