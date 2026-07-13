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

// This is deliberately conservative: it only backfills when the deployment has
// exactly one enabled industry profile and that profile is the established hotel profile.
func init() {
	register(30, "backfill knowledge base intent profiles", func() error {
		return sqls.WithTransaction(func(ctx *sqls.TxContext) error {
			profiles := repositories.ReplyIntentProfileRepository.Find(ctx.Tx, sqls.NewCnd().Eq("status", enums.StatusOk))
			if len(profiles) != 1 || profiles[0].Code != replyintent.DefaultHotelProfileCode {
				return nil
			}

			now := time.Now()
			profileID := profiles[0].ID
			if err := ctx.Tx.Model(&models.KnowledgeBase{}).
				Where("intent_profile_id = ? AND status = ?", 0, enums.StatusOk).
				Updates(map[string]any{
					"intent_profile_id": profileID,
					"update_user_id":    constants.SystemAuditUserID,
					"update_user_name":  constants.SystemAuditUserName,
					"updated_at":        now,
				}).Error; err != nil {
				return err
			}

			// Existing standalone hotel accounts become explicitly profile-bound.
			// Company-bound accounts remain inherited from their company and are left for an admin to bind.
			return ctx.Tx.Model(&models.WxWorkProtocolInstance{}).
				Where("intent_profile_id = ? AND company_id = ? AND status = ?", 0, 0, enums.StatusOk).
				Updates(map[string]any{
					"intent_profile_id": profileID,
					"update_user_id":    constants.SystemAuditUserID,
					"update_user_name":  constants.SystemAuditUserName,
					"updated_at":        now,
				}).Error
		})
	})
}
