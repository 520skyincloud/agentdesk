package repositories

import (
	"errors"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/httpx/params"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var AgentProfileRepository = newAgentProfileRepository()

func newAgentProfileRepository() *agentProfileRepository {
	return &agentProfileRepository{}
}

type agentProfileRepository struct {
}

func (r *agentProfileRepository) Get(db *gorm.DB, id int64) *models.AgentProfile {
	ret := &models.AgentProfile{}
	if err := db.First(ret, "id = ?", id).Error; err != nil {
		return nil
	}
	return ret
}

func (r *agentProfileRepository) GetInTenant(db *gorm.DB, id, tenantID int64) *models.AgentProfile {
	if id <= 0 || tenantID <= 0 {
		return nil
	}
	return r.Take(db, "id = ? AND tenant_id = ?", id, tenantID)
}

func (r *agentProfileRepository) GetForUpdateInTenant(db *gorm.DB, id, tenantID int64) (*models.AgentProfile, error) {
	if id <= 0 || tenantID <= 0 {
		return nil, nil
	}
	ret := &models.AgentProfile{}
	err := db.Clauses(clause.Locking{Strength: "UPDATE"}).First(ret, "id = ? AND tenant_id = ?", id, tenantID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return ret, nil
}

func (r *agentProfileRepository) Take(db *gorm.DB, where ...interface{}) *models.AgentProfile {
	ret := &models.AgentProfile{}
	if err := db.Take(ret, where...).Error; err != nil {
		return nil
	}
	return ret
}

func (r *agentProfileRepository) Find(db *gorm.DB, cnd *sqls.Cnd) (list []models.AgentProfile) {
	cnd.Find(db, &list)
	return
}

func (r *agentProfileRepository) FindOne(db *gorm.DB, cnd *sqls.Cnd) *models.AgentProfile {
	ret := &models.AgentProfile{}
	if err := cnd.FindOne(db, &ret); err != nil {
		return nil
	}
	return ret
}

func (r *agentProfileRepository) FindPageByParams(db *gorm.DB, params *params.QueryParams) (list []models.AgentProfile, paging *sqls.Paging) {
	return r.FindPageByCnd(db, &params.Cnd)
}

func (r *agentProfileRepository) FindPageByCnd(db *gorm.DB, cnd *sqls.Cnd) (list []models.AgentProfile, paging *sqls.Paging) {
	cnd.Find(db, &list)
	count := cnd.Count(db, &models.AgentProfile{})

	paging = &sqls.Paging{
		Page:  cnd.Paging.Page,
		Limit: cnd.Paging.Limit,
		Total: count,
	}
	return
}

func (r *agentProfileRepository) FindBySql(db *gorm.DB, sqlStr string, paramArr ...interface{}) (list []models.AgentProfile) {
	db.Raw(sqlStr, paramArr...).Scan(&list)
	return
}

func (r *agentProfileRepository) CountBySql(db *gorm.DB, sqlStr string, paramArr ...interface{}) (count int64) {
	db.Raw(sqlStr, paramArr...).Count(&count)
	return
}

func (r *agentProfileRepository) Count(db *gorm.DB, cnd *sqls.Cnd) int64 {
	return cnd.Count(db, &models.AgentProfile{})
}

func (r *agentProfileRepository) Create(db *gorm.DB, t *models.AgentProfile) (err error) {
	err = db.Create(t).Error
	return
}

func (r *agentProfileRepository) Update(db *gorm.DB, t *models.AgentProfile) (err error) {
	err = db.Save(t).Error
	return
}

func (r *agentProfileRepository) Updates(db *gorm.DB, id int64, columns map[string]interface{}) (err error) {
	err = db.Model(&models.AgentProfile{}).Where("id = ?", id).Updates(columns).Error
	return
}

func (r *agentProfileRepository) UpdatesInTenant(db *gorm.DB, id, tenantID int64, columns map[string]any) error {
	return db.Model(&models.AgentProfile{}).Where("id = ? AND tenant_id = ?", id, tenantID).Updates(columns).Error
}

func (r *agentProfileRepository) UpdateColumn(db *gorm.DB, id int64, name string, value interface{}) (err error) {
	err = db.Model(&models.AgentProfile{}).Where("id = ?", id).UpdateColumn(name, value).Error
	return
}

func (r *agentProfileRepository) Delete(db *gorm.DB, id int64) {
	db.Delete(&models.AgentProfile{}, "id = ?", id)
}

func (r *agentProfileRepository) DeleteInTenant(db *gorm.DB, id, tenantID int64) error {
	return db.Delete(&models.AgentProfile{}, "id = ? AND tenant_id = ?", id, tenantID).Error
}
