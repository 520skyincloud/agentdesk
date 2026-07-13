package services

import (
	"fmt"
	"strings"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/errorsx"
	"agent-desk/internal/pkg/utils"
	"agent-desk/internal/repositories"

	"github.com/google/uuid"
	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

var StoreAccountLifecycleService = newStoreAccountLifecycleService()

type storeAccountLifecycleService struct{}

func newStoreAccountLifecycleService() *storeAccountLifecycleService {
	return &storeAccountLifecycleService{}
}

func (s *storeAccountLifecycleService) CompleteRemoteSetup(instance *models.WxWorkProtocolInstance, req request.UpdateWxWorkProtocolRemoteSetupRequest) (*models.WxWorkProtocolInstance, error) {
	if instance == nil {
		return nil, errorsx.InvalidParam("远程配置链接不存在或已失效")
	}
	email, err := normalizeVerificationEmail(req.Email)
	if err != nil {
		return nil, err
	}
	storeName := utils.RepairMojibakeText(strings.TrimSpace(firstNonBlank(req.StoreName, req.EmployeeName)))
	if storeName == "" {
		return nil, errorsx.InvalidParam("请填写店名")
	}
	if strings.TrimSpace(req.EmailVerificationToken) == "" {
		return nil, errorsx.InvalidParam("请先完成邮箱验证码验证")
	}

	var updated *models.WxWorkProtocolInstance
	err = sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		current := repositories.WxWorkProtocolInstanceRepository.GetForUpdate(ctx.Tx, instance.ID)
		if current == nil || current.Status == enums.StatusDeleted || current.RemoteSetupToken != req.Token {
			return errorsx.InvalidParam("远程配置链接不存在或已失效")
		}
		if current.RemoteSetupExpiresAt != nil && time.Now().After(*current.RemoteSetupExpiresAt) {
			return errorsx.InvalidParam("远程配置链接已过期，请联系总部重新生成")
		}
		if current.RemoteSetupSubmittedAt != nil {
			return errorsx.InvalidParam("远程配置已提交，请勿重复开户")
		}
		var replaced *models.WxWorkProtocolInstance
		if current.ReplacesInstanceID > 0 {
			replaced = repositories.WxWorkProtocolInstanceRepository.GetForUpdate(ctx.Tx, current.ReplacesInstanceID)
			if replaced == nil || replaced.Status == enums.StatusDeleted || replaced.StoreID <= 0 || replaced.StoreID != current.StoreID {
				return errorsx.InvalidParam("待替换的原企微员工号状态不正确")
			}
			if strings.TrimSpace(current.EmployeeUserID) == "" {
				return errorsx.InvalidParam("请先使用新的企微员工号完成扫码登录")
			}
			binding := repositories.StoreStaffBindingRepository.Take(ctx.Tx, "store_id = ? AND status <> ?", replaced.StoreID, enums.StatusDeleted)
			if binding == nil || binding.UserID <= 0 {
				return errorsx.InvalidParam("原门店尚未绑定主邮箱账号")
			}
			owner := repositories.UserRepository.Get(ctx.Tx, binding.UserID)
			if owner == nil || owner.Email == nil || !strings.EqualFold(strings.TrimSpace(*owner.Email), email) {
				return errorsx.InvalidParam("必须使用原门店主邮箱验证更换员工号")
			}
		}
		if err := EmailVerificationService.ConsumeVerifiedToken(ctx.Tx, EmailVerificationPurposeRemoteSetup, email, req.Token, req.EmailVerificationToken); err != nil {
			return err
		}

		store, err := s.ensureStableStore(ctx.Tx, current, req.StoreID, storeName)
		if err != nil {
			return err
		}
		user, err := s.ensurePrimaryStoreUser(ctx.Tx, store, email, storeName)
		if err != nil {
			return err
		}
		binding, err := s.ensureStoreStaffBinding(ctx.Tx, current, store, user, req)
		if err != nil {
			return err
		}

		now := time.Now()
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
			"company_id":                 store.CompanyID,
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
		if err := repositories.WxWorkProtocolInstanceRepository.Updates(ctx.Tx, current.ID, updates); err != nil {
			return err
		}
		if replaced != nil {
			if err := s.copyInstanceModelSettings(ctx.Tx, replaced, current, user, now); err != nil {
				return err
			}
			if err := repositories.WxWorkProtocolInstanceRepository.Updates(ctx.Tx, replaced.ID, map[string]any{
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
		updated = repositories.WxWorkProtocolInstanceRepository.Get(ctx.Tx, current.ID)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return updated, nil
}

func (s *storeAccountLifecycleService) copyInstanceModelSettings(db *gorm.DB, oldInstance, newInstance *models.WxWorkProtocolInstance, user *models.User, now time.Time) error {
	settings := repositories.StoreAIModelSettingRepository.Find(db, sqls.NewCnd().Eq("wx_work_instance_id", oldInstance.ID).Where("status <> ?", enums.StatusDeleted))
	for i := range settings {
		setting := settings[i]
		setting.ID = 0
		setting.CompanyID = newInstance.CompanyID
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

func (s *storeAccountLifecycleService) ensureStableStore(db *gorm.DB, instance *models.WxWorkProtocolInstance, requestedStoreID int64, storeName string) (*models.Store, error) {
	storeID := requestedStoreID
	if instance.StoreID > 0 {
		storeID = instance.StoreID
	}
	if storeID > 0 {
		store := repositories.StoreRepository.Get(db, storeID)
		if store == nil || store.Status == enums.StatusDeleted {
			return nil, errorsx.InvalidParam("门店不存在")
		}
		if instance.CompanyID > 0 && store.CompanyID > 0 && store.CompanyID != instance.CompanyID {
			return nil, errorsx.InvalidParam("门店不属于开户链接绑定的公司")
		}
		updates := map[string]any{"name": storeName, "updated_at": time.Now(), "update_user_name": "remote_store_setup"}
		if instance.CompanyID > 0 && store.CompanyID == 0 {
			updates["company_id"] = instance.CompanyID
		}
		if err := repositories.StoreRepository.Updates(db, store.ID, updates); err != nil {
			return nil, err
		}
		store.Name = storeName
		if instance.CompanyID > 0 {
			store.CompanyID = instance.CompanyID
		}
		return store, nil
	}
	now := time.Now()
	store := &models.Store{
		StoreCode: generateWxWorkInternalStoreCode(instance.CompanyID),
		Name:      storeName,
		CompanyID: instance.CompanyID,
		Status:    enums.StatusOk,
		Remark:    "门店自助开户注册生成的长期资源容器",
		AuditFields: models.AuditFields{
			CreatedAt:      now,
			CreateUserID:   constants.SystemAuditUserID,
			CreateUserName: constants.SystemAuditUserName,
			UpdatedAt:      now,
			UpdateUserID:   constants.SystemAuditUserID,
			UpdateUserName: constants.SystemAuditUserName,
		},
	}
	if err := repositories.StoreRepository.Create(db, store); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *storeAccountLifecycleService) ensurePrimaryStoreUser(db *gorm.DB, store *models.Store, email, storeName string) (*models.User, error) {
	user := repositories.UserRepository.GetByEmail(db, email)
	if user != nil {
		if user.Status != enums.StatusOk || user.DeletedAt != nil {
			return nil, errorsx.InvalidParam("该邮箱绑定的系统账号不可用")
		}
		if binding := repositories.StoreStaffBindingRepository.Take(db, "user_id = ? AND store_id <> ? AND status <> ?", user.ID, store.ID, enums.StatusDeleted); binding != nil {
			return nil, errorsx.InvalidParam("该邮箱已绑定其他门店")
		}
		now := time.Now()
		if err := repositories.UserRepository.Updates(db, user.ID, map[string]any{"email_verified_at": now, "updated_at": now, "update_user_id": user.ID, "update_user_name": user.Username}); err != nil {
			return nil, err
		}
		user.EmailVerifiedAt = &now
		if err := s.ensureStoreStaffRole(db, user); err != nil {
			return nil, err
		}
		return user, nil
	}

	now := time.Now()
	username := "store_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:16]
	user = &models.User{
		Username:        username,
		Nickname:        storeName,
		Email:           &email,
		EmailVerifiedAt: &now,
		Status:          enums.StatusOk,
		Remark:          "门店自助开户注册，使用邮箱验证码登录",
		AuditFields: models.AuditFields{
			CreatedAt:      now,
			CreateUserID:   constants.SystemAuditUserID,
			CreateUserName: constants.SystemAuditUserName,
			UpdatedAt:      now,
			UpdateUserID:   constants.SystemAuditUserID,
			UpdateUserName: constants.SystemAuditUserName,
		},
	}
	if err := repositories.UserRepository.Create(db, user); err != nil {
		return nil, err
	}
	if err := s.ensureStoreStaffRole(db, user); err != nil {
		return nil, err
	}
	return user, nil
}

func (s *storeAccountLifecycleService) ensureStoreStaffRole(db *gorm.DB, user *models.User) error {
	role := repositories.RoleRepository.GetByCode(db, constants.RoleCodeStoreStaff)
	if role == nil || role.Status != enums.StatusOk {
		return errorsx.InvalidParam("系统缺少门店员工角色配置")
	}
	if repositories.UserRoleRepository.FindOne(db, sqls.NewCnd().Eq("user_id", user.ID).Eq("role_id", role.ID)) != nil {
		return nil
	}
	now := time.Now()
	return repositories.UserRoleRepository.Create(db, &models.UserRole{
		UserID: user.ID,
		RoleID: role.ID,
		AuditFields: models.AuditFields{
			CreatedAt:      now,
			CreateUserID:   user.ID,
			CreateUserName: user.Username,
			UpdatedAt:      now,
			UpdateUserID:   user.ID,
			UpdateUserName: user.Username,
		},
	})
}

func (s *storeAccountLifecycleService) ensureStoreStaffBinding(db *gorm.DB, instance *models.WxWorkProtocolInstance, store *models.Store, user *models.User, req request.UpdateWxWorkProtocolRemoteSetupRequest) (*models.StoreStaffBinding, error) {
	now := time.Now()
	if binding := repositories.StoreStaffBindingRepository.Take(db, "store_id = ? AND status <> ?", store.ID, enums.StatusDeleted); binding != nil {
		if binding.UserID > 0 && binding.UserID != user.ID {
			return nil, errorsx.InvalidParam("该门店已绑定其他主邮箱账号")
		}
		if err := repositories.StoreStaffBindingRepository.Updates(db, binding.ID, map[string]any{
			"user_id":                    user.ID,
			"company_id":                 store.CompanyID,
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
			return nil, err
		}
		binding.UserID = user.ID
		return binding, nil
	}
	binding := &models.StoreStaffBinding{
		UserID:                  user.ID,
		CompanyID:               store.CompanyID,
		StoreID:                 store.ID,
		ManagedMode:             normalizeStoreManagedMode(req.ManagedMode),
		ServiceHours:            strings.TrimSpace(req.ServiceHours),
		StoreRoomConversationID: normalizeWxWorkRoomConversationID(req.StoreRoomConversationID),
		StoreRoomNotifyEnabled:  req.StoreRoomNotifyEnabled,
		StoreRoomAtList:         normalizeWxWorkAtList(req.StoreRoomAtList),
		FallbackToHQ:            req.FallbackToHQ,
		ManualTimeoutMinutes:    normalizeManualTimeoutMinutes(req.ManualTimeoutMinutes),
		Status:                  enums.StatusOk,
		Remark:                  fmt.Sprintf("门店主邮箱 %s", emailForAudit(user)),
		AuditFields: models.AuditFields{
			CreatedAt:      now,
			CreateUserID:   user.ID,
			CreateUserName: user.Username,
			UpdatedAt:      now,
			UpdateUserID:   user.ID,
			UpdateUserName: user.Username,
		},
	}
	if err := repositories.StoreStaffBindingRepository.Create(db, binding); err != nil {
		return nil, err
	}
	return binding, nil
}

func emailForAudit(user *models.User) string {
	if user != nil && user.Email != nil {
		return *user.Email
	}
	return ""
}
