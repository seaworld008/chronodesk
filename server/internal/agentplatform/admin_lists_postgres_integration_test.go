package agentplatform

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/datatypes"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestAdminListsPostgresStablePagesAndBoundCursors(t *testing.T) {
	db := openAdminListsPostgresIntegrationDB(t)
	createdAt := time.Date(2026, 7, 31, 10, 0, 0, 123000, time.UTC)
	projectA := models.Project{
		ID:             5101,
		PublicID:       "00000000-0000-7000-8500-000000005101",
		CreatedAt:      createdAt,
		UpdatedAt:      createdAt,
		OrganizationID: 510,
		BusinessUnitID: 1,
		Key:            "PGLISTA",
		Name:           "PostgreSQL Agent List A",
		Status:         models.ProjectStatusActive,
	}
	projectB := models.Project{
		ID:             5102,
		PublicID:       "00000000-0000-7000-8500-000000005102",
		CreatedAt:      createdAt,
		UpdatedAt:      createdAt,
		OrganizationID: projectA.OrganizationID,
		BusinessUnitID: 1,
		Key:            "PGLISTB",
		Name:           "PostgreSQL Agent List B",
		Status:         models.ProjectStatusActive,
	}
	if err := db.Create(&[]models.Project{projectA, projectB}).Error; err != nil {
		t.Fatalf("seed PostgreSQL list projects: %v", err)
	}

	principals := make([]models.ServicePrincipal, 0, 150)
	grants := make([]models.ProjectPrincipalGrant, 0, 150)
	for i := 1; i <= 150; i++ {
		principalID := fmt.Sprintf(
			"00000000-0000-7000-8600-%012d",
			i,
		)
		principals = append(principals, models.ServicePrincipal{
			ID:          principalID,
			CreatedAt:   createdAt,
			UpdatedAt:   createdAt,
			Name:        fmt.Sprintf("PostgreSQL Agent List %03d", i),
			Status:      models.ServicePrincipalStatusActive,
			Scopes:      datatypes.JSON([]byte(`["tickets:read"]`)),
			PolicyEpoch: 1,
		})
		grants = append(grants, models.ProjectPrincipalGrant{
			CreatedAt:          createdAt,
			UpdatedAt:          createdAt,
			ProjectID:          projectA.ID,
			ServicePrincipalID: principalID,
			Role:               models.ProjectRoleAgent,
			Scopes:             datatypes.JSON([]byte(`["tickets:read"]`)),
			IsActive:           true,
		})
	}
	if err := db.CreateInBatches(&principals, 100).Error; err != nil {
		t.Fatalf("seed PostgreSQL principals: %v", err)
	}
	if err := db.CreateInBatches(&grants, 100).Error; err != nil {
		t.Fatalf("seed PostgreSQL principal grants: %v", err)
	}

	events := make([]models.DomainEvent, 0, 152)
	for i := 1; i <= 151; i++ {
		events = append(events, models.DomainEvent{
			ID: fmt.Sprintf(
				"00000000-0000-7000-8700-%012d",
				i,
			),
			CreatedAt:       createdAt,
			OrganizationID:  projectA.OrganizationID,
			ProjectID:       projectA.ID,
			SpecVersion:     "1.0",
			Source:          "/chronodesk/postgres/admin-list-test",
			Type:            "io.chronodesk.test.admin-list.v1",
			Time:            createdAt,
			DataContentType: "application/json",
			Data:            datatypes.JSON([]byte(`{}`)),
			ActorType:       models.ActorTypeSystem,
			ActorID:         "admin-list-postgres-test",
			ResourceVersion: 1,
		})
	}
	events = append(events, models.DomainEvent{
		ID:              "00000000-0000-7000-8800-000000000001",
		CreatedAt:       createdAt,
		OrganizationID:  projectB.OrganizationID,
		ProjectID:       projectB.ID,
		SpecVersion:     "1.0",
		Source:          "/chronodesk/postgres/admin-list-test",
		Type:            "io.chronodesk.test.admin-list.foreign.v1",
		Time:            createdAt,
		DataContentType: "application/json",
		Data:            datatypes.JSON([]byte(`{}`)),
		ActorType:       models.ActorTypeSystem,
		ActorID:         "admin-list-postgres-test",
		ResourceVersion: 1,
	})
	if err := db.CreateInBatches(&events, 100).Error; err != nil {
		t.Fatalf("seed PostgreSQL events: %v", err)
	}

	service, err := NewAdminListService(
		db,
		[]byte("admin-list-postgres-stable-cursor-key-20260731"),
	)
	if err != nil {
		t.Fatal(err)
	}
	firstPrincipals, err := service.ListPrincipals(
		adminListTestContext(t, projectA.Scope()),
		projectA.Scope(),
		AdminPageQuery{Page: 1, PageSize: 100},
	)
	if err != nil {
		t.Fatal(err)
	}
	secondPrincipals, err := service.ListPrincipals(
		adminListTestContext(t, projectA.Scope()),
		projectA.Scope(),
		AdminPageQuery{Page: 2, PageSize: 100},
	)
	if err != nil {
		t.Fatal(err)
	}
	if firstPrincipals.Total != 150 ||
		firstPrincipals.TotalPages != 2 ||
		len(firstPrincipals.Items) != 100 ||
		len(secondPrincipals.Items) != 50 {
		t.Fatalf(
			"unexpected PostgreSQL principal pages: first=%+v second=%+v",
			firstPrincipals,
			secondPrincipals,
		)
	}
	principalIDs := make(map[string]struct{}, 150)
	for _, principal := range append(
		firstPrincipals.Items,
		secondPrincipals.Items...,
	) {
		if _, duplicate := principalIDs[principal.ID]; duplicate {
			t.Fatalf("duplicate PostgreSQL principal %q", principal.ID)
		}
		principalIDs[principal.ID] = struct{}{}
	}
	if len(principalIDs) != 150 {
		t.Fatalf(
			"PostgreSQL principal unique count=%d, want 150",
			len(principalIDs),
		)
	}

	firstEvents, err := service.ListDomainEvents(
		adminListTestContext(t, projectA.Scope()),
		projectA.Scope(),
		AdminCursorQuery{Limit: 100},
	)
	if err != nil {
		t.Fatal(err)
	}
	secondEvents, err := service.ListDomainEvents(
		adminListTestContext(t, projectA.Scope()),
		projectA.Scope(),
		AdminCursorQuery{
			Limit:  100,
			Cursor: firstEvents.NextCursor,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(firstEvents.Items) != 100 ||
		!firstEvents.HasMore ||
		len(secondEvents.Items) != 51 ||
		secondEvents.HasMore {
		t.Fatalf(
			"unexpected PostgreSQL event pages: first=%+v second=%+v",
			firstEvents,
			secondEvents,
		)
	}
	eventIDs := make(map[string]struct{}, 151)
	for _, event := range append(firstEvents.Items, secondEvents.Items...) {
		if event.Type == "io.chronodesk.test.admin-list.foreign.v1" {
			t.Fatal("cross-project PostgreSQL event leaked")
		}
		if _, duplicate := eventIDs[event.ID]; duplicate {
			t.Fatalf("duplicate PostgreSQL event %q", event.ID)
		}
		eventIDs[event.ID] = struct{}{}
	}
	if len(eventIDs) != 151 {
		t.Fatalf(
			"PostgreSQL event unique count=%d, want 151",
			len(eventIDs),
		)
	}

	tampered := firstEvents.NextCursor
	replacement := byte('A')
	if tampered[len(tampered)-1] == replacement {
		replacement = 'B'
	}
	tampered = tampered[:len(tampered)-1] + string(replacement)
	for name, test := range map[string]struct {
		scope  models.ProjectScope
		cursor string
	}{
		"tamper": {
			scope:  projectA.Scope(),
			cursor: tampered,
		},
		"cross-project": {
			scope:  projectB.Scope(),
			cursor: firstEvents.NextCursor,
		},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := service.ListDomainEvents(
				adminListTestContext(t, test.scope),
				test.scope,
				AdminCursorQuery{Limit: 100, Cursor: test.cursor},
			); !errors.Is(err, ErrInvalidAdminListCursor) {
				t.Fatalf(
					"PostgreSQL cursor rejection error=%v, want ErrInvalidAdminListCursor",
					err,
				)
			}
		})
	}
}

