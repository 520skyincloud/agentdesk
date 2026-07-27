package migration

import (
	"fmt"
	"strconv"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

const initializeTenantRuntimeAgentsMigrationRemark = "initialize tenant runtime strategy identities"

func init() {
	register(77, initializeTenantRuntimeAgentsMigrationRemark, func() error {
		return sqls.WithTransaction(func(ctx *sqls.TxContext) error {
			return initializeTenantRuntimeAgents(ctx.Tx)
		})
	})
}

func initializeTenantRuntimeAgents(tx *gorm.DB) error {
	if tx == nil {
		return fmt.Errorf("tenant runtime agent migration database is nil")
	}
	var tenants []models.Tenant
	if err := tx.Where("status <> ?", enums.StatusDeleted).Order("id ASC").Find(&tenants).Error; err != nil {
		return err
	}
	now := time.Now()
	for i := range tenants {
		tenant := tenants[i]
		if repositories.AIAgentRepository.FindOne(
			tx,
			sqls.NewCnd().Eq("tenant_id", tenant.ID).Where("status <> ?", enums.StatusDeleted),
		) != nil {
			continue
		}
		team := repositories.AgentTeamRepository.FindOne(
			tx,
			sqls.NewCnd().
				Eq("tenant_id", tenant.ID).
				Eq("is_default", true).
				Where("status <> ?", enums.StatusDeleted),
		)
		teamIDs := ""
		if team != nil {
			teamIDs = strconv.FormatInt(team.ID, 10)
		}
		if err := repositories.AIAgentRepository.Create(tx, &models.AIAgent{
			TenantID:            tenant.ID,
			Name:                "默认接待策略",
			Description:         "接入公司内部运行身份",
			Status:              enums.StatusOk,
			ServiceMode:         enums.IMConversationServiceModeAIFirst,
			SystemPrompt:        "回答应简短、准确；需要真实动作时必须进入现有服务路由，不得虚构已处理结果。",
			ReplyTimeoutSeconds: 180,
			TeamIDs:             teamIDs,
			HandoffMode:         enums.AIAgentHandoffModeWaitPool,
			FallbackMode:        enums.AIAgentFallbackModeNoAnswer,
			AuditFields:         tenantRuntimeAgentAuditFields(now),
		}); err != nil {
			return err
		}
	}
	return nil
}

func tenantRuntimeAgentAuditFields(now time.Time) models.AuditFields {
	return models.AuditFields{
		CreatedAt:      now,
		CreateUserID:   constants.SystemAuditUserID,
		CreateUserName: constants.SystemAuditUserName,
		UpdatedAt:      now,
		UpdateUserID:   constants.SystemAuditUserID,
		UpdateUserName: constants.SystemAuditUserName,
	}
}
