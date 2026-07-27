package repositories

import (
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"hash"
	"math"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"
)

var DatabaseRestoreAuditRepository = newDatabaseRestoreAuditRepository()

var databaseRestoreIdentifierPattern = regexp.MustCompile(`^[A-Za-z0-9_]+$`)

func newDatabaseRestoreAuditRepository() *databaseRestoreAuditRepository {
	return &databaseRestoreAuditRepository{}
}

type databaseRestoreAuditRepository struct{}

type DatabaseRestoreSnapshot struct {
	DatabaseDriver        string
	EndpointFingerprint   string
	ApplicationTableCount int
	TotalRows             int64
	SchemaSHA256          string
	DataSHA256            string
	MigrationSHA256       string
	MigrationRows         int64
	FailedMigrationRows   int64
	Tables                map[string]DatabaseRestoreTableSnapshot
}

type DatabaseRestoreTableSnapshot struct {
	Name         string
	RowCount     int64
	SchemaSHA256 string
	DataSHA256   string
}

type databaseRestoreSQLiteIndex struct {
	Sequence int    `gorm:"column:seq"`
	Name     string `gorm:"column:name"`
	Unique   int    `gorm:"column:unique"`
	Origin   string `gorm:"column:origin"`
	Partial  int    `gorm:"column:partial"`
}

type databaseRestoreSQLiteIndexColumn struct {
	Sequence int            `gorm:"column:seqno"`
	Column   sql.NullString `gorm:"column:name"`
}

type databaseRestoreSQLiteDDL struct {
	Type string `gorm:"column:type"`
	Name string `gorm:"column:name"`
	SQL  string `gorm:"column:sql"`
}

type databaseRestoreMySQLTrigger struct {
	Name      string `gorm:"column:trigger_name"`
	Timing    string `gorm:"column:action_timing"`
	Event     string `gorm:"column:event_manipulation"`
	Statement string `gorm:"column:action_statement"`
}

func (r *databaseRestoreAuditRepository) Capture(db *gorm.DB) (*DatabaseRestoreSnapshot, error) {
	if db == nil {
		return nil, fmt.Errorf("database restore snapshot requires a database")
	}
	endpointFingerprint, err := r.endpointFingerprint(db)
	if err != nil {
		return nil, err
	}
	tables, err := db.Migrator().GetTables()
	if err != nil {
		return nil, fmt.Errorf("list application tables failed: %w", err)
	}
	tables = filterDatabaseRestoreTables(tables)
	if len(tables) == 0 {
		return nil, fmt.Errorf("database restore snapshot found no t_ application tables")
	}

	snapshot := &DatabaseRestoreSnapshot{
		DatabaseDriver:      db.Dialector.Name(),
		EndpointFingerprint: endpointFingerprint,
		Tables:              make(map[string]DatabaseRestoreTableSnapshot, len(tables)),
	}
	schemaHasher := sha256.New()
	dataHasher := sha256.New()
	for _, table := range tables {
		tableSnapshot, captureErr := r.captureTable(db, table)
		if captureErr != nil {
			return nil, captureErr
		}
		snapshot.Tables[table] = tableSnapshot
		snapshot.TotalRows += tableSnapshot.RowCount
		writeDatabaseRestoreHashField(schemaHasher, table)
		writeDatabaseRestoreHashField(schemaHasher, tableSnapshot.SchemaSHA256)
		writeDatabaseRestoreHashField(dataHasher, table)
		writeDatabaseRestoreHashField(dataHasher, strconv.FormatInt(tableSnapshot.RowCount, 10))
		writeDatabaseRestoreHashField(dataHasher, tableSnapshot.DataSHA256)
	}
	snapshot.ApplicationTableCount = len(snapshot.Tables)
	snapshot.SchemaSHA256 = hex.EncodeToString(schemaHasher.Sum(nil))
	snapshot.DataSHA256 = hex.EncodeToString(dataHasher.Sum(nil))

	const migrationTable = "t_migration"
	migrationHasher := sha256.New()
	tableSnapshot, ok := snapshot.Tables[migrationTable]
	if !ok {
		writeDatabaseRestoreHashField(migrationHasher, migrationTable)
		writeDatabaseRestoreHashField(migrationHasher, "missing")
	} else {
		writeDatabaseRestoreHashField(migrationHasher, migrationTable)
		writeDatabaseRestoreHashField(migrationHasher, tableSnapshot.SchemaSHA256)
		writeDatabaseRestoreHashField(migrationHasher, tableSnapshot.DataSHA256)
		snapshot.MigrationRows = tableSnapshot.RowCount
		if err := db.Table(migrationTable).Where("success = ?", false).Count(&snapshot.FailedMigrationRows).Error; err != nil {
			return nil, fmt.Errorf("count failed migration records failed: %w", err)
		}
	}
	snapshot.MigrationSHA256 = hex.EncodeToString(migrationHasher.Sum(nil))
	return snapshot, nil
}

