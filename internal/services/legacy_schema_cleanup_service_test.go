package services

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestLegacySchemaCleanupHistoricalSQLiteLifecycle(t *testing.T) {
	db, err := gorm.Open(
		sqlite.Open("file:legacy_schema_cleanup_lifecycle?mode=memory&cache=shared"),
		&gorm.Config{Logger: logger.Default.LogMode(logger.Silent)},
	)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	createLegacySchemaCleanupFixture(t, db)

	before, err := LegacySchemaCleanupService.Inspect(db)
	if err != nil {
		t.Fatalf("inspect historical schema: %v", err)
	}
	if !before.Ready {
		t.Fatalf("historical schema unexpectedly blocked: %#v", before.Violations)
	}
	if before.ChangeCount != 12 {
		t.Fatalf("change count=%d want=12", before.ChangeCount)
	}
	for _, item := range before.Tables {
		if !item.Exists || item.RowCount != 1 {
			t.Fatalf("unexpected table inventory: %#v", item)
		}
	}
	for _, item := range before.Columns {
		if !item.Exists || item.RowCount != 1 || item.ReferenceCount != 1 {
			t.Fatalf("unexpected column inventory: %#v", item)
		}
		for _, index := range item.Indexes {
			if !index.Allowed {
				t.Fatalf("approved fixture index was rejected: %#v", index)
			}
		}
	}

	result, err := LegacySchemaCleanupService.Execute(db, before.InventoryDigest)
	if err != nil {
		after, inspectErr := LegacySchemaCleanupService.Inspect(db)
		t.Fatalf("execute historical cleanup: %v; after=%#v inspectErr=%v", err, after, inspectErr)
	}
	if len(result.AppliedSteps) != 16 {
		t.Fatalf("applied steps=%d want=16: %#v", len(result.AppliedSteps), result.AppliedSteps)
	}
	assertLegacySchemaCleanupFinalSchema(t, db)

	repeatInventory, err := LegacySchemaCleanupService.Inspect(db)
	if err != nil {
		t.Fatalf("inspect cleaned schema: %v", err)
	}
	repeatResult, err := LegacySchemaCleanupService.Execute(db, repeatInventory.InventoryDigest)
	if err != nil {
		t.Fatalf("repeat cleanup: %v", err)
	}
	if len(repeatResult.AppliedSteps) != 0 || repeatResult.After.ChangeCount != 0 {
		t.Fatalf("repeat cleanup was not idempotent: %#v", repeatResult)
	}
}

