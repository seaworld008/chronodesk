package services

import (
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/seaworld008/chronodesk/server/internal/models"
)

func setupFilterTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	db := openTestDB(t)

	if err := db.AutoMigrate(
		&models.User{},
		&models.Category{},
		&models.Ticket{},
		&models.TicketComment{},
		&models.SLAConfig{},
		&models.DomainEvent{},
		&models.OutboxDelivery{},
		&models.TicketHistory{},
	); err != nil {
		t.Fatalf("failed to migrate: %v", err)
	}

	return db
}

func seedUser(t *testing.T, db *gorm.DB, username string) models.User {
	t.Helper()

	user := models.User{
		Username:     username,
		Email:        username + "@example.com",
		PasswordHash: "hashed",
		PlatformRole: models.PlatformRolePlatformAdmin,
		Status:       models.UserStatusActive,
	}

	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("failed to create user: %v", err)
	}

	return user
}

func seedTicket(t *testing.T, db *gorm.DB, ticket models.Ticket) models.Ticket {
	t.Helper()

	if err := db.Create(&ticket).Error; err != nil {
		t.Fatalf("failed to create ticket: %v", err)
	}

	return ticket
}

func boolPtr(value bool) *bool {
	return &value
}

func TestGetTicketsFilters_SLAOverdueUnassigned(t *testing.T) {
	db := setupFilterTestDB(t)
	creator := seedUser(t, db, "creator")
	assignee := seedUser(t, db, "assignee")

	now := time.Now()

	seedTicket(t, db, models.Ticket{
		TicketNumber: "T-001",
		Title:        "SLA breached",
		Description:  "desc",
		Status:       models.TicketStatusOpen,
		Priority:     models.TicketPriorityHigh,
		Type:         models.TicketTypeRequest,
		Source:       models.TicketSourceWeb,
		CreatedByID:  &creator.ID,
		AssignedToID: &assignee.ID,
		SLABreached:  true,
		DueDate:      ptrTime(now.Add(48 * time.Hour)),
	})

	seedTicket(t, db, models.Ticket{
		TicketNumber: "T-002",
		Title:        "Overdue",
		Description:  "desc",
		Status:       models.TicketStatusOpen,
		Priority:     models.TicketPriorityNormal,
		Type:         models.TicketTypeRequest,
		Source:       models.TicketSourceWeb,
		CreatedByID:  &creator.ID,
		AssignedToID: &assignee.ID,
		DueDate:      ptrTime(now.Add(-24 * time.Hour)),
	})

	seedTicket(t, db, models.Ticket{
		TicketNumber: "T-003",
		Title:        "Unassigned",
		Description:  "desc",
		Status:       models.TicketStatusOpen,
		Priority:     models.TicketPriorityLow,
		Type:         models.TicketTypeRequest,
		Source:       models.TicketSourceWeb,
		CreatedByID:  &creator.ID,
		AssignedToID: nil,
		DueDate:      ptrTime(now.Add(24 * time.Hour)),
	})
	seedTicket(t, db, models.Ticket{
		TicketNumber: "T-004",
		Title:        "Resolved legacy SLA projection",
		Description:  "desc",
		Status:       models.TicketStatusResolved,
		Priority:     models.TicketPriorityHigh,
		Type:         models.TicketTypeRequest,
		Source:       models.TicketSourceWeb,
		CreatedByID:  &creator.ID,
		AssignedToID: &assignee.ID,
		SLABreached:  true,
		DueDate:      ptrTime(now.Add(48 * time.Hour)),
	})

	ctx := testProjectOperationContext(t, db, models.HumanActor(creator.ID))
	svc := newTicketServiceForTest(t, db)

	tickets, total, err := svc.GetTickets(ctx, TicketFilters{
		SLABreached: boolPtr(true),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if total != 1 || len(tickets) != 1 {
		t.Fatalf("expected 1 sla_breached ticket, got total=%d len=%d", total, len(tickets))
	}

	tickets, total, err = svc.GetTickets(ctx, TicketFilters{
		IsOverdue: boolPtr(true),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if total != 1 || len(tickets) != 1 {
		t.Fatalf("expected 1 overdue ticket, got total=%d len=%d", total, len(tickets))
	}

	tickets, total, err = svc.GetTickets(ctx, TicketFilters{
		Unassigned: boolPtr(true),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if total != 1 || len(tickets) != 1 {
		t.Fatalf("expected 1 unassigned ticket, got total=%d len=%d", total, len(tickets))
	}
}

func TestGetTicketsFilters_Source(t *testing.T) {
	db := setupFilterTestDB(t)
	creator := seedUser(t, db, "source-creator")

	webTicket := seedTicket(t, db, models.Ticket{
		TicketNumber: "SOURCE-WEB",
		Title:        "Web ticket",
		Description:  "desc",
		Status:       models.TicketStatusOpen,
		Priority:     models.TicketPriorityNormal,
		Type:         models.TicketTypeRequest,
		Source:       models.TicketSourceWeb,
		CreatedByID:  &creator.ID,
	})
	seedTicket(t, db, models.Ticket{
		TicketNumber: "SOURCE-API",
		Title:        "API ticket",
		Description:  "desc",
		Status:       models.TicketStatusOpen,
		Priority:     models.TicketPriorityNormal,
		Type:         models.TicketTypeRequest,
		Source:       models.TicketSourceAPI,
		CreatedByID:  &creator.ID,
	})

	svc := newTicketServiceForTest(t, db)
	ctx := testProjectOperationContext(t, db, models.HumanActor(creator.ID))
	tickets, total, err := svc.GetTickets(ctx, TicketFilters{
		Source: string(models.TicketSourceWeb),
	})
	if err != nil {
		t.Fatalf("GetTickets() source filter error = %v", err)
	}
	if total != 1 || len(tickets) != 1 {
		t.Fatalf("GetTickets() source filter total=%d len=%d, want 1", total, len(tickets))
	}
	if tickets[0].ID != webTicket.ID {
		t.Fatalf("GetTickets() source filter returned id=%d, want %d", tickets[0].ID, webTicket.ID)
	}
}

func TestGetTickets_SortFieldInjectionFallsBackToCreatedAt(t *testing.T) {
	db := setupFilterTestDB(t)
	creator := seedUser(t, db, "sort-creator")

	older := seedTicket(t, db, models.Ticket{
		TicketNumber: "S-001",
		Title:        "Older ticket",
		Description:  "desc",
		Status:       models.TicketStatusOpen,
		Priority:     models.TicketPriorityNormal,
		Type:         models.TicketTypeRequest,
		Source:       models.TicketSourceWeb,
		CreatedByID:  &creator.ID,
	})
	time.Sleep(10 * time.Millisecond)
	newer := seedTicket(t, db, models.Ticket{
		TicketNumber: "S-002",
		Title:        "Newer ticket",
		Description:  "desc",
		Status:       models.TicketStatusOpen,
		Priority:     models.TicketPriorityNormal,
		Type:         models.TicketTypeRequest,
		Source:       models.TicketSourceWeb,
		CreatedByID:  &creator.ID,
	})

	svc := newTicketServiceForTest(t, db)
	ctx := testProjectOperationContext(t, db, models.HumanActor(creator.ID))

	tickets, _, err := svc.GetTickets(ctx, TicketFilters{
		SortBy:    "bad_column",
		SortOrder: "DESC",
	})
	if err != nil {
		t.Fatalf("expected injected sort field to be sanitized, got error: %v", err)
	}
	if len(tickets) < 2 {
		t.Fatalf("expected at least 2 tickets, got %d", len(tickets))
	}

	// Invalid sort field should fall back to created_at DESC.
	if tickets[0].ID != newer.ID {
		t.Fatalf("expected newest ticket first after fallback sort, got first id=%d want=%d (older id=%d)", tickets[0].ID, newer.ID, older.ID)
	}
}

func TestGetSLABreachedTicketsUsesAuthoritativeProjection(t *testing.T) {
	db := setupFilterTestDB(t)
	creator := seedUser(t, db, "sla-creator")
	assignee := seedUser(t, db, "sla-assignee")
	slaHours := 4
	category := models.Category{
		Name:      "SLA category",
		Slug:      "sla-category",
		Type:      models.CategoryTypeSupport,
		Status:    models.CategoryStatusActive,
		SLAHours:  &slaHours,
		CreatedBy: creator.ID,
	}
	if err := db.Create(&category).Error; err != nil {
		t.Fatalf("failed to create SLA category: %v", err)
	}

	past := time.Now().Add(-time.Hour)
	future := time.Now().Add(time.Hour)
	seedTicket(t, db, models.Ticket{
		TicketNumber: "SLA-001",
		Title:        "Expired SLA deadline",
		Description:  "desc",
		Status:       models.TicketStatusOpen,
		Priority:     models.TicketPriorityHigh,
		Type:         models.TicketTypeRequest,
		Source:       models.TicketSourceWeb,
		CreatedByID:  &creator.ID,
		AssignedToID: &assignee.ID,
		CategoryID:   &category.ID,
		SLADueDate:   &past,
	})
	seedTicket(t, db, models.Ticket{
		TicketNumber: "SLA-002",
		Title:        "Explicit SLA breach",
		Description:  "desc",
		Status:       models.TicketStatusInProgress,
		Priority:     models.TicketPriorityUrgent,
		Type:         models.TicketTypeIncident,
		Source:       models.TicketSourceWeb,
		CreatedByID:  &creator.ID,
		AssignedToID: &assignee.ID,
		CategoryID:   &category.ID,
		SLABreached:  true,
		SLADueDate:   &future,
	})
	seedTicket(t, db, models.Ticket{
		TicketNumber: "SLA-003",
		Title:        "Resolved breach",
		Description:  "desc",
		Status:       models.TicketStatusResolved,
		Priority:     models.TicketPriorityNormal,
		Type:         models.TicketTypeRequest,
		Source:       models.TicketSourceWeb,
		CreatedByID:  &creator.ID,
		AssignedToID: &assignee.ID,
		CategoryID:   &category.ID,
		SLABreached:  true,
	})

	svc := newTicketServiceForTest(t, db)
	ctx := testProjectOperationContext(t, db, models.HumanActor(assignee.ID))
	tickets, total, err := svc.GetSLABreachedTickets(
		ctx,
		assignee.ID,
		"agent",
	)
	if err != nil {
		t.Fatalf("GetSLABreachedTickets returned error: %v", err)
	}
	if total != 1 || len(tickets) != 1 {
		t.Fatalf("expected 1 projected SLA breach, got total=%d len=%d", total, len(tickets))
	}
	if tickets[0].TicketNumber != "SLA-002" {
		t.Fatalf(
			"expired legacy sla_due_date must not bypass the projection, got %s",
			tickets[0].TicketNumber,
		)
	}
}

func ptrTime(t time.Time) *time.Time {
	return &t
}
