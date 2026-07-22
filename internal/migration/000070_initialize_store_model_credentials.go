package migration

import (
	"fmt"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/services"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

const initializeStoreModelCredentialsMigrationRemark = "initialize encrypted store model credential records"

func init() {
	register(70, initializeStoreModelCredentialsMigrationRemark, func() error {
		return initializeStoreModelCredentials(sqls.DB())
	})
}

func initializeStoreModelCredentials(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("store model credential migration database is nil")
	}
	return db.Transaction(func(tx *gorm.DB) error {
		var stores []models.Store
		if err := tx.Where("tenant_id > 0 AND status <> ?", enums.StatusDeleted).Order("tenant_id ASC, id ASC").Find(&stores).Error; err != nil {
			return err
		}
		for i := range stores {
			if err := services.StoreModelCredentialService.EnsureStoreRecordsDB(tx, &stores[i], nil); err != nil {
				return fmt.Errorf("initialize store %d model credential: %w", stores[i].ID, err)
			}
		}
		return nil
	})
}
