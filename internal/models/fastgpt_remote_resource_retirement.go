package models

import (
	"time"

	"agent-desk/internal/pkg/enums"
)

// FastGPTRemoteResourceRetirement is the explicit, non-sensitive cleanup list
// for a legacy managed Dataset. Migration never deletes a remote resource; a
// replacement must first become the Store authority, after which operations can
// clean the exact Tenant + Store + remote IDs recorded here.
type FastGPTRemoteResourceRetirement struct {
	ID                         int64                               `gorm:"primaryKey;autoIncrement"`
	TenantID                   int64                               `gorm:"type:bigint;not null;default:0;uniqueIndex:uk_fastgpt_remote_retirement,priority:1;index"`
	StoreID                    int64                               `gorm:"type:bigint;not null;default:0;uniqueIndex:uk_fastgpt_remote_retirement,priority:2;index"`
	LegacyKnowledgeBaseID      int64                               `gorm:"type:bigint;not null;default:0;index"`
	LegacyTeamID               string                              `gorm:"type:varchar(128);not null;default:'';index"`
	LegacyDatasetID            string                              `gorm:"type:varchar(128);not null;default:'';uniqueIndex:uk_fastgpt_remote_retirement,priority:3;index"`
	ReplacementKnowledgeBaseID int64                               `gorm:"type:bigint;not null;default:0;index"`
	ReplacementDatasetID       string                              `gorm:"type:varchar(128);not null;default:'';index"`
	Status                     enums.FastGPTRemoteRetirementStatus `gorm:"type:varchar(40);not null;default:'awaiting_replacement';index"`
	Reason                     string                              `gorm:"type:varchar(100);not null;default:'';index"`
	CutoverAt                  *time.Time                          `gorm:"type:datetime;index"`
	CleanedAt                  *time.Time                          `gorm:"type:datetime;index"`
	CreatedAt                  time.Time                           `gorm:"type:datetime;not null;index"`
	UpdatedAt                  time.Time                           `gorm:"type:datetime;not null;index"`
}
