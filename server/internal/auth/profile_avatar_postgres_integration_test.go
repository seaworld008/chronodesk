package auth

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"mime/multipart"
	"net"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/services"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const (
	profileAvatarPostgresLockTimeout      = 5 * time.Second
	profileAvatarPostgresStatementTimeout = 10 * time.Second
)

type profileAvatarPostgresFixture struct {
	adminDB            *gorm.DB
	fixtureDB          *gorm.DB
	profileDB          *gorm.DB
	uploadDB           *gorm.DB
	profileApplication string
	uploadApplication  string
	storage            *services.LocalAttachmentStorage
	user               models.User
	profile            models.UserProfile
	oldAvatarURL       string
	oldAvatarKey       string
}

type profileAvatarPostgresUpdateBarrier struct {
	table       string
	reached     chan struct{}
	release     chan struct{}
	reachedOnce sync.Once
	releaseOnce sync.Once
}

type profileAvatarPostgresUploadResult struct {
	url string
	err error
}

type profileAvatarPostgresActivity struct {
	Query         string
	WaitEventType string
	WaitEvent     string
}

func TestPostgresAuthProfilePatchAndAvatarUploadSerializeWithoutLostFields(
	t *testing.T,
) {
	tests := []struct {
		name         string
		profileFirst bool
	}{
		{name: "profile_then_upload", profileFirst: true},
		{name: "upload_then_profile", profileFirst: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := openProfileAvatarPostgresFixture(t, test.name)
			barrierTable := "user_profiles"
			barrierDB := fixture.uploadDB
			if test.profileFirst {
				barrierTable = "users"
				barrierDB = fixture.profileDB
			}
			barrier := newProfileAvatarPostgresUpdateBarrier(barrierTable)
			t.Cleanup(barrier.Release)
			if err := barrierDB.Callback().
				Update().
				After("gorm:update").
				Register(
					"test:profile-avatar-postgres-"+test.name,
					barrier.AfterUpdate,
				); err != nil {
				t.Fatal(err)
			}

			authService := &AuthService{
				profileRepo: NewGormProfileRepository(fixture.profileDB),
			}
			userService := services.NewUserService(fixture.uploadDB)
			userService.SetAvatarStorage(fixture.storage, 2*1024*1024)

			firstName := "并发更新"
			phone := "+8613900139000"
			timezone := "Asia/Tokyo"
			language := "en"
			request := &UpdateProfileRequest{
				FirstName:   &firstName,
				PhoneNumber: &phone,
				Timezone:    &timezone,
				Language:    &language,
			}
			avatarFile, avatarHeader := profileAvatarPostgresUploadFile(
				t,
				profileAvatarPostgresPNG(t),
			)
			defer avatarFile.Close()

			operationContext, cancel := context.WithTimeout(
				context.Background(),
				profileAvatarPostgresStatementTimeout,
			)
			defer cancel()

			profileResult := make(chan error, 1)
			profileDone := make(chan struct{})
			startProfile := func() {
				go func() {
					profileResult <- authService.UpdateProfile(
						operationContext,
						fixture.user.ID,
						request,
					)
					close(profileDone)
				}()
			}

			uploadResult := make(
				chan profileAvatarPostgresUploadResult,
				1,
			)
			uploadDone := make(chan struct{})
			startUpload := func() {
				go func() {
					avatarURL, err := userService.UploadAvatar(
						operationContext,
						fixture.user.ID,
						avatarFile,
						avatarHeader,
					)
					uploadResult <- profileAvatarPostgresUploadResult{
						url: avatarURL,
						err: err,
					}
					close(uploadDone)
				}()
			}

			if test.profileFirst {
				startProfile()
				waitProfileAvatarPostgresBarrier(
					t,
					barrier,
					profileDone,
					"profile update",
				)
				startUpload()
				assertProfileAvatarPostgresUserLockWait(
					t,
					fixture.adminDB,
					fixture.uploadApplication,
					uploadDone,
				)
			} else {
				startUpload()
				waitProfileAvatarPostgresBarrier(
					t,
					barrier,
					uploadDone,
					"avatar upload",
				)
				startProfile()
				assertProfileAvatarPostgresUserLockWait(
					t,
					fixture.adminDB,
					fixture.profileApplication,
					profileDone,
				)
			}

			barrier.Release()
			if err := waitProfileAvatarPostgresError(
				t,
				profileResult,
				"profile update",
			); err != nil {
				t.Fatalf("UpdateProfile: %v", err)
			}
			uploaded := waitProfileAvatarPostgresUpload(
				t,
				uploadResult,
			)
			if uploaded.err != nil {
				t.Fatalf("UploadAvatar: %v", uploaded.err)
			}

			assertProfileAvatarPostgresFinalState(
				t,
				fixture,
				uploaded.url,
				firstName,
				phone,
				timezone,
				language,
			)
		})
	}
}

