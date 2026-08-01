package auth

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func setupProfileRepoTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	dsn := "file:" + t.Name() + "?mode=memory&cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open sqlite db: %v", err)
	}

	if err := db.AutoMigrate(&models.User{}, &models.UserProfile{}); err != nil {
		t.Fatalf("failed to migrate sqlite schema: %v", err)
	}

	return db
}

func createProfileRepoTestUser(t *testing.T, db *gorm.DB) models.User {
	t.Helper()

	user := models.User{
		Username:     "profile_repo_user",
		Email:        "profile_repo_user@example.com",
		PasswordHash: "$2a$10$7EqJtq98hPqEX7fNZaFWoOPKfN6obU6fY9w7NwQDJ5D6LzA6gW6Ga",
		PlatformRole: models.PlatformRoleMember,
		Status:       models.UserStatusActive,
	}

	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("failed to create test user: %v", err)
	}

	return user
}

func TestGormProfileRepository_CreateAndGetByUserID(t *testing.T) {
	db := setupProfileRepoTestDB(t)
	user := createProfileRepoTestUser(t, db)
	repo := NewGormProfileRepository(db)

	ctx := context.Background()
	profile := &UserProfile{
		UserID:      user.ID,
		FirstName:   "Smoke",
		LastName:    "Tester",
		DisplayName: "Smoke Tester",
		Phone:       "1234567890",
		Department:  "QA",
		Position:    "Engineer",
		Timezone:    "UTC",
		Language:    "en",
	}

	if err := repo.Create(ctx, profile); err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if profile.ID == 0 {
		t.Fatalf("expected profile ID to be set after create")
	}

	got, err := repo.GetByUserID(ctx, user.ID)
	if err != nil {
		t.Fatalf("GetByUserID returned error: %v", err)
	}

	if got.FirstName != "Smoke" || got.LastName != "Tester" {
		t.Fatalf("expected synced name fields, got first=%q last=%q", got.FirstName, got.LastName)
	}
	if got.Department != "QA" || got.Position != "Engineer" {
		t.Fatalf("expected synced org fields, got department=%q position=%q", got.Department, got.Position)
	}
	if got.Timezone != "UTC" || got.Language != "en" {
		t.Fatalf("expected profile timezone/language to persist, got timezone=%q language=%q", got.Timezone, got.Language)
	}
}

func TestGormProfileRepository_UpdateSyncsUserFields(t *testing.T) {
	db := setupProfileRepoTestDB(t)
	user := createProfileRepoTestUser(t, db)
	repo := NewGormProfileRepository(db)

	ctx := context.Background()
	profile := &UserProfile{
		UserID:      user.ID,
		FirstName:   "Before",
		LastName:    "User",
		DisplayName: "Before User",
		Department:  "Support",
		Position:    "Agent",
		Timezone:    "UTC",
		Language:    "en",
	}

	if err := repo.Create(ctx, profile); err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	firstName := "After"
	lastName := "Editor"
	phone := "18800001111"
	timezone := "Asia/Shanghai"
	language := "zh-CN"
	if err := repo.Patch(ctx, user.ID, ProfilePatch{
		FirstName: &firstName,
		LastName:  &lastName,
		Phone:     &phone,
		Timezone:  &timezone,
		Language:  &language,
	}); err != nil {
		t.Fatalf("Patch returned error: %v", err)
	}

	got, err := repo.GetByUserID(ctx, user.ID)
	if err != nil {
		t.Fatalf("GetByUserID returned error: %v", err)
	}

	if got.FirstName != "After" || got.LastName != "Editor" {
		t.Fatalf("expected updated names, got first=%q last=%q", got.FirstName, got.LastName)
	}
	if got.Department != "Support" || got.Position != "Agent" {
		t.Fatalf("patch overwrote omitted org info, got department=%q position=%q", got.Department, got.Position)
	}
	if got.Phone != "18800001111" {
		t.Fatalf("expected updated phone, got %q", got.Phone)
	}
}

