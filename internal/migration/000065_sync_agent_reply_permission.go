package migration

import (
	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

func init() {
	register(65, "sync customer service reply and dispatch permissions", func() error {
		return sqls.WithTransaction(func(ctx *sqls.TxContext) error {
			return syncAgentReplyPermission(ctx.Tx)
		})
	})
}

func syncAgentReplyPermission(tx *gorm.DB) error {
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
