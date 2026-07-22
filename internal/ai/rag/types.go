package rag

import "encoding/json"

type RetrieveRequest struct {
	KnowledgeBaseIDs []int64
	Query            string
	TopK             int
	ScoreThreshold   float64
	ContextMaxTokens int
}

type RetrieveResult struct {
	KnowledgeBaseID int64   `json:"knowledgeBaseId"`
	ChunkID         int64   `json:"chunkId"`
	DocumentID      int64   `json:"documentId"`
	DocumentTitle   string  `json:"documentTitle"`
	FaqID           int64   `json:"faqId"`
	FaqQuestion     string  `json:"faqQuestion"`
	ChunkNo         int     `json:"chunkNo"`
	Title           string  `json:"title"`
	SectionPath     string  `json:"sectionPath"`
	Content         string  `json:"content"`
	SourceRecordID  string  `json:"sourceRecordId,omitempty"`
	Score           float32 `json:"score"`
	ChunkType       string  `json:"chunkType"`
}

type RerankRequest struct {
	Model     string   `json:"model"`
	Query     string   `json:"query"`
	Documents []string `json:"documents"`
	TopN      int      `json:"top_n"`
}

type RerankResponse struct {
	Results []struct {
		Document       json.RawMessage `json:"document"`
		Index          int             `json:"index"`
		RelevanceScore float64         `json:"relevance_score"`
	} `json:"results"`
	Usage struct {
		PromptTokens     int64 `json:"prompt_tokens"`
		CompletionTokens int64 `json:"completion_tokens"`
		TotalTokens      int64 `json:"total_tokens"`
	} `json:"usage"`
	Meta struct {
		APIVersion struct {
			Version string `json:"version"`
		} `json:"api_version"`
	} `json:"meta"`
}

type RerankResult struct {
	Index          int     `json:"index"`
	RelevanceScore float64 `json:"relevanceScore"`
}

type RerankUsage struct {
	PromptTokens     int64
	CompletionTokens int64
	TotalTokens      int64
}
