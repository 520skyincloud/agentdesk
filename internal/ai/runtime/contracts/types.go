package contracts

import "time"

const (
	MessageAnalysisV1SchemaVersion        = SchemaMessageAnalysisV1
	DialogueStateSnapshotV1SchemaVersion  = SchemaDialogueStateSnapshotV1
	IntentTasksV2SchemaVersion            = SchemaIntentTasksV2
	ReplyPlanV2SchemaVersion              = SchemaReplyPlanV2
	ActionLedgerV1SchemaVersion           = SchemaActionLedgerV1
	EvidenceBundleV1SchemaVersion         = SchemaEvidenceBundleV1
	RuntimeContextSnapshotV1SchemaVersion = SchemaRuntimeContextSnapshotV1
	ReplyOutputV2SchemaVersion            = SchemaReplyOutputV2
	ValidationResultV1SchemaVersion       = SchemaValidationResultV1
	ReplyTagContextV1SchemaVersion        = SchemaReplyTagContextV1
	RuntimeTraceV2SchemaVersion           = SchemaRuntimeTraceV2
)

type MessageAnalysisV1 struct {
	SchemaVersion      string                  `json:"schemaVersion"`
	MessageID          int64                   `json:"messageId"`
	SourceRevision     int                     `json:"sourceRevision"`
	ContentFingerprint string                  `json:"contentFingerprint"`
	Status             string                  `json:"status"`
	Analyzer           MessageAnalysisAnalyzer `json:"analyzer"`
	Result             *MessageAnalysisResult  `json:"result"`
	ErrorCode          *string                 `json:"errorCode"`
	AnalyzedAt         *time.Time              `json:"analyzedAt"`
}

type MessageAnalysisAnalyzer struct {
	Kind    string `json:"kind"`
	Name    string `json:"name"`
	Version string `json:"version"`
}

type MessageAnalysisResult struct {
	Language         string                  `json:"language"`
	DialogueAct      string                  `json:"dialogueAct"`
	RelationToPrior  string                  `json:"relationToPrior"`
	NormalizedText   string                  `json:"normalizedText"`
	Entities         []MessageAnalysisEntity `json:"entities"`
	MentionedTagKeys []string                `json:"mentionedTagKeys"`
	RiskSignals      []string                `json:"riskSignals"`
	Confidence       float64                 `json:"confidence"`
}

type MessageAnalysisEntity struct {
	Type       string  `json:"type"`
	Value      string  `json:"value"`
	Start      int     `json:"start"`
	End        int     `json:"end"`
	Confidence float64 `json:"confidence"`
}

type DialogueStateSnapshotV1 struct {
	SchemaVersion      string                      `json:"schemaVersion"`
	ConversationID     int64                       `json:"conversationId"`
	SessionNo          int                         `json:"sessionNo"`
	Revision           int64                       `json:"revision"`
	BasedOnMessageID   int64                       `json:"basedOnMessageId"`
	BasedOnTurnID      int64                       `json:"basedOnTurnId"`
	BasedOnTurnVersion int                         `json:"basedOnTurnVersion"`
	ConversationMode   string                      `json:"conversationMode"`
	Focus              DialogueStateFocus          `json:"focus"`
	ResolvedTasks      []DialogueStateResolvedTask `json:"resolvedTasks"`
	OpenTasks          []DialogueStateOpenTask     `json:"openTasks"`
	SessionFacts       []DialogueStateSessionFact  `json:"sessionFacts"`
	LastAssistant      *DialogueStateLastAssistant `json:"lastAssistantMessage"`
	UpdatedAt          time.Time                   `json:"updatedAt"`
}

type DialogueStateFocus struct {
	Topic           string   `json:"topic"`
	RelationToPrior string   `json:"relationToPrior"`
	ActiveTaskKeys  []string `json:"activeTaskKeys"`
}

type DialogueStateResolvedTask struct {
	TaskKey         string    `json:"taskKey"`
	Outcome         string    `json:"outcome"`
	AnswerMessageID int64     `json:"answerMessageId"`
	ResolvedAt      time.Time `json:"resolvedAt"`
}

