package handlers

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/seaworld008/chronodesk/server/internal/services"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestLegacyBusinessAnalyticsRedirectsWithoutCallingServices(t *testing.T) {
	gin.SetMode(gin.TestMode)
	// Nil dependencies are deliberate: a compatibility redirect must not
	// resolve project memberships or run any legacy analytics query.
	handler := NewAnalyticsHandler(nil)
	router := gin.New()
	router.GET("/business", handler.GetBusinessStats)
	router.GET("/dashboard", handler.GetDashboardStats)

	for _, path := range []string{"/business", "/dashboard"} {
		response := httptest.NewRecorder()
		router.ServeHTTP(
			response,
			httptest.NewRequest(http.MethodGet, path, nil),
		)
		if response.Code != http.StatusTemporaryRedirect {
			t.Fatalf(
				"%s status=%d body=%s",
				path,
				response.Code,
				response.Body,
			)
		}
		if location := response.Header().Get("Location"); location !=
			"/api/workbench/dashboard" {
			t.Fatalf("%s Location=%q", path, location)
		}
	}
}

func TestAnalyticsHandlersRejectNonCanonicalBoundedQueries(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := NewAnalyticsHandler(nil)
	router := gin.New()
	router.GET("/timerange", handler.GetTimeRangeStats)
	router.GET("/export", handler.ExportStats)

	tests := []string{
		"/timerange",
		"/timerange?start_date=&end_date=2026-03-31",
		"/timerange?start_date=2026-01-01&start_date=2026-01-02&end_date=2026-03-31",
		"/timerange?start_date=2026-01-01&end_date=2026-03-31&unknown=1",
		"/timerange?start_date=2026-01-01&end_date=2026-04-01",
		"/timerange?start_date=2026-01-01&end_date=2026-04-11",
		"/timerange?start_date=2026-03-31&end_date=2026-01-01",
		"/export?format=",
		"/export?format=csv",
		"/export?format=json&format=json",
		"/export?start_date=2026-01-01",
		"/export?start_date=2026-01-01&end_date=2026-04-01",
		"/export?start_date=2026-01-01&end_date=2026-04-11",
		"/export?unknown=1",
	}
	for _, target := range tests {
		response := httptest.NewRecorder()
		router.ServeHTTP(
			response,
			httptest.NewRequest(http.MethodGet, target, nil),
		)
		if response.Code != http.StatusBadRequest {
			t.Fatalf(
				"%s status=%d body=%s",
				target,
				response.Code,
				response.Body,
			)
		}
	}
}

func TestParseAnalyticsQueryAcceptsNinetyInclusiveDays(t *testing.T) {
	query, err := parseAnalyticsQuery(
		"start_date=2026-01-01&end_date=2026-03-31",
		false,
	)
	if err != nil {
		t.Fatal(err)
	}
	if query.startDate == nil || query.endDate == nil {
		t.Fatalf("parsed range = %+v", query)
	}
	if got := query.endDate.Format(time.RFC3339Nano); got !=
		"2026-03-31T23:59:59.999999999Z" {
		t.Fatalf("inclusive end = %s", got)
	}

	query, err = parseAnalyticsQuery("", true)
	if err != nil {
		t.Fatal(err)
	}
	if query.format != "json" ||
		query.startDate != nil ||
		query.endDate != nil {
		t.Fatalf("default export query = %+v", query)
	}
}

func TestAnalyticsHandlerAcceptsNinetyInclusiveDays(t *testing.T) {
	db := openAnalyticsHandlerTestDB(t)
	seedAnalyticsHandlerFixture(t, db, true)
	projectService, err := services.NewProjectService(db)
	if err != nil {
		t.Fatal(err)
	}
	handler := NewAnalyticsHandler(db, projectService)
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/timerange", func(c *gin.Context) {
		c.Set("user_id", uint(11))
		handler.GetTimeRangeStats(c)
	})
	response := httptest.NewRecorder()
	router.ServeHTTP(
		response,
		httptest.NewRequest(
			http.MethodGet,
			"/timerange?start_date=2026-01-01&end_date=2026-03-31",
			nil,
		),
	)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body)
	}
}