func (r *databaseRestoreAuditRepository) captureTable(
	db *gorm.DB,
	table string,
) (DatabaseRestoreTableSnapshot, error) {
	if !databaseRestoreIdentifierPattern.MatchString(table) {
		return DatabaseRestoreTableSnapshot{}, fmt.Errorf("application table uses an unsupported identifier")
	}
	columnTypes, err := db.Migrator().ColumnTypes(table)
	if err != nil {
		return DatabaseRestoreTableSnapshot{}, fmt.Errorf("inspect table %s columns failed: %w", table, err)
	}
	if len(columnTypes) == 0 {
		return DatabaseRestoreTableSnapshot{}, fmt.Errorf("application table %s has no columns", table)
	}
	sort.Slice(columnTypes, func(i, j int) bool {
		return columnTypes[i].Name() < columnTypes[j].Name()
	})
	ddlDescriptors, err := r.ddlDescriptors(db, table)
	if err != nil {
		return DatabaseRestoreTableSnapshot{}, fmt.Errorf("inspect table %s DDL failed: %w", table, err)
	}
	indexDescriptors, err := r.indexDescriptors(db, table)
	if err != nil {
		return DatabaseRestoreTableSnapshot{}, fmt.Errorf("inspect table %s indexes failed: %w", table, err)
	}

	schemaHasher := sha256.New()
	columnNames := make([]string, 0, len(columnTypes))
	for _, descriptor := range ddlDescriptors {
		writeDatabaseRestoreHashField(schemaHasher, descriptor)
	}
	for _, columnType := range columnTypes {
		columnNames = append(columnNames, columnType.Name())
		writeDatabaseRestoreHashField(schemaHasher, databaseRestoreColumnDescriptor(columnType))
	}
	for _, descriptor := range indexDescriptors {
		writeDatabaseRestoreHashField(schemaHasher, descriptor)
	}
	dataSHA256, rowCount, err := r.tableDataFingerprint(db, table, columnNames)
	if err != nil {
		return DatabaseRestoreTableSnapshot{}, err
	}
	return DatabaseRestoreTableSnapshot{
		Name:         table,
		RowCount:     rowCount,
		SchemaSHA256: hex.EncodeToString(schemaHasher.Sum(nil)),
		DataSHA256:   dataSHA256,
	}, nil
}

func (r *databaseRestoreAuditRepository) tableDataFingerprint(
	db *gorm.DB,
	table string,
	columnNames []string,
) (string, int64, error) {
	quotedColumns := make([]string, 0, len(columnNames))
	for _, column := range columnNames {
		if !databaseRestoreIdentifierPattern.MatchString(column) {
			return "", 0, fmt.Errorf("table %s uses an unsupported column identifier", table)
		}
		quotedColumns = append(quotedColumns, quoteDatabaseRestoreIdentifier(db, column))
	}
	query := "SELECT " + strings.Join(quotedColumns, ", ") +
		" FROM " + quoteDatabaseRestoreIdentifier(db, table)
	rows, err := db.Raw(query).Rows()
	if err != nil {
		return "", 0, fmt.Errorf("read table %s for restore fingerprint failed: %w", table, err)
	}
	defer rows.Close()

	var rowCount int64
	var digestXOR [sha256.Size]byte
	var digestSum [sha256.Size]byte
	values := make([]any, len(columnNames))
	destinations := make([]any, len(columnNames))
	for i := range values {
		destinations[i] = &values[i]
	}
	for rows.Next() {
		for i := range values {
			values[i] = nil
		}
		if err := rows.Scan(destinations...); err != nil {
			return "", 0, fmt.Errorf("scan table %s for restore fingerprint failed: %w", table, err)
		}
		rowHasher := sha256.New()
		for i, value := range values {
			writeDatabaseRestoreHashField(rowHasher, columnNames[i])
			writeDatabaseRestoreValue(rowHasher, value)
		}
		rowDigest := rowHasher.Sum(nil)
		for i := range digestXOR {
			digestXOR[i] ^= rowDigest[i]
		}
		addDatabaseRestoreDigest(&digestSum, rowDigest)
		rowCount++
	}
	if err := rows.Err(); err != nil {
		return "", 0, fmt.Errorf("iterate table %s for restore fingerprint failed: %w", table, err)
	}

	tableHasher := sha256.New()
	writeDatabaseRestoreHashField(tableHasher, strconv.FormatInt(rowCount, 10))
	_, _ = tableHasher.Write(digestXOR[:])
	_, _ = tableHasher.Write(digestSum[:])
	return hex.EncodeToString(tableHasher.Sum(nil)), rowCount, nil
}

