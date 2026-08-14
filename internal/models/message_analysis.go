package models

import "time"

// MessageAnalysis 是媒体/文本分析的权威持久状态（多模态可靠性计划 7）。
// Payload 只保留兼容投影；Runtime V2 的权威来源是本表最新有效 revision。
// Analysis row 本身就是可 claim 的持久化分析工作（ClaimedBy/Lease/Attempt/NextRetry），
// 不新增平行 MediaJob 表。
type MessageAnalysis struct {
	ID                 int64      `gorm:"primaryKey;autoIncrement"`
	TenantID           int64      `gorm:"type:bigint;not null;default:0;index;uniqueIndex:uk_message_analysis,priority:1"`
	MessageID          int64      `gorm:"type:bigint;not null;default:0;index;uniqueIndex:uk_message_analysis,priority:2"`
	SourceRevision     int        `gorm:"type:int;not null;default:1;uniqueIndex:uk_message_analysis,priority:3"`
	ContentFingerprint string     `gorm:"type:varchar(64);not null;default:'';index"`
	AnalysisStatus     string     `gorm:"type:varchar(20);not null;default:'pending';index;index:idx_message_analysis_due,priority:1"`
	SchemaVersion      string     `gorm:"type:varchar(40);not null;default:'message_analysis.v1'"`
	AnalysisJSON       string     `gorm:"type:text"`
	AnalyzerKind       string     `gorm:"type:varchar(20);not null;default:''"`
	AnalyzerName       string     `gorm:"type:varchar(80);not null;default:''"`
	AnalyzerVersion    string     `gorm:"type:varchar(40);not null;default:''"`
	ErrorCode          string     `gorm:"type:varchar(80);not null;default:'';index"`
	AnalyzedAt         *time.Time `gorm:"type:datetime;index;index:idx_message_analysis_status_at,priority:2"`
	// 分析 Worker 账本字段（多模态契约 3.1.2）：claim/lease/attempt/nextRetry。
	ClaimedBy      string     `gorm:"type:varchar(128);not null;default:'';index"`
	LeaseExpiresAt *time.Time `gorm:"type:datetime;index"`
	AttemptCount   int        `gorm:"type:int;not null;default:0"`
	NextRetryAt    *time.Time `gorm:"type:datetime;index;index:idx_message_analysis_due,priority:2"`
	LastErrorClass string     `gorm:"type:varchar(80);not null;default:'';index"`
	AuditFields
}
