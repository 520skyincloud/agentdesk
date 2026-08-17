package runtime

import (
	"agent-desk/internal/ai/runtime/contracts"
	"agent-desk/internal/ai/runtime/registry"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/modelconfig"
)

type Request struct {
	Conversation models.Conversation
	UserMessage  models.Message
	AIAgent      models.AIAgent
	ModelConfig  modelconfig.Config
	JobID        int64
	CheckPointID string
	ToolSet      *registry.ToolSet
}

type ResumeRequest struct {
	Conversation models.Conversation
	AIAgent      models.AIAgent
	ModelConfig  modelconfig.Config
	CheckPointID string
	ResumeData   map[string]string
	ToolSet      *registry.ToolSet
}

type InterruptContextSummary struct {
	Type        string `json:"type,omitempty"`
	ID          string `json:"id"`
	InfoPreview string `json:"infoPreview,omitempty"`
}

type Summary struct {
	RunID                     string
	Status                    string
	ReplyText                 string
	PlannedSkillCode          string
	PlannedSkillName          string
	PlanReason                string
	SkillRouteTrace           string
	SkillAllowedToolCodes     []string
	ModelName                 string
	PromptTokens              int
	CompletionTokens          int
	TotalTokens               int
	CachedPromptTokens        int
	ReasoningTokens           int
	HistoryMessageCount       int
	RetrieverCount            int
	ToolCallCount             int
	ToolCodes                 []string
	InvokedToolCodes          []string
	CheckPointID              string
	Interrupted               bool
	Interrupts                []InterruptContextSummary
	TraceData                 string
	ErrorMessage              string
	PolicySkipped             bool
	PolicySkipReason          string
	RuntimeDeferred           bool
	RuntimeDeferReason        string
	ReplyModelAttempted       bool
	ModelUsageCalls           []ModelUsageCall
	TaskLedgerEnabled         bool
	TaskKeys                  []string
	FailedTaskKeys            []string
	HumanTaskKeys             []string
	HasRemainingTasks         bool
	NeedsHumanDispatch        bool
	CoveredByTaskID           int64
	ReplyParts                []contracts.ReplyPartV2
	ResolvedReplyPartsV3      []contracts.ResolvedPartV3
	PreparedActions           []contracts.PreparedActionV1
	ActionLedgerV2            *contracts.ActionLedgerV1
	ActionLedgerAuthoritative bool
}

type ModelUsageCall struct {
	PromptTokens       int
	CompletionTokens   int
	CachedPromptTokens int
	ReasoningTokens    int
}
