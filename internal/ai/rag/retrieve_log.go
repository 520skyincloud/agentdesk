package rag

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/dto"
	"agent-desk/internal/pkg/dto/response"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/repositories"

	"github.com/google/uuid"
	"github.com/mlogclub/simple/sqls"
)

var RetrieveLog = &retrieveLog{}

type retrieveLog struct {
}

type CreateRetrieveLogRequest struct {
	TenantID         int64
	KnowledgeBaseID  int64
	Channel          string
	Scene            string
	SessionID        string
	ConversationID   int64
	MessageID        int64
	RequestID        string
	Question         string
	RewriteQuestion  string
	Answer           string
	AnswerStatus     int
	RerankEnabled    bool
	RerankLimit      int
	Hits             []response.KnowledgeSearchResult
	UsedHits         []response.KnowledgeSearchResult
	UsedHitRankNos   []int
	Citations        []response.KnowledgeCitation
	LatencyMs        int64
	RetrieveMs       int64
	GenerateMs       int64
	PromptTokens     int
	CompletionTokens int
	ModelName        string
}

type retrieveTraceData struct {
	Retrieve  retrieveTraceRetrieve   `json:"retrieve"`
	Linkage   retrieveTraceLinkage    `json:"linkage"`
	Context   retrieveTraceContext    `json:"context"`
	Hits      []retrieveTraceHit      `json:"hits,omitempty"`
	Citations []retrieveTraceCitation `json:"citations"`
}

type retrieveTraceLinkage struct {
	ConversationID int64  `json:"conversationId,omitempty"`
	MessageID      int64  `json:"messageId,omitempty"`
	RequestID      string `json:"requestId,omitempty"`
}

type retrieveTraceHit struct {
	SourceRecordID string `json:"sourceRecordId,omitempty"`
	RawRankNo      int    `json:"rawRankNo"`
	ContextRankNo  int    `json:"contextRankNo,omitempty"`
	DiscardReason  string `json:"discardReason,omitempty"`
}

type retrieveTraceRetrieve struct {
	Provider        string `json:"provider"`
	RerankEnabled   bool   `json:"rerankEnabled"`
	RerankLimit     int    `json:"rerankLimit"`
	RawHitCount     int    `json:"rawHitCount"`
	ContextHitCount int    `json:"contextHitCount"`
	CitationCount   int    `json:"citationCount"`
}

type retrieveTraceContext struct {
	KnowledgeBaseIDs []int64  `json:"knowledgeBaseIds"`
	SourceRecordIDs  []string `json:"sourceRecordIds"`
}

type retrieveTraceCitation struct {
	SourceRecordID string `json:"sourceRecordId"`
}

func (s *retrieveLog) FindHitsByRetrieveLogID(retrieveLogID int64) []models.KnowledgeRetrieveHit {
	if retrieveLogID <= 0 {
		return nil
	}
	var list []models.KnowledgeRetrieveHit
	sqls.DB().Where("retrieve_log_id = ?", retrieveLogID).Order("rank_no asc, id asc").Find(&list)
	return list
}

