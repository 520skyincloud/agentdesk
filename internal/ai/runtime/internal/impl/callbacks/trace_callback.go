package callbacks

type ToolTraceItem struct {
	ToolCode      string         `json:"toolCode"`
	ServerCode    string         `json:"serverCode"`
	ToolName      string         `json:"toolName"`
	Arguments     map[string]any `json:"arguments,omitempty"`
	ResultPreview string         `json:"resultPreview,omitempty"`
	ResultReduced bool           `json:"resultReduced,omitempty"`
	OriginalChars int            `json:"originalChars,omitempty"`
	KeptChars     int            `json:"keptChars,omitempty"`
	LatencyMs     int64          `json:"latencyMs,omitempty"`
	Status        string         `json:"status,omitempty"`
	ErrorMessage  string         `json:"errorMessage,omitempty"`
	Blocked       bool           `json:"blocked,omitempty"`
	BlockedReason string         `json:"blockedReason,omitempty"`
}

type ToolSearchTraceItem struct {
	Action             string   `json:"action,omitempty"`
	Query              string   `json:"query,omitempty"`
	TargetToolCode     string   `json:"targetToolCode,omitempty"`
	TargetServerCode   string   `json:"targetServerCode,omitempty"`
	TargetToolName     string   `json:"targetToolName,omitempty"`
	CandidateToolCodes []string `json:"candidateToolCodes,omitempty"`
	Status             string   `json:"status,omitempty"`
	ErrorMessage       string   `json:"errorMessage,omitempty"`
}

type GraphToolTraceItem struct {
	ToolCode          string         `json:"toolCode"`
	ToolName          string         `json:"toolName"`
	Arguments         map[string]any `json:"arguments,omitempty"`
	ResultPreview     string         `json:"resultPreview,omitempty"`
	ResultReduced     bool           `json:"resultReduced,omitempty"`
	OriginalChars     int            `json:"originalChars,omitempty"`
	KeptChars         int            `json:"keptChars,omitempty"`
	LatencyMs         int64          `json:"latencyMs,omitempty"`
	Status            string         `json:"status,omitempty"`
	ErrorMessage      string         `json:"errorMessage,omitempty"`
	RecommendedAction string         `json:"recommendedAction,omitempty"`
	RiskLevel         string         `json:"riskLevel,omitempty"`
	TicketDraftReady  bool           `json:"ticketDraftReady,omitempty"`
}

type RetrieverTraceItem struct {
	Query           string  `json:"query,omitempty"`
	KnowledgeBaseID int64   `json:"knowledgeBaseId,omitempty"`
	DocumentTitle   string  `json:"documentTitle,omitempty"`
	SourceRecordID  string  `json:"sourceRecordId,omitempty"`
	RawRankNo       int     `json:"rawRankNo,omitempty"`
	ContextRankNo   int     `json:"contextRankNo,omitempty"`
	UsedInContext   bool    `json:"usedInContext,omitempty"`
	DiscardReason   string  `json:"discardReason,omitempty"`
	Score           float64 `json:"score,omitempty"`
	LatencyMs       int64   `json:"latencyMs,omitempty"`
}

type RetrieverTraceSummary struct {
	TopK             int
	ScoreThreshold   float64
	ContextMaxTokens int
	MaxContextItems  int
	HitCount         int
	ContextCount     int
	EmbeddingMs      int64
	VectorSearchMs   int64
	HydrateMs        int64
	Policies         []RetrieverPolicyTraceItem
}

type AnswerabilityTraceData struct {
	Status       string   `json:"status,omitempty"`
	Reason       string   `json:"reason,omitempty"`
	MissingInfo  []string `json:"missingInfo,omitempty"`
	LatencyMs    int64    `json:"latencyMs,omitempty"`
	ErrorMessage string   `json:"errorMessage,omitempty"`
}

