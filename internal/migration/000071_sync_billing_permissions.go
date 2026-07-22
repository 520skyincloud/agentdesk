package migration

import (
	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

const syncBillingPermissionsMigrationRemark = "sync store billing query permissions"

func init() {
	register(71, syncBillingPermissionsMigrationRemark, func() error {
		return sqls.WithTransaction(func(ctx *sqls.TxContext) error {
			return syncBillingPermissions(ctx.Tx)
		})
	})
}

func syncBillingPermissions(tx *gorm.DB) error {
	permissions, err := ensurePermissions(tx)
	if err != nil {
		return err
	}
	roles, err := ensureRoles(tx)
	if err != nil {
		return err
	}
	return ensureRolePermissions(tx, roles, permissions)
}
