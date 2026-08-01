package repositories

import (
	"errors"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/httpx/params"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (r *wxWorkProtocolInstanceRepository) GetForUpdate(db *gorm.DB, id int64) *models.WxWorkProtocolInstance {
	ret := &models.WxWorkProtocolInstance{}
	if err := db.Clauses(clause.Locking{Strength: "UPDATE"}).First(ret, "id = ?", id).Error; err != nil {
		return nil
	}
	return ret
}

func (r *wxWorkProtocolInstanceRepository) GetForUpdateInTenant(db *gorm.DB, id, tenantID int64) *models.WxWorkProtocolInstance {
	if id <= 0 || tenantID <= 0 {
		return nil
	}
	ret := &models.WxWorkProtocolInstance{}
	if err := db.Clauses(clause.Locking{Strength: "UPDATE"}).First(ret, "id = ? AND tenant_id = ?", id, tenantID).Error; err != nil {
		return nil
	}
	return ret
}

var WxWorkProtocolInstanceRepository = newWxWorkProtocolInstanceRepository()

func newWxWorkProtocolInstanceRepository() *wxWorkProtocolInstanceRepository {
	return &wxWorkProtocolInstanceRepository{}
}

type wxWorkProtocolInstanceRepository struct{}

const (
	wxWorkProtocolCurrentInstanceCondition        = "replaced_by_instance_id = 0 AND (replaces_instance_id = 0 OR remote_setup_submitted_at IS NOT NULL)"
	wxWorkProtocolCurrentInstanceAliasedCondition = "instance.replaced_by_instance_id = 0 AND (instance.replaces_instance_id = 0 OR instance.remote_setup_submitted_at IS NOT NULL)"
)

func (r *wxWorkProtocolInstanceRepository) Get(db *gorm.DB, id int64) *models.WxWorkProtocolInstance {
	ret := &models.WxWorkProtocolInstance{}
	if err := db.First(ret, "id = ?", id).Error; err != nil {
		return nil
	}
	return ret
}

func (r *wxWorkProtocolInstanceRepository) GetInTenant(db *gorm.DB, id, tenantID int64) *models.WxWorkProtocolInstance {
	if id <= 0 || tenantID <= 0 {
		return nil
	}
	ret := &models.WxWorkProtocolInstance{}
	if err := db.First(ret, "id = ? AND tenant_id = ?", id, tenantID).Error; err != nil {
		return nil
	}
	return ret
}

func (r *wxWorkProtocolInstanceRepository) GetActivatedCurrentInTenant(db *gorm.DB, id, tenantID int64) *models.WxWorkProtocolInstance {
	if db == nil || id <= 0 || tenantID <= 0 {
		return nil
	}
	ret := &models.WxWorkProtocolInstance{}
	if err := db.First(
		ret,
		"id = ? AND tenant_id = ? AND status = ? AND "+wxWorkProtocolCurrentInstanceCondition,
		id,
		tenantID,
		enums.StatusOk,
	).Error; err != nil {
		return nil
	}
	return ret
}

