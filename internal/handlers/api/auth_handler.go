package api

import (
	"agent-desk/internal/pkg/config"
	"agent-desk/internal/pkg/dto/request"
	"agent-desk/internal/pkg/dto/response"
	"agent-desk/internal/pkg/httpx"
	"agent-desk/internal/pkg/httpx/params"
	"agent-desk/internal/services"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

func Login(ctx *gin.Context) {
	cfg := config.Current()
	req := request.LoginRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}

	ret, err := services.AuthService.Login(req, cfg.Auth, ctx.ClientIP(), ctx.GetHeader("User-Agent"))
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, ret)
}

func AuthPostEmailCodeSend(ctx *gin.Context) {
	req := request.SendEmailCodeRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	challenge, err := services.EmailVerificationService.SendCode(
		ctx.Request.Context(),
		services.EmailVerificationPurposeLogin,
		req.Email,
		"",
		ctx.ClientIP(),
		ctx.GetHeader("User-Agent"),
	)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, &response.EmailCodeChallengeResponse{
		ExpiresAt:         challenge.ExpiresAt.Format(time.RFC3339),
		RetryAfterSeconds: challenge.RetryAfterSecond,
	})
}

func AuthPostEmailCodeLogin(ctx *gin.Context) {
	req := request.EmailCodeLoginRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	ret, err := services.EmailVerificationService.LoginWithCode(
		ctx.Request.Context(),
		req.Email,
		req.Code,
		ctx.ClientIP(),
		ctx.GetHeader("User-Agent"),
		config.Current().Auth,
	)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, ret)
}

func AuthOptions(ctx *gin.Context) {
	cfg := config.Current()
	httpx.WriteJSON(ctx, &response.AuthOptionsResponse{
		WxWorkEnabled:             cfg.WxWork.Enabled,
		OIDCEnabled:               cfg.OIDC.Enabled,
		TenantRegistrationEnabled: cfg.TenantRegistration.Enabled,
		EmailCodeEnabled:          cfg.Email.Enabled,
	})
}

func WxWorkLogin(ctx *gin.Context) {
	loginURL, err := services.WxWorkLoginService.BuildWxWorkLoginURL(ctx.Query("next"))
	if err != nil {
		ctx.Redirect(http.StatusFound, "/login?wxworkError="+url.QueryEscape(wxWorkErrorMessage(err.Error())))
		return
	}
	ctx.Redirect(http.StatusFound, loginURL)
}

func WxWorkQRLogin(ctx *gin.Context) {
	loginURL, err := services.WxWorkLoginService.BuildWxWorkQRCodeLoginURL(ctx.Query("next"))
	if err != nil {
		ctx.Redirect(http.StatusFound, "/login?wxworkError="+url.QueryEscape(wxWorkErrorMessage(err.Error())))
		return
	}
	ctx.Redirect(http.StatusFound, loginURL)
}

func WxWorkCallback(ctx *gin.Context) {
	cfg := config.Current()
	ticket, next, err := services.WxWorkLoginService.LoginByWxWork(
		ctx.Query("code"),
		ctx.Query("state"),
		cfg.Auth,
		ctx.ClientIP(),
		ctx.GetHeader("User-Agent"),
	)
	if err != nil {
		ctx.Redirect(http.StatusFound, "/login?wxworkError="+url.QueryEscape(wxWorkErrorMessage(err.Error())))
		return
	}
	ctx.Redirect(http.StatusFound, "/dashboard/login/wxwork/callback?ticket="+url.QueryEscape(ticket)+"&next="+url.QueryEscape(next))
}

func WxWorkExchange(ctx *gin.Context) {
	req := request.WxWorkExchangeRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	ret, err := services.WxWorkLoginService.ExchangeWxWorkLoginTicket(req.Ticket)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, ret)
}

func OIDCLogin(ctx *gin.Context) {
	loginURL, err := services.OIDCLoginService.BuildOIDCLoginURL(ctx.Query("next"))
	if err != nil {
		ctx.Redirect(http.StatusFound, "/dashboard/login?oidcError="+url.QueryEscape(loginErrorMessage(err.Error())))
		return
	}
	ctx.Redirect(http.StatusFound, loginURL)
}

func OIDCCallback(ctx *gin.Context) {
	cfg := config.Current()
	ticket, next, err := services.OIDCLoginService.LoginByOIDC(
		ctx.Request.Context(),
		ctx.Query("code"),
		ctx.Query("state"),
		cfg.Auth,
		ctx.ClientIP(),
		ctx.GetHeader("User-Agent"),
	)
	if err != nil {
		ctx.Redirect(http.StatusFound, "/dashboard/login?oidcError="+url.QueryEscape(loginErrorMessage(err.Error())))
		return
	}
	ctx.Redirect(http.StatusFound, "/dashboard/login/oidc/callback?ticket="+url.QueryEscape(ticket)+"&next="+url.QueryEscape(next))
}

func OIDCExchange(ctx *gin.Context) {
	req := request.OIDCExchangeRequest{}
	if err := params.ReadJSON(ctx, &req); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	ret, err := services.OIDCLoginService.ExchangeOIDCLoginTicket(req.Ticket)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, ret)
}

func Logout(ctx *gin.Context) {
	if err := services.AuthService.Logout(ctx.GetHeader("Authorization")); err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, nil)
}

func Profile(ctx *gin.Context) {
	ret, err := services.AuthService.CurrentProfile(ctx)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}
	httpx.WriteJSON(ctx, ret)
}

func wxWorkErrorMessage(message string) string {
	return loginErrorMessage(message)
}

func loginErrorMessage(message string) string {
	if idx := strings.Index(message, ": "); idx >= 0 {
		message = message[idx+2:]
	}
	return message
}
