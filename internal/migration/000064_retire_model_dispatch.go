package migration

import (
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/enums"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

const retiredDispatchModelUsageCode = "dispatch_decision_llm"

func init() {
	register(64, "retire model dispatch and disable invalid automatic capacities", func() error {
		return sqls.WithTransaction(func(ctx *sqls.TxContext) error {
			return retireModelDispatch(ctx.Tx)
		})
	})
}

func retireModelDispatch(tx *gorm.DB) error {
	now := time.Now()
	audit := map[string]any{
		"updated_at":       now,
		"update_user_id":   constants.SystemAuditUserID,
		"update_user_name": constants.SystemAuditUserName,
	}
	if err := tx.Model(&models.AgentTeam{}).
		Where("dispatch_mode = ?", "intelligent").
		Updates(mergeMigrationColumns(audit, map[string]any{
			"dispatch_mode": enums.AgentTeamDispatchModeRule,
		})).Error; err != nil {
		return err
	}
	if err := tx.Model(&models.StoreAIModelSetting{}).
		Where("usage_code = ? AND status <> ?", retiredDispatchModelUsageCode, enums.StatusDeleted).
		Updates(mergeMigrationColumns(audit, map[string]any{
			"status": enums.StatusDeleted,
			"remark": "派单已改为确定性规则，该历史模型用途不再参与运行",
		})).Error; err != nil {
		return err
	}
	return tx.Model(&models.AgentProfile{}).
		Where("auto_assign_enabled = ? AND max_concurrent_count <= ? AND status <> ?", true, 0, enums.StatusDeleted).
		Updates(mergeMigrationColumns(audit, map[string]any{
			"auto_assign_enabled": false,
		})).Error
}
