package migration

import (
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/enums"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

const enforceStoreStaffActiveOwnershipMigrationRemark = "enforce one active store identity per store staff account"

func init() {
	register(76, enforceStoreStaffActiveOwnershipMigrationRemark, func() error {
		return sqls.WithTransaction(func(ctx *sqls.TxContext) error {
			return enforceStoreStaffActiveOwnership(ctx.Tx)
		})
	})
}

func enforceStoreStaffActiveOwnership(tx *gorm.DB) error {
	if tx == nil || !tx.Migrator().HasTable(&models.StoreStaffBinding{}) {
		return nil
	}
	now := time.Now()
	audit := map[string]any{
		"updated_at":       now,
		"update_user_id":   constants.SystemAuditUserID,
		"update_user_name": constants.SystemAuditUserName,
	}
	if err := tx.Model(&models.StoreStaffBinding{}).
		Where("active_user_id IS NOT NULL").
		Update("active_user_id", nil).Error; err != nil {
		return err
	}

	bindings := make([]models.StoreStaffBinding, 0)
	if err := tx.Where("user_id > 0 AND status <> ?", enums.StatusDeleted).
		Order("user_id ASC, status ASC, id ASC").
		Find(&bindings).Error; err != nil {
		return err
	}
	for start := 0; start < len(bindings); {
		end := start + 1
		for end < len(bindings) && bindings[end].UserID == bindings[start].UserID {
			end++
		}
		winner := &bindings[start]
		for index := start + 1; index < end; index++ {
			if err := archiveDuplicateStoreStaffBinding(tx, &bindings[index], audit); err != nil {
				return err
			}
		}
		if winner.Status == enums.StatusOk {
			var account models.User
			accountReady := tx.Where(
				"id = ? AND tenant_id = ? AND status = ? AND deleted_at IS NULL",
				winner.UserID,
				winner.TenantID,
				enums.StatusOk,
			).First(&account).Error == nil
			if !accountReady {
				if err := disableInvalidStoreStaffBinding(tx, winner, audit); err != nil {
					return err
				}
			} else if err := tx.Model(&models.StoreStaffBinding{}).
				Where("id = ? AND status = ?", winner.ID, enums.StatusOk).
				Updates(mergeMigrationColumns(audit, map[string]any{"active_user_id": winner.UserID})).Error; err != nil {
				return err
			}
		}
		start = end
	}

	var invalidActiveCount int64
	if err := tx.Model(&models.StoreStaffBinding{}).
		Where(
			"status = ? AND (tenant_id <= 0 OR store_id <= 0 OR user_id <= 0 OR active_user_id IS NULL OR active_user_id <> user_id)",
			enums.StatusOk,
		).
		Count(&invalidActiveCount).Error; err != nil {
		return err
	}
	if invalidActiveCount > 0 {
		return gorm.ErrInvalidData
	}
	return nil
}

func archiveDuplicateStoreStaffBinding(tx *gorm.DB, binding *models.StoreStaffBinding, audit map[string]any) error {
	if binding == nil {
		return nil
	}
	reason := "同一系统账号的重复历史门店绑定已软归档，仅保留一个稳定门店身份"
	if err := tx.Model(&models.StoreStaffBinding{}).
		Where("id = ? AND status <> ?", binding.ID, enums.StatusDeleted).
		Updates(mergeMigrationColumns(audit, map[string]any{
			"active_user_id": nil,
			"status":         enums.StatusDeleted,
			"remark":         appendMigrationRemark(binding.Remark, reason),
		})).Error; err != nil {
		return err
	}
	return disableStoreStaffBindingInstances(tx, binding, audit)
}

func disableInvalidStoreStaffBinding(tx *gorm.DB, binding *models.StoreStaffBinding, audit map[string]any) error {
	if binding == nil {
		return nil
	}
	reason := "门店员工绑定账号不存在、已停用或跨租户，唯一账号占用已解除"
	if err := tx.Model(&models.StoreStaffBinding{}).
		Where("id = ? AND status = ?", binding.ID, enums.StatusOk).
		Updates(mergeMigrationColumns(audit, map[string]any{
			"active_user_id": nil,
			"status":         enums.StatusDisabled,
			"remark":         appendMigrationRemark(binding.Remark, reason),
		})).Error; err != nil {
		return err
	}
	return disableStoreStaffBindingInstances(tx, binding, audit)
}

func disableStoreStaffBindingInstances(tx *gorm.DB, binding *models.StoreStaffBinding, audit map[string]any) error {
	if binding == nil {
		return nil
	}
	return tx.Model(&models.WxWorkProtocolInstance{}).
		Where(
			"tenant_id = ? AND store_staff_binding_id = ? AND status <> ?",
			binding.TenantID,
			binding.ID,
			enums.StatusDeleted,
		).
		Updates(mergeMigrationColumns(audit, map[string]any{
			"status":           enums.StatusDisabled,
			"ai_reply_enabled": false,
			"health_status":    "pending_binding",
		})).Error
}
