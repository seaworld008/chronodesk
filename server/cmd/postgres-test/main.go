package main

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/database"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/joho/godotenv"
)

const (
	probeTimeout = 10 * time.Second

	selectOneQuery       = `SELECT 1`
	createTempTableQuery = `
		CREATE TEMP TABLE chronodesk_cloud_connection_probe (
			probe_value integer NOT NULL
		) ON COMMIT DROP`
	insertTempValueQuery = `
		INSERT INTO chronodesk_cloud_connection_probe (probe_value)
		VALUES (1)`
	selectTempValueQuery = `
		SELECT probe_value
		FROM chronodesk_cloud_connection_probe`
)

type dsnSelection struct {
	value    string
	source   string
	identity string
}

type endpointDescriptor struct {
	provider    string
	fingerprint string
}

type postgresGateDependencies struct {
	getenv            func(string) string
	validateTransport func(string, bool) error
	openDatabase      func(string) (probeDatabase, error)
}

type probeDatabase interface {
	PingContext(context.Context) error
	Conn(context.Context) (probeConnection, error)
	Close() error
}

type probeConnection interface {
	QueryRowContext(context.Context, string, ...any) probeRow
	BeginTx(context.Context, *sql.TxOptions) (probeTransaction, error)
	Close() error
}

type probeTransaction interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryRowContext(context.Context, string, ...any) probeRow
	Rollback() error
}

type probeRow interface {
	Scan(...any) error
}

type stdlibProbeDatabase struct {
	database *sql.DB
}

type stdlibProbeConnection struct {
	connection *sql.Conn
}

type stdlibProbeTransaction struct {
	transaction *sql.Tx
}

type stdlibProbeRow struct {
	row *sql.Row
}

func main() {
	// 从 server/ 目录运行时加载本机私密配置；进程中已存在的变量优先。
	_ = godotenv.Load()
	os.Exit(runPostgresGate(context.Background(), os.Stdout))
}

func runPostgresGate(parent context.Context, output io.Writer) int {
	return runPostgresGateWithDependencies(parent, output, postgresGateDependencies{
		getenv:            os.Getenv,
		validateTransport: database.ValidatePostgresTransport,
		openDatabase:      openPostgresDatabase,
	})
}

func runPostgresGateWithDependencies(
	parent context.Context,
	output io.Writer,
	dependencies postgresGateDependencies,
) int {
	fmt.Fprintln(output, "PostgreSQL 云连通性门禁")

	selection, ok := selectPostgresDSN(dependencies.getenv)
	if !ok {
		fmt.Fprintln(output, "- 未配置运行时或迁移 PostgreSQL DSN")
		fmt.Fprintln(output, "结论：失败（missing_configuration）")
		return 2
	}

	descriptor := describeEndpoint(selection.value)
	fmt.Fprintf(
		output,
		"- 身份：%s（来源 %s）\n",
		selection.identity,
		selection.source,
	)
	fmt.Fprintf(
		output,
		"- 端点：provider=%s，fingerprint=%s\n",
		descriptor.provider,
		descriptor.fingerprint,
	)

	if descriptor.provider == "local" {
		fmt.Fprintln(output, "- 云端点：失败（local_endpoint_not_allowed）")
		fmt.Fprintln(output, "结论：失败")
		return 1
	}
	if strings.TrimSpace(selection.value) != selection.value {
		fmt.Fprintln(output, "- TLS 配置：失败（invalid_configuration）")
		fmt.Fprintln(output, "结论：失败")
		return 1
	}
	if err := dependencies.validateTransport(selection.value, false); err != nil {
		fmt.Fprintf(
			output,
			"- TLS 配置：失败（%s）\n",
			classifyTransportValidationError(err),
		)
		fmt.Fprintln(output, "结论：失败")
		return 1
	}
	fmt.Fprintln(output, "- TLS 配置：通过（tls_required）")

	probe, err := dependencies.openDatabase(selection.value)
	if err != nil {
		fmt.Fprintf(
			output,
			"- 打开连接：失败（%s）\n",
			classifyPostgresError(err, "open_failed"),
		)
		fmt.Fprintln(output, "结论：失败")
		return 1
	}
	defer func() { _ = probe.Close() }()

	pingContext, cancelPing := context.WithTimeout(parent, probeTimeout)
	err = probe.PingContext(pingContext)
	cancelPing()
	if err != nil {
		fmt.Fprintf(
			output,
			"- PingContext（10s）：失败（%s）\n",
			classifyPostgresError(err, "ping_failed"),
		)
		fmt.Fprintln(output, "结论：失败")
		return 1
	}
	fmt.Fprintln(output, "- PingContext（10s）：通过（ping_ok）")

	readContext, cancelRead := context.WithTimeout(parent, probeTimeout)
	connection, err := probe.Conn(readContext)
	if err != nil {
		cancelRead()
		fmt.Fprintf(
			output,
			"- SELECT 1：失败（%s）\n",
			classifyPostgresError(err, "connection_acquire_failed"),
		)
		fmt.Fprintln(output, "结论：失败")
		return 1
	}
	var selectedValue int
	err = connection.QueryRowContext(
		readContext,
		selectOneQuery,
	).Scan(&selectedValue)
	cancelRead()
	if err != nil {
		_ = connection.Close()
		fmt.Fprintf(
			output,
			"- SELECT 1：失败（%s）\n",
			classifyPostgresError(err, "read_failed"),
		)
		fmt.Fprintln(output, "结论：失败")
		return 1
	}
	if selectedValue != 1 {
		_ = connection.Close()
		fmt.Fprintln(output, "- SELECT 1：失败（unexpected_result）")
		fmt.Fprintln(output, "结论：失败")
		return 1
	}
	fmt.Fprintln(output, "- SELECT 1：通过（read_ok）")

	writeContext, cancelWrite := context.WithTimeout(parent, probeTimeout)
	category := runTemporaryWriteRollback(writeContext, connection)
	cancelWrite()
	_ = connection.Close()
	if category != "" {
		fmt.Fprintf(
			output,
			"- 临时写入并回滚：失败（%s）\n",
			category,
		)
		fmt.Fprintln(output, "结论：失败")
		return 1
	}

	fmt.Fprintln(output, "- 临时写入并回滚：通过（rollback_ok）")
	fmt.Fprintf(
		output,
		"结论：通过（%s 身份可连接、读取并执行回滚写入）\n",
		selection.identity,
	)
	return 0
}

