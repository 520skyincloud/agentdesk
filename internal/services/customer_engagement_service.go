package services

import (
	"encoding/json"
	"strings"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
)

const (
	EngagementConsentUnknown = "unknown"
	EngagementConsentGranted = "granted"
	EngagementConsentDenied  = "denied"
)

type customerEngagementService struct{}

var CustomerEngagementService = &customerEngagementService{}

func (s *customerEngagementService) GetOrCreate(customerID, storeID int64) (*models.CustomerEngagementProfile, error) {
	item := repositories.CustomerEngagementProfileRepository.FindByCustomerStore(sqls.DB(), customerID, storeID)
	if item != nil {
		return item, nil
	}
	item = &models.CustomerEngagementProfile{
		CustomerID:      customerID,
		StoreID:         storeID,
		ConsentStatus:   EngagementConsentUnknown,
		TagsJSON:        "{}",
		PreferencesJSON: "{}",
		AuditFields: models.AuditFields{
			CreatedAt:      time.Now(),
			UpdatedAt:      time.Now(),
			CreateUserName: "system",
			UpdateUserName: "system",
		},
	}
	if err := repositories.CustomerEngagementProfileRepository.Create(sqls.DB(), item); err != nil {
		return nil, err
	}
	return item, nil
}

func (s *customerEngagementService) SetConsent(customerID, storeID int64, granted bool) (*models.CustomerEngagementProfile, error) {
	item, err := s.GetOrCreate(customerID, storeID)
	if err != nil {
		return nil, err
	}
	item.ConsentStatus = EngagementConsentDenied
	if granted {
		item.ConsentStatus = EngagementConsentGranted
		item.UnsubscribedAt = nil
	} else {
		now := time.Now()
		item.UnsubscribedAt = &now
	}
	item.UpdatedAt = time.Now()
	if err := repositories.CustomerEngagementProfileRepository.Update(sqls.DB(), item); err != nil {
		return nil, err
	}
	return item, nil
}

func (s *customerEngagementService) UpdatePreferences(customerID, storeID int64, preferences map[string]any) (*models.CustomerEngagementProfile, error) {
	item, err := s.GetOrCreate(customerID, storeID)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(preferences)
	if err != nil {
		return nil, err
	}
	item.PreferencesJSON = string(raw)
	item.UpdatedAt = time.Now()
	if err := repositories.CustomerEngagementProfileRepository.Update(sqls.DB(), item); err != nil {
		return nil, err
	}
	return item, nil
}

func (s *customerEngagementService) CanContact(customerID, storeID int64, minInterval time.Duration) (bool, string) {
	item := repositories.CustomerEngagementProfileRepository.FindByCustomerStore(sqls.DB(), customerID, storeID)
	if item == nil {
		return false, "consent_unknown"
	}
	if item.ConsentStatus != EngagementConsentGranted || item.UnsubscribedAt != nil {
		return false, "not_consented"
	}
	if minInterval <= 0 {
		minInterval = 24 * time.Hour
	}
	if item.LastContactAt != nil && time.Since(*item.LastContactAt) < minInterval {
		return false, "frequency_limit"
	}
	return true, ""
}

func (s *customerEngagementService) MarkContacted(item *models.CustomerEngagementProfile) error {
	if item == nil {
		return nil
	}
	now := time.Now()
	item.LastContactAt = &now
	item.ContactCount++
	item.UpdatedAt = now
	return repositories.CustomerEngagementProfileRepository.Update(sqls.DB(), item)
}

func NormalizeEngagementTag(value string) string {
	return strings.TrimSpace(strings.ToLower(value))
}
