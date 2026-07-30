package services

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/gorm"
)

func TestCrossProjectWorkbenchRestrictsEveryViewToExplicitMemberships(
	t *testing.T,
) {
	db := crossProjectWorkbenchTestDB(t)
	userID := uint(7)
	seedCrossProjectWorkbench(t, db, userID)
	service, err := NewCrossProjectWorkbenchService(db)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name        string
		view        CrossProjectWorkbenchView
		wantIDs     []uint
		wantTotal   int64
		wantProject string
	}{
		{
			name:        "todo excludes completed and unauthorized projects",
			view:        CrossProjectWorkbenchTodo,
			wantIDs:     []uint{101},
			wantTotal:   1,
			wantProject: "Operations",
		},
		{
			name:        "assigned includes completed but not unauthorized projects",
			view:        CrossProjectWorkbenchAssigned,
			wantIDs:     []uint{102, 101},
			wantTotal:   2,
			wantProject: "Operations",
		},
		{
			name:        "created uses authoritative human actor",
			view:        CrossProjectWorkbenchCreated,
			wantIDs:     []uint{103},
			wantTotal:   1,
			wantProject: "Operations",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			page, queryErr := service.ListTickets(
				context.Background(),
				CrossProjectWorkbenchQuery{
					UserID:   userID,
					View:     test.view,
					Page:     1,
					PageSize: 20,
				},
			)
			if queryErr != nil {
				t.Fatal(queryErr)
			}
			if page.Total != test.wantTotal {
				t.Fatalf("total = %d, want %d", page.Total, test.wantTotal)
			}
			if len(page.Items) != len(test.wantIDs) {
				t.Fatalf("items = %+v, want ids %v", page.Items, test.wantIDs)
			}
			for index, item := range page.Items {
				if item.ID != test.wantIDs[index] {
					t.Fatalf(
						"items[%d].id = %d, want %d",
						index,
						item.ID,
						test.wantIDs[index],
					)
				}
				if item.ProjectID != 10 ||
					item.ProjectKey != "OPS" ||
					item.ProjectName != test.wantProject {
					t.Fatalf("missing explicit project source: %+v", item)
				}
			}
		})
	}
}