type RetrieverPolicyTraceItem struct {
	KnowledgeBaseID int64   `json:"knowledgeBaseId,omitempty"`
	TopK            int     `json:"topK,omitempty"`
	ScoreThreshold  float64 `json:"scoreThreshold,omitempty"`
}

type InstructionTraceSummary struct {
	SectionTitles []string
	HasAgentRule  bool
	HasSkillRule  bool
	HasToolRule   bool
}

type RuntimeTraceData struct {
	Version     string         `json:"version"`
	RuntimeMode string         `json:"runtimeMode,omitempty"`
	Status      string         `json:"status"`
	RunID       string         `json:"runId,omitempty"`
	Skill       SkillTraceData `json:"skill,omitempty"`
	Interrupt   struct {
		CheckPointID string                  `json:"checkPointId,omitempty"`
		Items        []InterruptTraceContext `json:"items,omitempty"`
	} `json:"interrupt"`
	Model struct {
		Provider string `json:"provider,omitempty"`
		Name     string `json:"name,omitempty"`
		Usage    struct {
			PromptTokens       int `json:"promptTokens,omitempty"`
			CompletionTokens   int `json:"completionTokens,omitempty"`
			TotalTokens        int `json:"totalTokens,omitempty"`
			CachedPromptTokens int `json:"cachedPromptTokens,omitempty"`
			ReasoningTokens    int `json:"reasoningTokens,omitempty"`
		} `json:"usage,omitempty"`
	} `json:"model"`
	Instruction struct {
		SectionTitles []string `json:"sectionTitles,omitempty"`
		HasAgentRule  bool     `json:"hasAgentRule,omitempty"`
		HasSkillRule  bool     `json:"hasSkillRule,omitempty"`
		HasToolRule   bool     `json:"hasToolRule,omitempty"`
	} `json:"instruction"`
	Input struct {
		HistoryMessageCount       int      `json:"historyMessageCount,omitempty"`
		ContextMemorySource       string   `json:"contextMemorySource,omitempty"`
		ContextMemoryMessageCount int      `json:"contextMemoryMessageCount,omitempty"`
		KnowledgeBaseIDs          []int64  `json:"knowledgeBaseIds,omitempty"`
		ToolCodes                 []string `json:"toolCodes,omitempty"`
		StaticToolCodes           []string `json:"staticToolCodes,omitempty"`
		DynamicToolCodes          []string `json:"dynamicToolCodes,omitempty"`
		ToolSearchEnabled         bool     `json:"toolSearchEnabled,omitempty"`
		CurrentUserMessagePreview string   `json:"currentUserMessagePreview,omitempty"`
	} `json:"input"`
	Pipeline struct {
		Normalize     NormalizeTraceData     `json:"normalize,omitempty"`
		Intent        IntentTraceData        `json:"intent,omitempty"`
		PromptSelect  IntentPromptTraceData  `json:"promptSelect,omitempty"`
		ContextBuild  ContextBuildTraceData  `json:"contextBuild,omitempty"`
		ToolKnowledge ToolKnowledgeTraceData `json:"toolKnowledge,omitempty"`
		ReplyPlan     ReplyPlanTraceData     `json:"replyPlan,omitempty"`
		Generate      GenerateTraceData      `json:"generate,omitempty"`
		Validate      ValidateTraceData      `json:"validate,omitempty"`
	} `json:"pipeline,omitempty"`
	ActionLedger ActionLedgerTraceData `json:"actionLedger,omitempty"`
	Retriever    struct {
		Count            int                        `json:"count,omitempty"`
		TopK             int                        `json:"topK,omitempty"`
		ScoreThreshold   float64                    `json:"scoreThreshold,omitempty"`
		ContextMaxTokens int                        `json:"contextMaxTokens,omitempty"`
		MaxContextItems  int                        `json:"maxContextItems,omitempty"`
		ContextCount     int                        `json:"contextCount,omitempty"`
		EmbeddingMs      int64                      `json:"embeddingMs,omitempty"`
		VectorSearchMs   int64                      `json:"vectorSearchMs,omitempty"`
		HydrateMs        int64                      `json:"hydrateMs,omitempty"`
		Policies         []RetrieverPolicyTraceItem `json:"policies,omitempty"`
		Items            []RetrieverTraceItem       `json:"items,omitempty"`
	} `json:"retriever"`
	Answerability      AnswerabilityTraceData       `json:"answerability,omitempty"`
	KnowledgeResources []KnowledgeResourceTraceData `json:"knowledgeResources,omitempty"`
	Tools              struct {
		Count int             `json:"count,omitempty"`
		Items []ToolTraceItem `json:"items,omitempty"`
	} `json:"tools"`
	ToolSearch struct {
		Count int                   `json:"count,omitempty"`
		Items []ToolSearchTraceItem `json:"items,omitempty"`
	} `json:"toolSearch"`
	GraphTools struct {
		Count int                  `json:"count,omitempty"`
		Items []GraphToolTraceItem `json:"items,omitempty"`
	} `json:"graphTools"`
	Output struct {
		ReplyText      string                   `json:"replyText,omitempty"`
		FinishReason   string                   `json:"finishReason,omitempty"`
		CommitMessages []CommitMessageTraceData `json:"commitMessages,omitempty"`
	} `json:"output"`
	Error struct {
		Message string `json:"message,omitempty"`
		Stage   string `json:"stage,omitempty"`
	} `json:"error"`
}

