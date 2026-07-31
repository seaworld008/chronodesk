package services

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

var adminAuditTestCursorKey = []byte(
	"admin-audit-test-cursor-root-key-32-bytes-minimum",
)

func newAdminAuditExplorerForTest(
	t *testing.T,
	db *gorm.DB,
) *AdminAuditService {
	t.Helper()
	service, err := NewAdminAuditServiceWithCursorKey(
		db,
		adminAuditTestCursorKey,
	)
	if err != nil {
		t.Fatalf("create audit explorer: %v", err)
	}
	return service
}

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
	service := newAdminAuditExplorerForTest(t, db)
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
	service := newAdminAuditExplorerForTest(t, db)
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

	restartedService := newAdminAuditExplorerForTest(t, db)
	second, err := restartedService.Explore(
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

	parts := strings.Split(first.NextCursor, ".")
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		t.Fatal(err)
	}
	var forged map[string]any
	if err := json.Unmarshal(payload, &forged); err != nil {
		t.Fatal(err)
	}
	forged["id"] = float64(1)
	forgedPayload, err := json.Marshal(forged)
	if err != nil {
		t.Fatal(err)
	}
	publicChecksum := sha256.Sum256(forgedPayload)
	tampered := base64.RawURLEncoding.EncodeToString(forgedPayload) + "." +
		base64.RawURLEncoding.EncodeToString(publicChecksum[:])
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

	forged["unexpected"] = "signed but unpublished"
	unknownPayload, err := json.Marshal(forged)
	if err != nil {
		t.Fatal(err)
	}
	mac := hmac.New(sha256.New, service.cursorSigningKey)
	_, _ = mac.Write(unknownPayload)
	unknownCursor := base64.RawURLEncoding.EncodeToString(unknownPayload) +
		"." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if _, err := service.Explore(
		context.Background(),
		&AdminAuditFilter{Limit: 2, Cursor: unknownCursor},
	); !errors.Is(err, ErrInvalidAdminAuditCursor) {
		t.Fatalf("unknown cursor field error = %v", err)
	}
}

func TestAdminAuditExploreNumberedPagesUseStableNonOverlappingOffsets(
	t *testing.T,
) {
	db, err := gorm.Open(
		sqlite.Open("file:admin_audit_numbered_pages?mode=memory&cache=shared"),
		&gorm.Config{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.AdminAuditLog{}); err != nil {
		t.Fatal(err)
	}
	createdAt := time.Date(2026, 7, 31, 11, 0, 0, 0, time.UTC)
	for index := 0; index < 6; index++ {
		if err := db.Create(&models.AdminAuditLog{
			CreatedAt:    createdAt.Add(-time.Duration(index/2) * time.Minute),
			Username:     "numbered-page-auditor",
			PlatformRole: models.PlatformRoleSecurityAuditor,
			Action:       "GET /api/platform/audit-logs",
			Method:       "GET",
			Path:         "/api/platform/audit-logs",
			StatusCode:   200,
			Result:       "success",
		}).Error; err != nil {
			t.Fatal(err)
		}
	}
	service := newAdminAuditExplorerForTest(t, db)
	first, err := service.Explore(
		context.Background(),
		&AdminAuditFilter{Page: 1, Limit: 3},
	)
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.Explore(
		context.Background(),
		&AdminAuditFilter{Page: 2, Limit: 3},
	)
	if err != nil {
		t.Fatal(err)
	}
	if first.Page != 1 || second.Page != 2 ||
		len(first.Items) != 3 || len(second.Items) != 3 {
		t.Fatalf("numbered pages = first:%+v second:%+v", first, second)
	}
	seen := map[uint]struct{}{}
	var previous *AdminAuditListItem
	for _, item := range append(first.Items, second.Items...) {
		if _, duplicate := seen[item.ID]; duplicate {
			t.Fatalf("page overlap at audit id %d", item.ID)
		}
		seen[item.ID] = struct{}{}
		if previous != nil &&
			(item.CreatedAt.After(previous.CreatedAt) ||
				(item.CreatedAt.Equal(previous.CreatedAt) &&
					item.ID >= previous.ID)) {
			t.Fatalf(
				"unstable order previous=%+v current=%+v",
				previous,
				item,
			)
		}
		previous = item
	}
}

func TestAdminAuditMigrationCreatesStablePaginationIndex(t *testing.T) {
	db, err := gorm.Open(
		sqlite.Open("file:admin_audit_cursor_index?mode=memory&cache=shared"),
		&gorm.Config{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.AdminAuditLog{}); err != nil {
		t.Fatal(err)
	}
	if !db.Migrator().HasIndex(
		&models.AdminAuditLog{},
		"idx_admin_audit_logs_created_id",
	) {
		t.Fatal("stable audit pagination composite index is missing")
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
		Username:     "admin",
		PlatformRole: models.PlatformRolePlatformAdmin,
		Action:       "",
		Method:       "DELETE",
		Path:         "/api/platform/users/42",
		StatusCode:   204,
		ClientIP:     "2001:db8:abcd:1234::1",
		Query: "keyword=safe&token=top-secret&nested=" +
			url.QueryEscape(
				`{"safe":"visible","auth":{"access_token":"nested-secret"}}`,
			),
		UserAgent: "browser Authorization: Bearer bearer-secret " +
			"Cookie: session=cookie-secret",
		Notes: `{"safe":"visible","password":"json-secret",` +
			`"nested":{"api_key":"nested-api-secret"}}`,
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
	for _, secret := range []string{
		"top-secret",
		"bearer-secret",
		"cookie-secret",
		"json-secret",
		"nested-secret",
		"nested-api-secret",
	} {
		if strings.Contains(joined, secret) {
			t.Fatalf("detail leaked %q: %+v", secret, detail)
		}
	}
	for _, safe := range []string{"keyword=safe", "visible"} {
		if !strings.Contains(joined, safe) {
			t.Fatalf("detail lost safe value %q: %+v", safe, detail)
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