func selectPostgresDSN(getenv func(string) string) (dsnSelection, bool) {
	if value := getenv("DATABASE_RUNTIME_URL"); strings.TrimSpace(value) != "" {
		return dsnSelection{
			value:    value,
			source:   "DATABASE_RUNTIME_URL",
			identity: "runtime",
		}, true
	}

	for _, source := range []string{
		"DATABASE_MIGRATION_URL",
		"DATABASE_URL_UNPOOLED",
		"POSTGRES_URL_NON_POOLING",
		"DATABASE_URL",
	} {
		if value := getenv(source); strings.TrimSpace(value) != "" {
			return dsnSelection{
				value:    value,
				source:   source,
				identity: "migration",
			}, true
		}
	}
	return dsnSelection{}, false
}

func describeEndpoint(dsn string) endpointDescriptor {
	descriptor := endpointDescriptor{
		provider:    "unknown",
		fingerprint: "unknown",
	}
	config, err := pgx.ParseConfig(dsn)
	if err != nil || strings.TrimSpace(config.Host) == "" {
		return descriptor
	}

	host := strings.ToLower(strings.Trim(strings.TrimSpace(config.Host), "[]"))
	descriptor.provider = postgresProvider(host)
	digest := sha256.Sum256([]byte(net.JoinHostPort(
		host,
		strconv.Itoa(int(config.Port)),
	)))
	descriptor.fingerprint = fmt.Sprintf("sha256:%x", digest[:6])
	return descriptor
}

func postgresProvider(host string) string {
	switch {
	case isLoopbackHost(host) || strings.HasPrefix(host, "/"):
		return "local"
	case hostMatchesDomain(host, "neon.tech"):
		return "neon"
	case hostMatchesDomain(host, "supabase.co"),
		hostMatchesDomain(host, "pooler.supabase.com"):
		return "supabase"
	case hostMatchesDomain(host, "vercel-storage.com"):
		return "vercel-postgres"
	case hostMatchesDomain(host, "render.com"):
		return "render"
	case hostMatchesDomain(host, "railway.app"):
		return "railway"
	default:
		return "other"
	}
}

