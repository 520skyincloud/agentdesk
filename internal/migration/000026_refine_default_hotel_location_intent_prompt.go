package migration

import (
	"strings"
	"time"

	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/replyintent"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
)

const legacyHotelLocationIntentPromptMarker = "“要门店变量”才 hotel_variable"

func init() {
	register(26, "refine default hotel location intent prompt", func() error {
		return sqls.WithTransaction(func(ctx *sqls.TxContext) error {
			profile := repositories.ReplyIntentProfileRepository.Take(ctx.Tx, "code = ?", replyintent.DefaultHotelProfileCode)
			if profile == nil || profile.UpdateUserName != constants.SystemAuditUserName || !strings.Contains(profile.IntentDetectPrompt, legacyHotelLocationIntentPromptMarker) {
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
