package services

import (
	"encoding/json"
	"strings"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/errorsx"
	"agent-desk/internal/pkg/utils"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
)

var TicketViewService = newTicketViewService()

func newTicketViewService() *ticketViewService {
	return &ticketViewService{}
}

type ticketViewService struct {
}

func (s *ticketViewService) Get(id int64) *models.TicketView {
	return repositories.TicketViewRepository.Get(sqls.DB(), id)
}

func (s *ticketViewService) Find(cnd *sqls.Cnd) []models.TicketView {
	return repositories.TicketViewRepository.Find(sqls.DB(), cnd)
}

func (s *ticketViewService) ListForOperator(operator *dto.AuthPrincipal) ([]models.TicketView, error) {
	tenantID, err := requireActiveTenantID(operator, "工单视图")
	if err != nil {
		return nil, err
	}
	return s.Find(sqls.NewCnd().Eq("tenant_id", tenantID).Eq("user_id", operator.UserID).Asc("sort_no").Desc("id")), nil
}

func (s *ticketViewService) Save(req request.SaveTicketViewRequest, operator *dto.AuthPrincipal) (*models.TicketView, error) {
	tenantID, err := requireActiveTenantID(operator, "工单视图")
	if err != nil {
		return nil, err
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return nil, errorsx.InvalidParam("视图名称不能为空")
	}
	filtersJSON, err := json.Marshal(req.Filters)
	if err != nil {
		return nil, errorsx.InvalidParam("视图筛选条件格式不正确")
	}
	now := time.Now()
	if req.ID > 0 {
		item := repositories.TicketViewRepository.GetInTenant(sqls.DB(), req.ID, tenantID)
		if item == nil || item.UserID != operator.UserID {
			return nil, errorsx.InvalidParam("视图不存在")
		}
		if err := repositories.TicketViewRepository.UpdatesInTenant(sqls.DB(), req.ID, tenantID, map[string]any{
			"name":             name,
			"filters_json":     string(filtersJSON),
			"update_user_id":   operator.UserID,
			"update_user_name": operator.Username,
			"updated_at":       now,
		}); err != nil {
			return nil, err
		}
		return repositories.TicketViewRepository.GetInTenant(sqls.DB(), req.ID, tenantID), nil
	}
	item := &models.TicketView{
		TenantID:    tenantID,
		UserID:      operator.UserID,
		Name:        name,
		FiltersJSON: string(filtersJSON),
		AuditFields: utils.BuildAuditFields(operator),
	}
	if err := repositories.TicketViewRepository.Create(sqls.DB(), item); err != nil {
		return nil, err
	}
	return item, nil
}

func (s *ticketViewService) Delete(id int64, operator *dto.AuthPrincipal) error {
	tenantID, err := requireActiveTenantID(operator, "工单视图")
	if err != nil {
		return err
	}
	item := repositories.TicketViewRepository.GetInTenant(sqls.DB(), id, tenantID)
	if item == nil || item.UserID != operator.UserID {
		return errorsx.InvalidParam("视图不存在")
	}
	return repositories.TicketViewRepository.DeleteInTenant(sqls.DB(), id, tenantID)
}
