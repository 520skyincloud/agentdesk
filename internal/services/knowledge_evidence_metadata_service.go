package services

import (
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"strings"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
)

var KnowledgeEvidenceMetadataService = newKnowledgeEvidenceMetadataService()

type knowledgeEvidenceMetadataService struct{}

func newKnowledgeEvidenceMetadataService() *knowledgeEvidenceMetadataService {
	return &knowledgeEvidenceMetadataService{}
}

// MetadataCandidate 是一次回填/判定的输入：检索命中的问题与答案正文。
type MetadataCandidate struct {
	KnowledgeBaseID int64
	SourceRecordID  string
	Question        string
	Answer          string
}

// Classify 按确定性规则判定一条知识的元数据（文档 8.2）：
// 出题式/摘要式元内容 -> derived_qa + meta + weak + pending（不得直接面对客户）；
// 其余无法确认来源的 -> unknown + fact + supported + pending（普通事实可继续作答）。
func (s *knowledgeEvidenceMetadataService) Classify(candidate MetadataCandidate) models.KnowledgeEvidenceMetadata {
	meta := DetectKnowledgeMetaContent(candidate.Question, candidate.Answer)
	sourceClass := "unknown"
	claimType := "fact"
	trustLevel := "supported"
	if meta {
		sourceClass = "derived_qa"
		claimType = "meta"
		trustLevel = "weak"
	}
	return models.KnowledgeEvidenceMetadata{
		SourceClass:     sourceClass,
		FactScope:       "store",
		ClaimType:       claimType,
		TrustLevel:      trustLevel,
		Freshness:       "unknown",
		ResourcePurpose: "unknown",
		ReviewStatus:    "pending",
		SourceDigest:    s.SourceDigest(candidate.Question, candidate.Answer),
	}
}

// SourceDigest 是知识正文指纹；内容变化时 UpsertBatch 会递增 revision。
func (s *knowledgeEvidenceMetadataService) SourceDigest(question, answer string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(question) + "\x1f" + strings.TrimSpace(answer)))
	return hex.EncodeToString(sum[:16])
}

// Backfill 幂等回填一批候选的元数据。
func (s *knowledgeEvidenceMetadataService) Backfill(tenantID, storeID int64, candidates []MetadataCandidate) (int, error) {
	if tenantID <= 0 || storeID <= 0 || len(candidates) == 0 {
		return 0, nil
	}
	now := time.Now()
	items := make([]models.KnowledgeEvidenceMetadata, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.KnowledgeBaseID <= 0 || strings.TrimSpace(candidate.SourceRecordID) == "" {
			continue
		}
		item := s.Classify(candidate)
		item.TenantID = tenantID
		item.StoreID = storeID
		item.KnowledgeBaseID = candidate.KnowledgeBaseID
		item.SourceRecordID = strings.TrimSpace(candidate.SourceRecordID)
		item.CreatedAt = now
		item.UpdatedAt = now
		items = append(items, item)
	}
	if len(items) == 0 {
		return 0, nil
	}
	if err := repositories.KnowledgeEvidenceMetadataRepository.UpsertBatch(sqls.DB(), items); err != nil {
		return 0, err
	}
	return len(items), nil
}

// JudgeBySourceRecords 供 Evidence Judge 运行时 join：批量取元数据并按 SourceRecordID 索引。
func (s *knowledgeEvidenceMetadataService) JudgeBySourceRecords(tenantID, storeID, knowledgeBaseID int64, sourceRecordIDs []string) map[string]models.KnowledgeEvidenceMetadata {
	list := repositories.KnowledgeEvidenceMetadataRepository.FindBySourceRecords(sqls.DB(), tenantID, storeID, knowledgeBaseID, sourceRecordIDs)
	ret := make(map[string]models.KnowledgeEvidenceMetadata, len(list))
	for _, item := range list {
		ret[item.SourceRecordID] = item
	}
	return ret
}

