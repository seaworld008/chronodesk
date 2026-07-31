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
	"strings"
	"testing"

	"github.com/seaworld008/chronodesk/server/internal/models"
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
