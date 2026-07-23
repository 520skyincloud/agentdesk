package migration

import (
	"strconv"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

func init() {
	register(59, "migrate tenant AI model access", func() error {
		return sqls.WithTransaction(func(ctx *sqls.TxContext) error {
			if err := removeRetiredAIAgentManagementPermissions(ctx.Tx); err != nil {
				return err
			}
			if err := syncPlatformSystemPermissions(ctx.Tx); err != nil {
				return err
			}
			if err := removeTenantRolePlatformPermissions(ctx.Tx); err != nil {
				return err
			}
			return migrateTenantAIModelAccess(ctx.Tx)
		})
	})
}

func removeRetiredAIAgentManagementPermissions(tx *gorm.DB) error {
	var permissions []models.Permission
	if err := tx.Where("code IN ?", []string{"aiAgent.create", "aiAgent.update", "aiAgent.delete"}).Find(&permissions).Error; err != nil {
		return err
	}
	for _, permission := range permissions {
		if err := tx.Where("permission_id = ?", permission.ID).Delete(&models.RolePermission{}).Error; err != nil {
			return err
		}
		if err := tx.Delete(&models.Permission{}, "id = ?", permission.ID).Error; err != nil {
			return err
		}
	}
	return nil
}

func migrateTenantAIModelAccess(tx *gorm.DB) error {
	if tx == nil {
		return nil
	}
	now := time.Now()
	if tx.Migrator().HasTable(&legacyAIConfig{}) {
		if err := tx.Model(&legacyAIConfig{}).Where("api_key <> ''").Updates(map[string]any{
			"api_key": "", "updated_at": now,
			"update_user_id": constants.SystemAuditUserID, "update_user_name": constants.SystemAuditUserName,
		}).Error; err != nil {
			return err
		}
	}
	if tx.Migrator().HasTable(&legacyStoreAIModelSetting{}) {
		if err := tx.Model(&legacyStoreAIModelSetting{}).Where("status <> ?", enums.StatusDeleted).Updates(map[string]any{
			"status": enums.StatusDisabled, "provider": "", "base_url": "", "api_key": "",
			"model_type": "", "model_name": "", "config_fingerprint": "", "remark": "",
			"updated_at": now, "update_user_id": constants.SystemAuditUserID, "update_user_name": constants.SystemAuditUserName,
		}).Error; err != nil {
			return err
		}
	}
	if tx.Migrator().HasTable(&legacyTenantAIModelGrant{}) {
		if err := tx.Model(&legacyTenantAIModelGrant{}).Where("status <> ?", enums.StatusDeleted).Updates(map[string]any{
			"status": enums.StatusDisabled, "updated_at": now,
			"update_user_id": constants.SystemAuditUserID, "update_user_name": constants.SystemAuditUserName,
		}).Error; err != nil {
			return err
		}
	}
	if tx.Migrator().HasColumn("t_ai_agent", "ai_config_id") {
		if err := tx.Table("t_ai_agent").Where("ai_config_id <> 0").Updates(map[string]any{
			"ai_config_id": 0, "updated_at": now,
			"update_user_id": constants.SystemAuditUserID, "update_user_name": constants.SystemAuditUserName,
		}).Error; err != nil {
			return err
		}
	}

	var tenants []models.Tenant
	if err := tx.Where("status <> ?", enums.StatusDeleted).Order("id").Find(&tenants).Error; err != nil {
		return err
	}
	for i := range tenants {
		if repositories.AIAgentRepository.FindOne(tx, sqls.NewCnd().Eq("tenant_id", tenants[i].ID).Where("status <> ?", enums.StatusDeleted)) != nil {
			continue
		}
		team := repositories.AgentTeamRepository.FindOne(tx, sqls.NewCnd().Eq("tenant_id", tenants[i].ID).Eq("is_default", true).Where("status <> ?", enums.StatusDeleted))
		teamIDs := ""
		if team != nil {
			teamIDs = strconv.FormatInt(team.ID, 10)
		}
		if err := repositories.AIAgentRepository.Create(tx, &models.AIAgent{
			TenantID: tenants[i].ID, Name: "默认接待策略", Description: "接入公司内部运行身份",
			Status: enums.StatusOk, ServiceMode: enums.IMConversationServiceModeAIFirst,
			SystemPrompt:        "回答应简短、准确；需要真实动作时必须进入现有服务路由，不得虚构已处理结果。",
			ReplyTimeoutSeconds: 180, TeamIDs: teamIDs, HandoffMode: enums.AIAgentHandoffModeWaitPool,
			FallbackMode: enums.AIAgentFallbackModeNoAnswer, AuditFields: systemModelAuditFields(now),
		}); err != nil {
			return err
		}
	}
	return nil
}

func systemModelAuditFields(now time.Time) models.AuditFields {
	return models.AuditFields{
		CreatedAt: now, CreateUserID: constants.SystemAuditUserID, CreateUserName: constants.SystemAuditUserName,
		UpdatedAt: now, UpdateUserID: constants.SystemAuditUserID, UpdateUserName: constants.SystemAuditUserName,
	}
}
