package services

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type memoryAuditExportStorage struct {
	mu      sync.Mutex
	objects map[string][]byte
}

func newMemoryAuditExportStorage() *memoryAuditExportStorage {
	return &memoryAuditExportStorage{objects: map[string][]byte{}}
}

func (storage *memoryAuditExportStorage) Put(
	ctx context.Context,
	key string,
	reader io.Reader,
	maxBytes int64,
) (*StoredAttachmentObject, error) {
	payload, err := io.ReadAll(io.LimitReader(reader, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(payload)) > maxBytes {
		return nil, ErrAttachmentTooLarge
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	sum := sha256.Sum256(payload)
	storage.mu.Lock()
	storage.objects[key] = append([]byte(nil), payload...)
	storage.mu.Unlock()
	return &StoredAttachmentObject{
		Key:    key,
		Size:   int64(len(payload)),
		SHA256: hex.EncodeToString(sum[:]),
	}, nil
}

func (storage *memoryAuditExportStorage) Open(
	ctx context.Context,
	key string,
) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	storage.mu.Lock()
	payload, ok := storage.objects[key]
	storage.mu.Unlock()
	if !ok {
		return nil, errors.New("object not found")
	}
	return io.NopCloser(bytes.NewReader(payload)), nil
}

func (storage *memoryAuditExportStorage) Delete(
	ctx context.Context,
	key string,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	storage.mu.Lock()
	delete(storage.objects, key)
	storage.mu.Unlock()
	return nil
}

type adminAuditExportFixture struct {
	db      *gorm.DB
	audit   *AdminAuditService
	service *AdminAuditExportService
	storage *memoryAuditExportStorage
	user    models.User
	now     time.Time
}

func newAdminAuditExportFixture(t *testing.T) adminAuditExportFixture {
	t.Helper()
	db, err := gorm.Open(
		sqlite.Open(
			"file:admin_audit_export_"+
				strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())+
				"?mode=memory&cache=shared",
		),
		&gorm.Config{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(
		&models.User{},
		&models.AdminAuditLog{},
		&models.AdminAuditExportJob{},
	); err != nil {
		t.Fatal(err)
	}
	user := models.User{
		Username:     "audit-export-owner",
		Email:        "audit-export-owner@example.test",
		PasswordHash: "hash",
		PlatformRole: models.PlatformRoleSecurityAuditor,
		Status:       models.UserStatusActive,
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	audit, err := NewAdminAuditServiceWithCursorKey(
		db,
		[]byte("audit-export-test-signing-key-32-bytes"),
	)
	if err != nil {
		t.Fatal(err)
	}
	storage := newMemoryAuditExportStorage()
	service, err := NewAdminAuditExportService(db, storage, audit)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 31, 10, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return now }
	return adminAuditExportFixture{
		db:      db,
		audit:   audit,
		service: service,
		storage: storage,
		user:    user,
		now:     now,
	}
}

func (fixture adminAuditExportFixture) createAnchor(
	t *testing.T,
) *AdminAuditRecord {
	t.Helper()
	record := &AdminAuditRecord{
		Actor:            models.HumanActor(fixture.user.ID),
		UserID:           &fixture.user.ID,
		PlatformRole:     fixture.user.PlatformRole,
		Action:           "创建平台审计导出",
		ActionCode:       "platform.audit_export.create",
		ResourceType:     "audit_export",
		ResourcePublicID: "new",
		Method:           "POST",
		Path:             "/api/platform/audit-exports",
		Result:           "pending",
	}
	if err := fixture.audit.Record(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	return record
}

func (fixture adminAuditExportFixture) filter(
	start time.Time,
	end time.Time,
) *AdminAuditFilter {
	return &AdminAuditFilter{
		StartTime: &start,
		EndTime:   &end,
	}
}

func TestAdminAuditExportAcceptsExactThirtyDaysAndBindsOwnerAnchor(
	t *testing.T,
) {
	fixture := newAdminAuditExportFixture(t)
	anchor := fixture.createAnchor(t)
	start := fixture.now.Add(-MaxAdminAuditExportRange)
	view, err := fixture.service.Create(
		context.Background(),
		fixture.user.ID,
		fixture.user.PlatformRole,
		fixture.filter(start, fixture.now),
		anchor.ID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if view.State != models.AdminAuditExportQueued ||
		view.PublicID == "" ||
		view.RequestedAt != fixture.now {
		t.Fatalf("unexpected export view: %+v", view)
	}
	if _, err := fixture.service.Get(
		context.Background(),
		fixture.user.ID+1,
		view.PublicID,
	); !errors.Is(err, ErrAdminAuditExportNotFound) {
		t.Fatalf("other owner get error = %v", err)
	}

	otherAnchor := fixture.createAnchor(t)
	tooEarly := start.Add(-time.Nanosecond)
	if _, err := fixture.service.Create(
		context.Background(),
		fixture.user.ID,
		fixture.user.PlatformRole,
		fixture.filter(tooEarly, fixture.now),
		otherAnchor.ID,
	); !errors.Is(err, ErrAdminAuditExportInvalidRange) {
		t.Fatalf("31-day export error = %v", err)
	}
}

func TestAuditExportPublicIDParserRequiresCanonicalUUIDv7(t *testing.T) {
	valid := uuid.Must(uuid.NewV7()).String()
	for _, test := range []struct {
		value string
		valid bool
	}{
		{value: valid, valid: true},
		{value: uuid.NewString()},
		{value: strings.ToUpper(valid)},
		{value: " " + valid},
		{value: "not-a-uuid"},
	} {
		if got := validAuditExportPublicID(test.value); got != test.valid {
			t.Errorf(
				"validAuditExportPublicID(%q) = %t, want %t",
				test.value,
				got,
				test.valid,
			)
		}
	}
}

func TestAdminAuditExportCSVRedactsAndProtectsFormulaCells(t *testing.T) {
	fixture := newAdminAuditExportFixture(t)
	role := models.PlatformRoleSecurityAuditor
	createdAt := fixture.now.Add(-time.Hour)
	records := []models.AdminAuditLog{
		{
			CreatedAt:    createdAt,
			ActorType:    models.ActorTypeHuman,
			ActorID:      "7",
			Username:     "=2+2",
			PlatformRole: &role,
			Action:       "+cmd Bearer top-secret",
			Method:       "GET",
			Path:         "/api/platform/audit-logs?cookie=session-secret",
			StatusCode:   200,
			ClientIP:     "192.168.20.33",
			Result:       "@SUM(A1:A2)",
			RequestID:    "-request",
		},
		{
			CreatedAt:    createdAt.Add(-time.Minute),
			ActorType:    models.ActorTypeHuman,
			ActorID:      "8",
			Username:     "审计员\xff",
			PlatformRole: &role,
			Action:       "包含,逗号\"引号\n换行",
			Method:       "POST",
			Path:         "/api/platform/users",
			StatusCode:   201,
			ClientIP:     "2001:db8:abcd::1",
			Result:       "success",
		},
		{
			CreatedAt:    createdAt.Add(-2 * time.Minute),
			ActorType:    models.ActorTypeHuman,
			ActorID:      "9",
			Username:     "third",
			PlatformRole: &role,
			Action:       "third",
			Method:       "GET",
			Path:         "/third",
			StatusCode:   200,
			Result:       "success",
		},
	}
	if err := fixture.db.Create(&records).Error; err != nil {
		t.Fatal(err)
	}
	anchor := fixture.createAnchor(t)
	view, err := fixture.service.Create(
		context.Background(),
		fixture.user.ID,
		fixture.user.PlatformRole,
		fixture.filter(fixture.now.Add(-24*time.Hour), fixture.now),
		anchor.ID,
	)
	if err != nil {
		t.Fatal(err)
	}
	worker, err := NewAdminAuditExportWorker(
		fixture.service,
		"audit-export-test-worker",
	)
	if err != nil {
		t.Fatal(err)
	}
	worker.maxRows = 2
	processed, err := worker.ProcessOne(context.Background())
	if err != nil || !processed {
		t.Fatalf("process export: processed=%t err=%v", processed, err)
	}
	ready, err := fixture.service.Get(
		context.Background(),
		fixture.user.ID,
		view.PublicID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if ready.State != models.AdminAuditExportCompleted ||
		ready.RowCount != 2 ||
		!ready.Truncated ||
		ready.SizeBytes <= 0 ||
		len(ready.SHA256) != 64 {
		t.Fatalf("ready export = %+v", ready)
	}
	download, err := fixture.service.Open(
		context.Background(),
		fixture.user.ID,
		view.PublicID,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer download.Reader.Close()
	payload, err := io.ReadAll(download.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(payload, []byte("top-secret")) ||
		bytes.Contains(payload, []byte("session-secret")) ||
		!utf8Payload(payload) {
		t.Fatalf("unsafe export payload: %q", payload)
	}
	rows, err := csv.NewReader(bytes.NewReader(payload)).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 || rows[1][1] != "'=2+2" ||
		!strings.HasPrefix(rows[1][3], "'+cmd") ||
		!strings.HasPrefix(rows[1][6], "'@SUM") ||
		rows[1][12] != "'-request" {
		t.Fatalf("CSV formula protection rows = %#v", rows)
	}
}

func TestAdminAuditExportFencingRejectsStaleWorkerFinalization(
	t *testing.T,
) {
	fixture := newAdminAuditExportFixture(t)
	anchor := fixture.createAnchor(t)
	start := fixture.now.Add(-time.Hour)
	if _, err := fixture.service.Create(
		context.Background(),
		fixture.user.ID,
		fixture.user.PlatformRole,
		fixture.filter(start, fixture.now),
		anchor.ID,
	); err != nil {
		t.Fatal(err)
	}
	first, err := fixture.service.claimAdminAuditExport(
		context.Background(),
		"worker-one",
		time.Minute,
	)
	if err != nil || first == nil {
		t.Fatalf("first claim: %+v %v", first, err)
	}
	secondNow := fixture.now.Add(2 * time.Minute)
	fixture.service.now = func() time.Time { return secondNow }
	second, err := fixture.service.claimAdminAuditExport(
		context.Background(),
		"worker-two",
		time.Minute,
	)
	if err != nil || second == nil {
		t.Fatalf("second claim: %+v %v", second, err)
	}
	if second.FencingToken <= first.FencingToken {
		t.Fatalf(
			"fencing tokens first=%d second=%d",
			first.FencingToken,
			second.FencingToken,
		)
	}
	stored := &StoredAttachmentObject{
		Key:    "audit-exports/stale.csv",
		Size:   10,
		SHA256: strings.Repeat("a", 64),
	}
	if ok, err := fixture.service.finalizeAdminAuditExport(
		context.Background(),
		*first,
		adminAuditExportGeneration{Rows: 1},
		stored,
	); err != nil || ok {
		t.Fatalf("stale finalization ok=%t err=%v", ok, err)
	}
	if ok, err := fixture.service.finalizeAdminAuditExport(
		context.Background(),
		*second,
		adminAuditExportGeneration{Rows: 1},
		stored,
	); err != nil || !ok {
		t.Fatalf("current finalization ok=%t err=%v", ok, err)
	}
}

func TestAdminAuditExportCleanupExpiresObjectAndUsesSystemActor(
	t *testing.T,
) {
	fixture := newAdminAuditExportFixture(t)
	role := models.PlatformRoleSecurityAuditor
	expires := fixture.now.Add(-time.Minute)
	payload := []byte("time,actor\n")
	sum := sha256.Sum256(payload)
	key := "audit-exports/expired.csv"
	fixture.storage.objects[key] = payload
	job := models.AdminAuditExportJob{
		RequesterUserID: fixture.user.ID,
		RequesterRole:   role,
		FilterSnapshot:  `{"start_time":"2026-07-30T10:00:00Z","end_time":"2026-07-31T10:00:00Z"}`,
		FilterHash:      strings.Repeat("a", 64),
		StartTime:       fixture.now.Add(-24 * time.Hour),
		EndTime:         fixture.now,
		AnchorCreatedAt: fixture.now,
		AnchorID:        1,
		State:           models.AdminAuditExportCompleted,
		RequestedAt:     fixture.now.Add(-2 * time.Hour),
		CompletedAt:     adminAuditExportTimePointer(fixture.now.Add(-time.Hour)),
		ExpiresAt:       &expires,
		ObjectKey:       key,
		SHA256:          hex.EncodeToString(sum[:]),
		SizeBytes:       int64(len(payload)),
	}
	if err := fixture.db.Create(&job).Error; err != nil {
		t.Fatal(err)
	}
	cleaned, err := fixture.service.CleanupExpired(
		context.Background(),
		25,
	)
	if err != nil || cleaned != 1 {
		t.Fatalf("cleanup count=%d err=%v", cleaned, err)
	}
	var persisted models.AdminAuditExportJob
	if err := fixture.db.First(&persisted, job.ID).Error; err != nil {
		t.Fatal(err)
	}
	if persisted.State != models.AdminAuditExportExpired ||
		persisted.ObjectKey != "" {
		t.Fatalf("persisted expired job = %+v", persisted)
	}
	var audit models.AdminAuditLog
	if err := fixture.db.Where(
		"action_code = ?",
		"platform.audit_export.cleanup",
	).First(&audit).Error; err != nil {
		t.Fatal(err)
	}
	if audit.ActorType != models.ActorTypeSystem ||
		audit.ActorID != "audit-export-cleaner" ||
		audit.PlatformRole != nil ||
		audit.UserID != nil {
		t.Fatalf("cleanup audit actor = %+v", audit)
	}
}

func adminAuditExportTimePointer(value time.Time) *time.Time {
	return &value
}

func utf8Payload(value []byte) bool {
	return strings.ToValidUTF8(string(value), "\uFFFD") == string(value)
}