func (r *databaseRestoreAuditRepository) endpointFingerprint(db *gorm.DB) (string, error) {
	hasher := sha256.New()
	switch db.Dialector.Name() {
	case "sqlite":
		var rows []struct {
			Sequence int    `gorm:"column:seq"`
			Name     string `gorm:"column:name"`
			File     string `gorm:"column:file"`
		}
		if err := db.Raw("PRAGMA database_list").Scan(&rows).Error; err != nil {
			return "", fmt.Errorf("inspect sqlite restore endpoint failed: %w", err)
		}
		for _, row := range rows {
			if row.Name != "main" {
				continue
			}
			path, err := filepath.Abs(row.File)
			if err != nil {
				return "", fmt.Errorf("resolve sqlite restore endpoint failed: %w", err)
			}
			if resolved, resolveErr := filepath.EvalSymlinks(path); resolveErr == nil {
				path = resolved
			}
			writeDatabaseRestoreHashField(hasher, "sqlite")
			writeDatabaseRestoreHashField(hasher, path)
			return hex.EncodeToString(hasher.Sum(nil)), nil
		}
		return "", fmt.Errorf("sqlite restore endpoint has no main database")
	case "mysql":
		var identity struct {
			ServerUUID   string `gorm:"column:server_uuid"`
			Hostname     string `gorm:"column:hostname"`
			Port         int    `gorm:"column:port"`
			DatabaseName string `gorm:"column:database_name"`
		}
		if err := db.Raw(
			"SELECT @@server_uuid AS server_uuid, @@hostname AS hostname, @@port AS port, DATABASE() AS database_name",
		).Scan(&identity).Error; err != nil {
			return "", fmt.Errorf("inspect mysql restore endpoint failed: %w", err)
		}
		if identity.ServerUUID == "" || identity.DatabaseName == "" {
			return "", fmt.Errorf("mysql restore endpoint identity is incomplete")
		}
		writeDatabaseRestoreHashField(hasher, "mysql")
		writeDatabaseRestoreHashField(hasher, identity.ServerUUID)
		writeDatabaseRestoreHashField(hasher, identity.Hostname)
		writeDatabaseRestoreHashField(hasher, strconv.Itoa(identity.Port))
		writeDatabaseRestoreHashField(hasher, identity.DatabaseName)
		return hex.EncodeToString(hasher.Sum(nil)), nil
	default:
		return "", fmt.Errorf("unsupported restore audit database driver: %s", db.Dialector.Name())
	}
}

func (r *databaseRestoreAuditRepository) ddlDescriptors(db *gorm.DB, table string) ([]string, error) {
	switch db.Dialector.Name() {
	case "sqlite":
		var rows []databaseRestoreSQLiteDDL
		if err := db.Raw(
			"SELECT type, name, sql FROM sqlite_master WHERE tbl_name = ? AND sql IS NOT NULL ORDER BY type ASC, name ASC",
			table,
		).Scan(&rows).Error; err != nil {
			return nil, err
		}
		ret := make([]string, 0, len(rows))
		for _, row := range rows {
			ret = append(ret, strings.Join([]string{row.Type, row.Name, row.SQL}, "|"))
		}
		return ret, nil
	case "mysql":
		var tableName string
		var createSQL string
		if err := db.Raw(
			"SHOW CREATE TABLE "+quoteDatabaseRestoreIdentifier(db, table),
		).Row().Scan(&tableName, &createSQL); err != nil {
			return nil, err
		}
		ret := []string{"table|" + tableName + "|" + createSQL}
		var triggers []databaseRestoreMySQLTrigger
		if err := db.Raw(
			`SELECT trigger_name, action_timing, event_manipulation, action_statement
			 FROM information_schema.triggers
			 WHERE trigger_schema = DATABASE() AND event_object_table = ?
			 ORDER BY trigger_name ASC`,
			table,
		).Scan(&triggers).Error; err != nil {
			return nil, err
		}
		for _, trigger := range triggers {
			ret = append(ret, strings.Join([]string{
				"trigger", trigger.Name, trigger.Timing, trigger.Event, trigger.Statement,
			}, "|"))
		}
		return ret, nil
	default:
		return nil, fmt.Errorf("unsupported restore audit database driver: %s", db.Dialector.Name())
	}
}

