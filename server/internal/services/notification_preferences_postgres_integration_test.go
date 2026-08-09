package services

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestPostgresNotificationDailyLimitSerializesConcurrentDelivery(
	t *testing.T,
) {
	if os.Getenv("CHRONODESK_POSTGRES_INTEGRATION") != "1" {
		t.Skip(
			"set CHRONODESK_POSTGRES_INTEGRATION=1 for PostgreSQL notification preference evidence",
		)
	}
	rawDSN := strings.TrimSpace(
		os.Getenv("CHRONODESK_POSTGRES_INTEGRATION_DSN"),
	)
	if rawDSN == "" {
		t.Fatal("CHRONODESK_POSTGRES_INTEGRATION_DSN is required")
	}
	parsed, err := url.Parse(rawDSN)
	if err != nil {
		t.Fatalf("parse PostgreSQL integration DSN: %v", err)
	}
	host := parsed.Hostname()
	if host != "localhost" {
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			t.Fatal(
				"notification preference integration test requires a loopback PostgreSQL target",
			)
		}
	}
	admin, err := gorm.Open(postgres.Open(rawDSN), &gorm.Config{
		TranslateError: true,
		Logger:         logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open PostgreSQL notification administrator: %v", err)
	}
	adminSQL, err := admin.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = adminSQL.Close() })

	schemaName := fmt.Sprintf(
		"chronodesk_notification_preference_%d",
		time.Now().UnixNano(),
	)
	quotedSchema := `"` + schemaName + `"`
	if err := admin.Exec("CREATE SCHEMA " + quotedSchema).Error; err != nil {
		t.Fatalf("create notification preference schema: %v", err)
	}
	t.Cleanup(func() {
		if cleanupErr := admin.Exec(
			"DROP SCHEMA IF EXISTS " + quotedSchema + " CASCADE",
		).Error; cleanupErr != nil {
			t.Errorf(
				"drop notification preference schema: %v",
				cleanupErr,
			)
		}
	})

	scopedURL := *parsed
	query := scopedURL.Query()
	query.Set("search_path", schemaName)
	scopedURL.RawQuery = query.Encode()
	db, err := gorm.Open(postgres.Open(scopedURL.String()), &gorm.Config{
		TranslateError: true,
		Logger:         logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open schema-scoped notification database: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(4)
	sqlDB.SetMaxIdleConns(4)
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.AutoMigrate(
		&models.User{},
		&models.Notification{},
		&models.NotificationPreference{},
	); err != nil {
		t.Fatalf("migrate notification preference fixture: %v", err)
	}

	user := models.User{
		Username:     "postgres-preference-recipient",
		Email:        "postgres-preference-recipient@example.test",
		PasswordHash: "test-only",
		PlatformRole: models.PlatformRoleMember,
		Status:       models.UserStatusActive,
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&models.NotificationPreference{}).Create(
		map[string]any{
			"user_id":           user.ID,
			"notification_type": models.NotificationTypeTicketAssigned,
			"email_enabled":     true,
			"in_app_enabled":    true,
			"webhook_enabled":   false,
			"max_daily_count":   1,
			"batch_delivery":    false,
			"batch_interval":    60,
		},
	).Error; err != nil {
		t.Fatal(err)
	}

	service := NewNotificationServiceWithProtector(db, nil)
	type deliveryResult struct {
		created bool
		err     error
	}
	results := make(chan deliveryResult, 2)
	start := make(chan struct{})
	var ready sync.WaitGroup
	ready.Add(2)
	for index := 0; index < 2; index++ {
		index := index
		go func() {
			ready.Done()
			<-start
			sourceKey := fmt.Sprintf(
				"postgres-preference-concurrent-%d",
				index,
			)
			_, created, err :=
				service.persistTicketNotificationWithPreference(
					context.Background(),
					&models.Notification{
						OrganizationID: 1,
						ProjectID:      10,
						SourceEventKey: &sourceKey,
						Type: models.
							NotificationTypeTicketAssigned,
						Title:       "concurrent preference",
						Content:     "daily limit serialization",
						Priority:    models.NotificationPriorityHigh,
						Channel:     models.NotificationChannelInApp,
						RecipientID: user.ID,
					},
					map[string]any{"attempt": index},
				)
			results <- deliveryResult{created: created, err: err}
		}()
	}
	ready.Wait()
	close(start)

	createdCount := 0
	for index := 0; index < 2; index++ {
		result := <-results
		if result.err != nil {
			t.Fatal(result.err)
		}
		if result.created {
			createdCount++
		}
	}
	if createdCount != 1 {
		t.Fatalf(
			"visible concurrent deliveries = %d, want 1",
			createdCount,
		)
	}
	var visibleCount int64
	if err := db.Model(&models.Notification{}).
		Where(
			"(delivery_status IS NULL OR delivery_status <> ?)",
			NotificationDeliveryStatusSuppressedByPreference,
		).
		Count(&visibleCount).Error; err != nil {
		t.Fatal(err)
	}
	var suppressedCount int64
	if err := db.Model(&models.Notification{}).
		Where(
			"delivery_status = ?",
			NotificationDeliveryStatusSuppressedByPreference,
		).
		Count(&suppressedCount).Error; err != nil {
		t.Fatal(err)
	}
	if visibleCount != 1 || suppressedCount != 1 {
		t.Fatalf(
			"PostgreSQL delivery rows = visible:%d suppressed:%d, want 1/1",
			visibleCount,
			suppressedCount,
		)
	}
}

func TestPostgresEmailNotificationPreferenceCreateFollowsUpdateOrder(
	t *testing.T,
) {
	db, applicationName := openNotificationEmailPreferenceIntegrationDB(t)
	user := models.User{
		Username:     "postgres-email-preference-recipient",
		Email:        "postgres-email-preference-recipient@example.test",
		PasswordHash: "test-only",
		PlatformRole: models.PlatformRoleMember,
		Status:       models.UserStatusActive,
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	scope := seedNotificationProjectMembership(t, db, user.ID)
	service := NewNotificationServiceWithProtector(db, nil)
	ctx := notificationTestOperationContext(
		t,
		scope,
		models.SystemActor("postgres-email-preference-test"),
	)
	updateEmailPreference := func(emailEnabled bool) error {
		return service.UpdateNotificationPreferences(
			context.Background(),
			user.ID,
			[]models.NotificationPreference{{
				NotificationType: models.NotificationTypeSystemAlert,
				EmailEnabled:     emailEnabled,
				InAppEnabled:     true,
				MaxDailyCount:    50,
				BatchInterval:    60,
			}},
		)
	}
	type createResult struct {
		notification *models.Notification
		err          error
	}
	createEmail := func(title string) createResult {
		notification, err := service.CreateNotification(
			ctx,
			&models.NotificationCreateRequest{
				Type:        models.NotificationTypeSystemAlert,
				Title:       title,
				Content:     "PostgreSQL preference ordering evidence",
				Channel:     models.NotificationChannelEmail,
				RecipientID: user.ID,
			},
		)
		return createResult{notification: notification, err: err}
	}
	countIntents := func(t *testing.T) (int64, int64) {
		t.Helper()
		var events, deliveries int64
		if err := db.Model(&models.DomainEvent{}).Count(&events).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Model(&models.OutboxDelivery{}).Count(&deliveries).Error; err != nil {
			t.Fatal(err)
		}
		return events, deliveries
	}
	holdPreferenceUserLock := func(t *testing.T) *gorm.DB {
		t.Helper()
		tx := db.Begin()
		if tx.Error != nil {
			t.Fatal(tx.Error)
		}
		if err := lockNotificationPreferenceUser(tx, user.ID); err != nil {
			_ = tx.Rollback().Error
			t.Fatal(err)
		}
		return tx
	}
	waitForQueuedPreferenceLock := func(t *testing.T, wantWaiters int64) {
		t.Helper()
		if wantWaiters < 1 {
			t.Fatalf("queued preference lock waiters = %d, want at least 1", wantWaiters)
		}
		deadline := time.Now().Add(5 * time.Second)
		for {
			var blocked int64
			if err := db.Raw(
				"SELECT COUNT(*) FROM pg_stat_activity WHERE application_name = ? AND wait_event_type = 'Lock'",
				applicationName,
			).Scan(&blocked).Error; err != nil {
				t.Fatal(err)
			}
			if blocked >= wantWaiters {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf(
					"timed out waiting for queued notification preference locks: got %d, want at least %d",
					blocked,
					wantWaiters,
				)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	waitUpdate := func(t *testing.T, result <-chan error) {
		t.Helper()
		select {
		case err := <-result:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for notification preference update")
		}
	}
	waitCreate := func(
		t *testing.T,
		result <-chan createResult,
	) *models.Notification {
		t.Helper()
		select {
		case outcome := <-result:
			if outcome.err != nil {
				t.Fatal(outcome.err)
			}
			return outcome.notification
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for email notification creation")
			return nil
		}
	}

	t.Run("update first suppresses create without Outbox", func(t *testing.T) {
		if err := updateEmailPreference(true); err != nil {
			t.Fatal(err)
		}
		hold := holdPreferenceUserLock(t)
		updateDone := make(chan error, 1)
		go func() { updateDone <- updateEmailPreference(false) }()
		waitForQueuedPreferenceLock(t, 1)
		createDone := make(chan createResult, 1)
		go func() { createDone <- createEmail("update-first") }()
		waitForQueuedPreferenceLock(t, 2)
		if err := hold.Commit().Error; err != nil {
			t.Fatal(err)
		}
		waitUpdate(t, updateDone)
		notification := waitCreate(t, createDone)
		if notification.DeliveryStatus !=
			NotificationDeliveryStatusSuppressedByPreference ||
			!notification.IsRead || notification.ReadAt == nil ||
			notification.ExpiresAt == nil {
			t.Fatalf("update-first notification = %+v", notification)
		}
		events, deliveries := countIntents(t)
		if events != 0 || deliveries != 0 {
			t.Fatalf(
				"update-first intents = events:%d deliveries:%d, want 0/0",
				events,
				deliveries,
			)
		}
	})

	t.Run("create first queues one Outbox before update", func(t *testing.T) {
		if err := updateEmailPreference(true); err != nil {
			t.Fatal(err)
		}
		hold := holdPreferenceUserLock(t)
		createDone := make(chan createResult, 1)
		go func() { createDone <- createEmail("create-first") }()
		waitForQueuedPreferenceLock(t, 1)
		updateDone := make(chan error, 1)
		go func() { updateDone <- updateEmailPreference(false) }()
		waitForQueuedPreferenceLock(t, 2)
		if err := hold.Commit().Error; err != nil {
			t.Fatal(err)
		}
		notification := waitCreate(t, createDone)
		if notification.DeliveryStatus != "" || notification.IsRead ||
			notification.ReadAt != nil || notification.ExpiresAt != nil {
			t.Fatalf("create-first notification = %+v", notification)
		}
		waitUpdate(t, updateDone)
		events, deliveries := countIntents(t)
		if events != 1 || deliveries != 1 {
			t.Fatalf(
				"create-first intents = events:%d deliveries:%d, want 1/1",
				events,
				deliveries,
			)
		}
	})
}

func openNotificationEmailPreferenceIntegrationDB(
	t *testing.T,
) (*gorm.DB, string) {
	t.Helper()
	if os.Getenv("CHRONODESK_POSTGRES_INTEGRATION") != "1" {
		t.Skip(
			"set CHRONODESK_POSTGRES_INTEGRATION=1 for PostgreSQL notification preference evidence",
		)
	}
	rawDSN := strings.TrimSpace(
		os.Getenv("CHRONODESK_POSTGRES_INTEGRATION_DSN"),
	)
	if rawDSN == "" {
		t.Fatal("CHRONODESK_POSTGRES_INTEGRATION_DSN is required")
	}
	parsed, err := url.Parse(rawDSN)
	if err != nil {
		t.Fatalf("parse PostgreSQL integration DSN: %v", err)
	}
	host := parsed.Hostname()
	if host != "localhost" {
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			t.Fatal(
				"notification preference integration test requires a loopback PostgreSQL target",
			)
		}
	}
	admin, err := gorm.Open(postgres.Open(rawDSN), &gorm.Config{
		TranslateError: true,
		Logger:         logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open PostgreSQL notification administrator: %v", err)
	}
	adminSQL, err := admin.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = adminSQL.Close() })

	schemaName := fmt.Sprintf(
		"chronodesk_email_preference_%d",
		time.Now().UnixNano(),
	)
	quotedSchema := `"` + schemaName + `"`
	if err := admin.Exec("CREATE SCHEMA " + quotedSchema).Error; err != nil {
		t.Fatalf("create email preference schema: %v", err)
	}
	t.Cleanup(func() {
		if cleanupErr := admin.Exec(
			"DROP SCHEMA IF EXISTS " + quotedSchema + " CASCADE",
		).Error; cleanupErr != nil {
			t.Errorf("drop email preference schema: %v", cleanupErr)
		}
	})

	scopedURL := *parsed
	query := scopedURL.Query()
	query.Set("search_path", schemaName)
	query.Set("application_name", schemaName)
	scopedURL.RawQuery = query.Encode()
	db, err := gorm.Open(postgres.Open(scopedURL.String()), &gorm.Config{
		TranslateError: true,
		Logger:         logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open schema-scoped email preference database: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(4)
	sqlDB.SetMaxIdleConns(4)
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.AutoMigrate(
		&models.User{},
		&models.Notification{},
		&models.NotificationPreference{},
		&models.DomainEvent{},
		&models.OutboxDelivery{},
	); err != nil {
		t.Fatalf("migrate email preference fixture: %v", err)
	}
	return db, schemaName
}
