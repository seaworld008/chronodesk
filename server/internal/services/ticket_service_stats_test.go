package services

import (
	"testing"
	"time"

	"gorm.io/gorm"

	"gongdan-system/internal/models"
)

func setupStatsTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	db := openTestDB(t)

	if err := db.AutoMigrate(&models.User{}, &models.Ticket{}, &models.TicketComment{}); err != nil {
		t.Fatalf("failed to migrate: %v", err)
	}

	user := models.User{
		Username:     "admin",
		Email:        "admin@example.com",
		PasswordHash: "hashed",
		Role:         models.RoleAdmin,
		Status:       models.UserStatusActive,
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("failed to create user: %v", err)
	}

	now := time.Now()
	assigneeID := user.ID

	tickets := []models.Ticket{
		{
			TicketNumber: "S-001",
			Title:        "Open SLA",
			Description:  "desc",
			Status:       models.TicketStatusOpen,
			Priority:     models.TicketPriorityHigh,
			Type:         models.TicketTypeIncident,
			Source:       models.TicketSourceWeb,
			CreatedByID:  user.ID,
			AssignedToID: &assigneeID,
			SLABreached:  true,
			IsEscalated:  true,
		},
		{
			TicketNumber: "S-002",
			Title:        "In Progress",
			Description:  "desc",
			Status:       models.TicketStatusInProgress,
			Priority:     models.TicketPriorityNormal,
			Type:         models.TicketTypeIncident,
			Source:       models.TicketSourceWeb,
			CreatedByID:  user.ID,
			AssignedToID: &assigneeID,
		},
		{
			TicketNumber: "S-003",
			Title:        "Pending Unassigned",
			Description:  "desc",
			Status:       models.TicketStatusPending,
			Priority:     models.TicketPriorityLow,
			Type:         models.TicketTypeIncident,
			Source:       models.TicketSourceWeb,
			CreatedByID:  user.ID,
			AssignedToID: nil,
			DueDate:      &now,
		},
		{
			TicketNumber: "S-004",
			Title:        "Resolved",
			Description:  "desc",
			Status:       models.TicketStatusResolved,
			Priority:     models.TicketPriorityNormal,
			Type:         models.TicketTypeIncident,
			Source:       models.TicketSourceWeb,
			CreatedByID:  user.ID,
			AssignedToID: &assigneeID,
		},
	}

	if err := db.Create(&tickets).Error; err != nil {
		t.Fatalf("failed to create tickets: %v", err)
	}

	return db
}

func TestGetTicketStatistics_Aggregates(t *testing.T) {
	db := setupStatsTestDB(t)
	svc := NewTicketService(db)

	stats, err := svc.GetTicketStatistics(1, "admin")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if stats.Total != 4 {
		t.Fatalf("expected total 4, got %d", stats.Total)
	}
	if stats.Open != 1 {
		t.Fatalf("expected open 1, got %d", stats.Open)
	}
	if stats.InProgress != 1 {
		t.Fatalf("expected in_progress 1, got %d", stats.InProgress)
	}
	if stats.SLABreached != 1 {
		t.Fatalf("expected sla_breached 1, got %d", stats.SLABreached)
	}
	if stats.Escalated != 1 {
		t.Fatalf("expected escalated 1, got %d", stats.Escalated)
	}
	if stats.Unassigned != 1 {
		t.Fatalf("expected unassigned 1, got %d", stats.Unassigned)
	}
}
