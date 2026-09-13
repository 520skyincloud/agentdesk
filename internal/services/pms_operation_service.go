package services

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/config"
	"agent-desk/internal/pms"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm/clause"
)

const (
	PMSOperationTypeRenew = "renew"
	PMSOperationPending   = "pending"
	PMSOperationConfirmed = "confirmed"
	PMSOperationSucceeded = "succeeded"
	PMSOperationFailed    = "failed"
	PMSOperationCancelled = "cancelled"
	PMSOperationExpired   = "expired"
)

var PMSOperationService = newPMSOperationService()

type pmsOperationService struct{}

func newPMSOperationService() *pmsOperationService {
	return &pmsOperationService{}
}

type PMSRenewDraft struct {
	OperationID    int64
	PreviewText    string
	IdempotencyKey string
}

type PMSRenewOutcome struct {
	Status     string
	ReplyText  string
	Operation  *models.PMSOperation
	ResultData any
}

func (s *pmsOperationService) CreateRenewDraft(conversationID, sourceMessageID int64, request pms.RenewRequest, preview string) (*PMSRenewDraft, error) {
	if conversationID <= 0 || sourceMessageID <= 0 {
		return nil, fmt.Errorf("续住操作缺少会话或消息")
	}
	if request.ReceptOrderID <= 0 {
		return nil, fmt.Errorf("receptOrderId 必填")
	}
	raw, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("续住参数无法保存: %w", err)
	}
	key := renewIdempotencyKey(conversationID, sourceMessageID, raw)
	now := time.Now()
	var item *models.PMSOperation
	err = sqls.WithTransaction(func(tx *sqls.TxContext) error {
		locked := &models.PMSOperation{}
		if err := tx.Tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("conversation_id = ? AND operation_type = ? AND status = ?", conversationID, PMSOperationTypeRenew, PMSOperationPending).
			Order("id DESC").Take(locked).Error; err == nil {
			item = locked
			return nil
		}
		if existing := repositories.PMSOperationRepository.FindByIdempotencyKeyForUpdate(tx.Tx, key); existing != nil {
			item = existing
			return nil
		}
		item = &models.PMSOperation{
			ConversationID:  conversationID,
			SourceMessageID: sourceMessageID,
			OperationType:   PMSOperationTypeRenew,
			Status:          PMSOperationPending,
			IdempotencyKey:  key,
			RequestData:     string(raw),
			PreviewText:     strings.TrimSpace(preview),
			ExpiresAt:       timePtr(now.Add(10 * time.Minute)),
			CreatedAt:       now,
			UpdatedAt:       now,
		}
		return repositories.PMSOperationRepository.Create(tx.Tx, item)
	})
	if err != nil {
		return nil, err
	}
	return &PMSRenewDraft{OperationID: item.ID, PreviewText: item.PreviewText, IdempotencyKey: item.IdempotencyKey}, nil
}

func (s *pmsOperationService) FindPendingRenew(conversationID int64) *models.PMSOperation {
	return repositories.PMSOperationRepository.FindPendingByConversationID(sqls.DB(), conversationID)
}

