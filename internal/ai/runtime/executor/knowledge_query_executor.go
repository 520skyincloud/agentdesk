package executor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"agent-desk/internal/ai/rag"
	"agent-desk/internal/ai/runtime/internal/impl/callbacks"
	"agent-desk/internal/ai/runtime/internal/impl/retrievers"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/dto/response"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/tracex"
	"agent-desk/internal/pkg/usagex"

	"golang.org/x/text/unicode/norm"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	knowledgeCheckpointLeaseDuration = 90 * time.Second
	knowledgeCheckpointPollInterval  = 40 * time.Millisecond
	checkpointKnowledgeDefaultTopK   = 8
	checkpointKnowledgeDefaultScore  = 0.3
)

var errKnowledgeCheckpointFailed = errors.New("knowledge checkpoint already failed")

func knowledgeTaskConcurrencyForDB(db *gorm.DB) int {
	if db != nil && db.Dialector != nil && db.Dialector.Name() == "sqlite" {
		// SQLite serializes writers at database/table level. ExecuteKnowledgeQuery
		// persists its checkpoint before and after the external request, so running
		// several instances concurrently can deadlock even when the retrievals are
		// independent. MySQL keeps the normal bounded parallelism used in production.
		return 1
	}
	return runtimeKnowledgeTaskConcurrency
}

// KnowledgeRetrieverIFace 是执行器依赖的最小检索接口。
type KnowledgeRetrieverIFace interface {
	RetrieveContextByOptions(context.Context, retrievers.KnowledgeRetrieveOptions, string) (*retrievers.KnowledgeRetrieveResult, error)
}

// KnowledgeQueryPlanV2 是一次知识检索的完整持久范围。持久 Task 的 checkpoint
// 不包含 TurnVersion：同一 Task 在 Turn 升版或 Job 恢复后必须复用同一份证据。
// 尚未创建 Task 的条件探测才使用 TurnVersion 隔离不同输入版本。
type KnowledgeQueryPlanV2 struct {
	TenantID         int64
	StoreID          int64
	ConversationID   int64
	SessionNo        int
	TurnID           int64
	TurnVersion      int
	TaskID           int64
	TaskKey          string
	ScopeFingerprint string
	Query            string
	QueryFingerprint string
	QueryKey         string
	Purpose          string // answer | conditional_probe
	CheckpointKey    string
}

// BuildKnowledgeQueryPlan 构造稳定计划（scope = tenant+store+conversation+session）。
func BuildKnowledgeQueryPlan(
	tenantID, storeID, conversationID int64,
	sessionNo int,
	query, purpose string,
	turnID int64,
	turnVersion int,
	taskID int64,
	taskKey string,
) KnowledgeQueryPlanV2 {
	query = strings.TrimSpace(query)
	purpose = strings.TrimSpace(purpose)
	if purpose == "" {
		purpose = "answer"
	}
	taskKey = strings.TrimSpace(taskKey)
	scope := fmt.Sprintf("%d/%d/%d/%d", tenantID, storeID, conversationID, sessionNo)
	scopeFP := sha256hex(scope)
	queryFP := sha256hex(canonicalKnowledgeQuery(query))
	taskIdentity := fmt.Sprintf("task/%d/%s", taskID, taskKey)
	if taskID <= 0 {
		taskIdentity = fmt.Sprintf("probe/%d/%s", turnVersion, taskKey)
	}
	checkpointIdentity := fmt.Sprintf(
		"%d/%s/%d/%s/%s/%s",
		tenantID,
		scopeFP,
		turnID,
		taskIdentity,
		purpose,
		queryFP,
	)
	checkpointKey := sha256hex(checkpointIdentity)
	return KnowledgeQueryPlanV2{
		TenantID: tenantID, StoreID: storeID, ConversationID: conversationID, SessionNo: sessionNo,
		TurnID: turnID, TurnVersion: turnVersion, TaskID: taskID, TaskKey: taskKey,
		ScopeFingerprint: scopeFP,
		Query:            query, QueryFingerprint: queryFP,
		QueryKey: "kq_" + checkpointKey[:24], Purpose: purpose,
		CheckpointKey: checkpointKey,
	}
}