func TestLegacySchemaCleanupRejectsObjectsOutsideFixedAllowlist(t *testing.T) {
	tests := []struct {
		name       string
		setup      func(t *testing.T, db *gorm.DB)
		wantCode   string
		assertKept func(t *testing.T, db *gorm.DB)
	}{
		{
			name: "unapproved index",
			setup: func(t *testing.T, db *gorm.DB) {
				execLegacySchemaCleanupFixtureSQL(t, db, "CREATE TABLE t_ai_agent (id BIGINT PRIMARY KEY, ai_config_id BIGINT NOT NULL DEFAULT 0)")
				execLegacySchemaCleanupFixtureSQL(t, db, "CREATE INDEX idx_unapproved_ai_config ON t_ai_agent (ai_config_id)")
			},
			wantCode: "UNAPPROVED_INDEX",
			assertKept: func(t *testing.T, db *gorm.DB) {
				if !db.Migrator().HasColumn("t_ai_agent", "ai_config_id") {
					t.Fatal("blocked cleanup removed ai_config_id")
				}
			},
		},
		{
			name: "unexpected column location",
			setup: func(t *testing.T, db *gorm.DB) {
				execLegacySchemaCleanupFixtureSQL(t, db, "CREATE TABLE t_unapproved_reference (id BIGINT PRIMARY KEY, ai_config_id BIGINT NOT NULL DEFAULT 0)")
			},
			wantCode: "UNAPPROVED_COLUMN_LOCATION",
			assertKept: func(t *testing.T, db *gorm.DB) {
				if !db.Migrator().HasColumn("t_unapproved_reference", "ai_config_id") {
					t.Fatal("blocked cleanup modified the unapproved table")
				}
			},
		},
		{
			name: "foreign key",
			setup: func(t *testing.T, db *gorm.DB) {
				execLegacySchemaCleanupFixtureSQL(t, db, "PRAGMA foreign_keys = ON")
				execLegacySchemaCleanupFixtureSQL(t, db, "CREATE TABLE t_ai_config (id BIGINT PRIMARY KEY)")
				execLegacySchemaCleanupFixtureSQL(t, db, "CREATE TABLE t_reference (id BIGINT PRIMARY KEY, legacy_id BIGINT REFERENCES t_ai_config(id))")
			},
			wantCode: "FOREIGN_KEY_REQUIRES_REVIEW",
			assertKept: func(t *testing.T, db *gorm.DB) {
				if !db.Migrator().HasTable("t_ai_config") {
					t.Fatal("blocked cleanup removed the referenced legacy table")
				}
			},
		},
		{
			name: "dependent view",
			setup: func(t *testing.T, db *gorm.DB) {
				execLegacySchemaCleanupFixtureSQL(t, db, "CREATE TABLE t_ai_config (id BIGINT PRIMARY KEY)")
				execLegacySchemaCleanupFixtureSQL(t, db, "CREATE VIEW legacy_config_view AS SELECT id FROM t_ai_config")
			},
			wantCode: "DEPENDENT_OBJECT_REQUIRES_REVIEW",
			assertKept: func(t *testing.T, db *gorm.DB) {
				if !db.Migrator().HasTable("t_ai_config") {
					t.Fatal("blocked cleanup removed a table used by a view")
				}
			},
		},
	}

	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dsn := fmt.Sprintf("file:legacy_schema_cleanup_block_%d?mode=memory&cache=shared", index)
			db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
			if err != nil {
				t.Fatalf("open sqlite: %v", err)
			}
			test.setup(t, db)
			report, err := LegacySchemaCleanupService.Inspect(db)
			if err != nil {
				t.Fatalf("inspect blocked schema: %v", err)
			}
			if report.Ready || !legacySchemaCleanupHasViolation(report, test.wantCode) {
				t.Fatalf("violations=%#v want code=%s", report.Violations, test.wantCode)
			}
			if _, err := LegacySchemaCleanupService.Execute(db, report.InventoryDigest); err == nil {
				t.Fatal("blocked schema cleanup unexpectedly executed")
			}
			test.assertKept(t, db)
		})
	}
}

func TestLegacySchemaCleanupFreshSQLiteIsNoOp(t *testing.T) {
	db, err := gorm.Open(
		sqlite.Open("file:legacy_schema_cleanup_fresh?mode=memory&cache=shared"),
		&gorm.Config{Logger: logger.Default.LogMode(logger.Silent)},
	)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	report, err := LegacySchemaCleanupService.Inspect(db)
	if err != nil {
		t.Fatalf("inspect fresh schema: %v", err)
	}
	if !report.Ready || report.ChangeCount != 0 {
		t.Fatalf("fresh schema inventory=%#v", report)
	}
	result, err := LegacySchemaCleanupService.Execute(db, report.InventoryDigest)
	if err != nil {
		t.Fatalf("execute fresh no-op cleanup: %v", err)
	}
	if len(result.AppliedSteps) != 0 {
		t.Fatalf("fresh cleanup applied steps: %#v", result.AppliedSteps)
	}
}

