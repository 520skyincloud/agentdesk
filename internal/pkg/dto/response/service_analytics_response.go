package response

type ServiceAnalyticsSummaryResponse struct {
	SessionCount                int64   `json:"sessionCount"`
	UniqueCustomerCount         int64   `json:"uniqueCustomerCount"`
	ClosedSessionCount          int64   `json:"closedSessionCount"`
	HumanQueueCount             int64   `json:"humanQueueCount"`
	AssignedCount               int64   `json:"assignedCount"`
	HumanRepliedCount           int64   `json:"humanRepliedCount"`
	UnansweredCount             int64   `json:"unansweredCount"`
	QueueFailureCount           int64   `json:"queueFailureCount"`
	TransferSessionCount        int64   `json:"transferSessionCount"`
	RepeatConsultationCount     int64   `json:"repeatConsultationCount"`
	TotalMessageCount           int64   `json:"totalMessageCount"`
	CustomerMessageCount        int64   `json:"customerMessageCount"`
	AIMessageCount              int64   `json:"aiMessageCount"`
	HumanMessageCount           int64   `json:"humanMessageCount"`
	AssignmentAccessRate        float64 `json:"assignmentAccessRate"`
	EffectiveAccessRate         float64 `json:"effectiveAccessRate"`
	TransferRate                float64 `json:"transferRate"`
	RepeatConsultationRate      float64 `json:"repeatConsultationRate"`
	AverageQueueSeconds         float64 `json:"averageQueueSeconds"`
	P50QueueSeconds             float64 `json:"p50QueueSeconds"`
	P90QueueSeconds             float64 `json:"p90QueueSeconds"`
	AverageFirstReplySeconds    float64 `json:"averageFirstReplySeconds"`
	P50FirstReplySeconds        float64 `json:"p50FirstReplySeconds"`
	P90FirstReplySeconds        float64 `json:"p90FirstReplySeconds"`
	AverageResponseSeconds      float64 `json:"averageResponseSeconds"`
	P50ResponseSeconds          float64 `json:"p50ResponseSeconds"`
	P90ResponseSeconds          float64 `json:"p90ResponseSeconds"`
	AverageHumanWaitSeconds     float64 `json:"averageHumanWaitSeconds"`
	P50HumanWaitSeconds         float64 `json:"p50HumanWaitSeconds"`
	P90HumanWaitSeconds         float64 `json:"p90HumanWaitSeconds"`
	AverageSessionSeconds       float64 `json:"averageSessionSeconds"`
	P50SessionSeconds           float64 `json:"p50SessionSeconds"`
	P90SessionSeconds           float64 `json:"p90SessionSeconds"`
	AverageMessagesPerSession   float64 `json:"averageMessagesPerSession"`
	QueueSLARate                float64 `json:"queueSlaRate"`
	FirstReplySLARate           float64 `json:"firstReplySlaRate"`
	ResponseSLARate             float64 `json:"responseSlaRate"`
	QualityInspectableCount     int64   `json:"qualityInspectableCount"`
	QualityInspectionCount      int64   `json:"qualityInspectionCount"`
	QualityPendingCount         int64   `json:"qualityPendingCount"`
	QualityPassedCount          int64   `json:"qualityPassedCount"`
	QualityFailedCount          int64   `json:"qualityFailedCount"`
	QualityCoverageRate         float64 `json:"qualityCoverageRate"`
	QualityPassRate             float64 `json:"qualityPassRate"`
	AverageQualityScore         float64 `json:"averageQualityScore"`
	EvaluationInviteCount       int64   `json:"evaluationInviteCount"`
	EvaluationSubmittedCount    int64   `json:"evaluationSubmittedCount"`
	SatisfiedCount              int64   `json:"satisfiedCount"`
	EvaluationParticipationRate float64 `json:"evaluationParticipationRate"`
	SatisfactionRate            float64 `json:"satisfactionRate"`
	AverageSatisfaction         float64 `json:"averageSatisfaction"`
	ExactSessionCount           int64   `json:"exactSessionCount"`
	EstimatedSessionCount       int64   `json:"estimatedSessionCount"`
	IncompleteSessionCount      int64   `json:"incompleteSessionCount"`
}

type ServiceAnalyticsTrendResponse struct {
	Date              string  `json:"date"`
	Sessions          int64   `json:"sessions"`
	HumanQueues       int64   `json:"humanQueues"`
	HumanReplies      int64   `json:"humanReplies"`
	Messages          int64   `json:"messages"`
	AverageQueue      float64 `json:"averageQueue"`
	AverageFirstReply float64 `json:"averageFirstReply"`
	AverageResponse   float64 `json:"averageResponse"`
	AverageSession    float64 `json:"averageSession"`
}

