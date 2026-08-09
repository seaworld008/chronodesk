package database

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mattn/go-sqlite3"
	"github.com/seaworld008/chronodesk/server/internal/models"
	gormsqlite "gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

var sqliteRecordingDriverSequence atomic.Uint64

type sqliteRecordedOperation struct {
	connectionID uint64
	kind         string
	statement    string
}

type sqliteConnectionRecorder struct {
	mu                        sync.Mutex
	nextConnectionID          uint64
	operations                []sqliteRecordedOperation
	rotateAfterForeignKeyRead bool
	rotationArmed             bool
	rotatedConnectionID       uint64
}

func (recorder *sqliteConnectionRecorder) connectionID() uint64 {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	recorder.nextConnectionID++
	return recorder.nextConnectionID
}

func (recorder *sqliteConnectionRecorder) record(
	connectionID uint64,
	kind string,
	statement string,
) {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	normalized := strings.ToLower(strings.Join(strings.Fields(statement), " "))
	recorder.operations = append(recorder.operations, sqliteRecordedOperation{
		connectionID: connectionID,
		kind:         kind,
		statement:    normalized,
	})
	if recorder.rotateAfterForeignKeyRead &&
		kind == "query" &&
		normalized == "pragma foreign_keys" {
		recorder.rotationArmed = true
		recorder.rotateAfterForeignKeyRead = false
	}
}

func (recorder *sqliteConnectionRecorder) resetSession(
	connectionID uint64,
) error {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	if !recorder.rotationArmed {
		return nil
	}
	recorder.rotationArmed = false
	recorder.rotatedConnectionID = connectionID
	return driver.ErrBadConn
}

func (recorder *sqliteConnectionRecorder) armRotation() {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	recorder.operations = nil
	recorder.rotatedConnectionID = 0
	recorder.rotationArmed = false
	recorder.rotateAfterForeignKeyRead = true
}

func (recorder *sqliteConnectionRecorder) disarmRotation() {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	recorder.rotationArmed = false
	recorder.rotateAfterForeignKeyRead = false
}

func (recorder *sqliteConnectionRecorder) clear() {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	recorder.operations = nil
	recorder.rotatedConnectionID = 0
	recorder.rotationArmed = false
	recorder.rotateAfterForeignKeyRead = false
}

func (recorder *sqliteConnectionRecorder) snapshot() (
	[]sqliteRecordedOperation,
	uint64,
) {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	return append([]sqliteRecordedOperation(nil), recorder.operations...),
		recorder.rotatedConnectionID
}

type sqliteRecordingDriver struct {
	recorder *sqliteConnectionRecorder
	base     *sqlite3.SQLiteDriver
}

func (recording *sqliteRecordingDriver) Open(
	name string,
) (driver.Conn, error) {
	connection, err := recording.base.Open(name)
	if err != nil {
		return nil, err
	}
	return &sqliteRecordingConnection{
		Conn:       connection,
		id:         recording.recorder.connectionID(),
		recorder:   recording.recorder,
		connection: connection,
	}, nil
}

type sqliteRecordingConnection struct {
	driver.Conn
	id         uint64
	recorder   *sqliteConnectionRecorder
	connection driver.Conn
}

func (connection *sqliteRecordingConnection) BeginTx(
	ctx context.Context,
	options driver.TxOptions,
) (driver.Tx, error) {
	tx, err := connection.connection.(driver.ConnBeginTx).
		BeginTx(ctx, options)
	if err == nil {
		connection.recorder.record(connection.id, "begin", "")
	}
	return tx, err
}

func (connection *sqliteRecordingConnection) ExecContext(
	ctx context.Context,
	query string,
	args []driver.NamedValue,
) (driver.Result, error) {
	result, err := connection.connection.(driver.ExecerContext).
		ExecContext(ctx, query, args)
	if err == nil {
		connection.recorder.record(connection.id, "exec", query)
	}
	return result, err
}

func (connection *sqliteRecordingConnection) QueryContext(
	ctx context.Context,
	query string,
	args []driver.NamedValue,
) (driver.Rows, error) {
	rows, err := connection.connection.(driver.QueryerContext).
		QueryContext(ctx, query, args)
	if err == nil {
		connection.recorder.record(connection.id, "query", query)
	}
	return rows, err
}