func TestLegacySchemaCleanupHistoricalMySQLLifecycle(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("TEST_MYSQL_DSN"))
	if dsn == "" {
		t.Skip("TEST_MYSQL_DSN is not configured")
	}
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open mysql: %v", err)
	}
	dropLegacySchemaCleanupFixture(t, db)
	t.Cleanup(func() { dropLegacySchemaCleanupFixture(t, db) })
	createLegacySchemaCleanupFixture(t, db)

	before, err := LegacySchemaCleanupService.Inspect(db)
	if err != nil {
		t.Fatalf("inspect mysql historical schema: %v", err)
	}
	if !before.Ready || before.ChangeCount != 12 {
		t.Fatalf("mysql inventory=%#v", before)
	}
	if _, err := LegacySchemaCleanupService.Execute(db, before.InventoryDigest); err != nil {
		t.Fatalf("execute mysql historical cleanup: %v", err)
	}
	assertLegacySchemaCleanupFinalSchema(t, db)
}

func createLegacySchemaCleanupFixture(t *testing.T, db *gorm.DB) {
	t.Helper()
	for _, statement := range []string{
		"CREATE TABLE t_conversation_service_session (id BIGINT PRIMARY KEY, tag_ids_json TEXT, keep_text TEXT)",
		"CREATE TABLE t_ai_agent (id BIGINT PRIMARY KEY, ai_config_id BIGINT NOT NULL DEFAULT 0, keep_text VARCHAR(100))",
		"CREATE TABLE t_agent_run_log (id BIGINT PRIMARY KEY, ai_config_id BIGINT NOT NULL DEFAULT 0, keep_text TEXT)",
		"CREATE TABLE t_ai_usage_event (id BIGINT PRIMARY KEY, ai_config_id BIGINT NOT NULL DEFAULT 0, keep_text TEXT)",
		"CREATE TABLE t_skill_run_log (id BIGINT PRIMARY KEY, ai_config_id BIGINT NOT NULL DEFAULT 0, keep_text TEXT)",
		"CREATE INDEX idx_t_ai_agent_ai_config_id ON t_ai_agent (ai_config_id)",
		"CREATE INDEX idx_t_ai_agent_keep_text ON t_ai_agent (keep_text)",
		"CREATE INDEX idx_t_agent_run_log_ai_config_id ON t_agent_run_log (ai_config_id)",
		"CREATE INDEX idx_t_ai_usage_event_ai_config_id ON t_ai_usage_event (ai_config_id)",
		"CREATE INDEX idx_t_skill_run_log_ai_config_id ON t_skill_run_log (ai_config_id)",
		"CREATE TABLE t_ai_config (id BIGINT PRIMARY KEY, api_key TEXT)",
		"CREATE TABLE t_tenant_ai_model_grant (id BIGINT PRIMARY KEY, ai_config_id BIGINT)",
		"CREATE TABLE t_store_ai_model_setting (id BIGINT PRIMARY KEY, ai_config_id BIGINT, api_key TEXT)",
		"CREATE TABLE t_conversation_tag (id BIGINT PRIMARY KEY, tag_id BIGINT)",
		"CREATE TABLE t_knowledge_document (id BIGINT PRIMARY KEY, content TEXT)",
		"CREATE TABLE t_knowledge_faq (id BIGINT PRIMARY KEY, answer TEXT)",
		"CREATE TABLE t_knowledge_chunk (id BIGINT PRIMARY KEY, content TEXT)",
		"CREATE TABLE t_cleanup_keep (id BIGINT PRIMARY KEY, value TEXT)",
	} {
		execLegacySchemaCleanupFixtureSQL(t, db, statement)
	}
	for _, statement := range []string{
		"INSERT INTO t_conversation_service_session (id, tag_ids_json, keep_text) VALUES (1, '[7]', 'keep-session')",
		"INSERT INTO t_ai_agent (id, ai_config_id, keep_text) VALUES (1, 7, 'keep-agent')",
		"INSERT INTO t_agent_run_log (id, ai_config_id, keep_text) VALUES (1, 7, 'keep-agent-log')",
		"INSERT INTO t_ai_usage_event (id, ai_config_id, keep_text) VALUES (1, 7, 'keep-usage')",
		"INSERT INTO t_skill_run_log (id, ai_config_id, keep_text) VALUES (1, 7, 'keep-skill')",
		"INSERT INTO t_ai_config (id, api_key) VALUES (7, 'must-never-be-returned')",
		"INSERT INTO t_tenant_ai_model_grant (id, ai_config_id) VALUES (1, 7)",
		"INSERT INTO t_store_ai_model_setting (id, ai_config_id, api_key) VALUES (1, 7, 'must-never-be-returned')",
		"INSERT INTO t_conversation_tag (id, tag_id) VALUES (1, 8)",
		"INSERT INTO t_knowledge_document (id, content) VALUES (1, 'customer-content')",
		"INSERT INTO t_knowledge_faq (id, answer) VALUES (1, 'customer-content')",
		"INSERT INTO t_knowledge_chunk (id, content) VALUES (1, 'customer-content')",
		"INSERT INTO t_cleanup_keep (id, value) VALUES (1, 'untouched')",
	} {
		execLegacySchemaCleanupFixtureSQL(t, db, statement)
	}
}

