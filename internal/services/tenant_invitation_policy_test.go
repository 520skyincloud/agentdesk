package services

import (
	"testing"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
)

func TestTenantInvitationUsableAtFailsClosedForMissingOrExpiredDeadline(t *testing.T) {
	now := time.Date(2026, time.July, 15, 9, 0, 0, 0, time.UTC)
	past := now.Add(-time.Second)
	future := now.Add(time.Second)
	tests := []struct {
		name       string
		invitation *models.TenantInvitation
		want       bool
	}{
		{name: "missing invitation", invitation: nil, want: false},
		{name: "missing expiry", invitation: &models.TenantInvitation{Status: enums.StatusOk}, want: false},
		{name: "expired", invitation: &models.TenantInvitation{Status: enums.StatusOk, ExpiresAt: &past}, want: false},
		{name: "disabled", invitation: &models.TenantInvitation{Status: enums.StatusDisabled, ExpiresAt: &future}, want: false},
		{name: "active", invitation: &models.TenantInvitation{Status: enums.StatusOk, ExpiresAt: &future}, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tenantInvitationUsableAt(tt.invitation, now); got != tt.want {
				t.Fatalf("tenantInvitationUsableAt()=%v want=%v", got, tt.want)
			}
		})
	}
}
