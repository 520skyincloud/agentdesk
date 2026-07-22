package migration

import (
	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"

	"github.com/mlogclub/simple/sqls"
)

func init() {
	register(65, "repair standard hotel tag category flags", func() error {
		return repairStandardHotelTagCategoryFlags()
	})
}

func repairStandardHotelTagCategoryFlags() error {
	db := sqls.DB()
	if db == nil {
		return nil
	}
	return db.Model(&models.Tag{}).
		Where("company_id = ? AND parent_id = ?", 0, 0).
		Where("name IN ?", standardHotelTagCategoryNames()).
		Updates(map[string]any{
			"ai_enabled":       false,
			"reply_enabled":    false,
			"update_user_id":   constants.SystemAuditUserID,
			"update_user_name": constants.SystemAuditUserName,
		}).Error
}

func standardHotelTagCategoryNames() []string {
	categories := standardHotelTagCategories()
	names := make([]string, 0, len(categories))
	for _, category := range categories {
		names = append(names, category.Name)
	}
	return names
}
