package request

type UpdateKnowledgeBaseRequest struct {
	ID                    int64    `json:"id"`
	Name                  string   `json:"name"`
	Description           string   `json:"description"`
	DefaultTopK           int      `json:"defaultTopK"`
	DefaultScoreThreshold float64  `json:"defaultScoreThreshold"`
	DefaultRerankLimit    int      `json:"defaultRerankLimit"`
	AnswerMode            int      `json:"answerMode"`
	ResourceAllowedHosts  []string `json:"resourceAllowedHosts"`
}

type SyncKnowledgeResourceGroupRequest struct {
	KnowledgeBaseID        int64  `json:"knowledgeBaseId"`
	Query                  string `json:"query"`
	ExpectedSourceRecordID string `json:"expectedSourceRecordId"`
}

type DeleteKnowledgeResourceGroupRequest struct {
	ID int64 `json:"id"`
}

type KnowledgeSearchRequest struct {
	KnowledgeBaseIDs []int64 `json:"knowledgeBaseIds"`
	Question         string  `json:"question"`
	TopK             int     `json:"topK"`
	ScoreThreshold   float64 `json:"scoreThreshold"`
	RerankLimit      int     `json:"rerankLimit"`
	Channel          string  `json:"channel"`
	Scene            string  `json:"scene"`
	SessionID        string  `json:"sessionId"`
	ConversationID   int64   `json:"conversationId"`
}

type KnowledgeAnswerRequest struct {
	KnowledgeBaseIDs []int64 `json:"knowledgeBaseIds"`
	Question         string  `json:"question"`
	TopK             int     `json:"topK"`
	ScoreThreshold   float64 `json:"scoreThreshold"`
	RerankLimit      int     `json:"rerankLimit"`
	Channel          string  `json:"channel"`
	Scene            string  `json:"scene"`
	SessionID        string  `json:"sessionId"`
	ConversationID   int64   `json:"conversationId"`
	AnswerMode       int     `json:"answerMode"`
}

type CreateKnowledgeFeedbackRequest struct {
	RetrieveLogID  int64  `json:"retrieveLogId"`
	FeedbackType   int    `json:"feedbackType"`
	FeedbackReason string `json:"feedbackReason"`
	Remark         string `json:"remark"`
}

type UpdateKnowledgeCandidateRequest struct {
	ID         int64   `json:"id"`
	Question   string  `json:"question"`
	Answer     string  `json:"answer"`
	Summary    string  `json:"summary"`
	Confidence float64 `json:"confidence"`
	Status     string  `json:"status"`
}

type ReviewKnowledgeCandidateRequest struct {
	ID     int64  `json:"id"`
	Remark string `json:"remark"`
}

type BatchReviewKnowledgeCandidateRequest struct {
	IDs    []int64 `json:"ids"`
	Remark string  `json:"remark"`
}

type AnalyzeKnowledgeCandidateConversationRequest struct {
	ConversationID int64 `json:"conversationId"`
}

type ExportKnowledgeCandidateWeeklyRequest struct {
	StoreID int64  `json:"storeId"`
	Year    int    `json:"year"`
	Week    int    `json:"week"`
	Status  string `json:"status"`
}
