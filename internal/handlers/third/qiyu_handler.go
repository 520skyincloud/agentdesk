package third

import (
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/httpx"
	"agent-desk/internal/pkg/httpx/params"
	"agent-desk/internal/services"

	"github.com/gin-gonic/gin"
)

func QiyuPostCallback(ctx *gin.Context) {
	req := request.QiyuCallbackRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		if formErr := params.ReadForm(ctx, &req); formErr != nil {
			httpx.WriteJSON(ctx, err)
			return
		}
	}
	httpx.WriteJSON(ctx, services.QiyuAdapterService.HandleCallback(req))
}