func TestCrossProjectWorkbenchPaginationIsBounded(t *testing.T) {
	db := crossProjectWorkbenchTestDB(t)
	userID := uint(7)
	seedCrossProjectWorkbench(t, db, userID)
	service, err := NewCrossProjectWorkbenchService(db)
	if err != nil {
		t.Fatal(err)
	}

	page, err := service.ListTickets(
		context.Background(),
		CrossProjectWorkbenchQuery{
			UserID:   userID,
			View:     CrossProjectWorkbenchAssigned,
			Page:     2,
			PageSize: 1,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 2 ||
		page.TotalPages != 2 ||
		page.Page != 2 ||
		len(page.Items) != 1 ||
		page.Items[0].ID != 101 {
		t.Fatalf("unexpected second page: %+v", page)
	}

	page, err = service.ListTickets(
		context.Background(),
		CrossProjectWorkbenchQuery{
			UserID:   userID,
			View:     CrossProjectWorkbenchAssigned,
			Page:     1,
			PageSize: maxCrossProjectWorkbenchPageSize,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if page.PageSize != maxCrossProjectWorkbenchPageSize ||
		page.Total != 2 ||
		len(page.Items) != 2 {
		t.Fatalf("maximum bounded page is invalid: %+v", page)
	}

	_, err = service.ListTickets(
		context.Background(),
		CrossProjectWorkbenchQuery{
			UserID:   userID,
			View:     CrossProjectWorkbenchTodo,
			Page:     1,
			PageSize: maxCrossProjectWorkbenchPageSize + 1,
		},
	)
	if !errors.Is(err, ErrCrossProjectWorkbenchQuery) {
		t.Fatalf("oversized page error = %v", err)
	}
}

func TestNormalizeCrossProjectWorkbenchQueryRejectsIntegerExtremes(
	t *testing.T,
) {
	maxInt := int(^uint(0) >> 1)
	minInt := -maxInt - 1
	tests := []struct {
		name     string
		page     int
		pageSize int
	}{
		{
			name:     "maximum page size",
			page:     1,
			pageSize: maxInt,
		},
		{
			name:     "minimum page size",
			page:     1,
			pageSize: minInt,
		},
		{
			name:     "maximum page",
			page:     maxInt,
			pageSize: defaultCrossProjectWorkbenchPageSize,
		},
		{
			name:     "minimum page",
			page:     minInt,
			pageSize: defaultCrossProjectWorkbenchPageSize,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := normalizeCrossProjectWorkbenchQuery(
				CrossProjectWorkbenchQuery{
					UserID:   7,
					View:     CrossProjectWorkbenchTodo,
					Page:     test.page,
					PageSize: test.pageSize,
				},
			)
			if !errors.Is(err, ErrCrossProjectWorkbenchQuery) {
				t.Fatalf(
					"normalize page=%d pageSize=%d error=%v",
					test.page,
					test.pageSize,
					err,
				)
			}
		})
	}
}

func TestCrossProjectWorkbenchWithoutMembershipDoesNotFallBackToGlobalAccess(
	t *testing.T,
) {
	db := crossProjectWorkbenchTestDB(t)
	seedCrossProjectWorkbench(t, db, 7)
	service, err := NewCrossProjectWorkbenchService(db)
	if err != nil {
		t.Fatal(err)
	}

	// User 99 can represent a platform administrator at the transport layer.
	// The service has no role bypass and therefore returns an empty workbench.
	page, err := service.ListTickets(
		context.Background(),
		CrossProjectWorkbenchQuery{
			UserID: 99,
			View:   CrossProjectWorkbenchAssigned,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 0 || len(page.Items) != 0 {
		t.Fatalf("ungranted user received project data: %+v", page)
	}
}

func TestCrossProjectWorkbenchRejectsMissingIdentityAndUnknownView(
	t *testing.T,
) {
	db := crossProjectWorkbenchTestDB(t)
	service, err := NewCrossProjectWorkbenchService(db)
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.ListTickets(
		context.Background(),
		CrossProjectWorkbenchQuery{View: CrossProjectWorkbenchTodo},
	)
	if !errors.Is(err, ErrCrossProjectWorkbenchAccessDenied) {
		t.Fatalf("missing identity error = %v", err)
	}
	_, err = service.ListTickets(
		context.Background(),
		CrossProjectWorkbenchQuery{
			UserID: 7,
			View:   "everything",
		},
	)
	if !errors.Is(err, ErrCrossProjectWorkbenchQuery) {
		t.Fatalf("unknown view error = %v", err)
	}
}

func crossProjectWorkbenchTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db := openTestDB(t)
	statements := []string{
		`CREATE TABLE projects (
			id INTEGER PRIMARY KEY,
			organization_id INTEGER NOT NULL,
			key TEXT NOT NULL,
			name TEXT NOT NULL,
			status TEXT NOT NULL
		)`,
		`CREATE TABLE project_memberships (
			id INTEGER PRIMARY KEY,
			project_id INTEGER NOT NULL,
			user_id INTEGER NOT NULL,
			role TEXT NOT NULL,
			is_active BOOLEAN NOT NULL
		)`,
		`CREATE TABLE users (
			id INTEGER PRIMARY KEY,
			username TEXT NOT NULL,
			display_name TEXT
		)`,
		`CREATE TABLE tickets (
			id INTEGER PRIMARY KEY,
			public_id TEXT NOT NULL,
			organization_id INTEGER NOT NULL,
			project_id INTEGER NOT NULL,
			ticket_number TEXT NOT NULL,
			title TEXT NOT NULL,
			type TEXT NOT NULL,
			priority TEXT NOT NULL,
			status TEXT NOT NULL,
			created_by_id INTEGER,
			assigned_to_id INTEGER,
			created_by_actor_type TEXT NOT NULL,
			created_by_actor_id TEXT NOT NULL,
			assigned_to_actor_type TEXT,
			assigned_to_actor_id TEXT,
			due_date DATETIME,
			sla_due_date DATETIME,
			sla_breached BOOLEAN NOT NULL DEFAULT FALSE,
			version INTEGER NOT NULL,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			deleted_at DATETIME
		)`,
	}
	for _, statement := range statements {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatalf("create workbench test schema: %v", err)
		}
	}
	return db
}

func seedCrossProjectWorkbench(t *testing.T, db *gorm.DB, userID uint) {
	t.Helper()
	for _, statement := range []string{
		`INSERT INTO projects (id, organization_id, key, name, status)
		 VALUES
		 (10, 1, 'OPS', 'Operations', 'active'),
		 (20, 1, 'FIN', 'Finance', 'active'),
		 (30, 1, 'OLD', 'Archived membership', 'active')`,
		fmt.Sprintf(
			`INSERT INTO project_memberships
			 (id, project_id, user_id, role, is_active)
			 VALUES
			 (1, 10, %d, 'project_admin', TRUE),
			 (2, 30, %d, 'agent', FALSE)`,
			userID,
			userID,
		),
		`INSERT INTO users (id, username, display_name)
		 VALUES (7, 'owner', 'Owner'), (8, 'other', 'Other')`,
	} {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatalf("seed workbench scope: %v", err)
		}
	}

	base := time.Date(2026, 7, 30, 8, 0, 0, 0, time.UTC)
	tickets := []struct {
		id            uint
		projectID     uint
		status        models.TicketStatus
		creatorID     uint
		assigneeID    uint
		creatorType   models.ActorType
		creatorActor  string
		assigneeType  models.ActorType
		assigneeActor string
		updatedAt     time.Time
	}{
		{
			101, 10, models.TicketStatusOpen, 8, 7,
			models.ActorTypeHuman, "8", models.ActorTypeHuman, "7",
			base.Add(time.Minute),
		},
		{
			102, 10, models.TicketStatusResolved, 8, 7,
			models.ActorTypeHuman, "8", models.ActorTypeHuman, "7",
			base.Add(2 * time.Minute),
		},
		{
			103, 10, models.TicketStatusOpen, 7, 8,
			models.ActorTypeHuman, "7", models.ActorTypeHuman, "8",
			base.Add(3 * time.Minute),
		},
		// Human projection IDs are not authorization facts. This row must be
		// excluded because its authoritative actors are service principals.
		{
			104, 10, models.TicketStatusOpen, 7, 7,
			models.ActorTypeServicePrincipal, "creator-sp",
			models.ActorTypeServicePrincipal, "assignee-sp",
			base.Add(6 * time.Minute),
		},
		// Both unauthorized rows would match the human views if the explicit
		// project ID predicate were ever removed.
		{
			201, 20, models.TicketStatusOpen, 7, 7,
			models.ActorTypeHuman, "7", models.ActorTypeHuman, "7",
			base.Add(4 * time.Minute),
		},
		{
			301, 30, models.TicketStatusOpen, 7, 7,
			models.ActorTypeHuman, "7", models.ActorTypeHuman, "7",
			base.Add(5 * time.Minute),
		},
	}
	for _, ticket := range tickets {
		if err := db.Exec(
			`INSERT INTO tickets (
				id, public_id, organization_id, project_id, ticket_number, title, type,
				priority, status, created_by_id, assigned_to_id,
				created_by_actor_type, created_by_actor_id,
				assigned_to_actor_type, assigned_to_actor_id,
				sla_breached, version, created_at, updated_at
			) VALUES (?, ?, 1, ?, ?, ?, 'request', 'normal', ?, ?, ?,
				?, ?, ?, ?, FALSE, 1, ?, ?)`,
			ticket.id,
			fmt.Sprintf("00000000-0000-7000-8000-%012d", ticket.id),
			ticket.projectID,
			fmt.Sprintf("T-%d", ticket.id),
			fmt.Sprintf("Ticket %d", ticket.id),
			ticket.status,
			ticket.creatorID,
			ticket.assigneeID,
			ticket.creatorType,
			ticket.creatorActor,
			ticket.assigneeType,
			ticket.assigneeActor,
			base,
			ticket.updatedAt,
		).Error; err != nil {
			t.Fatalf("seed ticket %d: %v", ticket.id, err)
		}
	}
}