func canonicalKnowledgeQuery(value string) string {
	value = norm.NFKC.String(strings.ToLower(strings.TrimSpace(value)))
	value = strings.Join(strings.Fields(value), " ")
	return strings.TrimRightFunc(value, func(r rune) bool {
		return unicode.IsPunct(r) || unicode.IsSpace(r)
	})
}

func sha256hex(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

// ExecuteKnowledgeQuery 统一执行器：先查 checkpoint（仅复用已盖章的检索行），
// 命中 succeeded 直接复用；否则调用检索（retriever 自写日志行），成功后把
// 该日志行盖章为本计划 checkpoint（query_key/purpose/scope/终态）。
// 不再自建日志行，避免空 hit 行污染复用缓存。
func ExecuteKnowledgeQuery(
	ctx context.Context,
	plan KnowledgeQueryPlanV2,
	retriever KnowledgeRetrieverIFace,
	options retrievers.KnowledgeRetrieveOptions,
	db *gorm.DB,
) (*retrievers.KnowledgeRetrieveResult, error) {
	// 无 DB（纯函数测试/降级）：直查，不建 checkpoint。
	if db == nil {
		return retriever.RetrieveContextByOptions(ctx, options, plan.Query)
	}
	// 1. 终态复用：succeeded/no_hit 直接返回；failed 返回确定错误。
	if cached := findTerminalCheckpoint(db, plan, options); cached != nil {
		return cached, nil
	}
	if findFailedCheckpoint(db, plan) {
		return nil, errKnowledgeCheckpointFailed
	}
	// 2. 原子 Claim：拿到租约的 Worker 才请求上游。
	owner := knowledgeCheckpointOwner()
	checkpoint := newKnowledgeCheckpoint(ctx, plan, owner)
	claimed, err := claimKnowledgeCheckpoint(db, checkpoint)
	if err != nil {
		return nil, err
	}
	if !claimed {
		// 3. 其他 Worker 持有租约：等待终态（含租约过期后重试）。
		result, retry, waitErr := waitForKnowledgeCheckpoint(ctx, db, plan, options)
		if waitErr != nil {
			return nil, waitErr
		}
		if !retry && result != nil {
			return result, nil
		}
		// retry=true：租约过期或无记录，递归重 Claim（有限次数由 ctx 控制）。
		reclaimed, reclaimErr := claimKnowledgeCheckpoint(db, checkpoint)
		if reclaimErr != nil {
			return nil, reclaimErr
		}
		if !reclaimed {
			return nil, errKnowledgeCheckpointFailed
		}
	}
	// 4. 调用上游：告知 retriever 日志由执行器管理，绑定计划字段。
	options.CheckpointManaged = true
	options.QueryKey = plan.QueryKey
	options.QueryPurpose = plan.Purpose
	options.ScopeFingerprint = plan.ScopeFingerprint
	options.TurnID = plan.TurnID
	options.TurnVersion = plan.TurnVersion
	options.TaskID = plan.TaskID
	options.TaskKey = plan.TaskKey
	result, err := retriever.RetrieveContextByOptions(ctx, options, plan.Query)
	if err != nil {
		// 5a. 上游失败：当前 checkpoint 标记 failed，不重试上游。
		return nil, finishKnowledgeCheckpointAfterError(db, checkpoint.ID, plan.TenantID, owner, err)
	}
	// 5b. 成功/no_hit：hits、used hits、状态、审计一次性写入同一 RetrieveLog。
	status := terminalFor(result)
	if err := completeKnowledgeCheckpoint(ctx, db, checkpoint.ID, owner, plan, options, result, status); err != nil {
		_ = finishKnowledgeCheckpoint(db, checkpoint.ID, plan.TenantID, "failed", owner)
		return nil, err
	}
	result.RetrieveLogID = checkpoint.ID
	return result, nil
}

// findFailedCheckpoint 检查是否存在 failed 终态（不复用、不重试上游）。
func findFailedCheckpoint(db *gorm.DB, plan KnowledgeQueryPlanV2) bool {
	if db == nil || strings.TrimSpace(plan.CheckpointKey) == "" {
		return false
	}
	var count int64
	db.Model(&models.KnowledgeRetrieveLog{}).
		Where("tenant_id = ? AND checkpoint_key = ? AND execution_status = ?", plan.TenantID, plan.CheckpointKey, "failed").
		Count(&count)
	return count > 0
}

// knowledgeCheckpointOwner 生成本次 Claim 的租约持有者标识。
func knowledgeCheckpointOwner() string {
	return fmt.Sprintf("kx_%d", time.Now().UnixNano())
}

func terminalFor(result *retrievers.KnowledgeRetrieveResult) string {
	if result == nil || len(result.Hits) == 0 {
		return "no_hit"
	}
	return "succeeded"
}

func newKnowledgeCheckpoint(ctx context.Context, plan KnowledgeQueryPlanV2, owner string) *models.KnowledgeRetrieveLog {
	now := time.Now()
	expiresAt := now.Add(knowledgeCheckpointLeaseDuration)
	checkpointKey := plan.CheckpointKey
	requestID := strings.TrimSpace(tracex.RequestIDFromContext(ctx))
	if requestID == "" {
		requestID = strings.TrimSpace(usagex.ScopeFromContext(ctx).RequestID)
	}
	return &models.KnowledgeRetrieveLog{
		TenantID: plan.TenantID, ConversationID: plan.ConversationID, RequestID: requestID,
		Question: plan.Query, SourceType: "fastgpt",
		Channel: string(enums.KnowledgeRetrieveChannelIM), Scene: string(enums.KnowledgeRetrieveSceneFirstResponse),
		TurnID: plan.TurnID, TurnVersion: plan.TurnVersion, TaskID: plan.TaskID, TaskKey: plan.TaskKey,
		QueryFingerprint: plan.QueryFingerprint, QueryKey: plan.QueryKey, QueryPurpose: plan.Purpose,
		ScopeFingerprint: plan.ScopeFingerprint, CheckpointKey: &checkpointKey,
		ExecutionStatus: "running", LeaseOwner: owner, LeaseExpiresAt: &expiresAt,
		CreatedAt: now,
	}
}

func claimKnowledgeCheckpoint(db *gorm.DB, checkpoint *models.KnowledgeRetrieveLog) (bool, error) {
	if db == nil || checkpoint == nil || checkpoint.CheckpointKey == nil {
		return false, fmt.Errorf("knowledge checkpoint is invalid")
	}
	result := db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "checkpoint_key"}},
		DoNothing: true,
	}).Create(checkpoint)
	if result.Error != nil {
		return false, result.Error
	}
	if result.RowsAffected > 0 && checkpoint.ID > 0 {
		return true, nil
	}

	existing := &models.KnowledgeRetrieveLog{}
	if err := db.Where("tenant_id = ? AND checkpoint_key = ?", checkpoint.TenantID, *checkpoint.CheckpointKey).
		Take(existing).Error; err != nil {
		return false, err
	}
	checkpoint.ID = existing.ID
	if existing.ExecutionStatus == "succeeded" || existing.ExecutionStatus == "no_hit" {
		return false, nil
	}
	now := time.Now()
	claimable := existing.ExecutionStatus == "pending" ||
		existing.ExecutionStatus == "running" && (existing.LeaseExpiresAt == nil || !existing.LeaseExpiresAt.After(now))
	if !claimable {
		return false, nil
	}
	expiresAt := now.Add(knowledgeCheckpointLeaseDuration)
	claim := db.Model(&models.KnowledgeRetrieveLog{}).
		Where("id = ? AND tenant_id = ? AND checkpoint_key = ? AND execution_status NOT IN ? AND (execution_status IN ? OR lease_expires_at IS NULL OR lease_expires_at <= ?)",
			existing.ID,
			existing.TenantID,
			*checkpoint.CheckpointKey,
			[]string{"succeeded", "no_hit"},
			[]string{"pending"},
			now,
		).
		Updates(map[string]any{
			"execution_status": "running",
			"lease_owner":      checkpoint.LeaseOwner,
			"lease_expires_at": expiresAt,
			"completed_at":     nil,
		})
	return claim.RowsAffected == 1, claim.Error
}

