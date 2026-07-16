package dashboard

import (
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/dto/response"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/httpx"
	"agent-desk/internal/services"

	"github.com/gin-gonic/gin"
	"github.com/mlogclub/simple/sqls"
)

// AIAgentGetList_all returns only the internal runtime strategy identity needed
// by channel binding and platform diagnostics. Model and prompt details are not
// part of this tenant-facing option contract.
func AIAgentGetList_all(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionAIAgentView)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	list := services.AIAgentService.FindInTenant(
		sqls.NewCnd().Eq("status", enums.StatusOk).Desc("sort_no").Desc("id"),
		operator,
	)
	results := make([]response.AIAgentOptionResponse, 0, len(list))
	for _, item := range list {
		results = append(results, response.AIAgentOptionResponse{
			ID:     item.ID,
			Name:   item.Name,
			Status: item.Status,
		})
	}
	httpx.WriteJSON(ctx, results)
}
