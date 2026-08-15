package executor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"agent-desk/internal/ai/rag"
	"agent-desk/internal/ai/runtime/internal/impl/retrievers"
	"agent-desk/internal/models"
	"gorm.io/gorm"
)

// 契约 4.18/22.12：所有知识请求先形成 QueryPlan，再由同一个有界执行器执行。
// 唯一键 (tenant, scope_fingerprint, query_fingerprint)；同键 checkpoint 已
// succeeded 时直接复用，不再请求 FastGPT——条件探测与正式回答共享 checkpoint。

// KnowledgeRetrieverIFace 是执行器依赖的最小检索接口。
type KnowledgeRetrieverIFace interface {
	RetrieveContextByOptions(context.Context, retrievers.KnowledgeRetrieveOptions, string) (*retrievers.KnowledgeRetrieveResult, error)
}

// KnowledgeQueryPlanV2 是一次知识检索的完整计划。
type KnowledgeQueryPlanV2 struct {
	TenantID         int64
	ScopeFingerprint string
	Query            string
	QueryFingerprint string
	QueryKey         string
	Purpose          string // answer | conditional_probe
	TurnID           int64
	TaskID           int64
	TaskKey          string
}

// BuildKnowledgeQueryPlan 构造计划（scope = tenant+store+conversation+session）。
func BuildKnowledgeQueryPlan(tenantID, storeID, conversationID int64, sessionNo int, query, purpose string, turnID, taskID int64, taskKey string) KnowledgeQueryPlanV2 {
	scope := fmt.Sprintf("%d/%d/%d/%d", tenantID, storeID, conversationID, sessionNo)
	queryFP := sha256hex(query)
	return KnowledgeQueryPlanV2{
		TenantID: tenantID, ScopeFingerprint: sha256hex(scope),
		Query: query, QueryFingerprint: queryFP,
		QueryKey: "kq_" + queryFP[:24], Purpose: purpose,
		TurnID: turnID, TaskID: taskID, TaskKey: taskKey,
	}
}