func (r *databaseRestoreAuditRepository) indexDescriptors(db *gorm.DB, table string) ([]string, error) {
	if db.Dialector.Name() != "sqlite" {
		indexes, err := db.Migrator().GetIndexes(table)
		if err != nil {
			return nil, err
		}
		ret := make([]string, 0, len(indexes))
		for _, index := range indexes {
			primary, primaryKnown := index.PrimaryKey()
			unique, uniqueKnown := index.Unique()
			ret = append(ret, strings.Join([]string{
				index.Name(),
				strings.Join(index.Columns(), ","),
				databaseRestoreBoolDescriptor(primary, primaryKnown),
				databaseRestoreBoolDescriptor(unique, uniqueKnown),
				index.Option(),
			}, "|"))
		}
		sort.Strings(ret)
		return ret, nil
	}

	var indexes []databaseRestoreSQLiteIndex
	if err := db.Raw(
		`SELECT seq, name, "unique", origin, partial FROM pragma_index_list(?)`,
		table,
	).Scan(&indexes).Error; err != nil {
		return nil, err
	}
	ret := make([]string, 0, len(indexes))
	for _, index := range indexes {
		var columns []databaseRestoreSQLiteIndexColumn
		if err := db.Raw(
			"SELECT seqno, name FROM pragma_index_info(?) ORDER BY seqno ASC",
			index.Name,
		).Scan(&columns).Error; err != nil {
			return nil, err
		}
		columnNames := make([]string, 0, len(columns))
		for _, column := range columns {
			if column.Column.Valid {
				columnNames = append(columnNames, column.Column.String)
			} else {
				columnNames = append(columnNames, "<expression>")
			}
		}
		ret = append(ret, strings.Join([]string{
			index.Name,
			strings.Join(columnNames, ","),
			strconv.Itoa(index.Unique),
			index.Origin,
			strconv.Itoa(index.Partial),
		}, "|"))
	}
	sort.Strings(ret)
	return ret, nil
}

func filterDatabaseRestoreTables(tables []string) []string {
	seen := make(map[string]struct{}, len(tables))
	ret := make([]string, 0, len(tables))
	for _, table := range tables {
		if !strings.HasPrefix(table, "t_") {
			continue
		}
		if _, ok := seen[table]; ok {
			continue
		}
		seen[table] = struct{}{}
		ret = append(ret, table)
	}
	sort.Strings(ret)
	return ret
}

func databaseRestoreColumnDescriptor(columnType gorm.ColumnType) string {
	columnTypeValue, columnTypeKnown := columnType.ColumnType()
	primary, primaryKnown := columnType.PrimaryKey()
	autoIncrement, autoIncrementKnown := columnType.AutoIncrement()
	length, lengthKnown := columnType.Length()
	precision, scale, decimalKnown := columnType.DecimalSize()
	nullable, nullableKnown := columnType.Nullable()
	unique, uniqueKnown := columnType.Unique()
	comment, commentKnown := columnType.Comment()
	defaultValue, defaultKnown := columnType.DefaultValue()
	scanType := ""
	if columnType.ScanType() != nil {
		scanType = columnType.ScanType().String()
	}
	return strings.Join([]string{
		columnType.Name(),
		strings.ToLower(columnType.DatabaseTypeName()),
		databaseRestoreStringDescriptor(columnTypeValue, columnTypeKnown),
		databaseRestoreBoolDescriptor(primary, primaryKnown),
		databaseRestoreBoolDescriptor(autoIncrement, autoIncrementKnown),
		databaseRestoreIntDescriptor(length, lengthKnown),
		databaseRestoreDecimalDescriptor(precision, scale, decimalKnown),
		databaseRestoreBoolDescriptor(nullable, nullableKnown),
		databaseRestoreBoolDescriptor(unique, uniqueKnown),
		databaseRestoreStringDescriptor(comment, commentKnown),
		databaseRestoreStringDescriptor(defaultValue, defaultKnown),
		scanType,
	}, "|")
}

func databaseRestoreBoolDescriptor(value, known bool) string {
	if !known {
		return "unknown"
	}
	return strconv.FormatBool(value)
}

func databaseRestoreIntDescriptor(value int64, known bool) string {
	if !known {
		return "unknown"
	}
	return strconv.FormatInt(value, 10)
}

func databaseRestoreDecimalDescriptor(precision, scale int64, known bool) string {
	if !known {
		return "unknown"
	}
	return strconv.FormatInt(precision, 10) + "," + strconv.FormatInt(scale, 10)
}

