package repositories

import (
	"agent-desk/internal/models"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

var AgentTeamSquadRepository = &agentTeamSquadRepository{}

type agentTeamSquadRepository struct{}

func (r *agentTeamSquadRepository) Get(db *gorm.DB, id int64) *models.AgentTeamSquad {
	ret := &models.AgentTeamSquad{}
	if err := db.First(ret, "id = ?", id).Error; err != nil {
		return nil
	}
	return ret
}

func (r *agentTeamSquadRepository) GetInTenant(db *gorm.DB, id, tenantID int64) *models.AgentTeamSquad {
	if id <= 0 || tenantID <= 0 {
		return nil
	}
	return r.Take(db, "id = ? AND tenant_id = ?", id, tenantID)
}

func (r *agentTeamSquadRepository) Take(db *gorm.DB, where ...any) *models.AgentTeamSquad {
	ret := &models.AgentTeamSquad{}
	if err := db.Take(ret, where...).Error; err != nil {
		return nil
	}
	return ret
}

func (r *agentTeamSquadRepository) Find(db *gorm.DB, cnd *sqls.Cnd) (list []models.AgentTeamSquad) {
	cnd.Find(db, &list)
	return
}

func (r *agentTeamSquadRepository) Create(db *gorm.DB, item *models.AgentTeamSquad) error {
	return db.Create(item).Error
}

func (r *agentTeamSquadRepository) Updates(db *gorm.DB, id int64, columns map[string]any) error {
	return db.Model(&models.AgentTeamSquad{}).Where("id = ?", id).Updates(columns).Error
}

func (r *agentTeamSquadRepository) UpdatesInTenant(db *gorm.DB, id, tenantID int64, columns map[string]any) error {
	return db.Model(&models.AgentTeamSquad{}).Where("id = ? AND tenant_id = ?", id, tenantID).Updates(columns).Error
}
