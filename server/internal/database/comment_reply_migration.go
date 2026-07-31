package database

import (
	"fmt"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/gorm"
)

const nestedCommentReplyMigrationBatchSize = 500

type nestedCommentReply struct {
	ID           uint `gorm:"column:id"`
	RootParentID uint `gorm:"column:root_parent_id"`
}

// MigrateNestedCommentReplies flattens legacy reply chains to the first
// top-level ancestor. New writes reject nested replies in the shared domain
// service; this migration keeps older rows reachable from the bounded replies
// endpoint instead of silently orphaning them from the Human UI.
func MigrateNestedCommentReplies(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("comment reply migration database is required")
	}
	if !db.Migrator().HasTable(&models.TicketComment{}) {
		return nil
	}

	for iteration := 0; iteration < 1_000; iteration++ {
		var rows []nestedCommentReply
		if err := db.Table("ticket_comments AS child").
			Select("child.id, parent.parent_id AS root_parent_id").
			Joins(
				`JOIN ticket_comments AS parent
				 ON parent.id = child.parent_id
				AND parent.ticket_id = child.ticket_id
				AND parent.organization_id = child.organization_id
				AND parent.project_id = child.project_id`,
			).
			Where("child.parent_id IS NOT NULL AND parent.parent_id IS NOT NULL").
			Order("child.id ASC").
			Limit(nestedCommentReplyMigrationBatchSize).
			Scan(&rows).Error; err != nil {
			return fmt.Errorf("load nested comment replies: %w", err)
		}
		if len(rows) == 0 {
			if err := db.Exec(`
				UPDATE ticket_comments AS parent
				SET reply_count = (
					SELECT COUNT(*)
					FROM ticket_comments AS reply
					WHERE reply.parent_id = parent.id
					  AND reply.ticket_id = parent.ticket_id
					  AND reply.organization_id = parent.organization_id
					  AND reply.project_id = parent.project_id
					  AND reply.is_deleted = ?
				)
			`, false).Error; err != nil {
				return fmt.Errorf("rebuild comment reply counts: %w", err)
			}
			return nil
		}
		for _, row := range rows {
			if row.ID == 0 || row.RootParentID == 0 || row.ID == row.RootParentID {
				return fmt.Errorf("nested comment reply cycle detected at comment %d", row.ID)
			}
			result := db.Model(&models.TicketComment{}).
				Where("id = ?", row.ID).
				UpdateColumn("parent_id", row.RootParentID)
			if result.Error != nil {
				return fmt.Errorf("flatten nested comment reply %d: %w", row.ID, result.Error)
			}
			if result.RowsAffected != 1 {
				return fmt.Errorf("nested comment reply %d disappeared", row.ID)
			}
		}
	}
	return fmt.Errorf("nested comment reply migration exceeded iteration limit")
}