// DetectKnowledgeMetaContent 确定性识别「派生元问题」内容：
// 出题式（用户可能通过哪些方式问/首先介绍了哪个/分为哪几类）、
// 审题式（这个表格是否包含/缺少了什么关键信息/如果填充真实数据）等。
// 这类内容是对原始资料的二次生成，不能作为客户可见答案或推荐依据。
func DetectKnowledgeMetaContent(question, answer string) bool {
	text := strings.TrimSpace(question)
	if text == "" {
		text = strings.TrimSpace(answer)
	}
	patterns := []string{
		"用户可能通过", "用户可能怎么问", "用户会如何", "可能通过哪些不同的方式",
		"首先介绍了哪", "分为哪两个类别", "分为哪几类", "分为以下几个类别",
		"这个表格是否包含", "表格中是否包含", "缺少了什么关键信息", "缺少哪些关键信息",
		"如果填充真实数据", "填充真实对话", "隐私方面，如果", "如果这是对话记录",
		"原文提到了哪", "文中提到哪", "上文中哪", "材料中哪",
	}
	for _, pattern := range patterns {
		if strings.Contains(text, pattern) {
			return true
		}
	}
	return false
}

// BackfillFromRetrieveHits 契约 17.3.1：从历史检索命中的 sourceRecord 快照
// 离线回填质量元数据（在线零模型调用）。按 (tenant,kb,sourceRecordID) 去重，
// 只回填尚无元数据行的记录；幂等，可周期执行。
func (s *knowledgeEvidenceMetadataService) BackfillFromRetrieveHits(limit int) (int, error) {
	if limit <= 0 {
		limit = 200
	}
	rows := make([]knowledgeMetadataHitRow, 0, limit)
	err := sqls.DB().Raw(`
		SELECT h.tenant_id, h.knowledge_base_id, h.source_record_id,
		       MAX(h.title) AS title, MAX(h.snippet) AS snippet
		FROM t_knowledge_retrieve_hit h
		JOIN t_knowledge_base kb ON kb.id = h.knowledge_base_id AND kb.tenant_id = h.tenant_id
		WHERE h.source_record_id <> '' AND kb.store_id > 0
		GROUP BY h.tenant_id, h.knowledge_base_id, h.source_record_id
		ORDER BY MAX(h.id) DESC
		LIMIT ?`, limit).Scan(&rows).Error
	if err != nil {
		return 0, err
	}
	if len(rows) == 0 {
		return 0, nil
	}
	// 按 (tenant,kb) 分组批量回填；storeID 从知识库行解析。
	type kbKey struct{ tenant, kb int64 }
	storeByKB := map[kbKey]int64{}
	kbs := repositories.KnowledgeBaseRepository.Find(sqls.DB(), sqls.NewCnd().
		In("tenant_id", distinctTenants(rows)))
	for _, kb := range kbs {
		storeByKB[kbKey{kb.TenantID, kb.ID}] = kb.StoreID
	}
	grouped := map[kbKey][]MetadataCandidate{}
	for _, row := range rows {
		key := kbKey{row.TenantID, row.KnowledgeBaseID}
		grouped[key] = append(grouped[key], MetadataCandidate{
			KnowledgeBaseID: row.KnowledgeBaseID,
			SourceRecordID:  row.SourceRecordID,
			Question:        row.Title,
			Answer:          row.Snippet,
		})
	}
	total := 0
	for key, candidates := range grouped {
		storeID := storeByKB[key]
		if storeID <= 0 {
			continue
		}
		count, err := s.Backfill(key.tenant, storeID, candidates)
		if err != nil {
			slog.Warn("knowledge metadata backfill failed", "tenant_id", key.tenant, "knowledge_base_id", key.kb, "error", err)
			continue
		}
		total += count
	}
	return total, nil
}

type knowledgeMetadataHitRow struct {
	TenantID        int64
	KnowledgeBaseID int64
	SourceRecordID  string
	Title           string
	Snippet         string
}

func distinctTenants(rows []knowledgeMetadataHitRow) []int64 {
	seen := map[int64]struct{}{}
	ret := make([]int64, 0, 4)
	for _, row := range rows {
		if _, ok := seen[row.TenantID]; ok {
			continue
		}
		seen[row.TenantID] = struct{}{}
		ret = append(ret, row.TenantID)
	}
	return ret
}
