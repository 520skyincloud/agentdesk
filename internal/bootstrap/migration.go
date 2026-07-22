package bootstrap

import (
	"agent-desk/internal/migration"
	"agent-desk/internal/models"

	"github.com/mlogclub/simple/sqls"
)

func InitMigrations() error {
	// Validate migration identities before AutoMigrate changes any schema. An
	// unknown parallel-branch definition must stop production startup first.
	if err := migration.Preflight(sqls.DB()); err != nil {
		return err
	}
	if err := sqls.DB().AutoMigrate(models.Models...); err != nil {
		return err
	}
	if err := retireLegacyGlobalUniqueIndexes(sqls.DB()); err != nil {
		return err
	}
	return migration.Migrate()
}
