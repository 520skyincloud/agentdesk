package repositories

import (
	"agent-desk/internal/models"

	"gorm.io/gorm"
)

var RolePermissionChangeLogRepository = &rolePermissionChangeLogRepository{}

type rolePermissionChangeLogRepository struct{}

type RolePermissionChangeLogAuditRow struct {
	ID                        int64  `gorm:"column:id"`
	RoleID                    int64  `gorm:"column:role_id"`
	BeforePermissionIDsJSON   string `gorm:"column:before_permission_ids_json"`
	AfterPermissionIDsJSON    string `gorm:"column:after_permission_ids_json"`
	BeforePermissionCodesJSON string `gorm:"column:before_permission_codes_json"`
	AfterPermissionCodesJSON  string `gorm:"column:after_permission_codes_json"`
}

func (r *rolePermissionChangeLogRepository) Create(db *gorm.DB, item *models.RolePermissionChangeLog) error {
	return db.Create(item).Error
}

func (r *rolePermissionChangeLogRepository) FindAuditRows(db *gorm.DB) ([]RolePermissionChangeLogAuditRow, error) {
	rows := make([]RolePermissionChangeLogAuditRow, 0)
	err := db.Model(&models.RolePermissionChangeLog{}).
		Select("id, role_id, before_permission_ids_json, after_permission_ids_json, before_permission_codes_json, after_permission_codes_json").
		Order("role_id ASC, id ASC").
		Scan(&rows).Error
	return rows, err
}
