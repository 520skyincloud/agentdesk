package contracts

// 多模态契约 19：validation_result.v3。Validator 只分类输出和建议恢复阶段，
// 不决定是否转人工。

const ValidationResultV3SchemaVersion = SchemaValidationResultV3
const EvidenceBundleV2SchemaVersion = SchemaEvidenceBundleV2

// ValidationResultV3 是 ValidatorV3 的输出。
type ValidationResultV3 struct {
	SchemaVersion   string              `json:"schemaVersion"`
	Status          string              `json:"status"` // passed/warning/retryable_content_error/repairable_protocol_error/rejected
	NormalizedParts []ResolvedPartV3    `json:"normalizedParts"`
	Checks          ValidationChecksV3  `json:"checks"`
	Errors          []ValidationIssueV1 `json:"errors"`
	Warnings        []ValidationIssueV1 `json:"warnings"`
	// RecoveryStage 是建议的恢复阶段（generate/commit/none）。
	RecoveryStage string `json:"recoveryStage"`
}

// ResolvedPartV3 是服务端解析引用后的规范化分段。
type ResolvedPartV3 struct {
	GroupKey              string   `json:"groupKey"`
	TaskKeys              []string `json:"taskKeys"`
	Content               string   `json:"content"`
	GroundingEvidenceRefs []string `json:"groundingEvidenceRefs"`
	ResolvedActionRefs    []string `json:"resolvedActionRefs"`
}

// ValidationChecksV3 覆盖 19.1 调用顺序。
type ValidationChecksV3 struct {
	Schema             string `json:"schema"`
	GroupCoverage      string `json:"groupCoverage"`
	TaskCoverage       string `json:"taskCoverage"`
	ServerResolvedRefs string `json:"serverResolvedRefs"`
	DuplicateContent   string `json:"duplicateContent"`
	FactSource         string `json:"factSource"`
	KnowledgeQuality   string `json:"knowledgeQuality"`
	ActionClaims       string `json:"actionClaims"`
	Safety             string `json:"safety"`
	CommitInvariants   string `json:"commitInvariants"`
}

// EvidenceBundleV2 是证据账本（Validator 只消费最终投影字段）。
type EvidenceBundleV2 struct {
	SchemaVersion    string               `json:"schemaVersion"`
	ScopeFingerprint string               `json:"scopeFingerprint"`
	RetrievalStatus  string               `json:"retrievalStatus"`
	Items            []EvidenceItemV2     `json:"items"`
	Resources        []EvidenceResourceV2 `json:"resources"`
}

// EvidenceItemV2 携带 Metadata Judge 的最终判定。
type EvidenceItemV2 struct {
	Ref            string   `json:"ref"`
	SourceType     string   `json:"sourceType"`
	SourceClass    string   `json:"sourceClass"`
	SourceRecordID string   `json:"sourceRecordId,omitempty"`
	FactKey        string   `json:"factKey,omitempty"`
	TaskKeys       []string `json:"taskKeys"`
	Title          string   `json:"title"`
	Content        string   `json:"content"`
	Score          float64  `json:"score"`
	FactScope      string   `json:"factScope"`
	ClaimType      string   `json:"claimType"`
	TrustLevel     string   `json:"trustLevel"`
	Freshness      string   `json:"freshness"`
	TopicLabels    []string `json:"topicLabels,omitempty"`
	TopicMatch     string   `json:"topicMatch"`
	Answerability  string   `json:"answerability"` // supporting/context_only/blocked
	AllowedUses    []string `json:"allowedUses"`
	BlockedReasons []string `json:"blockedReasons"`
	ResourceRefs   []string `json:"resourceRefs"`
}

type EvidenceResourceV2 struct {
	Ref      string   `json:"ref"`
	Type     string   `json:"type"`
	AssetID  *string  `json:"assetId"`
	Title    string   `json:"title"`
	TaskKeys []string `json:"taskKeys"`
}

// ObservationV1 是客户媒体的受限观察。Observation 不是门店事实，允许用途
// 和禁止用途必须由服务端 ObservationPolicy 投影，模型不能自行提升权限。
type ObservationV1 struct {
	Ref             string   `json:"ref"`
	SourceMessageID int64    `json:"sourceMessageId"`
	SourceRevision  int      `json:"sourceRevision"`
	Status          string   `json:"status"`
	SourceType      string   `json:"sourceType"`
	ObservationType string   `json:"observationType"`
	Text            string   `json:"text"`
	Confidence      float64  `json:"confidence"`
	AllowedUses     []string `json:"allowedUses"`
	ForbiddenUses   []string `json:"forbiddenUses"`
}

// RuntimeContextSnapshotV2 是 Generate 可见的当前轮快照。当前任务、媒体观察、
// 权威事实和已准备动作分别进入独立字段，禁止把历史/客户断言混成门店事实。
type RuntimeContextSnapshotV2 struct {
	SchemaVersion    string                         `json:"schemaVersion"`
	ConversationMode string                         `json:"conversationMode"`
	DialogueAct      string                         `json:"dialogueAct"`
	Focus            RuntimeContextFocus            `json:"focus"`
	Tasks            []RuntimeContextTaskV2         `json:"tasks"`
	Observations     []RuntimeContextObservationV2  `json:"observations"`
	Facts            []RuntimeContextFactV2         `json:"facts"`
	PreparedActions  []RuntimeContextPreparedAction `json:"preparedActions"`
	ResponsePolicy   RuntimeContextResponsePolicyV2 `json:"responsePolicy"`
}

type RuntimeContextTaskV2 struct {
	TaskKey          string   `json:"taskKey"`
	Sequence         int      `json:"sequence"`
	Objective        string   `json:"objective"`
	ClaimType        string   `json:"claimType"`
	OutputMode       string   `json:"outputMode"`
	KnowledgeStatus  string   `json:"knowledgeStatus"`
	EvidenceRefs     []string `json:"evidenceRefs"`
	ObservationRefs  []string `json:"observationRefs"`
	RequiredFactRefs []string `json:"requiredFactRefs"`
	ActionRefs       []string `json:"actionRefs"`
	Constraints      []string `json:"constraints"`
}

type RuntimeContextObservationV2 struct {
	Ref           string   `json:"ref"`
	SourceClass   string   `json:"sourceClass"`
	SourceID      string   `json:"sourceId,omitempty"`
	Speaker       string   `json:"speaker,omitempty"`
	Content       string   `json:"content"`
	AllowedUses   []string `json:"allowedUses"`
	ForbiddenUses []string `json:"forbiddenUses"`
}

type RuntimeContextResponsePolicyV2 struct {
	MaxParts                       int    `json:"maxParts"`
	Style                          string `json:"style"`
	MustNotMentionInternalState    bool   `json:"mustNotMentionInternalState"`
	MustNotClaimUncommittedActions bool   `json:"mustNotClaimUncommittedActions"`
	MustCiteProtectedFacts         bool   `json:"mustCiteProtectedFacts"`
}

// RuntimeContextFactV2 是带引用的事实条目（S* 为门店权威）。
type RuntimeContextFactV2 struct {
	Ref   string `json:"ref"`
	Key   string `json:"key"`
	Value string `json:"value"`
}
