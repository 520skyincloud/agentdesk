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

var StoreStaffBindingRepository = newStoreStaffBindingRepository()

func newStoreStaffBindingRepository() *storeStaffBindingRepository {
	return &storeStaffBindingRepository{}
}

type storeStaffBindingRepository struct{}

func (r *storeStaffBindingRepository) Get(db *gorm.DB, id int64) *models.StoreStaffBinding {
	ret := &models.StoreStaffBinding{}
	if err := db.First(ret, "id = ?", id).Error; err != nil {
		return nil
	}
	return ret
}

func (r *storeStaffBindingRepository) GetInTenant(db *gorm.DB, id, tenantID int64) *models.StoreStaffBinding {
	if id <= 0 || tenantID <= 0 {
		return nil
	}
	ret := &models.StoreStaffBinding{}
	if err := db.First(ret, "id = ? AND tenant_id = ?", id, tenantID).Error; err != nil {
		return nil
	}
	return ret
}

func (r *storeStaffBindingRepository) GetForUpdateInTenant(db *gorm.DB, id, tenantID int64) (*models.StoreStaffBinding, error) {
	if id <= 0 || tenantID <= 0 {
		return nil, nil
	}
	ret := &models.StoreStaffBinding{}
	err := db.Clauses(clause.Locking{Strength: "UPDATE"}).First(ret, "id = ? AND tenant_id = ?", id, tenantID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return ret, nil
}

func (r *storeStaffBindingRepository) Take(db *gorm.DB, where ...any) *models.StoreStaffBinding {
	ret := &models.StoreStaffBinding{}
	if err := db.Take(ret, where...).Error; err != nil {
		return nil
	}
	return ret
}

func (r *storeStaffBindingRepository) TakeInTenant(db *gorm.DB, tenantID int64, where ...any) *models.StoreStaffBinding {
	if tenantID <= 0 {
		return nil
	}
	ret := &models.StoreStaffBinding{}
	if err := db.Where("tenant_id = ?", tenantID).Take(ret, where...).Error; err != nil {
		return nil
	}
	return ret
}

func (r *storeStaffBindingRepository) Find(db *gorm.DB, cnd *sqls.Cnd) (list []models.StoreStaffBinding) {
	cnd.Find(db, &list)
	return
}

func (r *storeStaffBindingRepository) FindForUpdateByUserInTenant(db *gorm.DB, tenantID, userID int64) ([]models.StoreStaffBinding, error) {
	if tenantID <= 0 || userID <= 0 {
		return []models.StoreStaffBinding{}, nil
	}
	var list []models.StoreStaffBinding
	err := db.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("tenant_id = ? AND user_id = ? AND status = ?", tenantID, userID, enums.StatusOk).
		Order("id ASC").
		Find(&list).Error
	return list, err
}

func (r *storeStaffBindingRepository) FindAllForUpdateByUserInTenant(db *gorm.DB, tenantID, userID int64) ([]models.StoreStaffBinding, error) {
	if tenantID <= 0 || userID <= 0 {
		return []models.StoreStaffBinding{}, nil
	}
	var list []models.StoreStaffBinding
	err := db.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("tenant_id = ? AND user_id = ? AND status <> ?", tenantID, userID, enums.StatusDeleted).
		Order("id ASC").
		Find(&list).Error
	return list, err
}

func (r *storeStaffBindingRepository) FindForUpdateByTeamOrUsersInTenant(db *gorm.DB, tenantID, teamID int64, userIDs []int64) ([]models.StoreStaffBinding, error) {
	if tenantID <= 0 || teamID <= 0 {
		return []models.StoreStaffBinding{}, nil
	}
	query := db.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("tenant_id = ? AND status = ?", tenantID, enums.StatusOk)
	if len(userIDs) == 0 {
		query = query.Where("agent_team_id = ?", teamID)
	} else {
		query = query.Where("(agent_team_id = ? OR user_id IN ?)", teamID, userIDs)
	}
	var list []models.StoreStaffBinding
	err := query.Order("id ASC").Find(&list).Error
	return list, err
}

func (r *storeStaffBindingRepository) FindPageByParams(db *gorm.DB, params *params.QueryParams) (list []models.StoreStaffBinding, paging *sqls.Paging) {
	return r.FindPageByCnd(db, &params.Cnd)
}

func (r *storeStaffBindingRepository) FindPageByCnd(db *gorm.DB, cnd *sqls.Cnd) (list []models.StoreStaffBinding, paging *sqls.Paging) {
	cnd.Find(db, &list)
	count := cnd.Count(db, &models.StoreStaffBinding{})
	paging = &sqls.Paging{Page: cnd.Paging.Page, Limit: cnd.Paging.Limit, Total: count}
	return
}

func (r *storeStaffBindingRepository) Create(db *gorm.DB, t *models.StoreStaffBinding) error {
	return db.Create(t).Error
}

func (r *storeStaffBindingRepository) Updates(db *gorm.DB, id int64, columns map[string]any) error {
	return db.Model(&models.StoreStaffBinding{}).Where("id = ?", id).Updates(columns).Error
}

func (r *storeStaffBindingRepository) UpdatesInTenant(db *gorm.DB, id, tenantID int64, columns map[string]any) error {
	return db.Model(&models.StoreStaffBinding{}).Where("id = ? AND tenant_id = ?", id, tenantID).Updates(columns).Error
}
