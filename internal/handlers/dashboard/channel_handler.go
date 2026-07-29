package dashboard

import (
	"encoding/json"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/dto/request"
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

func ChannelGetBy(ctx *gin.Context) {
	id, ok := httpx.GetPathInt64(ctx, "id")
	if !ok {
		return
	}
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionChannelUpdate)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if _, err = services.AuthService.RequireTenantContext(ctx); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	item := services.ChannelService.GetInTenant(id, operator)
	if item == nil || item.Status == enums.StatusDeleted {
		httpx.WriteJSON(ctx, web.JsonErrorMsg("channel not found"))
		return
	}
	httpx.WriteJSON(ctx, buildChannelResponse(item))
}

func ChannelAnyWxworkKfAccounts(ctx *gin.Context) {
	_, err := services.AuthService.RequirePermission(ctx, constants.PermissionChannelView)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if _, err = services.AuthService.RequireTenantContext(ctx); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	list, err := services.ChannelService.ListWxWorkKFAccounts()
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, list)
}

func ChannelPostCreate(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionChannelCreate)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if _, err = services.AuthService.RequireTenantContext(ctx); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	req := request.CreateChannelRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	item, err := services.ChannelService.CreateChannel(req, operator)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, buildChannelPublicResponse(item))
}

func ChannelPostUpdate(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionChannelUpdate)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if _, err = services.AuthService.RequireTenantContext(ctx); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	req := request.UpdateChannelRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if err := services.ChannelService.UpdateChannel(req, operator); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, nil)
}

func ChannelPostUpdate_status(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionChannelUpdate)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if _, err = services.AuthService.RequireTenantContext(ctx); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	req := request.UpdateChannelStatusRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if err := services.ChannelService.UpdateStatus(req.ID, req.Status, operator); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, nil)
}

func ChannelPostReset_user_token_secret(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionChannelUpdate)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if _, err = services.AuthService.RequireTenantContext(ctx); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	req := request.ResetChannelUserTokenSecretRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	secret, err := services.ChannelService.ResetUserTokenSecret(req.ID, operator)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, map[string]string{"userTokenSecret": secret})
}

func ChannelPostDelete(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionChannelDelete)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if _, err = services.AuthService.RequireTenantContext(ctx); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	req := request.DeleteChannelRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if err := services.ChannelService.DeleteChannel(req.ID, operator); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, nil)
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