type ServiceAnalyticsAgentResponse struct {
	AgentID                     int64    `json:"agentId"`
	AgentName                   string   `json:"agentName"`
	TeamID                      int64    `json:"teamId"`
	TeamName                    string   `json:"teamName"`
	SquadNames                  []string `json:"squadNames"`
	CurrentStatus               string   `json:"currentStatus"`
	CurrentActiveCount          int64    `json:"currentActiveCount"`
	MaxConcurrentCount          int      `json:"maxConcurrentCount"`
	AssignedCount               int64    `json:"assignedCount"`
	RepliedCount                int64    `json:"repliedCount"`
	UnansweredCount             int64    `json:"unansweredCount"`
	HumanMessageCount           int64    `json:"humanMessageCount"`
	ResponseCount               int64    `json:"responseCount"`
	ServiceSeconds              int64    `json:"serviceSeconds"`
	AverageFirstReplySeconds    float64  `json:"averageFirstReplySeconds"`
	P50FirstReplySeconds        float64  `json:"p50FirstReplySeconds"`
	P90FirstReplySeconds        float64  `json:"p90FirstReplySeconds"`
	AverageResponseSeconds      float64  `json:"averageResponseSeconds"`
	P50ResponseSeconds          float64  `json:"p50ResponseSeconds"`
	P90ResponseSeconds          float64  `json:"p90ResponseSeconds"`
	ResponseSLARate             float64  `json:"responseSlaRate"`
	OnlineSeconds               int64    `json:"onlineSeconds"`
	IdleSeconds                 int64    `json:"idleSeconds"`
	BusySeconds                 int64    `json:"busySeconds"`
	BreakSeconds                int64    `json:"breakSeconds"`
	FirstOnlineAt               string   `json:"firstOnlineAt,omitempty"`
	LastOnlineAt                string   `json:"lastOnlineAt,omitempty"`
	UtilizationRate             float64  `json:"utilizationRate"`
	QualityInspectableCount     int64    `json:"qualityInspectableCount"`
	QualityInspectionCount      int64    `json:"qualityInspectionCount"`
	QualityPendingCount         int64    `json:"qualityPendingCount"`
	QualityPassedCount          int64    `json:"qualityPassedCount"`
	QualityFailedCount          int64    `json:"qualityFailedCount"`
	QualityPassRate             float64  `json:"qualityPassRate"`
	AverageQualityScore         float64  `json:"averageQualityScore"`
	EvaluationInviteCount       int64    `json:"evaluationInviteCount"`
	EvaluationSubmittedCount    int64    `json:"evaluationSubmittedCount"`
	SatisfiedCount              int64    `json:"satisfiedCount"`
	EvaluationParticipationRate float64  `json:"evaluationParticipationRate"`
	SatisfactionRate            float64  `json:"satisfactionRate"`
	AverageSatisfaction         float64  `json:"averageSatisfaction"`
}

type ServiceAnalyticsSourceResponse struct {
	StoreID                     int64   `json:"storeId"`
	StoreName                   string  `json:"storeName"`
	WxWorkInstanceID            int64   `json:"wxWorkInstanceId"`
	WxWorkEmployeeName          string  `json:"wxWorkEmployeeName"`
	SessionCount                int64   `json:"sessionCount"`
	HumanQueueCount             int64   `json:"humanQueueCount"`
	HumanRepliedCount           int64   `json:"humanRepliedCount"`
	MessageCount                int64   `json:"messageCount"`
	AverageFirstReply           float64 `json:"averageFirstReply"`
	EffectiveAccessRate         float64 `json:"effectiveAccessRate"`
	QualityInspectableCount     int64   `json:"qualityInspectableCount"`
	QualityInspectionCount      int64   `json:"qualityInspectionCount"`
	QualityPassedCount          int64   `json:"qualityPassedCount"`
	QualityCoverageRate         float64 `json:"qualityCoverageRate"`
	QualityPassRate             float64 `json:"qualityPassRate"`
	AverageQualityScore         float64 `json:"averageQualityScore"`
	EvaluationInviteCount       int64   `json:"evaluationInviteCount"`
	EvaluationSubmittedCount    int64   `json:"evaluationSubmittedCount"`
	SatisfiedCount              int64   `json:"satisfiedCount"`
	EvaluationParticipationRate float64 `json:"evaluationParticipationRate"`
	SatisfactionRate            float64 `json:"satisfactionRate"`
	AverageSatisfaction         float64 `json:"averageSatisfaction"`
}

