package services

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/repositories"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

var LegacySchemaCleanupService = newLegacySchemaCleanupService()

const legacySchemaCleanupContractVersion = "b14-schema-cleanup-v1"

type legacySchemaCleanupService struct{}

func newLegacySchemaCleanupService() *legacySchemaCleanupService {
	return &legacySchemaCleanupService{}
}

type LegacySchemaCleanupTableInventory struct {
	Name     string `json:"name"`
	Exists   bool   `json:"exists"`
	RowCount int64  `json:"rowCount"`
}

type LegacySchemaCleanupIndexInventory struct {
	Name    string   `json:"name"`
	Columns []string `json:"columns"`
	Allowed bool     `json:"allowed"`
}

type LegacySchemaCleanupColumnInventory struct {
	Table          string                              `json:"table"`
	Name           string                              `json:"name"`
	Exists         bool                                `json:"exists"`
	RowCount       int64                               `json:"rowCount"`
	ReferenceCount int64                               `json:"referenceCount"`
	Indexes        []LegacySchemaCleanupIndexInventory `json:"indexes"`
}

type LegacySchemaCleanupReferenceInventory struct {
	Type         string `json:"type"`
	Name         string `json:"name"`
	SourceTable  string `json:"sourceTable,omitempty"`
	SourceColumn string `json:"sourceColumn,omitempty"`
	TargetTable  string `json:"targetTable,omitempty"`
	TargetColumn string `json:"targetColumn,omitempty"`
}

type LegacySchemaCleanupViolation struct {
	Code    string `json:"code"`
	Object  string `json:"object"`
	Message string `json:"message"`
}

type LegacySchemaCleanupInventory struct {
	ContractVersion string                                  `json:"contractVersion"`
	DatabaseDriver  string                                  `json:"databaseDriver"`
	Ready           bool                                    `json:"ready"`
	ChangeCount     int                                     `json:"changeCount"`
	InventoryCode   string                                  `json:"inventoryCode"`
	Tables          []LegacySchemaCleanupTableInventory     `json:"tables"`
	Columns         []LegacySchemaCleanupColumnInventory    `json:"columns"`
	References      []LegacySchemaCleanupReferenceInventory `json:"references"`
	Violations      []LegacySchemaCleanupViolation          `json:"violations"`
	InventoryDigest string                                  `json:"-"`
}

type LegacySchemaCleanupExecutionResult struct {
	Before       *LegacySchemaCleanupInventory `json:"before"`
	After        *LegacySchemaCleanupInventory `json:"after"`
	AppliedSteps []string                      `json:"appliedSteps"`
}

type LegacySchemaCleanupPilotIdentity struct {
	TenantID   int64  `json:"tenantId"`
	TenantCode string `json:"tenantCode"`
	TenantName string `json:"tenantName"`
	StoreID    int64  `json:"storeId"`
	StoreCode  string `json:"storeCode"`
	StoreName  string `json:"storeName"`
}

type legacySchemaCleanupColumnTarget struct {
	Table          string
	Column         string
	ReferenceKind  string
	AllowedIndexes []string
}

var legacySchemaCleanupTableTargets = []string{
	"t_knowledge_chunk",
	"t_knowledge_faq",
	"t_knowledge_document",
	"t_conversation_tag",
	"t_store_ai_model_setting",
	"t_tenant_ai_model_grant",
	"t_ai_config",
}

var legacySchemaCleanupColumnTargets = []legacySchemaCleanupColumnTarget{
	{
		Table: "t_conversation_service_session", Column: "tag_ids_json",
		ReferenceKind: "json",
	},
	{
		Table: "t_ai_agent", Column: "ai_config_id", ReferenceKind: "positive",
		AllowedIndexes: []string{"idx_t_ai_agent_ai_config_id"},
	},
	{
		Table: "t_agent_run_log", Column: "ai_config_id", ReferenceKind: "positive",
		AllowedIndexes: []string{"idx_t_agent_run_log_ai_config_id"},
	},
	{
		Table: "t_ai_usage_event", Column: "ai_config_id", ReferenceKind: "positive",
		AllowedIndexes: []string{"idx_t_ai_usage_event_ai_config_id"},
	},
	{
		Table: "t_skill_run_log", Column: "ai_config_id", ReferenceKind: "positive",
		AllowedIndexes: []string{"idx_t_skill_run_log_ai_config_id"},
	},
}