func waitForKnowledgeCheckpoint(
	ctx context.Context,
	db *gorm.DB,
	plan KnowledgeQueryPlanV2,
	options retrievers.KnowledgeRetrieveOptions,
) (*retrievers.KnowledgeRetrieveResult, bool, error) {
	ticker := time.NewTicker(knowledgeCheckpointPollInterval)
	defer ticker.Stop()
	for {
		row := &models.KnowledgeRetrieveLog{}
		err := db.Where("tenant_id = ? AND checkpoint_key = ?", plan.TenantID, plan.CheckpointKey).Take(row).Error
		if err != nil {
			if err == gorm.ErrRecordNotFound {
				return nil, true, nil
			}
			return nil, false, err
		}
		switch row.ExecutionStatus {
		case "succeeded", "no_hit":
			return checkpointResult(db, row, options), false, nil
		case "pending":
			return nil, true, nil
		case "failed", "superseded":
			return nil, false, errKnowledgeCheckpointFailed
		case "running":
			if row.LeaseExpiresAt == nil || !row.LeaseExpiresAt.After(time.Now()) {
				return nil, true, nil
			}
		default:
			return nil, true, nil
		}
		select {
		case <-ctx.Done():
			return nil, false, ctx.Err()
		case <-ticker.C:
		}
	}
}

