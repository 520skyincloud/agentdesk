package enums

type ServiceSessionStatus string

const (
	ServiceSessionStatusOpen   ServiceSessionStatus = "open"
	ServiceSessionStatusClosed ServiceSessionStatus = "closed"
)

var serviceSessionStatusLabelMap = map[ServiceSessionStatus]string{
	ServiceSessionStatusOpen:   "进行中",
	ServiceSessionStatusClosed: "已关闭",
}

type ResponseSpanStatus string

const (
	ResponseSpanStatusWaiting     ResponseSpanStatus = "waiting"
	ResponseSpanStatusReplied     ResponseSpanStatus = "replied"
	ResponseSpanStatusTransferred ResponseSpanStatus = "transferred"
	ResponseSpanStatusAbandoned   ResponseSpanStatus = "abandoned"
)

var responseSpanStatusLabelMap = map[ResponseSpanStatus]string{
	ResponseSpanStatusWaiting:     "等待回复",
	ResponseSpanStatusReplied:     "已回复",
	ResponseSpanStatusTransferred: "已转派",
	ResponseSpanStatusAbandoned:   "已结束等待",
}

type AnalyticsFactOrigin string

const (
	AnalyticsFactOriginRuntime  AnalyticsFactOrigin = "runtime"
	AnalyticsFactOriginBackfill AnalyticsFactOrigin = "backfill"
	AnalyticsFactOriginRepair   AnalyticsFactOrigin = "repair"
)

var analyticsFactOriginLabelMap = map[AnalyticsFactOrigin]string{
	AnalyticsFactOriginRuntime:  "实时采集",
	AnalyticsFactOriginBackfill: "历史回填",
	AnalyticsFactOriginRepair:   "人工修复",
}

type AnalyticsDataQuality string

const (
	AnalyticsDataQualityExact      AnalyticsDataQuality = "exact"
	AnalyticsDataQualityEstimated  AnalyticsDataQuality = "estimated"
	AnalyticsDataQualityIncomplete AnalyticsDataQuality = "incomplete"
)

var analyticsDataQualityLabelMap = map[AnalyticsDataQuality]string{
	AnalyticsDataQualityExact:      "精确",
	AnalyticsDataQualityEstimated:  "估算",
	AnalyticsDataQualityIncomplete: "不完整",
}

type AgentPresenceStatus string

const (
	AgentPresenceStatusOnline AgentPresenceStatus = "online"
	AgentPresenceStatusIdle   AgentPresenceStatus = "idle"
	AgentPresenceStatusBusy   AgentPresenceStatus = "busy"
	AgentPresenceStatusBreak  AgentPresenceStatus = "break"
)

var agentPresenceStatusLabelMap = map[AgentPresenceStatus]string{
	AgentPresenceStatusOnline: "在线",
	AgentPresenceStatusIdle:   "空闲",
	AgentPresenceStatusBusy:   "忙碌",
	AgentPresenceStatusBreak:  "休息",
}

type QualityRuleType string

const (
	QualityRuleTypeScore      QualityRuleType = "score"
	QualityRuleTypeMetric     QualityRuleType = "metric"
	QualityRuleTypeProhibited QualityRuleType = "prohibited"
)

var qualityRuleTypeLabelMap = map[QualityRuleType]string{
	QualityRuleTypeScore:      "人工评分",
	QualityRuleTypeMetric:     "系统指标",
	QualityRuleTypeProhibited: "禁忌项",
}

type QualityInspectionStatus string

const (
	QualityInspectionStatusDraft     QualityInspectionStatus = "draft"
	QualityInspectionStatusCompleted QualityInspectionStatus = "completed"
)

var qualityInspectionStatusLabelMap = map[QualityInspectionStatus]string{
	QualityInspectionStatusDraft:     "草稿",
	QualityInspectionStatusCompleted: "已完成",
}

type QualityInspectionResult string

const (
	QualityInspectionResultExcellent QualityInspectionResult = "excellent"
	QualityInspectionResultPassed    QualityInspectionResult = "passed"
	QualityInspectionResultFailed    QualityInspectionResult = "failed"
)

var qualityInspectionResultLabelMap = map[QualityInspectionResult]string{
	QualityInspectionResultExcellent: "优秀",
	QualityInspectionResultPassed:    "合格",
	QualityInspectionResultFailed:    "不合格",
}

type QualitySamplingStatus string

const (
	QualitySamplingStatusReady     QualitySamplingStatus = "ready"
	QualitySamplingStatusCompleted QualitySamplingStatus = "completed"
)

var qualitySamplingStatusLabelMap = map[QualitySamplingStatus]string{
	QualitySamplingStatusReady:     "待质检",
	QualitySamplingStatusCompleted: "已完成",
}

type DispatchDecisionStatus string

const (
	DispatchDecisionStatusSelected DispatchDecisionStatus = "selected"
	DispatchDecisionStatusFallback DispatchDecisionStatus = "fallback"
	DispatchDecisionStatusFailed   DispatchDecisionStatus = "failed"
	DispatchDecisionStatusOverride DispatchDecisionStatus = "override"
	DispatchDecisionStatusStale    DispatchDecisionStatus = "stale"
)

var dispatchDecisionStatusLabelMap = map[DispatchDecisionStatus]string{
	DispatchDecisionStatusSelected: "已选择",
	DispatchDecisionStatusFallback: "已降级",
	DispatchDecisionStatusFailed:   "失败",
	DispatchDecisionStatusOverride: "人工覆盖",
	DispatchDecisionStatusStale:    "结果过期",
}

type ConversationEvaluationStatus string

const (
	ConversationEvaluationStatusPending   ConversationEvaluationStatus = "pending"
	ConversationEvaluationStatusSubmitted ConversationEvaluationStatus = "submitted"
	ConversationEvaluationStatusExpired   ConversationEvaluationStatus = "expired"
	ConversationEvaluationStatusCancelled ConversationEvaluationStatus = "cancelled"
)

var conversationEvaluationStatusLabelMap = map[ConversationEvaluationStatus]string{
	ConversationEvaluationStatusPending:   "待评价",
	ConversationEvaluationStatusSubmitted: "已评价",
	ConversationEvaluationStatusExpired:   "已过期",
	ConversationEvaluationStatusCancelled: "已取消",
}
