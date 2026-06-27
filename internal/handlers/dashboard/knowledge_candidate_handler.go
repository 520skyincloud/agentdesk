package dashboard

import (
	"agent-desk/internal/builders"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/dto/response"
	"agent-desk/internal/pkg/httpx"
	"agent-desk/internal/pkg/httpx/params"
	"agent-desk/internal/services"

	"github.com/gin-gonic/gin"
	"github.com/mlogclub/simple/web"
)

func KnowledgeCandidateAnyList(ctx *gin.Context) {
	if _, err := services.AuthService.RequirePermission(ctx, constants.PermissionKnowledgeBaseView); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	cnd := params.NewPagedSqlCnd(ctx,
		params.QueryFilter{ParamName: "storeId"},
		params.QueryFilter{ParamName: "knowledgeBaseId"},
		params.QueryFilter{ParamName: "source"},
		params.QueryFilter{ParamName: "status"},
		params.QueryFilter{ParamName: "question", Op: params.Like},
	).Desc("frequency").Desc("id")
	list, paging := services.KnowledgeCandidateService.FindPageByCnd(cnd)
	results := builders.BuildKnowledgeCandidateList(list)
	for i := range results {
		if store := services.StoreService.Get(results[i].StoreID); store != nil {
			results[i].StoreCode = store.StoreCode
			results[i].StoreName = store.Name
		}
		if kb := services.KnowledgeBaseService.Get(results[i].KnowledgeBaseID); kb != nil {
			results[i].KnowledgeBaseName = kb.Name
		}
	}
	httpx.WriteJSON(ctx, &web.PageResult{Results: results, Page: paging})
}

func KnowledgeCandidatePostUpdate(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionKnowledgeBaseUpdate)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	req := request.UpdateKnowledgeCandidateRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, services.KnowledgeCandidateService.Update(req, operator))
}

func KnowledgeCandidatePostApprove(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionKnowledgeBaseUpdate)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	req := request.ReviewKnowledgeCandidateRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, services.KnowledgeCandidateService.Approve(req.ID, operator))
}

func KnowledgeCandidatePostReject(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionKnowledgeBaseUpdate)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	req := request.ReviewKnowledgeCandidateRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, services.KnowledgeCandidateService.Reject(req.ID, operator))
}

func KnowledgeCandidatePostMark_imported(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionKnowledgeBaseUpdate)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	req := request.ReviewKnowledgeCandidateRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, services.KnowledgeCandidateService.MarkImported(req.ID, operator))
}

func KnowledgeCandidatePostExport_weekly(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionKnowledgeBaseUpdate)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	req := request.ExportKnowledgeCandidateWeeklyRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	ret, err := services.KnowledgeCandidateService.ExportWeekly(req, operator)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if ret == nil {
		ret = &response.KnowledgeCandidateExportResponse{}
	}
	httpx.WriteJSON(ctx, ret)
}
