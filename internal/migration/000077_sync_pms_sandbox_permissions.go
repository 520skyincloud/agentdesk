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

func init() {
	register(77, "add scoped test PMS permissions", func() error {
		return sqls.WithTransaction(func(tx *sqls.TxContext) error {
			return syncPMSSandboxPermissions(tx.Tx)
		})
	})
}

// Only add the new permissions. Do not reapply unrelated historical role grants.
func syncPMSSandboxPermissions(db *gorm.DB) error {
	now := time.Now()
	audit := models.AuditFields{
		CreatedAt: now, UpdatedAt: now,
		CreateUserID: constants.SystemAuditUserID, CreateUserName: constants.SystemAuditUserName,
		UpdateUserID: constants.SystemAuditUserID, UpdateUserName: constants.SystemAuditUserName,
	}
	specs := []constants.Permission{
		constants.PermissionPMSSandboxView,
		constants.PermissionPMSSandboxManage,
		constants.PermissionPMSSandboxExecute,
	}
	for _, spec := range specs {
		permission := repositories.PermissionRepository.FindOne(db, sqls.NewCnd().Eq("code", spec.Code))
		if permission == nil {
			permission = &models.Permission{
				Name: spec.Name, Code: spec.Code, Type: spec.Type, GroupName: spec.GroupName,
				Method: spec.Method, APIPath: spec.APIPath, SortNo: spec.SortNo,
				Status: enums.StatusOk, IsBuiltin: true, AuditFields: audit,
			}
			if err := repositories.PermissionRepository.Create(db, permission); err != nil {
				return err
			}
		}
		for _, code := range []string{constants.RoleCodeSuperAdmin, constants.RoleCodeAdmin, constants.RoleCodeCsTeamLeader, constants.RoleCodeStoreStaff} {
			role := repositories.RoleRepository.GetByCode(db, code)
			if role == nil {
				continue
			}
			if repositories.RolePermissionRepository.FindOne(db, sqls.NewCnd().Eq("role_id", role.ID).Eq("permission_id", permission.ID)) != nil {
				continue
			}
			if err := repositories.RolePermissionRepository.Create(db, &models.RolePermission{
				RoleID: role.ID, PermissionID: permission.ID, AuditFields: audit,
			}); err != nil {
				return err
			}
		}
	}
	return nil
}
