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
	RecoveryStatusPending    = "pending"
	RecoveryStatusApproved   = "approved"
	RecoveryStatusRejected   = "rejected"
	RecoveryStatusCompleted  = "completed"
	RecoveryTypeCoupon       = "coupon"
	RecoveryTypeUpgrade      = "room_upgrade"
	RecoveryTypeLateCheckout = "late_checkout"
	RecoveryTypeRefund       = "refund"
)

type ServiceRecoveryRequest struct {
	ConversationID int64
	TicketID       int64
	CustomerID     int64
	StoreID        int64
	RecoveryType   string
	IdempotencyKey string
	Reason         string
	Payload        map[string]any
}

type serviceRecoveryService struct{}

var ServiceRecoveryService = &serviceRecoveryService{}

// CreateRequest creates an auditable, idempotent recovery request. It does not
// call an external compensation provider.
func (s *serviceRecoveryService) CreateRequest(req ServiceRecoveryRequest) (*models.ServiceRecoveryCase, error) {
	key := strings.TrimSpace(req.IdempotencyKey)
	if key == "" {
		key = "conversation:" + formatInt64(req.ConversationID) + ":ticket:" + formatInt64(req.TicketID) + ":" + strings.TrimSpace(req.RecoveryType)
	}
	if existing := repositories.ServiceRecoveryCaseRepository.FindOne(sqls.DB(), sqls.NewCnd().Eq("idempotency_key", key)); existing != nil {
		return existing, nil
	}
	payload := ""
	if len(req.Payload) > 0 {
		encoded, err := json.Marshal(req.Payload)
		if err != nil {
			return nil, err
		}
		payload = string(encoded)
	}
	item := &models.ServiceRecoveryCase{
		ConversationID: req.ConversationID,
		TicketID:       req.TicketID,
		CustomerID:     req.CustomerID,
		StoreID:        req.StoreID,
		RecoveryType:   strings.TrimSpace(req.RecoveryType),
		Status:         RecoveryStatusPending,
		ApprovalStatus: RecoveryStatusPending,
		IdempotencyKey: key,
		Reason:         strings.TrimSpace(req.Reason),
		Payload:        payload,
		RequestedAt:    time.Now(),
	}
	if err := repositories.ServiceRecoveryCaseRepository.Create(sqls.DB(), item); err != nil {
		if existing := repositories.ServiceRecoveryCaseRepository.FindOne(sqls.DB(), sqls.NewCnd().Eq("idempotency_key", key)); existing != nil {
			return existing, nil
		}
		return nil, err
	}
	return item, nil
}

func formatInt64(value int64) string {
	if value <= 0 {
		return "0"
	}
	const digits = "0123456789"
	var buf [20]byte
	index := len(buf)
	for value > 0 {
		index--
		buf[index] = digits[value%10]
		value /= 10
	}
	return string(buf[index:])
}
