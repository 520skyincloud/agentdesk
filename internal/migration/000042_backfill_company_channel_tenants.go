package migration

import (
	"fmt"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

func init() {
	register(42, "backfill company and channel tenants", func() error {
		return sqls.WithTransaction(func(ctx *sqls.TxContext) error {
			return backfillCompanyAndChannelTenants(ctx.Tx)
		})
	})
}

func backfillCompanyAndChannelTenants(tx *gorm.DB) error {
	legacyTenant := repositories.TenantRepository.GetByTenantCode(tx, constants.LegacyDefaultTenantCode)
	if legacyTenant == nil {
		return fmt.Errorf("legacy default tenant is required before company and channel tenant backfill")
	}
	var tenants []models.Tenant
	if err := tx.Find(&tenants).Error; err != nil {
		return err
	}
	validTenantIDs := make(map[int64]struct{}, len(tenants))
	for i := range tenants {
		validTenantIDs[tenants[i].ID] = struct{}{}
	}
	if err := backfillTenantOwnedCompanies(tx, legacyTenant.ID, validTenantIDs); err != nil {
		return err
	}
	return backfillTenantOwnedChannels(tx, legacyTenant.ID, validTenantIDs)
}

func backfillTenantOwnedCompanies(tx *gorm.DB, legacyTenantID int64, validTenantIDs map[int64]struct{}) error {
	var list []models.Company
	if err := tx.Order("id ASC").Find(&list).Error; err != nil {
		return err
	}
	for i := range list {
		if err := ensureTenantOwnedRecord(tx, &models.Company{}, "company", list[i].ID, list[i].TenantID, legacyTenantID, validTenantIDs); err != nil {
			return err
		}
	}
	return nil
}

func backfillTenantOwnedChannels(tx *gorm.DB, legacyTenantID int64, validTenantIDs map[int64]struct{}) error {
	var list []models.Channel
	if err := tx.Order("id ASC").Find(&list).Error; err != nil {
		return err
	}
	for i := range list {
		if err := ensureTenantOwnedRecord(tx, &models.Channel{}, "channel", list[i].ID, list[i].TenantID, legacyTenantID, validTenantIDs); err != nil {
			return err
		}
	}
	return nil
}

func ensureTenantOwnedRecord(tx *gorm.DB, model any, resource string, id, currentTenantID, legacyTenantID int64, validTenantIDs map[int64]struct{}) error {
	if currentTenantID > 0 {
		if _, ok := validTenantIDs[currentTenantID]; !ok {
			return fmt.Errorf("%s %d references missing tenant %d", resource, id, currentTenantID)
		}
		return nil
	}
	result := tx.Model(model).Where("id = ? AND tenant_id = ?", id, 0).Updates(map[string]any{
		"tenant_id":        legacyTenantID,
		"update_user_id":   constants.SystemAuditUserID,
		"update_user_name": constants.SystemAuditUserName,
		"updated_at":       time.Now(),
	})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return fmt.Errorf("%s %d tenant backfill did not update the expected row", resource, id)
	}
	return nil
}
