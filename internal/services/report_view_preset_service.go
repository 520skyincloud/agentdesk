package services

import (
	"encoding/json"
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

var ReportViewPresetService = &reportViewPresetService{}

type reportViewPresetService struct{}

var allowedReportViewPages = map[string]struct{}{
	"service-analytics":    {},
	"conversation-records": {},
}

func (s *reportViewPresetService) List(pageCode string, operator *dto.AuthPrincipal) ([]models.ReportViewPreset, error) {
	tenantID := AgentTeamScopeService.ActiveTenantID(operator)
	pageCode = strings.TrimSpace(pageCode)
	if tenantID <= 0 || operator == nil || operator.UserID <= 0 {
		return nil, errorsx.Forbidden("请先进入需要管理视图的接入公司")
	}
	if _, ok := allowedReportViewPages[pageCode]; !ok {
		return nil, errorsx.InvalidParam("不支持的报表页面")
	}
	return repositories.ReportViewPresetRepository.FindOwned(sqls.DB(), tenantID, operator.UserID, pageCode), nil
}

func (s *reportViewPresetService) Save(req request.SaveReportViewPresetRequest, operator *dto.AuthPrincipal) (*models.ReportViewPreset, error) {
	tenantID := AgentTeamScopeService.ActiveTenantID(operator)
	if tenantID <= 0 || operator == nil || operator.UserID <= 0 {
		return nil, errorsx.Forbidden("请先进入需要管理视图的接入公司")
	}
	pageCode := strings.TrimSpace(req.PageCode)
	name := strings.TrimSpace(req.Name)
	if _, ok := allowedReportViewPages[pageCode]; !ok {
		return nil, errorsx.InvalidParam("不支持的报表页面")
	}
	if name == "" || len(name) > 100 {
		return nil, errorsx.InvalidParam("视图名称不能为空且不能超过100个字符")
	}
	for _, value := range []string{req.FiltersJSON, req.ColumnsJSON, req.SortJSON} {
		if !validReportViewJSON(value) {
			return nil, errorsx.InvalidParam("保存视图包含无效JSON配置")
		}
	}
	var presetID int64
	err := sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		now := time.Now()
		if req.IsDefault {
			if err := repositories.ReportViewPresetRepository.ClearDefault(ctx.Tx, tenantID, operator.UserID, pageCode); err != nil {
				return err
			}
		}
		if req.ID > 0 {
			current := repositories.ReportViewPresetRepository.GetOwned(ctx.Tx, req.ID, tenantID, operator.UserID)
			if current == nil {
				return errorsx.InvalidParam("保存视图不存在")
			}
			presetID = current.ID
			return repositories.ReportViewPresetRepository.UpdatesOwned(ctx.Tx, current.ID, tenantID, operator.UserID, map[string]any{
				"page_code": pageCode, "name": name, "filters_json": strings.TrimSpace(req.FiltersJSON),
				"columns_json": strings.TrimSpace(req.ColumnsJSON), "sort_json": strings.TrimSpace(req.SortJSON),
				"is_default": req.IsDefault, "status": enums.StatusOk, "updated_at": now,
				"update_user_id": operator.UserID, "update_user_name": operator.Username,
			})
		}
		item := &models.ReportViewPreset{
			TenantID: tenantID, UserID: operator.UserID, PageCode: pageCode, Name: name,
			FiltersJSON: strings.TrimSpace(req.FiltersJSON), ColumnsJSON: strings.TrimSpace(req.ColumnsJSON),
			SortJSON: strings.TrimSpace(req.SortJSON), IsDefault: req.IsDefault, Status: enums.StatusOk,
			AuditFields: utils.BuildAuditFields(operator),
		}
		if err := repositories.ReportViewPresetRepository.Create(ctx.Tx, item); err != nil {
			return err
		}
		presetID = item.ID
		return nil
	})
	if err != nil {
		return nil, err
	}
	return repositories.ReportViewPresetRepository.GetOwned(sqls.DB(), presetID, tenantID, operator.UserID), nil
}

func (s *reportViewPresetService) Delete(id int64, operator *dto.AuthPrincipal) error {
	tenantID := AgentTeamScopeService.ActiveTenantID(operator)
	item := repositories.ReportViewPresetRepository.GetOwned(sqls.DB(), id, tenantID, operator.UserID)
	if item == nil {
		return errorsx.InvalidParam("保存视图不存在")
	}
	return repositories.ReportViewPresetRepository.UpdatesOwned(sqls.DB(), item.ID, tenantID, operator.UserID, map[string]any{
		"status": enums.StatusDeleted, "is_default": false, "updated_at": time.Now(),
		"update_user_id": operator.UserID, "update_user_name": operator.Username,
	})
}

func validReportViewJSON(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return true
	}
	if !json.Valid([]byte(value)) {
		return false
	}
	var decoded any
	if json.Unmarshal([]byte(value), &decoded) != nil {
		return false
	}
	switch decoded.(type) {
	case map[string]any, []any:
		return true
	default:
		return false
	}
}
