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
	register(44, "backfill store and wxwork tenants", func() error {
		return sqls.WithTransaction(func(ctx *sqls.TxContext) error {
			return backfillStoreAndWxWorkTenants(ctx.Tx)
		})
	})
}

type storeTenantEvidence struct {
	source   string
	sourceID int64
	tenantID int64
}

type storeTenantResolver struct {
	resource       string
	resourceID     int64
	validTenantIDs map[int64]struct{}
	tenantID       int64
}

func backfillStoreAndWxWorkTenants(tx *gorm.DB) error {
	legacyTenant := repositories.TenantRepository.GetByTenantCode(tx, constants.LegacyDefaultTenantCode)
	if legacyTenant == nil {
		return fmt.Errorf("legacy default tenant is required before store tenant backfill")
	}
	validTenantIDs, err := loadValidTenantIDs(tx)
	if err != nil {
		return err
	}
	companyTenants, err := loadStoreTenantResourceTenants(tx, &models.Company{})
	if err != nil {
		return err
	}
	channelTenants, err := loadStoreTenantResourceTenants(tx, &models.Channel{})
	if err != nil {
		return err
	}
	userTenants, err := loadStoreTenantResourceTenants(tx, &models.User{})
	if err != nil {
		return err
	}
	teamTenants, err := loadStoreTenantResourceTenants(tx, &models.AgentTeam{})
	if err != nil {
		return err
	}
	customerTenants, err := loadStoreTenantResourceTenants(tx, &models.Customer{})
	if err != nil {
		return err
	}

	var stores []models.Store
	if err := tx.Order("id ASC").Find(&stores).Error; err != nil {
		return err
	}
	var bindings []models.StoreStaffBinding
	if err := tx.Order("id ASC").Find(&bindings).Error; err != nil {
		return err
	}
	var instances []models.WxWorkProtocolInstance
	if err := tx.Order("id ASC").Find(&instances).Error; err != nil {
		return err
	}
	var relations []models.StoreCustomerRelation
	if err := tx.Order("id ASC").Find(&relations).Error; err != nil {
		return err
	}

	storeEvidence, err := collectStoreTenantEvidence(
		bindings,
		instances,
		relations,
		companyTenants,
		channelTenants,
		userTenants,
		teamTenants,
		customerTenants,
	)
	if err != nil {
		return err
	}
	storeTenants := make(map[int64]int64, len(stores))
	for i := range stores {
		item := &stores[i]
		resolver := newStoreTenantResolver("store", item.ID, item.TenantID, validTenantIDs)
		if item.CompanyID > 0 {
			if err := resolver.mergeReference("company", item.CompanyID, companyTenants); err != nil {
				return err
			}
		}
		for _, evidence := range storeEvidence[item.ID] {
			if err := resolver.merge(evidence.source, evidence.sourceID, evidence.tenantID); err != nil {
				return err
			}
		}
		tenantID, err := resolver.resolve(legacyTenant.ID)
		if err != nil {
			return err
		}
		if err := assignStoreDomainTenant(tx, &models.Store{}, "store", item.ID, item.TenantID, tenantID); err != nil {
			return err
		}
		storeTenants[item.ID] = tenantID
	}
	for storeID := range storeEvidence {
		if _, ok := storeTenants[storeID]; !ok {
			return fmt.Errorf("store tenant evidence references missing store %d", storeID)
		}
	}

	bindingTenants := make(map[int64]int64, len(bindings))
	for i := range bindings {
		item := &bindings[i]
		resolver := newStoreTenantResolver("store staff binding", item.ID, item.TenantID, validTenantIDs)
		if item.StoreID > 0 {
			if err := resolver.mergeReference("store", item.StoreID, storeTenants); err != nil {
				return err
			}
		}
		if item.UserID > 0 {
			if err := resolver.mergeReference("user", item.UserID, userTenants); err != nil {
				return err
			}
		}
		if item.AgentTeamID > 0 {
			if err := resolver.mergeReference("agent team", item.AgentTeamID, teamTenants); err != nil {
				return err
			}
		}
		if item.CompanyID > 0 {
			if err := resolver.mergeReference("company", item.CompanyID, companyTenants); err != nil {
				return err
			}
		}
		tenantID, err := resolver.resolve(legacyTenant.ID)
		if err != nil {
			return err
		}
		if err := assignStoreDomainTenant(tx, &models.StoreStaffBinding{}, "store staff binding", item.ID, item.TenantID, tenantID); err != nil {
			return err
		}
		bindingTenants[item.ID] = tenantID
	}

	for i := range instances {
		item := &instances[i]
		resolver := newStoreTenantResolver("wxwork protocol instance", item.ID, item.TenantID, validTenantIDs)
		if item.ChannelID > 0 {
			if err := resolver.mergeReference("channel", item.ChannelID, channelTenants); err != nil {
				return err
			}
		}
		if item.StoreID > 0 {
			if err := resolver.mergeReference("store", item.StoreID, storeTenants); err != nil {
				return err
			}
		}
		if item.StoreStaffBindingID > 0 {
			if err := resolver.mergeReference("store staff binding", item.StoreStaffBindingID, bindingTenants); err != nil {
				return err
			}
		}
		if item.AgentTeamID > 0 {
			if err := resolver.mergeReference("agent team", item.AgentTeamID, teamTenants); err != nil {
				return err
			}
		}
		if item.CompanyID > 0 {
			if err := resolver.mergeReference("company", item.CompanyID, companyTenants); err != nil {
				return err
			}
		}
		tenantID, err := resolver.resolve(legacyTenant.ID)
		if err != nil {
			return err
		}
		if err := assignStoreDomainTenant(tx, &models.WxWorkProtocolInstance{}, "wxwork protocol instance", item.ID, item.TenantID, tenantID); err != nil {
			return err
		}
	}
	return nil
}

