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
	register(21, "clean reply intent prompt configs for task-first schema", func() error {
		return sqls.WithTransaction(func(ctx *sqls.TxContext) error {
			now := time.Now()
			for _, item := range defaultReplyIntentConfigs() {
				current := repositories.ReplyIntentConfigRepository.Take(ctx.Tx, "code = ? AND scope_type = ? AND company_id = 0 AND store_id = 0 AND wx_work_instance_id = 0", item.Code, "global")
				updates := map[string]any{
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
					"remark":                "五大意图分类体系；IntentDetect 以 intentTasks 为唯一事实来源",
					"update_user_id":        constants.SystemAuditUserID,
					"update_user_name":      constants.SystemAuditUserName,
					"updated_at":            now,
				}
				if current != nil {
					if err := repositories.ReplyIntentConfigRepository.Updates(ctx.Tx, current.ID, updates); err != nil {
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
					Remark:             "五大意图分类体系；IntentDetect 以 intentTasks 为唯一事实来源",
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
				"hotel_faq", "media_question", "media_understanding", "no_reply_media_only",
				"account_resource_phone", "account_resource_location", "account_resource_miniprogram",
				"phone", "location", "checkin_miniprogram", "complaint_or_risk", "handoff",
				"thanks_confirm", "social", "social_confirm", "unknown_clarify", "unknown_or_clarify", "hotel_knowledge",
				"store_info_invoice", "store_info_supplies", "store_info_general", "network_wifi",
			} {
				if err := ctx.Tx.Model(&models.ReplyIntentConfig{}).
					Where("code = ? AND scope_type = ? AND company_id = 0 AND store_id = 0 AND wx_work_instance_id = 0", code, "global").
					Updates(map[string]any{
						"status":           enums.StatusDisabled,
						"remark":           "已停用：回复运行时只使用五大意图分类，媒体/变量小类不作为顶层分类",
						"update_user_id":   constants.SystemAuditUserID,
						"update_user_name": constants.SystemAuditUserName,
						"updated_at":       now,
					}).Error; err != nil {
					return err
				}
			}
			return nil
		})
	})
}
