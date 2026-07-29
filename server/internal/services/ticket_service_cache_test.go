package services

import (
	"context"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/seaworld008/chronodesk/server/internal/models"
)

type fakeRedis struct {
	mu    sync.RWMutex
	store map[string]string
}

func (f *fakeRedis) Ping(ctx context.Context) error { return nil }

func (f *fakeRedis) Set(ctx context.Context, key string, value interface{}, expiration time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.store[key] = value.(string)
	return nil
}

func (f *fakeRedis) Get(ctx context.Context, key string) (string, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	if val, ok := f.store[key]; ok {
		return val, nil
	}
	return "", gorm.ErrRecordNotFound
}

func (f *fakeRedis) Del(ctx context.Context, keys ...string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, key := range keys {
		delete(f.store, key)
	}
	return nil
}

func (f *fakeRedis) Exists(ctx context.Context, keys ...string) (int64, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	var count int64
	for _, key := range keys {
		if _, ok := f.store[key]; ok {
			count++
		}
	}
	return count, nil
}

func (f *fakeRedis) Expire(ctx context.Context, key string, expiration time.Duration) error {
	return nil
}

func (f *fakeRedis) TTL(ctx context.Context, key string) (time.Duration, error) {
	return 0, nil
}

func (f *fakeRedis) Close() error { return nil }

var _ StatsCache = (*fakeRedis)(nil)

func setupCacheTestDB(t *testing.T) *gorm.DB {
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

	ticket := models.Ticket{
		TicketNumber: "C-001",
		Title:        "Open",
		Description:  "desc",
		Status:       models.TicketStatusOpen,
		Priority:     models.TicketPriorityHigh,
		Type:         models.TicketTypeIncident,
		Source:       models.TicketSourceWeb,
		CreatedByID:  &user.ID,
	}
	if err := db.Create(&ticket).Error; err != nil {
		t.Fatalf("failed to create ticket: %v", err)
	}

	return db
}

func TestGetTicketStatistics_Cache(t *testing.T) {
	db := setupCacheTestDB(t)
	cache := &fakeRedis{store: map[string]string{}}
	svc := newTicketServiceWithDependenciesForTest(
		t,
		db,
		NewAgentNativeService(db),
		cache,
		30*time.Second,
	)

	stats1, err := svc.GetTicketStatistics(1, "admin")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if err := db.Model(&models.Ticket{}).Where("ticket_number = ?", "C-001").Update("status", models.TicketStatusClosed).Error; err != nil {
		t.Fatalf("failed to update ticket: %v", err)
	}

	stats2, err := svc.GetTicketStatistics(1, "admin")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if stats1.Open != stats2.Open {
		t.Fatalf("expected cached stats, open=%d vs %d", stats1.Open, stats2.Open)
	}
}
