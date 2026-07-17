package dashboard

import (
	"time"

	"agent-desk/internal/builders"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/httpx"
	"agent-desk/internal/services"

	"agent-desk/internal/pkg/httpx/params"

	"github.com/gin-gonic/gin"
)

func DashboardGetOverview(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionDashboardView)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	rangeValue, _ := params.Get(ctx, "range")
	days := 7
	if rangeValue == "today" {
		days = 1
	} else if rangeValue == "30d" {
		days = 30
	}
	now := time.Now()
	startAt := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).AddDate(0, 0, -(days - 1))
	aggregate, err := services.ServiceAnalyticsService.GetOverview(services.ServiceAnalyticsQuery{
		StartAt:                   startAt,
		EndAt:                     now,
		IncludeCurrentAgentRoster: true,
	}, operator)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, builders.BuildServiceAnalyticsOverview(aggregate))
}
