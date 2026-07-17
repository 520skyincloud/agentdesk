package request

type QualityTemplateItemRequest struct {
	ID          int64  `json:"id"`
	Code        string `json:"code"`
	Name        string `json:"name" validate:"required"`
	Description string `json:"description"`
	RuleType    string `json:"ruleType" validate:"omitempty,oneof=score metric prohibited"`
	MetricCode  string `json:"metricCode"`
	MaxScore    int    `json:"maxScore" validate:"gte=0"`
	Required    bool   `json:"required"`
	HardFail    bool   `json:"hardFail"`
	SortNo      int    `json:"sortNo"`
}

type SaveQualityTemplateRequest struct {
	ID          int64                        `json:"id"`
	Name        string                       `json:"name" validate:"required"`
	Description string                       `json:"description"`
	PassScore   int                          `json:"passScore" validate:"gte=0"`
	IsDefault   bool                         `json:"isDefault"`
	Items       []QualityTemplateItemRequest `json:"items" validate:"min=1"`
}

type QualityInspectionItemRequest struct {
	TemplateItemID int64   `json:"templateItemId" validate:"required"`
	Score          int     `json:"score" validate:"gte=0"`
	Violated       bool    `json:"violated"`
	Evidence       string  `json:"evidence"`
	MessageIDs     []int64 `json:"messageIds"`
	Comment        string  `json:"comment"`
}

type SaveQualityInspectionRequest struct {
	ID           int64                          `json:"id"`
	AssignmentID int64                          `json:"assignmentId" validate:"required"`
	TemplateID   int64                          `json:"templateId" validate:"required"`
	Status       string                         `json:"status" validate:"required,oneof=draft completed"`
	Summary      string                         `json:"summary"`
	Items        []QualityInspectionItemRequest `json:"items" validate:"min=1"`
}

type SaveServiceAnalyticsPolicyRequest struct {
	QueueTargetSeconds         int `json:"queueTargetSeconds" validate:"gte=1"`
	FirstResponseTargetSeconds int `json:"firstResponseTargetSeconds" validate:"gte=1"`
	ResponseTargetSeconds      int `json:"responseTargetSeconds" validate:"gte=1"`
	RepeatConsultationHours    int `json:"repeatConsultationHours" validate:"gte=1,lte=168"`
	SatisfactionThreshold      int `json:"satisfactionThreshold" validate:"gte=1,lte=5"`
	EvaluationExpiryHours      int `json:"evaluationExpiryHours" validate:"gte=1,lte=720"`
	DefaultSampleSize          int `json:"defaultSampleSize" validate:"gte=1,lte=1000"`
}

type UpdateServiceSessionAnnotationRequest struct {
	ID             int64   `json:"id" validate:"required"`
	ResolutionCode string  `json:"resolutionCode"`
	CategoryCode   string  `json:"categoryCode"`
	SessionSummary string  `json:"sessionSummary"`
	TagIDs         []int64 `json:"tagIds"`
}

type CreateQualitySamplingRequest struct {
	Name       string `json:"name" validate:"required"`
	TeamID     int64  `json:"teamId"`
	AgentID    int64  `json:"agentId"`
	StartAt    string `json:"startAt" validate:"required"`
	EndAt      string `json:"endAt" validate:"required"`
	SampleSize int    `json:"sampleSize" validate:"gte=1,lte=1000"`
}

type SaveReportViewPresetRequest struct {
	ID          int64  `json:"id"`
	PageCode    string `json:"pageCode" validate:"required"`
	Name        string `json:"name" validate:"required"`
	FiltersJSON string `json:"filtersJson"`
	ColumnsJSON string `json:"columnsJson"`
	SortJSON    string `json:"sortJson"`
	IsDefault   bool   `json:"isDefault"`
}

type UpdateAgentPresenceRequest struct {
	Status      string `json:"status" validate:"required,oneof=online idle busy break"`
	BreakReason string `json:"breakReason"`
}

type InviteConversationEvaluationRequest struct {
	ServiceSessionID int64 `json:"serviceSessionId" validate:"required"`
	AssignmentID     int64 `json:"assignmentId"`
}

type SubmitConversationEvaluationRequest struct {
	Token    string   `json:"token" validate:"required"`
	Rating   int      `json:"rating" validate:"gte=1,lte=5"`
	TagCodes []string `json:"tagCodes"`
	Comment  string   `json:"comment"`
}
