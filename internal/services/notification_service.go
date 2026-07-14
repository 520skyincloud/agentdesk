package services

import (
	"strings"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/dto/response"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/errorsx"
	"agent-desk/internal/pkg/utils"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
)

var NotificationService = newNotificationService()

func newNotificationService() *notificationService {
	return &notificationService{}
}

type notificationService struct {
}

func (s *notificationService) Create(req request.CreateNotificationRequest) (*models.Notification, error) {
	if req.RecipientUserID <= 0 {
		return nil, errorsx.InvalidParam("接收人不能为空")
	}
	recipient := repositories.UserRepository.Get(sqls.DB(), req.RecipientUserID)
	if recipient == nil || recipient.Status == enums.StatusDeleted {
		return nil, errorsx.InvalidParam("接收账号不存在")
	}
	now := time.Now()
	item := &models.Notification{
		TenantID:         recipient.TenantID,
		RecipientUserID:  req.RecipientUserID,
		Title:            strings.TrimSpace(req.Title),
		Content:          strings.TrimSpace(req.Content),
		NotificationType: strings.TrimSpace(req.NotificationType),
		BizType:          strings.TrimSpace(req.BizType),
		BizID:            req.BizID,
		ActionURL:        strings.TrimSpace(req.ActionURL),
		Status:           enums.StatusOk,
		CreatedAt:        now,
	}
	if err := repositories.NotificationRepository.Create(sqls.DB(), item); err != nil {
		return nil, err
	}
	return item, nil
}

func (s *notificationService) CreateAndPush(req request.CreateNotificationRequest) (*models.Notification, error) {
	item, err := s.Create(req)
	if err != nil {
		return nil, err
	}
	WsService.PublishNotificationCreated(item.RecipientUserID, response.NotificationResponse{
		ID:               item.ID,
		RecipientUserID:  item.RecipientUserID,
		Title:            item.Title,
		Content:          item.Content,
		NotificationType: item.NotificationType,
		BizType:          item.BizType,
		BizID:            item.BizID,
		ActionURL:        item.ActionURL,
		ReadAt:           utils.FormatTimePtr(item.ReadAt),
		CreatedAt:        utils.FormatTime(item.CreatedAt),
	})
	return item, nil
}

func (s *notificationService) FindPageForPrincipal(cnd *sqls.Cnd, operator *dto.AuthPrincipal) ([]models.Notification, *sqls.Paging) {
	if cnd == nil {
		cnd = sqls.NewCnd()
	}
	if cnd.Paging == nil {
		cnd.Page(1, 20)
	}
	if operator == nil || operator.UserID <= 0 || operator.TenantID < 0 {
		return repositories.NotificationRepository.FindPageByCnd(sqls.DB(), cnd.Where("1 = 0"))
	}
	return repositories.NotificationRepository.FindPageByCnd(sqls.DB(), cnd.
		Eq("recipient_user_id", operator.UserID).
		Eq("tenant_id", operator.TenantID))
}

func (s *notificationService) CountUnread(operator *dto.AuthPrincipal) int64 {
	if operator == nil || operator.UserID <= 0 || operator.TenantID < 0 {
		return 0
	}
	return repositories.NotificationRepository.Count(sqls.DB(), sqls.NewCnd().
		Eq("recipient_user_id", operator.UserID).
		Eq("tenant_id", operator.TenantID).
		Eq("status", enums.StatusOk).
		Where("read_at IS NULL"))
}

func (s *notificationService) MarkRead(id int64, operator *dto.AuthPrincipal) error {
	if operator == nil || operator.UserID <= 0 || operator.TenantID < 0 {
		return errorsx.Unauthorized("未登录或登录已过期")
	}
	if id <= 0 {
		return errorsx.InvalidParam("通知不存在")
	}
	item := repositories.NotificationRepository.GetForRecipient(sqls.DB(), id, operator.UserID, operator.TenantID)
	if item == nil {
		return errorsx.InvalidParam("通知不存在")
	}
	if item.ReadAt != nil {
		return nil
	}
	now := time.Now()
	return repositories.NotificationRepository.Updates(sqls.DB(), id, map[string]any{
		"read_at": now,
	})
}

func (s *notificationService) MarkAllRead(operator *dto.AuthPrincipal) error {
	if operator == nil || operator.UserID <= 0 || operator.TenantID < 0 {
		return errorsx.Unauthorized("未登录或登录已过期")
	}
	return repositories.NotificationRepository.MarkAllRead(sqls.DB(), operator.UserID, operator.TenantID, time.Now())
}
