package migration

import (
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/enums"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

const retireLocalKnowledgePermissionsMigrationRemark = "retire local document and FAQ permissions"

var retiredLocalKnowledgePermissionCodes = []string{
	"knowledgeDocument.view",
	"knowledgeDocument.create",
	"knowledgeDocument.update",
	"knowledgeDocument.delete",
	"knowledgeFAQ.view",
	"knowledgeFAQ.create",
	"knowledgeFAQ.update",
	"knowledgeFAQ.delete",
}

func init() {
	register(75, retireLocalKnowledgePermissionsMigrationRemark, func() error {
		return sqls.WithTransaction(func(ctx *sqls.TxContext) error {
			return retireLocalKnowledgePermissions(ctx.Tx)
		})
	})
}

func retireLocalKnowledgePermissions(tx *gorm.DB) error {
	var permissionIDs []int64
	if err := tx.Model(&models.Permission{}).
		Where("code IN ?", retiredLocalKnowledgePermissionCodes).
		Pluck("id", &permissionIDs).Error; err != nil {
		return err
	}
	if len(permissionIDs) == 0 {
		return nil
	}
	if err := tx.Where("permission_id IN ?", permissionIDs).Delete(&models.RolePermission{}).Error; err != nil {
		return err
	}
	return tx.Model(&models.Permission{}).
		Where("id IN ?", permissionIDs).
		Updates(map[string]any{
			"status":           enums.StatusDisabled,
			"remark":           "已随本地文档/FAQ知识链退出运行",
			"update_user_id":   constants.SystemAuditUserID,
			"update_user_name": constants.SystemAuditUserName,
			"updated_at":       time.Now(),
		}).Error
}
