package repositories

import (
	"errors"
	"fmt"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var StoreModelCredentialRepository = &storeModelCredentialRepository{}
var StoreCredentialPolicyRepository = &storeCredentialPolicyRepository{}
var StoreModelCredentialAuditLogRepository = &storeModelCredentialAuditLogRepository{}

type storeModelCredentialRepository struct{}
type storeCredentialPolicyRepository struct{}
type storeModelCredentialAuditLogRepository struct{}

type ActiveStoreModelCredentialMetadata struct {
	TenantID            int64
	StoreID             int64
	StoreStaffBindingID int64
	CredentialRevision  int64
}

type UsableModelProfileTestTargetMetadata struct {
	TenantID               int64
	TenantShortName        string
	TenantLegalName        string
	StoreID                int64
	StoreStaffBindingID    int64
	StoreCode              string
	StoreName              string
	CredentialRevision     int64
	ActiveTemplateID       int64
	ActiveTemplateName     string
	ActiveTemplateRevision int64
}

func (r *storeModelCredentialRepository) Get(db *gorm.DB, id int64) *models.StoreModelCredential {
	if db == nil || id <= 0 {
		return nil
	}
	item := &models.StoreModelCredential{}
	if err := db.First(item, "id = ?", id).Error; err != nil {
		return nil
	}
	return item
}

func (r *storeModelCredentialRepository) GetByBinding(db *gorm.DB, tenantID, storeID, bindingID int64) *models.StoreModelCredential {
	if db == nil || tenantID <= 0 || storeID <= 0 || bindingID <= 0 {
		return nil
	}
	item := &models.StoreModelCredential{}
	if err := db.Take(item, "tenant_id = ? AND store_id = ? AND store_staff_binding_id = ?", tenantID, storeID, bindingID).Error; err != nil {
		return nil
	}
	return item
}

func (r *storeModelCredentialRepository) FindByStore(db *gorm.DB, tenantID, storeID int64) (list []models.StoreModelCredential) {
	if db == nil || tenantID <= 0 || storeID <= 0 {
		return list
	}
	db.Where("tenant_id = ? AND store_id = ?", tenantID, storeID).Order("store_staff_binding_id ASC, id ASC").Find(&list)
	return list
}

func (r *storeModelCredentialRepository) GetForUpdateByBinding(db *gorm.DB, tenantID, storeID, bindingID int64) (*models.StoreModelCredential, error) {
	if db == nil || tenantID <= 0 || storeID <= 0 || bindingID <= 0 {
		return nil, errors.New("store staff model credential scope is required")
	}
	item := &models.StoreModelCredential{}
	err := db.Clauses(clause.Locking{Strength: "UPDATE"}).Take(item,
		"tenant_id = ? AND store_id = ? AND store_staff_binding_id = ?", tenantID, storeID, bindingID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return item, nil
}

func (r *storeModelCredentialRepository) FindByTenant(db *gorm.DB, tenantID int64) (list []models.StoreModelCredential) {
	if db == nil || tenantID <= 0 {
		return list
	}
	sqls.NewCnd().Eq("tenant_id", tenantID).Asc("store_id").Find(db, &list)
	return list
}

func (r *storeModelCredentialRepository) FindUsableProfileTestTargets(db *gorm.DB, limit int) (list []UsableModelProfileTestTargetMetadata, err error) {
	if db == nil {
		return list, errors.New("database is required")
	}
	query := r.usableProfileTestTargetQuery(db).
		Select(
			"credential.tenant_id AS tenant_id, tenant.short_name AS tenant_short_name, tenant.legal_name AS tenant_legal_name, " +
				"credential.store_id AS store_id, credential.store_staff_binding_id AS store_staff_binding_id, store.store_code AS store_code, store.name AS store_name, " +
				"credential.credential_revision AS credential_revision, template.id AS active_template_id, " +
				"template.name AS active_template_name, template.revision AS active_template_revision",
		).
		Order("credential.tenant_id ASC, credential.store_id ASC, credential.store_staff_binding_id ASC")
	if limit > 0 {
		query = query.Limit(limit)
	}
	err = query.Scan(&list).Error
	return list, err
}

func (r *storeModelCredentialRepository) FindActiveMetadataByBinding(db *gorm.DB, tenantID, storeID, bindingID int64) *ActiveStoreModelCredentialMetadata {
	if db == nil || tenantID <= 0 || storeID <= 0 || bindingID <= 0 {
		return nil
	}
	item := &ActiveStoreModelCredentialMetadata{}
	result := db.Model(&models.StoreModelCredential{}).
		Select("tenant_id, store_id, store_staff_binding_id, credential_revision").
		Where(
			"tenant_id = ? AND store_id = ? AND store_staff_binding_id = ? AND status = ? AND credential_revision > 0 AND encrypted_key <> ''",
			tenantID, storeID, bindingID, enums.StoreCredentialStatusActive,
		).
		Take(item)
	if result.Error != nil {
		return nil
	}
	return item
}

func (r *storeModelCredentialRepository) HasActive(db *gorm.DB) (bool, error) {
	if db == nil {
		return false, errors.New("database is required")
	}
	var row struct {
		ID int64
	}
	err := db.Model(&models.StoreModelCredential{}).
		Select("id").
		Where(
			"status = ? AND credential_revision > 0 AND encrypted_key <> ''",
			enums.StoreCredentialStatusActive,
		).
		Order("id ASC").
		Limit(1).
		Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return row.ID > 0, nil
}

func (r *storeModelCredentialRepository) HasUsableProfileTestTarget(db *gorm.DB) (bool, error) {
	if db == nil {
		return false, errors.New("database is required")
	}
	var row struct {
		ID int64
	}
	err := r.usableProfileTestTargetQuery(db).
		Select("credential.id").
		Order("credential.id ASC").
		Limit(1).
		Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return row.ID > 0, nil
}

func (r *storeModelCredentialRepository) usableProfileTestTargetQuery(db *gorm.DB) *gorm.DB {
	credentialTable := db.NamingStrategy.TableName("StoreModelCredential")
	storeTable := db.NamingStrategy.TableName("Store")
	tenantTable := db.NamingStrategy.TableName("Tenant")
	assignmentTable := db.NamingStrategy.TableName("StoreModelProfileAssignment")
	templateTable := db.NamingStrategy.TableName("ModelProfileTemplate")
	return db.Table(credentialTable+" AS credential").
		Joins(fmt.Sprintf(
			"JOIN %s AS store ON store.id = credential.store_id AND store.tenant_id = credential.tenant_id",
			storeTable,
		)).
		Joins(fmt.Sprintf(
			"JOIN %s AS tenant ON tenant.id = credential.tenant_id",
			tenantTable,
		)).
		Joins(fmt.Sprintf(
			"JOIN %s AS assignment ON assignment.tenant_id = credential.tenant_id AND assignment.store_id = credential.store_id",
			assignmentTable,
		)).
		Joins(fmt.Sprintf(
			"JOIN %s AS template ON template.id = assignment.template_id AND template.revision = assignment.template_revision",
			templateTable,
		)).
		Where(
			"credential.status = ? AND credential.credential_revision > 0 AND credential.encrypted_key <> ''",
			enums.StoreCredentialStatusActive,
		).
		Where("store.status = ? AND tenant.status = ?", enums.StatusOk, enums.StatusOk).
		Where(
			"assignment.status = ? AND assignment.readiness_status = ? AND assignment.template_id > 0 AND assignment.template_revision > 0",
			enums.StoreModelAssignmentStatusReady,
			"ready",
		).
		Where("template.status = ?", enums.ModelProfileStatusActive)
}

func (r *storeModelCredentialRepository) Create(db *gorm.DB, item *models.StoreModelCredential) error {
	if db == nil || item == nil {
		return errors.New("store model credential is required")
	}
	return db.Create(item).Error
}

func (r *storeModelCredentialRepository) Updates(db *gorm.DB, id int64, columns map[string]any) error {
	if db == nil || id <= 0 {
		return errors.New("store model credential id is required")
	}
	return db.Model(&models.StoreModelCredential{}).Where("id = ?", id).Updates(columns).Error
}

func (r *storeCredentialPolicyRepository) GetByStore(db *gorm.DB, tenantID, storeID int64) *models.StoreCredentialPolicy {
	if db == nil || tenantID <= 0 || storeID <= 0 {
		return nil
	}
	item := &models.StoreCredentialPolicy{}
	if err := db.Take(item, "tenant_id = ? AND store_id = ?", tenantID, storeID).Error; err != nil {
		return nil
	}
	return item
}

func (r *storeCredentialPolicyRepository) GetForUpdateByStore(db *gorm.DB, tenantID, storeID int64) (*models.StoreCredentialPolicy, error) {
	if db == nil || tenantID <= 0 || storeID <= 0 {
		return nil, errors.New("store credential policy scope is required")
	}
	item := &models.StoreCredentialPolicy{}
	err := db.Clauses(clause.Locking{Strength: "UPDATE"}).Take(item, "tenant_id = ? AND store_id = ?", tenantID, storeID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return item, nil
}

func (r *storeCredentialPolicyRepository) FindByTenant(db *gorm.DB, tenantID int64) (list []models.StoreCredentialPolicy) {
	if db == nil || tenantID <= 0 {
		return list
	}
	sqls.NewCnd().Eq("tenant_id", tenantID).Asc("store_id").Find(db, &list)
	return list
}

func (r *storeCredentialPolicyRepository) Create(db *gorm.DB, item *models.StoreCredentialPolicy) error {
	if db == nil || item == nil {
		return errors.New("store credential policy is required")
	}
	return db.Create(item).Error
}

func (r *storeCredentialPolicyRepository) Updates(db *gorm.DB, id int64, columns map[string]any) error {
	if db == nil || id <= 0 {
		return errors.New("store credential policy id is required")
	}
	return db.Model(&models.StoreCredentialPolicy{}).Where("id = ?", id).Updates(columns).Error
}

func (r *storeModelCredentialAuditLogRepository) Create(db *gorm.DB, item *models.StoreModelCredentialAuditLog) error {
	if db == nil || item == nil {
		return errors.New("store credential audit log is required")
	}
	return db.Create(item).Error
}

func (r *storeModelCredentialAuditLogRepository) FindLatestByStore(db *gorm.DB, tenantID, storeID int64, limit int) (list []models.StoreModelCredentialAuditLog) {
	if db == nil || tenantID <= 0 || storeID <= 0 {
		return list
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	db.Where("tenant_id = ? AND store_id = ?", tenantID, storeID).Order("id DESC").Limit(limit).Find(&list)
	return list
}

func (r *storeModelCredentialAuditLogRepository) FindLatestByBinding(db *gorm.DB, tenantID, storeID, bindingID int64, limit int) (list []models.StoreModelCredentialAuditLog) {
	if db == nil || tenantID <= 0 || storeID <= 0 || bindingID <= 0 {
		return list
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	db.Where("tenant_id = ? AND store_id = ? AND store_staff_binding_id = ?", tenantID, storeID, bindingID).
		Order("id DESC").Limit(limit).Find(&list)
	return list
}
