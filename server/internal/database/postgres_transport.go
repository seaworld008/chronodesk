package database

import (
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"strings"

	"github.com/jackc/pgx/v5"
)

// ValidatePostgresTransport rejects a remote PostgreSQL connection that can
// downgrade to plaintext. Local TCP and Unix-socket development remain
// available; an operator can explicitly opt into insecure transport only for a
// controlled test environment.
func ValidatePostgresTransport(dsn string, allowInsecure bool) error {
	if strings.TrimSpace(dsn) == "" {
		return errors.New("PostgreSQL DSN is required")
	}
	parsed, err := pgx.ParseConfig(dsn)
	if err != nil {
		return fmt.Errorf("invalid PostgreSQL DSN: %w", err)
	}
	if allowInsecure || isLocalPostgresHost(parsed.Host) {
		return nil
	}
	if parsed.TLSConfig == nil {
		return errors.New(
			"remote PostgreSQL requires TLS; configure sslmode=require, verify-ca, or verify-full",
		)
	}
	for _, fallback := range parsed.Fallbacks {
		if fallback != nil && fallback.TLSConfig == nil {
			return errors.New(
				"remote PostgreSQL must not allow a plaintext TLS fallback",
			)
		}
	}
	return nil
}

func isLocalPostgresHost(host string) bool {
	host = strings.TrimSpace(host)
	if host == "" || filepath.IsAbs(host) || strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}
