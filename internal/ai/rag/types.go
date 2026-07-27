package rag

type RetrieveRequest struct {
	KnowledgeBaseIDs []int64
	Query            string
	TopK             int
	ScoreThreshold   float64
	ContextMaxTokens int
}

type RetrieveResult struct {
	KnowledgeBaseID int64   `json:"knowledgeBaseId"`
	DocumentTitle   string  `json:"documentTitle"`
	Title           string  `json:"title"`
	SectionPath     string  `json:"sectionPath"`
	Content         string  `json:"content"`
	SourceRecordID  string  `json:"sourceRecordId,omitempty"`
	Score           float32 `json:"score"`
}
