package migration

import (
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
)

func init() {
	register(13, "normalize reply intent configs to seven categories", func() error {
		return sqls.WithTransaction(func(ctx *sqls.TxContext) error {
			now := time.Now()
			for _, item := range defaultReplyIntentConfigs() {
				current := findGlobalReplyIntentConfig(ctx, item.Code)
				if current == nil {
					current = findReusableLegacyReplyIntentConfig(ctx, item.Code)
				}
				columns := map[string]any{
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
					"status":                enums.StatusOk,
					"remark":                "七大意图分类体系",
					"update_user_id":        constants.SystemAuditUserID,
					"update_user_name":      constants.SystemAuditUserName,
					"updated_at":            now,
				}
				if current != nil {
					if err := repositories.ReplyIntentConfigRepository.Updates(ctx.Tx, current.ID, columns); err != nil {
						return err
					}
					continue
				}
				config := &models.ReplyIntentConfig{
					Code:               item.Code,
					Name:               item.Name,
					Description:        item.Name,
					ScopeType:          "global",
					Priority:           item.Priority,
					MatchMode:          "hybrid",
					Keywords:           item.Keywords,
					PositiveExamples:   item.PositiveExamples,
					RequiredContext:    item.RequiredContext,
					NeedsKnowledge:     item.NeedsKnowledge,
					NeedsResource:      item.NeedsResource,
					ResourceType:       item.ResourceType,
					NeedsHumanRoute:    item.NeedsHumanRoute,
					HumanRoutePolicy:   item.HumanRoutePolicy,
					PromptPack:         item.PromptPack,
					ReplyPlanTemplate:  item.ReplyPlan,
					ValidationRules:    item.ValidationRules,
					NoReplyWhenMatched: item.NoReply,
					Status:             enums.StatusOk,
					Remark:             "七大意图分类体系",
					AuditFields: models.AuditFields{
						CreatedAt:      now,
						UpdatedAt:      now,
						CreateUserID:   constants.SystemAuditUserID,
						UpdateUserID:   constants.SystemAuditUserID,
						CreateUserName: constants.SystemAuditUserName,
						UpdateUserName: constants.SystemAuditUserName,
					},
				}
				if err := repositories.ReplyIntentConfigRepository.Create(ctx.Tx, config); err != nil {
					return err
				}
			}
			for _, code := range []string{
				"account_resource_phone", "account_resource_location", "account_resource_miniprogram",
				"no_reply_media_only", "media_question", "complaint_or_risk", "handoff", "thanks_confirm", "social", "unknown_or_clarify",
				"invoice", "supplies_self_help", "hotel_knowledge", "store_info_invoice", "store_info_supplies", "store_info_general", "network_wifi",
			} {
				current := findGlobalReplyIntentConfig(ctx, code)
				if current == nil {
					continue
				}
				if err := repositories.ReplyIntentConfigRepository.Updates(ctx.Tx, current.ID, map[string]any{
					"status":           enums.StatusDisabled,
					"remark":           "已迁移到七大意图分类体系；变量和媒体门控不再作为独立分类",
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

func findGlobalReplyIntentConfig(ctx *sqls.TxContext, code string) *models.ReplyIntentConfig {
	return repositories.ReplyIntentConfigRepository.Take(ctx.Tx, "code = ? AND scope_type = ? AND company_id = 0 AND store_id = 0 AND wx_work_instance_id = 0", code, "global")
}

func findReusableLegacyReplyIntentConfig(ctx *sqls.TxContext, code string) *models.ReplyIntentConfig {
	legacyCodes := map[string][]string{
		"hotel_info":           {"hotel_knowledge", "network_wifi", "invoice", "supplies_self_help", "store_info_general"},
		"hotel_variable":       {"account_resource_phone", "account_resource_location", "account_resource_miniprogram", "phone", "location", "checkin_miniprogram"},
		"human_complaint_risk": {"complaint_or_risk", "handoff"},
		"social_confirm":       {"thanks_confirm", "social"},
		"unknown_clarify":      {"unknown_or_clarify"},
	}
	for _, legacyCode := range legacyCodes[code] {
		if current := findGlobalReplyIntentConfig(ctx, legacyCode); current != nil {
			return current
		}
	}
	return nil
}
