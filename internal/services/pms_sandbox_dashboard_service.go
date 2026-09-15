package services

import (
	"context"
	"errors"
	"slices"

	"agent-desk/internal/pkg/config"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/dto/response"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/errorsx"
	"agent-desk/internal/pms/sandbox"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
)

// RequirePMSSandboxStore supplements the operation permission with the current
// employee's store scope. Call before every dashboard query or mutation.
func RequirePMSSandboxStore(operator *dto.AuthPrincipal, storeID int64) error {
	if operator == nil {
		return errorsx.Unauthorized("请先登录")
	}
	if !config.PMSSandboxAvailable() {
		return errorsx.Forbidden("测试 PMS 仅在 test-2 显式启用后可用")
	}
	if storeID <= 0 {
		return errorsx.InvalidParam("请选择门店")
	}
	scope := AgentTeamScopeService.Resolve(operator)
	if !scope.Unrestricted && !slices.Contains(scope.StoreIDs, storeID) {
		return errorsx.Forbidden("无权访问该门店的测试 PMS")
	}
	store := repositories.StoreRepository.Get(sqls.DB(), storeID)
	if store == nil || store.Status != enums.StatusOk {
		return errorsx.InvalidParam("门店不存在或已停用")
	}
	return nil
}

func ListPMSSandboxCustomers(operator *dto.AuthPrincipal, storeID int64) ([]response.PMSSandboxCustomerOption, error) {
	if err := RequirePMSSandboxStore(operator, storeID); err != nil {
		return nil, err
	}
	conversations, err := repositories.PMSSandboxCustomerConversations(sqls.DB(), storeID)
	if err != nil {
		return nil, errorsx.BusinessError(60, "测试客户列表暂时无法读取")
	}
	result := make([]response.PMSSandboxCustomerOption, 0, len(conversations))
	seen := make(map[int64]bool)
	for _, conversation := range conversations {
		if seen[conversation.CustomerID] {
			continue
		}
		seen[conversation.CustomerID] = true
		result = append(result, response.PMSSandboxCustomerOption{ID: conversation.CustomerID, Name: conversation.CustomerName, ConversationID: conversation.ID})
	}
	return result, nil
}

func PMSSandboxScopeForConversation(conversationID, sourceMessageID int64) (sandbox.Scope, error) {
	conversation := ConversationService.Get(conversationID)
	route := ConversationRouteService.GetByConversationID(conversationID)
	if conversation == nil || route == nil || route.StoreID <= 0 {
		return sandbox.Scope{}, errors.New("当前会话未绑定测试 PMS 门店")
	}
	return sandbox.Scope{
		StoreID: route.StoreID, ConversationID: conversation.ID,
		CustomerID: conversation.CustomerID, SourceMessageID: sourceMessageID,
	}, nil
}

func CancelPMSSandboxDashboardOperation(ctx context.Context, operator *dto.AuthPrincipal, storeID, operationID int64) error {
	if err := RequirePMSSandboxStore(operator, storeID); err != nil {
		return err
	}
	item := repositories.PMSOperationRepository.Get(sqls.DB(), operationID)
	if item == nil || item.Provider != sandbox.Provider || item.StoreID != storeID || item.ConversationID <= 0 {
		return errorsx.InvalidParam("测试办理方案不存在")
	}
	scope, err := PMSSandboxScopeForConversation(item.ConversationID, 0)
	if err != nil || scope.StoreID != storeID {
		return errorsx.Forbidden("测试办理方案不属于当前门店")
	}
	_, err = PMSSandboxService.Cancel(ctx, scope, item.ID)
	return err
}

func ListPMSSandboxStores(operator *dto.AuthPrincipal) ([]response.PMSSandboxStoreOption, error) {
	if operator == nil {
		return nil, errorsx.Unauthorized("请先登录")
	}
	if !config.PMSSandboxAvailable() {
		return nil, errorsx.Forbidden("测试 PMS 仅在 test-2 显式启用后可用")
	}
	scope := AgentTeamScopeService.Resolve(operator)
	result := make([]response.PMSSandboxStoreOption, 0)
	if !scope.Unrestricted && len(scope.StoreIDs) == 0 {
		return result, nil
	}
	cnd := sqls.NewCnd().Eq("status", enums.StatusOk).Asc("id")
	if !scope.Unrestricted {
		cnd.In("id", scope.StoreIDs)
	}
	for _, store := range repositories.StoreRepository.Find(sqls.DB(), cnd) {
		result = append(result, response.PMSSandboxStoreOption{ID: store.ID, Name: store.Name})
	}
	return result, nil
}
