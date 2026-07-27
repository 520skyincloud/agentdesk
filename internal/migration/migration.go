package migration

import (
	"agent-desk/internal/models"
	"agent-desk/internal/services"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/mlogclub/simple/sqls"
	"github.com/spf13/cast"
	"gorm.io/gorm"
)

var migrationFuncs = make(map[int64]MigrationFunc)
var versions = make([]int64, 0)
var migrations = make(map[int64]models.Migration, 0)
var mu sync.Mutex

type MigrationFunc struct {
	Version int64
	Remark  string
	Fn      func() error
}

func Migrate() error {
	mu.Lock()
	defer mu.Unlock()

	if err := Preflight(sqls.DB()); err != nil {
		return err
	}
	migrations = make(map[int64]models.Migration)
	if list := services.MigrationService.Find(sqls.NewCnd().Asc("version")); len(list) > 0 {
		for _, element := range list {
			migrations[element.Version] = element
		}
	}

	for _, version := range versions {
		if err := runMigration(version); err != nil {
			slog.Error("migrate failed", "version", version, "error", err)
			return err
		}
	}
	return nil
}

// Preflight validates migration identities without mutating the database. It
// is deliberately called before AutoMigrate so an unknown parallel-branch
// definition cannot partially alter a production schema before startup fails.
func Preflight(db *gorm.DB) error {
	if db == nil {
		return errors.New("migration preflight database is nil")
	}
	if !db.Migrator().HasTable(&models.Migration{}) {
		return nil
	}
	var storedMigrations []models.Migration
	if err := db.Order("version ASC").Find(&storedMigrations).Error; err != nil {
		return fmt.Errorf("load migration definitions for preflight: %w", err)
	}
	for _, stored := range storedMigrations {
		current, ok := migrationFuncs[stored.Version]
		if !ok {
			return fmt.Errorf(
				"migration preflight found unknown version %d with remark %q; verify the source branch and remap the history before startup",
				stored.Version,
				stored.Remark,
			)
		}
		if err := validateMigrationDefinition(stored, current); err != nil {
			return fmt.Errorf("migration preflight failed: %w", err)
		}
	}
	return nil
}

func register(version int64, remark string, fn func() error) {
	if len(versions) == 0 || version > versions[len(versions)-1] {
		versions = append(versions, version)
		migrationFuncs[version] = MigrationFunc{
			Version: version,
			Remark:  remark,
			Fn:      fn,
		}
	} else {
		slog.Error("register migration failed, version is less than latest version", slog.Any("version", version))
		panic(errors.New("register migration failed, version is less than latest version. version: " + cast.ToString(version)))
	}
}

func runMigration(version int64) error {
	f, ok := migrationFuncs[version]
	if !ok {
		return errors.New("migration function not found")
	}
	migration, found := migrations[version]
	if found {
		if err := validateMigrationDefinition(migration, f); err != nil {
			return err
		}
		if migration.Success {
			return nil
		}
	}

	err := f.Fn()

	if !found {
		migration = models.Migration{
			Version:    f.Version,
			Remark:     f.Remark,
			Success:    false,
			RetryCount: 0,
			CreatedAt:  time.Now(),
			UpdatedAt:  time.Now(),
		}
	}
	if err == nil {
		migration.Success = true
	} else {
		migration.Success = false
		migration.ErrorInfo = err.Error()
	}
	migration.RetryCount++
	migration.UpdatedAt = time.Now()
	if found {
		if e := services.MigrationService.Update(&migration); e != nil {
			slog.Error("update migration failed", "version", version, "error", e)
		}
	} else {
		if e := services.MigrationService.Create(&migration); e != nil {
			slog.Error("create migration failed", "version", version, "error", e)
		}
	}

	return err
}

func validateMigrationDefinition(stored models.Migration, current MigrationFunc) error {
	if stored.Version == current.Version && stored.Remark == current.Remark {
		return nil
	}
	return fmt.Errorf(
		"migration version %d definition mismatch: database remark %q, code remark %q; rebuild the development database or verify and remap migration history before continuing",
		current.Version,
		stored.Remark,
		current.Remark,
	)
}
