package models

import "time"

type MessageAnalysis struct {
	ID                 int64      `gorm:"primaryKey;autoIncrement"`
	TenantID           int64      `gorm:"type:bigint;not null;default:0;index;uniqueIndex:uk_message_analysis,priority:1"`
	MessageID          int64      `gorm:"type:bigint;not null;default:0;index;uniqueIndex:uk_message_analysis,priority:2"`
	SourceRevision     int        `gorm:"type:int;not null;default:1;uniqueIndex:uk_message_analysis,priority:3"`
	ContentFingerprint string     `gorm:"type:varchar(64);not null;default:'';index"`
	AnalysisStatus     string     `gorm:"type:varchar(20);not null;default:'pending';index;index:idx_message_analysis_status_at,priority:1"`
	SchemaVersion      string     `gorm:"type:varchar(40);not null;default:'message_analysis.v1'"`
	AnalysisJSON       string     `gorm:"type:text"`
	AnalyzerKind       string     `gorm:"type:varchar(20);not null;default:''"`
	AnalyzerName       string     `gorm:"type:varchar(80);not null;default:''"`
	AnalyzerVersion    string     `gorm:"type:varchar(40);not null;default:''"`
	ErrorCode          string     `gorm:"type:varchar(80);not null;default:'';index"`
	AnalyzedAt         *time.Time `gorm:"type:datetime;index;index:idx_message_analysis_status_at,priority:2"`
	AuditFields
}
