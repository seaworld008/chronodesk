package services

import (
	"context"
	"testing"

	"gongdan-system/internal/models"
)

func TestDeleteTicketCleansRelatedData(t *testing.T) {
	db := openTestDB(t)

	if err := db.AutoMigrate(
		&models.User{},
		&models.Ticket{},
		&models.Notification{},
		&models.TicketHistory{},
		&models.TicketComment{},
		&models.TicketAttachment{},
	); err != nil {
		t.Fatalf("failed to migrate schemas: %v", err)
	}

	user := models.User{
		Username:     "agent-delete",
		Email:        "agent-delete@example.com",
		PasswordHash: "hashed",
		Role:         models.RoleAdmin,
		Status:       models.UserStatusActive,
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("failed to seed user: %v", err)
	}

	ticket := models.Ticket{
		TicketNumber: "T-DELETE-001",
		Title:        "Delete cleanup ticket",
		Description:  "cleanup test",
		Priority:     models.TicketPriorityNormal,
		Status:       models.TicketStatusOpen,
		Type:         models.TicketTypeRequest,
		Source:       models.TicketSourceWeb,
		CreatedByID:  user.ID,
	}
	if err := db.Create(&ticket).Error; err != nil {
		t.Fatalf("failed to seed ticket: %v", err)
	}

	notification := models.Notification{
		Type:            models.NotificationTypeTicketCreated,
		Title:           "Ticket created",
		Content:         "Created for delete cleanup test",
		Priority:        models.NotificationPriorityNormal,
		Channel:         models.NotificationChannelInApp,
		RecipientID:     user.ID,
		RelatedTicketID: &ticket.ID,
	}
	if err := db.Create(&notification).Error; err != nil {
		t.Fatalf("failed to seed notification: %v", err)
	}

	history := models.TicketHistory{
		TicketID:    ticket.ID,
		Action:      models.HistoryActionCreate,
		Description: "created",
	}
	if err := db.Create(&history).Error; err != nil {
		t.Fatalf("failed to seed ticket history: %v", err)
	}

	comment := models.TicketComment{
		TicketID:    ticket.ID,
		UserID:      user.ID,
		Content:     "first response",
		ContentType: "text",
		Type:        models.CommentTypePublic,
	}
	if err := db.Create(&comment).Error; err != nil {
		t.Fatalf("failed to seed ticket comment: %v", err)
	}

	attachment := models.TicketAttachment{
		TicketID:     ticket.ID,
		UploadedBy:   user.ID,
		FileName:     "test.txt",
		OriginalName: "test.txt",
		FileSize:     12,
		StoragePath:  "/tmp/test.txt",
	}
	if err := db.Create(&attachment).Error; err != nil {
		t.Fatalf("failed to seed ticket attachment: %v", err)
	}

	svc := &TicketService{db: db}
	if err := svc.DeleteTicket(context.Background(), ticket.ID, user.ID, "admin"); err != nil {
		t.Fatalf("DeleteTicket returned error: %v", err)
	}

	var notificationCount int64
	if err := db.Model(&models.Notification{}).Where("related_ticket_id = ?", ticket.ID).Count(&notificationCount).Error; err != nil {
		t.Fatalf("failed to count notifications: %v", err)
	}
	if notificationCount != 0 {
		t.Fatalf("expected notifications to be deleted, got %d", notificationCount)
	}

	var historyCount int64
	if err := db.Model(&models.TicketHistory{}).Where("ticket_id = ?", ticket.ID).Count(&historyCount).Error; err != nil {
		t.Fatalf("failed to count ticket histories: %v", err)
	}
	if historyCount != 0 {
		t.Fatalf("expected histories to be deleted, got %d", historyCount)
	}

	var commentCount int64
	if err := db.Model(&models.TicketComment{}).Where("ticket_id = ?", ticket.ID).Count(&commentCount).Error; err != nil {
		t.Fatalf("failed to count ticket comments: %v", err)
	}
	if commentCount != 0 {
		t.Fatalf("expected comments to be deleted, got %d", commentCount)
	}

	var attachmentCount int64
	if err := db.Model(&models.TicketAttachment{}).Where("ticket_id = ?", ticket.ID).Count(&attachmentCount).Error; err != nil {
		t.Fatalf("failed to count ticket attachments: %v", err)
	}
	if attachmentCount != 0 {
		t.Fatalf("expected attachments to be deleted, got %d", attachmentCount)
	}

	var ticketCount int64
	if err := db.Model(&models.Ticket{}).Where("id = ?", ticket.ID).Count(&ticketCount).Error; err != nil {
		t.Fatalf("failed to count tickets: %v", err)
	}
	if ticketCount != 0 {
		t.Fatalf("expected ticket to be deleted, got %d", ticketCount)
	}
}
