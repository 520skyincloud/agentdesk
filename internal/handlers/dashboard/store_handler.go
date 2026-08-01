package dashboard

import (
	"agent-desk/internal/builders"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/httpx"
	"agent-desk/internal/pkg/httpx/params"
	"agent-desk/internal/services"

	"github.com/gin-gonic/gin"
	"github.com/mlogclub/simple/web"
)

func StoreAnyList(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionStoreView)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	cnd := params.NewPagedSqlCnd(ctx,
		params.QueryFilter{ParamName: "status"},
	).Eq("tenant_id", operator.ActiveTenantID).Where("status <> ?", enums.StatusDeleted).Asc("name").Asc("id")
	if keyword, _ := params.Get(ctx, "keyword"); keyword != "" {
		value := "%" + keyword + "%"
		cnd.Where("name LIKE ? OR store_code LIKE ? OR brand_name LIKE ?", value, value, value)
	}
	list, paging := services.StoreService.FindPageByCnd(cnd)
	staffCounts, instanceCounts := services.StoreService.RelationCounts(operator.ActiveTenantID, list)
	httpx.WriteJSON(ctx, &web.PageResult{Results: builders.BuildStoreList(list, staffCounts, instanceCounts), Page: paging})
}

func StoreGetOptions(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionStoreView)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, builders.BuildStoreOptions(services.StoreService.ListActiveOptions(operator.ActiveTenantID)))
}

func StoreGetBy(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionStoreView)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	id, ok := httpx.GetPathInt64(ctx, "id")
	if !ok {
		return
	}
	item := services.StoreService.GetInTenant(id, operator.ActiveTenantID)
	if item == nil || item.Status == enums.StatusDeleted {
		httpx.WriteJSON(ctx, web.JsonErrorMsg("门店不存在"))
		return
	}
	staffCounts, instanceCounts := services.StoreService.RelationCounts(operator.ActiveTenantID, []models.Store{*item})
	httpx.WriteJSON(ctx, builders.BuildStore(item, staffCounts[item.ID], instanceCounts[item.ID]))
}

func StorePostCreate(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionStoreCreate)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	req := request.CreateStoreRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	item, err := services.StoreService.Create(req, operator)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	staffCounts, instanceCounts := services.StoreService.RelationCounts(operator.ActiveTenantID, []models.Store{*item})
	httpx.WriteJSON(ctx, builders.BuildStore(item, staffCounts[item.ID], instanceCounts[item.ID]))
}

func StorePostUpdate(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionStoreUpdate)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	req := request.UpdateStoreRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	item, err := services.StoreService.Update(req, operator)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	staffCounts, instanceCounts := services.StoreService.RelationCounts(operator.ActiveTenantID, []models.Store{*item})
	httpx.WriteJSON(ctx, builders.BuildStore(item, staffCounts[item.ID], instanceCounts[item.ID]))
}

func StorePostUpdateStatus(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionStoreUpdate)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	req := request.UpdateStoreStatusRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if err := services.StoreService.UpdateStatus(req, operator); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, nil)
}
