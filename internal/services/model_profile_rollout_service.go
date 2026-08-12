package services

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

const (
	modelProfileAutomaticRolloutPending = "system:model_profile_rollout"
	modelProfileAutomaticRolloutRunning = "system:model_profile_rollout:running"
	modelProfileAutomaticRolloutRetry   = time.Minute
	modelProfileAutomaticRolloutLease   = 5 * time.Minute
	modelProfileAutomaticRolloutTimeout = 3 * time.Minute
)

var ModelProfileRolloutService = &modelProfileRolloutService{}

type modelProfileRolloutService struct{}

func (s *modelProfileRolloutService) ReconcilePublishedProfiles() int {
	profiles := repositories.ModelProfileTemplateRepository.Find(sqls.DB(), sqls.NewCnd().
		In("status", []enums.ModelProfileStatus{enums.ModelProfileStatusCandidate, enums.ModelProfileStatusActive}).
		Asc("code").Desc("revision").Desc("id"))
	latestByCode := make(map[string]models.ModelProfileTemplate, len(profiles))
	for i := range profiles {
		if _, exists := latestByCode[profiles[i].Code]; exists {
			continue
		}
		latestByCode[profiles[i].Code] = profiles[i]
	}
	now := time.Now()
	scheduled := make([]models.StoreModelProfileAssignment, 0)
	err := sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		for _, target := range latestByCode {
			items, err := s.ScheduleFollowersDB(ctx.Tx, &target, now)
			if err != nil {
				return err
			}
			scheduled = append(scheduled, items...)
		}
		return nil
	})
	if err != nil {
		slog.Warn("reconcile automatic model profile rollouts failed", "error", err)
		return 0
	}
	for i := range scheduled {
		WsService.PublishStoreModelProfileChanged(
			scheduled[i].TenantID,
			scheduled[i].StoreID,
			scheduled[i].PendingTemplateID,
			scheduled[i].PendingTemplateRevision,
			"pending",
			now,
		)
	}
	return len(scheduled)
}

func (s *modelProfileRolloutService) ScheduleFollowersDB(db *gorm.DB, target *models.ModelProfileTemplate, now time.Time) ([]models.StoreModelProfileAssignment, error) {
	if db == nil || target == nil || target.ID <= 0 || strings.TrimSpace(target.Code) == "" {
		return nil, errors.New("model profile rollout target is required")
	}
	predecessors := repositories.ModelProfileTemplateRepository.Find(db, sqls.NewCnd().
		Eq("code", target.Code).
		NotEq("id", target.ID).
		In("status", []enums.ModelProfileStatus{enums.ModelProfileStatusActive, enums.ModelProfileStatusCandidate}))
	predecessorIDs := make([]int64, 0, len(predecessors))
	for i := range predecessors {
		if predecessors[i].Revision < target.Revision {
			predecessorIDs = append(predecessorIDs, predecessors[i].ID)
		}
	}
	assignments := repositories.StoreModelProfileAssignmentRepository.FindByTemplateIDs(db, predecessorIDs)
	scheduled := make([]models.StoreModelProfileAssignment, 0, len(assignments))
	for i := range assignments {
		assignment := assignments[i]
		if assignment.Status != enums.StoreModelAssignmentStatusReady || assignment.TemplateID <= 0 ||
			assignment.TemplateRevision <= 0 || assignment.TemplateRevision >= target.Revision {
			continue
		}
		if assignment.PendingTemplateID == target.ID &&
			assignment.PendingTemplateRevision == target.Revision &&
			(assignment.PendingRequestedByName == modelProfileAutomaticRolloutPending ||
				assignment.PendingRequestedByName == modelProfileAutomaticRolloutRunning) {
			continue
		}
		// A human-selected pending revision is an explicit override and must win.
		if assignment.PendingTemplateID > 0 &&
			assignment.PendingRequestedByName != modelProfileAutomaticRolloutPending &&
			assignment.PendingRequestedByName != modelProfileAutomaticRolloutRunning {
			continue
		}
		if err := repositories.StoreModelProfileAssignmentRepository.Updates(db, assignment.ID, map[string]any{
			"pending_template_id":       target.ID,
			"pending_template_revision": target.Revision,
			"pending_requested_at":      now,
			"pending_requested_by":      constants.SystemAuditUserID,
			"pending_requested_by_name": modelProfileAutomaticRolloutPending,
			"last_error_class":          "",
			"last_error_message":        "",
			"updated_at":                now,
			"update_user_id":            constants.SystemAuditUserID,
			"update_user_name":          constants.SystemAuditUserName,
		}); err != nil {
			return nil, err
		}
		assignment.PendingTemplateID = target.ID
		assignment.PendingTemplateRevision = target.Revision
		assignment.PendingRequestedAt = &now
		assignment.PendingRequestedBy = constants.SystemAuditUserID
		assignment.PendingRequestedByName = modelProfileAutomaticRolloutPending
		assignment.LastErrorClass = ""
		assignment.LastErrorMessage = ""
		assignment.UpdatedAt = now
		scheduled = append(scheduled, assignment)
	}
	return scheduled, nil
}

