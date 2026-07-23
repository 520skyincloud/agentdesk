package migration

import (
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/enums"

	"github.com/mlogclub/simple/sqls"
)

func init() {
	register(16, "disable legacy wxwork dedicated ai agents", func() error {
		now := time.Now()
		return sqls.WithTransaction(func(ctx *sqls.TxContext) error {
			if ctx.Tx.Migrator().HasColumn("t_wx_work_protocol_instance", "ai_agent_id") {
				if err := ctx.Tx.Table("t_wx_work_protocol_instance").Where("ai_agent_id > 0").Updates(map[string]any{
					"ai_agent_id": 0, "updated_at": now,
					"update_user_id": constants.SystemAuditUserID, "update_user_name": constants.SystemAuditUserName,
				}).Error; err != nil {
					return err
				}
			}
			return ctx.Tx.Model(&models.AIAgent{}).Where("name LIKE ?", "%独立配置%").Updates(map[string]any{
				"status": enums.StatusDisabled, "updated_at": now,
				"update_user_id": constants.SystemAuditUserID, "update_user_name": constants.SystemAuditUserName,
			}).Error
		})
	})
}
