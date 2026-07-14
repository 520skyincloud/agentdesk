package migration

import (
	"errors"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

func init() {
	register(56, "restrict store staff role to workbench", func() error {
		return sqls.WithTransaction(func(ctx *sqls.TxContext) error {
			return restrictStoreStaffRoleToWorkbench(ctx.Tx)
		})
	})
}

func restrictStoreStaffRoleToWorkbench(tx *gorm.DB) error {
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
	role := roles[constants.RoleCodeStoreStaff]
	if role == nil {
		return errors.New("builtin store staff role not found")
	}
	allowedIDs := make([]int64, 0, len(constants.RolePermissions[constants.RoleCodeStoreStaff]))
	for _, spec := range constants.RolePermissions[constants.RoleCodeStoreStaff] {
		permission := permissions[spec.Code]
		if permission == nil {
			return errors.New("builtin store workbench permission not found: " + spec.Code)
		}
		allowedIDs = append(allowedIDs, permission.ID)
	}
	return tx.Where("role_id = ? AND permission_id NOT IN ?", role.ID, allowedIDs).
		Delete(&models.RolePermission{}).Error
}
