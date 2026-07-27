package migration

import (
	"strconv"
	"strings"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/enums"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

func init() {
	register(63, "retire legacy company scopes from active store runtime", func() error {
		return sqls.WithTransaction(func(ctx *sqls.TxContext) error {
			return retireLegacyCompanyStoreScopes(ctx.Tx)
		})
	})
}

func retireLegacyCompanyStoreScopes(tx *gorm.DB) error {
	now := time.Now()
	audit := map[string]any{
		"updated_at":       now,
		"update_user_id":   constants.SystemAuditUserID,
		"update_user_name": constants.SystemAuditUserName,
	}
	if err := tx.Model(&models.Company{}).
		Where("intent_profile_id <> ?", 0).
		Updates(mergeMigrationColumns(audit, map[string]any{"intent_profile_id": 0})).Error; err != nil {
		return err
	}
	for _, target := range []struct {
		model any
		where string
		value map[string]any
	}{
		{&models.Store{}, "tenant_id > 0 AND company_id <> 0 AND status <> ?", mergeMigrationColumns(audit, map[string]any{"company_id": 0})},
		{&models.StoreStaffBinding{}, "tenant_id > 0 AND company_id <> 0 AND status <> ?", mergeMigrationColumns(audit, map[string]any{"company_id": 0})},
		{&models.WxWorkProtocolInstance{}, "tenant_id > 0 AND company_id <> 0 AND status <> ?", mergeMigrationColumns(audit, map[string]any{"company_id": 0})},
		{&models.AgentTeam{}, "tenant_id > 0 AND company_scope_ids <> '' AND status <> ?", mergeMigrationColumns(audit, map[string]any{"company_scope_ids": ""})},
		{&models.KnowledgeBase{}, "tenant_id > 0 AND store_id > 0 AND company_id <> 0 AND status <> ?", mergeMigrationColumns(audit, map[string]any{"company_id": 0})},
	} {
		if err := tx.Model(target.model).Where(target.where, enums.StatusDeleted).Updates(target.value).Error; err != nil {
			return err
		}
	}
	if err := retireKnowledgeResourceCompanyScopes(tx, audit); err != nil {
		return err
	}
	for _, target := range []struct {
		model any
		where string
	}{
		{&models.FastGPTStoreTenant{}, "tenant_id > 0 AND store_id > 0 AND company_id <> 0"},
		{&models.FastGPTUsageSyncState{}, "tenant_id > 0 AND store_id > 0 AND company_id <> 0"},
		{&models.FastGPTDatasetJob{}, "tenant_id > 0 AND store_id > 0 AND company_id <> 0"},
	} {
		if err := tx.Model(target.model).Where(target.where).Updates(map[string]any{"company_id": 0, "updated_at": now}).Error; err != nil {
			return err
		}
	}
	if err := tx.Model(&models.ReplyIntentConfig{}).
		Where("scope_type = ? AND status = ?", "company", enums.StatusOk).
		Updates(mergeMigrationColumns(audit, map[string]any{"status": enums.StatusDisabled})).Error; err != nil {
		return err
	}
	if err := retireInvalidStoreStaffBindings(tx, audit); err != nil {
		return err
	}
	if err := backfillStoreStaffRoleIdentities(tx, audit); err != nil {
		return err
	}
	return retireLegacyCompanyPermissions(tx)
}

func retireKnowledgeResourceCompanyScopes(tx *gorm.DB, audit map[string]any) error {
	groups := make([]models.KnowledgeResourceGroup, 0)
	if err := tx.Where("tenant_id > 0 AND store_id > 0").
		Order("store_id ASC, knowledge_base_id ASC, source_provider ASC, source_record_id ASC, id ASC").
		Find(&groups).Error; err != nil {
		return err
	}
	type resourceKey struct {
		storeID         int64
		knowledgeBaseID int64
		sourceProvider  string
		sourceRecordID  string
	}
	grouped := make(map[resourceKey][]models.KnowledgeResourceGroup, len(groups))
	for i := range groups {
		group := groups[i]
		key := resourceKey{group.StoreID, group.KnowledgeBaseID, group.SourceProvider, group.SourceRecordID}
		grouped[key] = append(grouped[key], group)
	}
	for _, candidates := range grouped {
		winner := candidates[0]
		for i := 1; i < len(candidates); i++ {
			candidate := candidates[i]
			candidateActive := candidate.Status == enums.StatusOk
			winnerActive := winner.Status == enums.StatusOk
			if candidateActive != winnerActive {
				if candidateActive {
					winner = candidate
				}
				continue
			}
			if candidate.UpdatedAt.After(winner.UpdatedAt) || (candidate.UpdatedAt.Equal(winner.UpdatedAt) && candidate.ID > winner.ID) {
				winner = candidate
			}
		}
		loserIDs := make([]int64, 0, len(candidates)-1)
		for i := range candidates {
			if candidates[i].ID != winner.ID {
				loserIDs = append(loserIDs, candidates[i].ID)
			}
		}
		if len(loserIDs) > 0 {
			if err := tx.Where("knowledge_resource_group_id IN ?", loserIDs).Delete(&models.KnowledgeResourceItem{}).Error; err != nil {
				return err
			}
			if err := tx.Where("id IN ?", loserIDs).Delete(&models.KnowledgeResourceGroup{}).Error; err != nil {
				return err
			}
		}
		if winner.CompanyID != 0 {
			if err := tx.Model(&models.KnowledgeResourceGroup{}).
				Where("id = ? AND tenant_id = ?", winner.ID, winner.TenantID).
				Updates(mergeMigrationColumns(audit, map[string]any{"company_id": 0})).Error; err != nil {
				return err
			}
		}
	}
	return nil
}

func backfillStoreStaffRoleIdentities(tx *gorm.DB, audit map[string]any) error {
	var role models.Role
	if err := tx.Where("code = ? AND status = ?", constants.RoleCodeStoreStaff, enums.StatusOk).First(&role).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil
		}
		return err
	}
	relations := make([]models.UserRole, 0)
	if err := tx.Where("role_id = ?", role.ID).Order("user_id ASC").Find(&relations).Error; err != nil {
		return err
	}
	for i := range relations {
		var user models.User
		if err := tx.Where("id = ? AND tenant_id > 0 AND status = ? AND deleted_at IS NULL", relations[i].UserID, enums.StatusOk).First(&user).Error; err != nil {
			if err == gorm.ErrRecordNotFound {
				continue
			}
			return err
		}
		var bindingCount int64
		if err := tx.Model(&models.StoreStaffBinding{}).
			Where("tenant_id = ? AND user_id = ? AND status = ?", user.TenantID, user.ID, enums.StatusOk).
			Count(&bindingCount).Error; err != nil {
			return err
		}
		if bindingCount > 0 {
			continue
		}
		storeName := strings.TrimSpace(user.Nickname)
		if storeName == "" {
			storeName = strings.TrimSpace(user.Username)
		}
		store := &models.Store{
			TenantID:  user.TenantID,
			StoreCode: "migrated-store-user-" + strconv.FormatInt(user.ID, 10),
			Name:      storeName,
			CompanyID: 0,
			Status:    enums.StatusOk,
			Remark:    "由历史门店员工号角色回填，门店名称待公司主管核对",
			AuditFields: models.AuditFields{
				CreatedAt:      audit["updated_at"].(time.Time),
				CreateUserID:   constants.SystemAuditUserID,
				CreateUserName: constants.SystemAuditUserName,
				UpdatedAt:      audit["updated_at"].(time.Time),
				UpdateUserID:   constants.SystemAuditUserID,
				UpdateUserName: constants.SystemAuditUserName,
			},
		}
		if err := tx.Create(store).Error; err != nil {
			return err
		}
		binding := &models.StoreStaffBinding{
			TenantID:             user.TenantID,
			UserID:               user.ID,
			StoreID:              store.ID,
			CompanyID:            0,
			ManagedMode:          constants.StoreManagedModeSemi,
			FallbackToHQ:         true,
			ManualTimeoutMinutes: 10,
			Status:               enums.StatusOk,
			Remark:               "由历史门店员工号角色回填",
			AuditFields:          store.AuditFields,
		}
		if err := tx.Create(binding).Error; err != nil {
			return err
		}
	}
	return nil
}