func (s *legacySchemaCleanupService) Inspect(db *gorm.DB) (*LegacySchemaCleanupInventory, error) {
	if db == nil {
		return nil, fmt.Errorf("legacy schema cleanup requires a database")
	}
	driver := db.Dialector.Name()
	if driver != "sqlite" && driver != "mysql" {
		return nil, fmt.Errorf("unsupported schema cleanup database driver: %s", driver)
	}

	report := &LegacySchemaCleanupInventory{
		ContractVersion: legacySchemaCleanupContractVersion,
		DatabaseDriver:  driver,
		Tables:          make([]LegacySchemaCleanupTableInventory, 0, len(legacySchemaCleanupTableTargets)),
		Columns:         make([]LegacySchemaCleanupColumnInventory, 0, len(legacySchemaCleanupColumnTargets)),
		References:      []LegacySchemaCleanupReferenceInventory{},
		Violations:      []LegacySchemaCleanupViolation{},
	}
	tableSet := make(map[string]bool, len(legacySchemaCleanupTableTargets))
	for _, table := range legacySchemaCleanupTableTargets {
		tableSet[table] = true
		item := LegacySchemaCleanupTableInventory{Name: table, Exists: db.Migrator().HasTable(table)}
		if item.Exists {
			rowCount, err := repositories.LegacySchemaCleanupRepository.CountRows(db, table)
			if err != nil {
				return nil, fmt.Errorf("count legacy table %s failed: %w", table, err)
			}
			item.RowCount = rowCount
			report.ChangeCount++
		}
		report.Tables = append(report.Tables, item)
	}

	columnTargetByLocation := make(map[string]legacySchemaCleanupColumnTarget, len(legacySchemaCleanupColumnTargets))
	for _, target := range legacySchemaCleanupColumnTargets {
		columnTargetByLocation[legacySchemaCleanupLocation(target.Table, target.Column)] = target
		item := LegacySchemaCleanupColumnInventory{
			Table: target.Table, Name: target.Column, Indexes: []LegacySchemaCleanupIndexInventory{},
		}
		if db.Migrator().HasTable(target.Table) && db.Migrator().HasColumn(target.Table, target.Column) {
			item.Exists = true
			report.ChangeCount++
			var err error
			item.RowCount, err = repositories.LegacySchemaCleanupRepository.CountRows(db, target.Table)
			if err != nil {
				return nil, fmt.Errorf("count rows in %s failed: %w", target.Table, err)
			}
			switch target.ReferenceKind {
			case "positive":
				item.ReferenceCount, err = repositories.LegacySchemaCleanupRepository.CountPositiveReferences(
					db,
					target.Table,
					target.Column,
				)
			case "json":
				item.ReferenceCount, err = repositories.LegacySchemaCleanupRepository.CountNonEmptyJSONReferences(
					db,
					target.Table,
					target.Column,
				)
			default:
				err = fmt.Errorf("unsupported reference count kind")
			}
			if err != nil {
				return nil, fmt.Errorf(
					"count legacy column references %s.%s failed: %w",
					target.Table,
					target.Column,
					err,
				)
			}
			indexes, err := repositories.LegacySchemaCleanupRepository.Indexes(db, target.Table)
			if err != nil {
				return nil, fmt.Errorf("inspect indexes on %s failed: %w", target.Table, err)
			}
			for _, index := range indexes {
				if !legacySchemaCleanupIndexContainsColumn(index.Columns, target.Column) {
					continue
				}
				allowed := slices.Contains(target.AllowedIndexes, index.Name) &&
					len(index.Columns) == 1 &&
					strings.EqualFold(index.Columns[0], target.Column)
				item.Indexes = append(item.Indexes, LegacySchemaCleanupIndexInventory{
					Name: index.Name, Columns: append([]string(nil), index.Columns...), Allowed: allowed,
				})
				if !allowed {
					report.addViolation(
						"UNAPPROVED_INDEX",
						target.Table+"."+index.Name,
						"目标旧列存在未列入固定白名单的索引，清理已阻断",
					)
				}
			}
		}
		report.Columns = append(report.Columns, item)
	}

	tables, err := repositories.LegacySchemaCleanupRepository.ListApplicationTables(db)
	if err != nil {
		return nil, fmt.Errorf("list application tables failed: %w", err)
	}
	legacyColumnNames := map[string]bool{"ai_config_id": true, "tag_ids_json": true}
	for _, table := range tables {
		columns, err := repositories.LegacySchemaCleanupRepository.ColumnNames(db, table)
		if err != nil {
			return nil, fmt.Errorf("inspect columns on %s failed: %w", table, err)
		}
		for _, column := range columns {
			if !legacyColumnNames[strings.ToLower(column)] || tableSet[table] {
				continue
			}
			if _, allowed := columnTargetByLocation[legacySchemaCleanupLocation(table, column)]; allowed {
				continue
			}
			report.addViolation(
				"UNAPPROVED_COLUMN_LOCATION",
				table+"."+column,
				"发现固定白名单之外的旧列同名引用，清理已阻断",
			)
		}
	}

	foreignKeys, err := repositories.LegacySchemaCleanupRepository.ForeignKeys(db)
	if err != nil {
		return nil, fmt.Errorf("inspect foreign keys failed: %w", err)
	}
	for _, foreignKey := range foreignKeys {
		_, sourceColumnTarget := columnTargetByLocation[legacySchemaCleanupLocation(
			foreignKey.SourceTable,
			foreignKey.SourceColumn,
		)]
		if !tableSet[foreignKey.SourceTable] &&
			!tableSet[foreignKey.ReferencedTable] &&
			!sourceColumnTarget {
			continue
		}
		report.References = append(report.References, LegacySchemaCleanupReferenceInventory{
			Type: "foreign_key", Name: foreignKey.ConstraintName,
			SourceTable: foreignKey.SourceTable, SourceColumn: foreignKey.SourceColumn,
			TargetTable: foreignKey.ReferencedTable, TargetColumn: foreignKey.ReferencedColumn,
		})
		report.addViolation(
			"FOREIGN_KEY_REQUIRES_REVIEW",
			foreignKey.SourceTable+"."+foreignKey.ConstraintName,
			"固定白名单对象仍存在外键依赖，清理不会自动扩大到约束",
		)
	}

	dependentObjects, err := repositories.LegacySchemaCleanupRepository.DependentObjects(db)
	if err != nil {
		return nil, fmt.Errorf("inspect views and triggers failed: %w", err)
	}
	for _, object := range dependentObjects {
		if !legacySchemaCleanupDefinitionReferencesTarget(object.Definition) {
			continue
		}
		report.References = append(report.References, LegacySchemaCleanupReferenceInventory{
			Type: object.Type, Name: object.Name, SourceTable: object.Table,
		})
		report.addViolation(
			"DEPENDENT_OBJECT_REQUIRES_REVIEW",
			object.Type+"."+object.Name,
			"视图或触发器仍引用固定白名单对象，清理已阻断",
		)
	}

	sort.Slice(report.References, func(i, j int) bool {
		left := report.References[i]
		right := report.References[j]
		if left.Type != right.Type {
			return left.Type < right.Type
		}
		if left.SourceTable != right.SourceTable {
			return left.SourceTable < right.SourceTable
		}
		return left.Name < right.Name
	})
	sort.Slice(report.Violations, func(i, j int) bool {
		if report.Violations[i].Code != report.Violations[j].Code {
			return report.Violations[i].Code < report.Violations[j].Code
		}
		return report.Violations[i].Object < report.Violations[j].Object
	})
	report.Ready = len(report.Violations) == 0
	digest, err := legacySchemaCleanupInventoryDigest(report)
	if err != nil {
		return nil, err
	}
	report.InventoryDigest = digest
	report.InventoryCode = digest[:16]
	return report, nil
}

