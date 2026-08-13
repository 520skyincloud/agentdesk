package dashboard

import (
	"agent-desk/internal/builders"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/httpx"
	"agent-desk/internal/pkg/httpx/params"
	"agent-desk/internal/services"

	"github.com/gin-gonic/gin"
)

func KnowledgeActionBindingAnyList(ctx *gin.Context) {
	if _, err := requirePlatformPermission(ctx, constants.PermissionReplyActionView); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, builders.BuildKnowledgeActionBindings(services.KnowledgeActionBindingService.List()))
}

func KnowledgeActionBindingPostSet(ctx *gin.Context) {
	user, err := requirePlatformPermission(ctx, constants.PermissionReplyActionUpdate)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	req := request.SetKnowledgeActionBindingRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	item, err := services.KnowledgeActionBindingService.Set(
		req.TenantID, req.StoreID, req.KnowledgeBaseID, req.SourceRecordID, req.ActionCode, req.Enabled, user,
	)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, builders.BuildKnowledgeActionBinding(item))
}

func KnowledgeActionBindingPostDelete(ctx *gin.Context) {
	user, err := requirePlatformPermission(ctx, constants.PermissionReplyActionUpdate)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	req := request.DeleteKnowledgeActionBindingRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if err := services.KnowledgeActionBindingService.Delete(req.ID, user); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, nil)
}