type NormalizeTraceData struct {
	CurrentUserText      string `json:"currentUserText,omitempty"`
	CurrentMessageType   string `json:"currentMessageType,omitempty"`
	RecentMessageCount   int    `json:"recentMessageCount,omitempty"`
	CompressedMemory     string `json:"compressedMemory,omitempty"`
	MediaContextDetected bool   `json:"mediaContextDetected,omitempty"`
}

type IntentTraceData struct {
	DetectedIntent       string   `json:"detectedIntent,omitempty"`
	MatchedIntentCode    string   `json:"matchedIntentCode,omitempty"`
	PrimaryIntent        string   `json:"primaryIntent,omitempty"`
	SubIntent            string   `json:"subIntent,omitempty"`
	DialogueAct          string   `json:"dialogueAct,omitempty"`
	SecondaryIntents     []string `json:"secondaryIntents,omitempty"`
	SecondaryIntentCodes []string `json:"secondaryIntentCodes,omitempty"`
	IntentConfidence     float64  `json:"intentConfidence,omitempty"`
	ShouldReply          bool     `json:"shouldReply,omitempty"`
	NeedsClarification   bool     `json:"needsClarification,omitempty"`
	NeedsKnowledge       bool     `json:"needsKnowledge,omitempty"`
	NeedsTool            bool     `json:"needsTool,omitempty"`
	NeedsResource        bool     `json:"needsResource,omitempty"`
	NeedsHumanRoute      bool     `json:"needsHumanRoute,omitempty"`
	ResourceType         string   `json:"resourceType,omitempty"`
	ResourceAction       string   `json:"resourceAction,omitempty"`
	ResourceActions      []string `json:"resourceActions,omitempty"`
	// Requirements 是契约 10.8 的模型建议答案义务（"kind|required"），服务端负责 ID 与状态机。
	Requirements      []string                  `json:"requirements,omitempty"`
	MixedSubTasks     []string                  `json:"mixedSubTasks,omitempty"`
	IntentTasks       []IntentTaskTraceData     `json:"intentTasks,omitempty"`
	UtteranceCoverage []IntentCoverageTraceData `json:"utteranceCoverage,omitempty"`
	ToolCodes         []string                  `json:"toolCodes,omitempty"`
	HumanRoutePolicy  string                    `json:"humanRoutePolicy,omitempty"`
	MatchedConfigID   int64                     `json:"matchedConfigId,omitempty"`
	MatchedConfig     string                    `json:"matchedConfig,omitempty"`
	MatchMode         string                    `json:"matchMode,omitempty"`
	Reason            string                    `json:"reason,omitempty"`
	ProtocolErrorCode string                    `json:"protocolErrorCode,omitempty"`
	ProtocolErrorPath string                    `json:"protocolErrorPath,omitempty"`
	RepairAttempted   bool                      `json:"repairAttempted,omitempty"`
	RepairSucceeded   bool                      `json:"repairSucceeded,omitempty"`
	ProtocolDegraded  bool                      `json:"protocolDegraded,omitempty"`
}

