package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/services"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestAnalyticsHandlerResolvesOnlyCurrentHumanMemberships(t *testing.T) {
	db := openAnalyticsHandlerTestDB(t)
	seedAnalyticsHandlerFixture(t, db, true)
	projectService, err := services.NewProjectService(db)
	if err != nil {
		t.Fatal(err)
	}
	handler := NewAnalyticsHandler(db, projectService)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/business", func(c *gin.Context) {
		c.Set("user_id", uint(11))
		handler.GetBusinessStats(c)
	})
	response := httptest.NewRecorder()
	router.ServeHTTP(
		response,
		httptest.NewRequest(http.MethodGet, "/business", nil),
	)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body)
	}
	var envelope struct {
		Data struct {
			TicketStats struct {
				Total int64 `json:"total"`
			} `json:"ticket_stats"`
			MembershipStats struct {
				Total int64 `json:"total"`
			} `json:"membership_stats"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Data.TicketStats.Total != 1 ||
		envelope.Data.MembershipStats.Total != 1 {
		t.Fatalf("unauthorized project leaked into analytics: %+v", envelope.Data)
	}
}

func TestAnalyticsHandlerWithoutMembershipFailsBeforeBusinessQuery(
	t *testing.T,
) {
	db := openAnalyticsHandlerTestDB(t)
	seedAnalyticsHandlerFixture(t, db, false)
	projectService, err := services.NewProjectService(db)
	if err != nil {
		t.Fatal(err)
	}
	handler := NewAnalyticsHandler(db, projectService)
	var ticketQueries atomic.Int64
	const callbackName = "test:analytics_handler_ticket_query"
	if err := db.Callback().Query().Before("gorm:query").
		Register(callbackName, func(tx *gorm.DB) {
			if tx.Statement != nil &&
				tx.Statement.Table == (models.Ticket{}).TableName() {
				ticketQueries.Add(1)
			}
		}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = db.Callback().Query().Remove(callbackName)
	})

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/business", func(c *gin.Context) {
		c.Set("user_id", uint(11))
		handler.GetBusinessStats(c)
	})
	response := httptest.NewRecorder()
	router.ServeHTTP(
		response,
		httptest.NewRequest(http.MethodGet, "/business", nil),
	)
	if response.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", response.Code, response.Body)
	}
	if ticketQueries.Load() != 0 {
		t.Fatalf(
			"membership-free analytics executed %d Ticket queries",
			ticketQueries.Load(),
		)
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
			is_active BOOLEAN NOT NULL
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
			name TEXT NOT NULL
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
