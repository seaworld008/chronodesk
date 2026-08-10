package database

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"

	"gorm.io/gorm"
)

func withPinnedGORMConnection(
	ctx context.Context,
	db *gorm.DB,
	run func(*gorm.DB) error,
) error {
	if ctx == nil || db == nil || run == nil {
		return errors.New(
			"pinned database connection requires context, database, and callback",
		)
	}
	if _, pinned := db.Statement.ConnPool.(*sql.Conn); pinned {
		return run(db.Session(&gorm.Session{
			NewDB:   true,
			Context: ctx,
		}))
	}
	if _, nested := db.Statement.ConnPool.(gorm.TxCommitter); nested {
		return errors.New(
			"pinned database connection requires a top-level database handle",
		)
	}
	return db.WithContext(ctx).Connection(func(pinned *gorm.DB) error {
		return run(pinned.Session(&gorm.Session{
			NewDB:   true,
			Context: ctx,
		}))
	})
}

func discardPinnedGORMConnection(db *gorm.DB) error {
	if db == nil || db.Statement == nil {
		return errors.New("discard pinned database connection requires a handle")
	}
	connection, ok := db.Statement.ConnPool.(*sql.Conn)
	if !ok || connection == nil {
		return errors.New(
			"discard pinned database connection requires a physical connection",
		)
	}
	err := connection.Raw(func(any) error {
		return driver.ErrBadConn
	})
	if err == nil ||
		errors.Is(err, driver.ErrBadConn) ||
		errors.Is(err, sql.ErrConnDone) {
		return nil
	}
	return fmt.Errorf("discard pinned physical connection: %w", err)
}
