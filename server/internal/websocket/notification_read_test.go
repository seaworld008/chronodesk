package websocket

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/scopeddb"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type notificationReadStoreFuncs struct {
	mark  func(context.Context, uint, uint) error
	count func(context.Context, uint) (int64, error)
}

func (store notificationReadStoreFuncs) MarkAsRead(
	ctx context.Context,
	notificationID uint,
	userID uint,
) error {
	return store.mark(ctx, notificationID, userID)
}

func (store notificationReadStoreFuncs) GetUnreadCount(
	ctx context.Context,
	userID uint,
) (int64, error) {
	return store.count(ctx, userID)
}

func TestDatabaseNotificationReadHandlerUsesOneExactScopedTransaction(
	t *testing.T,
) {
	db := openNotificationReadTestDB(t)
	var (
		orderMu sync.Mutex
		order   []string
	)
	record := func(step string) {
		orderMu.Lock()
		defer orderMu.Unlock()
		order = append(order, step)
	}
	requireScope := func(ctx context.Context) error {
		reusable, err := scopeddb.CanReuseProjectScopeTransaction(
			ctx,
			websocketTestScopeA,
		)
		if err != nil {
			return err
		}
		if !reusable {
			return errors.New("exact project transaction is not active")
		}
		return nil
	}

	handler := NewDatabaseNotificationReadHandler(
		db,
		func(
			ctx context.Context,
			scope models.ProjectScope,
			userID uint,
		) (context.Context, error) {
			record("context")
			if scopeddb.HasTransaction(ctx) {
				return nil, errors.New(
					"operation context factory ran inside a transaction",
				)
			}
			if scope != websocketTestScopeA || userID != 202 {
				return nil, errors.New("unexpected operation identity")
			}
			return ctx, nil
		},
		func(
			ctx context.Context,
			scope models.ProjectScope,
			userID uint,
		) error {
			record("revalidate")
			if scope != websocketTestScopeA || userID != 202 {
				return errors.New("unexpected authorization identity")
			}
			return requireScope(ctx)
		},
		notificationReadStoreFuncs{
			mark: func(
				ctx context.Context,
				notificationID uint,
				userID uint,
			) error {
				record("mark")
				if err := requireScope(ctx); err != nil {
					return err
				}
				return db.WithContext(ctx).
					Table("websocket_notification_states").
					Where("id = ? AND user_id = ?", notificationID, userID).
					Update("is_read", true).Error
			},
			count: func(
				ctx context.Context,
				userID uint,
			) (int64, error) {
				record("count")
				if err := requireScope(ctx); err != nil {
					return 0, err
				}
				var count int64
				err := db.WithContext(ctx).
					Table("websocket_notification_states").
					Where("user_id = ? AND is_read = ?", userID, false).
					Count(&count).Error
				return count, err
			},
		},
	)

	count, err := handler(
		context.Background(),
		websocketTestScopeA,
		202,
		88,
	)
	if err != nil {
		t.Fatalf("atomic notification read: %v", err)
	}
	if count != 1 {
		t.Fatalf("unread count = %d, want 1", count)
	}
	orderMu.Lock()
	defer orderMu.Unlock()
	want := []string{"context", "revalidate", "mark", "count"}
	if fmt.Sprint(order) != fmt.Sprint(want) {
		t.Fatalf("command order = %v, want %v", order, want)
	}
}

