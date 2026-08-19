package executor

import (
	"agent-desk/internal/ai/runtime/contextcompiler"
	"agent-desk/internal/ai/runtime/contracts"
	"agent-desk/internal/ai/runtime/registry"
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/modelconfig"
)

type RunInput struct {
	Conversation models.Conversation
	UserMessage  models.Message
	AIAgent      models.AIAgent
	ModelConfig  modelconfig.Config
	JobID        int64
	CheckPointID string
	ToolSet      *registry.ToolSet
}

type ResumeInput struct {
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

type RunResult struct {
	RunID string
	// RunRequest 保留本次运行输入，供受保护事实校验解析权威门店。
	RunRequest                 RunInput
	Status                     string
	GenerationOutcome          GenerationOutcome
	ReplyText                  string
	RawReplyOutput             string
	SelectedSkillCode          string
	SelectedSkillName          string
	SkillRouteReason           string
	SkillRouteTrace            string
	SkillAllowedToolCodes      []string
	ModelName                  string
	PromptTokens               int
	CompletionTokens           int
	TotalTokens                int
	CachedPromptTokens         int
	ReasoningTokens            int
	HistoryMessageCount        int
	ContextMemorySource        string
	ContextMemoryMessageCount  int
	RetrieverCount             int
	ToolCallCount              int
	ToolCodes                  []string
	InvokedToolCodes           []string
	CheckPointID               string
	Interrupted                bool
	Interrupts                 []InterruptContextSummary
	TraceData                  string
	ErrorMessage               string
	SkipReply                  bool
	ModelUsageCalls            []ModelUsageCall
	TaskLedgerEnabled          bool
	TaskKeys                   []string
	FailedTaskKeys             []string
	HumanTaskKeys              []string
	HasRemainingTasks          bool
	NeedsHumanDispatch         bool
	SkipGeneration             bool
	CoveredByTaskID            int64
	ReplyParts                 []contracts.ReplyPartV2
	ReplyPlanV2                *contracts.ReplyPlanV2
	EvidenceBundle             *contracts.EvidenceBundleV1
	ActionLedgerV2             *contracts.ActionLedgerV1
	PreparedActions            []contracts.PreparedActionV1
	ValidationResult           *contracts.ValidationResultV1
	CompiledContext            *contextcompiler.CompiledModelContext
	GenerateCompileInput       *contextcompiler.CompileInput
	UseRuntimeV2Generate       bool
	UseRuntimeV2DirectGenerate bool
	ReplyModelAttempted        bool
	RuntimeValidatorMode       string
	ActionLedgerAuthoritative  bool
	// ValidationGates 是 P9 门禁开关快照（生成前按 req 计算；零值默认全开）。
	ValidationGates ReplyValidationGates
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
