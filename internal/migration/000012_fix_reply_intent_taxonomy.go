package migration

import (
	"strings"
	"time"

	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
)

func init() {
	register(12, "fix reply intent taxonomy resource versus store info", func() error {
		return sqls.WithTransaction(func(ctx *sqls.TxContext) error {
			now := time.Now()
			for _, item := range defaultReplyIntentConfigs() {
				oldCode := legacyReplyIntentCode(item.Code)
				if oldCode == "" {
					oldCode = item.Code
				}
				current := repositories.ReplyIntentConfigRepository.Take(ctx.Tx, "code = ? AND scope_type = ? AND company_id = 0 AND store_id = 0 AND wx_work_instance_id = 0", oldCode, "global")
				if current == nil {
					current = repositories.ReplyIntentConfigRepository.Take(ctx.Tx, "code = ? AND scope_type = ? AND company_id = 0 AND store_id = 0 AND wx_work_instance_id = 0", item.Code, "global")
				}
				if current == nil {
					continue
				}
				updates := map[string]any{
					"code":                  item.Code,
					"name":                  item.Name,
					"description":           item.Name,
					"keywords":              item.Keywords,
					"positive_examples":     item.PositiveExamples,
					"required_context":      item.RequiredContext,
					"needs_knowledge":       item.NeedsKnowledge,
					"needs_resource":        item.NeedsResource,
					"resource_type":         item.ResourceType,
					"needs_human_route":     item.NeedsHumanRoute,
					"human_route_policy":    item.HumanRoutePolicy,
					"no_reply_when_matched": item.NoReply,
					"priority":              item.Priority,
					"prompt_pack":           item.PromptPack,
					"reply_plan_template":   item.ReplyPlan,
					"validation_rules":      item.ValidationRules,
					"update_user_id":        constants.SystemAuditUserID,
					"update_user_name":      constants.SystemAuditUserName,
					"updated_at":            now,
				}
				if err := repositories.ReplyIntentConfigRepository.Updates(ctx.Tx, current.ID, updates); err != nil {
					return err
				}
			}
			for _, code := range []string{"invoice", "supplies_self_help", "hotel_knowledge", "store_info_invoice", "store_info_supplies", "store_info_general"} {
				current := repositories.ReplyIntentConfigRepository.Take(ctx.Tx, "code = ? AND scope_type = ? AND company_id = 0 AND store_id = 0 AND wx_work_instance_id = 0", code, "global")
				if current == nil {
					continue
				}
				if err := repositories.ReplyIntentConfigRepository.Updates(ctx.Tx, current.ID, map[string]any{
					"status":           1,
					"remark":           "已合并到 hotel_info 酒店信息大分类",
					"update_user_id":   constants.SystemAuditUserID,
					"update_user_name": constants.SystemAuditUserName,
					"updated_at":       now,
				}); err != nil {
					return err
				}
			}
			return nil
		})
	})
}

func legacyReplyIntentCode(code string) string {
	switch strings.TrimSpace(code) {
	case "hotel_info":
		return "network_wifi"
	case "store_info_network_wifi":
		return "network_wifi"
	case "store_info_invoice":
		return "invoice"
	case "store_info_supplies":
		return "supplies_self_help"
	case "store_info_general":
		return "hotel_knowledge"
	case "account_resource_miniprogram":
		return "checkin_miniprogram"
	case "account_resource_location":
		return "location"
	case "account_resource_phone":
		return "phone"
	default:
		return ""
	}
}
