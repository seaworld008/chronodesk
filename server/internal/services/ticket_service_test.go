package services

import (
	"context"
	"fmt"
	"testing"

	"gongdan-system/internal/models"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// helper creates an in-memory sqlite DB with required tables for ticket tests.
func setupTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open sqlite memory db: %v", err)
	}

	if err := db.AutoMigrate(&models.User{}, &models.Ticket{}, &models.TicketComment{}); err != nil {
		t.Fatalf("failed to migrate schemas: %v", err)
	}

	// seed a basic user for foreign key references
	user := models.User{
		Username:     "agent1",
		Email:        "agent1@example.com",
		PasswordHash: "hashed",
		Role:         models.RoleAgent,
		Status:       models.UserStatusActive,
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("failed to seed user: %v", err)
	}

	// create a few tickets with different priorities and statuses
	ticketFixtures := []models.Ticket{
		{
			TicketNumber: "T-001",
			Title:        "Urgent open ticket",
			Description:  "needs attention",
			Priority:     models.TicketPriorityUrgent,
			Status:       models.TicketStatusOpen,
			Type:         models.TicketTypeIncident,
			Source:       models.TicketSourceWeb,
			CreatedByID:  user.ID,
		},
		{
			TicketNumber: "T-002",
			Title:        "Critical in progress",
			Description:  "mid action",
			Priority:     models.TicketPriorityCritical,
			Status:       models.TicketStatusInProgress,
			Type:         models.TicketTypeIncident,
			Source:       models.TicketSourceWeb,
			CreatedByID:  user.ID,
		},
		{
			TicketNumber: "T-003",
			Title:        "High priority",
			Description:  "should not match",
			Priority:     models.TicketPriorityHigh,
			Status:       models.TicketStatusOpen,
			Type:         models.TicketTypeIncident,
			Source:       models.TicketSourceWeb,
			CreatedByID:  user.ID,
		},
		{
			TicketNumber: "T-004",
			Title:        "Urgent resolved",
			Description:  "status filtered",
			Priority:     models.TicketPriorityUrgent,
			Status:       models.TicketStatusResolved,
			Type:         models.TicketTypeIncident,
			Source:       models.TicketSourceWeb,
			CreatedByID:  user.ID,
		},
	}

	if err := db.Create(&ticketFixtures).Error; err != nil {
		t.Fatalf("failed to seed tickets: %v", err)
	}

	return db
}

func TestGetTicketsSupportsMultiValueFilters(t *testing.T) {
	db := setupTestDB(t)
	svc := &TicketService{db: db}

	filters := TicketFilters{
		Status:    "open,in_progress",
		Priority:  "urgent,critical",
		Page:      1,
		Limit:     10,
		SortBy:    "created_at",
		SortOrder: "desc",
	}

	tickets, total, err := svc.GetTickets(context.Background(), filters)
	if err != nil {
		t.Fatalf("GetTickets returned error: %v", err)
	}

	if total != 2 {
		t.Fatalf("expected total 2, got %d", total)
	}
	if len(tickets) != 2 {
		t.Fatalf("expected 2 tickets, got %d", len(tickets))
	}

	for _, ticket := range tickets {
		if ticket.Priority != models.TicketPriorityUrgent && ticket.Priority != models.TicketPriorityCritical {
			t.Fatalf("ticket %s has unexpected priority %s", ticket.TicketNumber, ticket.Priority)
		}
		if ticket.Status != models.TicketStatusOpen && ticket.Status != models.TicketStatusInProgress {
			t.Fatalf("ticket %s has unexpected status %s", ticket.TicketNumber, ticket.Status)
		}
	}

	// control: single value still works
	filters.Priority = string(models.TicketPriorityUrgent)

	singleTickets, singleTotal, err := svc.GetTickets(context.Background(), filters)
	if err != nil {
		t.Fatalf("GetTickets single priority returned error: %v", err)
	}
	if singleTotal != 1 || len(singleTickets) != 1 {
		t.Fatalf("expected 1 urgent ticket, got total=%d len=%d", singleTotal, len(singleTickets))
	}
	if singleTickets[0].Priority != models.TicketPriorityUrgent {
		t.Fatalf("expected urgent ticket, got %s", singleTickets[0].Priority)
	}
}

func TestSplitCommaSeparated(t *testing.T) {
	cases := []struct {
		input    string
		expected []string
	}{
		{"", nil},
		{"single", []string{"single"}},
		{"a,b , c", []string{"a", "b", "c"}},
		{",a,,b,", []string{"a", "b"}},
	}

	for _, tc := range cases {
		got := splitCommaSeparated(tc.input)
		if fmt.Sprint(got) != fmt.Sprint(tc.expected) {
			t.Fatalf("splitCommaSeparated(%q) = %v, expected %v", tc.input, got, tc.expected)
		}
	}
}
