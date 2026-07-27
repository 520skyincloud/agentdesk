package dashboard

import (
	"agent-desk/internal/builders"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/httpx"
	"agent-desk/internal/pkg/httpx/params"
	"agent-desk/internal/services"

	"github.com/gin-gonic/gin"
	"github.com/mlogclub/simple/web"
)

func IndustryTagDefinitionGetListAll(ctx *gin.Context) {
	if _, err := requirePlatformPermission(ctx, constants.PermissionAIConfigView); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	profileID, _ := params.GetInt64(ctx, "intentProfileId")
	list, err := services.IndustryTagDefinitionService.FindByProfile(profileID)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, builders.BuildIndustryTagDefinitions(list))
}

func IndustryTagDefinitionAnyList(ctx *gin.Context) {
	if _, err := requirePlatformPermission(ctx, constants.PermissionAIConfigView); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	cnd := params.NewPagedSqlCnd(ctx,
		params.QueryFilter{ParamName: "intentProfileId", ColumnName: "intent_profile_id"},
		params.QueryFilter{ParamName: "parentId", ColumnName: "parent_id"},
		params.QueryFilter{ParamName: "name", Op: params.Like},
		params.QueryFilter{ParamName: "status"},
	).Where("status <> ?", enums.StatusDeleted).Asc("parent_id").Asc("sort_no").Asc("id")
	list, paging := services.IndustryTagDefinitionService.FindPageByCnd(cnd)
	httpx.WriteJSON(ctx, &web.PageResult{
		Results: builders.BuildIndustryTagDefinitions(list),
		Page:    paging,
	})
}

func IndustryTagDefinitionGetBy(ctx *gin.Context) {
	id, ok := httpx.GetPathInt64(ctx, "id")
	if !ok {
		return
	}
	if _, err := requirePlatformPermission(ctx, constants.PermissionAIConfigView); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	item := services.IndustryTagDefinitionService.Get(id)
	if item == nil {
		httpx.WriteJSON(ctx, web.JsonErrorMsg("行业标签模板不存在"))
		return
	}
	httpx.WriteJSON(ctx, builders.BuildIndustryTagDefinition(item))
}

func IndustryTagDefinitionPostCreate(ctx *gin.Context) {
	operator, err := requirePlatformPermission(ctx, constants.PermissionAIConfigUpdate)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	req := request.CreateIndustryTagDefinitionRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	item, err := services.IndustryTagDefinitionService.Create(req, operator)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, builders.BuildIndustryTagDefinition(item))
}

func IndustryTagDefinitionPostUpdate(ctx *gin.Context) {
	operator, err := requirePlatformPermission(ctx, constants.PermissionAIConfigUpdate)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	req := request.UpdateIndustryTagDefinitionRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if err := services.IndustryTagDefinitionService.Update(req, operator); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, nil)
}
