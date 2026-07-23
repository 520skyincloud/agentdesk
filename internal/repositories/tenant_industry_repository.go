package repositories

import (
	"strings"
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

func (r *tenantCustomerTagPolicyRepository) GetByTenantForUpdate(db *gorm.DB, tenantID int64) (*models.TenantCustomerTagPolicy, error) {
	ret := &models.TenantCustomerTagPolicy{}
	err := db.Clauses(clause.Locking{Strength: "UPDATE"}).Take(ret, "tenant_id = ?", tenantID).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return ret, nil
}

func (r *tenantCustomerTagPolicyRepository) Create(db *gorm.DB, item *models.TenantCustomerTagPolicy) error {
	return db.Create(item).Error
}

func (r *tenantCustomerTagPolicyRepository) UpdatesByTenant(db *gorm.DB, tenantID int64, columns map[string]any) error {
	return db.Model(&models.TenantCustomerTagPolicy{}).Where("tenant_id = ?", tenantID).Updates(columns).Error
}

type storeCustomerTagRuntimePolicyRepository struct{}

type StoreCustomerTagRuntimePolicyListFilter struct {
	Page             int
	Limit            int
	Keyword          string
	StoreStatus      *enums.Status
	EvolutionEnabled *bool
	ReplyEnabled     *bool
}

type StoreCustomerTagRuntimePolicyListItem struct {
	PolicyID                    int64
	StoreID                     int64
	StoreCode                   string
	StoreName                   string
	StoreStatus                 enums.Status
	CustomerTagEvolutionEnabled bool
	ReplyTagContextEnabled      bool
	PolicyStatus                enums.Status
	UpdatedAt                   *time.Time
}

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

func (r *storeCustomerTagRuntimePolicyRepository) GetByStoreForUpdate(db *gorm.DB, tenantID, storeID int64) (*models.StoreCustomerTagRuntimePolicy, error) {
	ret := &models.StoreCustomerTagRuntimePolicy{}
	err := db.Clauses(clause.Locking{Strength: "UPDATE"}).Take(ret, "tenant_id = ? AND store_id = ?", tenantID, storeID).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return ret, nil
}

func (r *storeCustomerTagRuntimePolicyRepository) FindByStores(db *gorm.DB, tenantID int64, storeIDs []int64) ([]models.StoreCustomerTagRuntimePolicy, error) {
	ret := make([]models.StoreCustomerTagRuntimePolicy, 0)
	if tenantID <= 0 || len(storeIDs) == 0 {
		return ret, nil
	}
	err := db.Where("tenant_id = ? AND store_id IN ?", tenantID, storeIDs).Order("store_id ASC").Find(&ret).Error
	return ret, err
}

func (r *storeCustomerTagRuntimePolicyRepository) Create(db *gorm.DB, item *models.StoreCustomerTagRuntimePolicy) error {
	return db.Create(item).Error
}

func (r *storeCustomerTagRuntimePolicyRepository) UpsertBatch(db *gorm.DB, items []models.StoreCustomerTagRuntimePolicy, updateColumns []string) error {
	if len(items) == 0 {
		return nil
	}
	return db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "tenant_id"}, {Name: "store_id"}},
		DoUpdates: clause.AssignmentColumns(updateColumns),
	}).CreateInBatches(items, 100).Error
}

func (r *storeCustomerTagRuntimePolicyRepository) FindStorePage(
	db *gorm.DB,
	tenantID int64,
	filter StoreCustomerTagRuntimePolicyListFilter,
) ([]StoreCustomerTagRuntimePolicyListItem, int64, error) {
	ret := make([]StoreCustomerTagRuntimePolicyListItem, 0)
	if tenantID <= 0 {
		return ret, 0, nil
	}
	if filter.Page < 1 {
		filter.Page = 1
	}
	if filter.Limit < 1 {
		filter.Limit = 20
	}
	if filter.Limit > 100 {
		filter.Limit = 100
	}
	storeTable := db.NamingStrategy.TableName("Store")
	policyTable := db.NamingStrategy.TableName("StoreCustomerTagRuntimePolicy")
	query := db.Table(storeTable+" AS stores").
		Joins("LEFT JOIN "+policyTable+" AS runtime_policy ON runtime_policy.tenant_id = stores.tenant_id AND runtime_policy.store_id = stores.id").
		Where("stores.tenant_id = ? AND stores.status <> ?", tenantID, enums.StatusDeleted)
	keyword := strings.TrimSpace(filter.Keyword)
	if keyword != "" {
		like := "%" + keyword + "%"
		query = query.Where("stores.name LIKE ? OR stores.store_code LIKE ?", like, like)
	}
	if filter.StoreStatus != nil {
		query = query.Where("stores.status = ?", *filter.StoreStatus)
	}
	if filter.EvolutionEnabled != nil {
		if *filter.EvolutionEnabled {
			query = query.Where("runtime_policy.id IS NOT NULL AND runtime_policy.customer_tag_evolution_enabled = ?", true)
		} else {
			query = query.Where("runtime_policy.id IS NULL OR runtime_policy.customer_tag_evolution_enabled = ?", false)
		}
	}
	if filter.ReplyEnabled != nil {
		if *filter.ReplyEnabled {
			query = query.Where("runtime_policy.id IS NOT NULL AND runtime_policy.reply_tag_context_enabled = ?", true)
		} else {
			query = query.Where("runtime_policy.id IS NULL OR runtime_policy.reply_tag_context_enabled = ?", false)
		}
	}
	var total int64
	if err := query.Session(&gorm.Session{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	err := query.Select(
		"runtime_policy.id AS policy_id, stores.id AS store_id, stores.store_code AS store_code, stores.name AS store_name, " +
			"stores.status AS store_status, runtime_policy.customer_tag_evolution_enabled AS customer_tag_evolution_enabled, " +
			"runtime_policy.reply_tag_context_enabled AS reply_tag_context_enabled, runtime_policy.status AS policy_status, " +
			"runtime_policy.updated_at AS updated_at",
	).
		Order("CASE WHEN stores.status = 0 THEN 0 ELSE 1 END ASC").
		Order("stores.name ASC").Order("stores.id ASC").
		Offset((filter.Page - 1) * filter.Limit).Limit(filter.Limit).
		Scan(&ret).Error
	return ret, total, err
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
