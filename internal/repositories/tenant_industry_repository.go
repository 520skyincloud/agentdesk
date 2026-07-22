package repositories

import (
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"

	"gorm.io/gorm"
)

var IndustryTagDefinitionRepository = &industryTagDefinitionRepository{}
var TenantCustomerTagPolicyRepository = &tenantCustomerTagPolicyRepository{}
var TenantIndustryChangeLogRepository = &tenantIndustryChangeLogRepository{}
var CustomerTagRelationRepository = &customerTagRelationRepository{}
var CustomerTagChangeLogRepository = &customerTagChangeLogRepository{}

type industryTagDefinitionRepository struct{}

func (r *industryTagDefinitionRepository) CountByProfile(db *gorm.DB, profileID int64) (int64, error) {
	var count int64
	err := db.Model(&models.IndustryTagDefinition{}).
		Where("intent_profile_id = ?", profileID).
		Count(&count).Error
	return count, err
}

func (r *industryTagDefinitionRepository) FindActiveByProfile(db *gorm.DB, profileID int64) ([]models.IndustryTagDefinition, error) {
	ret := make([]models.IndustryTagDefinition, 0)
	err := db.Where("intent_profile_id = ? AND status = ?", profileID, enums.StatusOk).
		Order("parent_id ASC, sort_no ASC, id ASC").Find(&ret).Error
	return ret, err
}

func (r *industryTagDefinitionRepository) TakeBySemanticKey(db *gorm.DB, profileID int64, semanticKey string) *models.IndustryTagDefinition {
	ret := &models.IndustryTagDefinition{}
	if err := db.Take(ret, "intent_profile_id = ? AND semantic_key = ?", profileID, semanticKey).Error; err != nil {
		return nil
	}
	return ret
}

func (r *industryTagDefinitionRepository) Create(db *gorm.DB, item *models.IndustryTagDefinition) error {
	return db.Create(item).Error
}

func (r *industryTagDefinitionRepository) Updates(db *gorm.DB, id int64, columns map[string]any) error {
	return db.Model(&models.IndustryTagDefinition{}).Where("id = ?", id).Updates(columns).Error
}

func (r *industryTagDefinitionRepository) UpdateRevisionByProfile(db *gorm.DB, profileID, revision int64, updatedAt time.Time, operatorID int64, operatorName string) error {
	return db.Model(&models.IndustryTagDefinition{}).
		Where("intent_profile_id = ?", profileID).
		Updates(map[string]any{
			"definition_revision": revision,
			"update_user_id":      operatorID,
			"update_user_name":    operatorName,
			"updated_at":          updatedAt,
		}).Error
}

type tenantCustomerTagPolicyRepository struct{}

func (r *tenantCustomerTagPolicyRepository) GetByTenant(db *gorm.DB, tenantID int64) *models.TenantCustomerTagPolicy {
	ret := &models.TenantCustomerTagPolicy{}
	if err := db.Take(ret, "tenant_id = ?", tenantID).Error; err != nil {
		return nil
	}
	return ret
}

func (r *tenantCustomerTagPolicyRepository) Create(db *gorm.DB, item *models.TenantCustomerTagPolicy) error {
	return db.Create(item).Error
}

func (r *tenantCustomerTagPolicyRepository) UpdatesByTenant(db *gorm.DB, tenantID int64, columns map[string]any) error {
	return db.Model(&models.TenantCustomerTagPolicy{}).Where("tenant_id = ?", tenantID).Updates(columns).Error
}

type tenantIndustryChangeLogRepository struct{}

func (r *tenantIndustryChangeLogRepository) Create(db *gorm.DB, item *models.TenantIndustryChangeLog) error {
	return db.Create(item).Error
}

type customerTagRelationRepository struct{}

func (r *customerTagRelationRepository) FindActiveByTenantAndTagIDs(db *gorm.DB, tenantID int64, tagIDs []int64) ([]models.CustomerTagRelation, error) {
	ret := make([]models.CustomerTagRelation, 0)
	if len(tagIDs) == 0 {
		return ret, nil
	}
	err := db.Where("tenant_id = ? AND tag_id IN ? AND relation_status = ?", tenantID, tagIDs, "active").
		Order("id ASC").Find(&ret).Error
	return ret, err
}

func (r *customerTagRelationRepository) Inactivate(db *gorm.DB, id, tenantID int64, columns map[string]any) error {
	return db.Model(&models.CustomerTagRelation{}).
		Where("id = ? AND tenant_id = ? AND relation_status = ?", id, tenantID, "active").
		Updates(columns).Error
}

type customerTagChangeLogRepository struct{}

func (r *customerTagChangeLogRepository) Create(db *gorm.DB, item *models.CustomerTagChangeLog) error {
	return db.Create(item).Error
}
