package services

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/gorm"
)

// helper creates an in-memory sqlite DB with required tables for ticket tests.
func setupTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	db := openTestDB(t)

	if err := db.AutoMigrate(&models.User{}, &models.Ticket{}, &models.TicketComment{}, &models.TicketHistory{}); err != nil {
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

func TestEscalateTicketMarksTicketAsEscalated(t *testing.T) {
	db := setupTestDB(t)
	var ticket models.Ticket
	if err := db.Where("ticket_number = ?", "T-001").First(&ticket).Error; err != nil {
		t.Fatalf("failed to load ticket: %v", err)
	}

	manager := models.User{
		Username:     "manager",
		Email:        "manager@example.com",
		PasswordHash: "hashed",
		Role:         models.RoleAdmin,
		Status:       models.UserStatusActive,
	}
	if err := db.Create(&manager).Error; err != nil {
		t.Fatalf("failed to create manager: %v", err)
	}

	svc := NewTicketService(db)
	updated, err := svc.EscalateTicket(ticket.ID, manager.ID, ticket.CreatedByID, "SLA breach", "please review")
	if err != nil {
		t.Fatalf("EscalateTicket returned error: %v", err)
	}
	if !updated.IsEscalated {
		t.Fatal("expected escalated ticket to set is_escalated")
	}
}

func TestReopenTicketClearsCompletionTimestamps(t *testing.T) {
	db := setupTestDB(t)
	var ticket models.Ticket
	if err := db.Where("ticket_number = ?", "T-004").First(&ticket).Error; err != nil {
		t.Fatalf("failed to load resolved ticket: %v", err)
	}

	completedAt := time.Now().Add(-time.Hour)
	if err := db.Model(&ticket).Updates(map[string]interface{}{
		"resolved_at": completedAt,
		"closed_at":   completedAt,
	}).Error; err != nil {
		t.Fatalf("failed to seed completion timestamps: %v", err)
	}

	svc := NewTicketService(db)
	reopened, err := svc.UpdateTicketStatus(ticket.ID, string(models.TicketStatusOpen), ticket.CreatedByID, "reopen", "")
	if err != nil {
		t.Fatalf("UpdateTicketStatus returned error: %v", err)
	}
	if reopened.ResolvedAt != nil || reopened.ClosedAt != nil {
		t.Fatalf("reopened ticket must clear completion timestamps, got resolved_at=%v closed_at=%v", reopened.ResolvedAt, reopened.ClosedAt)
	}
}

func TestCreateTicketDerivesSLADeadlineFromCategory(t *testing.T) {
	db := setupFilterTestDB(t)
	creator := seedUser(t, db, "sla-create-user")
	slaHours := 6
	category := models.Category{
		Name:      "Create SLA category",
		Slug:      "create-sla-category",
		Type:      models.CategoryTypeSupport,
		Status:    models.CategoryStatusActive,
		SLAHours:  &slaHours,
		CreatedBy: creator.ID,
	}
	if err := db.Create(&category).Error; err != nil {
		t.Fatalf("failed to create category: %v", err)
	}

	startedAt := time.Now()
	svc := NewTicketService(db)
	ticket, err := svc.CreateTicket(context.Background(), &models.TicketCreateRequest{
		Title:       "SLA-backed ticket",
		Description: "deadline should be derived",
		Type:        models.TicketTypeRequest,
		Priority:    models.TicketPriorityNormal,
		Source:      models.TicketSourceWeb,
		CategoryID:  &category.ID,
	}, creator.ID)
	if err != nil {
		t.Fatalf("CreateTicket returned error: %v", err)
	}
	if ticket.SLADueDate == nil {
		t.Fatal("expected SLA deadline from category")
	}

	earliest := startedAt.Add(time.Duration(slaHours) * time.Hour)
	latest := time.Now().Add(time.Duration(slaHours) * time.Hour)
	if ticket.SLADueDate.Before(earliest) || ticket.SLADueDate.After(latest) {
		t.Fatalf("unexpected SLA deadline %v, expected between %v and %v", ticket.SLADueDate, earliest, latest)
	}
}
