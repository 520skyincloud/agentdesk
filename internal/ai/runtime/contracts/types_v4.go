package contracts

// 多模态契约 14/15：reply_plan.v4 与 reply_output.v3 的 Go 结构。
// 模型输出只包含 groupKey/taskKeys/content；Evidence/Action 引用由服务端
// ResolveReplyPart 解析，不再要求模型回显。

const (
	ReplyPlanV4SchemaVersion   = SchemaReplyPlanV4
	ReplyOutputV3SchemaVersion = SchemaReplyOutputV3
)

// ReplyPlanV4 是 AnswerGroup 增强后的最终回复计划。
type ReplyPlanV4 struct {
	SchemaVersion     string             `json:"schemaVersion"`
	TurnVersion       int                `json:"turnVersion"`
	PlanFingerprint   string             `json:"planFingerprint"`
	ShouldGenerate    bool               `json:"shouldGenerate"`
	Tasks             []ReplyPlanTaskV4  `json:"tasks"`
	ReplyGroups       []ReplyPlanGroupV4 `json:"replyGroups"`
	GlobalConstraints ReplyPlanGlobalV4  `json:"globalConstraints"`
}

// ReplyPlanTaskV4 是 Plan 中单个 Task 的确定性投影。
type ReplyPlanTaskV4 struct {
	TaskKey          string               `json:"taskKey"`
	Sequence         int                  `json:"sequence"`
	Intent           string               `json:"intent"`
	SubIntent        string               `json:"subIntent"`
	AnswerGroupKey   string               `json:"answerGroupKey"`
	Objective        string               `json:"objective"`
	OutputMode       string               `json:"outputMode"`
	Knowledge        ReplyPlanKnowledgeV4 `json:"knowledge"`
	EvidenceRefs     []string             `json:"evidenceRefs"`
	RequiredFactRefs []string             `json:"requiredFactRefs"`
	ActionRefs       []string             `json:"actionRefs"`
	ResourcePolicy   ReplyResourcePolicy  `json:"resourcePolicy"`
	Constraints      []string             `json:"constraints"`
}

// ReplyPlanKnowledgeV4 是知识策略投影。
type ReplyPlanKnowledgeV4 struct {
	Policy     string `json:"policy"`
	Status     string `json:"status"`
	ReasonCode string `json:"reasonCode"`
}

// ReplyResourcePolicy 是 Task 级资源允许策略（不是 ResourceEligibility 账本）。
type ReplyResourcePolicy struct {
	Mode            string   `json:"mode"`
	AllowedTypes    []string `json:"allowedTypes"`
	AllowedPurposes []string `json:"allowedPurposes"`
}

// ReplyPlanGroupV4 是 Plan 内的 AnswerGroup。
type ReplyPlanGroupV4 struct {
	GroupKey   string   `json:"groupKey"`
	TaskKeys   []string `json:"taskKeys"`
	Sequence   int      `json:"sequence"`
	OutputMode string   `json:"outputMode"`
	MaxParts   int      `json:"maxParts"`
	Required   bool     `json:"required"`
}

// ReplyPlanGlobalV4 是全批输出约束。
type ReplyPlanGlobalV4 struct {
	MaxReplyParts       int      `json:"maxReplyParts"`
	MaxQuestionsPerPart int      `json:"maxQuestionsPerPart"`
	ForbiddenClaims     []string `json:"forbiddenClaims"`
}

// ReplyOutputV3 是模型唯一允许的输出形态。
type ReplyOutputV3 struct {
	SchemaVersion string        `json:"schemaVersion"`
	Parts         []ReplyPartV3 `json:"parts"`
}

// ReplyPartV3 模型只回显服务端下发的组/任务引用与自然表达内容。
type ReplyPartV3 struct {
	GroupKey string   `json:"groupKey"`
	TaskKeys []string `json:"taskKeys"`
	Content  string   `json:"content"`
}
