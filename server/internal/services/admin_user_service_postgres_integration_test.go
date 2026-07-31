package services

import (
	"context"
	"errors"
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

func TestPostgresLastPlatformAdministratorInvariantIsConcurrentSafe(
	t *testing.T,
) {
	tests := []struct {
		name   string
		mutate func(context.Context, *AdminUserService, uint) error
	}{
		{
			name: "demote",
			mutate: func(
				ctx context.Context,
				service *AdminUserService,
				userID uint,
			) error {
				role := models.PlatformRoleMember
				_, err := service.UpdateUser(
					ctx,
					models.HumanActor(userID),
					userID,
					&models.UserUpdateRequest{PlatformRole: &role},
				)
				return err
			},
		},
		{
			name: "deactivate",
			mutate: func(
				ctx context.Context,
				service *AdminUserService,
				userID uint,
			) error {
				status := models.UserStatusSuspended
				_, err := service.UpdateUser(
					ctx,
					models.HumanActor(userID),
					userID,
					&models.UserUpdateRequest{Status: &status},
				)
				return err
			},
		},
		{
			name: "delete",
			mutate: func(
				ctx context.Context,
				service *AdminUserService,
				userID uint,
			) error {
				return service.DeleteUser(
					ctx,
					models.HumanActor(userID),
					userID,
				)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := openAdminUserPostgresIntegrationDB(t, test.name)
			admins := []models.User{
				{
					Username: "concurrent-admin-a",
					Email:    "concurrent-admin-a@example.test",
				},
				{
					Username: "concurrent-admin-b",
					Email:    "concurrent-admin-b@example.test",
				},
			}
			for i := range admins {
				admins[i].PasswordHash = "hash"
				admins[i].PlatformRole = models.PlatformRolePlatformAdmin
				admins[i].Status = models.UserStatusActive
				if err := db.Create(&admins[i]).Error; err != nil {
					t.Fatal(err)
				}
			}

			service := NewAdminUserService(db)
			start := make(chan struct{})
			results := make(chan error, len(admins))
			var workers sync.WaitGroup
			for i := range admins {
				userID := admins[i].ID
				workers.Add(1)
				go func() {
					defer workers.Done()
					<-start
					ctx, cancel := context.WithTimeout(
						context.Background(),
						5*time.Second,
					)
					defer cancel()
					results <- test.mutate(ctx, service, userID)
				}()
			}
			close(start)
			workers.Wait()
			close(results)

			successes := 0
			invariantDenials := 0
			for err := range results {
				switch {
				case err == nil:
					successes++
				case errors.Is(err, ErrLastActivePlatformAdministrator):
					invariantDenials++
				default:
					t.Fatalf("unexpected concurrent mutation error: %v", err)
				}
			}
			if successes != 1 || invariantDenials != 1 {
				t.Fatalf(
					"concurrent outcomes: successes=%d invariant_denials=%d",
					successes,
					invariantDenials,
				)
			}

			var activeAdmins int64
			if err := db.Model(&models.User{}).
				Where(
					"platform_role = ? AND status = ?",
					models.PlatformRolePlatformAdmin,
					models.UserStatusActive,
				).
				Count(&activeAdmins).Error; err != nil {
				t.Fatal(err)
			}
			if activeAdmins != 1 {
				t.Fatalf("active platform administrators = %d, want 1", activeAdmins)
			}
		})
	}
}

func openAdminUserPostgresIntegrationDB(
	t *testing.T,
	fixture string,
) *gorm.DB {
	t.Helper()
	if os.Getenv("CHRONODESK_POSTGRES_INTEGRATION") != "1" {
		t.Skip("set CHRONODESK_POSTGRES_INTEGRATION=1 for PostgreSQL concurrency tests")
	}
	rawDSN := strings.TrimSpace(os.Getenv("CHRONODESK_POSTGRES_INTEGRATION_DSN"))
	if rawDSN == "" {
		t.Fatal("CHRONODESK_POSTGRES_INTEGRATION_DSN is required")
	}
	parsed, err := url.Parse(rawDSN)
	if err != nil {
		t.Fatal(err)
	}
	host := parsed.Hostname()
	if host != "localhost" {
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			t.Fatal("PostgreSQL integration tests require a loopback target")
		}
	}

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	schemaName := "chronodesk_admin_user_" + fixture + "_" + suffix
	adminDB, err := gorm.Open(postgres.Open(rawDSN), &gorm.Config{
		TranslateError: true,
		Logger:         logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatal(err)
	}
	adminSQL, err := adminDB.DB()
	if err != nil {
		t.Fatal(err)
	}
	quotedSchema := `"` + strings.ReplaceAll(schemaName, `"`, `""`) + `"`
	if err := adminDB.Exec("CREATE SCHEMA " + quotedSchema).Error; err != nil {
		t.Fatal(err)
	}

	runtimeURL := *parsed
	query := runtimeURL.Query()
	query.Set("search_path", schemaName)
	runtimeURL.RawQuery = query.Encode()
	db, err := gorm.Open(postgres.Open(runtimeURL.String()), &gorm.Config{
		TranslateError: true,
		Logger:         logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatal(err)
	}
	runtimeSQL, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	runtimeSQL.SetMaxOpenConns(4)
	runtimeSQL.SetMaxIdleConns(4)
	t.Cleanup(func() {
		_ = runtimeSQL.Close()
		_ = adminDB.Exec("DROP SCHEMA IF EXISTS " + quotedSchema + " CASCADE").Error
		_ = adminSQL.Close()
	})

	tableOnly := db.Session(&gorm.Session{NewDB: true})
	tableOnly.Config.IgnoreRelationshipsWhenMigrating = true
	if err := tableOnly.AutoMigrate(
		&models.User{},
		&models.LoginHistory{},
		&models.OTPTrustedDevice{},
	); err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`
		CREATE TABLE refresh_tokens (
			id BIGSERIAL PRIMARY KEY,
			user_id BIGINT NOT NULL,
			revoked BOOLEAN NOT NULL DEFAULT FALSE,
			revoked_at TIMESTAMPTZ
		)
	`).Error; err != nil {
		t.Fatal(err)
	}
	return db
}
