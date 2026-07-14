package migration

import (
	"encoding/json"
	"fmt"
	"strings"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

func init() {
	register(47, "backfill wxwork kf sync state tenants", func() error {
		return sqls.WithTransaction(func(ctx *sqls.TxContext) error {
			return backfillWxWorkKFSyncStateTenants(ctx.Tx)
		})
	})
}

type wxWorkKFSyncStateTenantEvidence struct {
	ChannelID int64
	TenantID  int64
}

func backfillWxWorkKFSyncStateTenants(tx *gorm.DB) error {
	validTenantIDs, err := loadValidTenantIDs(tx)
	if err != nil {
		return err
	}

	var states []models.WxWorkKFSyncState
	if err := tx.Order("id ASC").Find(&states).Error; err != nil {
		return err
	}
	if len(states) == 0 {
		return nil
	}

	wantedOpenKfIDs := make(map[string]struct{}, len(states))
	for i := range states {
		openKfID := strings.TrimSpace(states[i].OpenKfID)
		if openKfID == "" {
			return fmt.Errorf("wxwork kf sync state %d has no open_kf_id", states[i].ID)
		}
		wantedOpenKfIDs[openKfID] = struct{}{}
	}

	var channels []models.Channel
	if err := tx.Where("channel_type = ?", enums.ChannelTypeWxWorkKF).Order("id ASC").Find(&channels).Error; err != nil {
		return err
	}
	evidenceByOpenKfID := make(map[string]wxWorkKFSyncStateTenantEvidence, len(states))
	for i := range channels {
		var config struct {
			OpenKfID string `json:"openKfId"`
		}
		if err := json.Unmarshal([]byte(strings.TrimSpace(channels[i].ConfigJSON)), &config); err != nil {
			continue
		}
		openKfID := strings.TrimSpace(config.OpenKfID)
		if _, wanted := wantedOpenKfIDs[openKfID]; !wanted {
			continue
		}
		if channels[i].TenantID <= 0 {
			return fmt.Errorf("wxwork kf channel %d for open_kf_id %q has no tenant", channels[i].ID, openKfID)
		}
		if _, ok := validTenantIDs[channels[i].TenantID]; !ok {
			return fmt.Errorf("wxwork kf channel %d references missing tenant %d", channels[i].ID, channels[i].TenantID)
		}
		if previous, ok := evidenceByOpenKfID[openKfID]; ok {
			if previous.TenantID != channels[i].TenantID {
				return fmt.Errorf("open_kf_id %q maps to channel %d tenant %d and channel %d tenant %d", openKfID, previous.ChannelID, previous.TenantID, channels[i].ID, channels[i].TenantID)
			}
			continue
		}
		evidenceByOpenKfID[openKfID] = wxWorkKFSyncStateTenantEvidence{ChannelID: channels[i].ID, TenantID: channels[i].TenantID}
	}

	for i := range states {
		item := &states[i]
		openKfID := strings.TrimSpace(item.OpenKfID)
		evidence, ok := evidenceByOpenKfID[openKfID]
		if !ok {
			return fmt.Errorf("wxwork kf sync state %d open_kf_id %q has no channel tenant evidence", item.ID, openKfID)
		}
		resolver := newConversationDomainTenantResolver("wxwork kf sync state", item.ID, item.TenantID, validTenantIDs)
		if err := resolver.merge("channel", evidence.ChannelID, evidence.TenantID); err != nil {
			return err
		}
		tenantID, err := resolver.resolve(0)
		if err != nil {
			return err
		}
		if err := assignConversationDomainTenant(tx, &models.WxWorkKFSyncState{}, "wxwork kf sync state", item.ID, item.TenantID, tenantID, validTenantIDs); err != nil {
			return err
		}
	}
	return nil
}
