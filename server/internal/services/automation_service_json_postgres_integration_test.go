package services

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestPostgresAutomationConfigCreationPersistsValidJSONDefaults(
	t *testing.T,
) {
	if os.Getenv("CHRONODESK_POSTGRES_INTEGRATION") != "1" {
		t.Skip(
			"set CHRONODESK_POSTGRES_INTEGRATION=1 for PostgreSQL automation JSON evidence",
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
			t.Fatal(
				"automation JSON integration test requires a loopback PostgreSQL target",
			)
		}
	}
	admin, err := gorm.Open(postgres.Open(rawDSN), &gorm.Config{
		TranslateError: true,
		Logger:         logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open PostgreSQL automation administrator: %v", err)
	}
	adminSQL, err := admin.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = adminSQL.Close() })

	schemaName := fmt.Sprintf(
		"chronodesk_automation_json_%d",
		time.Now().UnixNano(),
	)
	quotedSchema := `"` + schemaName + `"`
	if err := admin.Exec("CREATE SCHEMA " + quotedSchema).Error; err != nil {
		t.Fatalf("create automation JSON schema: %v", err)
	}
	t.Cleanup(func() {
		if cleanupErr := admin.Exec(
			"DROP SCHEMA IF EXISTS " + quotedSchema + " CASCADE",
		).Error; cleanupErr != nil {
			t.Errorf("drop automation JSON schema: %v", cleanupErr)
		}
	})

	scopedURL := *parsed
	query := scopedURL.Query()
	query.Set("search_path", schemaName)
	scopedURL.RawQuery = query.Encode()
	db, err := gorm.Open(postgres.Open(scopedURL.String()), &gorm.Config{
		TranslateError: true,
		Logger:         logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open schema-scoped automation database: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.AutoMigrate(
		&models.User{},
		&models.SLAConfig{},
		&models.TicketTemplate{},
	); err != nil {
		t.Fatalf("migrate automation JSON fixture: %v", err)
	}

	user := models.User{
		Username:     "postgres-automation-json-author",
		Email:        "postgres-automation-json-author@example.test",
		PasswordHash: "test-only",
		PlatformRole: models.PlatformRoleMember,
		Status:       models.UserStatusActive,
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	scope := models.ProjectScope{OrganizationID: 41, ProjectID: 410}
	operationContext, err := WithOperationContext(
		context.Background(),
		OperationContext{
			Scope:  scope,
			Actor:  models.HumanActor(user.ID),
			Source: SourceProtocolHumanREST,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	service := NewAutomationService(db)
	sla, err := service.CreateSLAConfig(
		operationContext,
		&models.SLAConfigRequest{
			Name:           "PostgreSQL JSON defaults",
			ResponseTime:   30,
			ResolutionTime: 120,
		},
	)
	if err != nil {
		t.Fatalf("create PostgreSQL SLA with omitted JSON fields: %v", err)
	}
	template, err := service.CreateTemplate(
		operationContext,
		&models.TicketTemplateRequest{
			Name:     "PostgreSQL JSON defaults",
			Category: "request",
		},
		user.ID,
	)
	if err != nil {
		t.Fatalf(
			"create PostgreSQL template with omitted custom fields: %v",
			err,
		)
	}
	if sla.OrganizationID != scope.OrganizationID ||
		sla.ProjectID != scope.ProjectID ||
		template.OrganizationID != scope.OrganizationID ||
		template.ProjectID != scope.ProjectID {
		t.Fatalf(
			"automation JSON rows escaped operation scope: SLA=%+v template=%+v",
			sla,
			template,
		)
	}

	var slaJSONTypes struct {
		WorkingHours    string `gorm:"column:working_hours_type"`
		EscalationRules string `gorm:"column:escalation_rules_type"`
	}
	if err := db.Raw(
		`SELECT
			jsonb_typeof(working_hours::jsonb) AS working_hours_type,
			jsonb_typeof(escalation_rules::jsonb) AS escalation_rules_type
		FROM sla_configs
		WHERE id = ? AND organization_id = ? AND project_id = ?`,
		sla.ID,
		scope.OrganizationID,
		scope.ProjectID,
	).Scan(&slaJSONTypes).Error; err != nil {
		t.Fatalf("inspect PostgreSQL SLA JSON types: %v", err)
	}
	if slaJSONTypes.WorkingHours != "object" ||
		slaJSONTypes.EscalationRules != "array" {
		t.Fatalf("unexpected PostgreSQL SLA JSON types: %+v", slaJSONTypes)
	}

	var customFieldsType string
	if err := db.Raw(
		`SELECT jsonb_typeof(custom_fields::jsonb)
		FROM ticket_templates
		WHERE id = ? AND organization_id = ? AND project_id = ?`,
		template.ID,
		scope.OrganizationID,
		scope.ProjectID,
	).Scan(&customFieldsType).Error; err != nil {
		t.Fatalf("inspect PostgreSQL template JSON type: %v", err)
	}
	if customFieldsType != "array" {
		t.Fatalf(
			"PostgreSQL template custom_fields type = %q, want array",
			customFieldsType,
		)
	}
}
