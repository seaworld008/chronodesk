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
