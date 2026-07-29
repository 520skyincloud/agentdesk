package migration

import (
	"errors"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

const syncWxWorkProtocolRuntimePermissionsMigrationRemark = "sync wxwork protocol runtime permissions"

var wxWorkProtocolRuntimePermissionSpecs = []constants.Permission{
	constants.PermissionWxWorkDevicePoolAdopt,
	constants.PermissionWxWorkDevicePoolRepair,
}

func init() {
	register(71, syncWxWorkProtocolRuntimePermissionsMigrationRemark, func() error {
		return syncWxWorkProtocolRuntimePermissions(sqls.DB())
	})
}

func syncWxWorkProtocolRuntimePermissions(db *gorm.DB) error {
	if db == nil {
		return errors.New("wxwork protocol runtime permission migration database is nil")
	}
	return db.Transaction(func(tx *gorm.DB) error {
		permissions, err := ensurePermissionSpecs(tx, wxWorkProtocolRuntimePermissionSpecs)
		if err != nil {
			return err
		}
		permissionCodes := make(map[string]struct{}, len(wxWorkProtocolRuntimePermissionSpecs))
		for _, permission := range wxWorkProtocolRuntimePermissionSpecs {
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
