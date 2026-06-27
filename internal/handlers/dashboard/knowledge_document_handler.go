package dashboard

import (
	"agent-desk/internal/builders"
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

func KnowledgeDocumentAnyList(ctx *gin.Context) {
	if _, err := services.AuthService.RequirePermission(ctx, constants.PermissionKnowledgeDocumentView); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}

	cnd := params.NewPagedSqlCnd(ctx,
		params.QueryFilter{ParamName: "knowledgeBaseId"},
		params.QueryFilter{ParamName: "title", Op: params.Like},
	).Desc("id")

	if status, ok := params.GetInt64(ctx, "status"); ok {
		cnd.Where("status = ?", status)
	} else {
		cnd.Where("status != ?", enums.StatusDeleted)
	}
	if indexStatus, ok := params.Get(ctx, "indexStatus"); ok {
		if !enums.IsValidKnowledgeDocumentIndexStatus(indexStatus) {
			httpx.WriteJSON(ctx, web.JsonErrorMsg("indexStatus参数不合法"))
			return
		}
		cnd.Where("index_status = ?", indexStatus)
	}

	list, paging := services.KnowledgeDocumentService.FindPageListByCnd(cnd)
	results := make([]response.KnowledgeDocumentListResponse, 0, len(list))
	for _, item := range list {
		results = append(results, builders.BuildKnowledgeDocumentList(&item))
	}
	httpx.WriteJSON(ctx, &web.PageResult{Results: results, Page: paging})
}

func KnowledgeDocumentGetBy(ctx *gin.Context) {
	id, ok := httpx.GetPathInt64(ctx, "id")
	if !ok {
		return
	}
	if _, err := services.AuthService.RequirePermission(ctx, constants.PermissionKnowledgeDocumentView); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}

	item := services.KnowledgeDocumentService.Get(id)
	if item == nil {
		httpx.WriteJSON(ctx, web.JsonErrorMsg("文档不存在"))
		return
	}
	httpx.WriteJSON(ctx, builders.BuildKnowledgeDocument(item))
}

func KnowledgeDocumentPostCreate(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionKnowledgeDocumentCreate)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}

	req := request.CreateKnowledgeDocumentRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	item, err := services.KnowledgeDocumentService.CreateKnowledgeDocument(req, operator)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, builders.BuildKnowledgeDocument(item))
}

func KnowledgeDocumentPostUpdate(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionKnowledgeDocumentUpdate)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}

	req := request.UpdateKnowledgeDocumentRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if err := services.KnowledgeDocumentService.UpdateKnowledgeDocument(req, operator); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, nil)
}

func KnowledgeDocumentPostDelete(ctx *gin.Context) {
	if _, err := services.AuthService.RequirePermission(ctx, constants.PermissionKnowledgeDocumentDelete); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}

	var req struct {
		ID int64 `json:"id"`
	}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}

	if err := services.KnowledgeDocumentService.DeleteKnowledgeDocument(req.ID); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, nil)
}
