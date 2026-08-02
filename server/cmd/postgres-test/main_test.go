package main

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestSelectPostgresDSNPrefersRuntime(t *testing.T) {
	values := map[string]string{
		"DATABASE_RUNTIME_URL":     "runtime-dsn",
		"DATABASE_MIGRATION_URL":   "migration-dsn",
		"DATABASE_URL_UNPOOLED":    "unpooled-dsn",
		"POSTGRES_URL_NON_POOLING": "non-pooling-dsn",
		"DATABASE_URL":             "fallback-dsn",
	}

	selection, ok := selectPostgresDSN(func(name string) string {
		return values[name]
	})
	if !ok {
		t.Fatal("selectPostgresDSN() did not select configured runtime DSN")
	}
	if selection.value != "runtime-dsn" ||
		selection.source != "DATABASE_RUNTIME_URL" ||
		selection.identity != "runtime" {
		t.Fatalf("selection = %#v", selection)
	}
}

func TestSelectPostgresDSNUsesMigrationDiagnosticChain(t *testing.T) {
	sources := []string{
		"DATABASE_MIGRATION_URL",
		"DATABASE_URL_UNPOOLED",
		"POSTGRES_URL_NON_POOLING",
		"DATABASE_URL",
	}
	for index, source := range sources {
		t.Run(source, func(t *testing.T) {
			values := make(map[string]string)
			for later := index; later < len(sources); later++ {
				values[sources[later]] = sources[later] + "-dsn"
			}

			selection, ok := selectPostgresDSN(func(name string) string {
				return values[name]
			})
			if !ok {
				t.Fatal("selectPostgresDSN() did not select migration DSN")
			}
			if selection.source != source ||
				selection.identity != "migration" {
				t.Fatalf("selection = %#v, want source %s", selection, source)
			}
		})
	}
}

func TestDescribeEndpointDoesNotExposeDSNComponents(t *testing.T) {
	dsn := "postgres://runtime-user:top-secret@" +
		"ep-sensitive-name.us-east-1.aws.neon.tech:5432/secret-db" +
		"?sslmode=require"
	descriptor := describeEndpoint(dsn)
	rendered := fmt.Sprintf(
		"provider=%s fingerprint=%s",
		descriptor.provider,
		descriptor.fingerprint,
	)

	if descriptor.provider != "neon" {
		t.Fatalf("provider = %q, want neon", descriptor.provider)
	}
	if !strings.HasPrefix(descriptor.fingerprint, "sha256:") {
		t.Fatalf(
			"fingerprint = %q, want sha256 prefix",
			descriptor.fingerprint,
		)
	}
	for _, sensitive := range []string{
		"runtime-user",
		"top-secret",
		"ep-sensitive-name",
		"secret-db",
		"neon.tech",
	} {
		if strings.Contains(rendered, sensitive) {
			t.Fatalf("endpoint descriptor %q exposes %q", rendered, sensitive)
		}
	}
}

func TestClassifyPostgresError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{
			name: "authentication",
			err: &pgconn.PgError{
				Code:    "28P01",
				Message: "password authentication failed for a sensitive role",
			},
			want: "authentication_failed",
		},
		{
			name: "permission",
			err:  &pgconn.PgError{Code: "42501"},
			want: "permission_denied",
		},
		{
			name: "read only",
			err:  &pgconn.PgError{Code: "25006"},
			want: "read_only_transaction",
		},
		{
			name: "DNS",
			err: &net.DNSError{
				Err:  "not found",
				Name: "sensitive-host.example",
			},
			want: "dns_lookup_failed",
		},
		{
			name: "wrapped connection refused",
			err:  errors.Join(errors.New("sensitive DSN"), syscall.ECONNREFUSED),
			want: "connection_refused",
		},
		{
			name: "opaque fallback",
			err:  errors.New("sensitive DSN"),
			want: "ping_failed",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := classifyPostgresError(test.err, "ping_failed"); got != test.want {
				t.Fatalf(
					"classifyPostgresError() = %q, want %q",
					got,
					test.want,
				)
			}
		})
	}
}

