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

func TestAdminAuditRejectsOverflowingHumanActorID(t *testing.T) {
	db, err := gorm.Open(
		sqlite.Open("file:admin_audit_actor_overflow?mode=memory&cache=shared"),
		&gorm.Config{},
	)
	if err != nil {
		t.Fatal(err)
	}
	service := newAdminAuditExplorerForTest(t, db)
	record := &AdminAuditRecord{
		Actor: models.ActorRef{
			Type: models.ActorTypeHuman,
			ID:   overflowingUint,
		},
		Action: "GET /api/platform/audit",
		Method: "GET",
		Path:   "/api/platform/audit",
	}
	if err := service.Record(context.Background(), record); err == nil {
		t.Fatal("Record() accepted an overflowing human actor ID")
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
	auditorRole := models.PlatformRoleSecurityAuditor
	for index := 1; index <= 4; index++ {
		log := &models.AdminAuditLog{
			CreatedAt:    createdAt,
			Username:     "auditor",
			PlatformRole: &auditorRole,
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
	filter := &AdminAuditFilter{Limit: 2}
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

func TestAdminAuditExploreRejectsInvalidServiceLimits(t *testing.T) {
	db, err := gorm.Open(
		sqlite.Open("file:admin_audit_invalid_limits?mode=memory&cache=shared"),
		&gorm.Config{},
	)
	if err != nil {
		t.Fatal(err)
	}
	service := newAdminAuditExplorerForTest(t, db)
	for _, limit := range []int{-1, MaxAdminAuditLimit + 1} {
		if _, err := service.Explore(
			context.Background(),
			&AdminAuditFilter{Limit: limit},
		); !errors.Is(err, ErrInvalidAdminAuditLimit) {
			t.Fatalf("limit %d error = %v", limit, err)
		}
	}
}

func TestAdminAuditExploreCursorPagesUseStableNonOverlappingOrdering(
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
	auditorRole := models.PlatformRoleSecurityAuditor
	for index := 0; index < 6; index++ {
		if err := db.Create(&models.AdminAuditLog{
			CreatedAt:    createdAt.Add(-time.Duration(index/2) * time.Minute),
			Username:     "numbered-page-auditor",
			PlatformRole: &auditorRole,
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
		&AdminAuditFilter{Limit: 3},
	)
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.Explore(
		context.Background(),
		&AdminAuditFilter{Limit: 3, Cursor: first.NextCursor},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !first.HasMore || first.NextCursor == "" ||
		second.HasMore || second.NextCursor != "" ||
		len(first.Items) != 3 || len(second.Items) != 3 {
		t.Fatalf("cursor pages = first:%+v second:%+v", first, second)
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
	platformAdminRole := models.PlatformRolePlatformAdmin
	log := &models.AdminAuditLog{
		Username:     "admin",
		PlatformRole: &platformAdminRole,
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

func TestAdminAuditExploreAndDetailRedactHistoricalTextProjections(
	t *testing.T,
) {
	db, err := gorm.Open(
		sqlite.Open(
			"file:admin_audit_historical_projection_redaction?mode=memory&cache=shared",
		),
		&gorm.Config{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.AdminAuditLog{}); err != nil {
		t.Fatal(err)
	}

	longText := strings.Repeat("界", 5000)
	queryValues := url.Values{
		"safe":  []string{"visible"},
		"token": []string{"query-secret"},
		"nested": []string{
			`{"keep":"visible","password":"nested-query-secret"}`,
		},
	}
	for index := 0; index < 5; index++ {
		queryValues.Set(
			"long-"+string(rune('a'+index)),
			strings.Repeat("q", 700),
		)
	}
	role := models.PlatformRoleSecurityAuditor
	log := &models.AdminAuditLog{
		CreatedAt:        time.Now().UTC(),
		ActorType:        models.ActorTypeServicePrincipal,
		ActorID:          "token=actor-id-secret " + longText,
		Username:         "password=username-secret " + longText,
		PlatformRole:     &role,
		Action:           "password=raw-action-secret " + longText,
		ActionCode:       "token=action-code-secret " + longText,
		ResourceType:     "password=resource-type-secret " + longText,
		ResourcePublicID: "token=resource-public-id-secret " + longText,
		Method:           "token=method-secret " + longText,
		Path:             "token=path-secret /api/platform/audit/" + longText,
		StatusCode:       500,
		ClientIP:         "192.168.88.99",
		Query:            queryValues.Encode(),
		UserAgent: "browser Authorization: Bearer user-agent-secret " +
			longText,
		Notes: `{"keep":"visible","password":"notes-secret","long":"` +
			longText + `"}`,
		RequestID:     "token=request-id-secret " + longText,
		TraceID:       "password=trace-id-secret " + longText,
		CorrelationID: "token=correlation-id-secret " + longText,
		Result:        "password=result-secret " + longText,
	}
	if err := db.Create(log).Error; err != nil {
		t.Fatal(err)
	}
	fallback := &models.AdminAuditLog{
		CreatedAt:  log.CreatedAt.Add(-time.Second),
		ActorType:  models.ActorTypeSystem,
		ActorID:    "system",
		Username:   "system",
		Action:     "password=fallback-action-secret " + longText,
		Method:     "POST",
		Path:       "/api/platform/fallback",
		StatusCode: 200,
		ClientIP:   "127.0.0.1",
		Result:     "success",
	}
	if err := db.Create(fallback).Error; err != nil {
		t.Fatal(err)
	}

	service := newAdminAuditExplorerForTest(t, db)
	page, err := service.Explore(
		context.Background(),
		&AdminAuditFilter{Limit: 2},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 2 {
		t.Fatalf("Explore() items = %d, want 2", len(page.Items))
	}
	item := page.Items[0]
	if item.ID != log.ID {
		t.Fatalf("Explore() first id = %d, want %d", item.ID, log.ID)
	}
	if strings.Contains(page.Items[1].Action, "fallback-action-secret") {
		t.Fatalf(
			"Explore() fallback action leaked: %q",
			page.Items[1].Action,
		)
	}
	if len([]rune(page.Items[1].Action)) >
		adminAuditActionMaxRunes {
		t.Fatalf(
			"Explore() fallback action length = %d",
			len([]rune(page.Items[1].Action)),
		)
	}

	detail, err := service.GetDetail(context.Background(), log.ID)
	if err != nil {
		t.Fatal(err)
	}
	projected, err := json.Marshal(struct {
		Page   *AdminAuditPage   `json:"page"`
		Detail *AdminAuditDetail `json:"detail"`
	}{
		Page:   page,
		Detail: detail,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{
		"actor-id-secret",
		"username-secret",
		"action-code-secret",
		"resource-type-secret",
		"resource-public-id-secret",
		"method-secret",
		"path-secret",
		"result-secret",
		"query-secret",
		"nested-query-secret",
		"user-agent-secret",
		"notes-secret",
		"request-id-secret",
		"trace-id-secret",
		"correlation-id-secret",
		"fallback-action-secret",
	} {
		if strings.Contains(string(projected), secret) {
			t.Fatalf("read projection leaked %q: %s", secret, projected)
		}
	}

	assertAuditProjectionLimit := func(
		field string,
		value string,
		limit int,
	) {
		t.Helper()
		if got := len([]rune(value)); got > limit {
			t.Errorf("%s length = %d, want <= %d", field, got, limit)
		}
	}
	for field, projection := range map[string]struct {
		value string
		limit int
	}{
		"actor_type": {
			value: string(item.ActorType),
			limit: adminAuditActorTypeMaxRunes,
		},
		"actor_id": {
			value: item.ActorID,
			limit: adminAuditActorIDMaxRunes,
		},
		"username": {
			value: item.Username,
			limit: adminAuditUsernameMaxRunes,
		},
		"platform_role": {
			value: string(item.PlatformRole),
			limit: adminAuditPlatformRoleMaxRunes,
		},
		"action": {
			value: item.Action,
			limit: adminAuditActionMaxRunes,
		},
		"action_code": {
			value: item.ActionCode,
			limit: adminAuditActionCodeMaxRunes,
		},
		"resource_type": {
			value: item.ResourceType,
			limit: adminAuditResourceTypeMaxRunes,
		},
		"resource_public_id": {
			value: item.ResourcePublicID,
			limit: adminAuditResourcePublicIDMaxRunes,
		},
		"method": {
			value: item.Method,
			limit: adminAuditMethodMaxRunes,
		},
		"path": {
			value: item.Path,
			limit: adminAuditPathMaxRunes,
		},
		"result": {
			value: item.Result,
			limit: adminAuditResultMaxRunes,
		},
		"query": {
			value: detail.Query,
			limit: adminAuditQueryMaxRunes,
		},
		"user_agent": {
			value: detail.UserAgent,
			limit: adminAuditUserAgentMaxRunes,
		},
		"notes": {
			value: detail.Notes,
			limit: adminAuditNotesMaxRunes,
		},
		"request_id": {
			value: detail.RequestID,
			limit: adminAuditRequestIDMaxRunes,
		},
		"trace_id": {
			value: detail.TraceID,
			limit: adminAuditTraceIDMaxRunes,
		},
		"correlation_id": {
			value: detail.CorrelationID,
			limit: adminAuditCorrelationIDMaxRunes,
		},
	} {
		assertAuditProjectionLimit(field, projection.value, projection.limit)
	}

	csvRow := adminAuditExportCSVRow(log)
	for _, secret := range []string{
		"username-secret",
		"action-code-secret",
		"resource-type-secret",
		"resource-public-id-secret",
		"method-secret",
		"path-secret",
		"result-secret",
		"request-id-secret",
		"trace-id-secret",
		"correlation-id-secret",
	} {
		if strings.Contains(strings.Join(csvRow, ","), secret) {
			t.Fatalf("CSV projection leaked %q: %#v", secret, csvRow)
		}
	}
	if csvRow[1] != detail.Username ||
		csvRow[3] != detail.Action ||
		csvRow[4] != detail.Method ||
		csvRow[5] != detail.Path ||
		csvRow[6] != detail.Result ||
		csvRow[10] != detail.ResourceType ||
		csvRow[11] != detail.ResourcePublicID ||
		csvRow[12] != detail.RequestID ||
		csvRow[13] != detail.TraceID ||
		csvRow[14] != detail.CorrelationID {
		t.Fatalf(
			"CSV does not reuse the list/detail redaction boundary: %#v",
			csvRow,
		)
	}

	var persisted models.AdminAuditLog
	if err := db.First(&persisted, log.ID).Error; err != nil {
		t.Fatal(err)
	}
	rawEvidence, err := json.Marshal(persisted)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{
		"actor-id-secret",
		"username-secret",
		"raw-action-secret",
		"action-code-secret",
		"resource-type-secret",
		"resource-public-id-secret",
		"method-secret",
		"path-secret",
		"result-secret",
		"query-secret",
		"nested-query-secret",
		"user-agent-secret",
		"notes-secret",
		"request-id-secret",
		"trace-id-secret",
		"correlation-id-secret",
	} {
		if !strings.Contains(string(rawEvidence), secret) {
			t.Fatalf("raw audit evidence lost %q: %s", secret, rawEvidence)
		}
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