func (s *legacySchemaCleanupService) ResolvePilotIdentity(
	db *gorm.DB,
	tenantName string,
	storeName string,
) (*LegacySchemaCleanupPilotIdentity, error) {
	if db == nil {
		return nil, fmt.Errorf("pilot identity resolution requires a database")
	}
	tenantName = strings.TrimSpace(tenantName)
	storeName = strings.TrimSpace(storeName)
	if tenantName == "" || storeName == "" {
		return nil, fmt.Errorf("pilot tenant and Store names are required")
	}
	tenants := repositories.TenantRepository.Find(
		db,
		sqls.NewCnd().
			Where("(legal_name = ? OR short_name = ?)", tenantName, tenantName).
			Where("status <> ?", enums.StatusDeleted).
			Asc("id"),
	)
	if len(tenants) != 1 {
		return nil, fmt.Errorf("pilot Tenant identity must resolve to exactly one active record")
	}
	tenant := tenants[0]
	stores := repositories.StoreRepository.Find(
		db,
		sqls.NewCnd().
			Eq("tenant_id", tenant.ID).
			Eq("name", storeName).
			Where("status <> ?", enums.StatusDeleted).
			Asc("id"),
	)
	if len(stores) != 1 {
		return nil, fmt.Errorf("pilot Store identity must resolve to exactly one active record in the Tenant")
	}
	store := stores[0]
	return &LegacySchemaCleanupPilotIdentity{
		TenantID: tenant.ID, TenantCode: tenant.TenantCode,
		TenantName: legacySchemaCleanupTenantDisplayName(&tenant),
		StoreID:    store.ID, StoreCode: store.StoreCode, StoreName: store.Name,
	}, nil
}

