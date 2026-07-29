package services

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func openTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	name := strings.ReplaceAll(t.Name(), "/", "_")
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", name)

	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open sqlite db: %v", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("failed to get sql db: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)

	return db
}

func newTicketServiceForTest(t *testing.T, db *gorm.DB) *TicketService {
	t.Helper()
	return newTicketServiceWithDependenciesForTest(
		t,
		db,
		NewAgentNativeService(db),
		nil,
		0,
	)
}

func newTicketServiceWithDependenciesForTest(
	t *testing.T,
	db *gorm.DB,
	native *AgentNativeService,
	cache StatsCache,
	ttl time.Duration,
) *TicketService {
	t.Helper()
	service, err := NewTicketService(db, native, cache, ttl)
	if err != nil {
		t.Fatalf("NewTicketService() error = %v", err)
	}
	return service
}