type DialogueStateOpenTask struct {
	TaskKey      string  `json:"taskKey"`
	Intent       string  `json:"intent"`
	SubIntent    string  `json:"subIntent"`
	State        string  `json:"state"`
	MissingField *string `json:"missingField"`
}

type DialogueStateSessionFact struct {
	Key        string                  `json:"key"`
	Value      string                  `json:"value"`
	Source     DialogueStateFactSource `json:"source"`
	Confidence float64                 `json:"confidence"`
	ExpiresAt  *time.Time              `json:"expiresAt"`
}

type DialogueStateFactSource struct {
	Type      string  `json:"type"`
	MessageID int64   `json:"messageId"`
	TaskKey   *string `json:"taskKey"`
}

type DialogueStateLastAssistant struct {
	MessageID  int64    `json:"messageId"`
	SenderType string   `json:"senderType"`
	TaskKeys   []string `json:"taskKeys"`
}

type IntentTasksV2 struct {
	SchemaVersion string         `json:"schemaVersion"`
	DialogueAct   string         `json:"dialogueAct"`
	Tasks         []IntentTaskV2 `json:"tasks"`
}

type IntentTaskV2 struct {
	Sequence    int     `json:"sequence"`
	Intent      string  `json:"intent"`
	SubIntent   string  `json:"subIntent"`
	Text        string  `json:"text"`
	RequestMode string  `json:"requestMode"`
	Confidence  float64 `json:"confidence"`
}

type ReplyPlanV2 struct {
	SchemaVersion     string                     `json:"schemaVersion"`
	TurnVersion       int                        `json:"turnVersion"`
	ShouldGenerate    bool                       `json:"shouldGenerate"`
	Tasks             []ReplyPlanTaskV2          `json:"tasks"`
	GlobalConstraints ReplyPlanGlobalConstraints `json:"globalConstraints"`
}

type ReplyPlanTaskV2 struct {
	TaskKey      string             `json:"taskKey"`
	Sequence     int                `json:"sequence"`
	Intent       string             `json:"intent"`
	SubIntent    string             `json:"subIntent"`
	Objective    string             `json:"objective"`
	OutputMode   string             `json:"outputMode"`
	Knowledge    ReplyPlanKnowledge `json:"knowledge"`
	EvidenceRefs []string           `json:"evidenceRefs"`
	ActionRefs   []string           `json:"actionRefs"`
	Constraints  []string           `json:"constraints"`
}

type ReplyPlanKnowledge struct {
	Policy     string `json:"policy"`
	Status     string `json:"status"`
	ReasonCode string `json:"reasonCode"`
}

type ReplyPlanGlobalConstraints struct {
	MaxReplyParts       int      `json:"maxReplyParts"`
	MaxQuestionsPerPart int      `json:"maxQuestionsPerPart"`
	ForbiddenClaims     []string `json:"forbiddenClaims"`
}

type ActionLedgerV1 struct {
	SchemaVersion string               `json:"schemaVersion"`
	TurnVersion   int                  `json:"turnVersion"`
	Actions       []ActionLedgerItemV1 `json:"actions"`
}

type ActionLedgerItemV1 struct {
	ActionKey          string  `json:"actionKey"`
	TaskKey            string  `json:"taskKey"`
	ActionType         string  `json:"actionType"`
	ResourceType       *string `json:"resourceType"`
	Status             string  `json:"status"`
	CommittedMessageID int64   `json:"committedMessageId"`
	OutboxID           int64   `json:"outboxId"`
	ResultCode         string  `json:"resultCode"`
}

// PreparedActionV1 is an internal, in-memory handoff from resource preparation
// to Commit. Payload is never rendered into prompts, traces, or public DTOs.
type PreparedActionV1 struct {
	ActionKey              string
	TaskKey                string
	ActionType             string
	ResourceType           string
	ResourceRef            string
	Sequence               int
	MessageType            string
	Content                string
	Payload                string
	PreparedRevision       string
	EligibilityFingerprint string
	SourceEvidenceRef      string
	SourceRecordID         string
	ResourcePurpose        string
	EligibilityReasonCode  string
}

