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

// IsAutoHandoffEnabled returns the account-scoped customer preference.
// Missing settings intentionally default to enabled, preserving current behavior.
func (s *wxWorkCustomerHandoffSettingService) IsAutoHandoffEnabled(customerID, wxWorkInstanceID int64) bool {
	if customerID <= 0 || wxWorkInstanceID <= 0 {
		return true
	}
	setting := repositories.WxWorkCustomerHandoffSettingRepository.Take(sqls.DB(), "customer_id = ? AND wx_work_instance_id = ?", customerID, wxWorkInstanceID)
	return setting == nil || setting.AutoHandoffEnabled
}

func (s *wxWorkCustomerHandoffSettingService) IsAutoHandoffEnabledForConversation(conversationID int64) bool {
	conversation := ConversationService.Get(conversationID)
	if conversation == nil {
		return true
	}
	route := ConversationRouteService.GetByConversationID(conversationID)
	if route == nil {
		return true
	}
	return s.IsAutoHandoffEnabled(conversation.CustomerID, route.WxWorkInstanceID)
}

func (s *wxWorkCustomerHandoffSettingService) SetForConversation(conversationID int64, enabled bool, operator *dto.AuthPrincipal) error {
	if operator == nil {
		return errorsx.Unauthorized("未登录或登录已过期")
	}
	return sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		conversation := repositories.ConversationRepository.Get(ctx.Tx, conversationID)
		if conversation == nil {
			return errorsx.InvalidParam("会话不存在")
		}
		route := repositories.ConversationRouteStateRepository.Take(ctx.Tx, "conversation_id = ?", conversationID)
		if conversation.CustomerID <= 0 || route == nil || route.WxWorkInstanceID <= 0 {
			return errorsx.InvalidParam("当前会话未绑定可设置的企微客户账号")
		}
		now := time.Now()
		setting := repositories.WxWorkCustomerHandoffSettingRepository.Take(ctx.Tx, "customer_id = ? AND wx_work_instance_id = ?", conversation.CustomerID, route.WxWorkInstanceID)
		if setting == nil {
			setting = &models.WxWorkCustomerHandoffSetting{
				CustomerID:         conversation.CustomerID,
				WxWorkInstanceID:   route.WxWorkInstanceID,
				AutoHandoffEnabled: enabled,
				AuditFields:        utils.BuildAuditFields(operator),
			}
			if err := repositories.WxWorkCustomerHandoffSettingRepository.Create(ctx.Tx, setting); err != nil {
				return err
			}
		} else if err := repositories.WxWorkCustomerHandoffSettingRepository.Updates(ctx.Tx, setting.ID, map[string]any{
			"auto_handoff_enabled": enabled,
			"updated_at":           now,
			"update_user_id":       operator.UserID,
			"update_user_name":     operator.Username,
		}); err != nil {
			return err
		}

		if !enabled && route.PendingAction == string(enums.ConversationPendingActionHumanHandoff) {
			if err := repositories.ConversationRouteStateRepository.Updates(ctx.Tx, route.ID, map[string]any{
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

		content := "已允许该客户在当前企微员工号下自动转人工"
		if !enabled {
			content = "已禁止该客户在当前企微员工号下自动转人工"
		}
		return ConversationEventLogService.CreateEvent(ctx, conversationID, enums.IMEventTypeTransfer, enums.IMSenderTypeAgent, operator.UserID, content, ConversationService.buildEventPayload(map[string]any{
			"action":             "set_customer_auto_handoff",
			"autoHandoffEnabled": enabled,
			"wxWorkInstanceId":   route.WxWorkInstanceID,
		}))
	})
}
