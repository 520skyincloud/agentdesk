package api

import (
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/httpx"
	"agent-desk/internal/pkg/httpx/params"
	"agent-desk/internal/services"

	"github.com/gin-gonic/gin"
)

func MiniprogramPostSessionStart(ctx *gin.Context) {
	req := request.MiniprogramSessionStartRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	resp, err := services.MiniprogramChatService.StartSession(req)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, resp)
}

func MiniprogramPostMessageSend(ctx *gin.Context) {
	req := request.MiniprogramMessageSendRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	resp, err := services.MiniprogramChatService.SendMessage(req)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, resp)
}

func MiniprogramGetMessageList(ctx *gin.Context) {
	req := request.MiniprogramChatContextRequest{}
	req.SessionID, _ = params.Get(ctx, "sessionId")
	req.ConversationID, _ = params.GetInt64(ctx, "conversationId")
	req.StoreID, _ = params.Get(ctx, "storeId")
	req.BrandCode, _ = params.Get(ctx, "brandCode")
	req.OrderNo, _ = params.Get(ctx, "orderNo")
	req.Source, _ = params.Get(ctx, "source")
	req.HotelName, _ = params.Get(ctx, "hotelName")
	req.ChannelID, _ = params.Get(ctx, "channelId")
	limit, _ := params.GetInt(ctx, "limit")
	resp, err := services.MiniprogramChatService.ListMessages(req, limit)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, resp)
}