func openProfileAvatarPostgresFixture(
	t *testing.T,
	scenario string,
) *profileAvatarPostgresFixture {
	t.Helper()
	if os.Getenv("CHRONODESK_POSTGRES_INTEGRATION") != "1" {
		t.Skip(
			"set CHRONODESK_POSTGRES_INTEGRATION=1 for PostgreSQL profile/avatar concurrency evidence",
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
		t.Fatal("parse PostgreSQL integration DSN: invalid URL")
	}
	host := parsed.Hostname()
	if host != "localhost" {
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			t.Fatal(
				"PostgreSQL profile/avatar integration tests require a loopback target",
			)
		}
	}

	suffix := fmt.Sprintf("%x", time.Now().UnixNano())
	schemaName := "chronodesk_profile_avatar_" +
		strings.ReplaceAll(scenario, "-", "_") + "_" + suffix
	quotedSchema := `"` + strings.ReplaceAll(schemaName, `"`, `""`) + `"`
	profileApplication := "chronodesk-profile-" + suffix
	uploadApplication := "chronodesk-avatar-" + suffix

	adminDB, err := gorm.Open(postgres.Open(rawDSN), &gorm.Config{
		TranslateError: true,
		Logger:         logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open PostgreSQL profile/avatar admin: %v", err)
	}
	adminSQL, err := adminDB.DB()
	if err != nil {
		t.Fatal(err)
	}
	adminSQL.SetMaxOpenConns(4)
	adminSQL.SetMaxIdleConns(4)

	schemaCreated := false
	var runtimePools []io.Closer
	t.Cleanup(func() {
		for _, pool := range runtimePools {
			_ = pool.Close()
		}
		if schemaCreated {
			_ = adminDB.Exec(
				"DROP SCHEMA IF EXISTS " + quotedSchema + " CASCADE",
			).Error
		}
		_ = adminSQL.Close()
	})
	if err := adminDB.Exec("CREATE SCHEMA " + quotedSchema).Error; err != nil {
		t.Fatalf("create PostgreSQL profile/avatar schema: %v", err)
	}
	schemaCreated = true

	openRuntime := func(application string) *gorm.DB {
		t.Helper()
		runtimeURL := *parsed
		query := runtimeURL.Query()
		query.Set("search_path", schemaName)
		query.Set("application_name", application)
		query.Set("connect_timeout", "3")
		options := strings.TrimSpace(query.Get("options"))
		if options != "" {
			options += " "
		}
		options += fmt.Sprintf(
			"-c lock_timeout=%dms -c statement_timeout=%dms",
			profileAvatarPostgresLockTimeout.Milliseconds(),
			profileAvatarPostgresStatementTimeout.Milliseconds(),
		)
		query.Set("options", options)
		runtimeURL.RawQuery = query.Encode()

		db, openErr := gorm.Open(
			postgres.Open(runtimeURL.String()),
			&gorm.Config{
				TranslateError: true,
				Logger:         logger.Default.LogMode(logger.Silent),
			},
		)
		if openErr != nil {
			t.Fatalf(
				"open PostgreSQL profile/avatar runtime %q: %v",
				application,
				openErr,
			)
		}
		sqlDB, dbErr := db.DB()
		if dbErr != nil {
			t.Fatal(dbErr)
		}
		sqlDB.SetMaxOpenConns(4)
		sqlDB.SetMaxIdleConns(4)
		pingContext, cancel := context.WithTimeout(
			context.Background(),
			3*time.Second,
		)
		defer cancel()
		if pingErr := sqlDB.PingContext(pingContext); pingErr != nil {
			t.Fatalf(
				"ping PostgreSQL profile/avatar runtime %q: %v",
				application,
				pingErr,
			)
		}
		runtimePools = append(runtimePools, sqlDB)
		return db
	}

	fixtureDB := openRuntime("chronodesk-fixture-" + suffix)
	profileDB := openRuntime(profileApplication)
	uploadDB := openRuntime(uploadApplication)
	migrationDB := fixtureDB.Session(&gorm.Session{NewDB: true})
	migrationDB.Config.IgnoreRelationshipsWhenMigrating = true
	if err := migrationDB.AutoMigrate(
		&models.User{},
		&models.UserProfile{},
	); err != nil {
		t.Fatalf("migrate PostgreSQL profile/avatar fixture: %v", err)
	}

	storage, err := services.NewLocalAttachmentStorage(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	user := models.User{
		Username:      "profile-avatar-" + suffix,
		Email:         "profile-avatar-" + suffix + "@example.test",
		PasswordHash:  "hash",
		FirstName:     "Before",
		LastName:      "Preserved",
		DisplayName:   "Before Preserved",
		Phone:         "+8613800138000",
		Timezone:      "Asia/Shanghai",
		Language:      "zh-CN",
		Department:    "Support",
		JobTitle:      "Agent",
		PhoneVerified: true,
		PlatformRole:  models.PlatformRoleMember,
		Status:        models.UserStatusActive,
	}
	if err := fixtureDB.Create(&user).Error; err != nil {
		t.Fatalf("create PostgreSQL profile/avatar user: %v", err)
	}
	oldAvatarKey := fmt.Sprintf(
		"avatars/%d/%s.png",
		user.ID,
		uuid.NewString(),
	)
	oldAvatarURL := "/uploads/" + oldAvatarKey
	if _, err := storage.Put(
		context.Background(),
		oldAvatarKey,
		bytes.NewReader(profileAvatarPostgresPNG(t)),
		2*1024*1024,
	); err != nil {
		t.Fatalf("seed old avatar object: %v", err)
	}
	if err := fixtureDB.Model(&models.User{}).
		Where("id = ?", user.ID).
		Update("avatar", oldAvatarURL).Error; err != nil {
		t.Fatalf("seed old user avatar: %v", err)
	}
	user.Avatar = oldAvatarURL
	profile := models.UserProfile{
		UserID:   user.ID,
		Avatar:   oldAvatarURL,
		Bio:      "preserved bio",
		Phone:    user.Phone,
		Address:  "preserved address",
		Language: user.Language,
		Timezone: user.Timezone,
	}
	if err := fixtureDB.Create(&profile).Error; err != nil {
		t.Fatalf("create PostgreSQL profile/avatar profile: %v", err)
	}

	return &profileAvatarPostgresFixture{
		adminDB:            adminDB,
		fixtureDB:          fixtureDB,
		profileDB:          profileDB,
		uploadDB:           uploadDB,
		profileApplication: profileApplication,
		uploadApplication:  uploadApplication,
		storage:            storage,
		user:               user,
		profile:            profile,
		oldAvatarURL:       oldAvatarURL,
		oldAvatarKey:       oldAvatarKey,
	}
}

func newProfileAvatarPostgresUpdateBarrier(
	table string,
) *profileAvatarPostgresUpdateBarrier {
	return &profileAvatarPostgresUpdateBarrier{
		table:   table,
		reached: make(chan struct{}),
		release: make(chan struct{}),
	}
}

func (barrier *profileAvatarPostgresUpdateBarrier) AfterUpdate(db *gorm.DB) {
	if db.Statement.Table != barrier.table {
		return
	}
	barrier.reachedOnce.Do(func() {
		close(barrier.reached)
	})
	select {
	case <-barrier.release:
	case <-db.Statement.Context.Done():
		_ = db.AddError(db.Statement.Context.Err())
	}
}

func (barrier *profileAvatarPostgresUpdateBarrier) Release() {
	barrier.releaseOnce.Do(func() {
		close(barrier.release)
	})
}

func waitProfileAvatarPostgresBarrier(
	t *testing.T,
	barrier *profileAvatarPostgresUpdateBarrier,
	operationDone <-chan struct{},
	operation string,
) {
	t.Helper()
	select {
	case <-barrier.reached:
	case <-operationDone:
		t.Fatalf("%s completed before its transaction barrier", operation)
	case <-time.After(3 * time.Second):
		t.Fatalf("timed out waiting for %s transaction barrier", operation)
	}
}

func assertProfileAvatarPostgresUserLockWait(
	t *testing.T,
	adminDB *gorm.DB,
	application string,
	operationDone <-chan struct{},
) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	var last profileAvatarPostgresActivity
	for time.Now().Before(deadline) {
		select {
		case <-operationDone:
			t.Fatal(
				"concurrent operation completed before the first transaction committed",
			)
		default:
		}

		var activity profileAvatarPostgresActivity
		result := adminDB.Raw(`
			SELECT
				query,
				COALESCE(wait_event_type, '') AS wait_event_type,
				COALESCE(wait_event, '') AS wait_event
			FROM pg_stat_activity
			WHERE datname = current_database()
			  AND application_name = ?
			  AND state = 'active'
			  AND pid <> pg_backend_pid()
			ORDER BY query_start DESC
			LIMIT 1
		`, application).Scan(&activity)
		if result.Error != nil {
			t.Fatalf("inspect PostgreSQL lock wait: %v", result.Error)
		}
		if result.RowsAffected > 0 {
			last = activity
			query := strings.ToLower(activity.Query)
			if activity.WaitEventType == "Lock" &&
				strings.Contains(query, `from "users"`) &&
				strings.Contains(query, "for update") {
				select {
				case <-operationDone:
					t.Fatal(
						"concurrent operation completed while its users row lock was expected to wait",
					)
				default:
					return
				}
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf(
		"concurrent operation did not wait on users SELECT FOR UPDATE; last activity: wait=%s/%s query=%q",
		last.WaitEventType,
		last.WaitEvent,
		last.Query,
	)
}

func waitProfileAvatarPostgresError(
	t *testing.T,
	result <-chan error,
	operation string,
) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(profileAvatarPostgresStatementTimeout):
		t.Fatalf("timed out waiting for %s", operation)
		return nil
	}
}

func waitProfileAvatarPostgresUpload(
	t *testing.T,
	result <-chan profileAvatarPostgresUploadResult,
) profileAvatarPostgresUploadResult {
	t.Helper()
	select {
	case uploaded := <-result:
		return uploaded
	case <-time.After(profileAvatarPostgresStatementTimeout):
		t.Fatal("timed out waiting for avatar upload")
		return profileAvatarPostgresUploadResult{}
	}
}

func assertProfileAvatarPostgresFinalState(
	t *testing.T,
	fixture *profileAvatarPostgresFixture,
	avatarURL string,
	firstName string,
	phone string,
	timezone string,
	language string,
) {
	t.Helper()
	if avatarURL == "" || !strings.HasPrefix(
		avatarURL,
		fmt.Sprintf("/uploads/avatars/%d/", fixture.user.ID),
	) {
		t.Fatalf("unexpected committed avatar URL %q", avatarURL)
	}

	var user models.User
	if err := fixture.fixtureDB.First(&user, fixture.user.ID).Error; err != nil {
		t.Fatal(err)
	}
	var profile models.UserProfile
	if err := fixture.fixtureDB.
		Where("user_id = ?", fixture.user.ID).
		First(&profile).Error; err != nil {
		t.Fatal(err)
	}
	if user.Avatar != avatarURL || profile.Avatar != avatarURL {
		t.Fatalf(
			"avatar projections diverged after PostgreSQL interleaving: user=%q profile=%q want=%q",
			user.Avatar,
			profile.Avatar,
			avatarURL,
		)
	}
	if user.Phone != phone ||
		user.Timezone != timezone ||
		user.Language != language ||
		profile.Phone != phone ||
		profile.Timezone != timezone ||
		profile.Language != language {
		t.Fatalf(
			"profile fields were lost after PostgreSQL interleaving: user=%+v profile=%+v",
			user,
			profile,
		)
	}
	if user.FirstName != firstName ||
		user.LastName != fixture.user.LastName ||
		user.Department != fixture.user.Department ||
		user.JobTitle != fixture.user.JobTitle ||
		profile.Bio != fixture.profile.Bio ||
		profile.Address != fixture.profile.Address {
		t.Fatalf(
			"unrelated profile fields changed after PostgreSQL interleaving: user=%+v profile=%+v",
			user,
			profile,
		)
	}
	if user.PhoneVerified || user.PhoneVerifiedAt != nil {
		t.Fatalf(
			"changed phone retained verification: verified=%v verified_at=%v",
			user.PhoneVerified,
			user.PhoneVerifiedAt,
		)
	}

	newAvatarKey := strings.TrimPrefix(avatarURL, "/uploads/")
	reader, err := fixture.storage.Open(context.Background(), newAvatarKey)
	if err != nil {
		t.Fatalf("committed avatar object B is missing: %v", err)
	}
	_ = reader.Close()
	oldReader, err := fixture.storage.Open(
		context.Background(),
		fixture.oldAvatarKey,
	)
	if err == nil {
		_ = oldReader.Close()
		t.Fatalf(
			"superseded avatar object A still exists after commit: %s",
			fixture.oldAvatarURL,
		)
	}
}

func profileAvatarPostgresUploadFile(
	t *testing.T,
	payload []byte,
) (*os.File, *multipart.FileHeader) {
	t.Helper()
	file, err := os.CreateTemp(t.TempDir(), "avatar-postgres-*")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write(payload); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if _, err := file.Seek(0, 0); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	return file, &multipart.FileHeader{
		Filename: "avatar.png",
		Size:     int64(len(payload)),
	}
}

func profileAvatarPostgresPNG(t *testing.T) []byte {
	t.Helper()
	source := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for x := 0; x < 4; x++ {
		for y := 0; y < 4; y++ {
			source.Set(x, y, color.RGBA{
				R: 30,
				G: 120,
				B: 220,
				A: 255,
			})
		}
	}
	var payload bytes.Buffer
	if err := png.Encode(&payload, source); err != nil {
		t.Fatal(err)
	}
	return payload.Bytes()
}
