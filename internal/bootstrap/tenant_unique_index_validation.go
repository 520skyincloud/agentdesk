package bootstrap

import (
	"fmt"
	"reflect"
	"slices"

	"agent-desk/internal/models"

	"gorm.io/gorm"
)

type tenantScopedUniqueIndexSpec struct {
	model         any
	currentName   string
	currentFields []string
}

func tenantScopedUniqueIndexSpecs() []tenantScopedUniqueIndexSpec {
	return []tenantScopedUniqueIndexSpec{
		{
			model: &models.Store{}, currentName: "uk_store_tenant_code",
			currentFields: []string{"tenant_id", "store_code"},
		},
		{
			model: &models.AgentProfile{}, currentName: "uk_agent_profile_tenant_code",
			currentFields: []string{"tenant_id", "agent_code"},
		},
	}
}

func validateTenantScopedUniqueIndexes(db *gorm.DB) error {
	for _, spec := range tenantScopedUniqueIndexSpecs() {
		if err := requireUniqueIndex(db, spec.model, spec.currentName, spec.currentFields); err != nil {
			return err
		}
	}
	return nil
}

func requireUniqueIndex(db *gorm.DB, model any, name string, expectedFields []string) error {
	definition, err := readIndexDefinition(db, model, name)
	if err != nil {
		return fmt.Errorf("read index %s failed: %w", name, err)
	}
	if !definition.exists {
		return fmt.Errorf("required unique index %s does not exist", name)
	}
	if !definition.unique {
		return fmt.Errorf("index %s is not a unique index", name)
	}
	if !slices.Equal(definition.fields, expectedFields) {
		return fmt.Errorf("index %s fields are %v, expected %v", name, definition.fields, expectedFields)
	}
	return nil
}

type tenantUniqueIndexDefinition struct {
	exists bool
	unique bool
	fields []string
}

func readIndexDefinition(db *gorm.DB, model any, name string) (tenantUniqueIndexDefinition, error) {
	table, err := tenantUniqueModelTable(db, model)
	if err != nil {
		return tenantUniqueIndexDefinition{}, err
	}
	switch db.Dialector.Name() {
	case "sqlite":
		return readSQLiteIndexDefinition(db, table, name)
	case "mysql":
		return readMySQLIndexDefinition(db, table, name)
	default:
		return tenantUniqueIndexDefinition{}, fmt.Errorf("unsupported database driver %q", db.Dialector.Name())
	}
}

func tenantUniqueModelTable(db *gorm.DB, model any) (string, error) {
	statement := &gorm.Statement{DB: db}
	if err := statement.Parse(model); err != nil {
		return "", err
	}
	if statement.Schema == nil || statement.Schema.Table == "" {
		return "", fmt.Errorf("model %s has no table metadata", reflect.TypeOf(model))
	}
	return statement.Schema.Table, nil
}

func readSQLiteIndexDefinition(db *gorm.DB, table, name string) (tenantUniqueIndexDefinition, error) {
	type indexRow struct {
		Name   string `gorm:"column:name"`
		Unique int    `gorm:"column:is_unique"`
	}
	var rows []indexRow
	if err := db.Raw(`SELECT name, "unique" AS is_unique FROM pragma_index_list(?) WHERE name = ?`, table, name).Scan(&rows).Error; err != nil {
		return tenantUniqueIndexDefinition{}, err
	}
	if len(rows) == 0 {
		return tenantUniqueIndexDefinition{}, nil
	}
	var fields []string
	if err := db.Raw("SELECT name FROM pragma_index_info(?) ORDER BY seqno", name).Scan(&fields).Error; err != nil {
		return tenantUniqueIndexDefinition{}, err
	}
	return tenantUniqueIndexDefinition{exists: true, unique: rows[0].Unique == 1, fields: fields}, nil
}

func readMySQLIndexDefinition(db *gorm.DB, table, name string) (tenantUniqueIndexDefinition, error) {
	rows, err := db.Raw(
		"SELECT column_name, non_unique FROM information_schema.statistics WHERE table_schema = DATABASE() AND table_name = ? AND index_name = ? ORDER BY seq_in_index",
		table, name,
	).Rows()
	if err != nil {
		return tenantUniqueIndexDefinition{}, err
	}
	defer rows.Close()
	fields := make([]string, 0)
	unique := true
	for rows.Next() {
		var field string
		var nonUnique int
		if err := rows.Scan(&field, &nonUnique); err != nil {
			return tenantUniqueIndexDefinition{}, err
		}
		fields = append(fields, field)
		unique = unique && nonUnique == 0
	}
	if err := rows.Err(); err != nil {
		return tenantUniqueIndexDefinition{}, err
	}
	if len(fields) == 0 {
		return tenantUniqueIndexDefinition{}, nil
	}
	return tenantUniqueIndexDefinition{exists: true, unique: unique, fields: fields}, nil
}