// Execute applies only the compile-time B14 allowlist. MySQL DDL auto-commits,
// so callers must enforce the backup, restore and one-time-token gates first.
func (s *legacySchemaCleanupService) Execute(
	db *gorm.DB,
	expectedInventoryDigest string,
) (*LegacySchemaCleanupExecutionResult, error) {
	before, err := s.Inspect(db)
	if err != nil {
		return nil, err
	}
	if !before.Ready {
		return nil, fmt.Errorf("legacy schema cleanup inventory contains blocking references")
	}
	if expectedInventoryDigest == "" || before.InventoryDigest != expectedInventoryDigest {
		return nil, fmt.Errorf("legacy schema cleanup inventory changed after approval")
	}

	applied := make([]string, 0, before.ChangeCount+4)
	result := &LegacySchemaCleanupExecutionResult{
		Before: before, AppliedSteps: applied,
	}
	failed := func(cause error) (*LegacySchemaCleanupExecutionResult, error) {
		result.AppliedSteps = append([]string(nil), applied...)
		if after, inspectErr := s.Inspect(db); inspectErr == nil {
			result.After = after
		}
		return result, cause
	}
	for index, target := range legacySchemaCleanupColumnTargets {
		item := before.Columns[index]
		if !item.Exists {
			continue
		}
		for _, existingIndex := range item.Indexes {
			if !existingIndex.Allowed {
				return failed(fmt.Errorf("unapproved index reached schema cleanup execution"))
			}
			if err := repositories.LegacySchemaCleanupRepository.DropIndex(
				db,
				target.Table,
				existingIndex.Name,
			); err != nil {
				return failed(fmt.Errorf(
					"drop approved index %s.%s failed: %w",
					target.Table,
					existingIndex.Name,
					err,
				))
			}
			applied = append(applied, "drop_index:"+target.Table+"."+existingIndex.Name)
		}
		if err := repositories.LegacySchemaCleanupRepository.DropColumn(db, target.Table, target.Column); err != nil {
			return failed(fmt.Errorf("drop legacy column %s.%s failed: %w", target.Table, target.Column, err))
		}
		applied = append(applied, "drop_column:"+target.Table+"."+target.Column)
	}
	for _, table := range legacySchemaCleanupTableTargets {
		if !db.Migrator().HasTable(table) {
			continue
		}
		if err := repositories.LegacySchemaCleanupRepository.DropTable(db, table); err != nil {
			return failed(fmt.Errorf("drop legacy table %s failed: %w", table, err))
		}
		applied = append(applied, "drop_table:"+table)
	}

	after, err := s.Inspect(db)
	if err != nil {
		return failed(fmt.Errorf("inspect schema after cleanup failed: %w", err))
	}
	result.After = after
	result.AppliedSteps = append([]string(nil), applied...)
	if !after.Ready || after.ChangeCount != 0 {
		return result, fmt.Errorf("legacy schema cleanup did not reach the final schema")
	}
	return result, nil
}

