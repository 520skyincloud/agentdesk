package models

import (
	"agent-desk/internal/pkg/enums"
	"time"
)

// ConversationServiceSession is the rebuildable operational fact for one
// conversation session. It never participates in routing or reply decisions.
type ConversationServiceSession struct {
	ID                    int64                           `gorm:"primaryKey;autoIncrement"`
	TenantID              int64                           `gorm:"type:bigint;not null;default:0;index;uniqueIndex:uk_service_session,priority:1"`
	ConversationID        int64                           `gorm:"type:bigint;not null;default:0;index;uniqueIndex:uk_service_session,priority:2"`
	SessionNo             int                             `gorm:"type:int;not null;default:1;index;uniqueIndex:uk_service_session,priority:3"`
	CustomerID            int64                           `gorm:"type:bigint;not null;default:0;index"`
	ChannelID             int64                           `gorm:"type:bigint;not null;default:0;index"`
	StoreID               int64                           `gorm:"type:bigint;not null;default:0;index"`
	WxWorkInstanceID      int64                           `gorm:"type:bigint;not null;default:0;index"`
	ServiceMode           enums.IMConversationServiceMode `gorm:"type:int;not null;default:3;index"`
	Status                enums.ServiceSessionStatus      `gorm:"type:varchar(20);not null;default:'open';index"`
	StartedAt             time.Time                       `gorm:"type:datetime;not null;index"`
	QueueEnteredAt        *time.Time                      `gorm:"type:datetime;index"`
	AssignedAt            *time.Time                      `gorm:"type:datetime;index"`
	FirstHumanReplyAt     *time.Time                      `gorm:"type:datetime;index"`
	LastHumanReplyAt      *time.Time                      `gorm:"type:datetime;index"`
	EndedAt               *time.Time                      `gorm:"type:datetime;index"`
	FirstAssignmentID     int64                           `gorm:"type:bigint;not null;default:0;index"`
	LastAssignmentID      int64                           `gorm:"type:bigint;not null;default:0;index"`
	AssignedTeamID        int64                           `gorm:"type:bigint;not null;default:0;index"`
	AssignedSquadID       int64                           `gorm:"type:bigint;not null;default:0;index"`
	AssignedAgentID       int64                           `gorm:"type:bigint;not null;default:0;index"`
	CustomerMessageCount  int                             `gorm:"type:int;not null;default:0"`
	AIMessageCount        int                             `gorm:"type:int;not null;default:0"`
	HumanMessageCount     int                             `gorm:"type:int;not null;default:0"`
	SystemMessageCount    int                             `gorm:"type:int;not null;default:0"`
	AssignmentCount       int                             `gorm:"type:int;not null;default:0"`
	TransferCount         int                             `gorm:"type:int;not null;default:0"`
	HumanHandled          bool                            `gorm:"not null;default:false;index"`
	AIHandled             bool                            `gorm:"not null;default:false;index"`
	QueueSeconds          int64                           `gorm:"type:bigint;not null;default:0"`
	FirstResponseSeconds  int64                           `gorm:"type:bigint;not null;default:0"`
	TotalHumanWaitSeconds int64                           `gorm:"type:bigint;not null;default:0"`
	LastMessageID         int64                           `gorm:"type:bigint;not null;default:0;index"`
	LastMessageAt         *time.Time                      `gorm:"type:datetime;index"`
	CloseReason           string                          `gorm:"type:varchar(255);not null;default:''"`
	ResolutionCode        string                          `gorm:"type:varchar(50);not null;default:'';index"`
	CategoryCode          string                          `gorm:"type:varchar(50);not null;default:'';index"`
	TagIDsJSON            string                          `gorm:"type:text"`
	SessionSummary        string                          `gorm:"type:text"`
	FactOrigin            enums.AnalyticsFactOrigin       `gorm:"type:varchar(20);not null;default:'runtime';index"`
	DataQuality           enums.AnalyticsDataQuality      `gorm:"type:varchar(20);not null;default:'exact';index"`
	EstimatedFieldsJSON   string                          `gorm:"type:text"`
	AuditFields
}

