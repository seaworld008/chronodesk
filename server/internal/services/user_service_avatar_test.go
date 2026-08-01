package services

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"mime/multipart"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func avatarUploadFile(t *testing.T, payload []byte, name string) (*os.File, *multipart.FileHeader) {
	t.Helper()
	file, err := os.CreateTemp(t.TempDir(), "avatar-*")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write(payload); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if _, err := file.Seek(0, 0); err != nil {
		file.Close()
		t.Fatal(err)
	}
	return file, &multipart.FileHeader{Filename: name, Size: int64(len(payload))}
}

func validAvatarPNG(t *testing.T) []byte {
	t.Helper()
	source := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for x := 0; x < 4; x++ {
		for y := 0; y < 4; y++ {
			source.Set(x, y, color.RGBA{R: 30, G: 120, B: 220, A: 255})
		}
	}
	var payload bytes.Buffer
	if err := png.Encode(&payload, source); err != nil {
		t.Fatal(err)
	}
	return payload.Bytes()
}

func TestUploadAvatarPersistsSanitizedObjectAndReplacesPreviousObject(t *testing.T) {
	db := openTestDB(t)
	if err := db.AutoMigrate(&models.User{}, &models.UserProfile{}); err != nil {
		t.Fatal(err)
	}
	user := models.User{
		Username: "avatar-user", Email: "avatar@example.com", PasswordHash: "hash",
		PlatformRole: models.PlatformRoleMember, Status: models.UserStatusActive,
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	storage, err := NewLocalAttachmentStorage(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	service := NewUserService(db)
	service.SetAvatarStorage(storage, defaultAvatarMaxBytes)

	firstFile, firstHeader := avatarUploadFile(t, validAvatarPNG(t), "first.jpg")
	defer firstFile.Close()
	firstURL, err := service.UploadAvatar(context.Background(), user.ID, firstFile, firstHeader)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(firstURL, "/uploads/avatars/") || !strings.HasSuffix(firstURL, ".png") {
		t.Fatalf("avatar URL must use detected format and opaque key: %q", firstURL)
	}
	var storedUser models.User
	var storedProfile models.UserProfile
	if err := db.First(&storedUser, user.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Where("user_id = ?", user.ID).First(&storedProfile).Error; err != nil {
		t.Fatal(err)
	}
	if storedUser.Avatar != firstURL || storedProfile.Avatar != firstURL {
		t.Fatalf(
			"avatar projections diverged: user=%q profile=%q want=%q",
			storedUser.Avatar,
			storedProfile.Avatar,
			firstURL,
		)
	}

	filename := firstURL[strings.LastIndex(firstURL, "/")+1:]
	reader, contentType, err := service.OpenAvatar(context.Background(), user.ID, filename)
	if err != nil {
		t.Fatal(err)
	}
	storedPayload, err := io.ReadAll(reader)
	reader.Close()
	if err != nil {
		t.Fatal(err)
	}
	if contentType != "image/png" || len(storedPayload) == 0 {
		t.Fatalf("unexpected stored avatar: type=%q size=%d", contentType, len(storedPayload))
	}

	secondFile, secondHeader := avatarUploadFile(t, validAvatarPNG(t), "second.png")
	defer secondFile.Close()
	secondURL, err := service.UploadAvatar(context.Background(), user.ID, secondFile, secondHeader)
	if err != nil {
		t.Fatal(err)
	}
	if secondURL == firstURL {
		t.Fatal("replacement must use a new immutable URL")
	}
	firstKey, ok := avatarStorageKey(firstURL)
	if !ok {
		t.Fatalf("cannot derive first storage key: %q", firstURL)
	}
	if _, err := storage.Open(context.Background(), firstKey); err == nil {
		t.Fatal("superseded avatar object was not deleted")
	}
}

func TestUploadAvatarRejectsSpoofedAndOversizedPayloads(t *testing.T) {
	db := openTestDB(t)
	if err := db.AutoMigrate(&models.User{}, &models.UserProfile{}); err != nil {
		t.Fatal(err)
	}
	user := models.User{
		Username: "avatar-invalid", Email: "avatar-invalid@example.com", PasswordHash: "hash",
		PlatformRole: models.PlatformRoleMember, Status: models.UserStatusActive,
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	storage, err := NewLocalAttachmentStorage(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	service := NewUserService(db)
	service.SetAvatarStorage(storage, 64)

	spoofed, spoofedHeader := avatarUploadFile(t, []byte("<script>alert(1)</script>"), "avatar.png")
	defer spoofed.Close()
	if _, err := service.UploadAvatar(context.Background(), user.ID, spoofed, spoofedHeader); !errors.Is(err, ErrInvalidAvatar) {
		t.Fatalf("spoofed image error = %v, want ErrInvalidAvatar", err)
	}

	oversized, oversizedHeader := avatarUploadFile(t, bytes.Repeat([]byte{1}, 65), "avatar.png")
	defer oversized.Close()
	if _, err := service.UploadAvatar(context.Background(), user.ID, oversized, oversizedHeader); !errors.Is(err, ErrAttachmentTooLarge) {
		t.Fatalf("oversized image error = %v, want ErrAttachmentTooLarge", err)
	}
}

func TestUploadAvatarLocksRowsAndUpdatesOnlyAvatarColumns(t *testing.T) {
	db := openTestDB(t)
	if err := db.AutoMigrate(&models.User{}, &models.UserProfile{}); err != nil {
		t.Fatal(err)
	}
	user := models.User{
		Username:     "avatar-field-mask",
		Email:        "avatar-field-mask@example.test",
		PasswordHash: "hash",
		Avatar:       "/uploads/avatars/1/00000000-0000-4000-8000-000000000001.png",
		Phone:        "+8613800138000",
		Timezone:     "Asia/Tokyo",
		Language:     "en",
		PlatformRole: models.PlatformRoleMember,
		Status:       models.UserStatusActive,
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	profile := models.UserProfile{
		UserID:   user.ID,
		Avatar:   user.Avatar,
		Phone:    user.Phone,
		Timezone: user.Timezone,
		Language: user.Language,
	}
	if err := db.Create(&profile).Error; err != nil {
		t.Fatal(err)
	}

	var lockTables []string
	if err := db.Callback().Query().After("gorm:query").Register(
		"test:avatar-upload-locks",
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
		"test:avatar-upload-columns",
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

	storage, err := NewLocalAttachmentStorage(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	service := NewUserService(db)
	service.SetAvatarStorage(storage, defaultAvatarMaxBytes)
	file, header := avatarUploadFile(t, validAvatarPNG(t), "replacement.png")
	defer file.Close()
	avatarURL, err := service.UploadAvatar(
		context.Background(),
		user.ID,
		file,
		header,
	)
	if err != nil {
		t.Fatal(err)
	}

	if strings.Join(lockTables, ",") != "users,user_profiles" {
		t.Fatalf("avatar upload lock order = %v", lockTables)
	}
	if len(updates) != 2 {
		t.Fatalf("avatar update statements = %+v, want two projections", updates)
	}
	for _, update := range updates {
		if !strings.Contains(update.sql, "avatar") {
			t.Errorf("%s update omits avatar: %s", update.table, update.sql)
		}
		for _, forbidden := range []string{
			"phone",
			"timezone",
			"language",
			"first_name",
			"last_name",
			"department",
			"job_title",
		} {
			if strings.Contains(update.sql, forbidden) {
				t.Errorf(
					"%s avatar update writes omitted %s: %s",
					update.table,
					forbidden,
					update.sql,
				)
			}
		}
	}

	var gotUser models.User
	var gotProfile models.UserProfile
	if err := db.First(&gotUser, user.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Where("user_id = ?", user.ID).First(&gotProfile).Error; err != nil {
		t.Fatal(err)
	}
	if gotUser.Avatar != avatarURL || gotProfile.Avatar != avatarURL {
		t.Fatalf(
			"avatar projections = (%q, %q), want %q",
			gotUser.Avatar,
			gotProfile.Avatar,
			avatarURL,
		)
	}
	if gotUser.Phone != user.Phone ||
		gotUser.Timezone != user.Timezone ||
		gotUser.Language != user.Language ||
		gotProfile.Phone != profile.Phone ||
		gotProfile.Timezone != profile.Timezone ||
		gotProfile.Language != profile.Language {
		t.Fatalf(
			"avatar update overwrote profile fields: user=%+v profile=%+v",
			gotUser,
			gotProfile,
		)
	}
	storedKey := strings.TrimPrefix(avatarURL, "/uploads/")
	reader, err := storage.Open(context.Background(), storedKey)
	if err != nil {
		t.Fatalf("open committed avatar object: %v", err)
	}
	_ = reader.Close()
}

func TestUploadAvatarDeletesNewObjectWhenDatabaseTransactionFails(t *testing.T) {
	db := openTestDB(t)
	if err := db.AutoMigrate(&models.User{}, &models.UserProfile{}); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	storage, err := NewLocalAttachmentStorage(root)
	if err != nil {
		t.Fatal(err)
	}
	service := NewUserService(db)
	service.SetAvatarStorage(storage, defaultAvatarMaxBytes)
	file, header := avatarUploadFile(t, validAvatarPNG(t), "orphan.png")
	defer file.Close()
	if _, err := service.UploadAvatar(
		context.Background(),
		999,
		file,
		header,
	); err == nil {
		t.Fatal("missing avatar owner unexpectedly succeeded")
	}

	var storedFiles []string
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type().IsRegular() {
			storedFiles = append(storedFiles, path)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(storedFiles) != 0 {
		t.Fatalf("failed transaction left new avatar objects: %v", storedFiles)
	}
}

func TestUploadAvatarBackfillsLegacyProfileWithoutDroppingContactPreferences(t *testing.T) {
	db := openTestDB(t)
	if err := db.AutoMigrate(&models.User{}, &models.UserProfile{}); err != nil {
		t.Fatal(err)
	}
	user := models.User{
		Username:     "legacy-avatar",
		Email:        "legacy-avatar@example.test",
		Phone:        "+8613800138000",
		PasswordHash: "hashed",
		Timezone:     "Asia/Tokyo",
		Language:     "zh-CN",
		PlatformRole: models.PlatformRoleMember,
		Status:       models.UserStatusActive,
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	storage, err := NewLocalAttachmentStorage(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	service := NewUserService(db)
	service.SetAvatarStorage(storage, defaultAvatarMaxBytes)
	file, header := avatarUploadFile(t, validAvatarPNG(t), "legacy.png")
	defer file.Close()
	avatarURL, err := service.UploadAvatar(
		context.Background(),
		user.ID,
		file,
		header,
	)
	if err != nil {
		t.Fatalf("UploadAvatar: %v", err)
	}
	var profile models.UserProfile
	if err := db.Where("user_id = ?", user.ID).First(&profile).Error; err != nil {
		t.Fatal(err)
	}
	if profile.Avatar != avatarURL ||
		profile.Phone != user.Phone ||
		profile.Timezone != user.Timezone ||
		profile.Language != user.Language {
		t.Fatalf("legacy upload profile lost fields: %+v", profile)
	}
}
