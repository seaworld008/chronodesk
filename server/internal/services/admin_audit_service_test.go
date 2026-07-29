package services

import (
	"context"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestAdminAuditLifecyclePersistsAnchorBeforeFinalization(t *testing.T) {
	db, err := gorm.Open(
		sqlite.Open("file:admin_audit_lifecycle?mode=memory&cache=shared"),
		&gorm.Config{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.User{}, &models.AdminAuditLog{}); err != nil {
		t.Fatal(err)
	}
	admin := models.User{
		Username:     "audit-admin",
		Email:        "audit-admin@example.com",
		PasswordHash: "hash",
		Role:         models.RoleAdmin,
		Status:       models.UserStatusActive,
	}
	if err := db.Create(&admin).Error; err != nil {
		t.Fatal(err)
	}
	service := NewAdminAuditService(db)
	record := &AdminAuditRecord{
		UserID:     &admin.ID,
		Role:       string(admin.Role),
		Action:     "POST /api/admin/users",
		Method:     "POST",
		Path:       "/api/admin/users",
		StatusCode: 0,
		Result:     "pending",
		Notes:      "管理员写操作已进入执行阶段",
	}
	if err := service.Record(context.Background(), record); err != nil {
		t.Fatalf("Record() error = %v", err)
	}
	if record.ID == 0 {
		t.Fatal("Record() did not return the durable audit ID")
	}

	var anchor models.AdminAuditLog
	if err := db.First(&anchor, record.ID).Error; err != nil {
		t.Fatal(err)
	}
	if anchor.Result != "pending" || anchor.StatusCode != 0 {
		t.Fatalf("unexpected audit anchor: %+v", anchor)
	}

	record.StatusCode = 201
	record.Result = "success"
	record.Latency = 125 * time.Millisecond
	record.Notes = ""
	if err := service.Finalize(context.Background(), record); err != nil {
		t.Fatalf("Finalize() error = %v", err)
	}
	if err := db.First(&anchor, record.ID).Error; err != nil {
		t.Fatal(err)
	}
	if anchor.Result != "success" ||
		anchor.StatusCode != 201 ||
		anchor.LatencyMs != 125 ||
		anchor.Notes != "" {
		t.Fatalf("unexpected finalized audit: %+v", anchor)
	}
}

func TestAdminAuditFinalizeRejectsMissingAnchor(t *testing.T) {
	db, err := gorm.Open(
		sqlite.Open("file:admin_audit_missing?mode=memory&cache=shared"),
		&gorm.Config{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.AdminAuditLog{}); err != nil {
		t.Fatal(err)
	}
	service := NewAdminAuditService(db)
	if err := service.Finalize(
		context.Background(),
		&AdminAuditRecord{ID: 999, Result: "success"},
	); err == nil {
		t.Fatal("Finalize() accepted a missing durable audit anchor")
	}
}