func TestRunTemporaryWriteRollbackAlwaysRollsBack(t *testing.T) {
	tests := []struct {
		name         string
		execErrorAt  int
		queryError   error
		rollbackErr  error
		wantCategory string
	}{
		{name: "success"},
		{
			name:         "create failure",
			execErrorAt:  1,
			wantCategory: "temp_table_create_failed",
		},
		{
			name:         "insert failure",
			execErrorAt:  2,
			wantCategory: "temp_insert_failed",
		},
		{
			name:         "read failure",
			queryError:   errors.New("read failed"),
			wantCategory: "temp_read_failed",
		},
		{
			name:         "rollback failure",
			rollbackErr:  errors.New("rollback failed"),
			wantCategory: "rollback_failed",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			transaction := &fakeProbeTransaction{
				execErrorAt: test.execErrorAt,
				queryRow: fakeProbeRow{
					value: 1,
					err:   test.queryError,
				},
				rollbackError: test.rollbackErr,
			}
			connection := &fakeProbeConnection{transaction: transaction}

			category := runTemporaryWriteRollback(
				context.Background(),
				connection,
			)
			if category != test.wantCategory {
				t.Fatalf(
					"category = %q, want %q",
					category,
					test.wantCategory,
				)
			}
			if transaction.rollbackCalls != 1 {
				t.Fatalf(
					"rollback calls = %d, want 1",
					transaction.rollbackCalls,
				)
			}
		})
	}
}

func TestRunPostgresGateUsesTenSecondPingAndRedactsSecrets(t *testing.T) {
	const dsn = "postgres://sensitive-user:top-secret@" +
		"ep-sensitive-name.us-east-1.aws.neon.tech:5432/sensitive-db" +
		"?sslmode=require"

	transaction := &fakeProbeTransaction{
		queryRow: fakeProbeRow{value: 1},
	}
	connection := &fakeProbeConnection{
		queryRow:    fakeProbeRow{value: 1},
		transaction: transaction,
	}
	probe := &fakeProbeDatabase{connection: connection}
	var validatedDSN string
	var allowInsecure bool
	var openedDSN string
	var output bytes.Buffer

	exitCode := runPostgresGateWithDependencies(
		context.Background(),
		&output,
		postgresGateDependencies{
			getenv: func(name string) string {
				if name == "DATABASE_RUNTIME_URL" {
					return dsn
				}
				return ""
			},
			validateTransport: func(
				actualDSN string,
				actualAllowInsecure bool,
			) error {
				validatedDSN = actualDSN
				allowInsecure = actualAllowInsecure
				return nil
			},
			openDatabase: func(actualDSN string) (probeDatabase, error) {
				openedDSN = actualDSN
				return probe, nil
			},
		},
	)
	if exitCode != 0 {
		t.Fatalf("exit code = %d, output = %s", exitCode, output.String())
	}
	if validatedDSN != dsn || openedDSN != dsn {
		t.Fatal("gate did not validate and open the selected runtime DSN")
	}
	if allowInsecure {
		t.Fatal("cloud gate allowed insecure PostgreSQL transport")
	}
	if !probe.pingHadTenSecondDeadline {
		t.Fatal("PingContext did not receive a ten-second deadline")
	}
	if transaction.rollbackCalls != 1 {
		t.Fatalf(
			"rollback calls = %d, want 1",
			transaction.rollbackCalls,
		)
	}

	rendered := output.String()
	for _, expected := range []string{
		"身份：runtime",
		"provider=neon",
		"PingContext（10s）：通过",
		"SELECT 1：通过",
		"临时写入并回滚：通过",
	} {
		if !strings.Contains(rendered, expected) {
			t.Fatalf("output missing %q: %s", expected, rendered)
		}
	}
	for _, sensitive := range []string{
		dsn,
		"sensitive-user",
		"top-secret",
		"ep-sensitive-name",
		"sensitive-db",
		"neon.tech",
	} {
		if strings.Contains(rendered, sensitive) {
			t.Fatalf("output exposes %q: %s", sensitive, rendered)
		}
	}
}

func TestRunPostgresGateRejectsLocalEndpointsBeforeConnecting(t *testing.T) {
	tests := []struct {
		name string
		dsn  string
	}{
		{
			name: "localhost",
			dsn:  "postgres://runtime-user:test-only@localhost:5432/test-db?sslmode=require",
		},
		{
			name: "IPv4 loopback",
			dsn:  "postgres://runtime-user:test-only@127.0.0.1:5432/test-db?sslmode=require",
		},
		{
			name: "IPv6 loopback",
			dsn:  "postgres://runtime-user:test-only@[::1]:5432/test-db?sslmode=require",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var transportValidated bool
			var databaseOpened bool
			var output bytes.Buffer

			exitCode := runPostgresGateWithDependencies(
				context.Background(),
				&output,
				postgresGateDependencies{
					getenv: func(name string) string {
						if name == "DATABASE_RUNTIME_URL" {
							return test.dsn
						}
						return ""
					},
					validateTransport: func(string, bool) error {
						transportValidated = true
						return nil
					},
					openDatabase: func(string) (probeDatabase, error) {
						databaseOpened = true
						return nil, errors.New("must not open local endpoint")
					},
				},
			)

			if exitCode != 1 {
				t.Fatalf("exit code = %d, output = %s", exitCode, output.String())
			}
			if transportValidated {
				t.Fatal("local endpoint reached transport validation")
			}
			if databaseOpened {
				t.Fatal("local endpoint reached database open")
			}

			rendered := output.String()
			for _, expected := range []string{
				"provider=local",
				"local_endpoint_not_allowed",
				"结论：失败",
			} {
				if !strings.Contains(rendered, expected) {
					t.Fatalf("output missing %q: %s", expected, rendered)
				}
			}
			for _, sensitive := range []string{
				test.dsn,
				"runtime-user",
				"test-only",
				"test-db",
			} {
				if strings.Contains(rendered, sensitive) {
					t.Fatalf("output exposes %q: %s", sensitive, rendered)
				}
			}
		})
	}
}

