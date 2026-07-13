package dashboard

import (
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/errorsx"
	"agent-desk/internal/pkg/httpx"
	"agent-desk/internal/pkg/httpx/params"
	"agent-desk/internal/services"

	"github.com/gin-gonic/gin"
)

func FastGPTDatasetPostProvision(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionKnowledgeBaseView)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	req := request.ProvisionFastGPTDatasetRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	job, err := services.FastGPTDatasetService.EnqueueDefaultDataset(req.StoreID, req.Name, operator)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, map[string]any{"jobId": job.ID, "status": job.Status})
}

func FastGPTDatasetPostUpload(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionKnowledgeBaseView)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	knowledgeBaseID, err := parseMultipartInt64(ctx, "knowledgeBaseId")
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	file, err := ctx.FormFile("file")
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	job, err := services.FastGPTDatasetService.EnqueueUpload(knowledgeBaseID, file, operator)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, map[string]any{"jobId": job.ID, "status": job.Status})
}

func FastGPTDatasetPostCollections(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionKnowledgeBaseView)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	req := request.FastGPTDatasetActionRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	ret, err := services.FastGPTDatasetService.ListCollections(ctx.Request.Context(), req.KnowledgeBaseID, operator)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, ret)
}

func FastGPTDatasetPostSearchTest(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionKnowledgeBaseView)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	req := request.FastGPTDatasetActionRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	ret, err := services.FastGPTDatasetService.SearchTest(ctx.Request.Context(), req.KnowledgeBaseID, req.Query, operator)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, ret)
}

func FastGPTDatasetPostDeleteCollection(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionKnowledgeBaseView)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	req := request.FastGPTDatasetActionRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if err := services.FastGPTDatasetService.DeleteCollection(ctx.Request.Context(), req.KnowledgeBaseID, req.CollectionID, operator); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, nil)
}

func parseMultipartInt64(ctx *gin.Context, name string) (int64, error) {
	value, ok := params.GetInt64(ctx, name)
	if !ok || value <= 0 {
		return 0, errorsx.InvalidParam("知识库参数不正确")
	}
	return value, nil
}
