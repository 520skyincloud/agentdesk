package migration

import (
	"strings"
	"time"

	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/replyintent"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
)

const legacyHotelIntentDetectPromptMarker = "安全事件。安全事件 subIntent=emergency_safety。"

func init() {
	register(25, "refresh default hotel intent profile human route rules", func() error {
		return sqls.WithTransaction(func(ctx *sqls.TxContext) error {
			profile := repositories.ReplyIntentProfileRepository.Take(ctx.Tx, "code = ?", replyintent.DefaultHotelProfileCode)
			if profile == nil || !strings.Contains(profile.IntentDetectPrompt, legacyHotelIntentDetectPromptMarker) {
				return nil
			}
			now := time.Now()
			return repositories.ReplyIntentProfileRepository.Updates(ctx.Tx, profile.ID, map[string]any{
				"intent_detect_prompt": replyintent.DefaultHotelIntentDetectPrompt(),
				"intent_json_schema":   replyintent.DefaultHotelIntentJSONSchema(),
				"update_user_id":       constants.SystemAuditUserID,
				"update_user_name":     constants.SystemAuditUserName,
				"updated_at":           now,
			})
		})
	})
}
