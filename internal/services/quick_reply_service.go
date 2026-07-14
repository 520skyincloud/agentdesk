package services

import (
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/errorsx"
	"agent-desk/internal/pkg/utils"
	"agent-desk/internal/repositories"
	"strings"
	"time"

	"github.com/mlogclub/simple/sqls"
)

var QuickReplyService = newQuickReplyService()

func newQuickReplyService() *quickReplyService {
	return &quickReplyService{}
}

type quickReplyService struct {
}

func (s *quickReplyService) GetInTenant(id int64, operator *dto.AuthPrincipal) *models.QuickReply {
	tenantID := quickReplyTenantID(operator)
	if tenantID <= 0 {
		return nil
	}
	return repositories.QuickReplyRepository.GetInTenant(sqls.DB(), id, tenantID)
}

func (s *quickReplyService) FindInTenant(cnd *sqls.Cnd, operator *dto.AuthPrincipal) []models.QuickReply {
	return repositories.QuickReplyRepository.Find(sqls.DB(), applyQuickReplyTenantScope(cnd, operator))
}

func (s *quickReplyService) FindPageInTenant(cnd *sqls.Cnd, operator *dto.AuthPrincipal) (list []models.QuickReply, paging *sqls.Paging) {
	return repositories.QuickReplyRepository.FindPageByCnd(sqls.DB(), applyQuickReplyTenantScope(cnd, operator))
}

func (s *quickReplyService) CreateQuickReply(req request.CreateQuickReplyRequest, operator *dto.AuthPrincipal) (*models.QuickReply, error) {
	if operator == nil {
		return nil, errorsx.Unauthorized("未登录或登录已过期")
	}
	tenantID := quickReplyTenantID(operator)
	if tenantID <= 0 {
		return nil, errorsx.Forbidden("请先进入需要管理快捷回复的接入公司")
	}
	title := strings.TrimSpace(req.Title)
	content := strings.TrimSpace(req.Content)
	if title == "" || content == "" {
		return nil, errorsx.InvalidParam("标题和内容不能为空")
	}
	item := &models.QuickReply{
		TenantID:    tenantID,
		GroupName:   strings.TrimSpace(req.GroupName),
		Title:       title,
		Content:     content,
		Status:      req.Status,
		SortNo:      req.SortNo,
		AuditFields: utils.BuildAuditFields(operator),
	}
	if err := repositories.QuickReplyRepository.Create(sqls.DB(), item); err != nil {
		return nil, err
	}
	return item, nil
}

func (s *quickReplyService) UpdateQuickReply(req request.UpdateQuickReplyRequest, operator *dto.AuthPrincipal) error {
	if operator == nil {
		return errorsx.Unauthorized("未登录或登录已过期")
	}
	item := s.GetInTenant(req.ID, operator)
	if item == nil {
		return errorsx.InvalidParam("快捷回复不存在")
	}
	title := strings.TrimSpace(req.Title)
	content := strings.TrimSpace(req.Content)
	if title == "" || content == "" {
		return errorsx.InvalidParam("标题和内容不能为空")
	}
	return repositories.QuickReplyRepository.UpdatesInTenant(sqls.DB(), req.ID, item.TenantID, map[string]any{
		"group_name":       strings.TrimSpace(req.GroupName),
		"title":            title,
		"content":          content,
		"status":           req.Status,
		"sort_no":          req.SortNo,
		"update_user_id":   operator.UserID,
		"update_user_name": operator.Username,
		"updated_at":       time.Now(),
	})
}

func (s *quickReplyService) DeleteQuickReply(id int64, operator *dto.AuthPrincipal) error {
	if operator == nil {
		return errorsx.Unauthorized("未登录或登录已过期")
	}
	item := s.GetInTenant(id, operator)
	if item == nil {
		return errorsx.InvalidParam("快捷回复不存在")
	}
	return repositories.QuickReplyRepository.DeleteInTenant(sqls.DB(), id, item.TenantID)
}

func quickReplyTenantID(operator *dto.AuthPrincipal) int64 {
	if operator == nil || operator.ActiveTenantID <= 0 {
		return 0
	}
	return operator.ActiveTenantID
}

func applyQuickReplyTenantScope(cnd *sqls.Cnd, operator *dto.AuthPrincipal) *sqls.Cnd {
	if cnd == nil {
		cnd = sqls.NewCnd()
	}
	tenantID := quickReplyTenantID(operator)
	if tenantID <= 0 {
		return cnd.Where("1 = 0")
	}
	return cnd.Eq("tenant_id", tenantID)
}