func TestGormProfileRepositoryPatchLocksRowsAndUpdatesOnlyRequestedFields(
	t *testing.T,
) {
	db := setupProfileRepoTestDB(t)
	user := createProfileRepoTestUser(t, db)
	repo := NewGormProfileRepository(db)
	currentAvatar := "/uploads/avatars/1/00000000-0000-4000-8000-000000000001.png"
	profile := &UserProfile{
		UserID:      user.ID,
		FirstName:   "Before",
		LastName:    "User",
		DisplayName: "Before User",
		Avatar:      currentAvatar,
		Phone:       "+8613800138000",
		Department:  "Support",
		Position:    "Agent",
		Timezone:    "Asia/Shanghai",
		Language:    "zh-CN",
	}
	if err := repo.Create(context.Background(), profile); err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&models.User{}).
		Where("id = ?", user.ID).
		Updates(map[string]any{
			"phone_verified":    true,
			"phone_verified_at": time.Now(),
		}).Error; err != nil {
		t.Fatal(err)
	}

	var lockTables []string
	if err := db.Callback().Query().After("gorm:query").Register(
		"test:profile-patch-locks",
		func(query *gorm.DB) {
			lockClause, exists := query.Statement.Clauses["FOR"]
			if !exists {
				return
			}
			locking, ok := lockClause.Expression.(clause.Locking)
			if !ok || locking.Strength != "UPDATE" {
				return
			}
			lockTables = append(lockTables, query.Statement.Table)
		},
	); err != nil {
		t.Fatal(err)
	}
	type updateStatement struct {
		table string
		sql   string
	}
	var updates []updateStatement
	if err := db.Callback().Update().After("gorm:update").Register(
		"test:profile-patch-columns",
		func(update *gorm.DB) {
			if update.Statement.Table != "users" &&
				update.Statement.Table != "user_profiles" {
				return
			}
			updates = append(updates, updateStatement{
				table: update.Statement.Table,
				sql: strings.ToLower(update.Dialector.Explain(
					update.Statement.SQL.String(),
					update.Statement.Vars...,
				)),
			})
		},
	); err != nil {
		t.Fatal(err)
	}

	phone := "+8613900139000"
	if err := repo.Patch(
		context.Background(),
		user.ID,
		ProfilePatch{Phone: &phone},
	); err != nil {
		t.Fatal(err)
	}

	if fmt.Sprint(lockTables) != "[users user_profiles]" {
		t.Fatalf("profile patch lock order = %v", lockTables)
	}
	if len(updates) != 2 {
		t.Fatalf("profile patch statements = %+v, want two projections", updates)
	}
	for _, update := range updates {
		if !strings.Contains(update.sql, "phone") {
			t.Errorf("%s patch omits phone: %s", update.table, update.sql)
		}
		for _, forbidden := range []string{
			"avatar",
			"timezone",
			"language",
			"first_name",
			"last_name",
			"department",
			"job_title",
		} {
			if strings.Contains(update.sql, forbidden) {
				t.Errorf(
					"%s phone patch writes omitted %s: %s",
					update.table,
					forbidden,
					update.sql,
				)
			}
		}
	}

	got, err := repo.GetByUserID(context.Background(), user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Phone != phone ||
		got.Avatar != currentAvatar ||
		got.Timezone != profile.Timezone ||
		got.Language != profile.Language ||
		got.Department != profile.Department ||
		got.Position != profile.Position {
		t.Fatalf("field-level patch result = %+v", got)
	}
}

