package dashboard

import (
	"encoding/json"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/dto/response"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/httpx"
	"agent-desk/internal/services"

	"agent-desk/internal/pkg/httpx/params"

	"github.com/gin-gonic/gin"
	"github.com/mlogclub/simple/web"
)

func ChannelAnyList(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionChannelView)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if _, err = services.AuthService.RequireTenantContext(ctx); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	list, paging := services.ChannelService.FindPageInTenant(params.NewPagedSqlCnd(ctx,
		params.QueryFilter{ParamName: "status"},
		params.QueryFilter{ParamName: "name", Op: params.Like},
		params.QueryFilter{ParamName: "channelType"},
		params.QueryFilter{ParamName: "channelId", Op: params.Like},
	).Where("status <> ?", enums.StatusDeleted).Desc("id"), operator)
	results := make([]response.ChannelResponse, 0, len(list))
	for _, item := range list {
		results = append(results, buildChannelPublicResponse(&item))
	}
	httpx.WriteJSON(ctx, &web.PageResult{Results: results, Page: paging})
}

func buildChannelResponse(item *models.Channel) response.ChannelResponse {
	ret := response.BuildChannelResponse(item)
	if item == nil {
		return ret
	}
	if item.ChannelType == enums.ChannelTypeWxWorkProtocol {
		if cfg, err := services.ChannelService.ParseWxWorkProtocolChannelConfig(item.ConfigJSON); err == nil {
			cfg.AppKey = ""
			cfg.AppSecret = ""
			cfg.CallbackToken = ""
			if configJSON, err := json.Marshal(cfg); err == nil {
				ret.ConfigJSON = string(configJSON)
			} else {
				ret.ConfigJSON = ""
			}
		} else {
			ret.ConfigJSON = ""
		}
	}
	if aiAgent := services.AIAgentService.GetByTenantID(item.AIAgentID, item.TenantID); aiAgent != nil {
		ret.AIAgentName = aiAgent.Name
	}
	return ret
}

func buildChannelPublicResponse(item *models.Channel) response.ChannelResponse {
	ret := buildChannelResponse(item)
	ret.ConfigJSON = ""
	return ret
}