// ConversationResponseSpan measures one customer message burst until the next
// human agent reply. AI replies never close a span.
type ConversationResponseSpan struct {
	ID                     int64                      `gorm:"primaryKey;autoIncrement"`
	TenantID               int64                      `gorm:"type:bigint;not null;default:0;index;uniqueIndex:uk_response_span_start,priority:1"`
	ConversationID         int64                      `gorm:"type:bigint;not null;default:0;index"`
	SessionNo              int                        `gorm:"type:int;not null;default:1;index"`
	AssignmentID           int64                      `gorm:"type:bigint;not null;default:0;index"`
	TeamID                 int64                      `gorm:"type:bigint;not null;default:0;index"`
	SquadID                int64                      `gorm:"type:bigint;not null;default:0;index"`
	AgentID                int64                      `gorm:"type:bigint;not null;default:0;index"`
	CustomerStartMessageID int64                      `gorm:"type:bigint;not null;default:0;index;uniqueIndex:uk_response_span_start,priority:2"`
	CustomerEndMessageID   int64                      `gorm:"type:bigint;not null;default:0;index"`
	CustomerMessageCount   int                        `gorm:"type:int;not null;default:1"`
	StartedAt              time.Time                  `gorm:"type:datetime;not null;index"`
	RepliedAt              *time.Time                 `gorm:"type:datetime;index"`
	ReplyMessageID         int64                      `gorm:"type:bigint;not null;default:0;index"`
	WaitSeconds            int64                      `gorm:"type:bigint;not null;default:0"`
	Status                 enums.ResponseSpanStatus   `gorm:"type:varchar(20);not null;default:'waiting';index"`
	FactOrigin             enums.AnalyticsFactOrigin  `gorm:"type:varchar(20);not null;default:'runtime';index"`
	DataQuality            enums.AnalyticsDataQuality `gorm:"type:varchar(20);not null;default:'exact';index"`
	EstimatedFieldsJSON    string                     `gorm:"type:text"`
	AuditFields
}

type AgentPresenceSession struct {
	ID              int64                     `gorm:"primaryKey;autoIncrement"`
	TenantID        int64                     `gorm:"type:bigint;not null;default:0;index"`
	UserID          int64                     `gorm:"type:bigint;not null;default:0;index"`
	AgentProfileID  int64                     `gorm:"type:bigint;not null;default:0;index"`
	TeamID          int64                     `gorm:"type:bigint;not null;default:0;index"`
	Status          enums.AgentPresenceStatus `gorm:"type:varchar(20);not null;default:'online';index"`
	Source          string                    `gorm:"type:varchar(40);not null;default:'';index"`
	BreakReason     string                    `gorm:"type:varchar(100);not null;default:'';index"`
	ChangedBy       int64                     `gorm:"type:bigint;not null;default:0;index"`
	StartedAt       time.Time                 `gorm:"type:datetime;not null;index"`
	LastSeenAt      time.Time                 `gorm:"type:datetime;not null;index"`
	EndedAt         *time.Time                `gorm:"type:datetime;index"`
	DurationSeconds int64                     `gorm:"type:bigint;not null;default:0"`
	AuditFields
}

type QualityTemplate struct {
	ID          int64        `gorm:"primaryKey;autoIncrement"`
	TenantID    int64        `gorm:"type:bigint;not null;default:0;index;uniqueIndex:uk_quality_template_version,priority:1"`
	Name        string       `gorm:"type:varchar(120);not null;default:'';index;uniqueIndex:uk_quality_template_version,priority:2"`
	Description string       `gorm:"type:text"`
	TotalScore  int          `gorm:"type:int;not null;default:100"`
	PassScore   int          `gorm:"type:int;not null;default:80"`
	Version     int          `gorm:"type:int;not null;default:1;uniqueIndex:uk_quality_template_version,priority:3"`
	IsDefault   bool         `gorm:"not null;default:false;index"`
	Status      enums.Status `gorm:"type:int;not null;default:0;index"`
	AuditFields
}

type QualityTemplateItem struct {
	ID          int64                 `gorm:"primaryKey;autoIncrement"`
	TenantID    int64                 `gorm:"type:bigint;not null;default:0;index"`
	TemplateID  int64                 `gorm:"type:bigint;not null;default:0;index;uniqueIndex:uk_quality_template_item,priority:1"`
	Code        string                `gorm:"type:varchar(64);not null;default:'';index;uniqueIndex:uk_quality_template_item,priority:2"`
	Name        string                `gorm:"type:varchar(120);not null;default:''"`
	Description string                `gorm:"type:text"`
	RuleType    enums.QualityRuleType `gorm:"type:varchar(20);not null;default:'score';index"`
	MetricCode  string                `gorm:"type:varchar(64);not null;default:'';index"`
	MaxScore    int                   `gorm:"type:int;not null;default:0"`
	Required    bool                  `gorm:"not null;default:true"`
	HardFail    bool                  `gorm:"not null;default:false"`
	SortNo      int                   `gorm:"type:int;not null;default:0;index"`
	Status      enums.Status          `gorm:"type:int;not null;default:0;index"`
	AuditFields
}

