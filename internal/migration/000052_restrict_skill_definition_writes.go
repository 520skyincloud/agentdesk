package migration

import (
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

func init() {
	register(52, "restrict skill definition writes to platform", func() error {
		return sqls.WithTransaction(func(ctx *sqls.TxContext) error {
			return restrictSkillDefinitionWritesToPlatform(ctx.Tx)
		})
	})
}

func restrictSkillDefinitionWritesToPlatform(tx *gorm.DB) error {
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

	permissionIDs := make([]int64, 0, 3)
	for _, spec := range []constants.Permission{
		constants.PermissionSkillDefinitionCreate,
		constants.PermissionSkillDefinitionUpdate,
		constants.PermissionSkillDefinitionDelete,
	} {
		permissionIDs = append(permissionIDs, permissions[spec.Code].ID)
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
	return tx.Where("role_id IN ? AND permission_id IN ?", tenantRoleIDs, permissionIDs).
		Delete(&models.RolePermission{}).Error
}
