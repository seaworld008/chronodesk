package database

import (
	"testing"

	"github.com/seaworld008/chronodesk/server/internal/auth"
)

func TestAuthenticationSecurityAuditIsMigratedAndRuntimeRequired(t *testing.T) {
	migrated := false
	for _, model := range schemaMigrationModels() {
		if _, ok := model.(*auth.AuthenticationSecurityAuditEvent); ok {
			migrated = true
			break
		}
	}
	if !migrated {
		t.Fatal("authentication security audit model is absent from migrations")
	}

	var requiredColumns []string
	for _, requirement := range runtimeSchemaRequirements() {
		if requirement.table == "authentication_security_audit_events" {
			requiredColumns = requirement.columns
			break
		}
	}
	for _, column := range []string{
		"id",
		"user_id",
		"event_type",
		"source",
		"request_id",
		"trace_id",
		"correlation_id",
		"created_at",
	} {
		if !containsString(requiredColumns, column) {
			t.Fatalf(
				"runtime schema does not require authentication_security_audit_events.%s",
				column,
			)
		}
	}
}

func containsString(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}