func TestGormProfileRepositoryAvatarOmissionAndExactValueArePersistentNoOps(
	t *testing.T,
) {
	db := setupProfileRepoTestDB(t)
	user := createProfileRepoTestUser(t, db)
	repo := NewGormProfileRepository(db)
	currentAvatar := "/uploads/avatars/1/00000000-0000-4000-8000-000000000001.png"
	profile := &UserProfile{
		UserID:   user.ID,
		Avatar:   currentAvatar,
		Phone:    "+8613800138000",
		Timezone: "Asia/Shanghai",
		Language: "zh-CN",
	}
	if err := repo.Create(context.Background(), profile); err != nil {
		t.Fatal(err)
	}

	updateCalls := 0
	if err := db.Callback().Update().After("gorm:update").Register(
		"test:profile-avatar-no-op",
		func(update *gorm.DB) {
			if update.Statement.Table == "users" ||
				update.Statement.Table == "user_profiles" {
				updateCalls++
			}
		},
	); err != nil {
		t.Fatal(err)
	}

	if err := repo.Patch(
		context.Background(),
		user.ID,
		ProfilePatch{},
	); err != nil {
		t.Fatal(err)
	}
	if err := repo.Patch(
		context.Background(),
		user.ID,
		ProfilePatch{Avatar: &currentAvatar},
	); err != nil {
		t.Fatal(err)
	}
	if updateCalls != 0 {
		t.Fatalf("omitted/exact avatar emitted %d persistent updates", updateCalls)
	}

	clear := ""
	if err := repo.Patch(
		context.Background(),
		user.ID,
		ProfilePatch{Avatar: &clear},
	); err != nil {
		t.Fatal(err)
	}
	if updateCalls != 2 {
		t.Fatalf("explicit avatar clear emitted %d updates, want two", updateCalls)
	}
	got, err := repo.GetByUserID(context.Background(), user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Avatar != "" ||
		got.Phone != profile.Phone ||
		got.Timezone != profile.Timezone ||
		got.Language != profile.Language {
		t.Fatalf("explicit avatar clear changed unrelated fields: %+v", got)
	}
}

type barrierProfileRepository struct {
	mu sync.Mutex

	profile UserProfile

	patchAttemptOnce sync.Once
	patchAcquireOnce sync.Once
	patchAttempted   chan struct{}
	patchAcquired    chan struct{}
	patchRelease     chan struct{}

	uploadAttemptOnce sync.Once
	uploadAcquireOnce sync.Once
	uploadAttempted   chan struct{}
	uploadAcquired    chan struct{}
	uploadRelease     chan struct{}
}

func (repo *barrierProfileRepository) Create(
	context.Context,
	*UserProfile,
) error {
	return nil
}

func (repo *barrierProfileRepository) GetByUserID(
	context.Context,
	uint,
) (*UserProfile, error) {
	repo.mu.Lock()
	defer repo.mu.Unlock()
	result := repo.profile
	return &result, nil
}

func (repo *barrierProfileRepository) Patch(
	_ context.Context,
	_ uint,
	patch ProfilePatch,
) error {
	repo.patchAttemptOnce.Do(func() {
		if repo.patchAttempted != nil {
			close(repo.patchAttempted)
		}
	})
	repo.mu.Lock()
	defer repo.mu.Unlock()
	repo.patchAcquireOnce.Do(func() {
		if repo.patchAcquired != nil {
			close(repo.patchAcquired)
		}
	})
	if repo.patchRelease != nil {
		<-repo.patchRelease
	}
	if patch.FirstName != nil {
		repo.profile.FirstName = *patch.FirstName
	}
	if patch.LastName != nil {
		repo.profile.LastName = *patch.LastName
	}
	if patch.Phone != nil {
		repo.profile.Phone = *patch.Phone
	}
	if patch.Avatar != nil {
		repo.profile.Avatar = *patch.Avatar
	}
	if patch.Timezone != nil {
		repo.profile.Timezone = *patch.Timezone
	}
	if patch.Language != nil {
		repo.profile.Language = *patch.Language
	}
	return nil
}

func (repo *barrierProfileRepository) Delete(context.Context, uint) error {
	return nil
}

func (repo *barrierProfileRepository) uploadAvatar(
	avatarURL string,
	payload []byte,
) error {
	if err := os.WriteFile(avatarURL, payload, 0o600); err != nil {
		return err
	}
	repo.uploadAttemptOnce.Do(func() {
		if repo.uploadAttempted != nil {
			close(repo.uploadAttempted)
		}
	})
	repo.mu.Lock()
	defer repo.mu.Unlock()
	repo.uploadAcquireOnce.Do(func() {
		if repo.uploadAcquired != nil {
			close(repo.uploadAcquired)
		}
	})
	if repo.uploadRelease != nil {
		<-repo.uploadRelease
	}
	repo.profile.Avatar = avatarURL
	return nil
}

func TestAuthProfilePatchAndAvatarUploadSerializeWithoutLostFields(t *testing.T) {
	for _, profileFirst := range []bool{true, false} {
		name := "upload_then_profile"
		if profileFirst {
			name = "profile_then_upload"
		}
		t.Run(name, func(t *testing.T) {
			patchAttempted := make(chan struct{})
			patchAcquired := make(chan struct{})
			patchRelease := make(chan struct{})
			uploadAttempted := make(chan struct{})
			uploadAcquired := make(chan struct{})
			uploadRelease := make(chan struct{})
			repo := &barrierProfileRepository{
				profile: UserProfile{
					UserID:   42,
					Avatar:   "avatar-a",
					Phone:    "+8613800138000",
					Timezone: "Asia/Shanghai",
					Language: "zh-CN",
				},
				patchAttempted:  patchAttempted,
				patchAcquired:   patchAcquired,
				uploadAttempted: uploadAttempted,
				uploadAcquired:  uploadAcquired,
			}
			if profileFirst {
				repo.patchRelease = patchRelease
			} else {
				repo.uploadRelease = uploadRelease
			}
			service := &AuthService{profileRepo: repo}
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
			avatarB := filepath.Join(t.TempDir(), "avatar-b.png")

			profileResult := make(chan error, 1)
			uploadResult := make(chan error, 1)
			startProfile := func() {
				go func() {
					profileResult <- service.UpdateProfile(
						context.Background(),
						42,
						request,
					)
				}()
			}
			startUpload := func() {
				go func() {
					uploadResult <- repo.uploadAvatar(
						avatarB,
						[]byte("avatar-b"),
					)
				}()
			}

			if profileFirst {
				startProfile()
				<-patchAcquired
				startUpload()
				<-uploadAttempted
				close(patchRelease)
			} else {
				startUpload()
				<-uploadAcquired
				startProfile()
				<-patchAttempted
				close(uploadRelease)
			}
			if err := <-profileResult; err != nil {
				t.Fatalf("UpdateProfile: %v", err)
			}
			if err := <-uploadResult; err != nil {
				t.Fatalf("upload avatar: %v", err)
			}

			got, err := repo.GetByUserID(context.Background(), 42)
			if err != nil {
				t.Fatal(err)
			}
			if got.Avatar != avatarB ||
				got.FirstName != firstName ||
				got.Phone != phone ||
				got.Timezone != timezone ||
				got.Language != language {
				t.Fatalf("interleaved profile = %+v", got)
			}
			if _, err := os.Stat(avatarB); err != nil {
				t.Fatalf("new avatar object is missing: %v", err)
			}
		})
	}
}

func TestGormProfileRepositoryBackfillsLegacyUserWithoutDroppingFields(t *testing.T) {
	db := setupProfileRepoTestDB(t)
	user := createProfileRepoTestUser(t, db)
	if err := db.Model(&user).Updates(map[string]any{
		"first_name":     "Legacy",
		"last_name":      "User",
		"avatar":         "/uploads/avatars/1/00000000-0000-4000-8000-000000000001.png",
		"phone":          "+8613800138000",
		"timezone":       "Asia/Tokyo",
		"language":       "zh-CN",
		"phone_verified": true,
	}).Error; err != nil {
		t.Fatal(err)
	}

	repo := NewGormProfileRepository(db)
	profile, err := repo.GetByUserID(context.Background(), user.ID)
	if err != nil {
		t.Fatalf("GetByUserID legacy backfill: %v", err)
	}
	if profile.FirstName != "Legacy" ||
		profile.LastName != "User" ||
		profile.Phone != "+8613800138000" ||
		profile.Timezone != "Asia/Tokyo" ||
		profile.Language != "zh-CN" {
		t.Fatalf("legacy profile projection lost fields: %+v", profile)
	}

	var count int64
	if err := db.Model(&models.UserProfile{}).
		Where("user_id = ?", user.ID).
		Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("profile backfill rows = %d, want 1", count)
	}
	if _, err := repo.GetByUserID(context.Background(), user.ID); err != nil {
		t.Fatalf("idempotent profile read: %v", err)
	}
	if err := db.Model(&models.UserProfile{}).
		Where("user_id = ?", user.ID).
		Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("idempotent profile rows = %d, want 1", count)
	}
}