func (connection *sqliteRecordingConnection) ResetSession(
	ctx context.Context,
) error {
	if err := connection.recorder.resetSession(connection.id); err != nil {
		return err
	}
	if resetter, ok := connection.connection.(driver.SessionResetter); ok {
		return resetter.ResetSession(ctx)
	}
	return nil
}

func (connection *sqliteRecordingConnection) CheckNamedValue(
	value *driver.NamedValue,
) error {
	if checker, ok := connection.connection.(driver.NamedValueChecker); ok {
		return checker.CheckNamedValue(value)
	}
	return driver.ErrSkip
}

func (connection *sqliteRecordingConnection) Ping(ctx context.Context) error {
	if pinger, ok := connection.connection.(driver.Pinger); ok {
		return pinger.Ping(ctx)
	}
	return nil
}

func TestSQLiteStandaloneCutoverPinsEveryPRAGMAAndTransactionOperation(
	t *testing.T,
) {
	db, recorder := openRecordedLegacySQLiteReview3Database(
		t,
		"cutover",
	)
	seedLegacyWebhookCredentialPair(t, db)
	prewarmSQLiteReview3Connections(t, db, 4)
	recorder.armRotation()
	err := migrateWebhookSnapshotCredentialLifetimeContractAt(
		db,
		time.Date(2026, 8, 10, 1, 2, 3, 0, time.UTC),
	)
	recorder.disarmRotation()
	if err != nil {
		t.Fatal(err)
	}
	operations, rotated := recorder.snapshot()
	if rotated != 0 {
		t.Fatalf(
			"standalone cutover returned connection %d to the pool between initial foreign_keys read and cleanup",
			rotated,
		)
	}
	assertSQLiteReview3OperationsUseOneConnection(
		t,
		operations,
		func(operation sqliteRecordedOperation) bool {
			return operation.kind == "begin" ||
				strings.HasPrefix(operation.statement, "pragma foreign_key") ||
				strings.Contains(operation.statement, "schema_migration_checkpoints") ||
				strings.Contains(operation.statement, "webhook_delivery_snapshots") ||
				strings.Contains(operation.statement, "outbox_deliveries")
		},
	)
	assertEverySQLiteConnectionForeignKeysOn(t, db, 4)
}

func TestSQLiteRuntimeReadTransactionPinsForeignKeyPrecheckAndProjectScan(
	t *testing.T,
) {
	db, recorder := openRecordedSQLiteReview3Database(t, "runtime")
	if err := RunMigrations(db); err != nil {
		t.Fatal(err)
	}
	prewarmSQLiteReview3Connections(t, db, 4)
	recorder.armRotation()
	if err := validateWebhookCredentialRuntimeSnapshot(
		context.Background(),
		db,
	); err != nil {
		t.Fatal(err)
	}
	recorder.disarmRotation()
	operations, rotated := recorder.snapshot()
	if rotated != 0 {
		t.Fatalf(
			"runtime gate returned connection %d before its read transaction completed",
			rotated,
		)
	}
	const boundedForeignKeyCheck = `select "table", rowid, parent, fkid from pragma_foreign_key_check limit 1`
	foundBoundedForeignKeyCheck := false
	for _, operation := range operations {
		if operation.statement == boundedForeignKeyCheck {
			foundBoundedForeignKeyCheck = true
			break
		}
	}
	if !foundBoundedForeignKeyCheck {
		t.Fatalf(
			"runtime gate did not use the bounded SQLite foreign key check: %+v",
			operations,
		)
	}
	assertSQLiteReview3OperationsUseOneConnection(
		t,
		operations,
		func(operation sqliteRecordedOperation) bool {
			return operation.kind == "begin" ||
				operation.statement == "pragma foreign_keys" ||
				operation.statement == boundedForeignKeyCheck ||
				strings.Contains(operation.statement, "from `projects`")
		},
	)
}

