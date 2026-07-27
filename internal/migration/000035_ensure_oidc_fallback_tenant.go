package migration

import (
	"fmt"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

const ensureOIDCFallbackTenantMigrationRemark = "ensure current OIDC fallback tenant"

func init() {
	register(35, ensureOIDCFallbackTenantMigrationRemark, func() error {
		return sqls.WithTransaction(func(ctx *sqls.TxContext) error {
			return ensureOIDCFallbackTenant(ctx.Tx)
		})
	})
}

func ensureOIDCFallbackTenant(tx *gorm.DB) error {
	if tx == nil {
		return fmt.Errorf("OIDC fallback tenant migration database is nil")
	}
	fallbackTenant := repositories.TenantRepository.GetByTenantCode(tx, constants.LegacyDefaultTenantCode)
	if fallbackTenant != nil {
		return nil
	}
	now := time.Now()
	fallbackTenant = &models.Tenant{
		TenantCode:         constants.LegacyDefaultTenantCode,
		LegalName:          "系统 OIDC 兜底公司",
		ShortName:          "OIDC 兜底",
		RegistrationType:   "system",
		RegistrationNo:     "SYSTEM-OIDC-FALLBACK",
		VerificationStatus: enums.TenantVerificationStatusVerified,
		VerifiedAt:         &now,
		VerifiedBy:         constants.SystemAuditUserID,
		Status:             enums.StatusOk,
		Remark:             "仅承载当前 OIDC 自动注册账号；认证归属重构后删除。",
		AuditFields: models.AuditFields{
			CreatedAt:      now,
			CreateUserID:   constants.SystemAuditUserID,
			CreateUserName: constants.SystemAuditUserName,
			UpdatedAt:      now,
			UpdateUserID:   constants.SystemAuditUserID,
			UpdateUserName: constants.SystemAuditUserName,
		},
	}
	return repositories.TenantRepository.Create(tx, fallbackTenant)
}