func hostMatchesDomain(host, domain string) bool {
	return host == domain || strings.HasSuffix(host, "."+domain)
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func classifyTransportValidationError(err error) string {
	if err == nil {
		return "none"
	}
	message := err.Error()
	switch {
	case strings.Contains(message, "requires TLS"),
		strings.Contains(message, "plaintext TLS fallback"):
		return "insecure_transport"
	case strings.Contains(message, "invalid PostgreSQL DSN"):
		return "invalid_configuration"
	case strings.Contains(message, "DSN is required"):
		return "missing_configuration"
	default:
		return "transport_validation_failed"
	}
}

func classifyPostgresError(err error, fallback string) string {
	switch {
	case err == nil:
		return "none"
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, context.DeadlineExceeded),
		errors.Is(err, os.ErrDeadlineExceeded):
		return "timeout"
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		return "connection_eof"
	case errors.Is(err, syscall.ECONNRESET):
		return "connection_reset"
	case errors.Is(err, syscall.ECONNREFUSED):
		return "connection_refused"
	case errors.Is(err, syscall.ENETUNREACH),
		errors.Is(err, syscall.EHOSTUNREACH):
		return "network_unreachable"
	}

	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) {
		switch postgresError.Code {
		case "28P01":
			return "authentication_failed"
		case "28000":
			return "authorization_failed"
		case "3D000":
			return "database_not_found"
		case "42501":
			return "permission_denied"
		case "25006":
			return "read_only_transaction"
		case "53300":
			return "connection_limit_exceeded"
		case "57P01", "57P02", "57P03":
			return "database_unavailable"
		}
		if strings.HasPrefix(postgresError.Code, "08") {
			return "connection_failed"
		}
	}

	var dnsError *net.DNSError
	if errors.As(err, &dnsError) {
		return "dns_lookup_failed"
	}
	var hostnameError x509.HostnameError
	if errors.As(err, &hostnameError) {
		return "tls_hostname_mismatch"
	}
	var authorityError x509.UnknownAuthorityError
	if errors.As(err, &authorityError) {
		return "tls_untrusted_certificate"
	}
	var recordError tls.RecordHeaderError
	if errors.As(err, &recordError) {
		return "tls_handshake_failed"
	}
	var networkError net.Error
	if errors.As(err, &networkError) && networkError.Timeout() {
		return "timeout"
	}
	return fallback
}

func runTemporaryWriteRollback(
	ctx context.Context,
	connection probeConnection,
) string {
	transaction, err := connection.BeginTx(ctx, &sql.TxOptions{
		Isolation: sql.LevelReadCommitted,
		ReadOnly:  false,
	})
	if err != nil {
		return classifyPostgresError(err, "transaction_begin_failed")
	}

	rollbackAttempted := false
	defer func() {
		if !rollbackAttempted {
			_ = transaction.Rollback()
		}
	}()

	if _, err := transaction.ExecContext(ctx, createTempTableQuery); err != nil {
		return classifyPostgresError(err, "temp_table_create_failed")
	}
	if _, err := transaction.ExecContext(ctx, insertTempValueQuery); err != nil {
		return classifyPostgresError(err, "temp_insert_failed")
	}

	var selectedValue int
	if err := transaction.QueryRowContext(
		ctx,
		selectTempValueQuery,
	).Scan(&selectedValue); err != nil {
		return classifyPostgresError(err, "temp_read_failed")
	}
	if selectedValue != 1 {
		return "temp_unexpected_result"
	}

	rollbackAttempted = true
	if err := transaction.Rollback(); err != nil {
		return classifyPostgresError(err, "rollback_failed")
	}
	return ""
}

func openPostgresDatabase(dsn string) (probeDatabase, error) {
	sqlDatabase, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, err
	}
	sqlDatabase.SetMaxOpenConns(1)
	sqlDatabase.SetMaxIdleConns(0)
	return &stdlibProbeDatabase{database: sqlDatabase}, nil
}

func (database *stdlibProbeDatabase) PingContext(ctx context.Context) error {
	return database.database.PingContext(ctx)
}

func (database *stdlibProbeDatabase) Conn(
	ctx context.Context,
) (probeConnection, error) {
	connection, err := database.database.Conn(ctx)
	if err != nil {
		return nil, err
	}
	return &stdlibProbeConnection{connection: connection}, nil
}

func (database *stdlibProbeDatabase) Close() error {
	return database.database.Close()
}

func (connection *stdlibProbeConnection) QueryRowContext(
	ctx context.Context,
	query string,
	args ...any,
) probeRow {
	return &stdlibProbeRow{
		row: connection.connection.QueryRowContext(ctx, query, args...),
	}
}

func (connection *stdlibProbeConnection) BeginTx(
	ctx context.Context,
	options *sql.TxOptions,
) (probeTransaction, error) {
	transaction, err := connection.connection.BeginTx(ctx, options)
	if err != nil {
		return nil, err
	}
	return &stdlibProbeTransaction{transaction: transaction}, nil
}

func (connection *stdlibProbeConnection) Close() error {
	return connection.connection.Close()
}

func (transaction *stdlibProbeTransaction) ExecContext(
	ctx context.Context,
	query string,
	args ...any,
) (sql.Result, error) {
	return transaction.transaction.ExecContext(ctx, query, args...)
}

func (transaction *stdlibProbeTransaction) QueryRowContext(
	ctx context.Context,
	query string,
	args ...any,
) probeRow {
	return &stdlibProbeRow{
		row: transaction.transaction.QueryRowContext(ctx, query, args...),
	}
}

func (transaction *stdlibProbeTransaction) Rollback() error {
	return transaction.transaction.Rollback()
}

func (row *stdlibProbeRow) Scan(destinations ...any) error {
	return row.row.Scan(destinations...)
}
