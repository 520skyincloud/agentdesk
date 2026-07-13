package migration

import (
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/repositories"
	"fmt"
	"log/slog"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

func init() {
	register(34, "sync tenant auth foundation", func() error {
		return sqls.WithTransaction(func(ctx *sqls.TxContext) error {
			return syncTenantAuthFoundation(ctx.Tx)
		})
	})
}

func syncTenantAuthFoundation(tx *gorm.DB) error {
	var overrideCount int64
	if tx.Migrator().HasTable("t_user_permission") {
		if err := tx.Table("t_user_permission").Count(&overrideCount).Error; err != nil {
			return err
		}
		if overrideCount > 0 {
			slog.Error("legacy account permission overrides require manual migration",
				"user_permission_override_count", overrideCount,
			)
			return fmt.Errorf("found %d legacy account permission overrides; migrate them to roles before retrying migration 34", overrideCount)
		}
	}

	permissions, err := ensurePermissions(tx)
	if err != nil {
		return err
	}
	roles, err := ensureRoles(tx)
	if err != nil {
		return err
	}
	if err = ensureRolePermissions(tx, roles, permissions); err != nil {
		return err
	}

	legacyPermission := repositories.PermissionRepository.FindOne(tx, sqls.NewCnd().Eq("code", "permission.sync"))
	if legacyPermission != nil {
		if err = tx.Where("permission_id = ?", legacyPermission.ID).Delete(&models.RolePermission{}).Error; err != nil {
			return err
		}
		if err = tx.Delete(&models.Permission{}, "id = ?", legacyPermission.ID).Error; err != nil {
			return err
		}
	}

	slog.Info("tenant auth foundation synchronized",
		"tenant_admin_role_id", roles[constants.RoleCodeTenantAdmin].ID,
		"legacy_user_permission_overrides", overrideCount,
	)
	return nil
}
