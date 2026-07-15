package migration

import (
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/enums"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

func init() {
	register(58, "backfill tenant invitation expirations", func() error {
		return sqls.WithTransaction(func(ctx *sqls.TxContext) error {
			return backfillTenantInvitationExpirations(ctx.Tx, time.Now())
		})
	})
}

func backfillTenantInvitationExpirations(tx *gorm.DB, now time.Time) error {
	expiresAt := now.Add(time.Duration(constants.TenantInvitationValidityDays) * 24 * time.Hour)
	return tx.Model(&models.TenantInvitation{}).
		Where("expires_at IS NULL AND status = ?", enums.StatusOk).
		UpdateColumn("expires_at", expiresAt).Error
}