type ServiceAnalyticsRealtimeResponse struct {
	OpenSessionCount              int64   `json:"openSessionCount"`
	AIActiveCount                 int64   `json:"aiActiveCount"`
	QueueingCount                 int64   `json:"queueingCount"`
	AssignedActiveCount           int64   `json:"assignedActiveCount"`
	WaitingReplyCount             int64   `json:"waitingReplyCount"`
	LongestQueueSeconds           int64   `json:"longestQueueSeconds"`
	QueueSLAAlertCount            int64   `json:"queueSlaAlertCount"`
	OnlineAgentCount              int64   `json:"onlineAgentCount"`
	IdleAgentCount                int64   `json:"idleAgentCount"`
	BusyAgentCount                int64   `json:"busyAgentCount"`
	BreakAgentCount               int64   `json:"breakAgentCount"`
	OfflineAgentCount             int64   `json:"offlineAgentCount"`
	AvailableCapacity             int64   `json:"availableCapacity"`
	TodaySessionCount             int64   `json:"todaySessionCount"`
	TodayQueueCount               int64   `json:"todayQueueCount"`
	TodayAssignedCount            int64   `json:"todayAssignedCount"`
	TodayHumanRepliedCount        int64   `json:"todayHumanRepliedCount"`
	TodayTransferCount            int64   `json:"todayTransferCount"`
	TodayQueueFailureCount        int64   `json:"todayQueueFailureCount"`
	TodayMessageCount             int64   `json:"todayMessageCount"`
	TodayAverageQueueSeconds      float64 `json:"todayAverageQueueSeconds"`
	TodayAverageFirstReplySeconds float64 `json:"todayAverageFirstReplySeconds"`
}

type ServiceAnalyticsDistributionResponse struct {
	Key   string  `json:"key"`
	Label string  `json:"label"`
	Count int64   `json:"count"`
	Rate  float64 `json:"rate"`
}

type ServiceAnalyticsDimensionItemResponse struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	ParentID int64  `json:"parentId"`
}

type ServiceAnalyticsDimensionsResponse struct {
	Teams           []ServiceAnalyticsDimensionItemResponse `json:"teams"`
	Squads          []ServiceAnalyticsDimensionItemResponse `json:"squads"`
	Agents          []ServiceAnalyticsDimensionItemResponse `json:"agents"`
	Channels        []ServiceAnalyticsDimensionItemResponse `json:"channels"`
	Stores          []ServiceAnalyticsDimensionItemResponse `json:"stores"`
	WxWorkInstances []ServiceAnalyticsDimensionItemResponse `json:"wxWorkInstances"`
}

type ServiceAnalyticsDispatchResponse struct {
	DecisionCount                int64   `json:"decisionCount"`
	SelectedCount                int64   `json:"selectedCount"`
	AutoCount                    int64   `json:"autoCount"`
	ManualCount                  int64   `json:"manualCount"`
	RuleCount                    int64   `json:"ruleCount"`
	ModelCount                   int64   `json:"modelCount"`
	HybridCount                  int64   `json:"hybridCount"`
	FallbackCount                int64   `json:"fallbackCount"`
	FailedCount                  int64   `json:"failedCount"`
	StaleCount                   int64   `json:"staleCount"`
	OverrideCount                int64   `json:"overrideCount"`
	TransferCount                int64   `json:"transferCount"`
	AutoRate                     float64 `json:"autoRate"`
	AverageDecisionLatencyMillis float64 `json:"averageDecisionLatencyMillis"`
}

type ServiceAnalyticsOverviewResponse struct {
	StartAt                     string                                 `json:"startAt"`
	EndAt                       string                                 `json:"endAt"`
	GeneratedAt                 string                                 `json:"generatedAt"`
	Summary                     ServiceAnalyticsSummaryResponse        `json:"summary"`
	Realtime                    ServiceAnalyticsRealtimeResponse       `json:"realtime"`
	Trend                       []ServiceAnalyticsTrendResponse        `json:"trend"`
	FirstReplyDistribution      []ServiceAnalyticsDistributionResponse `json:"firstReplyDistribution"`
	ResponseDistribution        []ServiceAnalyticsDistributionResponse `json:"responseDistribution"`
	SessionDurationDistribution []ServiceAnalyticsDistributionResponse `json:"sessionDurationDistribution"`
	Agents                      []ServiceAnalyticsAgentResponse        `json:"agents"`
	Sources                     []ServiceAnalyticsSourceResponse       `json:"sources"`
	Dispatch                    ServiceAnalyticsDispatchResponse       `json:"dispatch"`
}

