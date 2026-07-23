package builders

import (
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/dto/response"
	"agent-desk/internal/pkg/enums"
	"encoding/json"
	"strings"
)

func BuildKnowledgeBase(item *models.KnowledgeBase) response.KnowledgeBaseResponse {
	resourceAllowedHosts := make([]string, 0)
	if item.KnowledgeType == string(enums.KnowledgeBaseTypeFastGPTCloud) {
		config := struct {
			ResourceAllowedHosts []string `json:"resourceAllowedHosts"`
		}{}
		if json.Unmarshal([]byte(item.Remark), &config) == nil {
			seen := map[string]bool{}
			for _, host := range config.ResourceAllowedHosts {
				host = strings.ToLower(strings.TrimSpace(host))
				if host != "" && !seen[host] {
					seen[host] = true
					resourceAllowedHosts = append(resourceAllowedHosts, host)
				}
			}
		}
	}
	return response.KnowledgeBaseResponse{
		ID:                     item.ID,
		StoreID:                item.StoreID,
		DatasetID:              item.DatasetID,
		DatasetName:            item.DatasetName,
		ConnectionID:           item.ConnectionID,
		RetrievalMode:          item.RetrievalMode,
		FastGPTProfileName:     item.FastGPTProfileName,
		FastGPTProfileRevision: item.FastGPTProfileRevision,
		FastGPTProfileStatus:   item.FastGPTProfileStatus,
		Name:                   item.Name,
		Description:            item.Description,
		Status:                 item.Status,
		StatusName:             enums.GetStatusLabel(item.Status),
		DefaultTopK:            item.DefaultTopK,
		DefaultScoreThreshold:  item.DefaultScoreThreshold,
		DefaultRerankLimit:     item.DefaultRerankLimit,
		AnswerMode:             item.AnswerMode,
		AnswerModeName:         enums.GetKnowledgeAnswerModeLabel(enums.KnowledgeAnswerMode(item.AnswerMode)),
		ResourceAllowedHosts:   resourceAllowedHosts,
		CreatedAt:              item.CreatedAt,
		UpdatedAt:              item.UpdatedAt,
		CreateUserName:         item.CreateUserName,
		UpdateUserName:         item.UpdateUserName,
	}
}

func BuildKnowledgeResourceGroup(item *models.KnowledgeResourceGroup, resourceItems []models.KnowledgeResourceItem) response.KnowledgeResourceGroupResponse {
	ret := response.KnowledgeResourceGroupResponse{}
	if item == nil {
		return ret
	}
	ret = response.KnowledgeResourceGroupResponse{
		ID:              item.ID,
		StoreID:         item.StoreID,
		KnowledgeBaseID: item.KnowledgeBaseID,
		SourceProvider:  item.SourceProvider,
		SourceRecordID:  item.SourceRecordID,
		Title:           item.Title,
		Description:     item.Description,
		Status:          item.Status,
		StatusName:      enums.GetStatusLabel(item.Status),
		CreatedAt:       item.CreatedAt,
		UpdatedAt:       item.UpdatedAt,
		CreateUserName:  item.CreateUserName,
		UpdateUserName:  item.UpdateUserName,
		Items:           make([]response.KnowledgeResourceItemResponse, 0, len(resourceItems)),
	}
	for _, resourceItem := range resourceItems {
		ret.Items = append(ret.Items, response.KnowledgeResourceItemResponse{
			ID:                       resourceItem.ID,
			KnowledgeResourceGroupID: resourceItem.KnowledgeResourceGroupID,
			AssetID:                  resourceItem.AssetID,
			Title:                    resourceItem.Title,
			Description:              resourceItem.Description,
			SortNo:                   resourceItem.SortNo,
			Status:                   resourceItem.Status,
			StatusName:               enums.GetStatusLabel(resourceItem.Status),
		})
	}
	return ret
}

func BuildKnowledgeCandidate(item *models.KnowledgeCandidate) response.KnowledgeCandidateResponse {
	if item == nil {
		return response.KnowledgeCandidateResponse{}
	}
	return response.KnowledgeCandidateResponse{
		ID:              item.ID,
		StoreID:         item.StoreID,
		KnowledgeBaseID: item.KnowledgeBaseID,
		ConversationID:  item.ConversationID,
		MessageIDs:      item.MessageIDs,
		Source:          string(item.Source),
		SourceName:      enums.GetKnowledgeCandidateSourceLabel(item.Source),
		Question:        item.Question,
		Answer:          item.Answer,
		Summary:         item.Summary,
		EvidenceText:    item.EvidenceText,
		Frequency:       item.Frequency,
		SimilarityKey:   item.SimilarityKey,
		Status:          string(item.Status),
		StatusName:      enums.GetKnowledgeCandidateStatusLabel(item.Status),
		Confidence:      item.Confidence,
		CreatedBy:       item.CreatedBy,
		ReviewUserID:    item.ReviewUserID,
		ReviewUserName:  item.ReviewUserName,
		ReviewedAt:      item.ReviewedAt,
		ExportedAt:      item.ExportedAt,
		ImportedAt:      item.ImportedAt,
		CreatedAt:       item.CreatedAt,
		UpdatedAt:       item.UpdatedAt,
	}
}

