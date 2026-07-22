package repositories

import (
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var IndustryTagDefinitionRepository = &industryTagDefinitionRepository{}
var TenantCustomerTagPolicyRepository = &tenantCustomerTagPolicyRepository{}
var StoreCustomerTagRuntimePolicyRepository = &storeCustomerTagRuntimePolicyRepository{}
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

type storeCustomerTagRuntimePolicyRepository struct{}

func (r *storeCustomerTagRuntimePolicyRepository) GetByStore(db *gorm.DB, tenantID, storeID int64) (*models.StoreCustomerTagRuntimePolicy, error) {
	ret := &models.StoreCustomerTagRuntimePolicy{}
	err := db.Take(ret, "tenant_id = ? AND store_id = ?", tenantID, storeID).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return ret, nil
}

type tenantIndustryChangeLogRepository struct{}

func (r *tenantIndustryChangeLogRepository) Create(db *gorm.DB, item *models.TenantIndustryChangeLog) error {
	return db.Create(item).Error
}

type customerTagRelationRepository struct{}

func (r *customerTagRelationRepository) GetByStoreRelationAndTag(
	db *gorm.DB,
	tenantID, storeID, storeCustomerRelationID, tagID int64,
) (*models.CustomerTagRelation, error) {
	ret := &models.CustomerTagRelation{}
	err := db.Take(ret,
		"tenant_id = ? AND store_id = ? AND store_customer_relation_id = ? AND tag_id = ?",
		tenantID, storeID, storeCustomerRelationID, tagID,
	).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return ret, nil
}

func (r *customerTagRelationRepository) GetByStoreRelationAndTagForUpdate(
	db *gorm.DB,
	tenantID, storeID, storeCustomerRelationID, tagID int64,
) (*models.CustomerTagRelation, error) {
	ret := &models.CustomerTagRelation{}
	err := db.Clauses(clause.Locking{Strength: "UPDATE"}).Take(ret,
		"tenant_id = ? AND store_id = ? AND store_customer_relation_id = ? AND tag_id = ?",
		tenantID, storeID, storeCustomerRelationID, tagID,
	).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return ret, nil
}

func (r *customerTagRelationRepository) FindActiveByTenantAndTagIDs(db *gorm.DB, tenantID int64, tagIDs []int64) ([]models.CustomerTagRelation, error) {
	ret := make([]models.CustomerTagRelation, 0)
	if len(tagIDs) == 0 {
		return ret, nil
	}
	err := db.Where("tenant_id = ? AND tag_id IN ? AND relation_status = ?", tenantID, tagIDs, "active").
		Order("id ASC").Find(&ret).Error
	return ret, err
}

func (r *customerTagRelationRepository) FindActiveByStoreRelation(db *gorm.DB, tenantID, storeID, storeCustomerRelationID int64) ([]models.CustomerTagRelation, error) {
	ret := make([]models.CustomerTagRelation, 0)
	err := db.Where(
		"tenant_id = ? AND store_id = ? AND store_customer_relation_id = ? AND relation_status = ?",
		tenantID, storeID, storeCustomerRelationID, "active",
	).Order("id ASC").Find(&ret).Error
	return ret, err
}

func (r *customerTagRelationRepository) FindActiveByStoreRelationForUpdate(db *gorm.DB, tenantID, storeID, storeCustomerRelationID int64) ([]models.CustomerTagRelation, error) {
	ret := make([]models.CustomerTagRelation, 0)
	err := db.Clauses(clause.Locking{Strength: "UPDATE"}).Where(
		"tenant_id = ? AND store_id = ? AND store_customer_relation_id = ? AND relation_status = ?",
		tenantID, storeID, storeCustomerRelationID, "active",
	).Order("id ASC").Find(&ret).Error
	return ret, err
}

func (r *customerTagRelationRepository) FindActiveByStoreRelations(db *gorm.DB, tenantID int64, storeCustomerRelationIDs []int64) ([]models.CustomerTagRelation, error) {
	ret := make([]models.CustomerTagRelation, 0)
	if tenantID <= 0 || len(storeCustomerRelationIDs) == 0 {
		return ret, nil
	}
	err := db.Where("tenant_id = ? AND store_customer_relation_id IN ? AND relation_status = ?", tenantID, storeCustomerRelationIDs, "active").
		Order("store_customer_relation_id ASC, id ASC").Find(&ret).Error
	return ret, err
}

func (r *customerTagRelationRepository) CountActiveByStoreRelation(db *gorm.DB, tenantID, storeID, storeCustomerRelationID int64) (int64, error) {
	var count int64
	err := db.Model(&models.CustomerTagRelation{}).Where(
		"tenant_id = ? AND store_id = ? AND store_customer_relation_id = ? AND relation_status = ?",
		tenantID, storeID, storeCustomerRelationID, "active",
	).Count(&count).Error
	return count, err
}

func (r *customerTagRelationRepository) Create(db *gorm.DB, item *models.CustomerTagRelation) error {
	return db.Create(item).Error
}

func (r *customerTagRelationRepository) UpdatesInScope(
	db *gorm.DB,
	id, tenantID, storeID, storeCustomerRelationID int64,
	columns map[string]any,
) error {
	return db.Model(&models.CustomerTagRelation{}).
		Where("id = ? AND tenant_id = ? AND store_id = ? AND store_customer_relation_id = ?", id, tenantID, storeID, storeCustomerRelationID).
		Updates(columns).Error
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

func (r *customerTagChangeLogRepository) FindPageByStoreRelation(
	db *gorm.DB,
	tenantID, storeID, storeCustomerRelationID int64,
	page, limit int,
) ([]models.CustomerTagChangeLog, int64, error) {
	if page < 1 {
		page = 1
	}
	if limit < 1 {
		limit = 20
	}
	query := db.Model(&models.CustomerTagChangeLog{}).Where(
		"tenant_id = ? AND store_id = ? AND store_customer_relation_id = ?",
		tenantID, storeID, storeCustomerRelationID,
	)
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	ret := make([]models.CustomerTagChangeLog, 0)
	err := query.Order("id DESC").Offset((page - 1) * limit).Limit(limit).Find(&ret).Error
	return ret, total, err
}
