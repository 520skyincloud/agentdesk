package migration

import (
	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

func init() {
	register(60, "sync service analytics and human quality permissions", func() error {
		return sqls.WithTransaction(func(ctx *sqls.TxContext) error {
			return syncServiceAnalyticsPermissions(ctx.Tx)
		})
	})
}

func syncServiceAnalyticsPermissions(tx *gorm.DB) error {
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
