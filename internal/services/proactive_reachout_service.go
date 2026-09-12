package services

import (
	"strings"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
)

type ProactiveReachoutRequest struct {
	CustomerID     int64
	StoreID        int64
	CampaignCode   string
	IdempotencyKey string
	Content        string
	Reason         string
	ScheduledAt    *time.Time
}

type proactiveReachoutService struct{}

var ProactiveReachoutService = &proactiveReachoutService{}

// Draft creates an auditable suggestion only. Sending is deliberately kept in
// the existing Outbox path and requires consent/frequency checks elsewhere.
func (s *proactiveReachoutService) Draft(req ProactiveReachoutRequest) (*models.ProactiveReachout, error) {
	key := strings.TrimSpace(req.IdempotencyKey)
	if key == "" {
		key = "customer:" + formatInt64(req.CustomerID) + ":store:" + formatInt64(req.StoreID) + ":" + strings.TrimSpace(req.CampaignCode)
	}
	if existing := repositories.ProactiveReachoutRepository.FindByIdempotencyKey(sqls.DB(), key); existing != nil {
		return existing, nil
	}
	item := &models.ProactiveReachout{
		CustomerID:     req.CustomerID,
		StoreID:        req.StoreID,
		CampaignCode:   strings.TrimSpace(req.CampaignCode),
		IdempotencyKey: key,
		Status:         "draft",
		Content:        strings.TrimSpace(req.Content),
		Reason:         strings.TrimSpace(req.Reason),
		ScheduledAt:    req.ScheduledAt,
		AuditFields: models.AuditFields{
			CreatedAt:      time.Now(),
			UpdatedAt:      time.Now(),
			CreateUserName: "system",
			UpdateUserName: "system",
		},
	}
	if err := repositories.ProactiveReachoutRepository.Create(sqls.DB(), item); err != nil {
		if existing := repositories.ProactiveReachoutRepository.FindByIdempotencyKey(sqls.DB(), key); existing != nil {
			return existing, nil
		}
		return nil, err
	}
	return item, nil
}
