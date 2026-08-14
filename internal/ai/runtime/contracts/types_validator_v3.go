package contracts

// 多模态契约 19：validation_result.v3。Validator 只分类输出和建议恢复阶段，
// 不决定是否转人工。

const ValidationResultV3SchemaVersion = "validation_result.v3"

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
	SchemaVersion string           `json:"schemaVersion"`
	Items         []EvidenceItemV2 `json:"items"`
}

// EvidenceItemV2 携带 Metadata Judge 的最终判定。
type EvidenceItemV2 struct {
	Ref            string   `json:"ref"`
	TaskKey        string   `json:"taskKey"`
	Content        string   `json:"content"`
	Answerability  string   `json:"answerability"` // supporting/restricted/blocked
	TrustLevel     string   `json:"trustLevel"`
	AllowedUses    []string `json:"allowedUses"`
	BlockedReasons []string `json:"blockedReasons"`
}

// ObservationV1 是客户媒体的受限观察（contentRole 决定允许用途）。
type ObservationV1 struct {
	ID          string   `json:"id"`
	ContentRole string   `json:"contentRole"`
	MediaType   string   `json:"mediaType"`
	Summary     string   `json:"summary"`
	AllowedUses []string `json:"allowedUses"`
}

// RuntimeContextSnapshotV2 是权威事实快照。
type RuntimeContextSnapshotV2 struct {
	SchemaVersion string                 `json:"schemaVersion"`
	Facts         []RuntimeContextFactV2 `json:"facts"`
}

// RuntimeContextFactV2 是带引用的事实条目（S* 为门店权威）。
type RuntimeContextFactV2 struct {
	Ref   string `json:"ref"`
	Key   string `json:"key"`
	Value string `json:"value"`
}
