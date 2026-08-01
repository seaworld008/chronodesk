package database

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/services"
	"gorm.io/gorm"
)

func TestPostgresAdministrativeDirectoriesUseStableIDTieBreaks(
	t *testing.T,
) {
	db, _, suffix := openPostgresMembershipReleaseTestDB(
		t,
		"directory_lists",
	)
	if err := RunMigrations(
		db,
		services.EnsureProjectScopeMigrationMembership,
	); err != nil {
		t.Fatalf("migrate isolated directory schema: %v", err)
	}
	assertPostgresDirectoryIndexes(t, db)

	var project models.Project
	if err := db.Where("key = ?", DefaultProjectKey).
		First(&project).Error; err != nil {
		t.Fatalf("load default project: %v", err)
	}
	if err := db.Model(&models.Queue{}).
		Where("project_id = ?", project.ID).
		Update("status", models.QueueStatusArchived).Error; err != nil {
		t.Fatalf("archive bootstrap queue for isolated page evidence: %v", err)
	}

	tiedAt := time.Date(2026, time.July, 31, 12, 0, 0, 0, time.UTC)
	users := make([]models.User, 0, 151)
	for index := 0; index < 151; index++ {
		users = append(users, models.User{
			Username: fmt.Sprintf(
				"directory-%s-%03d",
				suffix,
				index,
			),
			Email: fmt.Sprintf(
				"directory-%s-%03d@example.test",
				suffix,
				index,
			),
			PasswordHash: "test-only-password-hash",
			PlatformRole: models.PlatformRoleMember,
			Status:       models.UserStatusActive,
		})
	}
	if err := db.CreateInBatches(&users, 50).Error; err != nil {
		t.Fatalf("seed directory users: %v", err)
	}

	memberships := make([]models.ProjectMembership, 0, 151)
	devices := make([]models.OTPTrustedDevice, 0, 151)
	configs := make([]models.SystemConfig, 0, 151)
	cleanupLogs := make([]models.CleanupLog, 0, 150)
	queues := make([]models.Queue, 0, 151)
	for index, user := range users {
		memberships = append(memberships, models.ProjectMembership{
			CreatedAt: tiedAt,
			ProjectID: project.ID,
			UserID:    user.ID,
			Role:      models.ProjectRoleObserver,
			IsActive:  true,
		})
		devices = append(devices, models.OTPTrustedDevice{
			CreatedAt: tiedAt,
			UpdatedAt: tiedAt,
			UserID:    users[0].ID,
			DeviceTokenHash: fmt.Sprintf(
				"postgres-directory-device-%s-%03d",
				suffix,
				index,
			),
			DeviceName: fmt.Sprintf("Device %03d", index),
			LastUsedAt: tiedAt,
			ExpiresAt:  tiedAt.Add(24 * time.Hour),
			Revoked:    false,
		})
		configs = append(configs, models.SystemConfig{
			CreatedAt: tiedAt,
			UpdatedAt: tiedAt,
			Key: fmt.Sprintf(
				"security.postgres-directory.%s.%03d",
				suffix,
				index,
			),
			Value:     "false",
			ValueType: "bool",
			Category:  services.CategorySecurity,
			Group:     "postgres-directory",
		})
		queues = append(queues, models.Queue{
			CreatedAt: tiedAt,
			UpdatedAt: tiedAt,
			ProjectID: project.ID,
			Key: models.QueueKey(fmt.Sprintf(
				"postgres-directory-%03d",
				index,
			)),
			Name:      "PostgreSQL tied queue",
			Status:    models.QueueStatusActive,
			IsDefault: false,
		})
		if index < 150 {
			cleanupLogs = append(cleanupLogs, models.CleanupLog{
				CreatedAt:      tiedAt,
				TaskType:       "login_history",
				Status:         "completed",
				StartTime:      tiedAt,
				RecordsDeleted: index,
				RetentionDays:  30,
				CutoffDate:     tiedAt.AddDate(0, 0, -30),
				TriggerType:    "scheduled",
			})
		}
	}
	if err := db.CreateInBatches(&memberships, 50).Error; err != nil {
		t.Fatalf("seed memberships directory: %v", err)
	}
	if err := db.CreateInBatches(&devices, 50).Error; err != nil {
		t.Fatalf("seed devices directory: %v", err)
	}
	if err := db.CreateInBatches(&configs, 50).Error; err != nil {
		t.Fatalf("seed configs directory: %v", err)
	}
	if err := db.CreateInBatches(&cleanupLogs, 50).Error; err != nil {
		t.Fatalf("seed cleanup directory: %v", err)
	}
	if err := db.CreateInBatches(&queues, 50).Error; err != nil {
		t.Fatalf("seed queues directory: %v", err)
	}

	configService := services.NewConfigService(db)
	firstConfigs, err := configService.ListConfigPage(
		context.Background(),
		services.CategorySecurity,
		services.DirectoryPageRequest{
			Page:      1,
			PageSize:  100,
			SortBy:    "created_at",
			SortOrder: "asc",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	secondConfigs, err := configService.ListConfigPage(
		context.Background(),
		services.CategorySecurity,
		services.DirectoryPageRequest{
			Page:      2,
			PageSize:  100,
			SortBy:    "created_at",
			SortOrder: "asc",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	assertStablePostgresDirectoryPages(
		t,
		firstConfigs.Total,
		idsFromSystemConfigs(firstConfigs.Items),
		idsFromSystemConfigs(secondConfigs.Items),
		151,
	)

	cleanupService := services.NewCleanupService(db)
	firstCleanup, err := cleanupService.ListCleanupLogPage(
		context.Background(),
		"login_history",
		services.DirectoryPageRequest{
			Page:      1,
			PageSize:  100,
			SortBy:    "created_at",
			SortOrder: "desc",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	secondCleanup, err := cleanupService.ListCleanupLogPage(
		context.Background(),
		"login_history",
		services.DirectoryPageRequest{
			Page:      2,
			PageSize:  100,
			SortBy:    "created_at",
			SortOrder: "desc",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	assertStablePostgresDirectoryPages(
		t,
		firstCleanup.Total,
		idsFromCleanupLogs(firstCleanup.Items),
		idsFromCleanupLogs(secondCleanup.Items),
		150,
	)

	trustedDeviceService := services.NewTrustedDeviceService(db)
	firstDevices, err := trustedDeviceService.ListTrustedDevicePage(
		context.Background(),
		users[0].ID,
		services.DirectoryPageRequest{
			Page:      1,
			PageSize:  100,
			SortBy:    "revoked",
			SortOrder: "asc",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	secondDevices, err := trustedDeviceService.ListTrustedDevicePage(
		context.Background(),
		users[0].ID,
		services.DirectoryPageRequest{
			Page:      2,
			PageSize:  100,
			SortBy:    "revoked",
			SortOrder: "asc",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	assertStablePostgresDirectoryPages(
		t,
		firstDevices.Total,
		idsFromTrustedDevices(firstDevices.Items),
		idsFromTrustedDevices(secondDevices.Items),
		151,
	)

	operationContext, err := services.WithOperationContext(
		context.Background(),
		services.OperationContext{
			Scope:  project.Scope(),
			Actor:  models.HumanActor(users[0].ID),
			Source: services.SourceProtocolHumanREST,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	projectService, err := services.NewProjectService(db)
	if err != nil {
		t.Fatal(err)
	}
	firstMemberships, err := projectService.ListHumanMembershipPage(
		operationContext,
		project.Scope(),
		services.DirectoryPageRequest{
			Page:      1,
			PageSize:  100,
			SortBy:    "role",
			SortOrder: "asc",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	secondMemberships, err := projectService.ListHumanMembershipPage(
		operationContext,
		project.Scope(),
		services.DirectoryPageRequest{
			Page:      2,
			PageSize:  100,
			SortBy:    "role",
			SortOrder: "asc",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	assertStablePostgresDirectoryPages(
		t,
		firstMemberships.Total,
		idsFromMembershipViews(firstMemberships.Items),
		idsFromMembershipViews(secondMemberships.Items),
		151,
	)

	firstQueues, err := projectService.ListQueuePage(
		operationContext,
		project.Scope(),
		services.DirectoryPageRequest{
			Page:      1,
			PageSize:  100,
			SortBy:    "name",
			SortOrder: "asc",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	secondQueues, err := projectService.ListQueuePage(
		operationContext,
		project.Scope(),
		services.DirectoryPageRequest{
			Page:      2,
			PageSize:  100,
			SortBy:    "name",
			SortOrder: "asc",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	assertStablePostgresDirectoryPages(
		t,
		firstQueues.Total,
		idsFromQueues(firstQueues.Items),
		idsFromQueues(secondQueues.Items),
		151,
	)
}

func assertPostgresDirectoryIndexes(t *testing.T, db *gorm.DB) {
	t.Helper()
	var names []string
	if err := db.Raw(`
		SELECT indexname
		FROM pg_indexes
		WHERE schemaname = CURRENT_SCHEMA()
		  AND indexname IN (
		    'idx_project_memberships_directory',
		    'idx_queues_directory',
		    'idx_otp_trusted_devices_directory',
		    'idx_system_configs_directory',
		    'idx_cleanup_logs_directory',
		    'idx_cleanup_logs_task_directory'
		  )
		ORDER BY indexname
	`).Scan(&names).Error; err != nil {
		t.Fatalf("read PostgreSQL directory indexes: %v", err)
	}
	if len(names) != 6 {
		t.Fatalf("PostgreSQL directory indexes = %v, want six", names)
	}
}

func assertStablePostgresDirectoryPages(
	t *testing.T,
	total int64,
	first []uint,
	second []uint,
	want int,
) {
	t.Helper()
	if total != int64(want) ||
		len(first) != min(want, 100) ||
		len(second) != max(want-100, 0) {
		t.Fatalf(
			"directory page sizes total=%d first=%d second=%d, want %d",
			total,
			len(first),
			len(second),
			want,
		)
	}
	seen := make(map[uint]struct{}, want)
	for _, id := range append(first, second...) {
		if _, duplicate := seen[id]; duplicate {
			t.Fatalf("directory ID %d appeared on multiple pages", id)
		}
		seen[id] = struct{}{}
	}
	if len(seen) != want {
		t.Fatalf("unique directory IDs=%d, want %d", len(seen), want)
	}
	if want > 1 {
		ascending := first[0] < first[len(first)-1]
		for index := 1; index < len(first); index++ {
			if (first[index-1] < first[index]) != ascending {
				t.Fatalf("first page does not follow one ID tie-break direction")
			}
		}
		for index := 1; index < len(second); index++ {
			if (second[index-1] < second[index]) != ascending {
				t.Fatalf("second page does not follow one ID tie-break direction")
			}
		}
		if len(second) > 0 &&
			((first[len(first)-1] < second[0]) != ascending) {
			t.Fatalf("page boundary does not follow the ID tie-break direction")
		}
	}
}

func idsFromSystemConfigs(items []models.SystemConfig) []uint {
	ids := make([]uint, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
	}
	return ids
}

func idsFromCleanupLogs(items []*models.CleanupLogResponse) []uint {
	ids := make([]uint, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
	}
	return ids
}

func idsFromTrustedDevices(items []*models.OTPTrustedDevice) []uint {
	ids := make([]uint, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
	}
	return ids
}

func idsFromMembershipViews(
	items []services.ProjectMembershipView,
) []uint {
	ids := make([]uint, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
	}
	return ids
}

func idsFromQueues(items []models.Queue) []uint {
	ids := make([]uint, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
	}
	return ids
}