func loadStoreTenantResourceTenants(tx *gorm.DB, model any) (map[int64]int64, error) {
	var rows []struct {
		ID       int64
		TenantID int64
	}
	if err := tx.Model(model).Select("id", "tenant_id").Find(&rows).Error; err != nil {
		return nil, err
	}
	result := make(map[int64]int64, len(rows))
	for i := range rows {
		result[rows[i].ID] = rows[i].TenantID
	}
	return result, nil
}

func collectStoreTenantEvidence(
	bindings []models.StoreStaffBinding,
	instances []models.WxWorkProtocolInstance,
	relations []models.StoreCustomerRelation,
	companyTenants map[int64]int64,
	channelTenants map[int64]int64,
	userTenants map[int64]int64,
	teamTenants map[int64]int64,
	customerTenants map[int64]int64,
) (map[int64][]storeTenantEvidence, error) {
	result := make(map[int64][]storeTenantEvidence)
	appendReference := func(storeID int64, source string, sourceID int64, tenantIDs map[int64]int64) error {
		if storeID <= 0 || sourceID <= 0 {
			return nil
		}
		tenantID, ok := tenantIDs[sourceID]
		if !ok {
			return fmt.Errorf("store %d %s evidence references missing %s %d", storeID, source, source, sourceID)
		}
		result[storeID] = append(result[storeID], storeTenantEvidence{source: source, sourceID: sourceID, tenantID: tenantID})
		return nil
	}
	for i := range bindings {
		item := &bindings[i]
		if item.StoreID <= 0 {
			continue
		}
		if item.TenantID > 0 {
			result[item.StoreID] = append(result[item.StoreID], storeTenantEvidence{source: "store staff binding", sourceID: item.ID, tenantID: item.TenantID})
		}
		if err := appendReference(item.StoreID, "user", item.UserID, userTenants); err != nil {
			return nil, err
		}
		if err := appendReference(item.StoreID, "agent team", item.AgentTeamID, teamTenants); err != nil {
			return nil, err
		}
		if err := appendReference(item.StoreID, "company", item.CompanyID, companyTenants); err != nil {
			return nil, err
		}
	}
	for i := range instances {
		item := &instances[i]
		if item.StoreID <= 0 {
			continue
		}
		if item.TenantID > 0 {
			result[item.StoreID] = append(result[item.StoreID], storeTenantEvidence{source: "wxwork protocol instance", sourceID: item.ID, tenantID: item.TenantID})
		}
		if err := appendReference(item.StoreID, "channel", item.ChannelID, channelTenants); err != nil {
			return nil, err
		}
		if err := appendReference(item.StoreID, "agent team", item.AgentTeamID, teamTenants); err != nil {
			return nil, err
		}
		if err := appendReference(item.StoreID, "company", item.CompanyID, companyTenants); err != nil {
			return nil, err
		}
	}
	for i := range relations {
		item := &relations[i]
		if item.StoreID <= 0 {
			continue
		}
		customerTenantID, ok := customerTenants[item.CustomerID]
		if !ok {
			return nil, fmt.Errorf("store customer relation %d references missing customer %d", item.ID, item.CustomerID)
		}
		if item.TenantID <= 0 || item.TenantID != customerTenantID {
			return nil, fmt.Errorf("store customer relation %d tenant %d conflicts with customer %d tenant %d", item.ID, item.TenantID, item.CustomerID, customerTenantID)
		}
		result[item.StoreID] = append(result[item.StoreID], storeTenantEvidence{source: "customer", sourceID: item.CustomerID, tenantID: customerTenantID})
	}
	return result, nil
}

