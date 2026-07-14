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
	register(43, "backfill customer domain tenants", func() error {
		return sqls.WithTransaction(func(ctx *sqls.TxContext) error {
			return backfillCustomerDomainTenants(ctx.Tx)
		})
	})
}

func backfillCustomerDomainTenants(tx *gorm.DB) error {
	legacyTenant := repositories.TenantRepository.GetByTenantCode(tx, constants.LegacyDefaultTenantCode)
	if legacyTenant == nil {
		return fmt.Errorf("legacy default tenant is required before customer tenant backfill")
	}
	validTenantIDs, err := loadValidTenantIDs(tx)
	if err != nil {
		return err
	}
	companyTenants, err := loadResourceTenantIDs[models.Company](tx)
	if err != nil {
		return err
	}
	channelTenants, err := loadResourceTenantIDs[models.Channel](tx)
	if err != nil {
		return err
	}
	conversationChannels, err := loadCustomerConversationChannels(tx)
	if err != nil {
		return err
	}

	var customers []models.Customer
	if err := tx.Order("id ASC").Find(&customers).Error; err != nil {
		return err
	}
	customerTenants := make(map[int64]int64, len(customers))
	for i := range customers {
		tenantID, err := resolveCustomerTenant(customers[i], legacyTenant.ID, validTenantIDs, companyTenants, channelTenants, conversationChannels[customers[i].ID])
		if err != nil {
			return err
		}
		if err := assignCustomerTenant(tx, customers[i].ID, customers[i].TenantID, tenantID); err != nil {
			return err
		}
		customerTenants[customers[i].ID] = tenantID
	}

	if err := backfillCustomerIdentityTenants(tx, customerTenants, validTenantIDs); err != nil {
		return err
	}
	if err := backfillCustomerContactTenants(tx, customerTenants, validTenantIDs); err != nil {
		return err
	}
	return backfillStoreCustomerRelationTenants(tx, customerTenants, validTenantIDs)
}

func loadValidTenantIDs(tx *gorm.DB) (map[int64]struct{}, error) {
	var tenants []models.Tenant
	if err := tx.Find(&tenants).Error; err != nil {
		return nil, err
	}
	result := make(map[int64]struct{}, len(tenants))
	for i := range tenants {
		result[tenants[i].ID] = struct{}{}
	}
	return result, nil
}

func loadResourceTenantIDs[T models.Company | models.Channel](tx *gorm.DB) (map[int64]int64, error) {
	var rows []struct {
		ID       int64
		TenantID int64
	}
	var model T
	if err := tx.Model(&model).Select("id", "tenant_id").Find(&rows).Error; err != nil {
		return nil, err
	}
	result := make(map[int64]int64, len(rows))
	for i := range rows {
		result[rows[i].ID] = rows[i].TenantID
	}
	return result, nil
}

func loadCustomerConversationChannels(tx *gorm.DB) (map[int64][]int64, error) {
	var rows []struct {
		CustomerID int64
		ChannelID  int64
	}
	if err := tx.Model(&models.Conversation{}).
		Select("customer_id", "channel_id").
		Where("customer_id > ? AND channel_id > ?", 0, 0).
		Group("customer_id, channel_id").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	result := make(map[int64][]int64)
	for i := range rows {
		result[rows[i].CustomerID] = append(result[rows[i].CustomerID], rows[i].ChannelID)
	}
	return result, nil
}

func resolveCustomerTenant(
	customer models.Customer,
	legacyTenantID int64,
	validTenantIDs map[int64]struct{},
	companyTenants map[int64]int64,
	channelTenants map[int64]int64,
	conversationChannelIDs []int64,
) (int64, error) {
	resolved := customer.TenantID
	if resolved > 0 {
		if _, ok := validTenantIDs[resolved]; !ok {
			return 0, fmt.Errorf("customer %d references missing tenant %d", customer.ID, resolved)
		}
	}
	merge := func(source string, sourceID, tenantID int64) error {
		if tenantID <= 0 {
			return fmt.Errorf("customer %d %s %d has no tenant", customer.ID, source, sourceID)
		}
		if _, ok := validTenantIDs[tenantID]; !ok {
			return fmt.Errorf("customer %d %s %d references missing tenant %d", customer.ID, source, sourceID, tenantID)
		}
		if resolved == 0 {
			resolved = tenantID
			return nil
		}
		if resolved != tenantID {
			return fmt.Errorf("customer %d tenant %d conflicts with %s %d tenant %d", customer.ID, resolved, source, sourceID, tenantID)
		}
		return nil
	}
	if customer.CompanyID > 0 {
		tenantID, ok := companyTenants[customer.CompanyID]
		if !ok {
			return 0, fmt.Errorf("customer %d references missing company %d", customer.ID, customer.CompanyID)
		}
		if err := merge("company", customer.CompanyID, tenantID); err != nil {
			return 0, err
		}
	}
	for _, channelID := range conversationChannelIDs {
		tenantID, ok := channelTenants[channelID]
		if !ok {
			return 0, fmt.Errorf("customer %d conversation references missing channel %d", customer.ID, channelID)
		}
		if err := merge("channel", channelID, tenantID); err != nil {
			return 0, err
		}
	}
	if resolved == 0 {
		resolved = legacyTenantID
	}
	return resolved, nil
}

