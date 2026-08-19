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

	addFunc(c, "@every 5m", func() {
		result := services.ArrivalMaintenanceService.ProcessDue(50)
		if result.CleanedContactWays > 0 ||
			result.RetriedContactWays > 0 ||
			result.ReconciledBindings > 0 ||
			result.ReconciledAcquisitionCustomers > 0 {
			slog.Info(
				"arrival maintenance completed",
				"cleaned_contact_ways", result.CleanedContactWays,
				"retried_contact_ways", result.RetriedContactWays,
				"reconciled_bindings", result.ReconciledBindings,
				"reconciled_acquisition_customers", result.ReconciledAcquisitionCustomers,
			)
		}
	})

	addFunc(c, "@every 1s", func() {
		count := services.WxWorkProtocolService.DispatchPendingOutbox(50)
		if count > 0 {
			slog.Info("wxwork protocol outbox dispatched", "count", count)
		}
	})

	addFunc(c, "@every 10s", func() {
		count, err := services.ChannelMessageOutboxService.RepairMissingOutboundMessages(100)
		if err != nil {
			slog.Warn("repair missing channel message outbox failed", "repaired_count", count, "error", err)
			return
		}
		if count > 0 {
			slog.Info("missing channel message outbox repaired", "count", count)
		}
	})

	addFunc(c, "@every 1s", func() {
		count := services.AIManualResumeTaskService.ProcessDue(20)
		if count > 0 {
			slog.Info("AI manual resume tasks handled", "count", count)
		}
	})

	addFunc(c, "@every 1s", func() {
		count := services.MediaUnderstandingService.ProcessDue(4)
		if count > 0 {
			slog.Debug("media analyses dispatched", "count", count)
		}
	})

	addFunc(c, "@every 1s", func() {
		count := services.AIReplyJobService.ProcessDue(4)
		if count > 0 {
			slog.Info("AI reply jobs dispatched", "count", count)
		}
	})

	addFunc(c, "@every 1m", func() {
		count, err := services.AIReplyJobService.RepairMissingRecent(100)
		if err != nil {
			slog.Warn("repair missing AI reply jobs failed", "repaired_count", count, "error_class", "database_error")
			return
		}
		if count > 0 {
			slog.Info("missing AI reply jobs repaired", "count", count)
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

	addFunc(c, "@every 30s", func() {
		count := services.ModelProfileRolloutService.ProcessDue(5)
		if count > 0 {
			slog.Info("automatic model profile rollouts handled", "count", count)
		}
	})

	addFunc(c, "@every 5m", func() {
		// 契约 17.3.1：从历史检索命中离线回填知识质量元数据（幂等，
		// 不覆盖人工审核）。回填后 17.2 门禁按 claimType/trustLevel 生效。
		if count, err := services.KnowledgeEvidenceMetadataService.BackfillFromRetrieveHits(200); err != nil {
			slog.Warn("knowledge evidence metadata backfill failed", slog.Any("err", err))
		} else if count > 0 {
			slog.Info("knowledge evidence metadata backfilled", "count", count)
		}
	})

	addFunc(c, "@every 1m", func() {
		managedFastGPTCount := services.FastGPTUsageSyncService.ProcessDue(50)
		if managedFastGPTCount > 0 {
			slog.Info("FastGPT managed usage events imported", "count", managedFastGPTCount)
		}
	})

	addFunc(c, "@every 1m", func() {
		count := services.ConversationEvolutionService.ProcessDue(20)
		if count > 0 {
			slog.Info("customer tag evolution states handled", "count", count)
		}
	})

	c.Start()
}

func addFunc(c *cron.Cron, sepc string, cmd func()) {
	if _, err := c.AddFunc(sepc, cmd); err != nil {
		slog.Error("add cron func error", slog.Any("err", err))
	}
}