func databaseRestoreStringDescriptor(value string, known bool) string {
	if !known {
		return "unknown"
	}
	return value
}

func quoteDatabaseRestoreIdentifier(db *gorm.DB, identifier string) string {
	var builder strings.Builder
	db.Dialector.QuoteTo(&builder, identifier)
	return builder.String()
}

func writeDatabaseRestoreHashField(hasher hash.Hash, value string) {
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], uint64(len(value)))
	_, _ = hasher.Write(size[:])
	_, _ = hasher.Write([]byte(value))
}

func writeDatabaseRestoreValue(hasher hash.Hash, value any) {
	switch typed := value.(type) {
	case nil:
		writeDatabaseRestoreHashField(hasher, "nil")
	case bool:
		writeDatabaseRestoreHashField(hasher, "bool")
		writeDatabaseRestoreHashField(hasher, strconv.FormatBool(typed))
	case int:
		writeDatabaseRestoreHashField(hasher, "int")
		writeDatabaseRestoreHashField(hasher, strconv.FormatInt(int64(typed), 10))
	case int8:
		writeDatabaseRestoreHashField(hasher, "int8")
		writeDatabaseRestoreHashField(hasher, strconv.FormatInt(int64(typed), 10))
	case int16:
		writeDatabaseRestoreHashField(hasher, "int16")
		writeDatabaseRestoreHashField(hasher, strconv.FormatInt(int64(typed), 10))
	case int32:
		writeDatabaseRestoreHashField(hasher, "int32")
		writeDatabaseRestoreHashField(hasher, strconv.FormatInt(int64(typed), 10))
	case int64:
		writeDatabaseRestoreHashField(hasher, "int64")
		writeDatabaseRestoreHashField(hasher, strconv.FormatInt(typed, 10))
	case uint:
		writeDatabaseRestoreHashField(hasher, "uint")
		writeDatabaseRestoreHashField(hasher, strconv.FormatUint(uint64(typed), 10))
	case uint8:
		writeDatabaseRestoreHashField(hasher, "uint8")
		writeDatabaseRestoreHashField(hasher, strconv.FormatUint(uint64(typed), 10))
	case uint16:
		writeDatabaseRestoreHashField(hasher, "uint16")
		writeDatabaseRestoreHashField(hasher, strconv.FormatUint(uint64(typed), 10))
	case uint32:
		writeDatabaseRestoreHashField(hasher, "uint32")
		writeDatabaseRestoreHashField(hasher, strconv.FormatUint(uint64(typed), 10))
	case uint64:
		writeDatabaseRestoreHashField(hasher, "uint64")
		writeDatabaseRestoreHashField(hasher, strconv.FormatUint(typed, 10))
	case float32:
		writeDatabaseRestoreHashField(hasher, "float32")
		writeDatabaseRestoreHashField(hasher, strconv.FormatUint(uint64(math.Float32bits(typed)), 10))
	case float64:
		writeDatabaseRestoreHashField(hasher, "float64")
		writeDatabaseRestoreHashField(hasher, strconv.FormatUint(math.Float64bits(typed), 10))
	case string:
		writeDatabaseRestoreHashField(hasher, "string")
		writeDatabaseRestoreHashField(hasher, typed)
	case []byte:
		writeDatabaseRestoreHashField(hasher, "bytes")
		var size [8]byte
		binary.BigEndian.PutUint64(size[:], uint64(len(typed)))
		_, _ = hasher.Write(size[:])
		_, _ = hasher.Write(typed)
	case sql.RawBytes:
		writeDatabaseRestoreHashField(hasher, "raw-bytes")
		var size [8]byte
		binary.BigEndian.PutUint64(size[:], uint64(len(typed)))
		_, _ = hasher.Write(size[:])
		_, _ = hasher.Write(typed)
	case time.Time:
		writeDatabaseRestoreHashField(hasher, "time")
		writeDatabaseRestoreHashField(hasher, typed.UTC().Format(time.RFC3339Nano))
	default:
		writeDatabaseRestoreHashField(hasher, reflect.TypeOf(value).String())
		writeDatabaseRestoreHashField(hasher, fmt.Sprint(value))
	}
}

func addDatabaseRestoreDigest(sum *[sha256.Size]byte, digest []byte) {
	carry := uint16(0)
	for i := len(sum) - 1; i >= 0; i-- {
		value := uint16(sum[i]) + uint16(digest[i]) + carry
		sum[i] = byte(value)
		carry = value >> 8
	}
}
