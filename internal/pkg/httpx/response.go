package httpx

import (
	"agent-desk/internal/pkg/i18nx"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/mlogclub/simple/sqls"
	"github.com/mlogclub/simple/web"
)

type cursorData struct {
	results any
	cursor  string
	hasMore bool
}

type pageData struct {
	results any
	paging  *sqls.Paging
}

func CursorData(results any, cursor string, hasMore bool) any {
	return cursorData{results: results, cursor: cursor, hasMore: hasMore}
}

func PageData(results any, paging *sqls.Paging) any {
	return pageData{results: results, paging: paging}
}

func WriteJSON(ctx *gin.Context, result any) {
	ctx.JSON(http.StatusOK, localizeJSONResult(ctx, buildJSONResult(result)))
}

func WriteHttpStatusJSON(ctx *gin.Context, statusCode int, result any) {
	ctx.JSON(statusCode, localizeJSONResult(ctx, buildJSONResult(result)))
}

func localizeJSONResult(ctx *gin.Context, result *web.JsonResult) *web.JsonResult {
	if result == nil || result.Success || result.Message == "" {
		return result
	}
	result.Message = i18nx.TranslateKnownMessage(i18nx.Locale(ctx), result.Message)
	return result
}

func buildJSONResult(result any) *web.JsonResult {
	switch value := result.(type) {
	case nil:
		return web.JsonSuccess()
	case *web.JsonResult:
		return value
	case web.JsonResult:
		return &value
	case *web.CodeError:
		return web.JsonError(value)
	case web.CodeError:
		return web.JsonError(&value)
	case error:
		return web.JsonError(value)
	case cursorData:
		return web.JsonCursorData(value.results, value.cursor, value.hasMore)
	case pageData:
		return web.JsonPageData(value.results, value.paging)
	case web.RspBuilder:
		return value.JsonResult()
	case *web.RspBuilder:
		return value.JsonResult()
	default:
		return web.JsonData(result)
	}
}
