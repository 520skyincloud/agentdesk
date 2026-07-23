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
	ChunkID         int64   `json:"chunkId"`
	DocumentID      int64   `json:"documentId"`
	DocumentTitle   string  `json:"documentTitle"`
	ChunkNo         int     `json:"chunkNo"`
	Title           string  `json:"title"`
	SectionPath     string  `json:"sectionPath"`
	Content         string  `json:"content"`
	SourceRecordID  string  `json:"sourceRecordId,omitempty"`
	Score           float32 `json:"score"`
}
