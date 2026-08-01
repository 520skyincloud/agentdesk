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

var StoreRepository = newStoreRepository()

func newStoreRepository() *storeRepository {
	return &storeRepository{}
}

type storeRepository struct{}

type StoreRelationCount struct {
	StoreID int64
	Count   int64
}

func (r *storeRepository) Get(db *gorm.DB, id int64) *models.Store {
	ret := &models.Store{}
	if err := db.First(ret, "id = ?", id).Error; err != nil {
		return nil
	}
	return ret
}

func (r *storeRepository) GetInTenant(db *gorm.DB, id, tenantID int64) *models.Store {
	if id <= 0 || tenantID <= 0 {
		return nil
	}
	ret := &models.Store{}
	if err := db.First(ret, "id = ? AND tenant_id = ?", id, tenantID).Error; err != nil {
		return nil
	}
	return ret
}

func (r *storeRepository) GetForUpdateInTenant(db *gorm.DB, id, tenantID int64) (*models.Store, error) {
	if db == nil || id <= 0 || tenantID <= 0 {
		return nil, errors.New("store id and tenant are required")
	}
	item := &models.Store{}
	err := db.Clauses(clause.Locking{Strength: "UPDATE"}).First(item, "id = ? AND tenant_id = ?", id, tenantID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return item, nil
}

func (r *storeRepository) FindByIDsForUpdateInTenant(db *gorm.DB, tenantID int64, ids []int64) ([]models.Store, error) {
	ret := make([]models.Store, 0)
	if db == nil || tenantID <= 0 || len(ids) == 0 {
		return ret, nil
	}
	err := db.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("tenant_id = ? AND id IN ? AND status <> ?", tenantID, ids, enums.StatusDeleted).
		Order("id ASC").Find(&ret).Error
	return ret, err
}

func (r *storeRepository) FindAllForUpdateInTenant(db *gorm.DB, tenantID int64) ([]models.Store, error) {
	ret := make([]models.Store, 0)
	if db == nil || tenantID <= 0 {
		return ret, nil
	}
	err := db.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("tenant_id = ? AND status <> ?", tenantID, enums.StatusDeleted).
		Order("id ASC").Find(&ret).Error
	return ret, err
}

func (r *storeRepository) Take(db *gorm.DB, where ...any) *models.Store {
	ret := &models.Store{}
	if err := db.Take(ret, where...).Error; err != nil {
		return nil
	}
	return ret
}

func (r *storeRepository) Find(db *gorm.DB, cnd *sqls.Cnd) (list []models.Store) {
	cnd.Find(db, &list)
	return
}

func (r *storeRepository) FindPageByParams(db *gorm.DB, params *params.QueryParams) (list []models.Store, paging *sqls.Paging) {
	return r.FindPageByCnd(db, &params.Cnd)
}

func (r *storeRepository) FindPageByCnd(db *gorm.DB, cnd *sqls.Cnd) (list []models.Store, paging *sqls.Paging) {
	cnd.Find(db, &list)
	count := cnd.Count(db, &models.Store{})
	paging = &sqls.Paging{Page: cnd.Paging.Page, Limit: cnd.Paging.Limit, Total: count}
	return
}

func (r *storeRepository) Create(db *gorm.DB, t *models.Store) error {
	return db.Create(t).Error
}

func (r *storeRepository) Updates(db *gorm.DB, id int64, columns map[string]any) error {
	return db.Model(&models.Store{}).Where("id = ?", id).Updates(columns).Error
}

func (r *storeRepository) UpdatesInTenant(db *gorm.DB, id, tenantID int64, columns map[string]any) error {
	return db.Model(&models.Store{}).Where("id = ? AND tenant_id = ?", id, tenantID).Updates(columns).Error
}

func (r *storeRepository) CountActiveBindingsByStoreIDs(db *gorm.DB, tenantID int64, storeIDs []int64) map[int64]int64 {
	result := make(map[int64]int64)
	if db == nil || tenantID <= 0 || len(storeIDs) == 0 {
		return result
	}
	var rows []StoreRelationCount
	if err := db.Model(&models.StoreStaffBinding{}).
		Select("store_id, COUNT(*) AS count").
		Where("tenant_id = ? AND store_id IN ? AND status = ?", tenantID, storeIDs, enums.StatusOk).
		Group("store_id").Scan(&rows).Error; err != nil {
		return result
	}
	for _, row := range rows {
		result[row.StoreID] = row.Count
	}
	return result
}

func (r *storeRepository) CountCurrentInstancesByStoreIDs(db *gorm.DB, tenantID int64, storeIDs []int64) map[int64]int64 {
	result := make(map[int64]int64)
	if db == nil || tenantID <= 0 || len(storeIDs) == 0 {
		return result
	}
	var rows []StoreRelationCount
	if err := db.Model(&models.WxWorkProtocolInstance{}).
		Select("store_id, COUNT(*) AS count").
		Where("tenant_id = ? AND store_id IN ? AND status = ? AND "+wxWorkProtocolCurrentInstanceCondition, tenantID, storeIDs, enums.StatusOk).
		Group("store_id").Scan(&rows).Error; err != nil {
		return result
	}
	for _, row := range rows {
		result[row.StoreID] = row.Count
	}
	return result
}
