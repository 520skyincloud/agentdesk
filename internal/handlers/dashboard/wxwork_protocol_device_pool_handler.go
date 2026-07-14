package dashboard

import (
	"strings"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/dto/response"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/errorsx"
	"agent-desk/internal/pkg/httpx"
	"agent-desk/internal/pkg/httpx/params"
	"agent-desk/internal/pkg/utils"
	"agent-desk/internal/services"

	"github.com/gin-gonic/gin"
	"github.com/mlogclub/simple/web"
)

func WxWorkProtocolDevicePoolAnyList(ctx *gin.Context) {
	if _, err := requirePlatformDevicePoolPermission(ctx, constants.PermissionWxWorkDevicePoolView); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	list, paging := services.WxWorkProtocolDevicePoolService.FindPageByCnd(params.NewPagedSqlCnd(ctx,
		params.QueryFilter{ParamName: "status"},
		params.QueryFilter{ParamName: "syncStatus", ColumnName: "sync_status"},
		params.QueryFilter{ParamName: "guid", Op: params.Like},
	).Where("status <> ?", enums.StatusDeleted).Desc("id"))
	results := make([]response.WxWorkProtocolDevicePoolInstanceResponse, 0, len(list))
	for _, item := range list {
		results = append(results, buildWxWorkProtocolDevicePoolResponse(&item))
	}
	httpx.WriteJSON(ctx, &web.PageResult{Results: results, Page: paging})
}

func WxWorkProtocolDevicePoolGetSettings(ctx *gin.Context) {
	if _, err := requirePlatformDevicePoolPermission(ctx, constants.PermissionWxWorkDevicePoolView); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, services.WxWorkProtocolDevicePoolService.Settings())
}

func WxWorkProtocolDevicePoolPostUpdate_settings(ctx *gin.Context) {
	operator, err := requirePlatformDevicePoolPermission(ctx, constants.PermissionWxWorkDevicePoolUpdate)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	req := request.UpdateWxWorkProtocolDevicePoolSettingsRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if err := services.WxWorkProtocolDevicePoolService.UpdateSettings(req, operator); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, services.WxWorkProtocolDevicePoolService.Settings())
}

func WxWorkProtocolDevicePoolPostSync(ctx *gin.Context) {
	operator, err := requirePlatformDevicePoolPermission(ctx, constants.PermissionWxWorkDevicePoolSync)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	ret, err := services.WxWorkProtocolDevicePoolService.Sync(operator)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, ret)
}

func requirePlatformDevicePoolPermission(ctx *gin.Context, permission constants.Permission) (*dto.AuthPrincipal, error) {
	operator, err := services.AuthService.RequirePermission(ctx, permission)
	if err != nil {
		return nil, err
	}
	if !operator.IsPlatformAccount {
		return nil, errorsx.Forbidden("只有平台账号可以管理企微设备池")
	}
	return operator, nil
}

func buildWxWorkProtocolDevicePoolResponse(item *models.WxWorkProtocolDevicePoolInstance) response.WxWorkProtocolDevicePoolInstanceResponse {
	if item == nil {
		return response.WxWorkProtocolDevicePoolInstanceResponse{}
	}
	ret := response.WxWorkProtocolDevicePoolInstanceResponse{
		ID:                            item.ID,
		ProviderInstanceID:            item.ProviderInstanceID,
		Guid:                          item.Guid,
		Uin:                           item.Uin,
		ProviderUserID:                item.ProviderUserID,
		ClientType:                    item.ClientType,
		SeatName:                      utils.RepairMojibakeText(item.SeatName),
		BridgeID:                      item.BridgeID,
		State:                         utils.RepairMojibakeText(item.State),
		ExpiredAt:                     item.ExpiredAt,
		SyncStatus:                    item.SyncStatus,
		LastSyncedAt:                  item.LastSyncedAt,
		BoundWxWorkProtocolInstanceID: item.BoundWxWorkProtocolInstanceID,
		Available:                     item.SyncStatus == "idle" && item.BoundWxWorkProtocolInstanceID == 0 && strings.TrimSpace(item.Uin) == "",
		Status:                        int(item.Status),
		Remark:                        utils.RepairMojibakeText(item.Remark),
		CreatedAt:                     item.CreatedAt,
		UpdatedAt:                     item.UpdatedAt,
	}
	if item.BoundWxWorkProtocolInstanceID > 0 {
		if instance := services.WxWorkProtocolInstanceService.Get(item.BoundWxWorkProtocolInstanceID); instance != nil {
			ret.BoundEmployeeName = utils.RepairMojibakeText(firstNonEmptyString(instance.EmployeeName, instance.EmployeeUserID, instance.Guid))
			if store := services.StoreService.Get(instance.StoreID); store != nil {
				ret.BoundStoreName = utils.RepairMojibakeText(store.Name)
			}
		}
	}
	return ret
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
