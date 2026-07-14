package migration

import (
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

func init() {
	register(53, "remove tenant role platform permissions", func() error {
		return sqls.WithTransaction(func(ctx *sqls.TxContext) error {
			return removeTenantRolePlatformPermissions(ctx.Tx)
		})
	})
}

func removeTenantRolePlatformPermissions(tx *gorm.DB) error {
	permissions, err := ensurePermissions(tx)
	if err != nil {
		return err
	}
	roles, err := ensureRoles(tx)
	if err != nil {
		return err
	}
	if err := ensureRolePermissions(tx, roles, permissions); err != nil {
		return err
	}

	var tenantRoleIDs []int64
	if err := tx.Model(&models.Role{}).
		Where("scope <> ?", constants.RoleScopePlatform).
		Pluck("id", &tenantRoleIDs).Error; err != nil {
		return err
	}
	if len(tenantRoleIDs) == 0 {
		return nil
	}

	var platformPermissionIDs []int64
	if err := tx.Model(&models.Permission{}).
		Where("scope = ?", constants.PermissionScopePlatform).
		Pluck("id", &platformPermissionIDs).Error; err != nil {
		return err
	}
	if len(platformPermissionIDs) == 0 {
		return nil
	}
	return tx.Where("role_id IN ? AND permission_id IN ?", tenantRoleIDs, platformPermissionIDs).
		Delete(&models.RolePermission{}).Error
}