// IntentCoverageTraceData records the durable outcome of one current-turn
// utterance without copying the customer text. MessageID is the stable bridge
// from the ephemeral U* reference to the persisted reply job coverage ledger.
type IntentCoverageTraceData struct {
	MessageID     int64  `json:"messageId"`
	Status        string `json:"status"`
	ReasonCode    string `json:"reasonCode,omitempty"`
	TaskSequences []int  `json:"taskSequences,omitempty"`
}

type IntentTaskTraceData struct {
	Sequence              int                               `json:"sequence,omitempty"`
	Intent                string                            `json:"intent,omitempty"`
	SubIntent             string                            `json:"subIntent,omitempty"`
	Text                  string                            `json:"text,omitempty"`
	RequestMode           string                            `json:"requestMode,omitempty"`
	Confidence            float64                           `json:"confidence,omitempty"`
	QuestionUnitKey       string                            `json:"questionUnitKey,omitempty"`
	SourceMessageID       int64                             `json:"sourceMessageId,omitempty"`
	AnalysisRevision      int                               `json:"analysisRevision,omitempty"`
	SourceSpanStart       int                               `json:"sourceSpanStart,omitempty"`
	SourceSpanEnd         int                               `json:"sourceSpanEnd,omitempty"`
	SourceBindings        []TaskSourceBindingTraceData      `json:"sourceBindings,omitempty"`
	ObservationBindings   []TaskObservationBindingTraceData `json:"observationBindings,omitempty"`
	SourceSetFingerprint  string                            `json:"sourceSetFingerprint,omitempty"`
	CanonicalQuestionHash string                            `json:"canonicalQuestionHash,omitempty"`
	NeedsKnowledge        bool                              `json:"needsKnowledge,omitempty"`
	NeedsResource         bool                              `json:"needsResource,omitempty"`
	NeedsTool             bool                              `json:"needsTool,omitempty"`
	NeedsHumanRoute       bool                              `json:"needsHumanRoute,omitempty"`
	ResourceAction        string                            `json:"resourceAction,omitempty"`
	MatchedConfigID       int64                             `json:"matchedConfigId,omitempty"`
	// Requirements 是契约 10.8 的模型建议答案义务（"kind|required"）。
	Requirements []string `json:"requirements,omitempty"`
	Reason       string   `json:"reason,omitempty"`
}

// TaskSourceBindingTraceData 只记录稳定消息 ID 与 rune span，不复制客户正文。
// Envelope 内临时 U*/O* 引用不会持久化到 Task 或 RunLog。
type TaskSourceBindingTraceData struct {
	MessageID int64 `json:"messageId"`
	SpanStart int   `json:"spanStart"`
	SpanEnd   int   `json:"spanEnd"`
}

// TaskObservationBindingTraceData converts envelope-local O* references into a
// durable media-analysis identity. O* values are intentionally not persisted
// because they are renumbered whenever an envelope is rebuilt.
type TaskObservationBindingTraceData struct {
	MessageID      int64 `json:"messageId"`
	SourceRevision int   `json:"sourceRevision"`
}

type CommitMessageTraceData struct {
	MessageID    int64  `json:"messageId,omitempty"`
	MessageType  string `json:"messageType,omitempty"`
	ResourceType string `json:"resourceType,omitempty"`
	Content      string `json:"content,omitempty"`
	Status       string `json:"status,omitempty"`
	ErrorMessage string `json:"errorMessage,omitempty"`
}