func openAdminListsPostgresIntegrationDB(t *testing.T) *gorm.DB {
	t.Helper()
	if os.Getenv("CHRONODESK_POSTGRES_INTEGRATION") != "1" {
		t.Skip(
			"set CHRONODESK_POSTGRES_INTEGRATION=1 for PostgreSQL administrator list evidence",
		)
	}
	rawDSN := strings.TrimSpace(
		os.Getenv("CHRONODESK_POSTGRES_INTEGRATION_DSN"),
	)
	if rawDSN == "" {
		t.Fatal("CHRONODESK_POSTGRES_INTEGRATION_DSN is required")
	}
	parsed, err := url.Parse(rawDSN)
	if err != nil || parsed.Hostname() == "" {
		t.Fatal("parse PostgreSQL integration DSN: invalid URL")
	}
	host := parsed.Hostname()
	if host != "localhost" {
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			t.Fatal(
				"PostgreSQL administrator list test requires a loopback target",
			)
		}
	}

	suffix := fmt.Sprintf("%x", time.Now().UnixNano())
	schemaName := "chronodesk_admin_lists_" + suffix
	quotedSchema := `"` + strings.ReplaceAll(schemaName, `"`, `""`) + `"`
	config := &gorm.Config{
		TranslateError: true,
		Logger:         logger.Default.LogMode(logger.Silent),
	}
	adminDB, err := gorm.Open(postgres.Open(rawDSN), config)
	if err != nil {
		t.Fatalf("open PostgreSQL administrator list database: %v", err)
	}
	adminSQL, err := adminDB.DB()
	if err != nil {
		t.Fatal(err)
	}
	schemaCreated := false
	var runtimeSQL interface{ Close() error }
	t.Cleanup(func() {
		if runtimeSQL != nil {
			_ = runtimeSQL.Close()
		}
		if schemaCreated {
			_ = adminDB.Exec(
				"DROP SCHEMA IF EXISTS " + quotedSchema + " CASCADE",
			).Error
		}
		_ = adminSQL.Close()
	})
	if err := adminDB.Exec("CREATE SCHEMA " + quotedSchema).Error; err != nil {
		t.Fatalf("create PostgreSQL administrator list schema: %v", err)
	}
	schemaCreated = true

	runtimeURL := *parsed
	query := runtimeURL.Query()
	query.Set("search_path", schemaName)
	query.Set("application_name", "chronodesk-admin-list-"+suffix)
	query.Set("connect_timeout", "3")
	runtimeURL.RawQuery = query.Encode()
	db, err := gorm.Open(postgres.Open(runtimeURL.String()), config)
	if err != nil {
		t.Fatalf("open scoped PostgreSQL administrator list database: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	runtimeSQL = sqlDB
	sqlDB.SetMaxOpenConns(4)
	sqlDB.SetMaxIdleConns(4)

	tableOnly := db.Session(&gorm.Session{NewDB: true})
	tableOnly.Config.IgnoreRelationshipsWhenMigrating = true
	if err := tableOnly.AutoMigrate(
		&models.Project{},
		&models.SystemConfig{},
		&models.ServicePrincipal{},
		&models.ProjectPrincipalGrant{},
		&models.DomainEvent{},
	); err != nil {
		t.Fatalf("migrate PostgreSQL administrator list schema: %v", err)
	}
	return db
}