func TestRunPostgresGateClassifiesDriverErrorWithoutLeakingIt(t *testing.T) {
	const dsn = "postgres://sensitive-user:top-secret@" +
		"db.private.example:5432/sensitive-db?sslmode=require"
	probe := &fakeProbeDatabase{
		pingError: &pgconn.PgError{
			Code:    "28P01",
			Message: "password authentication failed for sensitive-user",
		},
	}
	var output bytes.Buffer

	exitCode := runPostgresGateWithDependencies(
		context.Background(),
		&output,
		postgresGateDependencies{
			getenv: func(name string) string {
				if name == "DATABASE_MIGRATION_URL" {
					return dsn
				}
				return ""
			},
			validateTransport: func(string, bool) error {
				return nil
			},
			openDatabase: func(string) (probeDatabase, error) {
				return probe, nil
			},
		},
	)
	if exitCode != 1 {
		t.Fatalf("exit code = %d, output = %s", exitCode, output.String())
	}
	rendered := output.String()
	if !strings.Contains(rendered, "authentication_failed") ||
		!strings.Contains(rendered, "身份：migration") {
		t.Fatalf("unexpected output: %s", rendered)
	}
	for _, sensitive := range []string{
		"sensitive-user",
		"top-secret",
		"db.private.example",
		"sensitive-db",
	} {
		if strings.Contains(rendered, sensitive) {
			t.Fatalf("output exposes %q: %s", sensitive, rendered)
		}
	}
}

type fakeProbeDatabase struct {
	connection               probeConnection
	pingError                error
	pingHadTenSecondDeadline bool
}

func (database *fakeProbeDatabase) PingContext(ctx context.Context) error {
	deadline, ok := ctx.Deadline()
	if ok {
		remaining := time.Until(deadline)
		database.pingHadTenSecondDeadline =
			remaining > 9*time.Second &&
				remaining <= probeTimeout
	}
	return database.pingError
}

func (database *fakeProbeDatabase) Conn(
	context.Context,
) (probeConnection, error) {
	if database.connection == nil {
		return nil, errors.New("connection unavailable")
	}
	return database.connection, nil
}

func (database *fakeProbeDatabase) Close() error {
	return nil
}

type fakeProbeConnection struct {
	queryRow    fakeProbeRow
	transaction probeTransaction
	beginError  error
}

func (connection *fakeProbeConnection) QueryRowContext(
	context.Context,
	string,
	...any,
) probeRow {
	return connection.queryRow
}

func (connection *fakeProbeConnection) BeginTx(
	context.Context,
	*sql.TxOptions,
) (probeTransaction, error) {
	if connection.beginError != nil {
		return nil, connection.beginError
	}
	return connection.transaction, nil
}

func (connection *fakeProbeConnection) Close() error {
	return nil
}

type fakeProbeTransaction struct {
	execCalls     int
	execErrorAt   int
	queryRow      fakeProbeRow
	rollbackCalls int
	rollbackError error
}

func (transaction *fakeProbeTransaction) ExecContext(
	context.Context,
	string,
	...any,
) (sql.Result, error) {
	transaction.execCalls++
	if transaction.execCalls == transaction.execErrorAt {
		return nil, errors.New("statement failed")
	}
	return driver.RowsAffected(1), nil
}

func (transaction *fakeProbeTransaction) QueryRowContext(
	context.Context,
	string,
	...any,
) probeRow {
	return transaction.queryRow
}

func (transaction *fakeProbeTransaction) Rollback() error {
	transaction.rollbackCalls++
	return transaction.rollbackError
}

type fakeProbeRow struct {
	value int
	err   error
}

func (row fakeProbeRow) Scan(destinations ...any) error {
	if row.err != nil {
		return row.err
	}
	if len(destinations) != 1 {
		return io.ErrUnexpectedEOF
	}
	destination, ok := destinations[0].(*int)
	if !ok {
		return errors.New("destination must be *int")
	}
	*destination = row.value
	return nil
}