func (r *wxWorkProtocolInstanceRepository) FindCurrentByStoreStaffBindingInTenant(
	db *gorm.DB,
	tenantID, bindingID int64,
	forUpdate bool,
) ([]models.WxWorkProtocolInstance, error) {
	ret := make([]models.WxWorkProtocolInstance, 0, 1)
	if db == nil || tenantID <= 0 || bindingID <= 0 {
		return ret, nil
	}
	query := db.Where(
		"tenant_id = ? AND store_staff_binding_id = ? AND status = ? AND "+wxWorkProtocolCurrentInstanceCondition,
		tenantID,
		bindingID,
		enums.StatusOk,
	).Order("id DESC")
	if forUpdate {
		query = query.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	if err := query.Find(&ret).Error; err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	return ret, nil
}

func (r *wxWorkProtocolInstanceRepository) FindActivatedCurrent(
	db *gorm.DB,
	cnd *sqls.Cnd,
) []models.WxWorkProtocolInstance {
	if db == nil {
		return []models.WxWorkProtocolInstance{}
	}
	if cnd == nil {
		cnd = sqls.NewCnd()
	}
	return r.Find(db, cnd.
		Eq("status", enums.StatusOk).
		Where(wxWorkProtocolCurrentInstanceCondition))
}

func (r *wxWorkProtocolInstanceRepository) FindReservationCandidatesByStoreStaffBindingInTenant(
	db *gorm.DB,
	tenantID, bindingID int64,
) ([]models.WxWorkProtocolInstance, error) {
	ret := make([]models.WxWorkProtocolInstance, 0, 1)
	if db == nil || tenantID <= 0 || bindingID <= 0 {
		return ret, nil
	}
	err := db.Where(
		"tenant_id = ? AND store_staff_binding_id = ? AND replaced_by_instance_id = 0 AND status <> ?",
		tenantID,
		bindingID,
		enums.StatusDeleted,
	).Order("id DESC").Find(&ret).Error
	return ret, err
}

func (r *wxWorkProtocolInstanceRepository) Take(db *gorm.DB, where ...any) *models.WxWorkProtocolInstance {
	ret := &models.WxWorkProtocolInstance{}
	if err := db.Take(ret, where...).Error; err != nil {
		return nil
	}
	return ret
}

func (r *wxWorkProtocolInstanceRepository) Find(db *gorm.DB, cnd *sqls.Cnd) (list []models.WxWorkProtocolInstance) {
	cnd.Find(db, &list)
	return
}

func (r *wxWorkProtocolInstanceRepository) FindPageByParams(db *gorm.DB, params *params.QueryParams) (list []models.WxWorkProtocolInstance, paging *sqls.Paging) {
	return r.FindPageByCnd(db, &params.Cnd)
}

func (r *wxWorkProtocolInstanceRepository) FindPageByCnd(db *gorm.DB, cnd *sqls.Cnd) (list []models.WxWorkProtocolInstance, paging *sqls.Paging) {
	cnd.Find(db, &list)
	count := cnd.Count(db, &models.WxWorkProtocolInstance{})
	paging = &sqls.Paging{Page: cnd.Paging.Page, Limit: cnd.Paging.Limit, Total: count}
	return
}

func (r *wxWorkProtocolInstanceRepository) CountByWelcomeImageAssetID(db *gorm.DB, assetID string) (int64, error) {
	var count int64
	err := db.Model(&models.WxWorkProtocolInstance{}).
		Where("welcome_image_asset_id = ?", assetID).
		Count(&count).Error
	return count, err
}

func (r *wxWorkProtocolInstanceRepository) CountByWelcomeImageAssetIDInTenant(db *gorm.DB, assetID string, tenantID int64) (int64, error) {
	if assetID == "" || tenantID <= 0 {
		return 0, nil
	}
	var count int64
	err := db.Model(&models.WxWorkProtocolInstance{}).
		Where("welcome_image_asset_id = ? AND tenant_id = ?", assetID, tenantID).
		Count(&count).Error
	return count, err
}

func (r *wxWorkProtocolInstanceRepository) CountAIEnabledInTenant(db *gorm.DB, tenantID int64) (int64, error) {
	if db == nil || tenantID <= 0 {
		return 0, nil
	}
	var count int64
	err := db.Model(&models.WxWorkProtocolInstance{}).
		Where(
			"tenant_id = ? AND ai_reply_enabled = ? AND status = ? AND "+wxWorkProtocolCurrentInstanceCondition,
			tenantID,
			true,
			enums.StatusOk,
		).
		Count(&count).Error
	return count, err
}

func (r *wxWorkProtocolInstanceRepository) Create(db *gorm.DB, t *models.WxWorkProtocolInstance) error {
	return db.Create(t).Error
}

func (r *wxWorkProtocolInstanceRepository) Updates(db *gorm.DB, id int64, columns map[string]any) error {
	return db.Model(&models.WxWorkProtocolInstance{}).Where("id = ?", id).Updates(columns).Error
}

func (r *wxWorkProtocolInstanceRepository) UpdatesInTenant(db *gorm.DB, id, tenantID int64, columns map[string]any) error {
	return db.Model(&models.WxWorkProtocolInstance{}).Where("id = ? AND tenant_id = ?", id, tenantID).Updates(columns).Error
}

func (r *wxWorkProtocolInstanceRepository) ClaimTenant(db *gorm.DB, id, tenantID int64, columns map[string]any) (bool, error) {
	if id <= 0 || tenantID <= 0 {
		return false, nil
	}
	updates := make(map[string]any, len(columns)+1)
	for key, value := range columns {
		updates[key] = value
	}
	updates["tenant_id"] = tenantID
	result := db.Model(&models.WxWorkProtocolInstance{}).Where("id = ? AND tenant_id = ?", id, 0).Updates(updates)
	return result.RowsAffected == 1, result.Error
}

func (r *wxWorkProtocolInstanceRepository) ReleaseLoginBinding(db *gorm.DB, id, tenantID int64, columns map[string]any) (bool, error) {
	if id <= 0 || tenantID <= 0 {
		return false, nil
	}
	result := db.Model(&models.WxWorkProtocolInstance{}).
		Where("id = ? AND tenant_id IN ?", id, []int64{0, tenantID}).
		Updates(columns)
	return result.RowsAffected == 1, result.Error
}

func (r *wxWorkProtocolInstanceRepository) UpdatesByIDs(db *gorm.DB, ids []int64, columns map[string]any) error {
	if len(ids) == 0 {
		return nil
	}
	return db.Model(&models.WxWorkProtocolInstance{}).Where("id IN ?", ids).Updates(columns).Error
}

func (r *wxWorkProtocolInstanceRepository) UpdatesByStoreStaffBindingIDs(db *gorm.DB, bindingIDs []int64, columns map[string]any) error {
	if len(bindingIDs) == 0 {
		return nil
	}
	return db.Model(&models.WxWorkProtocolInstance{}).Where("store_staff_binding_id IN ?", bindingIDs).Updates(columns).Error
}

func (r *wxWorkProtocolInstanceRepository) UpdatesByStoreStaffBindingIDsInTenant(db *gorm.DB, bindingIDs []int64, tenantID int64, columns map[string]any) error {
	if len(bindingIDs) == 0 || tenantID <= 0 {
		return nil
	}
	return db.Model(&models.WxWorkProtocolInstance{}).
		Where("store_staff_binding_id IN ? AND tenant_id = ?", bindingIDs, tenantID).
		Updates(columns).Error
}

func (r *wxWorkProtocolInstanceRepository) UpdatesActiveByStoreStaffBindingIDsInTenant(db *gorm.DB, bindingIDs []int64, tenantID int64, columns map[string]any) error {
	if len(bindingIDs) == 0 || tenantID <= 0 {
		return nil
	}
	return db.Model(&models.WxWorkProtocolInstance{}).
		Where("store_staff_binding_id IN ? AND tenant_id = ? AND status <> ?", bindingIDs, tenantID, enums.StatusDeleted).
		Updates(columns).Error
}

func (r *wxWorkProtocolInstanceRepository) Delete(db *gorm.DB, id int64) error {
	return db.Delete(&models.WxWorkProtocolInstance{}, "id = ?", id).Error
}

func (r *wxWorkProtocolInstanceRepository) DeleteInTenant(db *gorm.DB, id, tenantID int64) error {
	return db.Delete(&models.WxWorkProtocolInstance{}, "id = ? AND tenant_id = ?", id, tenantID).Error
}