// QualityInspection scores only the human replies written during one
// ConversationAssignment segment.
type QualityInspection struct {
	ID             int64                         `gorm:"primaryKey;autoIncrement"`
	TenantID       int64                         `gorm:"type:bigint;not null;default:0;index;uniqueIndex:uk_quality_assignment,priority:1"`
	ConversationID int64                         `gorm:"type:bigint;not null;default:0;index"`
	SessionNo      int                           `gorm:"type:int;not null;default:1;index"`
	AssignmentID   int64                         `gorm:"type:bigint;not null;default:0;index;uniqueIndex:uk_quality_assignment,priority:2"`
	AgentID        int64                         `gorm:"type:bigint;not null;default:0;index"`
	TeamID         int64                         `gorm:"type:bigint;not null;default:0;index"`
	TemplateID     int64                         `gorm:"type:bigint;not null;default:0;index;uniqueIndex:uk_quality_assignment,priority:3"`
	Status         enums.QualityInspectionStatus `gorm:"type:varchar(20);not null;default:'draft';index"`
	TotalScore     int                           `gorm:"type:int;not null;default:0"`
	MaxScore       int                           `gorm:"type:int;not null;default:100"`
	HardFailed     bool                          `gorm:"not null;default:false;index"`
	Result         enums.QualityInspectionResult `gorm:"type:varchar(20);not null;default:'';index"`
	Summary        string                        `gorm:"type:text"`
	InspectedBy    int64                         `gorm:"type:bigint;not null;default:0;index"`
	InspectedAt    *time.Time                    `gorm:"type:datetime;index"`
	AuditFields
}

type QualityInspectionItem struct {
	ID             int64                 `gorm:"primaryKey;autoIncrement"`
	TenantID       int64                 `gorm:"type:bigint;not null;default:0;index"`
	InspectionID   int64                 `gorm:"type:bigint;not null;default:0;index;uniqueIndex:uk_quality_inspection_item,priority:1"`
	TemplateItemID int64                 `gorm:"type:bigint;not null;default:0;index;uniqueIndex:uk_quality_inspection_item,priority:2"`
	ItemCode       string                `gorm:"type:varchar(64);not null;default:'';index"`
	ItemName       string                `gorm:"type:varchar(120);not null;default:''"`
	RuleType       enums.QualityRuleType `gorm:"type:varchar(20);not null;default:'score';index"`
	MaxScore       int                   `gorm:"type:int;not null;default:0"`
	Score          int                   `gorm:"type:int;not null;default:0"`
	Passed         bool                  `gorm:"not null;default:false"`
	HardFailed     bool                  `gorm:"not null;default:false"`
	MetricValue    string                `gorm:"type:varchar(100);not null;default:''"`
	Evidence       string                `gorm:"type:text"`
	MessageIDsJSON string                `gorm:"type:text"`
	Comment        string                `gorm:"type:text"`
	AuditFields
}

type QualitySamplingBatch struct {
	ID           int64                       `gorm:"primaryKey;autoIncrement"`
	TenantID     int64                       `gorm:"type:bigint;not null;default:0;index"`
	Name         string                      `gorm:"type:varchar(120);not null;default:'';index"`
	CriteriaJSON string                      `gorm:"type:text"`
	Seed         string                      `gorm:"type:varchar(64);not null;default:'';index"`
	SampleSize   int                         `gorm:"type:int;not null;default:0"`
	Status       enums.QualitySamplingStatus `gorm:"type:varchar(20);not null;default:'ready';index"`
	CreatedBy    int64                       `gorm:"type:bigint;not null;default:0;index"`
	CompletedAt  *time.Time                  `gorm:"type:datetime;index"`
	AuditFields
}

type QualitySamplingItem struct {
	ID             int64 `gorm:"primaryKey;autoIncrement"`
	TenantID       int64 `gorm:"type:bigint;not null;default:0;index"`
	BatchID        int64 `gorm:"type:bigint;not null;default:0;index;uniqueIndex:uk_quality_sample_item,priority:1"`
	AssignmentID   int64 `gorm:"type:bigint;not null;default:0;index;uniqueIndex:uk_quality_sample_item,priority:2"`
	ConversationID int64 `gorm:"type:bigint;not null;default:0;index"`
	SessionNo      int   `gorm:"type:int;not null;default:1;index"`
	AgentID        int64 `gorm:"type:bigint;not null;default:0;index"`
	InspectionID   int64 `gorm:"type:bigint;not null;default:0;index"`
	AuditFields
}