type ServiceSessionResponse struct {
	ID                    int64    `json:"id"`
	ConversationID        int64    `json:"conversationId"`
	SessionNo             int      `json:"sessionNo"`
	CustomerID            int64    `json:"customerId"`
	CustomerName          string   `json:"customerName"`
	ChannelID             int64    `json:"channelId"`
	ChannelName           string   `json:"channelName"`
	StoreID               int64    `json:"storeId"`
	StoreName             string   `json:"storeName"`
	WxWorkInstanceID      int64    `json:"wxWorkInstanceId"`
	WxWorkEmployeeName    string   `json:"wxWorkEmployeeName"`
	Status                string   `json:"status"`
	StartedAt             string   `json:"startedAt"`
	QueueEnteredAt        string   `json:"queueEnteredAt,omitempty"`
	AssignedAt            string   `json:"assignedAt,omitempty"`
	FirstHumanReplyAt     string   `json:"firstHumanReplyAt,omitempty"`
	EndedAt               string   `json:"endedAt,omitempty"`
	AssignedTeamID        int64    `json:"assignedTeamId"`
	AssignedTeamName      string   `json:"assignedTeamName"`
	AssignedAgentID       int64    `json:"assignedAgentId"`
	AssignedAgentName     string   `json:"assignedAgentName"`
	CustomerMessageCount  int      `json:"customerMessageCount"`
	AIMessageCount        int      `json:"aiMessageCount"`
	HumanMessageCount     int      `json:"humanMessageCount"`
	AssignmentCount       int      `json:"assignmentCount"`
	TransferCount         int      `json:"transferCount"`
	QueueSeconds          int64    `json:"queueSeconds"`
	FirstResponseSeconds  int64    `json:"firstResponseSeconds"`
	TotalHumanWaitSeconds int64    `json:"totalHumanWaitSeconds"`
	CloseReason           string   `json:"closeReason"`
	LastMessageAt         string   `json:"lastMessageAt,omitempty"`
	ResolutionCode        string   `json:"resolutionCode"`
	CategoryCode          string   `json:"categoryCode"`
	TagIDs                []int64  `json:"tagIds"`
	SessionSummary        string   `json:"sessionSummary"`
	FactOrigin            string   `json:"factOrigin"`
	DataQuality           string   `json:"dataQuality"`
	EstimatedFields       []string `json:"estimatedFields"`
}

type QualityTemplateItemResponse struct {
	ID          int64  `json:"id"`
	Code        string `json:"code"`
	Name        string `json:"name"`
	Description string `json:"description"`
	RuleType    string `json:"ruleType"`
	MetricCode  string `json:"metricCode"`
	MaxScore    int    `json:"maxScore"`
	Required    bool   `json:"required"`
	HardFail    bool   `json:"hardFail"`
	SortNo      int    `json:"sortNo"`
}

type QualityTemplateResponse struct {
	ID          int64                         `json:"id"`
	Name        string                        `json:"name"`
	Description string                        `json:"description"`
	TotalScore  int                           `json:"totalScore"`
	PassScore   int                           `json:"passScore"`
	Version     int                           `json:"version"`
	IsDefault   bool                          `json:"isDefault"`
	Items       []QualityTemplateItemResponse `json:"items"`
}

type QualityInspectionItemResponse struct {
	TemplateItemID int64   `json:"templateItemId"`
	ItemCode       string  `json:"itemCode"`
	ItemName       string  `json:"itemName"`
	RuleType       string  `json:"ruleType"`
	MaxScore       int     `json:"maxScore"`
	Score          int     `json:"score"`
	Passed         bool    `json:"passed"`
	HardFailed     bool    `json:"hardFailed"`
	MetricValue    string  `json:"metricValue"`
	Evidence       string  `json:"evidence"`
	MessageIDs     []int64 `json:"messageIds"`
	Comment        string  `json:"comment"`
}

type QualityInspectionResponse struct {
	ID             int64                           `json:"id"`
	ConversationID int64                           `json:"conversationId"`
	SessionNo      int                             `json:"sessionNo"`
	AssignmentID   int64                           `json:"assignmentId"`
	AgentID        int64                           `json:"agentId"`
	AgentName      string                          `json:"agentName"`
	TeamID         int64                           `json:"teamId"`
	TeamName       string                          `json:"teamName"`
	TemplateID     int64                           `json:"templateId"`
	Status         string                          `json:"status"`
	TotalScore     int                             `json:"totalScore"`
	MaxScore       int                             `json:"maxScore"`
	Result         string                          `json:"result"`
	HardFailed     bool                            `json:"hardFailed"`
	Summary        string                          `json:"summary"`
	InspectedBy    int64                           `json:"inspectedBy"`
	InspectedAt    string                          `json:"inspectedAt,omitempty"`
	Items          []QualityInspectionItemResponse `json:"items"`
}

