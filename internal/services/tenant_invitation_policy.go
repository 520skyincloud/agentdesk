package services

import (
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/enums"
)

func tenantInvitationExpiresAt(now time.Time) *time.Time {
	expiresAt := now.Add(time.Duration(constants.TenantInvitationValidityDays) * 24 * time.Hour)
	return &expiresAt
}

func tenantInvitationExpiredAt(invitation *models.TenantInvitation, now time.Time) bool {
	return invitation == nil || invitation.ExpiresAt == nil || !invitation.ExpiresAt.After(now)
}

func tenantInvitationUsableAt(invitation *models.TenantInvitation, now time.Time) bool {
	return invitation != nil && invitation.Status == enums.StatusOk && !tenantInvitationExpiredAt(invitation, now)
}
