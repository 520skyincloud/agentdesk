package migration

import (
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/enums"

	"github.com/mlogclub/simple/sqls"
)

func init() {
	register(14, "disable media understanding as reply intent category", func() error {
		return sqls.WithTransaction(func(ctx *sqls.TxContext) error {
			now := time.Now()
			for _, code := range []string{"media_understanding", "media_question", "no_reply_media_only"} {
				if err := ctx.Tx.Model(&models.ReplyIntentConfig{}).
					Where("code = ? AND status <> ?", code, enums.StatusDisabled).
					Updates(map[string]any{
						"status":           enums.StatusDisabled,
						"remark":           "图片/文件理解已改为 Normalize/ContextBuild 上下文资产，不再作为意图分类；语音仍走既有语转文文本链路",
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