func findTerminalCheckpoint(db *gorm.DB, plan KnowledgeQueryPlanV2, options retrievers.KnowledgeRetrieveOptions) *retrievers.KnowledgeRetrieveResult {
	if db == nil || strings.TrimSpace(plan.CheckpointKey) == "" {
		return nil
	}
	row := &models.KnowledgeRetrieveLog{}
	err := db.Where("tenant_id = ? AND checkpoint_key = ? AND execution_status IN ?",
		plan.TenantID, plan.CheckpointKey, []string{"succeeded", "no_hit"}).
		Order("id DESC").First(row).Error
	if err != nil || row.ID <= 0 {
		return nil
	}
	return checkpointResult(db, row, options)
}

func checkpointResult(db *gorm.DB, row *models.KnowledgeRetrieveLog, options retrievers.KnowledgeRetrieveOptions) *retrievers.KnowledgeRetrieveResult {
	if db == nil || row == nil || row.ID <= 0 {
		return nil
	}
	var hitRows []models.KnowledgeRetrieveHit
	_ = db.Where("tenant_id = ? AND retrieve_log_id = ?", row.TenantID, row.ID).
		Order("rank_no ASC, id ASC").Find(&hitRows).Error
	hits := make([]rag.RetrieveResult, 0, len(hitRows))
	contextHits := make([]rag.RetrieveResult, 0, len(hitRows))
	for _, hit := range hitRows {
		item := rag.RetrieveResult{
			KnowledgeBaseID: hit.KnowledgeBaseID,
			SourceRecordID:  hit.SourceRecordID,
			DocumentTitle:   hit.DocumentTitle,
			Title:           hit.Title,
			SectionPath:     hit.SectionPath,
			Content:         hit.Snippet,
			Score:           float32(hit.Score),
		}
		hits = append(hits, item)
		if hit.UsedInAnswer {
			contextHits = append(contextHits, item)
		}
	}
	if len(contextHits) == 0 && len(hits) > 0 {
		contextHits = append(contextHits, hits...)
	}
	knowledgeBaseIDs := hitKnowledgeBaseIDs(hits)
	if len(knowledgeBaseIDs) == 0 && row.KnowledgeBaseID > 0 {
		knowledgeBaseIDs = []int64{row.KnowledgeBaseID}
	}
	policies, answerMode := checkpointKnowledgeMetadata(db, row.TenantID, knowledgeBaseIDs, hits, options)
	traceItems := checkpointTraceItems(row, hitRows)
	traceSummary := callbacks.RetrieverTraceSummary{
		TopK: options.TopK, ScoreThreshold: options.ScoreThreshold,
		ContextMaxTokens: options.ContextMaxTokens, MaxContextItems: options.MaxContextItems,
		HitCount: len(hits), ContextCount: len(contextHits), VectorSearchMs: row.RetrieveMs,
		Policies: checkpointPolicyTraceItems(policies),
	}
	if traceSummary.TopK <= 0 && len(policies) == 1 {
		traceSummary.TopK = policies[0].TopK
	}
	if traceSummary.ScoreThreshold <= 0 && len(policies) == 1 {
		traceSummary.ScoreThreshold = policies[0].ScoreThreshold
	}
	return &retrievers.KnowledgeRetrieveResult{
		KnowledgeBaseIDs: knowledgeBaseIDs,
		RetrieveLogID:    row.ID,
		Query:            row.Question,
		Options:          options,
		Hits:             hits,
		ContextResults:   contextHits,
		ContextText:      hitsContextText(contextHits),
		TopScore:         topScore(hits),
		AnswerMode:       answerMode,
		TraceItems:       traceItems,
		TraceSummary:     traceSummary,
		Policies:         policies,
		RetrieveMs:       row.RetrieveMs,
	}
}