func TestAuthProfileUpdateValidatesAndClearsPhoneVerification(t *testing.T) {
	db := setupProfileRepoTestDB(t)
	user := createProfileRepoTestUser(t, db)
	if err := db.Model(&user).Updates(map[string]any{
		"phone":             "+8613800138000",
		"phone_verified":    true,
		"phone_verified_at": time.Now(),
	}).Error; err != nil {
		t.Fatal(err)
	}
	repo := NewGormProfileRepository(db)
	service := &AuthService{profileRepo: repo}
	phone := "+8613900139000"
	timezone := "Asia/Tokyo"
	language := "zh-CN"
	if err := service.UpdateProfile(context.Background(), user.ID, &UpdateProfileRequest{
		PhoneNumber: &phone,
		Timezone:    &timezone,
		Language:    &language,
	}); err != nil {
		t.Fatalf("UpdateProfile: %v", err)
	}
	var persisted models.User
	if err := db.First(&persisted, user.ID).Error; err != nil {
		t.Fatal(err)
	}
	if persisted.Phone != phone || persisted.PhoneVerified ||
		persisted.PhoneVerifiedAt != nil {
		t.Fatalf("phone verification projection = %+v", persisted)
	}

	invalidZone := "Mars/Olympus"
	if err := service.UpdateProfile(context.Background(), user.ID, &UpdateProfileRequest{
		Timezone: &invalidZone,
	}); !errors.Is(err, ErrInvalidProfileZone) {
		t.Fatalf("invalid timezone error = %v", err)
	}
	english := "en"
	firstName := "English"
	if err := service.UpdateProfile(context.Background(), user.ID, &UpdateProfileRequest{
		FirstName: &firstName,
		Language:  &english,
	}); err != nil {
		t.Fatalf("existing en roundtrip: %v", err)
	}
	roundTripped, err := repo.GetByUserID(context.Background(), user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if roundTripped.Language != english || roundTripped.FirstName != firstName {
		t.Fatalf("existing en roundtrip profile = %+v", roundTripped)
	}

	unsupported := "fr"
	if err := service.UpdateProfile(context.Background(), user.ID, &UpdateProfileRequest{
		Language: &unsupported,
	}); !errors.Is(err, ErrInvalidProfileLocale) {
		t.Fatalf("unsupported language error = %v", err)
	}
}

func TestAuthProfileAvatarCompatibilityCannotForgeUploadResult(t *testing.T) {
	db := setupProfileRepoTestDB(t)
	user := createProfileRepoTestUser(t, db)
	legacyAvatar := "https://legacy.example.test/avatar.png"
	if err := db.Model(&user).Update("avatar", legacyAvatar).Error; err != nil {
		t.Fatal(err)
	}
	repo := NewGormProfileRepository(db)
	service := &AuthService{profileRepo: repo}
	firstName := "Preserved"
	if err := service.UpdateProfile(context.Background(), user.ID, &UpdateProfileRequest{
		FirstName: &firstName,
		Avatar:    &legacyAvatar,
	}); err != nil {
		t.Fatalf("legacy avatar exact no-op: %v", err)
	}

	forged := fmt.Sprintf(
		"/uploads/avatars/%d/00000000-0000-4000-8000-000000000001.png",
		user.ID,
	)
	if err := service.UpdateProfile(context.Background(), user.ID, &UpdateProfileRequest{
		Avatar: &forged,
	}); !errors.Is(err, ErrInvalidProfileAvatar) {
		t.Fatalf("forged uploaded path error = %v", err)
	}

	clear := ""
	if err := service.UpdateProfile(context.Background(), user.ID, &UpdateProfileRequest{
		Avatar: &clear,
	}); err != nil {
		t.Fatalf("clear avatar: %v", err)
	}
	profile, err := repo.GetByUserID(context.Background(), user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if profile.Avatar != "" {
		t.Fatalf("cleared avatar = %q", profile.Avatar)
	}
}
