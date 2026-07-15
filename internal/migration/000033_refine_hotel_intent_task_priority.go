package migration

import (
	"strings"
	"time"

	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/replyintent"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
)

const legacyHotelIntentTaskPriorityMarker = "否则若存在 hotel_variable 任务，primaryIntent=hotel_variable"

func init() {
	register(33, "refine hotel intent task priority and correction boundary", func() error {
		return sqls.WithTransaction(func(ctx *sqls.TxContext) error {
			profile := repositories.ReplyIntentProfileRepository.Take(ctx.Tx, "code = ?", replyintent.DefaultHotelProfileCode)
			if profile == nil || profile.UpdateUserName != constants.SystemAuditUserName || !strings.Contains(profile.IntentDetectPrompt, legacyHotelIntentTaskPriorityMarker) {
				return nil
			}
			now := time.Now()
			return repositories.ReplyIntentProfileRepository.Updates(ctx.Tx, profile.ID, map[string]any{
				"intent_detect_prompt": replyintent.DefaultHotelIntentDetectPrompt(),
				"update_user_id":       constants.SystemAuditUserID,
				"update_user_name":     constants.SystemAuditUserName,
				"updated_at":           now,
			})
		})
	})
}