func (s *pmsOperationService) ConfirmRenew(ctx context.Context, conversationID, operationID, confirmationMessageID int64) (PMSRenewOutcome, error) {
	if conversationID <= 0 || operationID <= 0 {
		return PMSRenewOutcome{}, fmt.Errorf("续住操作不存在")
	}
	var item models.PMSOperation
	shouldExecute := false
	err := sqls.WithTransaction(func(tx *sqls.TxContext) error {
		if err := tx.Tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", operationID).Take(&item).Error; err != nil {
			return fmt.Errorf("续住操作不存在")
		}
		if item.ConversationID != conversationID {
			return fmt.Errorf("续住操作不属于当前会话")
		}
		if item.Status != PMSOperationPending {
			return nil
		}
		if item.ExpiresAt != nil && time.Now().After(*item.ExpiresAt) {
			item.Status = PMSOperationExpired
			item.UpdatedAt = time.Now()
			return repositories.PMSOperationRepository.Updates(tx.Tx, item.ID, map[string]any{"status": item.Status, "updated_at": item.UpdatedAt})
		}
		item.Status = PMSOperationConfirmed
		item.ConfirmationMessageID = confirmationMessageID
		item.UpdatedAt = time.Now()
		shouldExecute = true
		return repositories.PMSOperationRepository.Updates(tx.Tx, item.ID, map[string]any{
			"status": item.Status, "confirmation_message_id": confirmationMessageID, "updated_at": item.UpdatedAt,
		})
	})
	if err != nil {
		return PMSRenewOutcome{}, err
	}
	if item.Status == PMSOperationExpired {
		return PMSRenewOutcome{Status: item.Status, ReplyText: "这次续住确认已过期，麻烦重新告诉我需要续住到哪天。", Operation: &item}, nil
	}
	if item.Status == PMSOperationSucceeded {
		return PMSRenewOutcome{Status: item.Status, ReplyText: "续住已经办理好了。", Operation: &item}, nil
	}
	if item.Status == PMSOperationFailed {
		return PMSRenewOutcome{Status: item.Status, ReplyText: "不好意思，续住暂时没有办理成功，请稍后再试。", Operation: &item}, nil
	}
	if !shouldExecute {
		return PMSRenewOutcome{Status: item.Status, ReplyText: "续住确认正在处理中，请稍等片刻。", Operation: &item}, nil
	}
	if item.Status != PMSOperationConfirmed {
		return PMSRenewOutcome{Status: item.Status, ReplyText: "这项续住操作已经处理过了。", Operation: &item}, nil
	}
	var request pms.RenewRequest
	if err := json.Unmarshal([]byte(item.RequestData), &request); err != nil {
		return PMSRenewOutcome{}, fmt.Errorf("续住参数无法恢复: %w", err)
	}
	client := pms.NewClient(config.Current().PMS)
	result, callErr := client.Renew(ctx, request)
	resultData, _ := json.Marshal(result.Data)
	status := PMSOperationSucceeded
	errorMessage := ""
	reply := "续住已经办理好了。"
	if callErr != nil {
		status = PMSOperationFailed
		errorMessage = callErr.Error()
		reply = "不好意思，续住暂时没有办理成功，请稍后再试。"
	} else {
		verifyID := request.ReceptOrderID
		if request.NewReceptOrderID > 0 {
			verifyID = request.NewReceptOrderID
		}
		if _, verifyErr := client.Query(ctx, "recept_order_detail", map[string]string{
			"receptOrderId": fmt.Sprintf("%d", verifyID),
		}); verifyErr != nil {
			status = PMSOperationFailed
			errorMessage = "PMS 续住已提交，但回读接待单失败: " + verifyErr.Error()
			reply = "不好意思，续住结果暂时还没核实成功，请稍后再试。"
		}
	}
	_ = repositories.PMSOperationRepository.Updates(sqls.DB(), item.ID, map[string]any{
		"status": status, "result_data": string(resultData), "error_message": errorMessage, "updated_at": time.Now(),
	})
	item.Status = status
	item.ResultData = string(resultData)
	item.ErrorMessage = errorMessage
	return PMSRenewOutcome{Status: status, ReplyText: reply, Operation: &item, ResultData: result.Data}, nil
}

func (s *pmsOperationService) CancelRenew(conversationID, operationID int64) error {
	if conversationID <= 0 || operationID <= 0 {
		return fmt.Errorf("续住操作不存在")
	}
	item := repositories.PMSOperationRepository.Get(sqls.DB(), operationID)
	if item == nil || item.ConversationID != conversationID {
		return fmt.Errorf("续住操作不存在")
	}
	if item.Status != PMSOperationPending {
		return nil
	}
	return repositories.PMSOperationRepository.Updates(sqls.DB(), operationID, map[string]any{
		"status": PMSOperationCancelled, "updated_at": time.Now(),
	})
}

func renewIdempotencyKey(conversationID, sourceMessageID int64, raw []byte) string {
	hash := sha256.Sum256(append([]byte(fmt.Sprintf("%d:%d:", conversationID, sourceMessageID)), raw...))
	return hex.EncodeToString(hash[:])
}

func timePtr(value time.Time) *time.Time {
	return &value
}
