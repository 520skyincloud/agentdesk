package repositories

import (
	"agent-desk/internal/models"

	"gorm.io/gorm"
)

var UserRoleChangeLogRepository = &userRoleChangeLogRepository{}

type userRoleChangeLogRepository struct{}

func (r *userRoleChangeLogRepository) Create(db *gorm.DB, item *models.UserRoleChangeLog) error {
	return db.Create(item).Error
}
