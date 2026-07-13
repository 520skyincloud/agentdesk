package migration

import (
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/repositories"
	"fmt"
	"time"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

func init() {
	register(35, "backfill tenant auth context", func() error {
		return sqls.WithTransaction(func(ctx *sqls.TxContext) error {
			return backfillTenantAuthContext(ctx.Tx)
		})
	})
}

func backfillTenantAuthContext(tx *gorm.DB) error {
	mixedScopeUserCount, err := countMixedScopeUsers(tx)
	if err != nil {
		return err
	}
	if mixedScopeUserCount > 0 {
		return fmt.Errorf("found %d users with both platform and tenant roles; resolve role scope conflicts before retrying migration 35", mixedScopeUserCount)
	}

	now := time.Now()
	legacyTenant := repositories.TenantRepository.GetByTenantCode(tx, constants.LegacyDefaultTenantCode)
	if legacyTenant == nil {
		legacyTenant = &models.Tenant{
			TenantCode:         constants.LegacyDefaultTenantCode,
			LegalName:          "历史默认公司",
			ShortName:          "默认公司",
			RegistrationType:   "legacy",
			RegistrationNo:     "LEGACY-DEFAULT",
			VerificationStatus: enums.TenantVerificationStatusVerified,
			VerifiedAt:         &now,
			Status:             enums.StatusOk,
			Remark:             "多租户升级时自动创建；历史业务数据在后续迁移阶段归入此公司。",
			AuditFields: models.AuditFields{
				CreatedAt:      now,
				CreateUserID:   constants.SystemAuditUserID,
				CreateUserName: constants.SystemAuditUserName,
				UpdatedAt:      now,
				UpdateUserID:   constants.SystemAuditUserID,
				UpdateUserName: constants.SystemAuditUserName,
			},
		}
		if err := repositories.TenantRepository.Create(tx, legacyTenant); err != nil {
			return err
		}
	}

	platformUserSubquery := tx.Table("t_user_role AS ur").
		Select("ur.user_id").
		Joins("JOIN t_role AS r ON r.id = ur.role_id").
		Where("r.scope = ? AND r.status = ?", constants.RoleScopePlatform, enums.StatusOk)
	if err := tx.Model(&models.User{}).
		Where("id IN (?)", platformUserSubquery).
		Where("tenant_id <> ? OR registration_source <> ? OR approval_status <> ? OR approved_at IS NULL",
			0, enums.UserRegistrationSourcePlatform, enums.UserApprovalStatusApproved).
		Updates(map[string]any{
			"tenant_id":           0,
			"registration_source": enums.UserRegistrationSourcePlatform,
			"approval_status":     enums.UserApprovalStatusApproved,
			"approved_at":         now,
			"approved_by":         constants.SystemAuditUserID,
			"updated_at":          now,
			"update_user_id":      constants.SystemAuditUserID,
			"update_user_name":    constants.SystemAuditUserName,
		}).Error; err != nil {
		return err
	}
	if err := tx.Model(&models.User{}).
		Where("id NOT IN (?)", platformUserSubquery).
		Where("tenant_id = ? AND approved_by = ?", 0, constants.SystemAuditUserID).
		Where("registration_source = ? OR registration_source = ''", enums.UserRegistrationSourcePlatform).
		Updates(map[string]any{
			"tenant_id":           legacyTenant.ID,
			"registration_source": enums.UserRegistrationSourceLegacyMigration,
			"approval_status":     enums.UserApprovalStatusApproved,
			"approved_at":         now,
			"approved_by":         constants.SystemAuditUserID,
			"updated_at":          now,
			"update_user_id":      constants.SystemAuditUserID,
			"update_user_name":    constants.SystemAuditUserName,
		}).Error; err != nil {
		return err
	}
	return nil
}

func countMixedScopeUsers(tx *gorm.DB) (int64, error) {
	var count int64
	err := tx.Table("t_user_role AS ur").
		Joins("JOIN t_role AS r ON r.id = ur.role_id AND r.status = ?", enums.StatusOk).
		Group("ur.user_id").
		Having("COUNT(DISTINCT r.scope) > 1").
		Count(&count).Error
	return count, err
}