func retireInvalidStoreStaffBindings(tx *gorm.DB, audit map[string]any) error {
	var role models.Role
	hasStoreStaffRole := tx.Where("code = ? AND status = ?", constants.RoleCodeStoreStaff, enums.StatusOk).First(&role).Error == nil
	bindings := make([]models.StoreStaffBinding, 0)
	if err := tx.Where("tenant_id > 0 AND status = ?", enums.StatusOk).
		Order("tenant_id ASC, user_id ASC, id ASC").
		Find(&bindings).Error; err != nil {
		return err
	}

	type ownerKey struct {
		tenantID int64
		userID   int64
	}
	seenOwners := make(map[ownerKey]int64, len(bindings))
	for i := range bindings {
		binding := &bindings[i]
		reason := ""
		if binding.UserID <= 0 {
			reason = "历史门店绑定缺少系统账号，已停用，需由公司主管重新绑定"
		} else {
			var user models.User
			if err := tx.Where("id = ? AND tenant_id = ? AND status = ? AND deleted_at IS NULL", binding.UserID, binding.TenantID, enums.StatusOk).First(&user).Error; err != nil {
				reason = "历史门店绑定的系统账号不可用，已停用，需由公司主管重新绑定"
			} else if hasStoreStaffRole {
				var count int64
				if err := tx.Model(&models.UserRole{}).Where("user_id = ? AND role_id = ?", binding.UserID, role.ID).Count(&count).Error; err != nil {
					return err
				}
				if count == 0 {
					reason = "历史门店绑定账号未分配门店员工号角色，已停用"
				}
			}
		}
		key := ownerKey{tenantID: binding.TenantID, userID: binding.UserID}
		if reason == "" {
			if binding.UserID > 0 && seenOwners[key] > 0 {
				reason = "同一系统账号存在多个历史门店绑定，重复绑定已停用"
			} else {
				seenOwners[key] = binding.ID
			}
		}
		if reason == "" {
			continue
		}
		updates := mergeMigrationColumns(audit, map[string]any{
			"status": enums.StatusDisabled,
			"remark": appendMigrationRemark(binding.Remark, reason),
		})
		if err := tx.Model(&models.StoreStaffBinding{}).
			Where("id = ? AND tenant_id = ? AND status = ?", binding.ID, binding.TenantID, enums.StatusOk).
			Updates(updates).Error; err != nil {
			return err
		}
		if err := tx.Model(&models.WxWorkProtocolInstance{}).
			Where("tenant_id = ? AND store_staff_binding_id = ? AND status <> ?", binding.TenantID, binding.ID, enums.StatusDeleted).
			Updates(mergeMigrationColumns(audit, map[string]any{
				"status":           enums.StatusDisabled,
				"ai_reply_enabled": false,
				"health_status":    "pending_binding",
			})).Error; err != nil {
			return err
		}
	}

	instances := make([]models.WxWorkProtocolInstance, 0)
	if err := tx.Where("tenant_id > 0 AND status <> ?", enums.StatusDeleted).Find(&instances).Error; err != nil {
		return err
	}
	for i := range instances {
		instance := &instances[i]
		var binding models.StoreStaffBinding
		valid := instance.StoreStaffBindingID > 0 && tx.Where(
			"id = ? AND tenant_id = ? AND store_id = ? AND status = ?",
			instance.StoreStaffBindingID, instance.TenantID, instance.StoreID, enums.StatusOk,
		).First(&binding).Error == nil
		if valid {
			continue
		}
		if err := tx.Model(&models.WxWorkProtocolInstance{}).
			Where("id = ? AND tenant_id = ?", instance.ID, instance.TenantID).
			Updates(mergeMigrationColumns(audit, map[string]any{
				"status":           enums.StatusDisabled,
				"ai_reply_enabled": false,
				"health_status":    "pending_binding",
			})).Error; err != nil {
			return err
		}
	}
	return nil
}

func appendMigrationRemark(current, message string) string {
	current = strings.TrimSpace(current)
	if current == "" {
		return message
	}
	if strings.Contains(current, message) {
		return current
	}
	return current + " | " + message
}

func retireLegacyCompanyPermissions(tx *gorm.DB) error {
	if !tx.Migrator().HasTable(&models.Permission{}) {
		return nil
	}
	permissions := make([]models.Permission, 0, 4)
	if err := tx.Where("code IN ?", []string{"company.view", "company.create", "company.update", "company.delete"}).Find(&permissions).Error; err != nil {
		return err
	}
	for i := range permissions {
		if tx.Migrator().HasTable(&models.RolePermission{}) {
			if err := tx.Where("permission_id = ?", permissions[i].ID).Delete(&models.RolePermission{}).Error; err != nil {
				return err
			}
		}
		if err := tx.Delete(&models.Permission{}, permissions[i].ID).Error; err != nil {
			return err
		}
	}
	return nil
}

func mergeMigrationColumns(base map[string]any, extra map[string]any) map[string]any {
	ret := make(map[string]any, len(base)+len(extra))
	for key, value := range base {
		ret[key] = value
	}
	for key, value := range extra {
		ret[key] = value
	}
	return ret
}