func checkpointKnowledgeMetadata(
	db *gorm.DB,
	tenantID int64,
	knowledgeBaseIDs []int64,
	hits []rag.RetrieveResult,
	options retrievers.KnowledgeRetrieveOptions,
) ([]retrievers.KnowledgeBaseRetrievePolicy, enums.KnowledgeAnswerMode) {
	answerMode := enums.KnowledgeAnswerModeStrict
	if db == nil || tenantID <= 0 || len(knowledgeBaseIDs) == 0 {
		return nil, answerMode
	}
	var bases []models.KnowledgeBase
	if err := db.Where("tenant_id = ? AND id IN ? AND status = ?", tenantID, knowledgeBaseIDs, enums.StatusOk).
		Find(&bases).Error; err != nil {
		return nil, answerMode
	}
	byID := make(map[int64]models.KnowledgeBase, len(bases))
	for _, base := range bases {
		byID[base.ID] = base
	}
	policies := make([]retrievers.KnowledgeBaseRetrievePolicy, 0, len(knowledgeBaseIDs))
	for _, knowledgeBaseID := range knowledgeBaseIDs {
		base, ok := byID[knowledgeBaseID]
		if !ok {
			continue
		}
		topK := base.DefaultTopK
		if topK <= 0 {
			topK = checkpointKnowledgeDefaultTopK
		}
		if options.TopK > 0 {
			topK = options.TopK
		}
		score := base.DefaultScoreThreshold
		if score <= 0 {
			score = checkpointKnowledgeDefaultScore
		}
		if options.ScoreThreshold > 0 {
			score = options.ScoreThreshold
		}
		policies = append(policies, retrievers.KnowledgeBaseRetrievePolicy{
			KnowledgeBaseID: knowledgeBaseID, TopK: topK, ScoreThreshold: score,
		})
	}
	preferredID := int64(0)
	if len(hits) > 0 {
		preferredID = hits[0].KnowledgeBaseID
	}
	if preferredID <= 0 && len(knowledgeBaseIDs) > 0 {
		preferredID = knowledgeBaseIDs[0]
	}
	if base, ok := byID[preferredID]; ok && enums.KnowledgeAnswerMode(base.AnswerMode) != 0 {
		answerMode = enums.KnowledgeAnswerMode(base.AnswerMode)
	}
	return policies, answerMode
}

func checkpointPolicyTraceItems(items []retrievers.KnowledgeBaseRetrievePolicy) []callbacks.RetrieverPolicyTraceItem {
	ret := make([]callbacks.RetrieverPolicyTraceItem, 0, len(items))
	for _, item := range items {
		ret = append(ret, callbacks.RetrieverPolicyTraceItem{
			KnowledgeBaseID: item.KnowledgeBaseID, TopK: item.TopK, ScoreThreshold: item.ScoreThreshold,
		})
	}
	return ret
}

func checkpointTraceItems(row *models.KnowledgeRetrieveLog, hits []models.KnowledgeRetrieveHit) []callbacks.RetrieverTraceItem {
	ret := make([]callbacks.RetrieverTraceItem, 0, len(hits))
	contextRank := 0
	for _, hit := range hits {
		currentContextRank := 0
		discardReason := "context_limit_or_duplicate"
		if hit.UsedInAnswer {
			contextRank++
			currentContextRank = contextRank
			discardReason = ""
		}
		ret = append(ret, callbacks.RetrieverTraceItem{
			Query: row.Question, KnowledgeBaseID: hit.KnowledgeBaseID,
			DocumentTitle: hit.DocumentTitle, SourceRecordID: hit.SourceRecordID,
			RawRankNo: hit.RankNo, ContextRankNo: currentContextRank, UsedInContext: hit.UsedInAnswer,
			DiscardReason: discardReason, Score: hit.Score, LatencyMs: row.RetrieveMs,
		})
	}
	return ret
}

