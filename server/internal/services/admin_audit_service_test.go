package services

import (
	"context"
	"errors"
	"strings"
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
		PlatformRole: models.PlatformRolePlatformAdmin,
		Status:       models.UserStatusActive,
	}
	if err := db.Create(&admin).Error; err != nil {
		t.Fatal(err)
	}
	service := NewAdminAuditService(db)
	record := &AdminAuditRecord{
		UserID:       &admin.ID,
		PlatformRole: admin.PlatformRole,
		Action:       "POST /api/admin/users",
		Method:       "POST",
		Path:         "/api/admin/users",
		StatusCode:   0,
		Result:       "pending",
		Notes:        "管理员写操作已进入执行阶段",
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

func TestAdminAuditExploreUsesStableCursorAndListProjection(t *testing.T) {
	db, err := gorm.Open(
		sqlite.Open("file:admin_audit_explore?mode=memory&cache=shared"),
		&gorm.Config{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.AdminAuditLog{}); err != nil {
		t.Fatal(err)
	}
	createdAt := time.Date(2026, 7, 31, 10, 0, 0, 0, time.UTC)
	for index := 1; index <= 4; index++ {
		log := &models.AdminAuditLog{
			CreatedAt:    createdAt,
			Username:     "auditor",
			PlatformRole: models.PlatformRoleSecurityAuditor,
			Action:       "POST /api/platform/users",
			Method:       "POST",
			Path:         "/api/platform/users",
			StatusCode:   200,
			ClientIP:     "192.168.10.25",
			Query:        "token=must-not-leak",
			UserAgent:    "browser token=must-not-leak",
			Notes:        "password=must-not-leak",
			Result:       "success",
		}
		if err := db.Create(log).Error; err != nil {
			t.Fatal(err)
		}
	}
	service := NewAdminAuditService(db)
	filter := &AdminAuditFilter{Page: 1, Limit: 2}
	first, err := service.Explore(context.Background(), filter)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Items) != 2 ||
		first.Items[0].ID <= first.Items[1].ID ||
		first.NextCursor == "" {
		t.Fatalf("first page = %+v", first)
	}
	if first.Items[0].MaskedIP != "192.168.*.*" {
		t.Fatalf("masked ip = %q", first.Items[0].MaskedIP)
	}

	second, err := service.Explore(
		context.Background(),
		&AdminAuditFilter{Limit: 2, Cursor: first.NextCursor},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Items) != 2 ||
		second.Items[0].ID >= first.Items[1].ID ||
		second.Items[0].ID <= second.Items[1].ID {
		t.Fatalf("second page = %+v", second)
	}

	tampered := first.NextCursor[:len(first.NextCursor)-1] + "A"
	if _, err := service.Explore(
		context.Background(),
		&AdminAuditFilter{Limit: 2, Cursor: tampered},
	); !errors.Is(err, ErrInvalidAdminAuditCursor) {
		t.Fatalf("tampered cursor error = %v", err)
	}
	if _, err := service.Explore(
		context.Background(),
		&AdminAuditFilter{
			Limit:  2,
			Cursor: first.NextCursor,
			Method: "POST",
		},
	); !errors.Is(err, ErrInvalidAdminAuditCursor) {
		t.Fatalf("cross-filter cursor error = %v", err)
	}
}

func TestAdminAuditDetailRedactsLongFieldsAtReadTime(t *testing.T) {
	db, err := gorm.Open(
		sqlite.Open("file:admin_audit_detail?mode=memory&cache=shared"),
		&gorm.Config{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.AdminAuditLog{}); err != nil {
		t.Fatal(err)
	}
	log := &models.AdminAuditLog{
		Username:      "admin",
		PlatformRole:  models.PlatformRolePlatformAdmin,
		Action:        "",
		Method:        "DELETE",
		Path:          "/api/platform/users/42",
		StatusCode:    204,
		ClientIP:      "2001:db8:abcd:1234::1",
		Query:         "keyword=safe&token=top-secret",
		UserAgent:     "browser authorization=Bearer-secret",
		Notes:         "password=hunter2",
		RequestID:     "request-1",
		TraceID:       "trace-1",
		CorrelationID: "correlation-1",
		Result:        "success",
	}
	if err := db.Create(log).Error; err != nil {
		t.Fatal(err)
	}
	detail, err := NewAdminAuditService(db).GetDetail(
		context.Background(),
		log.ID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Action != "DELETE /api/platform/users/42" {
		t.Fatalf("historical action fallback = %q", detail.Action)
	}
	joined := detail.Query + detail.UserAgent + detail.Notes
	for _, secret := range []string{"top-secret", "Bearer-secret", "hunter2"} {
		if strings.Contains(joined, secret) {
			t.Fatalf("detail leaked %q: %+v", secret, detail)
		}
	}
	if detail.MaskedIP != "2001:db8:abcd::/48" {
		t.Fatalf("masked ipv6 = %q", detail.MaskedIP)
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