// KnowledgeResourceTraceData records a scoped asset selected from a retrieved knowledge record.
// It stays outside IntentDetect output and outside the model message context.
type KnowledgeResourceTraceData struct {
	GroupID         int64  `json:"groupId,omitempty"`
	ItemID          int64  `json:"itemId,omitempty"`
	KnowledgeBaseID int64  `json:"knowledgeBaseId,omitempty"`
	SourceRecordID  string `json:"sourceRecordId,omitempty"`
	AssetID         string `json:"assetId,omitempty"`
	Title           string `json:"title,omitempty"`
	Description     string `json:"description,omitempty"`
	SortNo          int    `json:"sortNo,omitempty"`
}

type ActionLedgerTraceData struct {
	RequestedActions  []ActionLedgerItem `json:"requestedActions,omitempty"`
	PreparedActions   []ActionLedgerItem `json:"preparedActions,omitempty"`
	CommittedActions  []ActionLedgerItem `json:"committedActions,omitempty"`
	MissingActions    []ActionLedgerItem `json:"missingActions,omitempty"`
	SuppressedActions []ActionLedgerItem `json:"suppressedActions,omitempty"`
}

type ActionLedgerItem struct {
	Action       string `json:"action,omitempty"`
	ResourceType string `json:"resourceType,omitempty"`
	MessageType  string `json:"messageType,omitempty"`
	MessageID    int64  `json:"messageId,omitempty"`
	Status       string `json:"status,omitempty"`
	Reason       string `json:"reason,omitempty"`
}

type IntentPromptTraceData struct {
	PackName     string   `json:"packName,omitempty"`
	Instructions []string `json:"instructions,omitempty"`
}

type ContextBuildTraceData struct {
	Mode                    string   `json:"mode,omitempty"`
	CurrentTurn             string   `json:"currentTurn,omitempty"`
	RecentRawMessageCount   int      `json:"recentRawMessageCount,omitempty"`
	CompressedMemorySource  string   `json:"compressedMemorySource,omitempty"`
	CompressedMemoryCount   int      `json:"compressedMemoryCount,omitempty"`
	MediaContextCount       int      `json:"mediaContextCount,omitempty"`
	Priority                []string `json:"priority,omitempty"`
	IntentResourcesExpected []string `json:"intentResourcesExpected,omitempty"`
	ContextLimit            int      `json:"contextLimit,omitempty"`
	ReservedOutput          int      `json:"reservedOutput,omitempty"`
	SafetyMargin            int      `json:"safetyMargin,omitempty"`
	AvailableInput          int      `json:"availableInput,omitempty"`
	EstimatedInput          int      `json:"estimatedInput,omitempty"`
	Estimator               string   `json:"estimator,omitempty"`
	Fingerprint             string   `json:"fingerprint,omitempty"`
	ShadowStatus            string   `json:"shadowStatus,omitempty"`
	ShadowEstimatedInput    int      `json:"shadowEstimatedInput,omitempty"`
	ShadowFingerprint       string   `json:"shadowFingerprint,omitempty"`
	ShadowPrunedCount       int      `json:"shadowPrunedCount,omitempty"`
	ShadowError             string   `json:"shadowError,omitempty"`
}

type ReplyPlanTraceData struct {
	Intent     string                   `json:"intent,omitempty"`
	AnswerGoal string                   `json:"answerGoal,omitempty"`
	UseContext []string                 `json:"useContext,omitempty"`
	DoNot      []string                 `json:"doNot,omitempty"`
	Style      string                   `json:"style,omitempty"`
	TaskPlans  []ReplyTaskPlanTraceData `json:"taskPlans,omitempty"`
}

