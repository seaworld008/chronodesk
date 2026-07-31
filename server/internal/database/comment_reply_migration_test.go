package database

import (
	"strings"
	"testing"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestMigrateNestedCommentRepliesFlattensLegacyChains(t *testing.T) {
	db, err := gorm.Open(
		sqlite.Open("file:nested-comment-replies?mode=memory&cache=shared"),
		&gorm.Config{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(
		&models.User{},
		&models.Ticket{},
		&models.TicketComment{},
	); err != nil {
		t.Fatal(err)
	}
	user := models.User{
		Username: "reply-migration", Email: "reply-migration@example.com",
		PasswordHash: "hash", PlatformRole: models.PlatformRoleMember,
		Status: models.UserStatusActive,
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	ticket := models.Ticket{
		OrganizationID: 1, ProjectID: 1,
		TicketNumber: "REPLY-MIGRATION", Title: "reply migration",
		Description: "reply migration", Type: models.TicketTypeRequest,
		Priority: models.TicketPriorityNormal, Status: models.TicketStatusOpen,
		Source: models.TicketSourceWeb, CreatedByID: &user.ID, Version: 1,
	}
	if err := db.Create(&ticket).Error; err != nil {
		t.Fatal(err)
	}
	root := models.TicketComment{
		OrganizationID: 1, ProjectID: 1, TicketID: ticket.ID,
		UserID: &user.ID, ActorType: models.ActorTypeHuman, ActorID: "1",
		Content: "root", ContentType: "text", Type: models.CommentTypePublic,
		ReplyCount: 99,
	}
	if err := db.Create(&root).Error; err != nil {
		t.Fatal(err)
	}
	reply := root
	reply.ID = 0
	reply.Content = "reply"
	reply.ParentID = &root.ID
	if err := db.Create(&reply).Error; err != nil {
		t.Fatal(err)
	}
	nested := root
	nested.ID = 0
	nested.Content = "nested"
	nested.ParentID = &reply.ID
	if err := db.Create(&nested).Error; err != nil {
		t.Fatal(err)
	}
	deep := root
	deep.ID = 0
	deep.Content = "deep"
	deep.ParentID = &nested.ID
	if err := db.Create(&deep).Error; err != nil {
		t.Fatal(err)
	}

	for run := 1; run <= 2; run++ {
		if err := MigrateNestedCommentReplies(db); err != nil {
			t.Fatalf("migration run %d: %v", run, err)
		}
	}
	var rows []models.TicketComment
	if err := db.Order("id ASC").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 4 || rows[0].ParentID != nil || rows[0].ReplyCount != 3 {
		t.Fatalf("root projection after migration: %+v", rows)
	}
	for index := 1; index < len(rows); index++ {
		if rows[index].ParentID == nil || *rows[index].ParentID != root.ID ||
			rows[index].ReplyCount != 0 {
			t.Fatalf("reply %d was not flattened: %+v", index, rows[index])
		}
	}
}

func TestMigrateNestedCommentRepliesRejectsCycles(t *testing.T) {
	db, err := gorm.Open(
		sqlite.Open("file:nested-comment-reply-cycle?mode=memory&cache=shared"),
		&gorm.Config{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.TicketComment{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`
		INSERT INTO ticket_comments
			(id, organization_id, project_id, ticket_id, actor_type, actor_id,
			 content, content_type, type, parent_id, is_deleted)
		VALUES
			(1, 1, 1, 1, 'human', '1', 'one', 'text', 'public', 2, FALSE),
			(2, 1, 1, 1, 'human', '1', 'two', 'text', 'public', 1, FALSE)
	`).Error; err != nil {
		t.Fatal(err)
	}
	err = MigrateNestedCommentReplies(db)
	if err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("cycle migration error=%v", err)
	}
}
