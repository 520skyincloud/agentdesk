package migration

import (
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/replyintent"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
)

func init() {
	register(24, "seed reply intent industry profiles", func() error {
		return sqls.WithTransaction(func(ctx *sqls.TxContext) error {
			now := time.Now()
			profile := repositories.ReplyIntentProfileRepository.Take(ctx.Tx, "code = ?", replyintent.DefaultHotelProfileCode)
			if profile == nil {
				profile = &models.ReplyIntentProfile{
					Code:               replyintent.DefaultHotelProfileCode,
					Name:               "酒店行业",
					IndustryCode:       replyintent.DefaultHotelIndustryCode,
					Description:        "无人化酒店客服回复链路的默认意图识别行业配置。",
					IntentDetectPrompt: replyintent.DefaultHotelIntentDetectPrompt(),
					IntentJSONSchema:   replyintent.DefaultHotelIntentJSONSchema(),
					Status:             enums.StatusOk,
					SortNo:             10,
					Remark:             "系统默认：酒店行业 IntentDetect prompt 与 JSON schema",
					AuditFields: models.AuditFields{
						CreatedAt:      now,
						UpdatedAt:      now,
						CreateUserID:   constants.SystemAuditUserID,
						UpdateUserID:   constants.SystemAuditUserID,
						CreateUserName: constants.SystemAuditUserName,
						UpdateUserName: constants.SystemAuditUserName,
					},
				}
				if err := repositories.ReplyIntentProfileRepository.Create(ctx.Tx, profile); err != nil {
					return err
				}
			} else {
				if err := repositories.ReplyIntentProfileRepository.Updates(ctx.Tx, profile.ID, map[string]any{
					"name":                 "酒店行业",
					"industry_code":        replyintent.DefaultHotelIndustryCode,
					"description":          "无人化酒店客服回复链路的默认意图识别行业配置。",
					"intent_detect_prompt": firstNonBlank(profile.IntentDetectPrompt, replyintent.DefaultHotelIntentDetectPrompt()),
					"intent_json_schema":   firstNonBlank(profile.IntentJSONSchema, replyintent.DefaultHotelIntentJSONSchema()),
					"status":               enums.StatusOk,
					"sort_no":              10,
					"update_user_id":       constants.SystemAuditUserID,
					"update_user_name":     constants.SystemAuditUserName,
					"updated_at":           now,
				}); err != nil {
					return err
				}
			}
			profile = repositories.ReplyIntentProfileRepository.Take(ctx.Tx, "code = ?", replyintent.DefaultHotelProfileCode)
			if profile == nil {
				return nil
			}
			if err := ctx.Tx.Model(&models.ReplyIntentConfig{}).
				Where("intent_profile_id = 0 AND code IN ?", []string{"hotel_info", "hotel_variable", "service_request", "human_complaint_risk", "interaction"}).
				Updates(map[string]any{
					"intent_profile_id": profile.ID,
					"update_user_id":    constants.SystemAuditUserID,
					"update_user_name":  constants.SystemAuditUserName,
					"updated_at":        now,
				}).Error; err != nil {
				return err
			}
			if ctx.Tx.Migrator().HasIndex(&models.ReplyIntentConfig{}, "uk_reply_intent_scope") {
				if err := ctx.Tx.Migrator().DropIndex(&models.ReplyIntentConfig{}, "uk_reply_intent_scope"); err != nil {
					return err
				}
			}
			return ctx.Tx.Migrator().CreateIndex(&models.ReplyIntentConfig{}, "uk_reply_intent_scope")
		})
	})
}

func firstNonBlank(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
