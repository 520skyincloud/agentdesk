package repositories

import (
	"agent-desk/internal/models"

	"gorm.io/gorm"
)

var UserRoleChangeLogRepository = &userRoleChangeLogRepository{}

type userRoleChangeLogRepository struct{}

type UserRoleChangeLogAuditRow struct {
	ID                  int64  `gorm:"column:id"`
	BeforeRoleIDsJSON   string `gorm:"column:before_role_ids_json"`
	AfterRoleIDsJSON    string `gorm:"column:after_role_ids_json"`
	BeforeRoleCodesJSON string `gorm:"column:before_role_codes_json"`
	AfterRoleCodesJSON  string `gorm:"column:after_role_codes_json"`
}

func (r *userRoleChangeLogRepository) Create(db *gorm.DB, item *models.UserRoleChangeLog) error {
	return db.Create(item).Error
}

func (r *userRoleChangeLogRepository) FindAuditRows(db *gorm.DB) ([]UserRoleChangeLogAuditRow, error) {
	rows := make([]UserRoleChangeLogAuditRow, 0)
	err := db.Model(&models.UserRoleChangeLog{}).
		Select("id, before_role_ids_json, after_role_ids_json, before_role_codes_json, after_role_codes_json").
		Order("id ASC").
		Scan(&rows).Error
	return rows, err
}
