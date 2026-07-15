package executor

import (
	"agent-desk/internal/ai/runtime/registry"
	"agent-desk/internal/models"
)

type RunInput struct {
	Conversation models.Conversation
	UserMessage  models.Message
	AIAgent      models.AIAgent
	AIConfig     models.AIConfig
	CheckPointID string
	ToolSet      *registry.ToolSet
}

type ResumeInput struct {
	Conversation models.Conversation
	AIAgent      models.AIAgent
	AIConfig     models.AIConfig
	CheckPointID string
	ResumeData   map[string]string
	ToolSet      *registry.ToolSet
}

type InterruptContextSummary struct {
	Type        string `json:"type,omitempty"`
	ID          string `json:"id"`
	InfoPreview string `json:"infoPreview,omitempty"`
}

type RunResult struct {
	RunID                     string
	Status                    string
	ReplyText                 string
	SelectedSkillCode         string
	SelectedSkillName         string
	SkillRouteReason          string
	SkillRouteTrace           string
	SkillAllowedToolCodes     []string
	ModelName                 string
	PromptTokens              int
	CompletionTokens          int
	TotalTokens               int
	CachedPromptTokens        int
	ReasoningTokens           int
	HistoryMessageCount       int
	ContextMemorySource       string
	ContextMemoryMessageCount int
	RetrieverCount            int
	ToolCallCount             int
	ToolCodes                 []string
	InvokedToolCodes          []string
	CheckPointID              string
	Interrupted               bool
	Interrupts                []InterruptContextSummary
	TraceData                 string
	ErrorMessage              string
	SkipReply                 bool
	ModelUsageCalls           []ModelUsageCall
}

// ModelUsageCall preserves one upstream model response. It is intentionally
// separate from the aggregate counters used by run diagnostics so billing can
// meter retries without double counting the run total.
type ModelUsageCall struct {
	PromptTokens       int
	CompletionTokens   int
	CachedPromptTokens int
	ReasoningTokens    int
}
