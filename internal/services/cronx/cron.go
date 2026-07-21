package cronx

import (
	"agent-desk/internal/services"
	"fmt"
	"log/slog"

	"github.com/robfig/cron/v3"
)

func Init() {
	services.ConversationDispatchService.EnableRealtimeScheduling()
	c := cron.New()

	addFunc(c, "0 4 ? * *", func() {
		fmt.Println("cron test")
	})

	addFunc(c, "@every 30s", func() {
		if _, err := services.ConversationDispatchService.RecoverStaleAssignments(100); err != nil {
			slog.Warn("recover stale rule assignments failed", "error", err)
		}
		if _, err := services.ConversationDispatchService.DispatchPendingConversations(0); err != nil {
			slog.Warn("dispatch pending conversations loop failed", "error", err)
		}
	})

	// 好友申请和联系人变更由企微回调即时触发；低频扫描只补偿漏回调。
	addFunc(c, "@every 5m", func() {
		count := services.WxWorkProtocolContactAutomationService.Scan(20)
		if count > 0 {
			slog.Debug("wxwork contact automation scan completed", "count", count)
		}
	})

	addFunc(c, "@every 1s", func() {
		count := services.WxWorkProtocolService.DispatchPendingOutbox(50)
		if count > 0 {
			slog.Info("wxwork protocol outbox dispatched", "count", count)
		}
	})

	addFunc(c, "@every 1s", func() {
		count := services.AIManualResumeTaskService.ProcessDue(20)
		if count > 0 {
			slog.Info("AI manual resume tasks handled", "count", count)
		}
	})

	addFunc(c, "@every 1m", func() {
		count := services.ManualSessionTimeoutService.ScanAndRestoreExpired(50)
		if count > 0 {
			slog.Info("manual timeout tasks handled", "count", count)
		}
	})

	addFunc(c, "@every 10s", func() {
		count := services.FastGPTDatasetService.ProcessDue(10)
		if count > 0 {
			slog.Info("FastGPT dataset jobs handled", "count", count)
		}
	})

	addFunc(c, "@every 1m", func() {
		count := services.AIUsageGatewayCallService.ReconcilePending(50)
		if count > 0 {
			slog.Info("AI usage gateway calls reconciled", "count", count)
		}
		fastGPTCount := services.AIUsageGatewayCallService.ImportFastGPTPlatformUsage()
		if fastGPTCount > 0 {
			slog.Info("FastGPT platform model usage imported", "count", fastGPTCount)
		}
		managedFastGPTCount := services.FastGPTUsageSyncService.ProcessDue(50)
		if managedFastGPTCount > 0 {
			slog.Info("FastGPT managed usage events imported", "count", managedFastGPTCount)
		}
	})

	c.Start()
}

func addFunc(c *cron.Cron, sepc string, cmd func()) {
	if _, err := c.AddFunc(sepc, cmd); err != nil {
		slog.Error("add cron func error", slog.Any("err", err))
	}
}