func sha256hex(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

// ExecuteKnowledgeQuery 统一执行器：先查 checkpoint，命中 succeeded 直接复用；
// 未命中则写入 pending/running 租约行后执行检索，成功后 CAS 终态。
// maxInFlight 为同批并发上限（契约 22.12：4）。
func ExecuteKnowledgeQuery(
	ctx context.Context,
	db *gorm.DB,
	plan KnowledgeQueryPlanV2,
	retriever KnowledgeRetrieverIFace,
	options retrievers.KnowledgeRetrieveOptions,
	semaphore chan struct{},
) (*retrievers.KnowledgeRetrieveResult, error) {
	checkpointEnabled := db != nil
	// 1. checkpoint 复用：同 scope+query 已成功 → 不再请求 FastGPT。
	if checkpointEnabled {
		if cached := findSucceededCheckpoint(db, plan); cached != nil {
			return cached, nil
		}
	}
	// 2. 并发上限。
	if semaphore != nil {
		select {
		case semaphore <- struct{}{}:
			defer func() { <-semaphore }()
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	// 3. 租约行（唯一键防重复请求）。
	now := time.Now()
	leaseOwner := fmt.Sprintf("kx_%d", now.UnixNano())
	checkpoint := &models.KnowledgeRetrieveLog{
		TenantID: plan.TenantID, ScopeFingerprint: plan.ScopeFingerprint,
		QueryFingerprint: plan.QueryFingerprint, QueryKey: plan.QueryKey,
		QueryPurpose: plan.Purpose, ExecutionStatus: "running",
		LeaseOwner: leaseOwner, LeaseExpiresAt: &now,
		TurnID: plan.TurnID, TaskID: plan.TaskID, TaskKey: plan.TaskKey,
		Question: plan.Query, SourceType: "fastgpt", Channel: "im",
	}
	if checkpointEnabled {
		if err := claimCheckpoint(db, checkpoint); err != nil {
			// 租约冲突视为可复用终态未就绪：降级直查，不阻断回复链路。
			checkpoint.ID = 0
		}
	}
	// 4. 执行检索（含 retriever 内部网络重试预算）。
	result, err := retriever.RetrieveContextByOptions(ctx, options, plan.Query)
	if err != nil {
		if checkpoint.ID > 0 {
			finishCheckpoint(db, checkpoint.ID, plan.TenantID, "failed", leaseOwner)
		}
		return nil, err
	}
	if checkpoint.ID > 0 {
		finishCheckpoint(db, checkpoint.ID, plan.TenantID, terminalFor(result), leaseOwner)
	}
	return result, nil
}

func terminalFor(result *retrievers.KnowledgeRetrieveResult) string {
	if result == nil || len(result.Hits) == 0 {
		return "no_hit"
	}
	return "succeeded"
}

func findSucceededCheckpoint(db *gorm.DB, plan KnowledgeQueryPlanV2) *retrievers.KnowledgeRetrieveResult {
	row := &models.KnowledgeRetrieveLog{}
	err := db.Where("tenant_id = ? AND scope_fingerprint = ? AND query_fingerprint = ? AND execution_status = ?",
		plan.TenantID, plan.ScopeFingerprint, plan.QueryFingerprint, "succeeded").
		Order("id DESC").First(row).Error
	if err != nil || row.ID <= 0 {
		return nil
	}
	// 复用命中详情（hit 快照足够支撑后续证据选择；正文命中在 t_knowledge_retrieve_hit）。
	hits := repositoriesHitsForLog(db, row.ID)
	return &retrievers.KnowledgeRetrieveResult{
		KnowledgeBaseIDs: hitKnowledgeBaseIDs(hits),
		Query:            row.Question,
		Hits:             hits,
		ContextResults:   hits,
		ContextText:      hitsContextText(hits),
		TopScore:         topScore(hits),
	}
}

func claimCheckpoint(db *gorm.DB, checkpoint *models.KnowledgeRetrieveLog) error {
	// 幂等：唯一键冲突时读取既有终态。
	existing := &models.KnowledgeRetrieveLog{}
	err := db.Where("tenant_id = ? AND scope_fingerprint = ? AND query_fingerprint = ? AND execution_status IN ?",
		checkpoint.TenantID, checkpoint.ScopeFingerprint, checkpoint.QueryFingerprint,
		[]string{"running", "succeeded"}).First(existing).Error
	if err == nil && existing.ID > 0 {
		if existing.ExecutionStatus == "succeeded" {
			checkpoint.ID = existing.ID
			return nil
		}
		return fmt.Errorf("knowledge checkpoint leased by %s", existing.LeaseOwner)
	}
	return db.Create(checkpoint).Error
}

func finishCheckpoint(db *gorm.DB, id, tenantID int64, status, owner string) {
	if id <= 0 {
		return
	}
	now := time.Now()
	db.Model(&models.KnowledgeRetrieveLog{}).
		Where("id = ? AND tenant_id = ? AND lease_owner = ?", id, tenantID, owner).
		Updates(map[string]any{"execution_status": status, "lease_owner": "", "completed_at": now})
}

// 命中快照复用辅助（checkpoint 复用路径）。
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

func hitKnowledgeBaseIDs(hits []rag.RetrieveResult) []int64 {
	seen := map[int64]struct{}{}
	ids := make([]int64, 0, 2)
	for _, hit := range hits {
		if _, ok := seen[hit.KnowledgeBaseID]; ok {
			continue
		}
		seen[hit.KnowledgeBaseID] = struct{}{}
		ids = append(ids, hit.KnowledgeBaseID)
	}
	return ids
}

func hitsContextText(hits []rag.RetrieveResult) string {
	parts := make([]string, 0, len(hits))
	for _, hit := range hits {
		if strings.TrimSpace(hit.Content) != "" {
			parts = append(parts, hit.Content)
		}
	}
	return strings.Join(parts, "\n")
}

func topScore(hits []rag.RetrieveResult) float64 {
	if len(hits) == 0 {
		return 0
	}
	return float64(hits[0].Score)
}
