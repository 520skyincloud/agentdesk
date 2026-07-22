package repositories

import (
	"errors"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"

	"gorm.io/gorm"
)

var FastGPTRemoteResourceRetirementRepository = newFastGPTRemoteResourceRetirementRepository()

type fastGPTRemoteResourceRetirementRepository struct{}

func newFastGPTRemoteResourceRetirementRepository() *fastGPTRemoteResourceRetirementRepository {
	return &fastGPTRemoteResourceRetirementRepository{}
}

func (r *fastGPTRemoteResourceRetirementRepository) GetByLegacyDatasetInStore(db *gorm.DB, tenantID, storeID int64, datasetID string) *models.FastGPTRemoteResourceRetirement {
	if db == nil || tenantID <= 0 || storeID <= 0 || datasetID == "" {
		return nil
	}
	item := &models.FastGPTRemoteResourceRetirement{}
	if err := db.Take(item, "tenant_id = ? AND store_id = ? AND legacy_dataset_id = ?", tenantID, storeID, datasetID).Error; err != nil {
		return nil
	}
	return item
}

func (r *fastGPTRemoteResourceRetirementRepository) GetAwaitingByLegacyKnowledgeBase(db *gorm.DB, tenantID, storeID, knowledgeBaseID int64) *models.FastGPTRemoteResourceRetirement {
	if db == nil || tenantID <= 0 || storeID <= 0 || knowledgeBaseID <= 0 {
		return nil
	}
	item := &models.FastGPTRemoteResourceRetirement{}
	if err := db.Take(item,
		"tenant_id = ? AND store_id = ? AND legacy_knowledge_base_id = ? AND status = ?",
		tenantID, storeID, knowledgeBaseID, enums.FastGPTRemoteRetirementAwaitingReplacement,
	).Error; err != nil {
		return nil
	}
	return item
}

func (r *fastGPTRemoteResourceRetirementRepository) Create(db *gorm.DB, item *models.FastGPTRemoteResourceRetirement) error {
	if db == nil || item == nil || item.TenantID <= 0 || item.StoreID <= 0 || item.LegacyDatasetID == "" {
		return errors.New("FastGPT remote retirement scope is required")
	}
	return db.Create(item).Error
}

func (r *fastGPTRemoteResourceRetirementRepository) UpdatesInTenant(db *gorm.DB, id, tenantID int64, columns map[string]any) error {
	if db == nil || id <= 0 || tenantID <= 0 {
		return errors.New("FastGPT remote retirement scope is required")
	}
	return db.Model(&models.FastGPTRemoteResourceRetirement{}).Where("id = ? AND tenant_id = ?", id, tenantID).Updates(columns).Error
}