func LegacySchemaCleanupFixedTables() []string {
	return append([]string(nil), legacySchemaCleanupTableTargets...)
}

func LegacySchemaCleanupFixedColumns() [][2]string {
	ret := make([][2]string, 0, len(legacySchemaCleanupColumnTargets))
	for _, item := range legacySchemaCleanupColumnTargets {
		ret = append(ret, [2]string{item.Table, item.Column})
	}
	return ret
}

func (r *LegacySchemaCleanupInventory) addViolation(code, object, message string) {
	r.Violations = append(r.Violations, LegacySchemaCleanupViolation{
		Code: code, Object: object, Message: message,
	})
}

func legacySchemaCleanupLocation(table, column string) string {
	return strings.ToLower(table) + "\x00" + strings.ToLower(column)
}

func legacySchemaCleanupIndexContainsColumn(columns []string, target string) bool {
	for _, column := range columns {
		if strings.EqualFold(column, target) {
			return true
		}
	}
	return false
}

func legacySchemaCleanupDefinitionReferencesTarget(definition string) bool {
	definition = strings.ToLower(definition)
	for _, table := range legacySchemaCleanupTableTargets {
		if strings.Contains(definition, strings.ToLower(table)) {
			return true
		}
	}
	for _, column := range legacySchemaCleanupColumnTargets {
		if strings.Contains(definition, strings.ToLower(column.Column)) {
			return true
		}
	}
	return false
}

func legacySchemaCleanupInventoryDigest(report *LegacySchemaCleanupInventory) (string, error) {
	payload := struct {
		ContractVersion string
		DatabaseDriver  string
		ChangeCount     int
		Tables          []LegacySchemaCleanupTableInventory
		Columns         []LegacySchemaCleanupColumnInventory
		References      []LegacySchemaCleanupReferenceInventory
		Violations      []LegacySchemaCleanupViolation
	}{
		ContractVersion: report.ContractVersion,
		DatabaseDriver:  report.DatabaseDriver,
		ChangeCount:     report.ChangeCount,
		Tables:          report.Tables,
		Columns:         report.Columns,
		References:      report.References,
		Violations:      report.Violations,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("encode legacy schema cleanup inventory failed: %w", err)
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func legacySchemaCleanupTenantDisplayName(tenant *models.Tenant) string {
	if tenant == nil {
		return ""
	}
	if value := strings.TrimSpace(tenant.ShortName); value != "" {
		return value
	}
	return strings.TrimSpace(tenant.LegalName)
}