type EvidenceBundleV1 struct {
	SchemaVersion    string               `json:"schemaVersion"`
	ScopeFingerprint string               `json:"scopeFingerprint"`
	RetrievalStatus  string               `json:"retrievalStatus"`
	Items            []EvidenceItemV1     `json:"items"`
	Resources        []EvidenceResourceV1 `json:"resources"`
}

type EvidenceItemV1 struct {
	Ref           string   `json:"ref"`
	SourceType    string   `json:"sourceType"`
	TaskKeys      []string `json:"taskKeys"`
	Title         string   `json:"title"`
	Content       string   `json:"content"`
	Score         float64  `json:"score"`
	Answerability string   `json:"answerability"`
	ResourceRefs  []string `json:"resourceRefs"`
}

type EvidenceResourceV1 struct {
	Ref      string   `json:"ref"`
	Type     string   `json:"type"`
	AssetID  *string  `json:"assetId"`
	Title    string   `json:"title"`
	TaskKeys []string `json:"taskKeys"`
}

type RuntimeContextSnapshotV1 struct {
	SchemaVersion    string                         `json:"schemaVersion"`
	ConversationMode string                         `json:"conversationMode"`
	DialogueAct      string                         `json:"dialogueAct"`
	Focus            RuntimeContextFocus            `json:"focus"`
	Tasks            []RuntimeContextTask           `json:"tasks"`
	SessionFacts     []RuntimeContextFact           `json:"sessionFacts"`
	PreparedActions  []RuntimeContextPreparedAction `json:"preparedActions"`
	ResponsePolicy   RuntimeContextResponsePolicy   `json:"responsePolicy"`
}

type RuntimeContextFocus struct {
	Topic            string   `json:"topic"`
	RelationToPrior  string   `json:"relationToPrior"`
	ResolvedTaskKeys []string `json:"resolvedTaskKeys"`
}

type RuntimeContextTask struct {
	TaskKey         string   `json:"taskKey"`
	Sequence        int      `json:"sequence"`
	Objective       string   `json:"objective"`
	OutputMode      string   `json:"outputMode"`
	KnowledgeStatus string   `json:"knowledgeStatus"`
	EvidenceRefs    []string `json:"evidenceRefs"`
	ActionRefs      []string `json:"actionRefs"`
	Constraints     []string `json:"constraints"`
}

type RuntimeContextFact struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type RuntimeContextPreparedAction struct {
	ActionRef string `json:"actionRef"`
	TaskKey   string `json:"taskKey"`
	Type      string `json:"type"`
	Status    string `json:"status"`
}

type RuntimeContextResponsePolicy struct {
	MaxParts                       int    `json:"maxParts"`
	Style                          string `json:"style"`
	MustNotMentionInternalState    bool   `json:"mustNotMentionInternalState"`
	MustNotClaimUncommittedActions bool   `json:"mustNotClaimUncommittedActions"`
}

type ReplyOutputV2 struct {
	SchemaVersion string        `json:"schemaVersion"`
	Parts         []ReplyPartV2 `json:"parts"`
}

type ReplyPartV2 struct {
	TaskKeys     []string `json:"taskKeys"`
	Content      string   `json:"content"`
	EvidenceRefs []string `json:"evidenceRefs"`
	ActionRefs   []string `json:"actionRefs"`
}

type ValidationResultV1 struct {
	SchemaVersion   string              `json:"schemaVersion"`
	Status          string              `json:"status"`
	NormalizedParts []ReplyPartV2       `json:"normalizedParts"`
	Checks          ValidationChecksV1  `json:"checks"`
	Errors          []ValidationIssueV1 `json:"errors"`
	Warnings        []ValidationIssueV1 `json:"warnings"`
}

