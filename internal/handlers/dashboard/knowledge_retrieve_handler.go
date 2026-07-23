package dashboard

import (
	"agent-desk/internal/pkg/httpx"

	"agent-desk/internal/ai/rag"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/errorsx"
	"agent-desk/internal/pkg/usagex"
	"agent-desk/internal/services"

	"agent-desk/internal/pkg/httpx/params"

	"github.com/gin-gonic/gin"
)

func KnowledgeRetrievePostDebugSearch(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionKnowledgeBaseView)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}

	req := request.KnowledgeSearchRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if _, err := validateKnowledgeDebugAccess(req.KnowledgeBaseIDs, req.ConversationID, operator); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}

	resp, err := rag.Answer.DebugSearch(ctx.Request.Context(), req)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, resp)
}

func KnowledgeRetrievePostDebugAnswer(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionKnowledgeBaseView)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}

	req := request.KnowledgeAnswerRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	storeID, err := validateKnowledgeDebugAccess(req.KnowledgeBaseIDs, req.ConversationID, operator)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	var resolved *services.ModelCallConfig
	if req.ConversationID > 0 {
		resolved, err = services.ModelCallResolverService.ResolveForConversation(req.ConversationID, enums.ModelUsageSlotReplyLLM)
	} else {
		resolved, err = services.ModelCallResolverService.Resolve(operator.ActiveTenantID, storeID, enums.ModelUsageSlotReplyLLM)
	}
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}

	debugCtx := usagex.WithScope(ctx.Request.Context(), services.ModelCallUsageScope(resolved, req.ConversationID, 0, "knowledge_debug_answer"))
	resp, err := rag.Answer.DebugAnswer(debugCtx, req, resolved.RuntimeConfig(), operator)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if !operator.IsPlatformAccount {
		resp.ModelName = ""
		resp.PromptTokens = 0
		resp.CompletionTokens = 0
	}
	httpx.WriteJSON(ctx, resp)
}

func validateKnowledgeDebugAccess(knowledgeBaseIDs []int64, conversationID int64, operator *dto.AuthPrincipal) (int64, error) {
	if err := services.KnowledgeBaseService.ValidateAccessibleIDs(knowledgeBaseIDs, operator); err != nil {
		return 0, err
	}
	if conversationID > 0 && !services.AgentTeamScopeService.CanViewConversation(operator, conversationID) {
		return 0, errorsx.Forbidden("无权限使用该会话进行知识库调试")
	}
	storeID := int64(0)
	for _, knowledgeBaseID := range knowledgeBaseIDs {
		knowledgeBase := services.KnowledgeBaseService.GetForOperator(knowledgeBaseID, operator)
		if knowledgeBase == nil || knowledgeBase.StoreID <= 0 || knowledgeBase.KnowledgeType != string(enums.KnowledgeBaseTypeFastGPTCloud) {
			return 0, errorsx.InvalidParam("知识调试只支持当前门店的 FastGPT 托管知识库")
		}
		if storeID == 0 {
			storeID = knowledgeBase.StoreID
		} else if storeID != knowledgeBase.StoreID {
			return 0, errorsx.InvalidParam("一次知识调试不能跨门店")
		}
	}
	if storeID <= 0 {
		return 0, errorsx.InvalidParam("知识库不能为空")
	}
	if conversationID > 0 {
		route := services.ConversationRouteService.GetByConversationIDInTenant(conversationID, operator.ActiveTenantID)
		if route == nil || route.StoreID != storeID {
			return 0, errorsx.InvalidParam("会话与知识库不属于同一门店")
		}
	}
	return storeID, nil
}