func TestAnalyticsHandlerRejectsMoreThanBoundedAuthorizedProjects(t *testing.T) {
	db := openAnalyticsHandlerTestDB(t)
	if err := db.Exec(`
		INSERT INTO users (id, status, platform_role)
		VALUES (11, 'active', 'platform_admin')
	`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Transaction(func(tx *gorm.DB) error {
		for index := 1; index <= services.AnalyticsMaxProjects+1; index++ {
			if err := tx.Exec(`
				INSERT INTO projects (
					id, public_id, organization_id, business_unit_id, key, name,
					description, status, ticket_sequence
				) VALUES (?, ?, 10, 20, ?, ?, '', 'active', 0)
			`,
				index,
				fmt.Sprintf(
					"00000000-0000-7000-8000-%012d",
					index,
				),
				fmt.Sprintf("P%04d", index),
				fmt.Sprintf("Project %04d", index),
			).Error; err != nil {
				return err
			}
			if err := tx.Exec(`
				INSERT INTO project_memberships (
					id, project_id, user_id, role, is_active
				) VALUES (?, ?, 11, 'project_admin', true)
			`, index, index).Error; err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	projectService, err := services.NewProjectService(db)
	if err != nil {
		t.Fatal(err)
	}
	handler := NewAnalyticsHandler(db, projectService)
	router := gin.New()
	router.GET("/timerange", func(c *gin.Context) {
		c.Set("user_id", uint(11))
		handler.GetTimeRangeStats(c)
	})
	response := httptest.NewRecorder()
	router.ServeHTTP(
		response,
		httptest.NewRequest(
			http.MethodGet,
			"/timerange?start_date=2026-01-01&end_date=2026-01-07",
			nil,
		),
	)
	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status=%d body=%s", response.Code, response.Body)
	}
}

func openAnalyticsHandlerTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(
		sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"),
		&gorm.Config{},
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`CREATE TABLE projects (
			id INTEGER PRIMARY KEY,
			public_id TEXT NOT NULL,
			created_at DATETIME,
			updated_at DATETIME,
			organization_id INTEGER NOT NULL,
			business_unit_id INTEGER NOT NULL,
			key TEXT NOT NULL,
			name TEXT NOT NULL,
			description TEXT NOT NULL,
			status TEXT NOT NULL,
			ticket_sequence INTEGER NOT NULL
		)`,
		`CREATE TABLE users (
			id INTEGER PRIMARY KEY,
			status TEXT NOT NULL,
			platform_role TEXT NOT NULL,
			deleted_at DATETIME
		)`,
		`CREATE TABLE project_memberships (
			id INTEGER PRIMARY KEY,
			project_id INTEGER NOT NULL,
			user_id INTEGER NOT NULL,
			role TEXT NOT NULL,
			is_active BOOLEAN NOT NULL,
			knowledge_contributor BOOLEAN NOT NULL DEFAULT false
		)`,
		`CREATE TABLE tickets (
			id INTEGER PRIMARY KEY,
			organization_id INTEGER NOT NULL,
			project_id INTEGER NOT NULL,
			category_id INTEGER,
			status TEXT NOT NULL,
			priority TEXT NOT NULL,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			deleted_at DATETIME,
			response_time INTEGER,
			resolution_time INTEGER
		)`,
		`CREATE TABLE ticket_comments (
			id INTEGER PRIMARY KEY,
			organization_id INTEGER NOT NULL,
			project_id INTEGER NOT NULL,
			created_at DATETIME NOT NULL,
			deleted_at DATETIME
		)`,
		`CREATE TABLE categories (
			id INTEGER PRIMARY KEY,
			organization_id INTEGER NOT NULL,
			project_id INTEGER NOT NULL,
			name TEXT NOT NULL
		)`,
		`CREATE TABLE login_histories (
			id INTEGER PRIMARY KEY,
			user_id INTEGER NOT NULL,
			login_time DATETIME NOT NULL
		)`,
	} {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func seedAnalyticsHandlerFixture(
	t *testing.T,
	db *gorm.DB,
	withMembership bool,
) {
	t.Helper()
	now := time.Now().UTC()
	for _, command := range []struct {
		query string
		args  []any
	}{
		{
			query: `INSERT INTO projects (
				id, public_id, organization_id, business_unit_id, key, name,
				description, status, ticket_sequence
			) VALUES
				(1, '00000000-0000-7000-8000-000000000001', 10, 20, 'ONE', 'One', '', 'active', 0),
				(2, '00000000-0000-7000-8000-000000000002', 10, 20, 'TWO', 'Two', '', 'active', 0)`,
		},
		{
			query: `INSERT INTO users (id, status, platform_role)
				VALUES (11, 'active', 'platform_admin')`,
		},
		{
			query: `INSERT INTO tickets (
				id, organization_id, project_id, status, priority,
				created_at, updated_at
			) VALUES
				(101, 10, 1, 'open', 'high', ?, ?),
				(102, 10, 2, 'closed', 'low', ?, ?)`,
			args: []any{now, now, now, now},
		},
	} {
		if err := db.Exec(command.query, command.args...).Error; err != nil {
			t.Fatal(err)
		}
	}
	if withMembership {
		if err := db.Exec(`
			INSERT INTO project_memberships (
				id, project_id, user_id, role, is_active
			) VALUES (1, 1, 11, 'project_admin', true)
		`).Error; err != nil {
			t.Fatal(err)
		}
	}
}
