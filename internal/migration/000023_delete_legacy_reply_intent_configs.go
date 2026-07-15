package migration

import (
	"agent-desk/internal/models"

	"github.com/mlogclub/simple/sqls"
)

func init() {
	register(23, "delete legacy reply intent configs", func() error {
		legacyCodes := []string{
			"social_confirm", "unknown_clarify", "unknown_or_clarify", "thanks_confirm", "social",
			"hotel_faq", "hotel_knowledge", "media_question", "media_understanding", "no_reply_media_only",
			"account_resource_phone", "account_resource_location", "account_resource_miniprogram",
			"phone", "location", "checkin_miniprogram", "complaint_or_risk", "handoff",
			"invoice", "supplies_self_help", "store_info_invoice", "store_info_supplies", "store_info_general", "network_wifi",
		}
		return sqls.DB().Where("code IN ?", legacyCodes).Delete(&models.ReplyIntentConfig{}).Error
	})
}
