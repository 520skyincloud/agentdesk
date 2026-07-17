package api

import (
	"agent-desk/internal/builders"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/httpx"
	"agent-desk/internal/pkg/httpx/params"
	"agent-desk/internal/services"

	"github.com/gin-gonic/gin"
)

func ConversationEvaluationGetValidate(ctx *gin.Context) {
	item, err := services.ConversationEvaluationService.Validate(params.FormValue(ctx, "token"))
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, builders.BuildPublicConversationEvaluation(item))
}

func ConversationEvaluationPostSubmit(ctx *gin.Context) {
	req := request.SubmitConversationEvaluationRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	item, err := services.ConversationEvaluationService.Submit(req)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, builders.BuildPublicConversationEvaluation(item))
}