func assertLegacySchemaCleanupFinalSchema(t *testing.T, db *gorm.DB) {
	t.Helper()
	for _, table := range LegacySchemaCleanupFixedTables() {
		if db.Migrator().HasTable(table) {
			t.Fatalf("legacy table still exists: %s", table)
		}
	}
	for _, target := range LegacySchemaCleanupFixedColumns() {
		if db.Migrator().HasTable(target[0]) && db.Migrator().HasColumn(target[0], target[1]) {
			t.Fatalf("legacy column still exists: %s.%s", target[0], target[1])
		}
	}
	if !db.Migrator().HasIndex("t_ai_agent", "idx_t_ai_agent_keep_text") {
		t.Fatal("cleanup removed an unrelated active-table index")
	}
	for _, item := range []struct {
		table string
		want  string
	}{
		{table: "t_conversation_service_session", want: "keep-session"},
		{table: "t_ai_agent", want: "keep-agent"},
		{table: "t_agent_run_log", want: "keep-agent-log"},
		{table: "t_ai_usage_event", want: "keep-usage"},
		{table: "t_skill_run_log", want: "keep-skill"},
		{table: "t_cleanup_keep", want: "untouched"},
	} {
		var value string
		if err := db.Table(item.table).Select("keep_text").Where("id = ?", 1).Scan(&value).Error; err != nil {
			if item.table != "t_cleanup_keep" {
				t.Fatalf("read preserved row from %s: %v", item.table, err)
			}
			if err := db.Table(item.table).Select("value").Where("id = ?", 1).Scan(&value).Error; err != nil {
				t.Fatalf("read preserved row from %s: %v", item.table, err)
			}
		}
		if value != item.want {
			t.Fatalf("preserved value in %s=%q want=%q", item.table, value, item.want)
		}
	}
}

func dropLegacySchemaCleanupFixture(t *testing.T, db *gorm.DB) {
	t.Helper()
	tables := append(LegacySchemaCleanupFixedTables(), []string{
		"t_conversation_service_session",
		"t_ai_agent",
		"t_agent_run_log",
		"t_ai_usage_event",
		"t_skill_run_log",
		"t_cleanup_keep",
	}...)
	for _, table := range tables {
		if db.Migrator().HasTable(table) {
			if err := db.Migrator().DropTable(table); err != nil {
				t.Fatalf("drop mysql fixture table %s: %v", table, err)
			}
		}
	}
}

func execLegacySchemaCleanupFixtureSQL(t *testing.T, db *gorm.DB, statement string) {
	t.Helper()
	if err := db.Exec(statement).Error; err != nil {
		t.Fatalf("execute fixture SQL %q: %v", statement, err)
	}
}

func legacySchemaCleanupHasViolation(report *LegacySchemaCleanupInventory, code string) bool {
	for _, violation := range report.Violations {
		if violation.Code == code {
			return true
		}
	}
	return false
}