func completeKnowledgeCheckpoint(
	ctx context.Context,
	db *gorm.DB,
	logID int64,
	leaseOwner string,
	plan KnowledgeQueryPlanV2,
	options retrievers.KnowledgeRetrieveOptions,
	result *retrievers.KnowledgeRetrieveResult,
	status string,
) error {
	hits := knowledgeSearchResultsForLog(result.Hits)
	usedHits := knowledgeSearchResultsForLog(result.ContextResults)
	answerStatus := int(enums.KnowledgeAnswerStatusNormal)
	if status == "no_hit" {
		answerStatus = int(enums.KnowledgeAnswerStatusNoAnswer)
	}
	knowledgeBaseID := int64(0)
	if len(result.KnowledgeBaseIDs) > 0 {
		knowledgeBaseID = result.KnowledgeBaseIDs[0]
	} else if len(result.Hits) > 0 {
		knowledgeBaseID = result.Hits[0].KnowledgeBaseID
	}
	scope := usagex.ScopeFromContext(ctx)
	requestID := strings.TrimSpace(tracex.RequestIDFromContext(ctx))
	if requestID == "" {
		requestID = strings.TrimSpace(scope.RequestID)
	}
	req := &rag.CreateRetrieveLogRequest{
		TenantID:            plan.TenantID,
		KnowledgeBaseID:     knowledgeBaseID,
		TurnID:              plan.TurnID,
		TurnVersion:         plan.TurnVersion,
		TaskID:              plan.TaskID,
		TaskKey:             plan.TaskKey,
		QueryFingerprint:    plan.QueryFingerprint,
		QueryKey:            plan.QueryKey,
		QueryPurpose:        plan.Purpose,
		ScopeFingerprint:    plan.ScopeFingerprint,
		EvidenceFingerprint: knowledgeEvidenceFingerprint(result.ContextResults),
		Channel:             string(enums.KnowledgeRetrieveChannelIM),
		Scene:               string(enums.KnowledgeRetrieveSceneFirstResponse),
		ConversationID:      plan.ConversationID,
		MessageID:           scope.MessageID,
		RequestID:           requestID,
		Question:            plan.Query,
		AnswerStatus:        answerStatus,
		Hits:                hits,
		UsedHits:            usedHits,
		UsedHitRankNos:      usedKnowledgeHitRanks(result.Hits, result.ContextResults),
		RetrieveMs:          result.RetrieveMs,
		LatencyMs:           result.RetrieveMs,
		ModelName:           "runtime-retriever",
	}
	_, err := rag.RetrieveLog.CompleteRetrieveLogCheckpointDB(db, logID, plan.TenantID, leaseOwner, status, req)
	return err
}

func knowledgeSearchResultsForLog(items []rag.RetrieveResult) []response.KnowledgeSearchResult {
	ret := make([]response.KnowledgeSearchResult, 0, len(items))
	for _, item := range items {
		ret = append(ret, response.KnowledgeSearchResult{
			KnowledgeBaseID: item.KnowledgeBaseID,
			DocumentTitle:   item.DocumentTitle,
			Title:           item.Title,
			SectionPath:     item.SectionPath,
			SourceRecordID:  item.SourceRecordID,
			Content:         item.Content,
			Score:           float64(item.Score),
		})
	}
	return ret
}

func usedKnowledgeHitRanks(hits, used []rag.RetrieveResult) []int {
	usedKeys := make(map[string]struct{}, len(used))
	for _, item := range used {
		usedKeys[knowledgeRetrieveResultKey(item)] = struct{}{}
	}
	ranks := make([]int, 0, len(usedKeys))
	for index, item := range hits {
		if _, ok := usedKeys[knowledgeRetrieveResultKey(item)]; ok {
			ranks = append(ranks, index+1)
		}
	}
	return ranks
}

