package services

import (
	"context"
	"fmt"
	"strings"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/config"
	"agent-desk/internal/pkg/newapi"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
)

const (
	AIUsageGatewayNewAPI          = "new_api"
	AIUsageReconcilePending       = "pending"
	AIUsageReconcileRetry         = "retry"
	AIUsageReconcileCompleted     = "completed"
	AIUsageMatchStrategyRequestID = "request_id"
	AIUsageMatchConfidenceExact   = "exact"
)

var AIUsageGatewayCallService = newAIUsageGatewayCallService()

type aiUsageGatewayCallService struct{}

func newAIUsageGatewayCallService() *aiUsageGatewayCallService {
	return &aiUsageGatewayCallService{}
}

func (s *aiUsageGatewayCallService) RecordFromEvent(event models.AIUsageEvent) error {
	gateway := strings.TrimSpace(event.Gateway)
	gatewayRequestID := strings.TrimSpace(event.GatewayRequestID)
	if gateway == "" || gatewayRequestID == "" {
		return nil
	}
	startedAt := event.CreatedAt
	finishedAt := event.CreatedAt
	if event.CallStartedAt != nil {
		startedAt = *event.CallStartedAt
	}
	if event.CallFinishedAt != nil {
		finishedAt = *event.CallFinishedAt
	}
	item := &models.AIUsageGatewayCall{
		CallKey:           gateway + ":" + gatewayRequestID,
		EventKey:          event.EventKey,
		CompanyID:         event.CompanyID,
		StoreID:           event.StoreID,
		WxWorkInstanceID:  event.WxWorkInstanceID,
		ConversationID:    event.ConversationID,
		MessageID:         event.MessageID,
		LocalRequestID:    event.RequestID,
		Stage:             event.Stage,
		Gateway:           gateway,
		GatewayRequestID:  gatewayRequestID,
		UpstreamRequestID: event.GatewayUpstreamID,
		StartedAt:         startedAt,
		FinishedAt:        finishedAt,
		LatencyMS:         finishedAt.Sub(startedAt).Milliseconds(),
		ReconcileStatus:   AIUsageReconcilePending,
		CreatedAt:         time.Now(),
		UpdatedAt:         time.Now(),
	}
	return repositories.AIUsageGatewayCallRepository.CreateIfAbsent(sqls.DB(), item)
}

func (s *aiUsageGatewayCallService) ReconcilePending(limit int) int {
	cfg := config.Current().NewAPIUsage
	if !cfg.Enabled {
		return 0
	}
	timeout := time.Duration(cfg.TimeoutMS) * time.Millisecond
	client, err := newapi.NewClient(newapi.Config{
		BaseURL: cfg.BaseURL, AccessToken: cfg.AccessToken, UserID: cfg.UserID, Timeout: timeout,
	})
	if err != nil {
		return 0
	}
	items := repositories.AIUsageGatewayCallRepository.FindPending(sqls.DB(), AIUsageGatewayNewAPI, limit)
	handled := 0
	for i := range items {
		if s.reconcileOne(client, &items[i]) == nil {
			handled++
		}
	}
	return handled
}