func openRecordedLegacySQLiteReview3Database(
	t *testing.T,
	suffix string,
) (*gorm.DB, *sqliteConnectionRecorder) {
	t.Helper()
	db, recorder := openRecordedSQLiteReview3Database(t, suffix)
	if err := db.AutoMigrate(&models.SchemaMigrationCheckpoint{}); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`CREATE TABLE projects (
			id INTEGER NOT NULL,
			organization_id INTEGER NOT NULL,
			status VARCHAR(20) NOT NULL DEFAULT 'active',
			PRIMARY KEY (id),
			CONSTRAINT chk_projects_status CHECK (
				status IN ('active', 'archived')
			)
		)`,
		`CREATE UNIQUE INDEX idx_projects_scope_id
		 ON projects(organization_id, id)`,
		`CREATE TABLE domain_events (
			id TEXT PRIMARY KEY,
			organization_id INTEGER NOT NULL,
			project_id INTEGER NOT NULL
		)`,
		`CREATE TABLE webhook_delivery_snapshots (
			id TEXT PRIMARY KEY,
			created_at DATETIME NOT NULL,
			organization_id INTEGER NOT NULL,
			project_id INTEGER NOT NULL,
			config_id INTEGER NOT NULL,
			event_id TEXT NOT NULL,
			secret TEXT NOT NULL DEFAULT '',
			previous_secret TEXT NOT NULL DEFAULT '',
			access_token TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE TABLE outbox_deliveries (
			id TEXT PRIMARY KEY,
			organization_id INTEGER NOT NULL,
			project_id INTEGER NOT NULL,
			event_id TEXT NOT NULL,
			destination_type TEXT NOT NULL,
			destination_id TEXT NOT NULL,
			status VARCHAR(20) NOT NULL DEFAULT 'pending'
		)`,
	} {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
	return db, recorder
}

func openRecordedSQLiteReview3Database(
	t *testing.T,
	suffix string,
) (*gorm.DB, *sqliteConnectionRecorder) {
	t.Helper()
	recorder := &sqliteConnectionRecorder{}
	driverName := fmt.Sprintf(
		"chronodesk_task9a_sqlite_recording_%d",
		sqliteRecordingDriverSequence.Add(1),
	)
	sql.Register(driverName, &sqliteRecordingDriver{
		recorder: recorder,
		base:     &sqlite3.SQLiteDriver{},
	})
	dsn := "file:" + strings.ReplaceAll(t.Name()+"-"+suffix, "/", "-") +
		"?mode=memory&cache=shared&_foreign_keys=1"
	db, err := gorm.Open(
		gormsqlite.New(gormsqlite.Config{
			DriverName: driverName,
			DSN:        dsn,
		}),
		&gorm.Config{},
	)
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(4)
	sqlDB.SetMaxIdleConns(4)
	t.Cleanup(func() {
		if err := sqlDB.Close(); err != nil {
			t.Errorf("close recorded SQLite pool: %v", err)
		}
	})
	return db, recorder
}

func prewarmSQLiteReview3Connections(
	t *testing.T,
	db *gorm.DB,
	count int,
) {
	t.Helper()
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	connections := make([]*sql.Conn, 0, count)
	for index := 0; index < count; index++ {
		connection, err := sqlDB.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		connections = append(connections, connection)
		var one int
		if err := connection.QueryRowContext(ctx, "SELECT 1").Scan(&one); err != nil {
			t.Fatal(err)
		}
		if one != 1 {
			t.Fatalf("SQLite prewarm query = %d", one)
		}
	}
	for _, connection := range connections {
		if err := connection.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func assertSQLiteReview3OperationsUseOneConnection(
	t *testing.T,
	operations []sqliteRecordedOperation,
	include func(sqliteRecordedOperation) bool,
) {
	t.Helper()
	connectionIDs := make(map[uint64]struct{})
	matched := 0
	for _, operation := range operations {
		if !include(operation) {
			continue
		}
		matched++
		connectionIDs[operation.connectionID] = struct{}{}
	}
	if matched < 4 {
		t.Fatalf(
			"recorded only %d mutation-sensitive SQLite operations: %+v",
			matched,
			operations,
		)
	}
	if len(connectionIDs) != 1 {
		t.Fatalf(
			"mutation-sensitive SQLite operations used %d physical connections: %+v",
			len(connectionIDs),
			operations,
		)
	}
}
