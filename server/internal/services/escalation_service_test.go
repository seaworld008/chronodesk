package services

import (
	"context"
	"testing"

	"gongdan-system/internal/models"
)

func TestHasFirstResponseIgnoresSystemComments(t *testing.T) {
	db := openTestDB(t)

	if err := db.AutoMigrate(&models.User{}, &models.Ticket{}, &models.TicketComment{}); err != nil {
		t.Fatalf("failed to migrate schemas: %v", err)
	}

	user := models.User{
		Username:     "agent-response",
		Email:        "agent-response@example.com",
		PasswordHash: "hashed",
		Role:         models.RoleAgent,
		Status:       models.UserStatusActive,
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("failed to seed user: %v", err)
	}

	ticket := models.Ticket{
		TicketNumber: "T-RESP-001",
		Title:        "Response check",
		Description:  "response test",
		Priority:     models.TicketPriorityNormal,
		Status:       models.TicketStatusOpen,
		Type:         models.TicketTypeRequest,
		Source:       models.TicketSourceWeb,
		CreatedByID:  user.ID,
	}
	if err := db.Create(&ticket).Error; err != nil {
		t.Fatalf("failed to seed ticket: %v", err)
	}

	systemComment := models.TicketComment{
		TicketID:    ticket.ID,
		UserID:      user.ID,
		Content:     "system note",
		ContentType: "text",
		Type:        models.CommentTypeSystem,
	}
	if err := db.Create(&systemComment).Error; err != nil {
		t.Fatalf("failed to seed system comment: %v", err)
	}

	svc := NewEscalationService(db)
	ctx := context.Background()

	if svc.hasFirstResponse(ctx, ticket.ID) {
		t.Fatalf("expected no first response when only system comments exist")
	}

	publicComment := models.TicketComment{
		TicketID:    ticket.ID,
		UserID:      user.ID,
		Content:     "public response",
		ContentType: "text",
		Type:        models.CommentTypePublic,
	}
	if err := db.Create(&publicComment).Error; err != nil {
		t.Fatalf("failed to seed public comment: %v", err)
	}

	if !svc.hasFirstResponse(ctx, ticket.ID) {
		t.Fatalf("expected first response when public comment exists")
	}
}
