package dashboard

import (
	"agent-desk/internal/pkg/httpx"
	"context"

	"agent-desk/internal/ai/rag"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/errorsx"
	"agent-desk/internal/services"

	"agent-desk/internal/pkg/httpx/params"

	"github.com/gin-gonic/gin"
	"github.com/mlogclub/simple/web"
)

func KnowledgeRetrievePostDebugSearch(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionKnowledgeDocumentView)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}

	req := request.KnowledgeSearchRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if err := validateKnowledgeDebugAccess(req.KnowledgeBaseIDs, req.ConversationID, operator); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}

	resp, err := rag.Answer.DebugSearch(context.Background(), req)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, resp)
}

func KnowledgeRetrievePostDebugAnswer(ctx *gin.Context) {
	operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionKnowledgeDocumentView)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}

	req := request.KnowledgeAnswerRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	if err := validateKnowledgeDebugAccess(req.KnowledgeBaseIDs, req.ConversationID, operator); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}

	resp, err := rag.Answer.DebugAnswer(context.Background(), req, operator)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, resp)
}

func KnowledgeRetrievePostBuild(ctx *gin.Context) {
	req := struct {
		DocumentID int64 `json:"documentId"`
		FAQID      int64 `json:"faqId"`
	}{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}

	if req.DocumentID > 0 {
		operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionKnowledgeDocumentUpdate)
		if err != nil {
			httpx.WriteJSON(ctx, err)
			return
		}
		if services.KnowledgeDocumentService.GetForOperator(req.DocumentID, operator) == nil {
			httpx.WriteJSON(ctx, web.JsonErrorMsg("文档不存在或无权访问"))
			return
		}
		if err := rag.Answer.BuildDocumentIndex(context.Background(), req.DocumentID); err != nil {
			httpx.WriteJSON(ctx, err)
			return
		}
		httpx.WriteJSON(ctx, nil)
		return
	}

	if req.FAQID > 0 {
		operator, err := services.AuthService.RequirePermission(ctx, constants.PermissionKnowledgeFAQUpdate)
		if err != nil {
			httpx.WriteJSON(ctx, err)
			return
		}
		if services.KnowledgeFAQService.GetForOperator(req.FAQID, operator) == nil {
			httpx.WriteJSON(ctx, web.JsonErrorMsg("FAQ不存在或无权访问"))
			return
		}
		if err := rag.Index.IndexFAQByID(context.Background(), req.FAQID); err != nil {
			httpx.WriteJSON(ctx, err)
			return
		}
		httpx.WriteJSON(ctx, nil)
		return
	}

	httpx.WriteJSON(ctx, web.JsonErrorMsg("documentId或faqId不能为空"))
}

func validateKnowledgeDebugAccess(knowledgeBaseIDs []int64, conversationID int64, operator *dto.AuthPrincipal) error {
	if err := services.KnowledgeBaseService.ValidateAccessibleIDs(knowledgeBaseIDs, operator); err != nil {
		return err
	}
	if conversationID > 0 && !services.AgentTeamScopeService.CanViewConversation(operator, conversationID) {
		return errorsx.Forbidden("无权限使用该会话进行知识库调试")
	}
	return nil
}
