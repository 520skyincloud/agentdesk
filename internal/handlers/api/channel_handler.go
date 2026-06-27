package api

import (
	"agent-desk/internal/pkg/dto/response"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/errorsx"
	"agent-desk/internal/pkg/httpx"
	"agent-desk/internal/services"

	"github.com/gin-gonic/gin"
	"github.com/mlogclub/simple/web"
)

func ChannelAnyConfig(ctx *gin.Context) {
	channel := services.ChannelService.GetEnabledChannel(ctx)
	if channel == nil {
		httpx.WriteJSON(ctx, web.JsonErrorMsg("接入渠道未初始化"))
		return
	}
	cfg, err := resolveWidgetConfig(channel.ChannelType, channel.ConfigJSON)
	if err != nil {
		httpx.WriteJSON(ctx, err)
		return
	}

	ret := response.WidgetConfigResponse{
		ChannelID:   channel.ChannelID,
		ChannelType: channel.ChannelType,
		Title:       cfg.Title,
		Subtitle:    cfg.Subtitle,
		ThemeColor:  cfg.ThemeColor,
		Position:    cfg.Position,
		Width:       cfg.Width,
	}
	httpx.WriteJSON(ctx, ret)
}

type webLikeWidgetConfig struct {
	Title      string
	Subtitle   string
	ThemeColor string
	Position   string
	Width      string
}

func resolveWidgetConfig(channelType, rawConfig string) (*webLikeWidgetConfig, error) {
	switch channelType {
	case enums.ChannelTypeWeb:
		cfg, err := services.ChannelService.ParseWebChannelConfig(rawConfig)
		if err != nil {
			return nil, err
		}
		return &webLikeWidgetConfig{
			Title:      cfg.Title,
			Subtitle:   cfg.Subtitle,
			ThemeColor: cfg.ThemeColor,
			Position:   cfg.Position,
			Width:      cfg.Width,
		}, nil
	case enums.ChannelTypeWechatMP:
		cfg, err := services.ChannelService.ParseWechatMPChannelConfig(rawConfig)
		if err != nil {
			return nil, err
		}
		return &webLikeWidgetConfig{
			Title:      cfg.Title,
			Subtitle:   cfg.Subtitle,
			ThemeColor: cfg.ThemeColor,
			Position:   "right",
			Width:      "100%",
		}, nil
	default:
		return nil, errorsx.InvalidParam("该渠道不支持开放客服配置")
	}
}
