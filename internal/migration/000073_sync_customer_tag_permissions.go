package migration

import (
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

const syncCustomerTagPermissionsMigrationRemark = "replace legacy tag permissions with Store customer tag semantics"

func init() {
	register(73, syncCustomerTagPermissionsMigrationRemark, func() error {
		return syncCustomerTagPermissions(sqls.DB())
	})
}

func syncCustomerTagPermissions(db *gorm.DB) error {
	return db.Transaction(func(tx *gorm.DB) error {
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

		retiredNames := map[string]string{
			constants.PermissionTagCreate.Code: "已废弃：创建自定义标签",
			constants.PermissionTagDelete.Code: "已废弃：删除自定义标签",
		}
		retiredCodes := []string{constants.PermissionTagCreate.Code, constants.PermissionTagDelete.Code}
		var retired []models.Permission
		if err := tx.Where("code IN ?", retiredCodes).Find(&retired).Error; err != nil {
			return err
		}
		retiredIDs := make([]int64, 0, len(retired))
		now := time.Now()
		for i := range retired {
			retiredIDs = append(retiredIDs, retired[i].ID)
			if err := repositories.PermissionRepository.Updates(tx, retired[i].ID, map[string]any{
				"name":             retiredNames[retired[i].Code],
				"status":           enums.StatusDisabled,
				"update_user_id":   constants.SystemAuditUserID,
				"update_user_name": constants.SystemAuditUserName,
				"updated_at":       now,
			}); err != nil {
				return err
			}
		}
		if len(retiredIDs) > 0 {
			if err := tx.Where("permission_id IN ?", retiredIDs).Delete(&models.RolePermission{}).Error; err != nil {
				return err
			}
		}

		teamLeader := roles[constants.RoleCodeCsTeamLeader]
		tagUpdate := permissions[constants.PermissionTagUpdate.Code]
		if teamLeader != nil && tagUpdate != nil {
			if err := tx.Where("role_id = ? AND permission_id = ?", teamLeader.ID, tagUpdate.ID).
				Delete(&models.RolePermission{}).Error; err != nil {
				return err
			}
		}
		return nil
	})
}
