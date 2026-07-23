package dashboard

import (
	"strconv"
	"strings"

	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/dto/response"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/errorsx"
	"agent-desk/internal/pkg/httpx"
	"agent-desk/internal/pkg/httpx/params"
	"agent-desk/internal/services"

	"github.com/gin-gonic/gin"
	"github.com/mlogclub/simple/web"
)

func CustomerTagGetPolicy(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionTagView)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	result, err := services.CustomerTagRuntimePolicyService.GetPolicy(operator)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, result)
}

func CustomerTagAnyRuntime_list(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionTagView)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	filter, err := readCustomerTagRuntimeListFilter(ctx)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	list, paging, err := services.CustomerTagRuntimePolicyService.ListStorePolicies(filter, operator)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, &web.PageResult{Results: list, Page: paging})
}

func CustomerTagPostPolicy_update(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionTagUpdate)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	req := request.UpdateCustomerTagPolicyRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if err := services.CustomerTagRuntimePolicyService.UpdatePolicy(req, operator); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, nil)
}

func CustomerTagPostRuntime_batch_toggle(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionTagUpdate)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	req := request.BatchToggleCustomerTagRuntimeRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	storeIDs, err := services.CustomerTagRuntimePolicyService.BatchToggle(req, operator)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, &response.BatchToggleCustomerTagRuntimeResponse{AffectedStoreCount: len(storeIDs)})
}

func readCustomerTagRuntimeListFilter(ctx *gin.Context) (services.CustomerTagRuntimePolicyListFilter, error) {
	page, _ := params.GetInt(ctx, "page")
	limit, _ := params.GetInt(ctx, "limit")
	keyword, _ := params.Get(ctx, "keyword")
	ret := services.CustomerTagRuntimePolicyListFilter{Page: page, Limit: limit, Keyword: strings.TrimSpace(keyword)}
	if raw, ok := params.Get(ctx, "storeStatus"); ok && strings.TrimSpace(raw) != "" {
		value, err := strconv.Atoi(strings.TrimSpace(raw))
		if err != nil || (value != int(enums.StatusOk) && value != int(enums.StatusDisabled)) {
			return ret, errorsx.InvalidParam("门店状态筛选值不合法")
		}
		status := enums.Status(value)
		ret.StoreStatus = &status
	}
	var err error
	if ret.EvolutionEnabled, err = readOptionalBool(ctx, "evolutionEnabled"); err != nil {
		return ret, err
	}
	if ret.ReplyEnabled, err = readOptionalBool(ctx, "replyEnabled"); err != nil {
		return ret, err
	}
	return ret, nil
}

func readOptionalBool(ctx *gin.Context, name string) (*bool, error) {
	raw, ok := params.Get(ctx, name)
	if !ok || strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	value, err := strconv.ParseBool(strings.TrimSpace(raw))
	if err != nil {
		return nil, errorsx.InvalidParam("开关筛选值不合法")
	}
	return &value, nil
}
