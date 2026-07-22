package migration

import (
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

func init() {
	register(67, "retire legacy conversation tags and preserve customer tag permission", func() error {
		return retireLegacyConversationTags(sqls.DB())
	})
}

func retireLegacyConversationTags(db *gorm.DB) error {
	if db == nil {
		return nil
	}
	now := time.Now()
	permission := constants.PermissionCustomerTag
	return db.Model(&models.Permission{}).Where("code = ?", permission.Code).Updates(map[string]any{
		"name":             permission.Name,
		"type":             permission.Type,
		"group_name":       permission.GroupName,
		"method":           permission.Method,
		"api_path":         permission.APIPath,
		"sort_no":          permission.SortNo,
		"updated_at":       now,
		"update_user_id":   constants.SystemAuditUserID,
		"update_user_name": constants.SystemAuditUserName,
	}).Error
}