func TestDatabaseNotificationReadHandlerRollsBackWhenRevocationWinsBarrier(
	t *testing.T,
) {
	db := openNotificationReadTestDB(t)
	revalidationStarted := make(chan struct{})
	revocationCommitted := make(chan struct{})
	var markCalls atomic.Int32
	revoked := errors.New("membership revoked")

	handler := NewDatabaseNotificationReadHandler(
		db,
		func(
			ctx context.Context,
			_ models.ProjectScope,
			_ uint,
		) (context.Context, error) {
			return ctx, nil
		},
		func(
			ctx context.Context,
			_ models.ProjectScope,
			userID uint,
		) error {
			if err := db.WithContext(ctx).
				Exec(
					"INSERT INTO websocket_authorization_markers (user_id) VALUES (?)",
					userID,
				).Error; err != nil {
				return err
			}
			close(revalidationStarted)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-revocationCommitted:
				return revoked
			}
		},
		notificationReadStoreFuncs{
			mark: func(context.Context, uint, uint) error {
				markCalls.Add(1)
				return nil
			},
			count: func(context.Context, uint) (int64, error) {
				return 0, nil
			},
		},
	)

	result := make(chan error, 1)
	go func() {
		_, err := handler(
			context.Background(),
			websocketTestScopeA,
			202,
			88,
		)
		result <- err
	}()
	select {
	case <-revalidationStarted:
	case <-time.After(time.Second):
		t.Fatal("authorization barrier was not reached")
	}
	close(revocationCommitted)

	select {
	case err := <-result:
		if !errors.Is(err, revoked) {
			t.Fatalf("notification read error = %v, want revocation", err)
		}
	case <-time.After(time.Second):
		t.Fatal("notification read did not return after revocation")
	}
	if markCalls.Load() != 0 {
		t.Fatalf("revoked command marked notification %d time(s)", markCalls.Load())
	}
	var markerCount int64
	if err := db.Table("websocket_authorization_markers").
		Count(&markerCount).Error; err != nil {
		t.Fatal(err)
	}
	if markerCount != 0 {
		t.Fatalf(
			"revoked command committed %d authorization marker(s)",
			markerCount,
		)
	}
}

func TestClientDisconnectCancelsAndRollsBackNotificationRead(t *testing.T) {
	db := openNotificationReadTestDB(t)
	countStarted := make(chan struct{})
	handlerReturned := make(chan struct{})
	handler := NewDatabaseNotificationReadHandler(
		db,
		func(
			ctx context.Context,
			_ models.ProjectScope,
			_ uint,
		) (context.Context, error) {
			return ctx, nil
		},
		func(
			ctx context.Context,
			scope models.ProjectScope,
			_ uint,
		) error {
			reusable, err :=
				scopeddb.CanReuseProjectScopeTransaction(ctx, scope)
			if err != nil {
				return err
			}
			if !reusable {
				return errors.New("authorization transaction is not exact")
			}
			return nil
		},
		notificationReadStoreFuncs{
			mark: func(
				ctx context.Context,
				notificationID uint,
				userID uint,
			) error {
				return db.WithContext(ctx).
					Table("websocket_notification_states").
					Where("id = ? AND user_id = ?", notificationID, userID).
					Update("is_read", true).Error
			},
			count: func(
				ctx context.Context,
				_ uint,
			) (int64, error) {
				close(countStarted)
				<-ctx.Done()
				return 0, ctx.Err()
			},
		},
	)
	SetNotificationReadHandler(handler)
	t.Cleanup(func() {
		SetNotificationReadHandler(nil)
	})

	client := newWebSocketTestClient(
		newAuthorizedWebSocketTestHub(),
		202,
		websocketTestScopeA,
		1,
	)
	go func() {
		defer close(handlerReturned)
		client.handleMarkRead(map[string]interface{}{
			"notification_id": float64(88),
		})
	}()

	select {
	case <-countStarted:
	case <-time.After(time.Second):
		t.Fatal("notification count barrier was not reached")
	}
	client.close()
	select {
	case <-handlerReturned:
	case <-time.After(time.Second):
		t.Fatal("disconnect did not cancel in-flight notification read")
	}

	var isRead bool
	if err := db.Table("websocket_notification_states").
		Select("is_read").
		Where("id = ?", 88).
		Scan(&isRead).Error; err != nil {
		t.Fatal(err)
	}
	if isRead {
		t.Fatal("disconnect committed mark_read instead of rolling it back")
	}
}

func openNotificationReadTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := filepath.Join(t.TempDir(), "notification-read.db") +
		"?_busy_timeout=5000"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`CREATE TABLE websocket_notification_states (
			id INTEGER PRIMARY KEY,
			user_id INTEGER NOT NULL,
			is_read BOOLEAN NOT NULL
		)`,
		`CREATE TABLE websocket_authorization_markers (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id INTEGER NOT NULL
		)`,
		`INSERT INTO websocket_notification_states (id, user_id, is_read)
		 VALUES (88, 202, false), (89, 202, false)`,
	} {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatalf("create notification read fixture: %v", err)
		}
	}
	return db
}