func (s *retrieveLog) CreateRetrieveLog(req *CreateRetrieveLogRequest, operator *dto.AuthPrincipal) (*models.KnowledgeRetrieveLog, error) {
	if req == nil {
		return nil, fmt.Errorf("retrieve log request is nil")
	}
	tenantID, err := resolveRetrieveLogTenant(req, operator)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	topScore := 0.0
	if len(req.Hits) > 0 {
		topScore = req.Hits[0].Score
	}
	requestID := strings.TrimSpace(req.RequestID)
	if requestID == "" {
		requestID = uuid.New().String()
	}
	req.RequestID = requestID
	traceData := buildRetrieveTraceData(req)

	log := &models.KnowledgeRetrieveLog{
		TenantID:         tenantID,
		KnowledgeBaseID:  req.KnowledgeBaseID,
		SourceType:       "fastgpt",
		Channel:          req.Channel,
		Scene:            req.Scene,
		SessionID:        req.SessionID,
		ConversationID:   req.ConversationID,
		RequestID:        requestID,
		Question:         req.Question,
		RewriteQuestion:  req.RewriteQuestion,
		Answer:           req.Answer,
		AnswerStatus:     req.AnswerStatus,
		HitCount:         len(req.Hits),
		TopScore:         topScore,
		ChunkProvider:    string(enums.KnowledgeChunkProviderFastGPT),
		RerankEnabled:    req.RerankEnabled,
		RerankLimit:      req.RerankLimit,
		CitationCount:    len(req.Citations),
		UsedChunkCount:   len(req.UsedHits),
		LatencyMs:        req.LatencyMs,
		RetrieveMs:       req.RetrieveMs,
		GenerateMs:       req.GenerateMs,
		PromptTokens:     req.PromptTokens,
		CompletionTokens: req.CompletionTokens,
		ModelName:        req.ModelName,
		TraceData:        traceData,
		CreatedAt:        now,
	}

	usedHitKeys := make(map[string]struct{}, len(req.UsedHits))
	for _, item := range req.UsedHits {
		usedHitKeys[buildKnowledgeSearchResultKey(item)] = struct{}{}
	}
	usedHitRanks := make(map[int]struct{}, len(req.UsedHitRankNos))
	for _, rankNo := range req.UsedHitRankNos {
		if rankNo > 0 {
			usedHitRanks[rankNo] = struct{}{}
		}
	}
	citationKeys := make(map[string]struct{}, len(req.Citations))
	for _, item := range req.Citations {
		citationKeys[buildKnowledgeCitationKey(item)] = struct{}{}
	}

	if err := sqls.WithTransaction(func(ctx *sqls.TxContext) error {
		if err := ctx.Tx.Create(log).Error; err != nil {
			return err
		}
		for i, hit := range req.Hits {
			hitKey := buildKnowledgeSearchResultKey(hit)
			usedInAnswer := hasHitKey(usedHitKeys, hitKey)
			if len(usedHitRanks) > 0 {
				_, usedInAnswer = usedHitRanks[i+1]
			}
			hitRecord := &models.KnowledgeRetrieveHit{
				TenantID:        tenantID,
				RetrieveLogID:   log.ID,
				KnowledgeBaseID: hit.KnowledgeBaseID,
				ChunkID:         hit.ChunkID,
				DocumentID:      hit.DocumentID,
				DocumentTitle:   hit.DocumentTitle,
				ChunkNo:         hit.ChunkNo,
				Title:           hit.Title,
				SectionPath:     hit.SectionPath,
				ChunkType:       "",
				Provider:        string(enums.KnowledgeChunkProviderFastGPT),
				RankNo:          i + 1,
				Score:           hit.Score,
				RerankScore:     hit.RerankScore,
				UsedInAnswer:    usedInAnswer,
				IsCitation:      hasHitKey(citationKeys, hitKey),
				Snippet:         hit.Content,
				CreatedAt:       now,
			}
			if err := ctx.Tx.Create(hitRecord).Error; err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return nil, err
	}

	return log, nil
}

func resolveRetrieveLogTenant(req *CreateRetrieveLogRequest, operator *dto.AuthPrincipal) (int64, error) {
	tenantID := int64(0)
	merge := func(source string, sourceID, sourceTenantID int64) error {
		if sourceTenantID <= 0 {
			return fmt.Errorf("knowledge retrieve log %s %d has no tenant", source, sourceID)
		}
		if tenantID == 0 {
			tenantID = sourceTenantID
			return nil
		}
		if tenantID != sourceTenantID {
			return fmt.Errorf("knowledge retrieve log tenant %d conflicts with %s %d tenant %d", tenantID, source, sourceID, sourceTenantID)
		}
		return nil
	}
	if req.TenantID > 0 {
		tenantID = req.TenantID
	}
	if operator != nil && operator.ActiveTenantID > 0 {
		if err := merge("operator", operator.UserID, operator.ActiveTenantID); err != nil {
			return 0, err
		}
	}
	if req.ConversationID > 0 {
		conversation := repositories.ConversationRepository.Get(sqls.DB(), req.ConversationID)
		if conversation == nil {
			return 0, fmt.Errorf("knowledge retrieve log references missing conversation %d", req.ConversationID)
		}
		if err := merge("conversation", conversation.ID, conversation.TenantID); err != nil {
			return 0, err
		}
	}
	knowledgeBaseIDs := make([]int64, 0, len(req.Hits)+1)
	if req.KnowledgeBaseID > 0 {
		knowledgeBaseIDs = append(knowledgeBaseIDs, req.KnowledgeBaseID)
	}
	for _, hit := range req.Hits {
		if hit.KnowledgeBaseID > 0 {
			knowledgeBaseIDs = append(knowledgeBaseIDs, hit.KnowledgeBaseID)
		}
	}
	seen := make(map[int64]struct{}, len(knowledgeBaseIDs))
	for _, knowledgeBaseID := range knowledgeBaseIDs {
		if _, ok := seen[knowledgeBaseID]; ok {
			continue
		}
		seen[knowledgeBaseID] = struct{}{}
		knowledgeBase := repositories.KnowledgeBaseRepository.Get(sqls.DB(), knowledgeBaseID)
		if knowledgeBase == nil {
			return 0, fmt.Errorf("knowledge retrieve log references missing knowledge base %d", knowledgeBaseID)
		}
		if err := merge("knowledge base", knowledgeBase.ID, knowledgeBase.TenantID); err != nil {
			return 0, err
		}
	}
	if tenantID <= 0 {
		return 0, fmt.Errorf("knowledge retrieve log has no tenant evidence")
	}
	return tenantID, nil
}

func buildRetrieveTraceData(req *CreateRetrieveLogRequest) string {
	trace := retrieveTraceData{
		Retrieve: retrieveTraceRetrieve{
			Provider:        string(enums.KnowledgeChunkProviderFastGPT),
			RerankEnabled:   req.RerankEnabled,
			RerankLimit:     req.RerankLimit,
			RawHitCount:     len(req.Hits),
			ContextHitCount: len(req.UsedHits),
			CitationCount:   len(req.Citations),
		},
		Linkage: retrieveTraceLinkage{
			ConversationID: req.ConversationID,
			MessageID:      req.MessageID,
			RequestID:      strings.TrimSpace(req.RequestID),
		},
		Context: retrieveTraceContext{
			KnowledgeBaseIDs: distinctKnowledgeBaseIDs(req.UsedHits),
			SourceRecordIDs:  distinctSourceRecordIDs(req.UsedHits),
		},
		Citations: buildTraceCitations(req.Citations),
		Hits:      buildRetrieveTraceHits(req),
	}
	data, err := json.Marshal(trace)
	if err != nil {
		return ""
	}
	return string(data)
}

func buildRetrieveTraceHits(req *CreateRetrieveLogRequest) []retrieveTraceHit {
	if req == nil || len(req.Hits) == 0 {
		return nil
	}
	contextRanks := make(map[int]int, len(req.UsedHitRankNos))
	for contextIndex, rawRankNo := range req.UsedHitRankNos {
		if rawRankNo > 0 {
			contextRanks[rawRankNo] = contextIndex + 1
		}
	}
	result := make([]retrieveTraceHit, 0, len(req.Hits))
	for index := range req.Hits {
		rawRankNo := index + 1
		contextRankNo := contextRanks[rawRankNo]
		discardReason := ""
		if contextRankNo == 0 {
			discardReason = "context_limit_or_duplicate"
		}
		result = append(result, retrieveTraceHit{
			SourceRecordID: strings.TrimSpace(req.Hits[index].SourceRecordID),
			RawRankNo:      rawRankNo,
			ContextRankNo:  contextRankNo,
			DiscardReason:  discardReason,
		})
	}
	return result
}

func buildTraceCitations(citations []response.KnowledgeCitation) []retrieveTraceCitation {
	items := make([]retrieveTraceCitation, 0, len(citations))
	for _, item := range citations {
		items = append(items, retrieveTraceCitation{
			SourceRecordID: strings.TrimSpace(item.SourceRecordID),
		})
	}
	return items
}

func distinctKnowledgeBaseIDs(hits []response.KnowledgeSearchResult) []int64 {
	ids := make([]int64, 0)
	seen := make(map[int64]struct{})
	for _, item := range hits {
		if item.KnowledgeBaseID <= 0 {
			continue
		}
		if _, ok := seen[item.KnowledgeBaseID]; ok {
			continue
		}
		seen[item.KnowledgeBaseID] = struct{}{}
		ids = append(ids, item.KnowledgeBaseID)
	}
	return ids
}

func distinctSourceRecordIDs(hits []response.KnowledgeSearchResult) []string {
	seen := make(map[string]struct{})
	items := make([]string, 0)
	for _, item := range hits {
		sourceRecordID := strings.TrimSpace(item.SourceRecordID)
		if sourceRecordID == "" {
			continue
		}
		if _, ok := seen[sourceRecordID]; ok {
			continue
		}
		seen[sourceRecordID] = struct{}{}
		items = append(items, sourceRecordID)
	}
	return items
}

func buildKnowledgeSearchResultKey(item response.KnowledgeSearchResult) string {
	if sourceRecordID := strings.TrimSpace(item.SourceRecordID); sourceRecordID != "" {
		return "source:" + sourceRecordID
	}
	return fmt.Sprintf("%d|%s|%d", item.DocumentID, item.SectionPath, item.ChunkNo)
}

func buildKnowledgeCitationKey(item response.KnowledgeCitation) string {
	if sourceRecordID := strings.TrimSpace(item.SourceRecordID); sourceRecordID != "" {
		return "source:" + sourceRecordID
	}
	return fmt.Sprintf("%d|%s|%d", item.DocumentID, item.SectionPath, item.ChunkNo)
}

func hasHitKey(items map[string]struct{}, key string) bool {
	_, ok := items[key]
	return ok
}