func BuildKnowledgeCandidateList(list []models.KnowledgeCandidate) []response.KnowledgeCandidateResponse {
	results := make([]response.KnowledgeCandidateResponse, 0, len(list))
	for i := range list {
		results = append(results, BuildKnowledgeCandidate(&list[i]))
	}
	return results
}

func BuildKnowledgeRetrieveLog(item *models.KnowledgeRetrieveLog) response.KnowledgeRetrieveLogResponse {
	return response.KnowledgeRetrieveLogResponse{
		ID:               item.ID,
		KnowledgeBaseID:  item.KnowledgeBaseID,
		SourceType:       item.SourceType,
		SourceTypeName:   knowledgeRetrieveSourceTypeName(item.SourceType),
		Channel:          item.Channel,
		ChannelName:      enums.GetKnowledgeRetrieveChannelLabel(enums.KnowledgeRetrieveChannel(item.Channel)),
		Scene:            item.Scene,
		SceneName:        enums.GetKnowledgeRetrieveSceneLabel(enums.KnowledgeRetrieveScene(item.Scene)),
		SessionID:        item.SessionID,
		ConversationID:   item.ConversationID,
		RequestID:        item.RequestID,
		Question:         item.Question,
		RewriteQuestion:  item.RewriteQuestion,
		Answer:           item.Answer,
		AnswerStatus:     item.AnswerStatus,
		AnswerStatusName: enums.GetKnowledgeAnswerStatusLabel(enums.KnowledgeAnswerStatus(item.AnswerStatus)),
		HitCount:         item.HitCount,
		TopScore:         item.TopScore,
		ChunkProvider:    item.ChunkProvider,
		RerankEnabled:    item.RerankEnabled,
		RerankLimit:      item.RerankLimit,
		CitationCount:    item.CitationCount,
		UsedChunkCount:   item.UsedChunkCount,
		LatencyMs:        item.LatencyMs,
		RetrieveMs:       item.RetrieveMs,
		GenerateMs:       item.GenerateMs,
		PromptTokens:     item.PromptTokens,
		CompletionTokens: item.CompletionTokens,
		ModelName:        item.ModelName,
		TraceData:        item.TraceData,
		CreatedAt:        item.CreatedAt,
	}
}

func knowledgeRetrieveSourceTypeName(sourceType string) string {
	switch sourceType {
	case "fastgpt", "cloud_knowledge":
		return "托管 FastGPT"
	default:
		return "历史检索记录"
	}
}

func BuildKnowledgeRetrieveHitResponse(item *models.KnowledgeRetrieveHit) response.KnowledgeRetrieveHitResponse {
	return response.KnowledgeRetrieveHitResponse{
		ID:              item.ID,
		RetrieveLogID:   item.RetrieveLogID,
		KnowledgeBaseID: item.KnowledgeBaseID,
		ChunkID:         item.ChunkID,
		DocumentID:      item.DocumentID,
		DocumentTitle:   item.DocumentTitle,
		FaqID:           item.FaqID,
		FaqQuestion:     item.FaqQuestion,
		ChunkNo:         item.ChunkNo,
		Title:           item.Title,
		SectionPath:     item.SectionPath,
		ChunkType:       item.ChunkType,
		ChunkTypeName:   historicalKnowledgeChunkTypeName(item.ChunkType),
		Provider:        item.Provider,
		RankNo:          item.RankNo,
		Score:           item.Score,
		RerankScore:     item.RerankScore,
		UsedInAnswer:    item.UsedInAnswer,
		IsCitation:      item.IsCitation,
		Snippet:         item.Snippet,
		CreatedAt:       item.CreatedAt,
	}
}

func historicalKnowledgeChunkTypeName(chunkType string) string {
	switch strings.TrimSpace(chunkType) {
	case "text":
		return "文本"
	case "faq":
		return "历史问答"
	case "table":
		return "表格"
	case "code":
		return "代码"
	default:
		return ""
	}
}