type ReplyTaskPlanTraceData struct {
	TaskKey               string                            `json:"taskKey,omitempty"`
	Sequence              int                               `json:"sequence,omitempty"`
	AnswerGroup           string                            `json:"answerGroup,omitempty"`
	Intent                string                            `json:"intent,omitempty"`
	SubIntent             string                            `json:"subIntent,omitempty"`
	Text                  string                            `json:"text,omitempty"`
	RequestMode           string                            `json:"requestMode,omitempty"`
	RelationType          string                            `json:"relationType,omitempty"`
	QuestionUnitKey       string                            `json:"questionUnitKey,omitempty"`
	SourceMessageID       int64                             `json:"sourceMessageId,omitempty"`
	AnalysisRevision      int                               `json:"analysisRevision,omitempty"`
	SourceSpanStart       int                               `json:"sourceSpanStart,omitempty"`
	SourceSpanEnd         int                               `json:"sourceSpanEnd,omitempty"`
	SourceBindings        []TaskSourceBindingTraceData      `json:"sourceBindings,omitempty"`
	ObservationBindings   []TaskObservationBindingTraceData `json:"observationBindings,omitempty"`
	SourceSetFingerprint  string                            `json:"sourceSetFingerprint,omitempty"`
	CanonicalQuestionHash string                            `json:"canonicalQuestionHash,omitempty"`
	Output                string                            `json:"output,omitempty"`
	ResourceAction        string                            `json:"resourceAction,omitempty"`
	// Requirements 是契约 10.8 的模型建议答案义务（"kind|required"）。
	Requirements []string `json:"requirements,omitempty"`
}

func (d ReplyPlanTraceData) HasMultipleTasks() bool {
	return len(d.TaskPlans) > 1
}

type ToolKnowledgeTraceData struct {
	Policy             string   `json:"policy,omitempty"`
	ExpectedResources  []string `json:"expectedResources,omitempty"`
	KnowledgeTriggered bool     `json:"knowledgeTriggered,omitempty"`
	ToolTriggered      bool     `json:"toolTriggered,omitempty"`
}

type GenerateTraceData struct {
	Policy     string                   `json:"policy,omitempty"`
	Status     string                   `json:"status,omitempty"`
	Reason     string                   `json:"reason,omitempty"`
	LatencyMs  int64                    `json:"latencyMs,omitempty"`
	TagContext ReplyTagContextTraceData `json:"tagContext,omitempty"`
}

type ReplyTagContextTraceData struct {
	SchemaVersion string   `json:"schemaVersion,omitempty"`
	Status        string   `json:"status,omitempty"`
	Scenes        []string `json:"scenes,omitempty"`
	TagIDs        []int64  `json:"tagIds,omitempty"`
	Count         int      `json:"count,omitempty"`
	RenderedChars int      `json:"renderedChars,omitempty"`
	Reason        string   `json:"reason,omitempty"`
}

type ValidateTraceData struct {
	Rules  []string `json:"rules,omitempty"`
	Status string   `json:"status,omitempty"`
	Reason string   `json:"reason,omitempty"`
}

type SkillTraceData struct {
	Code               string   `json:"code,omitempty"`
	Name               string   `json:"name,omitempty"`
	Description        string   `json:"description,omitempty"`
	RouteReason        string   `json:"routeReason,omitempty"`
	RouteTrace         string   `json:"routeTrace,omitempty"`
	AllowedToolCodes   []string `json:"allowedToolCodes,omitempty"`
	FilteredToolCodes  []string `json:"filteredToolCodes,omitempty"`
	MiddlewareEnabled  bool     `json:"middlewareEnabled,omitempty"`
	MiddlewareToolName string   `json:"middlewareToolName,omitempty"`
	VisibleCodes       []string `json:"visibleCodes,omitempty"`
}

type InterruptTraceContext struct {
	Type        string `json:"type,omitempty"`
	ID          string `json:"id"`
	InfoPreview string `json:"infoPreview,omitempty"`
}
