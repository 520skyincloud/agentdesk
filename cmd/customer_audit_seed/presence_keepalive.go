package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/enums"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	defaultSimulationPresenceKeepaliveInterval = time.Minute
	minimumSimulationPresenceKeepaliveInterval = 10 * time.Second
)

func runSimulationPresenceKeepalive(db *gorm.DB, batch string, interval time.Duration) error {
	if interval < minimumSimulationPresenceKeepaliveInterval {
		return fmt.Errorf("keepalive interval must be at least %s", minimumSimulationPresenceKeepaliveInterval)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	refresh := func(at time.Time) error {
		count, err := refreshSimulationPresence(db, batch, at)
		if err != nil {
			return err
		}
		slog.Info("simulation agent presence refreshed", "batch", batch, "agents", count, "nextRefreshIn", interval)
		return nil
	}
	if err := refresh(time.Now()); err != nil {
		return err
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			slog.Info("simulation agent presence keepalive stopped", "batch", batch)
			return nil
		case at := <-ticker.C:
			if err := refresh(at); err != nil {
				return err
			}
		}
	}
}

func refreshSimulationPresence(db *gorm.DB, batch string, at time.Time) (int, error) {
	if db == nil {
		return 0, fmt.Errorf("database is not initialized")
	}
	if sqls.DB() == nil || sqls.DB() != db {
		return 0, fmt.Errorf("simulation keepalive database is not registered")
	}
	batch = strings.TrimSpace(batch)
	if batch == "" {
		return 0, fmt.Errorf("batch cannot be empty")
	}

	refreshed := 0
	err := sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		tenant := &models.Tenant{}
		err := ctx.Tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("registration_type = ? AND registration_no = ?", tenantRegistrationType, tenantRegistrationNo).
			Take(tenant).Error
		if err != nil {
			if err == gorm.ErrRecordNotFound {
				return fmt.Errorf("%s simulation tenant does not exist", tenantLegalName)
			}
			return fmt.Errorf("load simulation tenant failed: %w", err)
		}
		if tenant.Status != enums.StatusOk || tenant.LegalName != tenantLegalName || !strings.Contains(tenant.Remark, marker(batch)) {
			return fmt.Errorf("tenant %d is not the enabled %s simulation tenant for batch %s", tenant.ID, tenantLegalName, batch)
		}

		usernames := simulationAgentUsernames()
		users := make([]models.User, 0, len(usernames))
		if err := ctx.Tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("tenant_id = ? AND username IN ? AND remark LIKE ? AND deleted_at IS NULL", tenant.ID, usernames, likeMarker(marker(batch))).
			Order("id ASC").Find(&users).Error; err != nil {
			return fmt.Errorf("load simulation agents failed: %w", err)
		}
		if len(users) != expectedSimulationPresenceCount {
			return fmt.Errorf("simulation agent count is %d, expected %d; rerun seed before keepalive", len(users), expectedSimulationPresenceCount)
		}

		userIDs := make([]int64, 0, len(users))
		for index := range users {
			userIDs = append(userIDs, users[index].ID)
		}
		profiles := make([]models.AgentProfile, 0, len(users))
		if err := ctx.Tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("tenant_id = ? AND user_id IN ? AND remark LIKE ?", tenant.ID, userIDs, likeMarker(marker(batch))).
			Order("id ASC").Find(&profiles).Error; err != nil {
			return fmt.Errorf("load simulation agent profiles failed: %w", err)
		}
		if len(profiles) != len(users) {
			return fmt.Errorf("simulation agent profile count is %d, expected %d; rerun seed before keepalive", len(profiles), len(users))
		}
		profileByUserID := make(map[int64]models.AgentProfile, len(profiles))
		for index := range profiles {
			profileByUserID[profiles[index].UserID] = profiles[index]
		}

		syntheticSessions := make([]models.AgentPresenceSession, 0, len(users))
		if err := ctx.Tx.Where("tenant_id = ? AND user_id IN ? AND source = ?", tenant.ID, userIDs, simulationPresenceSource).
			Order("user_id ASC, id DESC").Find(&syntheticSessions).Error; err != nil {
			return fmt.Errorf("load simulation presence sessions failed: %w", err)
		}
		canonicalByUserID := make(map[int64]models.AgentPresenceSession, len(users))
		duplicateIDs := make([]int64, 0)
		for index := range syntheticSessions {
			session := syntheticSessions[index]
			if _, exists := canonicalByUserID[session.UserID]; exists {
				duplicateIDs = append(duplicateIDs, session.ID)
				continue
			}
			canonicalByUserID[session.UserID] = session
		}
		if len(duplicateIDs) > 0 {
			if err := ctx.Tx.Where("tenant_id = ? AND id IN ?", tenant.ID, duplicateIDs).Delete(&models.AgentPresenceSession{}).Error; err != nil {
				return fmt.Errorf("remove duplicate simulation presence sessions failed: %w", err)
			}
		}

		activeSessions := make([]models.AgentPresenceSession, 0, len(users))
		if err := ctx.Tx.Where("tenant_id = ? AND user_id IN ? AND ended_at IS NULL", tenant.ID, userIDs).
			Order("user_id ASC, id DESC").Find(&activeSessions).Error; err != nil {
			return fmt.Errorf("load active simulation agent presence failed: %w", err)
		}
		for index := range activeSessions {
			session := activeSessions[index]
			canonical, exists := canonicalByUserID[session.UserID]
			if exists && session.ID == canonical.ID {
				continue
			}
			endedAt := at
			if endedAt.Before(session.StartedAt) {
				endedAt = session.StartedAt
			}
			if err := ctx.Tx.Model(&models.AgentPresenceSession{}).
				Where("id = ? AND tenant_id = ?", session.ID, tenant.ID).
				Updates(map[string]any{
					"ended_at": endedAt, "duration_seconds": simulationPresenceDuration(session.StartedAt, endedAt),
					"updated_at": at, "update_user_id": constants.SystemAuditUserID, "update_user_name": constants.SystemAuditUserName,
				}).Error; err != nil {
				return fmt.Errorf("close duplicate active presence session %d failed: %w", session.ID, err)
			}
		}

		for index := range users {
			user := users[index]
			profile, exists := profileByUserID[user.ID]
			if !exists {
				return fmt.Errorf("simulation user %d has no marked agent profile", user.ID)
			}
			canonical, exists := canonicalByUserID[user.ID]
			if !exists {
				item := &models.AgentPresenceSession{
					TenantID: tenant.ID, UserID: user.ID, AgentProfileID: profile.ID, TeamID: profile.TeamID,
					Status: enums.AgentPresenceStatusOnline, Source: simulationPresenceSource,
					ChangedBy: constants.SystemAuditUserID, StartedAt: at, LastSeenAt: at,
					AuditFields: simulationAuditFields(at),
				}
				if err := ctx.Tx.Create(item).Error; err != nil {
					return fmt.Errorf("create simulation presence for user %d failed: %w", user.ID, err)
				}
				refreshed++
				continue
			}

			startedAt := canonical.StartedAt
			if startedAt.IsZero() || startedAt.After(at) {
				startedAt = at
			}
			if err := ctx.Tx.Model(&models.AgentPresenceSession{}).
				Where("id = ? AND tenant_id = ? AND user_id = ?", canonical.ID, tenant.ID, user.ID).
				Updates(map[string]any{
					"agent_profile_id": profile.ID, "team_id": profile.TeamID,
					"status": enums.AgentPresenceStatusOnline, "source": simulationPresenceSource,
					"break_reason": "", "changed_by": constants.SystemAuditUserID,
					"started_at": startedAt, "last_seen_at": at, "ended_at": nil,
					"duration_seconds": simulationPresenceDuration(startedAt, at),
					"updated_at":       at, "update_user_id": constants.SystemAuditUserID, "update_user_name": constants.SystemAuditUserName,
				}).Error; err != nil {
				return fmt.Errorf("refresh simulation presence for user %d failed: %w", user.ID, err)
			}
			refreshed++
		}
		return nil
	})
	return refreshed, err
}

func simulationAgentUsernames() []string {
	ret := make([]string, 0, expectedSimulationPresenceCount)
	for index := 1; index <= expectedSimulationPresenceCount; index++ {
		ret = append(ret, fmt.Sprintf("%scs_user_%03d", usernamePrefix, index))
	}
	return ret
}

func simulationPresenceDuration(startedAt, endedAt time.Time) int64 {
	if startedAt.IsZero() || endedAt.Before(startedAt) {
		return 0
	}
	return int64(endedAt.Sub(startedAt).Seconds())
}
