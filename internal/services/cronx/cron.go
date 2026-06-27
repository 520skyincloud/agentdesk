package cronx

import (
	"agent-desk/internal/services"
	"fmt"
	"log/slog"

	"github.com/robfig/cron/v3"
)

func Init() {
	c := cron.New()

	addFunc(c, "0 4 ? * *", func() {
		fmt.Println("cron test")
	})

	addFunc(c, "@every 30s", func() {
		if _, err := services.ConversationDispatchService.DispatchPendingConversations(0); err != nil {
			slog.Warn("dispatch pending conversations loop failed", "error", err)
		}
	})

	addFunc(c, "@every 1s", func() {
		count := services.WxWorkKFOutboundService.DispatchPendingOutbox()
		count += services.WxWorkProtocolService.DispatchPendingOutbox(50)
		if count > 0 {
			slog.Info("wxwork kf outbox dispatched", "count", count)
		}
	})

	addFunc(c, "@every 1m", func() {
		count := services.ManualSessionTimeoutService.ScanAndRestoreExpired(50)
		if count > 0 {
			slog.Info("manual sessions restored to ai", "count", count)
		}
	})

	c.Start()
}

func addFunc(c *cron.Cron, sepc string, cmd func()) {
	if _, err := c.AddFunc(sepc, cmd); err != nil {
		slog.Error("add cron func error", slog.Any("err", err))
	}
}
