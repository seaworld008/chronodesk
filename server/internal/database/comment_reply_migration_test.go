package database

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

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
		&models.SchemaMigrationCheckpoint{},
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

	if err := MigrateNestedCommentReplies(db); err != nil {
		t.Fatalf("first migration: %v", err)
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
	firstRunRows := append([]models.TicketComment(nil), rows...)

	if err := db.Exec(`
		CREATE TABLE ticket_comment_update_audit (
			event TEXT NOT NULL
		)
	`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`
		CREATE TRIGGER audit_ticket_comment_update
		AFTER UPDATE ON ticket_comments
		BEGIN
			INSERT INTO ticket_comment_update_audit (event) VALUES ('updated');
		END
	`).Error; err != nil {
		t.Fatal(err)
	}
	if err := MigrateNestedCommentReplies(db); err != nil {
		t.Fatalf("second migration: %v", err)
	}
	var updateCount int64
	if err := db.Table("ticket_comment_update_audit").Count(&updateCount).Error; err != nil {
		t.Fatal(err)
	}
	if updateCount != 0 {
		t.Fatalf("checkpointed second migration issued %d ticket comment UPDATE(s)", updateCount)
	}
	rows = nil
	if err := db.Order("id ASC").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(rows, firstRunRows) {
		t.Fatalf("checkpointed migration changed comment rows:\nfirst=%+v\nsecond=%+v", firstRunRows, rows)
	}
	var checkpoints int64
	if err := db.Model(&models.SchemaMigrationCheckpoint{}).
		Where("key = ?", nestedCommentReplyCheckpointKey).
		Count(&checkpoints).Error; err != nil {
		t.Fatal(err)
	}
	if checkpoints != 1 {
		t.Fatalf("comment reply checkpoint count=%d, want 1", checkpoints)
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
	if err := db.AutoMigrate(
		&models.TicketComment{},
		&models.SchemaMigrationCheckpoint{},
	); err != nil {
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

func TestMigrateNestedCommentRepliesRejectsMismatchedCheckpoint(t *testing.T) {
	db, err := gorm.Open(
		sqlite.Open("file:nested-comment-reply-checkpoint-mismatch?mode=memory&cache=shared"),
		&gorm.Config{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(
		&models.TicketComment{},
		&models.SchemaMigrationCheckpoint{},
	); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.SchemaMigrationCheckpoint{
		Key:         nestedCommentReplyCheckpointKey,
		Version:     nestedCommentReplyCheckpointVersion + 1,
		Checksum:    nestedCommentReplyCheckpointChecksum,
		CompletedAt: time.Now().UTC(),
	}).Error; err != nil {
		t.Fatal(err)
	}
	err = MigrateNestedCommentReplies(db)
	if err == nil || !strings.Contains(err.Error(), "unexpected version or checksum") {
		t.Fatalf("mismatched checkpoint error=%v", err)
	}
}

func TestMigrateNestedCommentRepliesRollsBackBeforeCheckpoint(t *testing.T) {
	db, err := gorm.Open(
		sqlite.Open("file:nested-comment-reply-rollback?mode=memory&cache=shared"),
		&gorm.Config{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(
		&models.TicketComment{},
		&models.SchemaMigrationCheckpoint{},
	); err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`
		INSERT INTO ticket_comments
			(id, organization_id, project_id, ticket_id, actor_type, actor_id,
			 content, content_type, type, parent_id, reply_count, is_deleted)
		VALUES
			(1, 1, 1, 1, 'human', '1', 'root', 'text', 'public', NULL, 99, FALSE),
			(2, 1, 1, 1, 'human', '1', 'reply', 'text', 'public', 1, 0, FALSE),
			(3, 1, 1, 1, 'human', '1', 'nested', 'text', 'public', 2, 0, FALSE)
	`).Error; err != nil {
		t.Fatal(err)
	}

	injected := errors.New("injected comment reply checkpoint failure")
	const callbackName = "test:fail-comment-reply-checkpoint"
	if err := db.Callback().Create().Before("gorm:create").Register(
		callbackName,
		func(tx *gorm.DB) {
			checkpoint, ok := tx.Statement.Dest.(*models.SchemaMigrationCheckpoint)
			if ok && checkpoint != nil &&
				checkpoint.Key == nestedCommentReplyCheckpointKey {
				tx.AddError(injected)
			}
		},
	); err != nil {
		t.Fatal(err)
	}
	err = MigrateNestedCommentReplies(db)
	if !errors.Is(err, injected) {
		t.Fatalf("migration error=%v, want injected checkpoint failure", err)
	}

	var rows []models.TicketComment
	if err := db.Order("id ASC").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 || rows[0].ReplyCount != 99 ||
		rows[2].ParentID == nil || *rows[2].ParentID != 2 {
		t.Fatalf("failed checkpoint did not roll back comment changes: %+v", rows)
	}
	var checkpoints int64
	if err := db.Model(&models.SchemaMigrationCheckpoint{}).
		Where("key = ?", nestedCommentReplyCheckpointKey).
		Count(&checkpoints).Error; err != nil {
		t.Fatal(err)
	}
	if checkpoints != 0 {
		t.Fatalf("failed migration retained %d checkpoint(s)", checkpoints)
	}
}
