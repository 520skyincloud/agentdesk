package migration

import (
	"errors"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

const syncArrivalPermissionsMigrationRemark = "sync arrival linking permissions"

var arrivalPermissionSpecs = []constants.Permission{
	constants.PermissionArrivalConnectionView,
	constants.PermissionArrivalConnectionManage,
	constants.PermissionArrivalConnectionInvite,
	constants.PermissionArrivalAuditView,
}

func init() {
	register(70, syncArrivalPermissionsMigrationRemark, func() error {
		return syncArrivalPermissions(sqls.DB())
	})
}

func syncArrivalPermissions(db *gorm.DB) error {
	if db == nil {
		return errors.New("arrival permission migration database is nil")
	}
	return db.Transaction(func(tx *gorm.DB) error {
		permissions, err := ensurePermissionSpecs(tx, arrivalPermissionSpecs)
		if err != nil {
			return err
		}
		permissionCodes := make(map[string]struct{}, len(arrivalPermissionSpecs))
		for _, permission := range arrivalPermissionSpecs {
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