func assignCustomerTenant(tx *gorm.DB, customerID, currentTenantID, tenantID int64) error {
	if currentTenantID == tenantID {
		return nil
	}
	result := tx.Model(&models.Customer{}).Where("id = ? AND tenant_id = ?", customerID, currentTenantID).Updates(customerTenantBackfillColumns(tenantID))
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return fmt.Errorf("customer %d tenant backfill did not update the expected row", customerID)
	}
	return nil
}

func backfillCustomerIdentityTenants(tx *gorm.DB, customerTenants map[int64]int64, validTenantIDs map[int64]struct{}) error {
	var list []models.CustomerIdentity
	if err := tx.Order("id ASC").Find(&list).Error; err != nil {
		return err
	}
	for i := range list {
		if err := backfillCustomerChildTenant(tx, &models.CustomerIdentity{}, "customer identity", list[i].ID, list[i].CustomerID, list[i].TenantID, customerTenants, validTenantIDs); err != nil {
			return err
		}
	}
	return nil
}

func backfillCustomerContactTenants(tx *gorm.DB, customerTenants map[int64]int64, validTenantIDs map[int64]struct{}) error {
	var list []models.CustomerContact
	if err := tx.Order("id ASC").Find(&list).Error; err != nil {
		return err
	}
	for i := range list {
		if err := backfillCustomerChildTenant(tx, &models.CustomerContact{}, "customer contact", list[i].ID, list[i].CustomerID, list[i].TenantID, customerTenants, validTenantIDs); err != nil {
			return err
		}
	}
	return nil
}

func backfillStoreCustomerRelationTenants(tx *gorm.DB, customerTenants map[int64]int64, validTenantIDs map[int64]struct{}) error {
	var list []models.StoreCustomerRelation
	if err := tx.Order("id ASC").Find(&list).Error; err != nil {
		return err
	}
	for i := range list {
		if err := backfillCustomerChildTenant(tx, &models.StoreCustomerRelation{}, "store customer relation", list[i].ID, list[i].CustomerID, list[i].TenantID, customerTenants, validTenantIDs); err != nil {
			return err
		}
	}
	return nil
}

func backfillCustomerChildTenant(
	tx *gorm.DB,
	model any,
	resource string,
	id, customerID, currentTenantID int64,
	customerTenants map[int64]int64,
	validTenantIDs map[int64]struct{},
) error {
	expectedTenantID, ok := customerTenants[customerID]
	if !ok {
		return fmt.Errorf("%s %d references missing customer %d", resource, id, customerID)
	}
	if currentTenantID > 0 {
		if _, ok := validTenantIDs[currentTenantID]; !ok {
			return fmt.Errorf("%s %d references missing tenant %d", resource, id, currentTenantID)
		}
		if currentTenantID != expectedTenantID {
			return fmt.Errorf("%s %d tenant %d conflicts with customer %d tenant %d", resource, id, currentTenantID, customerID, expectedTenantID)
		}
		return nil
	}
	result := tx.Model(model).Where("id = ? AND tenant_id = ?", id, 0).Updates(customerTenantBackfillColumns(expectedTenantID))
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return fmt.Errorf("%s %d tenant backfill did not update the expected row", resource, id)
	}
	return nil
}

func customerTenantBackfillColumns(tenantID int64) map[string]any {
	return map[string]any{
		"tenant_id":        tenantID,
		"update_user_id":   constants.SystemAuditUserID,
		"update_user_name": constants.SystemAuditUserName,
		"updated_at":       time.Now(),
	}
}