// ImportFastGPTPlatformUsage stores exact New API token usage for FastGPT's
// dedicated gateway token. It is intentionally platform-scoped until FastGPT
// propagates the originating Agent Desk request ID into its internal model calls.
func (s *aiUsageGatewayCallService) ImportFastGPTPlatformUsage() int {
	cfg := config.Current().NewAPIUsage
	if !cfg.Enabled || strings.TrimSpace(cfg.FastGPTTokenName) == "" {
		return 0
	}
	timeout := time.Duration(cfg.TimeoutMS) * time.Millisecond
	client, err := newapi.NewClient(newapi.Config{
		BaseURL: cfg.BaseURL, AccessToken: cfg.AccessToken, UserID: cfg.UserID, Timeout: timeout,
	})
	if err != nil {
		return 0
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	now := time.Now()
	items, err := client.ListAllUsageLogs(ctx, newapi.UsageLogQuery{
		StartTimestamp: now.Add(-10 * time.Minute).Unix(),
		EndTimestamp:   now.Add(time.Minute).Unix(),
		TokenName:      cfg.FastGPTTokenName,
		PageSize:       100,
	}, 100)
	if err != nil {
		return 0
	}
	created := 0
	for _, item := range items {
		requestID := strings.TrimSpace(item.RequestID)
		if requestID == "" {
			continue
		}
		if repositories.AIUsageGatewayCallRepository.TakeByGatewayRequestID(sqls.DB(), AIUsageGatewayNewAPI, requestID) != nil {
			continue
		}
		finishedAt := time.Unix(item.CreatedAt, 0)
		startedAt := finishedAt
		if item.UseTime > 0 && item.UseTime < 3600 {
			startedAt = finishedAt.Add(-time.Duration(item.UseTime) * time.Second)
		}
		now := time.Now()
		call := &models.AIUsageGatewayCall{
			CallKey:                  AIUsageGatewayNewAPI + ":fastgpt:" + requestID,
			Stage:                    "fastgpt_internal_model",
			Gateway:                  AIUsageGatewayNewAPI,
			GatewayRequestID:         requestID,
			UpstreamRequestID:        item.UpstreamRequestID,
			StartedAt:                startedAt,
			FinishedAt:               finishedAt,
			LatencyMS:                finishedAt.Sub(startedAt).Milliseconds(),
			ReconcileStatus:          AIUsageReconcileCompleted,
			MatchStrategy:            "dedicated_token_time_window",
			MatchConfidence:          "platform_aggregate",
			ExternalModel:            item.ModelName,
			ExternalTokenName:        item.TokenName,
			ExternalChannelID:        item.ChannelID,
			ExternalPromptTokens:     item.PromptTokens,
			ExternalCompletionTokens: item.CompletionTokens,
			ExternalQuota:            item.Quota,
			ExternalCreatedAt:        &finishedAt,
			ReconciledAt:             &now,
			CreatedAt:                now,
			UpdatedAt:                now,
		}
		if err := repositories.AIUsageGatewayCallRepository.CreateIfAbsent(sqls.DB(), call); err == nil {
			created++
		}
	}
	return created
}

func (s *aiUsageGatewayCallService) reconcileOne(client *newapi.Client, item *models.AIUsageGatewayCall) error {
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	usage, err := client.FindUsageByRequestID(ctx, item.GatewayRequestID)
	if err != nil {
		_ = repositories.AIUsageGatewayCallRepository.Updates(sqls.DB(), item.ID, map[string]any{
			"reconcile_status": AIUsageReconcileRetry,
			"last_error":       truncateUsageError(err.Error()),
			"updated_at":       time.Now(),
		})
		return err
	}
	if usage == nil {
		err = fmt.Errorf("new api usage log not available yet")
		_ = repositories.AIUsageGatewayCallRepository.Updates(sqls.DB(), item.ID, map[string]any{
			"reconcile_status": AIUsageReconcileRetry,
			"last_error":       err.Error(),
			"updated_at":       time.Now(),
		})
		return err
	}
	now := time.Now()
	externalCreatedAt := time.Unix(usage.CreatedAt, 0)
	return repositories.AIUsageGatewayCallRepository.Updates(sqls.DB(), item.ID, map[string]any{
		"reconcile_status":           AIUsageReconcileCompleted,
		"match_strategy":             AIUsageMatchStrategyRequestID,
		"match_confidence":           AIUsageMatchConfidenceExact,
		"external_model":             usage.ModelName,
		"external_token_name":        usage.TokenName,
		"external_channel_id":        usage.ChannelID,
		"external_prompt_tokens":     usage.PromptTokens,
		"external_completion_tokens": usage.CompletionTokens,
		"external_quota":             usage.Quota,
		"external_created_at":        &externalCreatedAt,
		"upstream_request_id":        firstNonBlank(usage.UpstreamRequestID, item.UpstreamRequestID),
		"reconciled_at":              &now,
		"last_error":                 "",
		"updated_at":                 now,
	})
}

func truncateUsageError(message string) string {
	runes := []rune(strings.TrimSpace(message))
	if len(runes) <= 500 {
		return string(runes)
	}
	return string(runes[:500])
}
