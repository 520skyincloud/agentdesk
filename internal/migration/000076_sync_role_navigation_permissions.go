package migration

import (
	"errors"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

const syncRoleNavigationPermissionsMigrationRemark = "sync role navigation view permissions"

var roleNavigationPermissionSpecs = []constants.Permission{
	constants.PermissionBillingView,
	constants.PermissionStoreWorkbenchView,
	constants.PermissionStoreView,
	constants.PermissionArrivalConnectionView,
	constants.PermissionArrivalAuditView,
}

func init() {
	register(76, syncRoleNavigationPermissionsMigrationRemark, func() error {
		return syncRoleNavigationPermissions(sqls.DB())
	})
}

func syncRoleNavigationPermissions(db *gorm.DB) error {
	if db == nil {
		return errors.New("role navigation permission migration database is nil")
	}
	return db.Transaction(func(tx *gorm.DB) error {
		permissions, err := ensurePermissionSpecs(tx, roleNavigationPermissionSpecs)
		if err != nil {
			return err
		}
		permissionCodes := make(map[string]struct{}, len(roleNavigationPermissionSpecs))
		for _, permission := range roleNavigationPermissionSpecs {
			permissionCodes[permission.Code] = struct{}{}
		}
		roles := make(map[string]*models.Role)
		for roleCode, rolePermissions := range constants.RolePermissions {
			for _, permission := range rolePermissions {
				if _, ok := permissionCodes[permission.Code]; !ok {
					continue
				}
				role := repositories.RoleRepository.GetByCode(tx, roleCode)
				if role == nil {
					return errors.New("builtin role not found: " + roleCode)
				}
				roles[roleCode] = role
				break
			}
		}
		return ensureRolePermissionsByCode(tx, roles, permissions, permissionCodes)
	})
}