type DispatchDecisionLog struct {
	ID                    int64                        `gorm:"primaryKey;autoIncrement"`
	TenantID              int64                        `gorm:"type:bigint;not null;default:0;index"`
	DecisionKey           string                       `gorm:"type:varchar(128);not null;default:'';uniqueIndex"`
	ConversationID        int64                        `gorm:"type:bigint;not null;default:0;index"`
	SessionNo             int                          `gorm:"type:int;not null;default:1;index"`
	AssignmentID          int64                        `gorm:"type:bigint;not null;default:0;index"`
	Trigger               string                       `gorm:"type:varchar(40);not null;default:'';index"`
	DecisionMode          string                       `gorm:"type:varchar(30);not null;default:'';index"`
	Status                enums.DispatchDecisionStatus `gorm:"type:varchar(20);not null;default:'';index"`
	CandidateUserIDsJSON  string                       `gorm:"type:text"`
	CandidateSnapshotJSON string                       `gorm:"type:text"`
	InputLastMessageID    int64                        `gorm:"type:bigint;not null;default:0;index"`
	SelectedUserID        int64                        `gorm:"type:bigint;not null;default:0;index"`
	SelectedTeamID        int64                        `gorm:"type:bigint;not null;default:0;index"`
	SelectedSquadID       int64                        `gorm:"type:bigint;not null;default:0;index"`
	DecisionLatencyMillis int64                        `gorm:"type:bigint;not null;default:0"`
	Reason                string                       `gorm:"type:text"`
	FallbackReason        string                       `gorm:"type:text"`
	OperatorID            int64                        `gorm:"type:bigint;not null;default:0;index"`
	DecidedAt             time.Time                    `gorm:"type:datetime;not null;index"`
	AuditFields
}

type ServiceAnalyticsPolicy struct {
	ID                         int64 `gorm:"primaryKey;autoIncrement"`
	TenantID                   int64 `gorm:"type:bigint;not null;default:0;uniqueIndex"`
	QueueTargetSeconds         int   `gorm:"type:int;not null;default:60"`
	FirstResponseTargetSeconds int   `gorm:"type:int;not null;default:180"`
	ResponseTargetSeconds      int   `gorm:"type:int;not null;default:300"`
	RepeatConsultationHours    int   `gorm:"type:int;not null;default:24"`
	SatisfactionThreshold      int   `gorm:"type:int;not null;default:4"`
	EvaluationExpiryHours      int   `gorm:"type:int;not null;default:72"`
	DefaultSampleSize          int   `gorm:"type:int;not null;default:20"`
	AuditFields
}

type ConversationEvaluation struct {
	ID             int64                              `gorm:"primaryKey;autoIncrement"`
	TenantID       int64                              `gorm:"type:bigint;not null;default:0;index"`
	ConversationID int64                              `gorm:"type:bigint;not null;default:0;index"`
	SessionNo      int                                `gorm:"type:int;not null;default:1;index"`
	AssignmentID   int64                              `gorm:"type:bigint;not null;default:0;index"`
	CustomerID     int64                              `gorm:"type:bigint;not null;default:0;index"`
	Status         enums.ConversationEvaluationStatus `gorm:"type:varchar(20);not null;default:'pending';index"`
	InviteChannel  string                             `gorm:"type:varchar(30);not null;default:'';index"`
	TokenHash      string                             `gorm:"type:varchar(64);not null;default:'';uniqueIndex"`
	InvitedBy      int64                              `gorm:"type:bigint;not null;default:0;index"`
	InvitedAt      time.Time                          `gorm:"type:datetime;not null;index"`
	ExpiresAt      time.Time                          `gorm:"type:datetime;not null;index"`
	SubmittedAt    *time.Time                         `gorm:"type:datetime;index"`
	Rating         int                                `gorm:"type:int;not null;default:0;index"`
	TagCodesJSON   string                             `gorm:"type:text"`
	Comment        string                             `gorm:"type:text"`
	AuditFields
}

type ReportViewPreset struct {
	ID          int64        `gorm:"primaryKey;autoIncrement"`
	TenantID    int64        `gorm:"type:bigint;not null;default:0;index;uniqueIndex:uk_report_view_preset,priority:1"`
	UserID      int64        `gorm:"type:bigint;not null;default:0;index;uniqueIndex:uk_report_view_preset,priority:2"`
	PageCode    string       `gorm:"type:varchar(50);not null;default:'';index;uniqueIndex:uk_report_view_preset,priority:3"`
	Name        string       `gorm:"type:varchar(100);not null;default:'';uniqueIndex:uk_report_view_preset,priority:4"`
	FiltersJSON string       `gorm:"type:text"`
	ColumnsJSON string       `gorm:"type:text"`
	SortJSON    string       `gorm:"type:text"`
	IsDefault   bool         `gorm:"not null;default:false;index"`
	Status      enums.Status `gorm:"type:int;not null;default:0;index"`
	AuditFields
}
