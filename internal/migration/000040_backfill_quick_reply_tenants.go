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
	register(40, "backfill quick reply tenants", func() error {
		return sqls.WithTransaction(func(ctx *sqls.TxContext) error {
			return backfillQuickReplyTenants(ctx.Tx)
		})
	})
}

func backfillQuickReplyTenants(tx *gorm.DB) error {
	legacyTenant := repositories.TenantRepository.GetByTenantCode(tx, constants.LegacyDefaultTenantCode)
	if legacyTenant == nil {
		return fmt.Errorf("legacy default tenant is required before migration 40")
	}
	var list []models.QuickReply
	if err := tx.Order("id ASC").Find(&list).Error; err != nil {
		return err
	}
	now := time.Now()
	for i := range list {
		item := &list[i]
		if item.TenantID <= 0 {
			if err := tx.Model(&models.QuickReply{}).Where("id = ? AND tenant_id = ?", item.ID, 0).Updates(map[string]any{
				"tenant_id": legacyTenant.ID, "updated_at": now,
				"update_user_id": constants.SystemAuditUserID, "update_user_name": constants.SystemAuditUserName,
			}).Error; err != nil {
				return err
			}
			continue
		}
		if repositories.TenantRepository.Get(tx, item.TenantID) == nil {
			return fmt.Errorf("quick reply %d references missing tenant %d", item.ID, item.TenantID)
		}
	}
	return nil
}
