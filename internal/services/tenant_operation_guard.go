package services

import (
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/errorsx"
)

func requireActiveTenantID(operator *dto.AuthPrincipal, resource string) (int64, error) {
	if operator == nil || operator.UserID <= 0 {
		return 0, errorsx.Unauthorized("未登录或登录已过期")
	}
	tenantID := AgentTeamScopeService.ActiveTenantID(operator)
	if tenantID <= 0 {
		return 0, errorsx.Forbidden("请先进入需要管理" + resource + "的接入公司")
	}
	return tenantID, nil
}
