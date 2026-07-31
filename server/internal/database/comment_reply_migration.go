package database

import (
	"errors"
	"fmt"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	nestedCommentReplyMigrationBatchSize = 500

	nestedCommentReplyCheckpointKey      = "20260731_nested_comment_replies_v1"
	nestedCommentReplyCheckpointVersion  = uint(1)
	nestedCommentReplyCheckpointChecksum = "faf915f3aece188b11e308bfc992b0f19e880b18e52de47f8fd3048c085dca75"
)

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
	if !db.Migrator().HasTable(&models.SchemaMigrationCheckpoint{}) {
		return errors.New("comment reply migration requires schema migration checkpoints")
	}

	return db.Transaction(func(tx *gorm.DB) error {
		completed, err := lockAndReadNestedCommentReplyMarker(tx)
		if err != nil {
			return err
		}
		if completed {
			return nil
		}
		if !tx.Migrator().HasTable(&models.TicketComment{}) {
			return nil
		}
		if err := flattenNestedCommentReplies(tx); err != nil {
			return err
		}
		if err := tx.Create(&models.SchemaMigrationCheckpoint{
			Key:         nestedCommentReplyCheckpointKey,
			Version:     nestedCommentReplyCheckpointVersion,
			Checksum:    nestedCommentReplyCheckpointChecksum,
			CompletedAt: time.Now().UTC(),
		}).Error; err != nil {
			return fmt.Errorf("record comment reply migration completion: %w", err)
		}
		return nil
	})
}

func flattenNestedCommentReplies(db *gorm.DB) error {
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

func lockAndReadNestedCommentReplyMarker(tx *gorm.DB) (bool, error) {
	if tx == nil {
		return false, errors.New("comment reply migration transaction is required")
	}
	if tx.Dialector.Name() == "postgres" {
		if err := tx.Exec(
			`SELECT pg_advisory_xact_lock(hashtextextended(?, 0))`,
			nestedCommentReplyCheckpointKey,
		).Error; err != nil {
			return false, fmt.Errorf("lock comment reply migration checkpoint: %w", err)
		}
	}
	var checkpoint models.SchemaMigrationCheckpoint
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("key = ?", nestedCommentReplyCheckpointKey).
		First(&checkpoint).Error
	switch {
	case err == nil:
		if checkpoint.Version != nestedCommentReplyCheckpointVersion ||
			checkpoint.Checksum != nestedCommentReplyCheckpointChecksum {
			return false, fmt.Errorf(
				"comment reply migration checkpoint %q has unexpected version or checksum",
				nestedCommentReplyCheckpointKey,
			)
		}
		return true, nil
	case errors.Is(err, gorm.ErrRecordNotFound):
		return false, nil
	default:
		return false, fmt.Errorf("read comment reply migration checkpoint: %w", err)
	}
}
