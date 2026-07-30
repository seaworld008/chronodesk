package database

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/services"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestPostgresSeedDataCreatesDefaultAdministratorMembership(t *testing.T) {
	if os.Getenv("CHRONODESK_POSTGRES_INTEGRATION") != "1" {
		t.Skip("set CHRONODESK_POSTGRES_INTEGRATION=1 for the isolated PostgreSQL seed test")
	}
	rawDSN := strings.TrimSpace(os.Getenv("CHRONODESK_POSTGRES_INTEGRATION_DSN"))
	if rawDSN == "" {
		t.Fatal("CHRONODESK_POSTGRES_INTEGRATION_DSN is required")
	}
	parsed, err := url.Parse(rawDSN)
	if err != nil {
		t.Fatalf("parse integration DSN: %v", err)
	}
	host := parsed.Hostname()
	if host != "localhost" {
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			t.Fatal("seed integration test requires a loopback PostgreSQL target")
		}
	}

	adminDB, err := gorm.Open(postgres.Open(rawDSN), &gorm.Config{
		TranslateError: true,
	})
	if err != nil {
		t.Fatalf("open integration PostgreSQL: %v", err)
	}
	schema := fmt.Sprintf("chronodesk_seed_%d", time.Now().UnixNano())
	if err := adminDB.Exec("CREATE SCHEMA " + schema).Error; err != nil {
		t.Fatalf("create isolated schema: %v", err)
	}
	t.Cleanup(func() {
		if cleanupErr := adminDB.Exec(
			"DROP SCHEMA IF EXISTS " + schema + " CASCADE",
		).Error; cleanupErr != nil {
			t.Errorf("drop isolated schema: %v", cleanupErr)
		}
	})

	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	db, err := gorm.Open(postgres.Open(parsed.String()), &gorm.Config{
		TranslateError: true,
	})
	if err != nil {
		t.Fatalf("open isolated PostgreSQL schema: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("open isolated PostgreSQL pool: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := sqlDB.Close(); closeErr != nil {
			t.Errorf("close isolated PostgreSQL pool: %v", closeErr)
		}
	})

	t.Setenv("ADMIN_EMAIL", "postgres-seed-admin@example.test")
	t.Setenv("ADMIN_PASSWORD", "ChronoDesk-Test-2026!")
	t.Setenv("ENVIRONMENT", "development")
	if err := RunMigrations(db); err != nil {
		t.Fatalf("run fresh PostgreSQL migration: %v", err)
	}
	for attempt := 1; attempt <= 2; attempt++ {
		if err := SeedData(db, SeedOptions{
			EnsureInitialAdministratorMembership: services.
				EnsureBootstrapProjectAdministratorMembership,
		}); err != nil {
			t.Fatalf("PostgreSQL seed attempt %d: %v", attempt, err)
		}
	}

	var organization models.Organization
	if err := db.Where("slug = ?", DefaultOrganizationSlug).
		First(&organization).Error; err != nil {
		t.Fatalf("load default organization: %v", err)
	}
	var project models.Project
	if err := db.Where(
		"organization_id = ? AND key = ?",
		organization.ID,
		DefaultProjectKey,
	).First(&project).Error; err != nil {
		t.Fatalf("load default project: %v", err)
	}
	var administrator models.User
	if err := db.Where("role = ?", models.RoleAdmin).
		First(&administrator).Error; err != nil {
		t.Fatalf("load initial administrator: %v", err)
	}
	var memberships []models.ProjectMembership
	if err := db.Where(
		"project_id = ? AND user_id = ?",
		project.ID,
		administrator.ID,
	).Find(&memberships).Error; err != nil {
		t.Fatalf("load initial administrator membership: %v", err)
	}
	if len(memberships) != 1 ||
		memberships[0].Role != models.ProjectRoleAdmin ||
		!memberships[0].IsActive {
		t.Fatalf(
			"PostgreSQL seed membership = %+v, want one active project administrator",
			memberships,
		)
	}

	var events []models.DomainEvent
	if err := db.Where(
		"type = ?",
		"io.chronodesk.project.membership.upserted.v1",
	).Find(&events).Error; err != nil {
		t.Fatalf("load bootstrap membership events: %v", err)
	}
	var deliveries []models.OutboxDelivery
	if err := db.Find(&deliveries).Error; err != nil {
		t.Fatalf("load bootstrap outbox deliveries: %v", err)
	}
	var auditEntries []models.AuditLedgerEntry
	if err := db.Find(&auditEntries).Error; err != nil {
		t.Fatalf("load bootstrap audit entries: %v", err)
	}
	if len(events) != 1 ||
		events[0].OrganizationID != organization.ID ||
		events[0].ProjectID != project.ID ||
		events[0].ActorType != models.ActorTypeSystem ||
		events[0].ActorID != "chronodesk-bootstrap" {
		t.Fatalf("unexpected PostgreSQL bootstrap membership events: %+v", events)
	}
	if len(deliveries) != 1 ||
		deliveries[0].EventID != events[0].ID ||
		deliveries[0].DestinationType != "event_stream" {
		t.Fatalf("unexpected PostgreSQL bootstrap deliveries: %+v", deliveries)
	}
	if len(auditEntries) != 1 ||
		auditEntries[0].DomainEventID != events[0].ID ||
		auditEntries[0].ActorType != models.ActorTypeSystem ||
		auditEntries[0].ActorID != "chronodesk-bootstrap" {
		t.Fatalf("unexpected PostgreSQL bootstrap audit entries: %+v", auditEntries)
	}
}
