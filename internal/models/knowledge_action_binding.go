package models

// KnowledgeActionBinding 把一条知识记录（以 SourceRecordID 稳定引用）绑定到一个回复动作。
// 知识正文存于 FastGPT，检索命中后按 SourceRecordID 查绑定，命中且启用时把该知识问题
// 提升为结构化动作，而不是让模型口头复述“转人工/已查询”。
type KnowledgeActionBinding struct {
	ID              int64  `gorm:"primaryKey;autoIncrement"`
	TenantID        int64  `gorm:"type:bigint;not null;default:0;index;uniqueIndex:uk_knowledge_action_binding,priority:1"`
	StoreID         int64  `gorm:"type:bigint;not null;default:0;index;uniqueIndex:uk_knowledge_action_binding,priority:2"`
	KnowledgeBaseID int64  `gorm:"type:bigint;not null;default:0;index;uniqueIndex:uk_knowledge_action_binding,priority:3"`
	SourceRecordID  string `gorm:"type:varchar(255);not null;default:'';index;uniqueIndex:uk_knowledge_action_binding,priority:4"`
	ActionCode      string `gorm:"type:varchar(64);not null;default:'';index"`
	Enabled         bool   `gorm:"not null;default:true;index"`
	SortNo          int    `gorm:"type:int;not null;default:0;index"`
	Remark          string `gorm:"type:text"`
	AuditFields
}
