package services

import (
	"strings"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/errorsx"
	"agent-desk/internal/pkg/utils"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
)

var HQQiyuRouteService = newHQQiyuRouteService()

func newHQQiyuRouteService() *hqQiyuRouteService {
	return &hqQiyuRouteService{}
}

type hqQiyuRouteService struct{}

func (s *hqQiyuRouteService) GetDefault() *models.HQQiyuRoute {
	if item := repositories.HQQiyuRouteRepository.Take(sqls.DB(), "status = ?", enums.StatusOk); item != nil {
		return item
	}
	return repositories.HQQiyuRouteRepository.Take(sqls.DB(), "id > 0")
}

func (s *hqQiyuRouteService) Update(req request.UpdateHQQiyuRouteRequest, operator *dto.AuthPrincipal) (*models.HQQiyuRoute, error) {
	if operator == nil {
		return nil, errorsx.Unauthorized("未登录或登录已过期")
	}
	timeoutMinutes := req.TimeoutMinutes
	if timeoutMinutes <= 0 {
		timeoutMinutes = DefaultManualTimeoutMinutes
	}
	status := enums.Status(req.Status)
	if !enums.IsValidStatus(req.Status) {
		status = enums.StatusOk
	}
	now := time.Now()
	columns := map[string]any{
		"default_group_id":   strings.TrimSpace(req.DefaultGroupID),
		"high_risk_group_id": strings.TrimSpace(req.HighRiskGroupID),
		"service_time":       strings.TrimSpace(req.ServiceTime),
		"fallback_mode":      strings.TrimSpace(req.FallbackMode),
		"timeout_minutes":    timeoutMinutes,
		"status":             status,
		"remark":             strings.TrimSpace(req.Remark),
		"updated_at":         now,
		"update_user_id":     operator.UserID,
		"update_user_name":   operator.Username,
	}
	if columns["fallback_mode"] == "" {
		columns["fallback_mode"] = "ai_after_timeout"
	}
	current := s.GetDefault()
	if current == nil {
		item := &models.HQQiyuRoute{
			DefaultGroupID:  columns["default_group_id"].(string),
			HighRiskGroupID: columns["high_risk_group_id"].(string),
			ServiceTime:     columns["service_time"].(string),
			FallbackMode:    columns["fallback_mode"].(string),
			TimeoutMinutes:  timeoutMinutes,
			Status:          status,
			Remark:          columns["remark"].(string),
			AuditFields:     utils.BuildAuditFields(operator),
		}
		if err := repositories.HQQiyuRouteRepository.Create(sqls.DB(), item); err != nil {
			return nil, err
		}
		return item, nil
	}
	if err := repositories.HQQiyuRouteRepository.Updates(sqls.DB(), current.ID, columns); err != nil {
		return nil, err
	}
	return repositories.HQQiyuRouteRepository.Get(sqls.DB(), current.ID), nil
}
