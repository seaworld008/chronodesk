package services

import (
	"context"
	"testing"
	"time"

	"gorm.io/gorm"

	"gongdan-system/internal/models"
)

func setupFilterTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	db := openTestDB(t)

	if err := db.AutoMigrate(&models.User{}, &models.Ticket{}, &models.TicketComment{}); err != nil {
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
		Role:         models.RoleAdmin,
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
		CreatedByID:  creator.ID,
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
		CreatedByID:  creator.ID,
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
		CreatedByID:  creator.ID,
		AssignedToID: nil,
		DueDate:      ptrTime(now.Add(24 * time.Hour)),
	})

	svc := NewTicketService(db)

	tickets, total, err := svc.GetTickets(context.Background(), TicketFilters{
		SLABreached: boolPtr(true),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if total != 1 || len(tickets) != 1 {
		t.Fatalf("expected 1 sla_breached ticket, got total=%d len=%d", total, len(tickets))
	}

	tickets, total, err = svc.GetTickets(context.Background(), TicketFilters{
		IsOverdue: boolPtr(true),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if total != 1 || len(tickets) != 1 {
		t.Fatalf("expected 1 overdue ticket, got total=%d len=%d", total, len(tickets))
	}

	tickets, total, err = svc.GetTickets(context.Background(), TicketFilters{
		Unassigned: boolPtr(true),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if total != 1 || len(tickets) != 1 {
		t.Fatalf("expected 1 unassigned ticket, got total=%d len=%d", total, len(tickets))
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
		CreatedByID:  creator.ID,
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
		CreatedByID:  creator.ID,
	})

	svc := NewTicketService(db)

	tickets, _, err := svc.GetTickets(context.Background(), TicketFilters{
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

func ptrTime(t time.Time) *time.Time {
	return &t
}
