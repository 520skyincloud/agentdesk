package models

// ReplyActionDefinition 登记系统可执行的回复动作（转人工、发定位、查房态等）。
// 动作定义由代码注册表初始化，后台只允许开关与排序，不新增/删除内置动作。
type ReplyActionDefinition struct {
	ID                  int64  `gorm:"primaryKey;autoIncrement"`
	Code                string `gorm:"type:varchar(64);not null;uniqueIndex"`
	Name                string `gorm:"type:varchar(120);not null;default:''"`
	Kind                string `gorm:"type:varchar(20);not null;default:'builtin';index"` // builtin / external / tool
	Description         string `gorm:"type:text"`
	InputSchema         string `gorm:"type:text"` // 需要客户补充的参数（JSON Schema 文本）。
	RequireConfirmation bool   `gorm:"not null;default:false"`
	ExecutorRef         string `gorm:"type:varchar(80);not null;default:''"`
	Enabled             bool   `gorm:"not null;default:false;index"` // 启用开关。
	SortNo              int    `gorm:"type:int;not null;default:0;index"`
	AuditFields
}
