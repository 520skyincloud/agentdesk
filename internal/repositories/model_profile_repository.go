package repositories

import (
	"errors"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ModelProfileTemplateRepository = &modelProfileTemplateRepository{}
var ModelProfileSlotRepository = &modelProfileSlotRepository{}
var ModelProfileTestRunRepository = &modelProfileTestRunRepository{}
var StoreModelProfileAssignmentRepository = &storeModelProfileAssignmentRepository{}

type modelProfileTemplateRepository struct{}
type modelProfileSlotRepository struct{}
type modelProfileTestRunRepository struct{}
type storeModelProfileAssignmentRepository struct{}

func (r *modelProfileTemplateRepository) Get(db *gorm.DB, id int64) *models.ModelProfileTemplate {
	if db == nil || id <= 0 {
		return nil
	}
	item := &models.ModelProfileTemplate{}
	if err := db.First(item, "id = ?", id).Error; err != nil {
		return nil
	}
	return item
}

func (r *modelProfileTemplateRepository) GetForUpdate(db *gorm.DB, id int64) (*models.ModelProfileTemplate, error) {
	if db == nil || id <= 0 {
		return nil, errors.New("model profile template id is required")
	}
	item := &models.ModelProfileTemplate{}
	err := db.Clauses(clause.Locking{Strength: "UPDATE"}).First(item, "id = ?", id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return item, nil
}

func (r *modelProfileTemplateRepository) GetLatestByCode(db *gorm.DB, code string) *models.ModelProfileTemplate {
	return r.FindOne(db, sqls.NewCnd().Eq("code", code).Desc("revision").Desc("id"))
}

func (r *modelProfileTemplateRepository) GetLatestByCodeForUpdate(db *gorm.DB, code string) (*models.ModelProfileTemplate, error) {
	if db == nil || code == "" {
		return nil, errors.New("model profile template code is required")
	}
	item := &models.ModelProfileTemplate{}
	err := db.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("code = ?", code).
		Order("revision DESC, id DESC").
		Take(item).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return item, nil
}

func (r *modelProfileTemplateRepository) FindDraftByCodeForUpdate(db *gorm.DB, code string) (*models.ModelProfileTemplate, error) {
	if db == nil || code == "" {
		return nil, errors.New("model profile template code is required")
	}
	item := &models.ModelProfileTemplate{}
	err := db.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("code = ? AND status = ?", code, enums.ModelProfileStatusDraft).
		Order("revision DESC, id DESC").
		Take(item).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return item, nil
}

func (r *modelProfileTemplateRepository) FindActiveByCode(db *gorm.DB, code string) *models.ModelProfileTemplate {
	return r.FindOne(db, sqls.NewCnd().Eq("code", code).Eq("status", enums.ModelProfileStatusActive).Desc("revision").Desc("id"))
}

func (r *modelProfileTemplateRepository) Find(db *gorm.DB, cnd *sqls.Cnd) (list []models.ModelProfileTemplate) {
	if db == nil || cnd == nil {
		return list
	}
	cnd.Find(db, &list)
	return list
}

func (r *modelProfileTemplateRepository) FindOne(db *gorm.DB, cnd *sqls.Cnd) *models.ModelProfileTemplate {
	if db == nil || cnd == nil {
		return nil
	}
	item := &models.ModelProfileTemplate{}
	if err := cnd.FindOne(db, item); err != nil {
		return nil
	}
	return item
}

func (r *modelProfileTemplateRepository) Create(db *gorm.DB, item *models.ModelProfileTemplate) error {
	if db == nil || item == nil {
		return errors.New("model profile template is required")
	}
	return db.Create(item).Error
}

func (r *modelProfileTemplateRepository) Updates(db *gorm.DB, id int64, columns map[string]any) error {
	if db == nil || id <= 0 {
		return errors.New("model profile template id is required")
	}
	return db.Model(&models.ModelProfileTemplate{}).Where("id = ?", id).Updates(columns).Error
}

func (r *modelProfileSlotRepository) GetByUsage(db *gorm.DB, templateID int64, usage enums.ModelUsageSlot) *models.ModelProfileSlot {
	if db == nil || templateID <= 0 || usage == "" {
		return nil
	}
	item := &models.ModelProfileSlot{}
	if err := db.Take(item, "template_id = ? AND usage_code = ?", templateID, usage).Error; err != nil {
		return nil
	}
	return item
}

func (r *modelProfileSlotRepository) FindByTemplateID(db *gorm.DB, templateID int64) (list []models.ModelProfileSlot) {
	if db == nil || templateID <= 0 {
		return list
	}
	sqls.NewCnd().Eq("template_id", templateID).Asc("sort_no").Asc("id").Find(db, &list)
	return list
}

func (r *modelProfileSlotRepository) ReplaceByTemplateID(db *gorm.DB, templateID int64, list []models.ModelProfileSlot) error {
	if db == nil || templateID <= 0 {
		return errors.New("model profile template id is required")
	}
	if err := db.Where("template_id = ?", templateID).Delete(&models.ModelProfileSlot{}).Error; err != nil {
		return err
	}
	if len(list) == 0 {
		return nil
	}
	zeroRetryUsages := make([]enums.ModelUsageSlot, 0, len(list))
	zeroRetrySet := make(map[enums.ModelUsageSlot]struct{}, len(list))
	for i := range list {
		if list[i].MaxRetryCount == 0 {
			zeroRetryUsages = append(zeroRetryUsages, list[i].UsageCode)
			zeroRetrySet[list[i].UsageCode] = struct{}{}
		}
	}
	if err := db.Create(&list).Error; err != nil {
		return err
	}
	if len(zeroRetryUsages) == 0 {
		return nil
	}
	// GORM applies the tag default to zero-valued struct fields during Create.
	// Restore explicit administrator zeroes in the same service transaction.
	if err := db.Model(&models.ModelProfileSlot{}).
		Where("template_id = ? AND usage_code IN ?", templateID, zeroRetryUsages).
		Update("max_retry_count", 0).Error; err != nil {
		return err
	}
	for i := range list {
		if _, ok := zeroRetrySet[list[i].UsageCode]; ok {
			list[i].MaxRetryCount = 0
		}
	}
	return nil
}

func (r *modelProfileTestRunRepository) Create(db *gorm.DB, item *models.ModelProfileTestRun) error {
	if db == nil || item == nil {
		return errors.New("model profile test run is required")
	}
	return db.Create(item).Error
}

func (r *modelProfileTestRunRepository) FindLatestByDigest(db *gorm.DB, templateID, revision int64, digest string) *models.ModelProfileTestRun {
	if db == nil || templateID <= 0 || revision <= 0 || digest == "" {
		return nil
	}
	item := &models.ModelProfileTestRun{}
	if err := db.Where(
		"template_id = ? AND template_revision = ? AND config_digest = ?",
		templateID,
		revision,
		digest,
	).Order("id DESC").Take(item).Error; err != nil {
		return nil
	}
	return item
}

func (r *modelProfileTestRunRepository) FindLatestPassedByDigest(db *gorm.DB, templateID, revision int64, digest string) *models.ModelProfileTestRun {
	if db == nil || templateID <= 0 || revision <= 0 || digest == "" {
		return nil
	}
	item := &models.ModelProfileTestRun{}
	if err := db.Where(
		"template_id = ? AND template_revision = ? AND config_digest = ? AND status = ?",
		templateID,
		revision,
		digest,
		enums.ModelProfileTestStatusPassed,
	).Order("id DESC").Take(item).Error; err != nil {
		return nil
	}
	return item
}

func (r *modelProfileTestRunRepository) FindLatestPassedForBinding(
	db *gorm.DB,
	templateID,
	revision,
	tenantID,
	storeID,
	storeStaffBindingID,
	credentialRevision int64,
	digest string,
) *models.ModelProfileTestRun {
	if db == nil || templateID <= 0 || revision <= 0 || tenantID <= 0 || storeID <= 0 || storeStaffBindingID <= 0 || credentialRevision <= 0 || digest == "" {
		return nil
	}
	item := &models.ModelProfileTestRun{}
	if err := db.Where(
		"template_id = ? AND template_revision = ? AND tenant_id = ? AND store_id = ? AND store_staff_binding_id = ? AND credential_revision = ? AND config_digest = ? AND status = ?",
		templateID,
		revision,
		tenantID,
		storeID,
		storeStaffBindingID,
		credentialRevision,
		digest,
		enums.ModelProfileTestStatusPassed,
	).Order("id DESC").Take(item).Error; err != nil {
		return nil
	}
	return item
}

func (r *storeModelProfileAssignmentRepository) GetByStore(db *gorm.DB, tenantID, storeID int64) *models.StoreModelProfileAssignment {
	if db == nil || tenantID <= 0 || storeID <= 0 {
		return nil
	}
	item := &models.StoreModelProfileAssignment{}
	if err := db.Take(item, "tenant_id = ? AND store_id = ?", tenantID, storeID).Error; err != nil {
		return nil
	}
	return item
}

func (r *storeModelProfileAssignmentRepository) GetForUpdateByStore(db *gorm.DB, tenantID, storeID int64) (*models.StoreModelProfileAssignment, error) {
	if db == nil || tenantID <= 0 || storeID <= 0 {
		return nil, errors.New("store model profile assignment scope is required")
	}
	item := &models.StoreModelProfileAssignment{}
	err := db.Clauses(clause.Locking{Strength: "UPDATE"}).Take(item, "tenant_id = ? AND store_id = ?", tenantID, storeID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return item, nil
}

func (r *storeModelProfileAssignmentRepository) FindByTenant(db *gorm.DB, tenantID int64) (list []models.StoreModelProfileAssignment) {
	if db == nil || tenantID <= 0 {
		return list
	}
	sqls.NewCnd().Eq("tenant_id", tenantID).Asc("store_id").Find(db, &list)
	return list
}

func (r *storeModelProfileAssignmentRepository) Create(db *gorm.DB, item *models.StoreModelProfileAssignment) error {
	if db == nil || item == nil {
		return errors.New("store model profile assignment is required")
	}
	return db.Create(item).Error
}

func (r *storeModelProfileAssignmentRepository) Updates(db *gorm.DB, id int64, columns map[string]any) error {
	if db == nil || id <= 0 {
		return errors.New("store model profile assignment id is required")
	}
	return db.Model(&models.StoreModelProfileAssignment{}).Where("id = ?", id).Updates(columns).Error
}
