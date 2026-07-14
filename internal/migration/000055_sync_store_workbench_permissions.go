package migration

import (
	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

func init() {
	register(55, "sync store workbench permissions", func() error {
		return sqls.WithTransaction(func(ctx *sqls.TxContext) error {
			return syncStoreWorkbenchPermissions(ctx.Tx)
		})
	})
}

func syncStoreWorkbenchPermissions(tx *gorm.DB) error {
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
