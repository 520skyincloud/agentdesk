package repositories

import (
	"database/sql"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var LegacySchemaCleanupRepository = newLegacySchemaCleanupRepository()

var legacySchemaCleanupIdentifierPattern = regexp.MustCompile(`^[A-Za-z0-9_]+$`)

type legacySchemaCleanupRepository struct{}

func newLegacySchemaCleanupRepository() *legacySchemaCleanupRepository {
	return &legacySchemaCleanupRepository{}
}

type LegacySchemaCleanupIndex struct {
	Table   string
	Name    string
	Columns []string
}

type LegacySchemaCleanupForeignKey struct {
	SourceTable      string
	SourceColumn     string
	ConstraintName   string
	ReferencedTable  string
	ReferencedColumn string
}

type LegacySchemaCleanupDependentObject struct {
	Type       string
	Name       string
	Table      string
	Definition string
}

func (r *legacySchemaCleanupRepository) ListApplicationTables(db *gorm.DB) ([]string, error) {
	if db == nil {
		return nil, fmt.Errorf("schema cleanup database is required")
	}
	tables, err := db.Migrator().GetTables()
	if err != nil {
		return nil, err
	}
	ret := make([]string, 0, len(tables))
	for _, table := range tables {
		if strings.HasPrefix(table, "t_") && legacySchemaCleanupIdentifierPattern.MatchString(table) {
			ret = append(ret, table)
		}
	}
	sort.Strings(ret)
	return ret, nil
}

func (r *legacySchemaCleanupRepository) ColumnNames(db *gorm.DB, table string) ([]string, error) {
	if err := validateLegacySchemaCleanupIdentifier(table); err != nil {
		return nil, err
	}
	if !db.Migrator().HasTable(table) {
		return []string{}, nil
	}
	columnTypes, err := db.Migrator().ColumnTypes(table)
	if err != nil {
		return nil, err
	}
	ret := make([]string, 0, len(columnTypes))
	for _, columnType := range columnTypes {
		name := columnType.Name()
		if !legacySchemaCleanupIdentifierPattern.MatchString(name) {
			return nil, fmt.Errorf("table %s contains an unsupported column identifier", table)
		}
		ret = append(ret, name)
	}
	sort.Strings(ret)
	return ret, nil
}

func (r *legacySchemaCleanupRepository) CountRows(db *gorm.DB, table string) (int64, error) {
	if err := validateLegacySchemaCleanupIdentifier(table); err != nil {
		return 0, err
	}
	var count int64
	err := db.Table(table).Count(&count).Error
	return count, err
}

func (r *legacySchemaCleanupRepository) CountPositiveReferences(
	db *gorm.DB,
	table string,
	column string,
) (int64, error) {
	if err := validateLegacySchemaCleanupIdentifiers(table, column); err != nil {
		return 0, err
	}
	quotedColumn, err := quoteLegacySchemaCleanupIdentifier(db, column)
	if err != nil {
		return 0, err
	}
	var count int64
	err = db.Table(table).Where(quotedColumn+" > ?", 0).Count(&count).Error
	return count, err
}

func (r *legacySchemaCleanupRepository) CountNonEmptyJSONReferences(
	db *gorm.DB,
	table string,
	column string,
) (int64, error) {
	if err := validateLegacySchemaCleanupIdentifiers(table, column); err != nil {
		return 0, err
	}
	quotedColumn, err := quoteLegacySchemaCleanupIdentifier(db, column)
	if err != nil {
		return 0, err
	}
	var count int64
	err = db.Table(table).
		Where(
			quotedColumn+" IS NOT NULL AND "+quotedColumn+" <> ? AND "+quotedColumn+" <> ? AND "+quotedColumn+" <> ?",
			"",
			"[]",
			"null",
		).
		Count(&count).Error
	return count, err
}

func (r *legacySchemaCleanupRepository) Indexes(
	db *gorm.DB,
	table string,
) ([]LegacySchemaCleanupIndex, error) {
	if err := validateLegacySchemaCleanupIdentifier(table); err != nil {
		return nil, err
	}
	if !db.Migrator().HasTable(table) {
		return []LegacySchemaCleanupIndex{}, nil
	}
	indexes, err := db.Migrator().GetIndexes(table)
	if err != nil {
		return nil, err
	}
	ret := make([]LegacySchemaCleanupIndex, 0, len(indexes))
	for _, index := range indexes {
		columns := append([]string(nil), index.Columns()...)
		ret = append(ret, LegacySchemaCleanupIndex{
			Table:   table,
			Name:    index.Name(),
			Columns: columns,
		})
	}
	sort.Slice(ret, func(i, j int) bool {
		return ret[i].Name < ret[j].Name
	})
	return ret, nil
}

func (r *legacySchemaCleanupRepository) ForeignKeys(
	db *gorm.DB,
) ([]LegacySchemaCleanupForeignKey, error) {
	switch db.Dialector.Name() {
	case "sqlite":
		return r.sqliteForeignKeys(db)
	case "mysql":
		return r.mysqlForeignKeys(db)
	default:
		return nil, fmt.Errorf("unsupported schema cleanup database driver: %s", db.Dialector.Name())
	}
}

func (r *legacySchemaCleanupRepository) DependentObjects(
	db *gorm.DB,
) ([]LegacySchemaCleanupDependentObject, error) {
	switch db.Dialector.Name() {
	case "sqlite":
		var rows []struct {
			Type       string `gorm:"column:type"`
			Name       string `gorm:"column:name"`
			Table      string `gorm:"column:tbl_name"`
			Definition string `gorm:"column:sql"`
		}
		if err := db.Raw(
			`SELECT type, name, tbl_name, sql
			 FROM sqlite_master
			 WHERE type IN ('view', 'trigger') AND sql IS NOT NULL
			 ORDER BY type ASC, name ASC`,
		).Scan(&rows).Error; err != nil {
			return nil, err
		}
		ret := make([]LegacySchemaCleanupDependentObject, 0, len(rows))
		for _, row := range rows {
			ret = append(ret, LegacySchemaCleanupDependentObject{
				Type: row.Type, Name: row.Name, Table: row.Table, Definition: row.Definition,
			})
		}
		return ret, nil
	case "mysql":
		var views []struct {
			Name       string `gorm:"column:name"`
			Definition string `gorm:"column:definition"`
		}
		if err := db.Raw(
			`SELECT table_name AS name, view_definition AS definition
			 FROM information_schema.views
			 WHERE table_schema = DATABASE()
			 ORDER BY table_name ASC`,
		).Scan(&views).Error; err != nil {
			return nil, err
		}
		var triggers []struct {
			Name       string `gorm:"column:name"`
			Table      string `gorm:"column:table_name"`
			Definition string `gorm:"column:definition"`
		}
		if err := db.Raw(
			`SELECT trigger_name AS name, event_object_table AS table_name, action_statement AS definition
			 FROM information_schema.triggers
			 WHERE trigger_schema = DATABASE()
			 ORDER BY trigger_name ASC`,
		).Scan(&triggers).Error; err != nil {
			return nil, err
		}
		ret := make([]LegacySchemaCleanupDependentObject, 0, len(views)+len(triggers))
		for _, row := range views {
			ret = append(ret, LegacySchemaCleanupDependentObject{
				Type: "view", Name: row.Name, Definition: row.Definition,
			})
		}
		for _, row := range triggers {
			ret = append(ret, LegacySchemaCleanupDependentObject{
				Type: "trigger", Name: row.Name, Table: row.Table, Definition: row.Definition,
			})
		}
		return ret, nil
	default:
		return nil, fmt.Errorf("unsupported schema cleanup database driver: %s", db.Dialector.Name())
	}
}

func (r *legacySchemaCleanupRepository) DropIndex(db *gorm.DB, table string, index string) error {
	if err := validateLegacySchemaCleanupIdentifiers(table, index); err != nil {
		return err
	}
	return db.Migrator().DropIndex(table, index)
}

func (r *legacySchemaCleanupRepository) DropColumn(db *gorm.DB, table string, column string) error {
	if err := validateLegacySchemaCleanupIdentifiers(table, column); err != nil {
		return err
	}
	// Both supported databases implement this exact DDL. Clause identifiers keep
	// the compile-time allowlist quoted without interpolating operator input.
	return db.Exec(
		"ALTER TABLE ? DROP COLUMN ?",
		clause.Table{Name: table},
		clause.Column{Name: column},
	).Error
}

func (r *legacySchemaCleanupRepository) DropTable(db *gorm.DB, table string) error {
	if err := validateLegacySchemaCleanupIdentifier(table); err != nil {
		return err
	}
	return db.Migrator().DropTable(table)
}

func (r *legacySchemaCleanupRepository) sqliteForeignKeys(
	db *gorm.DB,
) ([]LegacySchemaCleanupForeignKey, error) {
	tables, err := r.ListApplicationTables(db)
	if err != nil {
		return nil, err
	}
	ret := make([]LegacySchemaCleanupForeignKey, 0)
	for _, table := range tables {
		quotedTable, quoteErr := quoteLegacySchemaCleanupIdentifier(db, table)
		if quoteErr != nil {
			return nil, quoteErr
		}
		var rows []struct {
			ID               int            `gorm:"column:id"`
			Sequence         int            `gorm:"column:seq"`
			ReferencedTable  string         `gorm:"column:table"`
			SourceColumn     string         `gorm:"column:from"`
			ReferencedColumn sql.NullString `gorm:"column:to"`
		}
		if err := db.Raw("PRAGMA foreign_key_list(" + quotedTable + ")").Scan(&rows).Error; err != nil {
			return nil, err
		}
		for _, row := range rows {
			ret = append(ret, LegacySchemaCleanupForeignKey{
				SourceTable:      table,
				SourceColumn:     row.SourceColumn,
				ConstraintName:   fmt.Sprintf("sqlite_fk_%d_%d", row.ID, row.Sequence),
				ReferencedTable:  row.ReferencedTable,
				ReferencedColumn: row.ReferencedColumn.String,
			})
		}
	}
	sortLegacySchemaCleanupForeignKeys(ret)
	return ret, nil
}

func (r *legacySchemaCleanupRepository) mysqlForeignKeys(
	db *gorm.DB,
) ([]LegacySchemaCleanupForeignKey, error) {
	var rows []struct {
		SourceTable      string `gorm:"column:source_table"`
		SourceColumn     string `gorm:"column:source_column"`
		ConstraintName   string `gorm:"column:constraint_name"`
		ReferencedTable  string `gorm:"column:referenced_table"`
		ReferencedColumn string `gorm:"column:referenced_column"`
	}
	if err := db.Raw(
		`SELECT table_name AS source_table,
		        column_name AS source_column,
		        constraint_name,
		        referenced_table_name AS referenced_table,
		        referenced_column_name AS referenced_column
		 FROM information_schema.key_column_usage
		 WHERE table_schema = DATABASE() AND referenced_table_name IS NOT NULL
		 ORDER BY table_name ASC, constraint_name ASC, ordinal_position ASC`,
	).Scan(&rows).Error; err != nil {
		return nil, err
	}
	ret := make([]LegacySchemaCleanupForeignKey, 0, len(rows))
	for _, row := range rows {
		ret = append(ret, LegacySchemaCleanupForeignKey{
			SourceTable: row.SourceTable, SourceColumn: row.SourceColumn,
			ConstraintName: row.ConstraintName, ReferencedTable: row.ReferencedTable,
			ReferencedColumn: row.ReferencedColumn,
		})
	}
	sortLegacySchemaCleanupForeignKeys(ret)
	return ret, nil
}

func sortLegacySchemaCleanupForeignKeys(list []LegacySchemaCleanupForeignKey) {
	sort.Slice(list, func(i, j int) bool {
		left := list[i]
		right := list[j]
		if left.SourceTable != right.SourceTable {
			return left.SourceTable < right.SourceTable
		}
		if left.ConstraintName != right.ConstraintName {
			return left.ConstraintName < right.ConstraintName
		}
		return left.SourceColumn < right.SourceColumn
	})
}

func validateLegacySchemaCleanupIdentifiers(values ...string) error {
	for _, value := range values {
		if err := validateLegacySchemaCleanupIdentifier(value); err != nil {
			return err
		}
	}
	return nil
}

func validateLegacySchemaCleanupIdentifier(value string) error {
	if !legacySchemaCleanupIdentifierPattern.MatchString(value) {
		return fmt.Errorf("unsupported schema cleanup identifier")
	}
	return nil
}

func quoteLegacySchemaCleanupIdentifier(db *gorm.DB, value string) (string, error) {
	if err := validateLegacySchemaCleanupIdentifier(value); err != nil {
		return "", err
	}
	switch db.Dialector.Name() {
	case "sqlite":
		return `"` + value + `"`, nil
	case "mysql":
		return "`" + value + "`", nil
	default:
		return "", fmt.Errorf("unsupported schema cleanup database driver: %s", db.Dialector.Name())
	}
}
