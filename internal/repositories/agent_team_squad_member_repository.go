package repositories

import (
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

var AgentTeamSquadMemberRepository = &agentTeamSquadMemberRepository{}

type agentTeamSquadMemberRepository struct{}

func (r *agentTeamSquadMemberRepository) Take(db *gorm.DB, where ...any) *models.AgentTeamSquadMember {
	ret := &models.AgentTeamSquadMember{}
	if err := db.Take(ret, where...).Error; err != nil {
		return nil
	}
	return ret
}

func (r *agentTeamSquadMemberRepository) Find(db *gorm.DB, cnd *sqls.Cnd) (list []models.AgentTeamSquadMember) {
	cnd.Find(db, &list)
	return
}

func (r *agentTeamSquadMemberRepository) Create(db *gorm.DB, item *models.AgentTeamSquadMember) error {
	return db.Create(item).Error
}

func (r *agentTeamSquadMemberRepository) Updates(db *gorm.DB, id int64, columns map[string]any) error {
	return db.Model(&models.AgentTeamSquadMember{}).Where("id = ?", id).Updates(columns).Error
}

func (r *agentTeamSquadMemberRepository) UpdatesInTenant(db *gorm.DB, id, tenantID int64, columns map[string]any) error {
	return db.Model(&models.AgentTeamSquadMember{}).Where("id = ? AND tenant_id = ?", id, tenantID).Updates(columns).Error
}

func (r *agentTeamSquadMemberRepository) UpdatesActiveBySquadInTenant(db *gorm.DB, squadID, tenantID int64, columns map[string]any) error {
	return db.Model(&models.AgentTeamSquadMember{}).
		Where("squad_id = ? AND tenant_id = ? AND status <> ?", squadID, tenantID, enums.StatusDeleted).
		Updates(columns).Error
}
