package migration

import (
	"strings"
	"time"

	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/replyintent"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
)

const legacyHotelLocationReferenceIntentPromptMarker = "最近一轮尚未解决的外部地点"

func init() {
	register(28, "refine default hotel location discourse prompt", func() error {
		return sqls.WithTransaction(func(ctx *sqls.TxContext) error {
			profile := repositories.ReplyIntentProfileRepository.Take(ctx.Tx, "code = ?", replyintent.DefaultHotelProfileCode)
			if profile == nil || profile.UpdateUserName != constants.SystemAuditUserName || !strings.Contains(profile.IntentDetectPrompt, legacyHotelLocationReferenceIntentPromptMarker) {
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
