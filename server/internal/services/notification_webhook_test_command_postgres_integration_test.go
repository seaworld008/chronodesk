package services

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/scopeddb"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestWebhookTestCommandUnderNonOwnerPostgresForceRLS(t *testing.T) {
	if os.Getenv("CHRONODESK_POSTGRES_INTEGRATION") != "1" {
		t.Skip(
			"set CHRONODESK_POSTGRES_INTEGRATION=1 for PostgreSQL Webhook FORCE RLS evidence",
		)
	}
	rawDSN := strings.TrimSpace(
		os.Getenv("CHRONODESK_POSTGRES_INTEGRATION_DSN"),
	)
	if rawDSN == "" {
		t.Fatal("CHRONODESK_POSTGRES_INTEGRATION_DSN is required")
	}
	parsed, err := url.Parse(rawDSN)
	if err != nil {
		t.Fatalf("parse PostgreSQL integration DSN: %v", err)
	}
	host := parsed.Hostname()
	if host != "localhost" {
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			t.Fatal("PostgreSQL Webhook integration test requires a loopback target")
		}
	}

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	schemaName := "chronodesk_webhook_test_" + suffix
	roleName := "chronodesk_webhook_runtime_" + suffix
	rolePassword := "ChronoDeskWebhook" + suffix + "!"
	quotedSchema := quoteWebhookPostgresIdentifier(schemaName)
	quotedRole := quoteWebhookPostgresIdentifier(roleName)
	silentConfig := &gorm.Config{
		TranslateError: true,
		Logger:         logger.Default.LogMode(logger.Silent),
	}
	admin, err := gorm.Open(postgres.Open(rawDSN), silentConfig)
	if err != nil {
		t.Fatalf("open PostgreSQL integration administrator: %v", err)
	}
	adminSQL, err := admin.DB()
	if err != nil {
		t.Fatal(err)
	}
	var adminScopedSQL, runtimeSQL *sql.DB
	roleCreated := false
	schemaCreated := false
	t.Cleanup(func() {
		if runtimeSQL != nil {
			_ = runtimeSQL.Close()
		}
		if adminScopedSQL != nil {
			_ = adminScopedSQL.Close()
		}
		if schemaCreated {
			_ = admin.Exec(
				"DROP SCHEMA IF EXISTS " + quotedSchema + " CASCADE",
			).Error
		}
		if roleCreated {
			_ = admin.Exec("DROP ROLE IF EXISTS " + quotedRole).Error
		}
		_ = adminSQL.Close()
	})
	if err := admin.Exec("CREATE SCHEMA " + quotedSchema).Error; err != nil {
		t.Fatalf("create PostgreSQL Webhook schema: %v", err)
	}
	schemaCreated = true

	adminScopedURL := *parsed
	adminQuery := adminScopedURL.Query()
	adminQuery.Set("search_path", schemaName)
	adminScopedURL.RawQuery = adminQuery.Encode()
	adminScoped, err := gorm.Open(
		postgres.Open(adminScopedURL.String()),
		silentConfig,
	)
	if err != nil {
		t.Fatalf("open schema-scoped PostgreSQL administrator: %v", err)
	}
	adminScopedDB, err := adminScoped.DB()
	if err != nil {
		t.Fatal(err)
	}
	adminScopedSQL = adminScopedDB
	tableOnly := adminScoped.Session(&gorm.Session{NewDB: true})
	tableOnly.Config.IgnoreRelationshipsWhenMigrating = true
	if err := tableOnly.AutoMigrate(
		&models.Project{},
		&models.User{},
		&models.ProjectMembership{},
		&models.WebhookConfig{},
		&models.WebhookDeliverySnapshot{},
		&models.DomainEvent{},
		&models.OutboxDelivery{},
		&models.AuditChainHead{},
		&models.AuditLedgerEntry{},
	); err != nil {
		t.Fatalf("migrate PostgreSQL Webhook command schema: %v", err)
	}

	now := time.Now().UTC().Truncate(time.Microsecond)
	project := models.Project{
		ID:             101,
		PublicID:       "00000000-0000-7000-8000-000000000101",
		CreatedAt:      now,
		UpdatedAt:      now,
		OrganizationID: 10,
		BusinessUnitID: 1,
		Key:            "HOOK",
		Name:           "Webhook Test",
		Status:         models.ProjectStatusActive,
	}
	user := models.User{
		ID:           7,
		CreatedAt:    now,
		UpdatedAt:    now,
		Username:     "webhook-runtime-user",
		Email:        "webhook-runtime-user@example.test",
		PasswordHash: "test-only-password-hash",
		PlatformRole: models.PlatformRoleMember,
		Status:       models.UserStatusActive,
	}
	membership := models.ProjectMembership{
		ID:        701,
		CreatedAt: now,
		UpdatedAt: now,
		Version:   1,
		ProjectID: project.ID,
		UserID:    user.ID,
		Role:      models.ProjectRoleManager,
		IsActive:  true,
	}
	config := models.WebhookConfig{
		ID:             9001,
		CreatedAt:      now,
		UpdatedAt:      now,
		OrganizationID: project.OrganizationID,
		ProjectID:      project.ID,
		Name:           "FORCE RLS test target",
		Provider:       models.WebhookProviderCustom,
		WebhookURL:     "https://force-rls.example.test/callback",
		Status:         models.WebhookStatusActive,
		Secret:         "sealed-force-rls-envelope",
		EnabledEventsObj: []models.WebhookEventType{
			models.WebhookEventSystemAlert,
		},
		RetryCount: 2,
		CreatedBy:  user.ID,
	}
	for _, fixture := range []struct {
		name  string
		value any
	}{
		{name: "project", value: &project},
		{name: "user", value: &user},
		{name: "membership", value: &membership},
		{name: "config", value: &config},
	} {
		if err := adminScoped.Create(fixture.value).Error; err != nil {
			t.Fatalf(
				"seed PostgreSQL Webhook %s: %v",
				fixture.name,
				err,
			)
		}
	}

	projectTables := []string{
		"webhook_configs",
		"webhook_delivery_snapshots",
		"domain_events",
		"outbox_deliveries",
		"audit_chain_heads",
		"audit_ledger_entries",
	}
	for _, tableName := range projectTables {
		quotedTable := quoteWebhookPostgresIdentifier(tableName)
		if err := adminScoped.Exec(
			"ALTER TABLE " + quotedTable +
				" ENABLE ROW LEVEL SECURITY",
		).Error; err != nil {
			t.Fatal(err)
		}
		if err := adminScoped.Exec(
			"ALTER TABLE " + quotedTable +
				" FORCE ROW LEVEL SECURITY",
		).Error; err != nil {
			t.Fatal(err)
		}
		predicate := `(organization_id = NULLIF(current_setting(` +
			`'chronodesk.organization_id', true), '')::bigint AND ` +
			`project_id = NULLIF(current_setting(` +
			`'chronodesk.project_id', true), '')::bigint)`
		if err := adminScoped.Exec(
			"CREATE POLICY chronodesk_project_scope ON " + quotedTable +
				" FOR ALL TO PUBLIC USING " + predicate +
				" WITH CHECK " + predicate,
		).Error; err != nil {
			t.Fatal(err)
		}
	}

	if err := admin.Exec(
		"CREATE ROLE " + quotedRole +
			" LOGIN NOINHERIT NOSUPERUSER NOBYPASSRLS PASSWORD " +
			quoteWebhookPostgresLiteral(rolePassword),
	).Error; err != nil {
		t.Fatalf("create PostgreSQL Webhook runtime role: %v", err)
	}
	roleCreated = true
	if err := adminScoped.Exec(
		"GRANT USAGE ON SCHEMA " + quotedSchema + " TO " + quotedRole,
	).Error; err != nil {
		t.Fatal(err)
	}
	if err := adminScoped.Exec(
		"GRANT SELECT ON ALL TABLES IN SCHEMA " +
			quotedSchema + " TO " + quotedRole,
	).Error; err != nil {
		t.Fatal(err)
	}
	if err := adminScoped.Exec(
		"GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA " +
			quotedSchema + " TO " + quotedRole,
	).Error; err != nil {
		t.Fatal(err)
	}
	if err := adminScoped.Exec(
		"GRANT UPDATE ON " +
			quoteWebhookPostgresIdentifier("projects") + ", " +
			quoteWebhookPostgresIdentifier("users") + ", " +
			quoteWebhookPostgresIdentifier("project_memberships") + ", " +
			quoteWebhookPostgresIdentifier("webhook_configs") + ", " +
			quoteWebhookPostgresIdentifier("audit_chain_heads") +
			" TO " + quotedRole,
	).Error; err != nil {
		t.Fatal(err)
	}
	if err := adminScoped.Exec(
		"GRANT INSERT ON " +
			quoteWebhookPostgresIdentifier("webhook_delivery_snapshots") + ", " +
			quoteWebhookPostgresIdentifier("domain_events") + ", " +
			quoteWebhookPostgresIdentifier("outbox_deliveries") + ", " +
			quoteWebhookPostgresIdentifier("audit_chain_heads") + ", " +
			quoteWebhookPostgresIdentifier("audit_ledger_entries") +
			" TO " + quotedRole,
	).Error; err != nil {
		t.Fatal(err)
	}

	runtimeURL := adminScopedURL
	runtimeURL.User = url.UserPassword(roleName, rolePassword)
	runtimeDB, err := gorm.Open(
		postgres.Open(runtimeURL.String()),
		silentConfig,
	)
	if err != nil {
		t.Fatalf("open non-owner PostgreSQL Webhook runtime: %v", err)
	}
	runtimeDBSQL, err := runtimeDB.DB()
	if err != nil {
		t.Fatal(err)
	}
	runtimeSQL = runtimeDBSQL
	assertWebhookPostgresRuntimeRole(t, runtimeDB, roleName)

	var unscopedConfigs int64
	if err := runtimeDB.Model(&models.WebhookConfig{}).
		Count(&unscopedConfigs).Error; err != nil {
		t.Fatal(err)
	}
	if unscopedConfigs != 0 {
		t.Fatalf(
			"FORCE RLS exposed %d unscoped Webhook configurations",
			unscopedConfigs,
		)
	}

	projectService, err := NewProjectService(runtimeDB)
	if err != nil {
		t.Fatal(err)
	}
	auditLedger, err := NewAuditLedgerService(runtimeDB)
	if err != nil {
		t.Fatal(err)
	}
	native := NewAgentNativeService(
		runtimeDB,
		AgentNativeOptions{AuditLedger: auditLedger},
	)
	var httpAttempts atomic.Int32
	notificationService := NewNotificationServiceWithClientFactory(
		runtimeDB,
		nil,
		WebhookClientFactoryFunc(func(
			context.Context,
			*url.URL,
			time.Duration,
		) (*http.Client, error) {
			httpAttempts.Add(1)
			return nil, errors.New("HTTP is forbidden in the Human command")
		}),
	)
	notificationService.ConfigureWebhookTestCommands(projectService, native)
	commandContext, err := WithOperationContext(
		context.Background(),
		OperationContext{
			Scope:         project.Scope(),
			Actor:         models.HumanActor(user.ID),
			Source:        SourceProtocolHumanREST,
			TraceID:       "trace-webhook-force-rls",
			CorrelationID: "correlation-webhook-force-rls",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := notificationService.TestWebhook(
		commandContext,
		project.Scope(),
		config.ID,
	)
	if err != nil {
		t.Fatalf("queue FORCE RLS Webhook test delivery: %v", err)
	}
	if receipt == nil ||
		receipt.Status != "queued" ||
		receipt.ConfigID != config.ID ||
		httpAttempts.Load() != 0 {
		t.Fatalf(
			"FORCE RLS queued receipt=%+v HTTP attempts=%d",
			receipt,
			httpAttempts.Load(),
		)
	}

	assertWebhookPostgresCommittedIntent(
		t,
		runtimeDB,
		commandContext,
		project.Scope(),
		receipt,
		config,
	)

	if err := adminScoped.Model(&models.ProjectMembership{}).
		Where("id = ?", membership.ID).
		Update("is_active", false).Error; err != nil {
		t.Fatal(err)
	}
	revokedReceipt, err := notificationService.TestWebhook(
		commandContext,
		project.Scope(),
		config.ID,
	)
	if !errors.Is(err, ErrWebhookTestAccessDenied) ||
		!errors.Is(err, ErrProjectAccessDenied) {
		t.Fatalf("revoked PostgreSQL Human error = %v", err)
	}
	if revokedReceipt != nil || httpAttempts.Load() != 0 {
		t.Fatalf(
			"revoked PostgreSQL Human receipt=%+v HTTP attempts=%d",
			revokedReceipt,
			httpAttempts.Load(),
		)
	}
	var deliveryCount int64
	if err := scopeddb.WithProjectScopeContextTransaction(
		commandContext,
		runtimeDB,
		project.Scope(),
		func(scopedContext context.Context) error {
			return runtimeDB.WithContext(scopedContext).
				Model(&models.OutboxDelivery{}).
				Count(&deliveryCount).Error
		},
	); err != nil {
		t.Fatal(err)
	}
	if deliveryCount != 1 {
		t.Fatalf(
			"revoked PostgreSQL Human changed durable delivery count to %d",
			deliveryCount,
		)
	}
}

func assertWebhookPostgresCommittedIntent(
	t *testing.T,
	db *gorm.DB,
	ctx context.Context,
	scope models.ProjectScope,
	receipt *WebhookTestReceipt,
	config models.WebhookConfig,
) {
	t.Helper()
	if err := scopeddb.WithProjectScopeContextTransaction(
		ctx,
		db,
		scope,
		func(scopedContext context.Context) error {
			var snapshot models.WebhookDeliverySnapshot
			if err := db.WithContext(scopedContext).
				Where("id = ?", receipt.SnapshotID).
				Take(&snapshot).Error; err != nil {
				return err
			}
			if snapshot.EventID != receipt.EventID ||
				snapshot.ConfigID != config.ID ||
				snapshot.WebhookURL != config.WebhookURL ||
				snapshot.Secret != config.Secret ||
				!snapshot.ConfigUpdatedAt.Equal(config.UpdatedAt.UTC()) {
				return fmt.Errorf("invalid frozen Webhook snapshot: %+v", snapshot)
			}
			var event models.DomainEvent
			if err := db.WithContext(scopedContext).
				Where("id = ?", receipt.EventID).
				Take(&event).Error; err != nil {
				return err
			}
			if event.ActorType != models.ActorTypeHuman ||
				event.ActorID != models.HumanActor(config.CreatedBy).ID ||
				event.ConfigurationVersion != receipt.ConfigurationVersion {
				return fmt.Errorf("invalid Webhook test event: %+v", event)
			}
			var ledgerEntry models.AuditLedgerEntry
			if err := db.WithContext(scopedContext).
				Where("domain_event_id = ?", receipt.EventID).
				Take(&ledgerEntry).Error; err != nil {
				return err
			}
			if ledgerEntry.EventType != event.Type ||
				ledgerEntry.ActorType != event.ActorType ||
				ledgerEntry.ActorID != event.ActorID ||
				ledgerEntry.ConfigurationVersion !=
					event.ConfigurationVersion {
				return fmt.Errorf(
					"invalid Webhook test audit ledger entry: %+v",
					ledgerEntry,
				)
			}
			var delivery models.OutboxDelivery
			if err := db.WithContext(scopedContext).
				Where("id = ?", receipt.DeliveryID).
				Take(&delivery).Error; err != nil {
				return err
			}
			if delivery.EventID != event.ID ||
				delivery.DestinationType != "webhook" ||
				delivery.DestinationID !=
					webhookSnapshotDestinationPrefix+snapshot.ID ||
				delivery.Status != models.OutboxDeliveryPending {
				return fmt.Errorf("invalid Webhook Outbox delivery: %+v", delivery)
			}
			return nil
		},
	); err != nil {
		t.Fatalf("verify committed FORCE RLS Webhook intent: %v", err)
	}
}

func assertWebhookPostgresRuntimeRole(
	t *testing.T,
	db *gorm.DB,
	roleName string,
) {
	t.Helper()
	var state struct {
		CurrentUser string `gorm:"column:current_user"`
		Superuser   bool
		BypassRLS   bool `gorm:"column:bypass_rls"`
		TableOwner  string
		RLSEnabled  bool `gorm:"column:rls_enabled"`
		RLSForced   bool `gorm:"column:rls_forced"`
	}
	if err := db.Raw(`
		SELECT
			current_user,
			role.rolsuper AS superuser,
			role.rolbypassrls AS bypass_rls,
			owner.rolname AS table_owner,
			table_state.relrowsecurity AS rls_enabled,
			table_state.relforcerowsecurity AS rls_forced
		FROM pg_roles AS role
		JOIN pg_class AS table_state
			ON table_state.relname = 'webhook_configs'
		JOIN pg_namespace AS namespace
			ON namespace.oid = table_state.relnamespace
			AND namespace.nspname = current_schema()
		JOIN pg_roles AS owner ON owner.oid = table_state.relowner
		WHERE role.rolname = current_user
	`).Scan(&state).Error; err != nil {
		t.Fatal(err)
	}
	if state.CurrentUser != roleName ||
		state.Superuser ||
		state.BypassRLS ||
		state.TableOwner == roleName ||
		!state.RLSEnabled ||
		!state.RLSForced {
		t.Fatalf("Webhook runtime role is not least privilege: %+v", state)
	}
}

func quoteWebhookPostgresIdentifier(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}

func quoteWebhookPostgresLiteral(value string) string {
	return `'` + strings.ReplaceAll(value, `'`, `''`) + `'`
}
