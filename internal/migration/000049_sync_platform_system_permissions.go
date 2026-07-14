package migration

import (
	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

func init() {
	register(49, "sync platform system permissions", func() error {
		return sqls.WithTransaction(func(ctx *sqls.TxContext) error {
			return syncPlatformSystemPermissions(ctx.Tx)
		})
	})
}

func syncPlatformSystemPermissions(tx *gorm.DB) error {
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
