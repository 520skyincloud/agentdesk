package models

type ConversationDialogueState struct {
	ID                 int64  `gorm:"primaryKey;autoIncrement"`
	TenantID           int64  `gorm:"type:bigint;not null;default:0;index;uniqueIndex:uk_dialogue_state,priority:1"`
	ConversationID     int64  `gorm:"type:bigint;not null;default:0;index;uniqueIndex:uk_dialogue_state,priority:2"`
	SessionNo          int    `gorm:"type:int;not null;default:1;index;uniqueIndex:uk_dialogue_state,priority:3"`
	Revision           int64  `gorm:"type:bigint;not null;default:1;index"`
	BasedOnMessageID   int64  `gorm:"type:bigint;not null;default:0;index:idx_dialogue_state_message"`
	BasedOnTurnVersion int    `gorm:"type:int;not null;default:0"`
	SchemaVersion      string `gorm:"type:varchar(40);not null;default:'dialogue_state_snapshot.v1'"`
	SnapshotJSON       string `gorm:"type:text"`
	AuditFields
}