type ValidationChecksV1 struct {
	Schema             string `json:"schema"`
	TaskCoverage       string `json:"taskCoverage"`
	EvidenceReferences string `json:"evidenceReferences"`
	FactGrounding      string `json:"factGrounding"`
	ActionReferences   string `json:"actionReferences"`
	Safety             string `json:"safety"`
	CommitInvariants   string `json:"commitInvariants"`
}

type ValidationIssueV1 struct {
	Code    string `json:"code"`
	Path    string `json:"path"`
	Message string `json:"message"`
}

type ReplyTagContextV1 struct {
	SchemaVersion string                 `json:"schemaVersion"`
	Scenes        []string               `json:"scenes"`
	Tags          []ReplyTagContextTagV1 `json:"tags"`
}

type ReplyTagContextTagV1 struct {
	TagID       int64  `json:"tagId"`
	SemanticKey string `json:"semanticKey"`
	Name        string `json:"name"`
}

type RuntimeTraceV2 struct {
	SchemaVersion string                  `json:"schemaVersion"`
	RunID         string                  `json:"runId"`
	RequestID     string                  `json:"requestId"`
	Scope         RuntimeTraceScope       `json:"scope"`
	Turn          RuntimeTraceTurn        `json:"turn"`
	Stages        []RuntimeTraceStage     `json:"stages"`
	Context       RuntimeTraceContext     `json:"context"`
	ModelCalls    []RuntimeTraceModelCall `json:"modelCalls"`
	Final         RuntimeTraceFinal       `json:"final"`
}

type RuntimeTraceScope struct {
	TenantID            int64 `json:"tenantId"`
	StoreID             int64 `json:"storeId"`
	ConversationID      int64 `json:"conversationId"`
	SessionNo           int   `json:"sessionNo"`
	WxWorkInstanceID    int64 `json:"wxWorkInstanceId"`
	StoreStaffBindingID int64 `json:"storeStaffBindingId"`
}

type RuntimeTraceTurn struct {
	TurnID         int64  `json:"turnId"`
	TurnVersion    int    `json:"turnVersion"`
	JobID          int64  `json:"jobId"`
	LeaseOwnerHash string `json:"leaseOwnerHash"`
}

type RuntimeTraceStage struct {
	Name       string `json:"name"`
	Status     string `json:"status"`
	ReasonCode string `json:"reasonCode"`
	DurationMS int64  `json:"durationMs"`
}

type RuntimeTraceContext struct {
	Fingerprint    string                   `json:"fingerprint"`
	ContextLimit   int                      `json:"contextLimit"`
	ReservedOutput int                      `json:"reservedOutput"`
	SafetyMargin   int                      `json:"safetyMargin"`
	AvailableInput int                      `json:"availableInput"`
	EstimatedInput int                      `json:"estimatedInput"`
	Estimator      string                   `json:"estimator"`
	CategoryTokens map[string]int           `json:"categoryTokens"`
	Pruned         []RuntimeTracePrunedItem `json:"pruned"`
}

type RuntimeTracePrunedItem struct {
	Category        string `json:"category"`
	ItemRef         string `json:"itemRef"`
	Reason          string `json:"reason"`
	EstimatedTokens int    `json:"estimatedTokens"`
}

type RuntimeTraceModelCall struct {
	Stage            string  `json:"stage"`
	RepairIndex      int     `json:"repairIndex"`
	Provider         string  `json:"provider"`
	Model            string  `json:"model"`
	RequestID        string  `json:"requestId"`
	PromptTokens     int     `json:"promptTokens"`
	CompletionTokens int     `json:"completionTokens"`
	ReasoningTokens  int     `json:"reasoningTokens"`
	Quota            float64 `json:"quota"`
	DurationMS       int64   `json:"durationMs"`
	Status           string  `json:"status"`
}

type RuntimeTraceFinal struct {
	Status              string  `json:"status"`
	Action              string  `json:"action"`
	ReasonCode          string  `json:"reasonCode"`
	CommittedMessageIDs []int64 `json:"committedMessageIds"`
	OutboxIDs           []int64 `json:"outboxIds"`
}
