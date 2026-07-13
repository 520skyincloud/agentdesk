package migration

import (
	"fmt"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

func init() {
	register(36, "backfill agent team tenants", func() error {
		return sqls.WithTransaction(func(ctx *sqls.TxContext) error {
			return backfillAgentTeamTenants(ctx.Tx)
		})
	})
}

func backfillAgentTeamTenants(tx *gorm.DB) error {
	legacyTenant := repositories.TenantRepository.GetByTenantCode(tx, constants.LegacyDefaultTenantCode)
	if legacyTenant == nil {
		return fmt.Errorf("legacy default tenant is required before migration 36")
	}
	now := time.Now()
	return tx.Model(&models.AgentTeam{}).
		Where("tenant_id = ?", 0).
		Updates(map[string]any{
			"tenant_id":        legacyTenant.ID,
			"update_user_id":   constants.SystemAuditUserID,
			"update_user_name": constants.SystemAuditUserName,
			"updated_at":       now,
		}).Error
}
