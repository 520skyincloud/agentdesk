package migration

import (
	"fmt"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/enums"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const initializeCustomerTagRuntimePoliciesMigrationRemark = "initialize Store customer tag runtime policies"

func init() {
	register(74, initializeCustomerTagRuntimePoliciesMigrationRemark, func() error {
		return sqls.WithTransaction(func(ctx *sqls.TxContext) error {
			permissions, err := ensurePermissions(ctx.Tx)
			if err != nil {
				return err
			}
			roles, err := ensureRoles(ctx.Tx)
			if err != nil {
				return err
			}
			if err := ensureRolePermissions(ctx.Tx, roles, permissions); err != nil {
				return err
			}
			return initializeCustomerTagRuntimePolicies(ctx.Tx)
		})
	})
}

func initializeCustomerTagRuntimePolicies(db *gorm.DB) error {
	stores := make([]models.Store, 0)
	if err := db.Where("tenant_id > 0 AND status <> ?", enums.StatusDeleted).Order("tenant_id ASC, id ASC").Find(&stores).Error; err != nil {
		return err
	}
	if len(stores) == 0 {
		return nil
	}
	policies := make([]models.TenantCustomerTagPolicy, 0)
	if err := db.Where("tenant_id > 0 AND status = ?", enums.StatusOk).Find(&policies).Error; err != nil {
		return err
	}
	policyByTenant := make(map[int64]models.TenantCustomerTagPolicy, len(policies))
	for i := range policies {
		policyByTenant[policies[i].TenantID] = policies[i]
	}
	now := time.Now()
	items := make([]models.StoreCustomerTagRuntimePolicy, 0, len(stores))
	for i := range stores {
		store := stores[i]
		policy, ok := policyByTenant[store.TenantID]
		if !ok {
			return fmt.Errorf("Store %d tenant %d has no active customer tag policy", store.ID, store.TenantID)
		}
		items = append(items, models.StoreCustomerTagRuntimePolicy{
			TenantID: store.TenantID, StoreID: store.ID,
			CustomerTagEvolutionEnabled: policy.EvolutionDefaultEnabled,
			ReplyTagContextEnabled:      policy.ReplyTagContextDefaultEnabled,
			Status:                      enums.StatusOk,
			AuditFields: models.AuditFields{
				CreatedAt: now, UpdatedAt: now,
				CreateUserID: constants.SystemAuditUserID, CreateUserName: constants.SystemAuditUserName,
				UpdateUserID: constants.SystemAuditUserID, UpdateUserName: constants.SystemAuditUserName,
			},
		})
	}
	return db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "tenant_id"}, {Name: "store_id"}},
		DoNothing: true,
	}).CreateInBatches(items, 100).Error
}
