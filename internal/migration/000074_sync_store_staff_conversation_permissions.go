package migration

import (
	"errors"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

const syncStoreStaffConversationPermissionsMigrationRemark = "sync store staff conversation workspace permissions"

var storeStaffConversationPermissionSpecs = []constants.Permission{
	constants.PermissionConversationView,
	constants.PermissionConversationSend,
}

func init() {
	register(74, syncStoreStaffConversationPermissionsMigrationRemark, func() error {
		return syncStoreStaffConversationPermissions(sqls.DB())
	})
}

func syncStoreStaffConversationPermissions(db *gorm.DB) error {
	if db == nil {
		return errors.New("store staff conversation permission migration database is nil")
	}
	return db.Transaction(func(tx *gorm.DB) error {
		permissions, err := ensurePermissionSpecs(tx, storeStaffConversationPermissionSpecs)
		if err != nil {
			return err
		}
		role := repositories.RoleRepository.GetByCode(tx, constants.RoleCodeStoreStaff)
		if role == nil {
			return errors.New("builtin role not found: " + constants.RoleCodeStoreStaff)
		}
		now := time.Now()
		for _, permissionSpec := range storeStaffConversationPermissionSpecs {
			permission := permissions[permissionSpec.Code]
			if permission == nil {
				return errors.New("builtin permission not found: " + permissionSpec.Code)
			}
			if repositories.RolePermissionRepository.FindOne(tx, sqls.NewCnd().
				Eq("role_id", role.ID).
				Eq("permission_id", permission.ID)) != nil {
				continue
			}
			if err := repositories.RolePermissionRepository.Create(tx, &models.RolePermission{
				RoleID:       role.ID,
				PermissionID: permission.ID,
				AuditFields: models.AuditFields{
					CreatedAt:      now,
					CreateUserID:   constants.SystemAuditUserID,
					CreateUserName: constants.SystemAuditUserName,
					UpdatedAt:      now,
					UpdateUserID:   constants.SystemAuditUserID,
					UpdateUserName: constants.SystemAuditUserName,
				},
			}); err != nil {
				return err
			}
		}
		return nil
	})
}
