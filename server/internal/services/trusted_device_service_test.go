package services

import (
	"context"
	"fmt"
	"testing"
	"time"

	"gongdan-system/internal/models"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupTrustedDeviceTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared&_fk=1", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open sqlite memory db: %v", err)
	}

	if err := db.AutoMigrate(&models.User{}, &models.OTPTrustedDevice{}); err != nil {
		t.Fatalf("failed to migrate schemas: %v", err)
	}

	return db
}

func seedTrustedDeviceUser(t *testing.T, db *gorm.DB, email string) uint {
	t.Helper()
	user := models.User{
		Username:     email,
		Email:        email,
		PasswordHash: "hash",
		Role:         models.RoleCustomer,
		Status:       models.UserStatusActive,
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("failed to seed user: %v", err)
	}
	return user.ID
}

func TestTrustedDeviceService_ListTrustedDevices(t *testing.T) {
	db := setupTrustedDeviceTestDB(t)
	userID1 := seedTrustedDeviceUser(t, db, "user1@example.com")
	userID2 := seedTrustedDeviceUser(t, db, "user2@example.com")

	now := time.Now()
	fixtures := []models.OTPTrustedDevice{
		{UserID: userID1, DeviceTokenHash: "hash1", DeviceName: "Laptop", LastUsedAt: now.Add(-time.Hour), ExpiresAt: now.Add(10 * time.Hour)},
		{UserID: userID1, DeviceTokenHash: "hash2", DeviceName: "Phone", LastUsedAt: now, ExpiresAt: now.Add(20 * time.Hour)},
		{UserID: userID2, DeviceTokenHash: "hash3", DeviceName: "Other", LastUsedAt: now, ExpiresAt: now.Add(20 * time.Hour)},
	}
	for _, device := range fixtures {
		if err := db.Create(&device).Error; err != nil {
			t.Fatalf("failed to seed device: %v", err)
		}
	}

	service := NewTrustedDeviceService(db)

	devices, err := service.ListTrustedDevices(context.Background(), userID1)
	if err != nil {
		t.Fatalf("ListTrustedDevices returned error: %v", err)
	}

	if len(devices) != 2 {
		t.Fatalf("expected 2 devices, got %d", len(devices))
	}

	if devices[0].DeviceName != "Phone" {
		t.Fatalf("expected most recent device first, got %s", devices[0].DeviceName)
	}
}

func TestTrustedDeviceService_RevokeTrustedDevice(t *testing.T) {
	db := setupTrustedDeviceTestDB(t)
	userID := seedTrustedDeviceUser(t, db, "user1@example.com")

	device := models.OTPTrustedDevice{UserID: userID, DeviceTokenHash: "hash", DeviceName: "Laptop", LastUsedAt: time.Now(), ExpiresAt: time.Now().Add(24 * time.Hour)}
	if err := db.Create(&device).Error; err != nil {
		t.Fatalf("failed to create device: %v", err)
	}

	service := NewTrustedDeviceService(db)

	if err := service.RevokeTrustedDevice(context.Background(), userID, device.ID); err != nil {
		t.Fatalf("RevokeTrustedDevice returned error: %v", err)
	}

	var updated models.OTPTrustedDevice
	if err := db.First(&updated, device.ID).Error; err != nil {
		t.Fatalf("failed to load device: %v", err)
	}

	if !updated.Revoked {
		t.Fatalf("expected device to be revoked")
	}

	if !updated.ExpiresAt.Before(device.ExpiresAt) {
		t.Fatalf("expected expires_at to be updated")
	}
}

func TestTrustedDeviceService_RevokeAllTrustedDevices(t *testing.T) {
	db := setupTrustedDeviceTestDB(t)
	userID := seedTrustedDeviceUser(t, db, "user1@example.com")

	now := time.Now()
	devices := []models.OTPTrustedDevice{
		{UserID: userID, DeviceTokenHash: "hash1", DeviceName: "Laptop", LastUsedAt: now, ExpiresAt: now.Add(24 * time.Hour)},
		{UserID: userID, DeviceTokenHash: "hash2", DeviceName: "Phone", LastUsedAt: now, ExpiresAt: now.Add(48 * time.Hour)},
	}
	if err := db.Create(&devices).Error; err != nil {
		t.Fatalf("failed to seed devices: %v", err)
	}

	service := NewTrustedDeviceService(db)

	if err := service.RevokeAllTrustedDevices(context.Background(), userID); err != nil {
		t.Fatalf("RevokeAllTrustedDevices returned error: %v", err)
	}

	var count int64
	if err := db.Model(&models.OTPTrustedDevice{}).Where("user_id = ? AND revoked = ?", userID, true).Count(&count).Error; err != nil {
		t.Fatalf("failed to count revoked devices: %v", err)
	}

	if count != 2 {
		t.Fatalf("expected 2 revoked devices, got %d", count)
	}
}
