package migration

import (
	"fmt"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

func init() {
	register(39, "backfill agent organization tenants", func() error {
		return sqls.WithTransaction(func(ctx *sqls.TxContext) error {
			return backfillAgentOrganizationTenants(ctx.Tx)
		})
	})
}

func backfillAgentOrganizationTenants(tx *gorm.DB) error {
	teams, err := loadAgentOrganizationTeams(tx)
	if err != nil {
		return err
	}
	profiles, err := backfillAgentProfileTenants(tx, teams)
	if err != nil {
		return err
	}
	squads, err := backfillAgentTeamSquadTenants(tx, teams)
	if err != nil {
		return err
	}
	if err := backfillAgentTeamSquadMemberTenants(tx, squads, profiles); err != nil {
		return err
	}
	return backfillAgentTeamScheduleTenants(tx, teams, squads)
}

func loadAgentOrganizationTeams(tx *gorm.DB) (map[int64]models.AgentTeam, error) {
	var list []models.AgentTeam
	if err := tx.Order("id ASC").Find(&list).Error; err != nil {
		return nil, err
	}
	ret := make(map[int64]models.AgentTeam, len(list))
	for _, item := range list {
		if item.TenantID <= 0 {
			return nil, fmt.Errorf("agent team %d has no tenant; migration 36 must complete first", item.ID)
		}
		if item.LeaderUserID > 0 {
			var leader models.User
			if err := tx.Take(&leader, "id = ?", item.LeaderUserID).Error; err != nil {
				return nil, fmt.Errorf("agent team %d leader %d: %w", item.ID, item.LeaderUserID, err)
			}
			if leader.TenantID != item.TenantID {
				return nil, fmt.Errorf("agent team %d leader tenant %d conflicts with team tenant %d", item.ID, leader.TenantID, item.TenantID)
			}
		}
		ret[item.ID] = item
	}
	return ret, nil
}

func backfillAgentProfileTenants(tx *gorm.DB, teams map[int64]models.AgentTeam) (map[int64]models.AgentProfile, error) {
	var list []models.AgentProfile
	if err := tx.Order("id ASC").Find(&list).Error; err != nil {
		return nil, err
	}
	ret := make(map[int64]models.AgentProfile, len(list))
	for i := range list {
		item := &list[i]
		var user models.User
		if err := tx.Take(&user, "id = ?", item.UserID).Error; err != nil {
			return nil, fmt.Errorf("agent profile %d user %d: %w", item.ID, item.UserID, err)
		}
		if user.TenantID <= 0 {
			return nil, fmt.Errorf("agent profile %d user %d has no tenant", item.ID, user.ID)
		}
		resolvedTenantID := user.TenantID
		if item.TeamID > 0 {
			team, ok := teams[item.TeamID]
			if !ok {
				return nil, fmt.Errorf("agent profile %d references missing team %d", item.ID, item.TeamID)
			}
			if team.TenantID != user.TenantID {
				return nil, fmt.Errorf("agent profile %d user tenant %d conflicts with team tenant %d", item.ID, user.TenantID, team.TenantID)
			}
			resolvedTenantID = team.TenantID
		}
		if err := ensureAgentOrganizationTenant(tx, &models.AgentProfile{}, item.ID, item.TenantID, resolvedTenantID); err != nil {
			return nil, fmt.Errorf("agent profile %d: %w", item.ID, err)
		}
		item.TenantID = resolvedTenantID
		ret[item.ID] = *item
	}
	return ret, nil
}

func backfillAgentTeamSquadTenants(tx *gorm.DB, teams map[int64]models.AgentTeam) (map[int64]models.AgentTeamSquad, error) {
	var list []models.AgentTeamSquad
	if err := tx.Order("id ASC").Find(&list).Error; err != nil {
		return nil, err
	}
	ret := make(map[int64]models.AgentTeamSquad, len(list))
	for i := range list {
		item := &list[i]
		team, ok := teams[item.TeamID]
		if !ok {
			return nil, fmt.Errorf("agent team squad %d references missing team %d", item.ID, item.TeamID)
		}
		if err := ensureAgentOrganizationTenant(tx, &models.AgentTeamSquad{}, item.ID, item.TenantID, team.TenantID); err != nil {
			return nil, fmt.Errorf("agent team squad %d: %w", item.ID, err)
		}
		if item.LeaderUserID > 0 {
			var leader models.User
			if err := tx.Take(&leader, "id = ?", item.LeaderUserID).Error; err != nil {
				return nil, fmt.Errorf("agent team squad %d leader %d: %w", item.ID, item.LeaderUserID, err)
			}
			if leader.TenantID != team.TenantID {
				return nil, fmt.Errorf("agent team squad %d leader tenant %d conflicts with team tenant %d", item.ID, leader.TenantID, team.TenantID)
			}
		}
		item.TenantID = team.TenantID
		ret[item.ID] = *item
	}
	return ret, nil
}

func backfillAgentTeamSquadMemberTenants(tx *gorm.DB, squads map[int64]models.AgentTeamSquad, profiles map[int64]models.AgentProfile) error {
	var list []models.AgentTeamSquadMember
	if err := tx.Order("id ASC").Find(&list).Error; err != nil {
		return err
	}
	for i := range list {
		item := &list[i]
		squad, ok := squads[item.SquadID]
		if !ok {
			return fmt.Errorf("agent team squad member %d references missing squad %d", item.ID, item.SquadID)
		}
		profile, ok := profiles[item.AgentProfileID]
		if !ok {
			return fmt.Errorf("agent team squad member %d references missing profile %d", item.ID, item.AgentProfileID)
		}
		if profile.TeamID != squad.TeamID || profile.TenantID != squad.TenantID {
			return fmt.Errorf("agent team squad member %d crosses team or tenant boundary", item.ID)
		}
		if err := ensureAgentOrganizationTenant(tx, &models.AgentTeamSquadMember{}, item.ID, item.TenantID, squad.TenantID); err != nil {
			return fmt.Errorf("agent team squad member %d: %w", item.ID, err)
		}
	}
	return nil
}

func backfillAgentTeamScheduleTenants(tx *gorm.DB, teams map[int64]models.AgentTeam, squads map[int64]models.AgentTeamSquad) error {
	var list []models.AgentTeamSchedule
	if err := tx.Order("id ASC").Find(&list).Error; err != nil {
		return err
	}
	for i := range list {
		item := &list[i]
		team, ok := teams[item.TeamID]
		if !ok {
			return fmt.Errorf("agent team schedule %d references missing team %d", item.ID, item.TeamID)
		}
		if item.SquadID > 0 {
			squad, ok := squads[item.SquadID]
			if !ok {
				return fmt.Errorf("agent team schedule %d references missing squad %d", item.ID, item.SquadID)
			}
			if squad.TeamID != team.ID || squad.TenantID != team.TenantID {
				return fmt.Errorf("agent team schedule %d squad crosses team or tenant boundary", item.ID)
			}
		}
		if err := ensureAgentOrganizationTenant(tx, &models.AgentTeamSchedule{}, item.ID, item.TenantID, team.TenantID); err != nil {
			return fmt.Errorf("agent team schedule %d: %w", item.ID, err)
		}
	}
	return nil
}

func ensureAgentOrganizationTenant(tx *gorm.DB, model any, id, currentTenantID, resolvedTenantID int64) error {
	if resolvedTenantID <= 0 {
		return fmt.Errorf("resolved tenant is empty")
	}
	if currentTenantID > 0 {
		if currentTenantID != resolvedTenantID {
			return fmt.Errorf("explicit tenant %d conflicts with resolved tenant %d", currentTenantID, resolvedTenantID)
		}
		return nil
	}
	now := time.Now()
	result := tx.Model(model).Where("id = ? AND tenant_id = ?", id, 0).Updates(map[string]any{
		"tenant_id":        resolvedTenantID,
		"update_user_id":   constants.SystemAuditUserID,
		"update_user_name": constants.SystemAuditUserName,
		"updated_at":       now,
	})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return fmt.Errorf("tenant backfill did not update the expected row")
	}
	return nil
}
