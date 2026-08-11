package migration

import (
	"fmt"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

const switchUnifiedNewAPIGatewayMigrationRemark = "switch all model profiles to unified NewAPI gateway"

func init() {
	register(75, switchUnifiedNewAPIGatewayMigrationRemark, func() error {
		return switchUnifiedNewAPIGateway(sqls.DB())
	})
}

func switchUnifiedNewAPIGateway(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("unified NewAPI gateway migration database is nil")
	}
	now := time.Now()
	return db.Model(&models.ModelProfileTemplate{}).
		Where("gateway_base_url <> ? OR gateway_base_url IS NULL", constants.UnifiedNewAPIGatewayBaseURL).
		Updates(map[string]any{
			"gateway_base_url": constants.UnifiedNewAPIGatewayBaseURL,
			"updated_at":       now,
			"update_user_id":   constants.SystemAuditUserID,
			"update_user_name": constants.SystemAuditUserName,
		}).Error
}
