package services

import (
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/errorsx"
	"agent-desk/internal/pkg/utils"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
)

var WxWorkCustomerHandoffSettingService = newWxWorkCustomerHandoffSettingService()

type wxWorkCustomerHandoffSettingService struct{}

func newWxWorkCustomerHandoffSettingService() *wxWorkCustomerHandoffSettingService {
	return &wxWorkCustomerHandoffSettingService{}
}

// IsAutoHandoffEnabled returns the Store-staff-binding-scoped customer preference.
// Missing settings intentionally default to enabled, preserving current behavior.
func (s *wxWorkCustomerHandoffSettingService) IsAutoHandoffEnabled(customerID, storeStaffBindingID int64) bool {
	if customerID <= 0 || storeStaffBindingID <= 0 {
		return true
	}
	tenantID := int64(0)
	customer := repositories.CustomerRepository.Get(sqls.DB(), customerID)
	if customer != nil {
		tenantID = customer.TenantID
	}
	binding := repositories.StoreStaffBindingRepository.Get(sqls.DB(), storeStaffBindingID)
	if binding == nil || binding.TenantID <= 0 {
		return true
	}
	if tenantID > 0 && tenantID != binding.TenantID {
		return true
	}
	return s.IsAutoHandoffEnabledInTenant(customerID, storeStaffBindingID, binding.TenantID)
}

func (s *wxWorkCustomerHandoffSettingService) IsAutoHandoffEnabledInTenant(customerID, storeStaffBindingID, tenantID int64) bool {
	if customerID <= 0 || storeStaffBindingID <= 0 || tenantID <= 0 {
		return true
	}
	setting := repositories.WxWorkCustomerHandoffSettingRepository.Take(sqls.DB(), "tenant_id = ? AND customer_id = ? AND store_staff_binding_id = ?", tenantID, customerID, storeStaffBindingID)
	return setting == nil || setting.AutoHandoffEnabled
}

func (s *wxWorkCustomerHandoffSettingService) IsAutoHandoffEnabledForConversation(conversationID int64) bool {
	conversation := ConversationService.Get(conversationID)
	if conversation == nil {
		return true
	}
	return s.IsAutoHandoffEnabledInTenant(conversation.CustomerID, conversation.StoreStaffBindingID, conversation.TenantID)
}

func (s *wxWorkCustomerHandoffSettingService) SetForConversation(conversationID int64, enabled bool, operator *dto.AuthPrincipal) error {
	if operator == nil {
		return errorsx.Unauthorized("未登录或登录已过期")
	}
	tenantID, err := requireActiveTenantID(operator, "客户转人工设置")
	if err != nil {
		return err
	}
	return sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		conversation := repositories.ConversationRepository.GetInTenant(ctx.Tx, conversationID, tenantID)
		if conversation == nil {
			return errorsx.InvalidParam("会话不存在")
		}
		if conversation.CustomerID <= 0 || conversation.StoreStaffBindingID <= 0 {
			return errorsx.InvalidParam("当前会话未绑定可设置的门店员工号")
		}
		binding := repositories.StoreStaffBindingRepository.GetInTenant(ctx.Tx, conversation.StoreStaffBindingID, tenantID)
		if binding == nil || binding.Status == enums.StatusDeleted || binding.StoreID != conversation.StoreID {
			return errorsx.InvalidParam("当前会话门店员工号归属无效")
		}
		route := repositories.ConversationRouteStateRepository.Take(ctx.Tx, "conversation_id = ? AND tenant_id = ?", conversationID, tenantID)
		now := time.Now()
		setting := repositories.WxWorkCustomerHandoffSettingRepository.Take(ctx.Tx, "tenant_id = ? AND customer_id = ? AND store_staff_binding_id = ?", tenantID, conversation.CustomerID, binding.ID)
		if setting == nil {
			bindingID := binding.ID
			setting = &models.WxWorkCustomerHandoffSetting{
				TenantID:            tenantID,
				CustomerID:          conversation.CustomerID,
				StoreStaffBindingID: &bindingID,
				AutoHandoffEnabled:  enabled,
				AuditFields:         utils.BuildAuditFields(operator),
			}
			if err := repositories.WxWorkCustomerHandoffSettingRepository.Create(ctx.Tx, setting); err != nil {
				return err
			}
		} else if err := repositories.WxWorkCustomerHandoffSettingRepository.UpdatesInTenant(ctx.Tx, setting.ID, tenantID, map[string]any{
			"auto_handoff_enabled": enabled,
			"updated_at":           now,
			"update_user_id":       operator.UserID,
			"update_user_name":     operator.Username,
		}); err != nil {
			return err
		}

		if !enabled && route != nil && route.PendingAction == string(enums.ConversationPendingActionHumanHandoff) {
			if err := repositories.ConversationRouteStateRepository.UpdatesInTenant(ctx.Tx, route.ID, tenantID, map[string]any{
				"pending_action":           "",
				"pending_action_payload":   "",
				"pending_action_expire_at": nil,
				"updated_at":               now,
				"update_user_id":           operator.UserID,
				"update_user_name":         operator.Username,
			}); err != nil {
				return err
			}
		}

		content := "已允许该客户在当前门店员工号下自动转人工"
		if !enabled {
			content = "已禁止该客户在当前门店员工号下自动转人工"
		}
		return ConversationEventLogService.CreateEvent(ctx, conversationID, enums.IMEventTypeTransfer, enums.IMSenderTypeAgent, operator.UserID, content, ConversationService.buildEventPayload(map[string]any{
			"action":              "set_customer_auto_handoff",
			"autoHandoffEnabled":  enabled,
			"storeStaffBindingId": binding.ID,
		}))
	})
}
