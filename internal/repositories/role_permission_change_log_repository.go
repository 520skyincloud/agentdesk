package repositories

import (
	"agent-desk/internal/models"

	"gorm.io/gorm"
)

var RolePermissionChangeLogRepository = &rolePermissionChangeLogRepository{}

type rolePermissionChangeLogRepository struct{}

func (r *rolePermissionChangeLogRepository) Create(db *gorm.DB, item *models.RolePermissionChangeLog) error {
	return db.Create(item).Error
}
