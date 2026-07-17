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

type supersededMigrationDefinition struct {
	Version int64
	Remark  string
	Reason  string
}

var supersededMigrationDefinitions = []supersededMigrationDefinition{
	{Version: 21, Remark: "sync customer service team leader permissions", Reason: "parallel customer-service branch reused version 21"},
	{Version: 21, Remark: "backfill wxwork protocol instance agent team bindings", Reason: "historical customer-service branch reused version 21"},
	{Version: 22, Remark: "backfill store staff agent team bindings", Reason: "historical customer-service branch reused version 22"},
	{Version: 25, Remark: "backfill wxwork protocol instance agent team bindings", Reason: "parallel customer-service branch reused version 25"},
	{Version: 26, Remark: "backfill store staff agent team bindings", Reason: "parallel customer-service branch reused version 26"},
	{Version: 27, Remark: "sync tenant auth foundation", Reason: "parallel tenant branch reused version 27"},
}

var compatibleHistoricalMigrationDefinitions = map[int64]map[string]struct{}{
	13: {
		"normalize reply intent configs to seven categories": {},
	},
}

func Migrate() error {
	mu.Lock()
	defer mu.Unlock()

	if err := archiveSupersededMigrationDefinitions(sqls.DB()); err != nil {
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
	if remarks := compatibleHistoricalMigrationDefinitions[current.Version]; stored.Version == current.Version {
		if _, ok := remarks[stored.Remark]; ok {
			return nil
		}
	}
	return fmt.Errorf(
		"migration version %d definition mismatch: database remark %q, code remark %q; rebuild the development database or verify and remap migration history before continuing",
		current.Version,
		stored.Remark,
		current.Remark,
	)
}

func archiveSupersededMigrationDefinitions(db *gorm.DB) error {
	for _, definition := range supersededMigrationDefinitions {
		current, ok := migrationFuncs[definition.Version]
		if !ok || current.Remark == definition.Remark {
			continue
		}
		var stored models.Migration
		err := db.Take(&stored, "version = ? AND remark = ?", definition.Version, definition.Remark).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			continue
		}
		if err != nil {
			return fmt.Errorf("load superseded migration version %d: %w", definition.Version, err)
		}
		if err := db.Transaction(func(tx *gorm.DB) error {
			archive := models.MigrationDefinitionArchive{
				SourceMigrationID: stored.ID,
				Version:           stored.Version,
				Remark:            stored.Remark,
				Success:           stored.Success,
				ErrorInfo:         stored.ErrorInfo,
				RetryCount:        stored.RetryCount,
				OriginalCreatedAt: stored.CreatedAt,
				OriginalUpdatedAt: stored.UpdatedAt,
				ReplacementRemark: current.Remark,
				ArchiveReason:     definition.Reason,
				ArchivedAt:        time.Now(),
			}
			var existing models.MigrationDefinitionArchive
			err := tx.Take(&existing, "source_migration_id = ?", stored.ID).Error
			switch {
			case errors.Is(err, gorm.ErrRecordNotFound):
				if err := tx.Create(&archive).Error; err != nil {
					return err
				}
			case err != nil:
				return err
			case existing.Version != archive.Version || existing.Remark != archive.Remark:
				return fmt.Errorf("migration archive source %d conflicts with stored definition", stored.ID)
			}
			result := tx.Delete(&models.Migration{}, "id = ? AND version = ? AND remark = ?", stored.ID, stored.Version, stored.Remark)
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return fmt.Errorf("archive superseded migration version %d did not remove the expected record", stored.Version)
			}
			return nil
		}); err != nil {
			return fmt.Errorf("archive superseded migration version %d: %w", definition.Version, err)
		}
		slog.Warn("archived superseded migration definition",
			"version", definition.Version,
			"legacyRemark", definition.Remark,
			"currentRemark", current.Remark,
		)
	}
	return nil
}
