package models

import "time"

// KnowledgeEvidenceMetadata 是知识证据质量侧车表（分层隔离计划 P4）。
// FastGPT 检索命中只表示相似；本表按 SourceRecordID 关联，记录该条知识的
// 来源类别、内容类型、可信等级、时效与资源用途，供 Evidence Judge 判定
// 是否可作为客户可见答案。知识正文仍在 FastGPT，本表只存元数据。
type KnowledgeEvidenceMetadata struct {
	ID              int64  `gorm:"primaryKey;autoIncrement"`
	TenantID        int64  `gorm:"type:bigint;not null;default:0;index;uniqueIndex:uk_knowledge_evidence_metadata,priority:1"`
	StoreID         int64  `gorm:"type:bigint;not null;default:0;index;uniqueIndex:uk_knowledge_evidence_metadata,priority:2"`
	KnowledgeBaseID int64  `gorm:"type:bigint;not null;default:0;index;uniqueIndex:uk_knowledge_evidence_metadata,priority:3"`
	SourceRecordID  string `gorm:"type:varchar(255);not null;default:'';index;uniqueIndex:uk_knowledge_evidence_metadata,priority:4"`
	// SourceClass: store_authored / imported_faq / derived_qa / customer_content / unknown
	SourceClass string `gorm:"type:varchar(40);not null;default:'unknown';index"`
	// FactScope: store / nearby / general
	FactScope string `gorm:"type:varchar(30);not null;default:'store';index"`
	// ClaimType: fact / recommendation / procedure / policy / meta
	ClaimType string `gorm:"type:varchar(30);not null;default:'fact';index"`
	// TrustLevel: authoritative / supported / weak / blocked
	TrustLevel string `gorm:"type:varchar(30);not null;default:'supported';index"`
	// Freshness: current / stale / unknown
	Freshness string `gorm:"type:varchar(30);not null;default:'unknown'"`
	// TopicLabels 是 JSON 数组文本（主题标签，用于 topic 匹配辅助）。
	TopicLabels string `gorm:"type:text"`
	// ResourcePurpose: entrance_photo / map_image / address_image / facility_photo / room_photo / menu / instruction / unknown
	ResourcePurpose    string `gorm:"type:varchar(40);not null;default:'unknown'"`
	AutoAttachResource bool   `gorm:"not null;default:false"`
	// ReviewStatus: approved / pending / rejected
	ReviewStatus string `gorm:"type:varchar(30);not null;default:'pending';index"`
	// SourceDigest 是知识正文指纹（内容变化时递增 MetadataRevision）。
	SourceDigest     string    `gorm:"type:varchar(128);not null;default:''"`
	MetadataRevision int64     `gorm:"type:bigint;not null;default:1"`
	CreatedAt        time.Time `gorm:"type:datetime;not null;index"`
	UpdatedAt        time.Time `gorm:"type:datetime;not null"`
}