func newStoreTenantResolver(resource string, resourceID, explicitTenantID int64, validTenantIDs map[int64]struct{}) *storeTenantResolver {
	return &storeTenantResolver{
		resource:       resource,
		resourceID:     resourceID,
		validTenantIDs: validTenantIDs,
		tenantID:       explicitTenantID,
	}
}

func (r *storeTenantResolver) mergeReference(source string, sourceID int64, tenantIDs map[int64]int64) error {
	tenantID, ok := tenantIDs[sourceID]
	if !ok {
		return fmt.Errorf("%s %d references missing %s %d", r.resource, r.resourceID, source, sourceID)
	}
	return r.merge(source, sourceID, tenantID)
}

func (r *storeTenantResolver) merge(source string, sourceID, tenantID int64) error {
	if tenantID <= 0 {
		return fmt.Errorf("%s %d %s %d has no tenant", r.resource, r.resourceID, source, sourceID)
	}
	if _, ok := r.validTenantIDs[tenantID]; !ok {
		return fmt.Errorf("%s %d %s %d references missing tenant %d", r.resource, r.resourceID, source, sourceID, tenantID)
	}
	if r.tenantID == 0 {
		r.tenantID = tenantID
		return nil
	}
	if _, ok := r.validTenantIDs[r.tenantID]; !ok {
		return fmt.Errorf("%s %d references missing tenant %d", r.resource, r.resourceID, r.tenantID)
	}
	if r.tenantID != tenantID {
		return fmt.Errorf("%s %d tenant %d conflicts with %s %d tenant %d", r.resource, r.resourceID, r.tenantID, source, sourceID, tenantID)
	}
	return nil
}

func (r *storeTenantResolver) resolve(defaultTenantID int64) (int64, error) {
	if r.tenantID == 0 {
		r.tenantID = defaultTenantID
	}
	if _, ok := r.validTenantIDs[r.tenantID]; !ok {
		return 0, fmt.Errorf("%s %d references missing tenant %d", r.resource, r.resourceID, r.tenantID)
	}
	return r.tenantID, nil
}

func assignStoreDomainTenant(tx *gorm.DB, model any, resource string, id, currentTenantID, tenantID int64) error {
	if currentTenantID == tenantID {
		return nil
	}
	result := tx.Model(model).Where("id = ? AND tenant_id = ?", id, currentTenantID).Updates(map[string]any{
		"tenant_id":        tenantID,
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