type QualityPoolEntryResponse struct {
	AssignmentID       int64                      `json:"assignmentId"`
	ConversationID     int64                      `json:"conversationId"`
	SessionNo          int                        `json:"sessionNo"`
	CustomerName       string                     `json:"customerName"`
	AgentID            int64                      `json:"agentId"`
	AgentName          string                     `json:"agentName"`
	TeamID             int64                      `json:"teamId"`
	TeamName           string                     `json:"teamName"`
	StoreName          string                     `json:"storeName"`
	WxWorkEmployeeName string                     `json:"wxWorkEmployeeName"`
	AssignedAt         string                     `json:"assignedAt"`
	FinishedAt         string                     `json:"finishedAt,omitempty"`
	HumanReplyCount    int64                      `json:"humanReplyCount"`
	Inspection         *QualityInspectionResponse `json:"inspection,omitempty"`
}

type ServiceAnalyticsPolicyResponse struct {
	QueueTargetSeconds         int `json:"queueTargetSeconds"`
	FirstResponseTargetSeconds int `json:"firstResponseTargetSeconds"`
	ResponseTargetSeconds      int `json:"responseTargetSeconds"`
	RepeatConsultationHours    int `json:"repeatConsultationHours"`
	SatisfactionThreshold      int `json:"satisfactionThreshold"`
	EvaluationExpiryHours      int `json:"evaluationExpiryHours"`
	DefaultSampleSize          int `json:"defaultSampleSize"`
}

type QualitySamplingItemResponse struct {
	AssignmentID   int64 `json:"assignmentId"`
	ConversationID int64 `json:"conversationId"`
	SessionNo      int   `json:"sessionNo"`
	AgentID        int64 `json:"agentId"`
	InspectionID   int64 `json:"inspectionId"`
}

type QualitySamplingBatchResponse struct {
	ID           int64                         `json:"id"`
	Name         string                        `json:"name"`
	CriteriaJSON string                        `json:"criteriaJson"`
	Seed         string                        `json:"seed"`
	SampleSize   int                           `json:"sampleSize"`
	Status       string                        `json:"status"`
	CreatedBy    int64                         `json:"createdBy"`
	CreatedAt    string                        `json:"createdAt"`
	CompletedAt  string                        `json:"completedAt,omitempty"`
	Items        []QualitySamplingItemResponse `json:"items"`
}

type ReportViewPresetResponse struct {
	ID          int64  `json:"id"`
	PageCode    string `json:"pageCode"`
	Name        string `json:"name"`
	FiltersJSON string `json:"filtersJson"`
	ColumnsJSON string `json:"columnsJson"`
	SortJSON    string `json:"sortJson"`
	IsDefault   bool   `json:"isDefault"`
}

type AgentPresenceResponse struct {
	Status      string `json:"status"`
	BreakReason string `json:"breakReason"`
	StartedAt   string `json:"startedAt,omitempty"`
	LastSeenAt  string `json:"lastSeenAt,omitempty"`
}

type ConversationEvaluationResponse struct {
	ID             int64    `json:"id"`
	ConversationID int64    `json:"conversationId"`
	SessionNo      int      `json:"sessionNo"`
	AssignmentID   int64    `json:"assignmentId"`
	CustomerID     int64    `json:"customerId"`
	Status         string   `json:"status"`
	InviteChannel  string   `json:"inviteChannel"`
	InvitedAt      string   `json:"invitedAt"`
	ExpiresAt      string   `json:"expiresAt"`
	SubmittedAt    string   `json:"submittedAt,omitempty"`
	Rating         int      `json:"rating"`
	TagCodes       []string `json:"tagCodes"`
	Comment        string   `json:"comment"`
}

type ConversationEvaluationInviteResponse struct {
	Evaluation ConversationEvaluationResponse `json:"evaluation"`
	Path       string                         `json:"path"`
}

type PublicConversationEvaluationResponse struct {
	Status      string `json:"status"`
	CompanyName string `json:"companyName"`
	ExpiresAt   string `json:"expiresAt"`
	SubmittedAt string `json:"submittedAt,omitempty"`
	Rating      int    `json:"rating"`
}