func (s *modelProfileRolloutService) ProcessDue(limit int) int {
	if limit <= 0 {
		limit = 5
	}
	s.ReconcilePublishedProfiles()
	now := time.Now()
	items := repositories.StoreModelProfileAssignmentRepository.FindAutomaticPending(
		sqls.DB(),
		modelProfileAutomaticRolloutPending,
		modelProfileAutomaticRolloutRunning,
		now.Add(-modelProfileAutomaticRolloutRetry),
		now.Add(-modelProfileAutomaticRolloutLease),
		limit,
	)
	count := 0
	for i := range items {
		if !s.claim(items[i], now) {
			continue
		}
		count++
		ctx, cancel := context.WithTimeout(context.Background(), modelProfileAutomaticRolloutTimeout)
		err := StoreModelCredentialService.ActivatePendingProfileAutomatically(ctx, items[i])
		cancel()
		if err != nil {
			s.markRetry(items[i], err)
		}
	}
	return count
}

func (s *modelProfileRolloutService) claim(item models.StoreModelProfileAssignment, now time.Time) bool {
	result := sqls.DB().Model(&models.StoreModelProfileAssignment{}).
		Where(
			"id = ? AND pending_template_id = ? AND pending_template_revision = ? AND pending_requested_by_name = ? AND updated_at = ?",
			item.ID,
			item.PendingTemplateID,
			item.PendingTemplateRevision,
			item.PendingRequestedByName,
			item.UpdatedAt,
		).
		Updates(map[string]any{
			"pending_requested_by_name": modelProfileAutomaticRolloutRunning,
			"updated_at":                now,
			"update_user_id":            constants.SystemAuditUserID,
			"update_user_name":          constants.SystemAuditUserName,
		})
	return result.Error == nil && result.RowsAffected == 1
}

func (s *modelProfileRolloutService) markRetry(item models.StoreModelProfileAssignment, runErr error) {
	message := strings.TrimSpace(runErr.Error())
	if len(message) > 500 {
		message = message[:500]
	}
	now := time.Now()
	result := sqls.DB().Model(&models.StoreModelProfileAssignment{}).
		Where(
			"id = ? AND pending_template_id = ? AND pending_template_revision = ? AND pending_requested_by_name = ?",
			item.ID,
			item.PendingTemplateID,
			item.PendingTemplateRevision,
			modelProfileAutomaticRolloutRunning,
		).
		Updates(map[string]any{
			"pending_requested_by_name": modelProfileAutomaticRolloutPending,
			"last_error_class":          "automatic_profile_rollout_failed",
			"last_error_message":        message,
			"updated_at":                now,
			"update_user_id":            constants.SystemAuditUserID,
			"update_user_name":          constants.SystemAuditUserName,
		})
	if result.Error != nil {
		slog.Warn("mark automatic model profile rollout retry failed", "assignment_id", item.ID, "error", result.Error)
		return
	}
	slog.Warn(
		"automatic model profile rollout failed; active revision preserved",
		"assignment_id", item.ID,
		"tenant_id", item.TenantID,
		"store_id", item.StoreID,
		"target_profile_id", item.PendingTemplateID,
		"target_profile_revision", item.PendingTemplateRevision,
		"error", runErr,
	)
}
