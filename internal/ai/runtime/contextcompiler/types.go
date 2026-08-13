package contextcompiler

import (
	"context"

	"agent-desk/internal/ai/runtime/contracts"
	"agent-desk/internal/models"
	"agent-desk/internal/services"

	"github.com/cloudwego/eino/schema"
)

type CompileStage string

type ReplyContract string

const (
	CompileStageIntent   CompileStage = "intent"
	CompileStageGenerate CompileStage = "generate"

	ReplyContractLegacy ReplyContract = "legacy"
	ReplyContractV2     ReplyContract = "v2"
)

type RuntimeScope struct {
	TenantID            int64
	StoreID             int64
	ConversationID      int64
	SessionNo           int
	WxWorkInstanceID    int64
	StoreStaffBindingID int64
	TurnID              int64
	TurnVersion         int
	JobID               int64
}

type CompileInput struct {
	Stage                            CompileStage
	Scope                            RuntimeScope
	Model                            services.ModelCallConfig
	Instance                         models.WxWorkProtocolInstance
	Agent                            models.AIAgent
	CurrentMessages                  []models.Message
	RecentHistory                    []models.Message
	Memory                           *models.ConversationSessionSummary
	DialogueState                    *contracts.DialogueStateSnapshotV1
	ReplyPlan                        *contracts.ReplyPlanV2
	Evidence                         *contracts.EvidenceBundleV1
	PreparedActions                  []contracts.ActionLedgerItemV1
	ReplyTagText                     string
	Resume                           bool
	StablePolicy                     string
	GenerationInstruction            string
	ReplyContract                    ReplyContract
	IntentInstruction                string
	IntentSchema                     []byte
	IntentProfileRevision            int64
	RepairInstruction                string
	ExpectedEvidenceScopeFingerprint string
}

type PrunedContextItem struct {
	Category        string `json:"category"`
	ItemRef         string `json:"itemRef"`
	Reason          string `json:"reason"`
	EstimatedTokens int    `json:"estimatedTokens"`
}

type CompiledModelContext struct {
	Messages       []*schema.Message
	ContextLimit   int
	ReservedOutput int
	SafetyMargin   int
	AvailableInput int
	EstimatedInput int
	Estimator      string
	CategoryTokens map[string]int
	PrunedItems    []PrunedContextItem
	Fingerprint    string
}

type ContextCompiler interface {
	Compile(ctx context.Context, input CompileInput) (CompiledModelContext, error)
}

type HistoryTurn struct {
	CustomerMessages  []models.Message
	AssistantMessages []models.Message
	FirstSeqNo        int64
	LastSeqNo         int64
	ResolvedTaskKeys  []string
	TokenCount        int
}
