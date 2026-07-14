package migration

import (
	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

func init() {
	register(54, "sync dashboard overview permission", func() error {
		return sqls.WithTransaction(func(ctx *sqls.TxContext) error {
			return syncDashboardOverviewPermission(ctx.Tx)
		})
	})
}

func syncDashboardOverviewPermission(tx *gorm.DB) error {
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
