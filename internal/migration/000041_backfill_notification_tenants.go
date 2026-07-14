package migration

import (
	"fmt"

	"agent-desk/internal/models"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

func init() {
	register(41, "backfill notification tenants", func() error {
		return sqls.WithTransaction(func(ctx *sqls.TxContext) error {
			return backfillNotificationTenants(ctx.Tx)
		})
	})
}

func backfillNotificationTenants(tx *gorm.DB) error {
	var list []models.Notification
	if err := tx.Order("id ASC").Find(&list).Error; err != nil {
		return err
	}
	for i := range list {
		item := &list[i]
		recipient := repositories.UserRepository.Get(tx, item.RecipientUserID)
		if recipient == nil {
			return fmt.Errorf("notification %d references missing recipient user %d", item.ID, item.RecipientUserID)
		}
		if item.TenantID > 0 && item.TenantID != recipient.TenantID {
			return fmt.Errorf("notification %d tenant %d conflicts with recipient user %d tenant %d", item.ID, item.TenantID, recipient.ID, recipient.TenantID)
		}
		if item.TenantID == recipient.TenantID {
			continue
		}
		if err := tx.Model(&models.Notification{}).
			Where("id = ? AND tenant_id = ?", item.ID, item.TenantID).
			Update("tenant_id", recipient.TenantID).Error; err != nil {
			return err
		}
	}
	return nil
}