func knowledgeRetrieveResultKey(item rag.RetrieveResult) string {
	return strings.Join([]string{
		strconv.FormatInt(item.KnowledgeBaseID, 10),
		strings.TrimSpace(item.SourceRecordID),
		strings.TrimSpace(item.DocumentTitle),
		strings.TrimSpace(item.Title),
		strings.TrimSpace(item.Content),
	}, "\x1f")
}

func knowledgeEvidenceFingerprint(items []rag.RetrieveResult) string {
	keys := make([]string, 0, len(items))
	for _, item := range items {
		keys = append(keys, knowledgeRetrieveResultKey(item))
	}
	sort.Strings(keys)
	return sha256hex(strings.Join(keys, "\x1e"))
}

func acquireKnowledgeSemaphore(ctx context.Context, semaphore chan struct{}) error {
	if semaphore == nil {
		return nil
	}
	select {
	case semaphore <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func renewKnowledgeCheckpointLease(db *gorm.DB, id, tenantID int64, owner string) error {
	if db == nil || id <= 0 || tenantID <= 0 || strings.TrimSpace(owner) == "" {
		return fmt.Errorf("knowledge checkpoint lease is invalid")
	}
	expiresAt := time.Now().Add(knowledgeCheckpointLeaseDuration)
	result := db.Model(&models.KnowledgeRetrieveLog{}).
		Where("id = ? AND tenant_id = ? AND execution_status = ? AND lease_owner = ?", id, tenantID, "running", owner).
		Update("lease_expires_at", expiresAt)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return fmt.Errorf("knowledge checkpoint lease was lost")
	}
	return nil
}

func finishKnowledgeCheckpoint(db *gorm.DB, id, tenantID int64, status, owner string) error {
	if db == nil || id <= 0 || tenantID <= 0 {
		return fmt.Errorf("knowledge checkpoint terminal update is invalid")
	}
	now := time.Now()
	result := db.Model(&models.KnowledgeRetrieveLog{}).
		Where("id = ? AND tenant_id = ? AND execution_status = ? AND lease_owner = ?", id, tenantID, "running", owner).
		Updates(map[string]any{
			"execution_status": status,
			"lease_owner":      "",
			"lease_expires_at": nil,
			"completed_at":     now,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return fmt.Errorf("knowledge checkpoint lease was lost before terminal update")
	}
	return nil
}

func finishKnowledgeCheckpointAfterError(db *gorm.DB, id, tenantID int64, owner string, cause error) error {
	if cause == nil {
		cause = errKnowledgeCheckpointFailed
	}
	if terminalErr := finishKnowledgeCheckpoint(db, id, tenantID, "failed", owner); terminalErr != nil {
		return errors.Join(cause, terminalErr)
	}
	return cause
}

func hitKnowledgeBaseIDs(hits []rag.RetrieveResult) []int64 {
	seen := map[int64]struct{}{}
	ids := make([]int64, 0, 2)
	for _, hit := range hits {
		if hit.KnowledgeBaseID <= 0 {
			continue
		}
		if _, ok := seen[hit.KnowledgeBaseID]; ok {
			continue
		}
		seen[hit.KnowledgeBaseID] = struct{}{}
		ids = append(ids, hit.KnowledgeBaseID)
	}
	return ids
}

func repositoriesHitsForLog(db *gorm.DB, logID int64) []rag.RetrieveResult {
	var hits []models.KnowledgeRetrieveHit
	db.Where("retrieve_log_id = ?", logID).Order("rank_no ASC").Find(&hits)
	results := make([]rag.RetrieveResult, 0, len(hits))
	for _, hit := range hits {
		results = append(results, rag.RetrieveResult{
			KnowledgeBaseID: hit.KnowledgeBaseID,
			SourceRecordID:  hit.SourceRecordID,
			DocumentTitle:   hit.DocumentTitle,
			Title:           hit.Title,
			SectionPath:     hit.SectionPath,
			Content:         hit.Snippet,
			Score:           float32(hit.Score),
		})
	}
	return results
}

func hitsContextText(hits []rag.RetrieveResult) string {
	return strings.TrimSpace(rag.Retrieve.BuildContext(context.Background(), hits, 1<<30))
}

func topScore(hits []rag.RetrieveResult) float64 {
	if len(hits) == 0 {
		return 0
	}
	return float64(hits[0].Score)
}
