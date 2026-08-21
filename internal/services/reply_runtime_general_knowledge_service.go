package services

import (
	"encoding/json"
	"strconv"
	"strings"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
)

const replyRuntimeGeneralKnowledgeBaseByStoreConfigKey = "reply_runtime.general_knowledge_base_by_store"

var ReplyRuntimeGeneralKnowledgeService = newReplyRuntimeGeneralKnowledgeService()

func newReplyRuntimeGeneralKnowledgeService() *replyRuntimeGeneralKnowledgeService {
	return &replyRuntimeGeneralKnowledgeService{}
}

type replyRuntimeGeneralKnowledgeService struct{}

// ResolveKnowledgeBaseIDs appends the configured general knowledge base after
// the agent's store-specific knowledge bases. Invalid mappings fail closed and
// preserve the existing single-layer behavior.
func (s *replyRuntimeGeneralKnowledgeService) ResolveKnowledgeBaseIDs(knowledgeBaseIDs []int64) []int64 {
	normalized := normalizeReplyRuntimeKnowledgeBaseIDs(knowledgeBaseIDs)
	if len(normalized) == 0 || sqls.DB() == nil {
		return normalized
	}

	knowledgeBases := repositories.KnowledgeBaseRepository.Find(sqls.DB(), sqls.NewCnd().In("id", normalized))
	byID := make(map[int64]models.KnowledgeBase, len(knowledgeBases))
	for _, item := range knowledgeBases {
		byID[item.ID] = item
	}

	storeID := int64(0)
	for _, knowledgeBaseID := range normalized {
		item, ok := byID[knowledgeBaseID]
		if !ok || item.Status != enums.StatusOk || item.StoreID <= 0 {
			continue
		}
		storeID = item.StoreID
		break
	}
	if storeID <= 0 {
		return normalized
	}

	config := repositories.SystemConfigRepository.Take(sqls.DB(), "config_key = ? AND status = ?", replyRuntimeGeneralKnowledgeBaseByStoreConfigKey, enums.StatusOk)
	if config == nil {
		return normalized
	}
	generalKnowledgeBaseID := parseGeneralKnowledgeBaseID(config.ConfigValue, storeID)
	if generalKnowledgeBaseID <= 0 || containsReplyRuntimeKnowledgeBaseID(normalized, generalKnowledgeBaseID) {
		return normalized
	}

	generalKnowledgeBase := repositories.KnowledgeBaseRepository.Get(sqls.DB(), generalKnowledgeBaseID)
	if !isValidReplyRuntimeGeneralKnowledgeBase(generalKnowledgeBase, storeID) {
		return normalized
	}
	return append(normalized, generalKnowledgeBaseID)
}

func normalizeReplyRuntimeKnowledgeBaseIDs(knowledgeBaseIDs []int64) []int64 {
	ret := make([]int64, 0, len(knowledgeBaseIDs))
	seen := make(map[int64]struct{}, len(knowledgeBaseIDs))
	for _, knowledgeBaseID := range knowledgeBaseIDs {
		if knowledgeBaseID <= 0 {
			continue
		}
		if _, ok := seen[knowledgeBaseID]; ok {
			continue
		}
		seen[knowledgeBaseID] = struct{}{}
		ret = append(ret, knowledgeBaseID)
	}
	return ret
}

func parseGeneralKnowledgeBaseID(configValue string, storeID int64) int64 {
	if storeID <= 0 || strings.TrimSpace(configValue) == "" {
		return 0
	}
	values := map[string]json.RawMessage{}
	if err := json.Unmarshal([]byte(configValue), &values); err != nil {
		return 0
	}
	raw, ok := values[strconv.FormatInt(storeID, 10)]
	if !ok {
		return 0
	}
	text := strings.TrimSpace(string(raw))
	if len(text) >= 2 && text[0] == '"' {
		var decoded string
		if err := json.Unmarshal(raw, &decoded); err != nil {
			return 0
		}
		text = strings.TrimSpace(decoded)
	}
	value, err := strconv.ParseInt(text, 10, 64)
	if err != nil || value <= 0 {
		return 0
	}
	return value
}

func containsReplyRuntimeKnowledgeBaseID(knowledgeBaseIDs []int64, target int64) bool {
	for _, knowledgeBaseID := range knowledgeBaseIDs {
		if knowledgeBaseID == target {
			return true
		}
	}
	return false
}

func isValidReplyRuntimeGeneralKnowledgeBase(knowledgeBase *models.KnowledgeBase, storeID int64) bool {
	if knowledgeBase == nil || knowledgeBase.Status != enums.StatusOk || knowledgeBase.StoreID != storeID {
		return false
	}
	isFastGPT := knowledgeBase.KnowledgeType == string(enums.KnowledgeBaseTypeFastGPTCloud) ||
		knowledgeBase.ChunkProvider == string(enums.KnowledgeChunkProviderFastGPT)
	return isFastGPT && strings.TrimSpace(knowledgeBase.DatasetID) != ""
}
