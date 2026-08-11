package agentplatform

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/handlers"
	"github.com/seaworld008/chronodesk/server/internal/httpcontract"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/scopeddb"
	"github.com/seaworld008/chronodesk/server/internal/services"

	"github.com/gin-gonic/gin"
	"gorm.io/datatypes"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func newTestRuntimeControl(
	t *testing.T,
	db *gorm.DB,
	native *services.AgentNativeService,
	readOnly bool,
) *RuntimeControl {
	t.Helper()
	if native == nil {
		native = services.NewAgentNativeService(db, services.AgentNativeOptions{})
	}
	control, err := NewRuntimeControl(context.Background(), native, db, readOnly)
	if err != nil {
		t.Fatalf("NewRuntimeControl() error = %v", err)
	}
	return control
}

func TestRuntimeSafetyControlsSurviveRestart(t *testing.T) {
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.SystemConfig{}); err != nil {
		t.Fatal(err)
	}
	first := newTestRuntimeControl(t, db, nil, false)
	enabled := true
	if _, err := first.UpdateCAS(
		context.Background(),
		1,
		RuntimeControlPatch{
			GlobalReadOnly: &enabled,
			EmergencyStop:  &enabled,
		},
		models.HumanActor(7),
	); err != nil {
		t.Fatal(err)
	}

	restarted := newTestRuntimeControl(t, db, nil, false)
	if !restarted.ReadOnly() || !restarted.EmergencyStop() {
		t.Fatalf(
			"persisted controls were lost: read_only=%v emergency=%v",
			restarted.ReadOnly(),
			restarted.EmergencyStop(),
		)
	}
}

func TestRuntimeSafetyControlsUseOneCASForIndependentSwitches(t *testing.T) {
	dsn := fmt.Sprintf(
		"file:%s?mode=memory&cache=shared",
		strings.ReplaceAll(t.Name(), "/", "_"),
	)
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.SystemConfig{}); err != nil {
		t.Fatal(err)
	}
	control := newTestRuntimeControl(t, db, nil, false)
	initial, err := control.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if initial.Version != 1 ||
		initial.GlobalReadOnly ||
		initial.EmergencyStop {
		t.Fatalf("initial snapshot = %+v", initial)
	}

	enableReadOnly := true
	readOnlySnapshot, err := control.UpdateCAS(
		context.Background(),
		initial.Version,
		RuntimeControlPatch{GlobalReadOnly: &enableReadOnly},
		models.HumanActor(17),
	)
	if err != nil {
		t.Fatal(err)
	}
	if readOnlySnapshot.Version != 2 ||
		!readOnlySnapshot.GlobalReadOnly ||
		readOnlySnapshot.EmergencyStop ||
		!control.ReadOnly() ||
		control.EmergencyStop() {
		t.Fatalf("read-only snapshot/runtime = %+v", readOnlySnapshot)
	}

	enableEmergency := true
	emergencySnapshot, err := control.UpdateCAS(
		context.Background(),
		readOnlySnapshot.Version,
		RuntimeControlPatch{EmergencyStop: &enableEmergency},
		models.HumanActor(17),
	)
	if err != nil {
		t.Fatal(err)
	}
	if emergencySnapshot.Version != 3 ||
		!emergencySnapshot.GlobalReadOnly ||
		!emergencySnapshot.EmergencyStop ||
		!control.ReadOnly() ||
		!control.EmergencyStop() {
		t.Fatalf("emergency snapshot/runtime = %+v", emergencySnapshot)
	}

	var rows []models.SystemConfig
	if err := db.Where(
		"key IN ?",
		[]string{agentReadOnlyConfigKey, agentEmergencyConfigKey},
	).Order("key ASC").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("protected control rows = %d, want 2", len(rows))
	}
	for _, row := range rows {
		if row.Version != 3 || row.UpdatedBy == nil || *row.UpdatedBy != 17 {
			t.Fatalf("protected row metadata = %+v", row)
		}
	}

	disableEmergency := false
	_, err = control.UpdateCAS(
		context.Background(),
		initial.Version,
		RuntimeControlPatch{EmergencyStop: &disableEmergency},
		models.HumanActor(17),
	)
	var conflict *RuntimeControlVersionConflict
	if !errors.As(err, &conflict) ||
		conflict.Expected != 1 ||
		conflict.Current != 3 {
		t.Fatalf("stale CAS error = %#v, want current version 3", err)
	}
	unchanged, err := control.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if unchanged != emergencySnapshot {
		t.Fatalf(
			"stale CAS changed snapshot: got %+v want %+v",
			unchanged,
			emergencySnapshot,
		)
	}
}

func TestRuntimeSafetyControlCASRejectsMissingPatchAndNonHumanActor(
	t *testing.T,
) {
	db, err := gorm.Open(
		sqlite.Open("file:runtime_control_validation?mode=memory&cache=shared"),
		&gorm.Config{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.SystemConfig{}); err != nil {
		t.Fatal(err)
	}
	control := newTestRuntimeControl(t, db, nil, false)
	if _, err := control.UpdateCAS(
		context.Background(),
		1,
		RuntimeControlPatch{},
		models.HumanActor(7),
	); !errors.Is(err, ErrRuntimeControlPatchRequired) {
		t.Fatalf("empty patch error = %v", err)
	}
	enabled := true
	if _, err := control.UpdateCAS(
		context.Background(),
		1,
		RuntimeControlPatch{EmergencyStop: &enabled},
		models.ServicePrincipalActor("agent-7"),
	); !errors.Is(err, ErrRuntimeControlHumanActorRequired) {
		t.Fatalf("service-principal actor error = %v", err)
	}
}

func TestRuntimeControlHumanActorIDUsesNativeUintWidth(t *testing.T) {
	maximum := strconv.FormatUint(uint64(math.MaxUint), 10)
	got, err := runtimeControlHumanActorID(models.ActorRef{
		Type: models.ActorTypeHuman,
		ID:   maximum,
	})
	if err != nil {
		t.Fatalf("runtimeControlHumanActorID(%q) error = %v", maximum, err)
	}
	if got != math.MaxUint {
		t.Fatalf(
			"runtimeControlHumanActorID(%q) = %d, want %d",
			maximum,
			got,
			uint(math.MaxUint),
		)
	}

	overflow := "18446744073709551616"
	if strconv.IntSize == 32 {
		overflow = strconv.FormatUint(uint64(math.MaxUint32)+1, 10)
	}
	if _, err := runtimeControlHumanActorID(models.ActorRef{
		Type: models.ActorTypeHuman,
		ID:   overflow,
	}); !errors.Is(err, ErrRuntimeControlHumanActorRequired) {
		t.Fatalf(
			"runtimeControlHumanActorID(%q) error = %v, want %v",
			overflow,
			err,
			ErrRuntimeControlHumanActorRequired,
		)
	}
}

func TestRuntimeSafetyControlCASAllowsOnlyOneConcurrentWriter(t *testing.T) {
	db, err := gorm.Open(
		sqlite.Open("file:runtime_control_concurrent_cas?mode=memory&cache=shared"),
		&gorm.Config{},
	)
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	// Serialize SQLite transactions so this unit test exercises the same
	// compare-after-commit outcome as PostgreSQL row locks without accepting a
	// SQLite-specific "database locked" branch.
	sqlDB.SetMaxOpenConns(1)
	if err := db.AutoMigrate(&models.SystemConfig{}); err != nil {
		t.Fatal(err)
	}
	control := newTestRuntimeControl(t, db, nil, false)
	enabled := true
	start := make(chan struct{})
	results := make(chan error, 2)
	for _, patch := range []RuntimeControlPatch{
		{GlobalReadOnly: &enabled},
		{EmergencyStop: &enabled},
	} {
		patch := patch
		go func() {
			<-start
			_, updateErr := control.UpdateCAS(
				context.Background(),
				1,
				patch,
				models.HumanActor(23),
			)
			results <- updateErr
		}()
	}
	close(start)

	successes := 0
	conflicts := 0
	for range 2 {
		updateErr := <-results
		if updateErr == nil {
			successes++
			continue
		}
		var conflict *RuntimeControlVersionConflict
		if errors.As(updateErr, &conflict) && conflict.Current == 2 {
			conflicts++
			continue
		}
		t.Fatalf("concurrent CAS error = %v", updateErr)
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf(
			"concurrent CAS successes=%d conflicts=%d",
			successes,
			conflicts,
		)
	}
	snapshot, err := control.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Version != 2 {
		t.Fatalf("concurrent CAS snapshot = %+v", snapshot)
	}
}

func TestRuntimeSafetyControlSerializesRefreshBeforeCommittedCAS(t *testing.T) {
	dsn := fmt.Sprintf(
		"file:%s_%d?mode=memory&cache=shared",
		strings.ReplaceAll(t.Name(), "/", "_"),
		time.Now().UnixNano(),
	)
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.SystemConfig{}); err != nil {
		t.Fatal(err)
	}
	control := newTestRuntimeControl(t, db, nil, false)

	refreshRead := make(chan struct{})
	releaseRefresh := make(chan struct{})
	var pauseFirstRuntimeQuery sync.Once
	if err := db.Callback().Query().
		After("gorm:query").
		Register(
			"chronodesk:test:pause_runtime_refresh",
			func(tx *gorm.DB) {
				if tx.Statement == nil ||
					tx.Statement.Table != "system_configs" {
					return
				}
				pauseFirstRuntimeQuery.Do(func() {
					close(refreshRead)
					<-releaseRefresh
				})
			},
		); err != nil {
		t.Fatal(err)
	}

	refreshDone := make(chan error, 1)
	go func() {
		refreshDone <- control.Refresh(context.Background())
	}()
	<-refreshRead

	updateStarted := make(chan struct{})
	updateDone := make(chan error, 1)
	enabled := true
	go func() {
		close(updateStarted)
		_, updateErr := control.UpdateCAS(
			context.Background(),
			1,
			RuntimeControlPatch{EmergencyStop: &enabled},
			models.HumanActor(29),
		)
		updateDone <- updateErr
	}()
	<-updateStarted
	select {
	case updateErr := <-updateDone:
		t.Fatalf(
			"CAS bypassed in-flight refresh serialization: %v",
			updateErr,
		)
	case <-time.After(100 * time.Millisecond):
	}

	close(releaseRefresh)
	if err := <-refreshDone; err != nil {
		t.Fatalf("paused refresh failed: %v", err)
	}
	if err := <-updateDone; err != nil {
		t.Fatalf("serialized CAS failed: %v", err)
	}
	if !control.EmergencyStop() || !control.Healthy() {
		t.Fatalf(
			"stale refresh overwrote committed safety state: healthy=%v emergency=%v",
			control.Healthy(),
			control.EmergencyStop(),
		)
	}
	snapshot, err := control.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Version != 2 || !snapshot.EmergencyStop {
		t.Fatalf("final serialized snapshot = %+v", snapshot)
	}
}

func TestRuntimeSafetyControlStartupFailsClosedWhenPersistenceIsUnavailable(t *testing.T) {
	db, err := gorm.Open(
		sqlite.Open("file:runtime_control_startup_failure?mode=memory&cache=shared"),
		&gorm.Config{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.SystemConfig{}); err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatal(err)
	}
	native := services.NewAgentNativeService(db, services.AgentNativeOptions{})
	control, err := NewRuntimeControl(context.Background(), native, db, false)
	if err == nil || control != nil {
		t.Fatalf(
			"NewRuntimeControl() = (%v, %v), want nil control and persistence error",
			control,
			err,
		)
	}
}

func TestRuntimeSafetyControlRefreshFailureStopsWritesUntilRecovery(t *testing.T) {
	db, err := gorm.Open(
		sqlite.Open("file:runtime_control_refresh_failure?mode=memory&cache=shared"),
		&gorm.Config{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.SystemConfig{}); err != nil {
		t.Fatal(err)
	}
	native := services.NewAgentNativeService(db, services.AgentNativeOptions{})
	control := newTestRuntimeControl(t, db, native, false)
	if !control.Healthy() || control.EmergencyStop() {
		t.Fatalf(
			"initial runtime control state unhealthy: healthy=%v emergency=%v",
			control.Healthy(),
			control.EmergencyStop(),
		)
	}

	if err := db.Migrator().DropTable(&models.SystemConfig{}); err != nil {
		t.Fatal(err)
	}
	if err := control.Refresh(context.Background()); err == nil {
		t.Fatal("refresh unexpectedly succeeded without persistence table")
	}
	if control.Healthy() || !control.EmergencyStop() {
		t.Fatalf(
			"refresh failure did not fail closed: healthy=%v emergency=%v",
			control.Healthy(),
			control.EmergencyStop(),
		)
	}

	if err := db.AutoMigrate(&models.SystemConfig{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Transaction(func(tx *gorm.DB) error {
		return control.ensureRuntimeControlRowsTx(
			context.Background(),
			tx,
			0,
		)
	}); err != nil {
		t.Fatalf("restore persisted safety controls: %v", err)
	}
	if err := control.Refresh(context.Background()); err != nil {
		t.Fatalf("refresh after persistence recovery: %v", err)
	}
	if !control.Healthy() || control.EmergencyStop() || control.ReadOnly() {
		t.Fatalf(
			"runtime control did not recover persisted defaults: healthy=%v emergency=%v read_only=%v",
			control.Healthy(),
			control.EmergencyStop(),
			control.ReadOnly(),
		)
	}
}

func TestRuntimeSafetyControlCorruptionAlwaysFailsClosed(t *testing.T) {
	for _, test := range []struct {
		name    string
		corrupt func(*gorm.DB) error
	}{
		{
			name: "missing emergency row",
			corrupt: func(db *gorm.DB) error {
				return db.Delete(
					&models.SystemConfig{},
					"key = ?",
					agentEmergencyConfigKey,
				).Error
			},
		},
		{
			name: "inactive emergency row",
			corrupt: func(db *gorm.DB) error {
				return db.Model(&models.SystemConfig{}).
					Where("key = ?", agentEmergencyConfigKey).
					Update("is_active", false).Error
			},
		},
		{
			name: "invalid emergency boolean",
			corrupt: func(db *gorm.DB) error {
				return db.Model(&models.SystemConfig{}).
					Where("key = ?", agentEmergencyConfigKey).
					Update("value", "TRUE").Error
			},
		},
		{
			name: "invalid value type",
			corrupt: func(db *gorm.DB) error {
				return db.Model(&models.SystemConfig{}).
					Where("key = ?", agentEmergencyConfigKey).
					Update("value_type", "string").Error
			},
		},
		{
			name: "inconsistent versions",
			corrupt: func(db *gorm.DB) error {
				return db.Model(&models.SystemConfig{}).
					Where("key = ?", agentReadOnlyConfigKey).
					Update("version", 2).Error
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			dsn := fmt.Sprintf(
				"file:%s_%d?mode=memory&cache=shared",
				strings.ReplaceAll(t.Name(), "/", "_"),
				time.Now().UnixNano(),
			)
			db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
			if err != nil {
				t.Fatal(err)
			}
			if err := db.AutoMigrate(&models.SystemConfig{}); err != nil {
				t.Fatal(err)
			}
			control := newTestRuntimeControl(t, db, nil, false)
			if err := test.corrupt(db); err != nil {
				t.Fatal(err)
			}
			if err := control.Refresh(context.Background()); err == nil {
				t.Fatal("corrupt persisted controls unexpectedly refreshed")
			}
			if control.Healthy() || !control.EmergencyStop() {
				t.Fatalf(
					"corruption did not fail closed: healthy=%v emergency=%v",
					control.Healthy(),
					control.EmergencyStop(),
				)
			}
			if _, err := control.Snapshot(context.Background()); err == nil {
				t.Fatal("corrupt persisted controls unexpectedly returned a snapshot")
			}
		})
	}
}

type adminWriteEnvelope struct {
	Data    json.RawMessage `json:"data"`
	Receipt *Receipt        `json:"receipt"`
}

func bindAdminTestProjectScope(
	scope models.ProjectScope,
	adminID uint,
) gin.HandlerFunc {
	return func(c *gin.Context) {
		scopedContext, err := services.WithOperationContext(
			c.Request.Context(),
			services.OperationContext{
				Scope:         scope,
				Actor:         models.HumanActor(adminID),
				Source:        services.SourceProtocolHumanREST,
				TraceID:       c.GetHeader("X-Request-ID"),
				CorrelationID: c.GetHeader("X-Request-ID"),
			},
		)
		if err != nil {
			c.AbortWithStatus(http.StatusForbidden)
			return
		}
		c.Request = c.Request.WithContext(scopedContext)
		c.Next()
	}
}

func TestAdminWriteEndpointsReturnReceiptsAndSafeDomainEvents(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(
		&models.User{},
		&models.Category{},
		&models.SystemConfig{},
		&models.ServicePrincipal{},
		&models.AgentCredential{},
		&models.AgentPolicy{},
		&models.PolicyDecision{},
		&models.Ticket{},
		&models.TicketAttachment{},
		&models.TicketLease{},
		&models.DomainEvent{},
		&models.OutboxDelivery{},
		&models.IdempotencyRecord{},
		&models.AuditChainHead{},
		&models.AuditLedgerEntry{},
	); err != nil {
		t.Fatal(err)
	}
	admin := models.User{
		Username:     "control-admin",
		Email:        "control-admin@example.com",
		PasswordHash: "not-a-real-password",
		PlatformRole: models.PlatformRolePlatformAdmin,
		Status:       models.UserStatusActive,
	}
	if err := db.Create(&admin).Error; err != nil {
		t.Fatal(err)
	}
	projectFixture := ensureAPIHandlerTestProject(t, db)
	auditLedger, err := services.NewAuditLedgerService(db)
	if err != nil {
		t.Fatal(err)
	}

	native := services.NewAgentNativeService(db, services.AgentNativeOptions{
		CredentialPepper: []byte("admin-handler-test-pepper"),
		AuditLedger:      auditLedger,
	})
	control := newTestRuntimeControl(t, db, native, false)
	handler := NewAdminHandler(
		db,
		native,
		control,
		time.Hour,
		[]byte("admin-handler-stable-replay-encryption-key"),
	)
	adminLists, err := NewAdminListService(
		db,
		[]byte("admin-handler-stable-list-cursor-key"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := handler.ConfigureListService(adminLists); err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("user_id", admin.ID)
		c.Set("user_role", "admin")
		c.Set("request_id", "req-"+strings.ReplaceAll(c.FullPath(), "/", "-"))
		c.Next()
	})
	router.Use(bindAdminTestProjectScope(projectFixture.project.Scope(), admin.ID))
	group := router.Group("/api/projects/:projectKey/admin/agents")
	handler.RegisterRoutes(group)

	var requestCounter int
	doWrite := func(
		tt *testing.T,
		method string,
		path string,
		body string,
		wantStatus int,
		expectedVersion uint64,
		wantAdminEvent bool,
	) (*httptest.ResponseRecorder, adminWriteEnvelope, models.DomainEvent) {
		tt.Helper()
		requestCounter++
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(method, path, bytes.NewBufferString(body))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("X-Request-ID", fmt.Sprintf("admin-request-%d", requestCounter))
		request.Header.Set("Idempotency-Key", fmt.Sprintf("admin-test-key-%04d", requestCounter))
		if expectedVersion > 0 {
			request.Header.Set("If-Match", httpcontract.FormatETag(expectedVersion))
		}
		router.ServeHTTP(recorder, request)
		if recorder.Code != wantStatus {
			tt.Fatalf("%s %s status=%d want=%d body=%s", method, path, recorder.Code, wantStatus, recorder.Body.String())
		}
		var envelope adminWriteEnvelope
		if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
			tt.Fatalf("%s %s decode response: %v body=%s", method, path, err, recorder.Body.String())
		}
		if envelope.Receipt == nil {
			tt.Fatalf("%s %s response has no receipt: %s", method, path, recorder.Body.String())
		}
		receipt := envelope.Receipt
		if receipt.OperationID == "" ||
			receipt.ResourceID == "" ||
			receipt.ResourceVersion == 0 ||
			receipt.EventID == "" ||
			len(receipt.ChangedFields) == 0 {
			tt.Fatalf("%s %s returned incomplete receipt: %+v", method, path, receipt)
		}
		if receipt.PolicyDecisionID != "" {
			tt.Fatalf("%s %s human admin receipt unexpectedly has policy decision %q", method, path, receipt.PolicyDecisionID)
		}
		if recorder.Header().Get("ETag") != httpcontract.FormatETag(receipt.ResourceVersion) {
			tt.Fatalf(
				"%s %s ETag=%q, want %q",
				method,
				path,
				recorder.Header().Get("ETag"),
				httpcontract.FormatETag(receipt.ResourceVersion),
			)
		}
		var event models.DomainEvent
		if err := db.First(&event, "id = ?", receipt.EventID).Error; err != nil {
			tt.Fatalf("%s %s receipt event not persisted: %v", method, path, err)
		}
		if wantAdminEvent &&
			(event.ActorType != models.ActorTypeHuman || event.ActorID != strconv.FormatUint(uint64(admin.ID), 10)) {
			tt.Fatalf("%s %s event actor=%s/%s, want human/%d", method, path, event.ActorType, event.ActorID, admin.ID)
		}
		if event.Subject == "" || event.ResourceVersion != receipt.ResourceVersion {
			tt.Fatalf("%s %s event/receipt version mismatch: event=%+v receipt=%+v", method, path, event, receipt)
		}
		if event.OrganizationID != projectFixture.project.OrganizationID ||
			event.ProjectID != projectFixture.project.ID {
			tt.Fatalf(
				"%s %s event scope=%d/%d want=%d/%d",
				method,
				path,
				event.OrganizationID,
				event.ProjectID,
				projectFixture.project.OrganizationID,
				projectFixture.project.ID,
			)
		}
		if wantAdminEvent && !strings.HasPrefix(event.Type, "io.chronodesk.admin.") {
			tt.Fatalf("%s %s event type=%q, want administrator event", method, path, event.Type)
		}
		eventJSON := string(event.Data)
		if strings.Contains(eventJSON, "client_secret") ||
			strings.Contains(eventJSON, "admin-handler-test-pepper") {
			tt.Fatalf("%s %s event leaked credential material: %s", method, path, eventJSON)
		}
		if wantAdminEvent {
			var eventData struct {
				RequestID     string   `json:"request_id"`
				Subject       string   `json:"subject"`
				ResourceID    string   `json:"resource_id"`
				ChangedFields []string `json:"changed_fields"`
			}
			if err := json.Unmarshal(event.Data, &eventData); err != nil {
				tt.Fatalf("%s %s decode event data: %v", method, path, err)
			}
			if eventData.RequestID == "" ||
				eventData.Subject != event.Subject ||
				eventData.ResourceID != receipt.ResourceID ||
				len(eventData.ChangedFields) == 0 {
				tt.Fatalf("%s %s incomplete administrator event data: %+v", method, path, eventData)
			}
		}
		return recorder, envelope, event
	}

	var principalID string
	var initialSecret string
	t.Run("principal create", func(t *testing.T) {
		recorder, envelope, _ := doWrite(
			t,
			http.MethodPost,
			"/api/projects/TEST/admin/agents/service-principals",
			`{"name":"admin-created-agent","scopes":["tickets:read","tasks:manage"]}`,
			http.StatusCreated,
			0,
			true,
		)
		if recorder.Header().Get("Cache-Control") != "no-store" ||
			recorder.Header().Get("Pragma") != "no-cache" {
			t.Fatalf("one-time credential response is cacheable: headers=%v", recorder.Header())
		}
		var data struct {
			ClientID     string `json:"client_id"`
			ClientSecret string `json:"client_secret"`
			ProjectKey   string `json:"project_key"`
		}
		if err := json.Unmarshal(envelope.Data, &data); err != nil {
			t.Fatal(err)
		}
		if data.ClientID == "" ||
			data.ClientSecret == "" ||
			data.ProjectKey != string(projectFixture.project.Key) {
			t.Fatalf("missing issued credential: %s", envelope.Data)
		}
		var grant models.ProjectPrincipalGrant
		if err := db.Where(
			"project_id = ? AND service_principal_id = ?",
			projectFixture.project.ID,
			data.ClientID,
		).First(&grant).Error; err != nil {
			t.Fatalf("load created project grant: %v", err)
		}
		if !grant.IsActive ||
			grant.Role != models.ProjectRoleAgent ||
			!grant.HasScope(models.ScopeTicketsRead) ||
			!grant.HasScope(models.ScopeTasksManage) {
			t.Fatalf("created project grant is incomplete: %+v", grant)
		}
		var ledgerEntry models.AuditLedgerEntry
		if err := db.Where(
			"organization_id = ? AND project_id = ?",
			projectFixture.organization.ID,
			projectFixture.project.ID,
		).First(&ledgerEntry).Error; err != nil {
			t.Fatalf("load service principal audit ledger entry: %v", err)
		}
		if ledgerEntry.Actor() != models.HumanActor(admin.ID) ||
			ledgerEntry.EventType !=
				"io.chronodesk.admin.service_principal.created.v1" {
			t.Fatalf("unexpected service principal audit entry: %+v", ledgerEntry)
		}
		principalID, initialSecret = data.ClientID, data.ClientSecret
	})
	t.Run("principal status", func(t *testing.T) {
		doWrite(
			t,
			http.MethodPut,
			"/api/projects/TEST/admin/agents/service-principals/"+principalID+"/status",
			`{"read_only":true}`,
			http.StatusOK,
			1,
			true,
		)
	})

	var rotatedCredentialID string
	var rotatedSecret string
	t.Run("credential rotate", func(t *testing.T) {
		recorder, envelope, _ := doWrite(
			t,
			http.MethodPost,
			"/api/projects/TEST/admin/agents/service-principals/"+principalID+"/credentials/rotate",
			"",
			http.StatusOK,
			2,
			true,
		)
		if recorder.Header().Get("Cache-Control") != "no-store" ||
			recorder.Header().Get("Pragma") != "no-cache" {
			t.Fatalf("rotated credential response is cacheable: headers=%v", recorder.Header())
		}
		var data struct {
			ClientSecret string `json:"client_secret"`
		}
		if err := json.Unmarshal(envelope.Data, &data); err != nil {
			t.Fatal(err)
		}
		rotatedSecret = data.ClientSecret
		rotatedCredentialID, _, _ = strings.Cut(rotatedSecret, ".")
		if rotatedCredentialID == "" || rotatedSecret == "" || rotatedSecret == initialSecret {
			t.Fatalf("invalid rotated credential payload: %s", envelope.Data)
		}
	})
	t.Run("credential revoke", func(t *testing.T) {
		doWrite(
			t,
			http.MethodDelete,
			"/api/projects/TEST/admin/agents/service-principals/"+principalID+"/credentials/"+rotatedCredentialID,
			"",
			http.StatusOK,
			3,
			true,
		)
	})

	var policyID string
	t.Run("policy create", func(t *testing.T) {
		_, envelope, _ := doWrite(
			t,
			http.MethodPost,
			"/api/projects/TEST/admin/agents/service-principals/"+principalID+"/policies",
			`{"name":"allow ticket query","effect":"allow","scope":"tickets:read","action":"ticket.read","resource_type":"ticket","resource_id":"*"}`,
			http.StatusCreated,
			4,
			true,
		)
		var policy models.AgentPolicy
		if err := json.Unmarshal(envelope.Data, &policy); err != nil {
			t.Fatal(err)
		}
		if policy.ID == "" {
			t.Fatalf("missing policy ID: %s", envelope.Data)
		}
		policyID = policy.ID
	})
	t.Run("policy disable", func(t *testing.T) {
		doWrite(
			t,
			http.MethodDelete,
			"/api/projects/TEST/admin/agents/service-principals/"+principalID+"/policies/"+policyID,
			"",
			http.StatusOK,
			1,
			true,
		)
	})

	ticket := models.Ticket{
		OrganizationID:     projectFixture.organization.ID,
		ProjectID:          projectFixture.project.ID,
		QueueID:            projectFixture.queue.ID,
		TicketNumber:       "ADMIN-CONTROL-1",
		Title:              "Admin control test",
		Description:        "Safe test ticket",
		Type:               models.TicketTypeRequest,
		Priority:           models.TicketPriorityNormal,
		Status:             models.TicketStatusOpen,
		Source:             models.TicketSourceAgent,
		Version:            1,
		TrustLevel:         models.TicketTrustLevelSystem,
		CreatedByID:        &admin.ID,
		CreatedByActorType: models.ActorTypeHuman,
		CreatedByActorID:   strconv.FormatUint(uint64(admin.ID), 10),
	}
	if err := db.Create(&ticket).Error; err != nil {
		t.Fatal(err)
	}
	attachment := models.TicketAttachment{
		TicketID:     ticket.ID,
		UploadedBy:   &admin.ID,
		ActorType:    models.ActorTypeHuman,
		ActorID:      strconv.FormatUint(uint64(admin.ID), 10),
		FileName:     "attachment.txt",
		OriginalName: "attachment.txt",
		FileSize:     4,
		MimeType:     "text/plain",
		StoragePath:  "test/attachment.txt",
		VirusScan:    models.VirusScanPending,
	}
	if err := db.Create(&attachment).Error; err != nil {
		t.Fatal(err)
	}
	t.Run("attachment scan", func(t *testing.T) {
		doWrite(
			t,
			http.MethodPost,
			fmt.Sprintf("/api/projects/TEST/admin/agents/attachments/%d/scan", attachment.ID),
			`{"status":"clean","details":"scanner verified"}`,
			http.StatusOK,
			1,
			true,
		)
	})

	var replayDelivery models.OutboxDelivery
	if err := db.Where(
		"organization_id = ? AND project_id = ?",
		projectFixture.organization.ID,
		projectFixture.project.ID,
	).Order("created_at ASC").First(&replayDelivery).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&models.OutboxDelivery{}).
		Where("id = ?", replayDelivery.ID).
		Updates(map[string]any{
			"status":     models.OutboxDeliveryFailed,
			"last_error": "temporary delivery failure",
		}).Error; err != nil {
		t.Fatal(err)
	}
	t.Run("outbox replay", func(t *testing.T) {
		doWrite(
			t,
			http.MethodPost,
			"/api/projects/TEST/admin/agents/outbox/"+replayDelivery.ID+"/replay",
			"",
			http.StatusAccepted,
			1,
			true,
		)
	})

	lease := models.TicketLease{
		ID:              "admin-force-release-lease",
		TicketID:        ticket.ID,
		HolderActorType: models.ActorTypeServicePrincipal,
		HolderActorID:   principalID,
		TicketVersion:   ticket.Version,
		ExpiresAt:       time.Now().UTC().Add(time.Hour),
		LastHeartbeatAt: time.Now().UTC(),
	}
	if err := db.Create(&lease).Error; err != nil {
		t.Fatal(err)
	}
	t.Run("force lease release", func(t *testing.T) {
		_, _, event := doWrite(
			t,
			http.MethodPost,
			"/api/projects/TEST/admin/agents/leases/"+lease.ID+"/force-release",
			"",
			http.StatusOK,
			1,
			true,
		)
		if event.Type != "io.chronodesk.admin.ticket.lease.force_released.v1" {
			t.Fatalf("force release did not record administrator event: %q", event.Type)
		}
	})

	const legacyOutboxToken = "legacy-outbox-query-token"
	if err := db.Model(&models.OutboxDelivery{}).
		Where("id = ?", replayDelivery.ID).
		Update(
			"last_error",
			"https://push.example.test/a2a?access_token="+legacyOutboxToken,
		).Error; err != nil {
		t.Fatal(err)
	}
	assertVersion := func(name string, want uint64, found bool, got uint64) {
		t.Helper()
		if !found || got != want {
			t.Fatalf("%s resource version found=%v got=%d want=%d", name, found, got, want)
		}
	}
	requestList := func(path string, target any) {
		t.Helper()
		recorder := httptest.NewRecorder()
		router.ServeHTTP(
			recorder,
			httptest.NewRequest(http.MethodGet, path, nil),
		)
		if recorder.Code != http.StatusOK {
			t.Fatalf("list %s status=%d body=%s", path, recorder.Code, recorder.Body.String())
		}
		if err := json.Unmarshal(recorder.Body.Bytes(), target); err != nil {
			t.Fatal(err)
		}
	}
	var principalEnvelope struct {
		Data struct {
			Items []struct {
				ID              string `json:"id"`
				ResourceVersion uint64 `json:"resource_version"`
			} `json:"items"`
		} `json:"data"`
	}
	requestList(
		"/api/projects/TEST/admin/agents/service-principals",
		&principalEnvelope,
	)
	var outboxEnvelope struct {
		Data struct {
			Items []struct {
				ID              string `json:"id"`
				ResourceVersion uint64 `json:"resource_version"`
				LastError       string `json:"last_error"`
			} `json:"items"`
		} `json:"data"`
	}
	requestList(
		"/api/projects/TEST/admin/agents/outbox",
		&outboxEnvelope,
	)
	var attachmentEnvelope struct {
		Data struct {
			Items []struct {
				ID              uint   `json:"id"`
				ResourceVersion uint64 `json:"resource_version"`
			} `json:"items"`
		} `json:"data"`
	}
	requestList(
		"/api/projects/TEST/admin/agents/attachments",
		&attachmentEnvelope,
	)
	var principalFound, deliveryFound, attachmentFound bool
	var principalVersion, deliveryVersion, attachmentVersion uint64
	for _, row := range principalEnvelope.Data.Items {
		if row.ID == principalID {
			principalFound, principalVersion = true, row.ResourceVersion
		}
	}
	for _, row := range outboxEnvelope.Data.Items {
		if row.ID == replayDelivery.ID {
			deliveryFound, deliveryVersion = true, row.ResourceVersion
			if strings.Contains(row.LastError, legacyOutboxToken) ||
				strings.Contains(row.LastError, "https://push.example.test") {
				t.Fatalf("administrator Outbox response leaked callback URL: %q", row.LastError)
			}
		}
	}
	for _, row := range attachmentEnvelope.Data.Items {
		if row.ID == attachment.ID {
			attachmentFound, attachmentVersion = true, row.ResourceVersion
		}
	}
	assertVersion("principal", 5, principalFound, principalVersion)
	assertVersion("outbox", 2, deliveryFound, deliveryVersion)
	assertVersion("attachment", 2, attachmentFound, attachmentVersion)

	policyRecorder := httptest.NewRecorder()
	policyRequest := httptest.NewRequest(
		http.MethodGet,
		"/api/projects/TEST/admin/agents/service-principals/"+principalID+"/policies",
		nil,
	)
	router.ServeHTTP(policyRecorder, policyRequest)
	if policyRecorder.Code != http.StatusOK {
		t.Fatalf("policy list status=%d body=%s", policyRecorder.Code, policyRecorder.Body.String())
	}
	var policyEnvelope struct {
		Data struct {
			Items []struct {
				ID              string `json:"id"`
				ResourceVersion uint64 `json:"resource_version"`
			} `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal(policyRecorder.Body.Bytes(), &policyEnvelope); err != nil {
		t.Fatal(err)
	}
	var listedPolicy bool
	var listedPolicyVersion uint64
	for _, row := range policyEnvelope.Data.Items {
		if row.ID == policyID {
			listedPolicy, listedPolicyVersion = true, row.ResourceVersion
		}
	}
	assertVersion("policy", 2, listedPolicy, listedPolicyVersion)

	for _, secret := range []string{initialSecret, rotatedSecret} {
		var leaked int64
		if err := db.Model(&models.DomainEvent{}).
			Where("CAST(data AS TEXT) LIKE ?", "%"+secret+"%").
			Count(&leaked).Error; err != nil {
			t.Fatal(err)
		}
		if leaked != 0 {
			t.Fatalf("credential secret leaked into %d domain events", leaked)
		}
	}
	var unscopedIdempotency int64
	if err := db.Model(&models.IdempotencyRecord{}).
		Where("organization_id = 0 OR project_id = 0").
		Count(&unscopedIdempotency).Error; err != nil {
		t.Fatal(err)
	}
	if unscopedIdempotency != 0 {
		t.Fatalf(
			"administrator writes created %d unscoped idempotency records",
			unscopedIdempotency,
		)
	}
}

type adminContractFixture struct {
	db      *gorm.DB
	native  *services.AgentNativeService
	router  *gin.Engine
	admin   models.User
	scope   models.ProjectScope
	project models.Project
}

func newAdminContractFixture(t *testing.T) *adminContractFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)
	dsn := fmt.Sprintf(
		"file:%s?mode=memory&cache=shared&_busy_timeout=5000",
		strings.ReplaceAll(t.Name(), "/", "_"),
	)
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := scopeddb.Install(db); err != nil {
		t.Fatalf("install project scope transaction routing: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.AutoMigrate(
		&models.User{},
		&models.SystemConfig{},
		&models.ServicePrincipal{},
		&models.AgentCredential{},
		&models.AgentPolicy{},
		&models.PolicyDecision{},
		&models.Ticket{},
		&models.DomainEvent{},
		&models.OutboxDelivery{},
		&models.WebhookConfig{},
		&models.WebhookDeliverySnapshot{},
		&models.IdempotencyRecord{},
		&models.TicketLease{},
		&models.TicketAttachment{},
		&models.ProjectMembership{},
	); err != nil {
		t.Fatal(err)
	}
	admin := models.User{
		Username:     "contract-admin",
		Email:        "contract-admin@example.com",
		PasswordHash: "not-a-real-password",
		PlatformRole: models.PlatformRolePlatformAdmin,
		Status:       models.UserStatusActive,
	}
	if err := db.Create(&admin).Error; err != nil {
		t.Fatal(err)
	}
	projectFixture := ensureAPIHandlerTestProject(t, db)
	if err := db.Create(&models.ProjectMembership{
		ProjectID: projectFixture.project.ID,
		UserID:    admin.ID,
		Role:      models.ProjectRoleAdmin,
		IsActive:  true,
		Version:   1,
	}).Error; err != nil {
		t.Fatalf("seed administrator project membership: %v", err)
	}
	native := services.NewAgentNativeService(db, services.AgentNativeOptions{
		CredentialPepper: []byte("admin-contract-credential-pepper"),
	})
	router := newAdminContractRouter(
		t,
		db,
		native,
		admin,
		projectFixture.project.Scope(),
		[]byte("admin-contract-stable-replay-encryption-key"),
	)
	return &adminContractFixture{
		db:      db,
		native:  native,
		router:  router,
		admin:   admin,
		scope:   projectFixture.project.Scope(),
		project: projectFixture.project,
	}
}

func TestAdminReplayDueWebhookCommitsExpiryThenReturnsStableConflict(
	t *testing.T,
) {
	fixture := newAdminContractFixture(t)
	due := seedAdminDueWebhookReplay(t, fixture)

	response := performAdminContractRequest(
		fixture.router,
		http.MethodPost,
		"/api/projects/TEST/admin/agents/outbox/"+
			due.delivery.ID+"/replay",
		"",
		"expired-replay",
		httpcontract.FormatETag(1),
		"expired-replay",
	)
	assertAdminDueReplayCommitted(t, fixture, due, response)
}

func TestAdminReplayDueWebhookCommitsThroughProjectScopeMiddleware(
	t *testing.T,
) {
	fixture := newAdminContractFixture(t)
	due := seedAdminDueWebhookReplay(t, fixture)
	projectService, err := services.NewProjectService(fixture.db)
	if err != nil {
		t.Fatal(err)
	}
	handler := NewAdminHandler(
		fixture.db,
		fixture.native,
		newTestRuntimeControl(t, fixture.db, fixture.native, false),
		time.Hour,
		[]byte("project-middleware-expired-replay-key"),
	)
	adminLists, err := NewAdminListService(
		fixture.db,
		[]byte("project-middleware-expired-list-cursor-key"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := handler.ConfigureListService(adminLists); err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("user_id", fixture.admin.ID)
		c.Set("platform_role", models.PlatformRolePlatformAdmin)
		c.Set("request_id", "project-middleware-expired-replay")
		c.Next()
	})
	group := router.Group("/api/projects/:projectKey/admin/agents")
	group.Use(handlers.ProjectScopeMiddleware(
		projectService,
		fixture.db,
	))
	group.Use(handlers.RequireProjectRoles(models.ProjectRoleAdmin))
	handler.RegisterRoutes(group)

	response := performAdminContractRequest(
		router,
		http.MethodPost,
		"/api/projects/TEST/admin/agents/outbox/"+
			due.delivery.ID+"/replay",
		"",
		"project-middleware-expired-replay",
		httpcontract.FormatETag(1),
		"project-middleware-expired-replay",
	)
	assertAdminDueReplayCommitted(t, fixture, due, response)
	if response.Header().Get("ETag") != httpcontract.FormatETag(2) {
		t.Fatalf(
			"after-commit ETag = %q, want %q",
			response.Header().Get("ETag"),
			httpcontract.FormatETag(2),
		)
	}
	assertAdminOutboxHTTPProjectionSafe(
		t,
		router,
		due,
	)

	var idempotency models.IdempotencyRecord
	if err := fixture.db.Where(
		"key = ?",
		"project-middleware-expired-replay",
	).Take(&idempotency).Error; err != nil {
		t.Fatal(err)
	}
	if idempotency.State != models.IdempotencyStateFailed ||
		idempotency.LastErrorCode != "outbox_replay_expired" {
		t.Fatalf(
			"expired replay idempotency outcome = %+v",
			idempotency,
		)
	}
	var version models.SystemConfig
	if err := fixture.db.Where(
		"key = ?",
		adminResourceVersionKey(
			fixture.scope,
			"outbox/"+due.delivery.ID,
		),
	).Take(&version).Error; err != nil {
		t.Fatal(err)
	}
	if version.Version != 2 {
		t.Fatalf(
			"expired replay resource version = %d, want 2",
			version.Version,
		)
	}
}

func assertAdminOutboxHTTPProjectionSafe(
	t *testing.T,
	router http.Handler,
	due adminDueWebhookReplayFixture,
) {
	t.Helper()
	response := performAdminContractRequest(
		router,
		http.MethodGet,
		"/api/projects/TEST/admin/agents/outbox"+
			"?page=1&page_size=25&sort_by=created_at&sort_order=desc",
		"",
		"",
		"",
		"project-middleware-expired-list",
	)
	if response.Code != http.StatusOK {
		t.Fatalf(
			"safe Outbox list status=%d body=%s",
			response.Code,
			response.Body.String(),
		)
	}
	var envelope struct {
		Data struct {
			Items []map[string]json.RawMessage `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	var projected map[string]json.RawMessage
	for _, item := range envelope.Data.Items {
		var id string
		if err := json.Unmarshal(item["id"], &id); err != nil {
			t.Fatal(err)
		}
		if id == due.delivery.ID {
			projected = item
			break
		}
	}
	if projected == nil {
		t.Fatalf(
			"safe Outbox list omitted delivery %s: %s",
			due.delivery.ID,
			response.Body.String(),
		)
	}
	for _, required := range []string{
		"status",
		"expires_at",
		"expired_at",
		"destination_type",
		"destination_label",
	} {
		if _, exists := projected[required]; !exists {
			t.Errorf("safe Outbox projection omitted %q", required)
		}
	}
	for _, forbidden := range []string{
		"destination_id",
		"snapshot_id",
		"config_id",
		"locked_at",
		"locked_by",
		"lock_token",
		"generation",
		"credential",
		"credential_envelope",
		"webhook_url",
		"access_token",
		"secret",
		"previous_secret",
		"url",
		"headers",
	} {
		if _, exposed := projected[forbidden]; exposed {
			t.Errorf(
				"safe Outbox HTTP projection exposes %q: %s",
				forbidden,
				response.Body.String(),
			)
		}
	}
	for _, secret := range []string{
		due.snapshot.ID,
		due.snapshot.WebhookURL,
		due.snapshot.Secret,
		due.snapshot.PreviousSecret,
		due.snapshot.AccessToken,
	} {
		if secret != "" &&
			strings.Contains(response.Body.String(), secret) {
			t.Errorf(
				"safe Outbox HTTP projection leaked seeded value",
			)
		}
	}
}

type adminDueWebhookReplayFixture struct {
	event    models.DomainEvent
	snapshot models.WebhookDeliverySnapshot
	delivery models.OutboxDelivery
	deadline time.Time
}

func seedAdminDueWebhookReplay(
	t *testing.T,
	fixture *adminContractFixture,
) adminDueWebhookReplayFixture {
	t.Helper()
	now := time.Now().UTC()
	deadline := now.Add(-time.Minute)
	publishedAt := now.Add(-30 * time.Second)
	if err := fixture.db.Unscoped().Create(&models.WebhookConfig{
		ID:        1,
		CreatedAt: now.Add(-time.Hour),
		UpdatedAt: now.Add(-time.Hour),
		DeletedAt: gorm.DeletedAt{
			Time:  now.Add(-30 * time.Minute),
			Valid: true,
		},
		OrganizationID: fixture.scope.OrganizationID,
		ProjectID:      fixture.scope.ProjectID,
		Name:           "Soft-deleted replay lock anchor",
		Provider:       models.WebhookProviderCustom,
		WebhookURL:     "https://expired.invalid.example/events",
		Status:         models.WebhookStatusDisabled,
		CreatedBy:      fixture.admin.ID,
	}).Error; err != nil {
		t.Fatal(err)
	}
	event := models.DomainEvent{
		ID:              "00000000-0000-7000-8000-000000009101",
		OrganizationID:  fixture.scope.OrganizationID,
		ProjectID:       fixture.scope.ProjectID,
		SpecVersion:     "1.0",
		Source:          "urn:chronodesk:test:admin-replay-expired",
		Type:            "io.chronodesk.test.admin-replay-expired.v1",
		Subject:         "outbox-replay/expired",
		Time:            now.Add(-time.Hour),
		DataContentType: "application/json",
		Data:            datatypes.JSON(`{"safe":true}`),
		ActorType:       models.ActorTypeSystem,
		ActorID:         "admin-replay-expired-test",
		ResourceVersion: 1,
		PublishedAt:     &publishedAt,
	}
	if err := fixture.db.Create(&event).Error; err != nil {
		t.Fatal(err)
	}
	snapshot := models.WebhookDeliverySnapshot{
		ID:                  "00000000-0000-7000-8000-000000009102",
		OrganizationID:      fixture.scope.OrganizationID,
		ProjectID:           fixture.scope.ProjectID,
		ConfigID:            1,
		EventID:             event.ID,
		ConfigUpdatedAt:     now.Add(-time.Hour),
		Provider:            models.WebhookProviderCustom,
		WebhookURL:          "https://expired.invalid.example/events",
		Secret:              "sealed-secret",
		PreviousSecret:      "sealed-previous-secret",
		AccessToken:         "sealed-token",
		CredentialExpiresAt: deadline,
		EnabledEvents:       "ticket.created",
		RetryCount:          8,
		RetryInterval:       60,
		TimeoutSeconds:      10,
		RateLimit:           10,
		RateLimitWindow:     60,
	}
	if err := fixture.db.Create(&snapshot).Error; err != nil {
		t.Fatal(err)
	}
	destinationID, err :=
		models.WebhookDeliverySnapshotDestinationID(snapshot.ID)
	if err != nil {
		t.Fatal(err)
	}
	delivery := models.OutboxDelivery{
		ID:              "00000000-0000-7000-8000-000000009103",
		OrganizationID:  fixture.scope.OrganizationID,
		ProjectID:       fixture.scope.ProjectID,
		EventID:         event.ID,
		DestinationType: "webhook",
		DestinationID:   destinationID,
		Status:          models.OutboxDeliveryFailed,
		Attempts:        3,
		MaxAttempts:     8,
		NextAttemptAt:   deadline,
		LastError:       "bounded failure",
		ExpiresAt:       &deadline,
	}
	if err := fixture.db.Create(&delivery).Error; err != nil {
		t.Fatal(err)
	}
	return adminDueWebhookReplayFixture{
		event:    event,
		snapshot: snapshot,
		delivery: delivery,
		deadline: deadline,
	}
}

func assertAdminDueReplayCommitted(
	t *testing.T,
	fixture *adminContractFixture,
	due adminDueWebhookReplayFixture,
	response *httptest.ResponseRecorder,
) {
	t.Helper()
	if response.Code != http.StatusConflict ||
		!strings.Contains(
			response.Body.String(),
			ProblemOutboxExpired,
		) {
		t.Fatalf(
			"expired replay status=%d body=%s",
			response.Code,
			response.Body.String(),
		)
	}
	var currentDelivery models.OutboxDelivery
	if err := fixture.db.First(
		&currentDelivery,
		"id = ?",
		due.delivery.ID,
	).Error; err != nil {
		t.Fatal(err)
	}
	if currentDelivery.Status != models.OutboxDeliveryExpired ||
		currentDelivery.ExpiredAt == nil ||
		currentDelivery.ExpiresAt == nil ||
		!currentDelivery.ExpiresAt.Equal(due.deadline) {
		t.Fatalf(
			"expired replay terminal state rolled back: %+v",
			currentDelivery,
		)
	}
	var currentSnapshot models.WebhookDeliverySnapshot
	if err := fixture.db.First(
		&currentSnapshot,
		"id = ?",
		due.snapshot.ID,
	).Error; err != nil {
		t.Fatal(err)
	}
	if currentSnapshot.CredentialShreddedAt == nil ||
		currentSnapshot.CredentialShredReason == nil ||
		*currentSnapshot.CredentialShredReason !=
			models.WebhookCredentialShredReasonExpired ||
		currentSnapshot.Secret != "" ||
		currentSnapshot.PreviousSecret != "" ||
		currentSnapshot.AccessToken != "" {
		t.Fatalf(
			"expired replay retained credential material: %+v",
			currentSnapshot,
		)
	}
	var currentEvent models.DomainEvent
	if err := fixture.db.First(
		&currentEvent,
		"id = ?",
		due.event.ID,
	).Error; err != nil {
		t.Fatal(err)
	}
	if due.event.PublishedAt == nil ||
		currentEvent.PublishedAt == nil ||
		!currentEvent.PublishedAt.Equal(*due.event.PublishedAt) {
		t.Fatalf(
			"expired replay publication history=%v, want %v",
			currentEvent.PublishedAt,
			due.event.PublishedAt,
		)
	}
}

func TestNativeProblemMapsReplayExpiryAcrossSharedAdapters(t *testing.T) {
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Set("request_id", "outbox-replay-expired")

	writeNativeProblem(context, services.ErrOutboxReplayExpired)

	if recorder.Code != http.StatusConflict ||
		!strings.Contains(recorder.Body.String(), ProblemOutboxExpired) {
		t.Fatalf(
			"shared native problem status=%d body=%s",
			recorder.Code,
			recorder.Body.String(),
		)
	}
}

func newAdminContractRouter(
	t *testing.T,
	db *gorm.DB,
	native *services.AgentNativeService,
	admin models.User,
	scope models.ProjectScope,
	replayKey []byte,
) *gin.Engine {
	handler := NewAdminHandler(
		db,
		native,
		newTestRuntimeControl(t, db, native, false),
		time.Hour,
		replayKey,
	)
	adminLists, err := NewAdminListService(
		db,
		[]byte("admin-contract-stable-list-cursor-key"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := handler.ConfigureListService(adminLists); err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("user_id", admin.ID)
		c.Set("user_role", "admin")
		if requestID := c.GetHeader("X-Request-ID"); requestID != "" {
			c.Set("request_id", requestID)
		} else {
			c.Set("request_id", "admin-contract-request")
		}
		c.Next()
	})
	router.Use(bindAdminTestProjectScope(scope, admin.ID))
	handler.RegisterRoutes(
		router.Group("/api/projects/:projectKey/admin/agents"),
	)
	return router
}

func performAdminContractRequest(
	router http.Handler,
	method string,
	path string,
	body string,
	idempotencyKey string,
	ifMatch string,
	requestID string,
) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	request.Header.Set("Content-Type", "application/json")
	if idempotencyKey != "" {
		request.Header.Set("Idempotency-Key", idempotencyKey)
	}
	if ifMatch != "" {
		request.Header.Set("If-Match", ifMatch)
	}
	if requestID != "" {
		request.Header.Set("X-Request-ID", requestID)
	}
	router.ServeHTTP(recorder, request)
	return recorder
}

func TestCreateServicePrincipalDerivesProjectOnlyFromTrustedPath(t *testing.T) {
	fixture := newAdminContractFixture(t)

	untrustedBodyScope := performAdminContractRequest(
		fixture.router,
		http.MethodPost,
		"/api/projects/TEST/admin/agents/service-principals",
		`{"name":"body-scoped","project_key":"MISSING","scopes":["tickets:read"]}`,
		"body-project-rejected",
		"",
		"body-project-request",
	)
	if untrustedBodyScope.Code != http.StatusBadRequest ||
		!strings.Contains(untrustedBodyScope.Body.String(), "unknown field") {
		t.Fatalf(
			"body project field status=%d body=%s",
			untrustedBodyScope.Code,
			untrustedBodyScope.Body.String(),
		)
	}

	created := performAdminContractRequest(
		fixture.router,
		http.MethodPost,
		"/api/projects/TEST/admin/agents/service-principals",
		`{"name":"path-scoped","scopes":["tickets:read"]}`,
		"path-project-create",
		"",
		"path-project-request",
	)
	if created.Code != http.StatusCreated {
		t.Fatalf(
			"path-scoped create status=%d body=%s",
			created.Code,
			created.Body.String(),
		)
	}
	var envelope adminWriteEnvelope
	if err := json.Unmarshal(created.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	var result struct {
		ClientID   string `json:"client_id"`
		ProjectKey string `json:"project_key"`
	}
	if err := json.Unmarshal(envelope.Data, &result); err != nil {
		t.Fatal(err)
	}
	if result.ClientID == "" || result.ProjectKey != "TEST" {
		t.Fatalf("created principal did not bind path project: %s", envelope.Data)
	}

	var rejectedPrincipalCount int64
	if err := fixture.db.Model(&models.ServicePrincipal{}).
		Where("name = ?", "body-scoped").
		Count(&rejectedPrincipalCount).Error; err != nil {
		t.Fatal(err)
	}
	var grant models.ProjectPrincipalGrant
	if err := fixture.db.Where(
		"project_id = ? AND service_principal_id = ?",
		fixture.scope.ProjectID,
		result.ClientID,
	).First(&grant).Error; err != nil {
		t.Fatal(err)
	}
	if rejectedPrincipalCount != 0 || !grant.IsActive {
		t.Fatalf(
			"trusted path grant mismatch: rejected=%d grant=%+v",
			rejectedPrincipalCount,
			grant,
		)
	}
}

func TestAdminRoutesRequireProjectScopeAndDoNotExposePlatformControlWrites(
	t *testing.T,
) {
	fixture := newAdminContractFixture(t)

	for name, request := range map[string]*http.Request{
		"legacy global route": httptest.NewRequest(
			http.MethodGet,
			"/api/admin/agents/agent-control/overview",
			nil,
		),
		"project global read-only write": httptest.NewRequest(
			http.MethodPut,
			"/api/projects/TEST/admin/agents/agent-control/read-only",
			strings.NewReader(`{"enabled":true}`),
		),
		"project emergency-stop write": httptest.NewRequest(
			http.MethodPut,
			"/api/projects/TEST/admin/agents/agent-control/emergency-stop",
			strings.NewReader(`{"enabled":true}`),
		),
	} {
		t.Run(name, func(t *testing.T) {
			response := httptest.NewRecorder()
			fixture.router.ServeHTTP(response, request)
			if response.Code != http.StatusNotFound {
				t.Fatalf(
					"status=%d want=%d body=%s",
					response.Code,
					http.StatusNotFound,
					response.Body.String(),
				)
			}
		})
	}

	handler := NewAdminHandler(
		fixture.db,
		fixture.native,
		newTestRuntimeControl(t, fixture.db, fixture.native, false),
		time.Hour,
		[]byte("unscoped-admin-test-replay-key"),
	)
	unscoped := gin.New()
	unscoped.Use(func(c *gin.Context) {
		c.Set("user_id", fixture.admin.ID)
		c.Set("user_role", "admin")
		c.Next()
	})
	handler.RegisterRoutes(
		unscoped.Group("/api/projects/:projectKey/admin/agents"),
	)
	response := performAdminContractRequest(
		unscoped,
		http.MethodPost,
		"/api/projects/TEST/admin/agents/service-principals",
		`{"name":"unscoped-principal","scopes":["tickets:read"]}`,
		"unscoped-write",
		"",
		"unscoped-write",
	)
	if response.Code != http.StatusForbidden {
		t.Fatalf(
			"unscoped write status=%d want=%d body=%s",
			response.Code,
			http.StatusForbidden,
			response.Body.String(),
		)
	}
	mismatched := gin.New()
	mismatched.Use(func(c *gin.Context) {
		c.Set("user_id", fixture.admin.ID)
		c.Set("user_role", "admin")
		c.Next()
	})
	mismatched.Use(
		bindAdminTestProjectScope(fixture.scope, fixture.admin.ID+1),
	)
	handler.RegisterRoutes(
		mismatched.Group("/api/projects/:projectKey/admin/agents"),
	)
	mismatchedResponse := performAdminContractRequest(
		mismatched,
		http.MethodPost,
		"/api/projects/TEST/admin/agents/service-principals",
		`{"name":"mismatched-actor","scopes":["tickets:read"]}`,
		"mismatched-actor-write",
		"",
		"mismatched-actor-write",
	)
	if mismatchedResponse.Code != http.StatusForbidden {
		t.Fatalf(
			"mismatched actor status=%d want=%d body=%s",
			mismatchedResponse.Code,
			http.StatusForbidden,
			mismatchedResponse.Body.String(),
		)
	}
	var unsafeRecords int64
	if err := fixture.db.Model(&models.IdempotencyRecord{}).
		Where("organization_id = 0 OR project_id = 0").
		Count(&unsafeRecords).Error; err != nil {
		t.Fatal(err)
	}
	if unsafeRecords != 0 {
		t.Fatalf("unscoped administrator write created %d idempotency records", unsafeRecords)
	}
}

func TestAdminWriteRunsThroughHumanProjectScopeTransaction(t *testing.T) {
	fixture := newAdminContractFixture(t)
	projectService, err := services.NewProjectService(fixture.db)
	if err != nil {
		t.Fatal(err)
	}
	handler := NewAdminHandler(
		fixture.db,
		fixture.native,
		newTestRuntimeControl(t, fixture.db, fixture.native, false),
		time.Hour,
		[]byte("project-middleware-admin-replay-key"),
	)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("user_id", fixture.admin.ID)
		c.Set("platform_role", models.PlatformRolePlatformAdmin)
		c.Set("request_id", "project-middleware-admin-request")
		c.Next()
	})
	group := router.Group("/api/projects/:projectKey/admin/agents")
	group.Use(handlers.ProjectScopeMiddleware(projectService, fixture.db))
	handler.RegisterRoutes(group)

	response := performAdminContractRequest(
		router,
		http.MethodPost,
		"/api/projects/TEST/admin/agents/service-principals",
		`{"name":"middleware-scoped-principal","scopes":["tickets:read"]}`,
		"middleware-scoped-create",
		"",
		"middleware-scoped-request",
	)
	if response.Code != http.StatusCreated {
		t.Fatalf(
			"project middleware create status=%d body=%s",
			response.Code,
			response.Body.String(),
		)
	}
	var record models.IdempotencyRecord
	if err := fixture.db.Where(
		"key = ?",
		"middleware-scoped-create",
	).First(&record).Error; err != nil {
		t.Fatal(err)
	}
	if record.OrganizationID != fixture.scope.OrganizationID ||
		record.ProjectID != fixture.scope.ProjectID {
		t.Fatalf(
			"idempotency scope=%d/%d want=%d/%d",
			record.OrganizationID,
			record.ProjectID,
			fixture.scope.OrganizationID,
			fixture.scope.ProjectID,
		)
	}
	var event models.DomainEvent
	if err := fixture.db.First(&event, "id = ?", record.EventID).Error; err != nil {
		t.Fatal(err)
	}
	if event.OrganizationID != fixture.scope.OrganizationID ||
		event.ProjectID != fixture.scope.ProjectID {
		t.Fatalf(
			"domain event scope=%d/%d want=%d/%d",
			event.OrganizationID,
			event.ProjectID,
			fixture.scope.OrganizationID,
			fixture.scope.ProjectID,
		)
	}
}

func TestAdminProjectScopeFiltersOverviewAndOpaqueMutations(t *testing.T) {
	fixture := newAdminContractFixture(t)

	var unit models.BusinessUnit
	if err := fixture.db.First(
		&unit,
		"id = ?",
		fixture.project.BusinessUnitID,
	).Error; err != nil {
		t.Fatal(err)
	}
	foreignProject := models.Project{
		OrganizationID: fixture.scope.OrganizationID,
		BusinessUnitID: unit.ID,
		Key:            "OTHER",
		Name:           "Other",
		Status:         models.ProjectStatusActive,
	}
	if err := fixture.db.Create(&foreignProject).Error; err != nil {
		t.Fatal(err)
	}
	foreignQueue := models.Queue{
		ProjectID: foreignProject.ID,
		Key:       "default",
		Name:      "Foreign Default",
		Status:    models.QueueStatusActive,
		IsDefault: true,
	}
	if err := fixture.db.Create(&foreignQueue).Error; err != nil {
		t.Fatal(err)
	}
	foreignPrincipal, err := fixture.native.CreateServicePrincipal(
		context.Background(),
		services.CreateServicePrincipalInput{
			Name:   "foreign-project-agent",
			Scopes: []string{models.ScopeTicketsRead},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	grantAPIHandlerTestProject(
		t,
		fixture.db,
		foreignProject,
		foreignPrincipal.ID,
		foreignPrincipal.ScopeList(),
	)
	foreignTicket := models.Ticket{
		OrganizationID:     foreignProject.OrganizationID,
		ProjectID:          foreignProject.ID,
		QueueID:            foreignQueue.ID,
		TicketNumber:       "OTHER-1",
		Title:              "Foreign ticket",
		Description:        "Must remain isolated",
		Type:               models.TicketTypeRequest,
		Priority:           models.TicketPriorityNormal,
		Status:             models.TicketStatusOpen,
		Source:             models.TicketSourceAgent,
		Version:            1,
		TrustLevel:         models.TicketTrustLevelSystem,
		CreatedByActorType: models.ActorTypeSystem,
		CreatedByActorID:   "foreign-test",
	}
	if err := fixture.db.Create(&foreignTicket).Error; err != nil {
		t.Fatal(err)
	}
	foreignAttachment := models.TicketAttachment{
		TicketID:     foreignTicket.ID,
		ActorType:    models.ActorTypeSystem,
		ActorID:      "foreign-test",
		FileName:     "foreign.txt",
		OriginalName: "foreign.txt",
		FileSize:     7,
		MimeType:     "text/plain",
		StoragePath:  "foreign/foreign.txt",
		VirusScan:    models.VirusScanPending,
	}
	if err := fixture.db.Create(&foreignAttachment).Error; err != nil {
		t.Fatal(err)
	}
	foreignLease := models.TicketLease{
		ID:              "foreign-project-lease",
		TicketID:        foreignTicket.ID,
		HolderActorType: models.ActorTypeServicePrincipal,
		HolderActorID:   foreignPrincipal.ID,
		TicketVersion:   foreignTicket.Version,
		ExpiresAt:       time.Now().UTC().Add(time.Hour),
		LastHeartbeatAt: time.Now().UTC(),
	}
	if err := fixture.db.Create(&foreignLease).Error; err != nil {
		t.Fatal(err)
	}
	foreignContext := agentplatformTestOperationContext(
		t,
		foreignProject.Scope(),
		models.SystemActor("foreign-project-test"),
	)
	foreignEvent, err := appendTestDomainEvent(
		foreignContext,
		fixture.native,
		services.DomainEventInput{
			Type:            "io.chronodesk.test.foreign.v1",
			Subject:         "foreign/test",
			Actor:           models.SystemActor("foreign-project-test"),
			Scope:           foreignProject.Scope(),
			ResourceVersion: 1,
			Data:            gin.H{"foreign": true},
		},
		[]services.OutboxTarget{{
			Type: "event_stream",
			ID:   "foreign-project-test",
		}},
	)
	if err != nil {
		t.Fatal(err)
	}
	var foreignDelivery models.OutboxDelivery
	if err := fixture.db.First(
		&foreignDelivery,
		"event_id = ?",
		foreignEvent.ID,
	).Error; err != nil {
		t.Fatal(err)
	}
	foreignDecision := models.PolicyDecision{
		ID:                 "foreign-project-decision",
		OrganizationID:     foreignProject.OrganizationID,
		ProjectID:          foreignProject.ID,
		ServicePrincipalID: foreignPrincipal.ID,
		ActorType:          models.ActorTypeServicePrincipal,
		ActorID:            foreignPrincipal.ID,
		Scope:              models.ScopeTicketsRead,
		Action:             "ticket.read",
		ResourceType:       "ticket",
		ResourceID:         foreignTicket.TicketNumber,
		Allowed:            true,
		ReasonCode:         "scope_allowed",
		SourceProtocol:     "agent_rest",
	}
	if err := fixture.db.Create(&foreignDecision).Error; err != nil {
		t.Fatal(err)
	}

	overview := httptest.NewRecorder()
	fixture.router.ServeHTTP(
		overview,
		httptest.NewRequest(
			http.MethodGet,
			"/api/projects/TEST/admin/agents/agent-control/overview",
			nil,
		),
	)
	if overview.Code != http.StatusOK {
		t.Fatalf("overview status=%d body=%s", overview.Code, overview.Body.String())
	}
	for _, foreignValue := range []string{
		foreignPrincipal.ID,
		foreignLease.ID,
		foreignEvent.ID,
		foreignDelivery.ID,
		foreignAttachment.OriginalName,
		foreignDecision.ID,
		foreignTicket.TicketNumber,
	} {
		if strings.Contains(overview.Body.String(), foreignValue) {
			t.Fatalf(
				"project overview exposed foreign value %q: %s",
				foreignValue,
				overview.Body.String(),
			)
		}
	}

	foreignMutations := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{
			name:   "principal status",
			method: http.MethodPut,
			path: "/api/projects/TEST/admin/agents/service-principals/" +
				foreignPrincipal.ID + "/status",
			body: `{"read_only":true}`,
		},
		{
			name:   "lease release",
			method: http.MethodPost,
			path: "/api/projects/TEST/admin/agents/leases/" +
				foreignLease.ID + "/force-release",
		},
		{
			name:   "attachment scan",
			method: http.MethodPost,
			path: fmt.Sprintf(
				"/api/projects/TEST/admin/agents/attachments/%d/scan",
				foreignAttachment.ID,
			),
			body: `{"status":"clean"}`,
		},
		{
			name:   "outbox replay",
			method: http.MethodPost,
			path: "/api/projects/TEST/admin/agents/outbox/" +
				foreignDelivery.ID + "/replay",
		},
	}
	for index, mutation := range foreignMutations {
		response := performAdminContractRequest(
			fixture.router,
			mutation.method,
			mutation.path,
			mutation.body,
			fmt.Sprintf("foreign-mutation-%d", index),
			httpcontract.FormatETag(1),
			fmt.Sprintf("foreign-mutation-%d", index),
		)
		if response.Code != http.StatusNotFound {
			t.Fatalf(
				"%s status=%d want=%d body=%s",
				mutation.name,
				response.Code,
				http.StatusNotFound,
				response.Body.String(),
			)
		}
	}
	policies := httptest.NewRecorder()
	fixture.router.ServeHTTP(
		policies,
		httptest.NewRequest(
			http.MethodGet,
			"/api/projects/TEST/admin/agents/service-principals/"+
				foreignPrincipal.ID+
				"/policies",
			nil,
		),
	)
	if policies.Code != http.StatusNotFound {
		t.Fatalf(
			"foreign policy list status=%d want=%d body=%s",
			policies.Code,
			http.StatusNotFound,
			policies.Body.String(),
		)
	}

	var unsafeRecords int64
	if err := fixture.db.Model(&models.IdempotencyRecord{}).
		Where("organization_id = 0 OR project_id = 0").
		Count(&unsafeRecords).Error; err != nil {
		t.Fatal(err)
	}
	if unsafeRecords != 0 {
		t.Fatalf("foreign opaque IDs created %d unscoped idempotency rows", unsafeRecords)
	}
	var unsafeEvents int64
	if err := fixture.db.Model(&models.DomainEvent{}).
		Where("organization_id = 0 OR project_id = 0").
		Count(&unsafeEvents).Error; err != nil {
		t.Fatal(err)
	}
	if unsafeEvents != 0 {
		t.Fatalf("foreign opaque IDs created %d unscoped domain events", unsafeEvents)
	}
}

func TestAdminCommandsEnforceHeadersAndEncryptedExactReplay(t *testing.T) {
	fixture := newAdminContractFixture(t)

	writeEndpoints := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{"principal-create", http.MethodPost, "/api/projects/TEST/admin/agents/service-principals", `{"name":"missing-key-agent","scopes":["tickets:read"]}`},
		{"principal-status", http.MethodPut, "/api/projects/TEST/admin/agents/service-principals/missing/status", `{"read_only":true}`},
		{"credential-rotate", http.MethodPost, "/api/projects/TEST/admin/agents/service-principals/missing/credentials/rotate", ""},
		{"credential-revoke", http.MethodDelete, "/api/projects/TEST/admin/agents/service-principals/missing/credentials/missing", ""},
		{"policy-create", http.MethodPost, "/api/projects/TEST/admin/agents/service-principals/missing/policies", `{"effect":"allow","scope":"tickets:read"}`},
		{"policy-disable", http.MethodDelete, "/api/projects/TEST/admin/agents/service-principals/missing/policies/missing", ""},
		{"lease-release", http.MethodPost, "/api/projects/TEST/admin/agents/leases/missing/force-release", ""},
		{"attachment-scan", http.MethodPost, "/api/projects/TEST/admin/agents/attachments/1/scan", `{"status":"clean"}`},
		{"outbox-replay", http.MethodPost, "/api/projects/TEST/admin/agents/outbox/missing/replay", ""},
	}
	for _, endpoint := range writeEndpoints {
		t.Run("idempotency-required/"+endpoint.name, func(t *testing.T) {
			response := performAdminContractRequest(
				fixture.router,
				endpoint.method,
				endpoint.path,
				endpoint.body,
				"",
				httpcontract.FormatETag(1),
				"missing-idempotency",
			)
			if response.Code != http.StatusBadRequest ||
				!strings.Contains(response.Body.String(), "Idempotency-Key") {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
		if endpoint.name == "principal-create" {
			continue
		}
		t.Run("if-match-required/"+endpoint.name, func(t *testing.T) {
			response := performAdminContractRequest(
				fixture.router,
				endpoint.method,
				endpoint.path,
				endpoint.body,
				"headers-required-"+endpoint.name,
				"",
				"missing-if-match",
			)
			if response.Code != http.StatusPreconditionRequired ||
				!strings.Contains(response.Body.String(), "precondition_required") {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
	unknownField := performAdminContractRequest(
		fixture.router,
		http.MethodPost,
		"/api/projects/TEST/admin/agents/service-principals",
		`{"name":"unknown-field-agent","project_key":"TEST","scopes":["tickets:read"]}`,
		"unknown-field-rejected",
		"",
		"unknown-field",
	)
	if unknownField.Code != http.StatusBadRequest ||
		!strings.Contains(unknownField.Body.String(), "unknown field") {
		t.Fatalf("unknown field status=%d body=%s", unknownField.Code, unknownField.Body.String())
	}

	const createKey = "principal-create-exact-replay"
	first := performAdminContractRequest(
		fixture.router,
		http.MethodPost,
		"/api/projects/TEST/admin/agents/service-principals",
		` { "scopes" : ["tickets:read", "tasks:manage"], "name" : "exact-replay-agent" } `,
		createKey,
		"",
		"first-create-request",
	)
	if first.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", first.Code, first.Body.String())
	}
	if first.Header().Get("ETag") != httpcontract.FormatETag(1) ||
		first.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("create response headers=%v", first.Header())
	}
	var created adminWriteEnvelope
	if err := json.Unmarshal(first.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	var credentialData struct {
		ClientID     string `json:"client_id"`
		ClientSecret string `json:"client_secret"`
	}
	if err := json.Unmarshal(created.Data, &credentialData); err != nil {
		t.Fatal(err)
	}
	if credentialData.ClientID == "" || credentialData.ClientSecret == "" {
		t.Fatalf("credential missing from first response: %s", first.Body.String())
	}

	var credentialsBefore, eventsBefore int64
	if err := fixture.db.Model(&models.AgentCredential{}).Count(&credentialsBefore).Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.Model(&models.DomainEvent{}).Count(&eventsBefore).Error; err != nil {
		t.Fatal(err)
	}
	wrongKeyRouter := newAdminContractRouter(
		t,
		fixture.db,
		fixture.native,
		fixture.admin,
		fixture.scope,
		[]byte("different-admin-replay-encryption-key"),
	)
	wrongKeyReplay := performAdminContractRequest(
		wrongKeyRouter,
		http.MethodPost,
		"/api/projects/TEST/admin/agents/service-principals",
		`{"name":"exact-replay-agent","scopes":["tickets:read","tasks:manage"]}`,
		createKey,
		"",
		"wrong-key-retry",
	)
	if wrongKeyReplay.Code != http.StatusInternalServerError ||
		strings.Contains(wrongKeyReplay.Body.String(), credentialData.ClientSecret) {
		t.Fatalf(
			"wrong-key replay did not fail closed: status=%d body=%s",
			wrongKeyReplay.Code,
			wrongKeyReplay.Body.String(),
		)
	}
	restartedRouter := newAdminContractRouter(
		t,
		fixture.db,
		fixture.native,
		fixture.admin,
		fixture.scope,
		[]byte("admin-contract-stable-replay-encryption-key"),
	)
	replayed := performAdminContractRequest(
		restartedRouter,
		http.MethodPost,
		"/api/projects/TEST/admin/agents/service-principals",
		`{"name":"exact-replay-agent","scopes":["tickets:read","tasks:manage"]}`,
		createKey,
		"",
		"different-retry-request-id",
	)
	if replayed.Code != first.Code ||
		replayed.Body.String() != first.Body.String() ||
		replayed.Header().Get("ETag") != first.Header().Get("ETag") ||
		replayed.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf(
			"replay differs: first=(%d,%v,%s) replay=(%d,%v,%s)",
			first.Code,
			first.Header(),
			first.Body.String(),
			replayed.Code,
			replayed.Header(),
			replayed.Body.String(),
		)
	}
	var credentialsAfter, eventsAfter int64
	if err := fixture.db.Model(&models.AgentCredential{}).Count(&credentialsAfter).Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.Model(&models.DomainEvent{}).Count(&eventsAfter).Error; err != nil {
		t.Fatal(err)
	}
	if credentialsAfter != credentialsBefore || eventsAfter != eventsBefore {
		t.Fatalf(
			"replay duplicated side effects: credentials %d->%d events %d->%d",
			credentialsBefore,
			credentialsAfter,
			eventsBefore,
			eventsAfter,
		)
	}
	var replayRecord models.IdempotencyRecord
	if err := fixture.db.
		Where("key = ?", createKey).
		First(&replayRecord).Error; err != nil {
		t.Fatal(err)
	}
	persistedReplay := string(replayRecord.ResponseBody)
	if strings.Contains(persistedReplay, credentialData.ClientSecret) ||
		strings.Contains(persistedReplay, "client_secret") ||
		strings.Contains(persistedReplay, "exact-replay-agent") {
		t.Fatalf("one-time response persisted in plaintext: %s", persistedReplay)
	}

	conflicting := performAdminContractRequest(
		fixture.router,
		http.MethodPost,
		"/api/projects/TEST/admin/agents/service-principals",
		`{"name":"different-request","scopes":["tickets:read"]}`,
		createKey,
		"",
		"conflicting-request",
	)
	if conflicting.Code != http.StatusConflict ||
		!strings.Contains(conflicting.Body.String(), ProblemIdempotencyConflict) {
		t.Fatalf("conflicting replay status=%d body=%s", conflicting.Code, conflicting.Body.String())
	}

	statusKey := "principal-status-exact-replay"
	statusPath := "/api/projects/TEST/admin/agents/service-principals/" + credentialData.ClientID + "/status"
	statusFirst := performAdminContractRequest(
		fixture.router,
		http.MethodPut,
		statusPath,
		`{"read_only":true}`,
		statusKey,
		httpcontract.FormatETag(1),
		"status-first",
	)
	if statusFirst.Code != http.StatusOK || statusFirst.Header().Get("ETag") != httpcontract.FormatETag(2) {
		t.Fatalf("status update code=%d headers=%v body=%s", statusFirst.Code, statusFirst.Header(), statusFirst.Body.String())
	}
	statusReplay := performAdminContractRequest(
		fixture.router,
		http.MethodPut,
		statusPath,
		`{"read_only":true}`,
		statusKey,
		httpcontract.FormatETag(1),
		"status-retry",
	)
	if statusReplay.Code != statusFirst.Code ||
		statusReplay.Body.String() != statusFirst.Body.String() ||
		statusReplay.Header().Get("ETag") != httpcontract.FormatETag(2) {
		t.Fatalf("status replay differs: first=%s replay=%s", statusFirst.Body.String(), statusReplay.Body.String())
	}
	stale := performAdminContractRequest(
		fixture.router,
		http.MethodPut,
		statusPath,
		`{"emergency_disabled":true}`,
		"stale-version-command",
		httpcontract.FormatETag(1),
		"status-stale",
	)
	if stale.Code != http.StatusConflict ||
		stale.Header().Get("ETag") != httpcontract.FormatETag(2) ||
		!strings.Contains(stale.Body.String(), ProblemVersionConflict) {
		t.Fatalf("stale command status=%d headers=%v body=%s", stale.Code, stale.Header(), stale.Body.String())
	}

	processingBody := []byte(`{"read_only":false}`)
	const processingKey = "administrator-command-processing"
	processingReservation, err := fixture.native.ReserveIdempotency(
		agentplatformTestOperationContext(
			t,
			fixture.scope,
			models.HumanActor(fixture.admin.ID),
		),
		models.HumanActor(fixture.admin.ID),
		"admin:PUT:/api/projects/:projectKey/admin/agents/service-principals/:id/status",
		processingKey,
		commandFingerprint(
			http.MethodPut,
			statusPath,
			2,
			"",
			processingBody,
		),
		time.Hour,
	)
	if err != nil {
		t.Fatal(err)
	}
	if processingReservation.Replayed {
		t.Fatal("fresh processing reservation was unexpectedly replayed")
	}
	processing := performAdminContractRequest(
		fixture.router,
		http.MethodPut,
		statusPath,
		`{"read_only":false}`,
		processingKey,
		httpcontract.FormatETag(2),
		"status-processing",
	)
	if processing.Code != http.StatusConflict ||
		!strings.Contains(processing.Body.String(), ProblemIdempotencyConflict) {
		t.Fatalf("processing command status=%d body=%s", processing.Code, processing.Body.String())
	}

	rotateKey := "credential-rotation-exact-replay"
	rotatePath := "/api/projects/TEST/admin/agents/service-principals/" + credentialData.ClientID + "/credentials/rotate"
	rotated := performAdminContractRequest(
		fixture.router,
		http.MethodPost,
		rotatePath,
		"",
		rotateKey,
		httpcontract.FormatETag(2),
		"rotate-first",
	)
	if rotated.Code != http.StatusOK || rotated.Header().Get("ETag") != httpcontract.FormatETag(3) {
		t.Fatalf("rotate status=%d headers=%v body=%s", rotated.Code, rotated.Header(), rotated.Body.String())
	}
	var rotationCredentialsBefore int64
	if err := fixture.db.Model(&models.AgentCredential{}).Count(&rotationCredentialsBefore).Error; err != nil {
		t.Fatal(err)
	}
	rotationReplay := performAdminContractRequest(
		fixture.router,
		http.MethodPost,
		rotatePath,
		"",
		rotateKey,
		httpcontract.FormatETag(2),
		"rotate-retry",
	)
	if rotationReplay.Code != rotated.Code || rotationReplay.Body.String() != rotated.Body.String() {
		t.Fatalf("rotation replay differs: first=%s replay=%s", rotated.Body.String(), rotationReplay.Body.String())
	}
	var rotationCredentialsAfter int64
	if err := fixture.db.Model(&models.AgentCredential{}).Count(&rotationCredentialsAfter).Error; err != nil {
		t.Fatal(err)
	}
	if rotationCredentialsAfter != rotationCredentialsBefore {
		t.Fatalf("rotation replay created another credential: %d->%d", rotationCredentialsBefore, rotationCredentialsAfter)
	}
}

func TestAdminResourceCASAllowsOnlyOneConcurrentWriter(t *testing.T) {
	fixture := newAdminContractFixture(t)
	principal, err := fixture.native.CreateServicePrincipal(
		context.Background(),
		services.CreateServicePrincipalInput{
			Name:   "concurrent-admin-agent",
			Scopes: []string{models.ScopeTicketsRead},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	grantAPIHandlerTestProject(
		t,
		fixture.db,
		fixture.project,
		principal.ID,
		principal.ScopeList(),
	)
	path := "/api/projects/TEST/admin/agents/service-principals/" + principal.ID + "/status"
	type response struct {
		code int
		body string
		etag string
	}
	responses := make(chan response, 2)
	start := make(chan struct{})
	var wait sync.WaitGroup
	requests := []struct {
		key  string
		body string
	}{
		{"concurrent-writer-read-only", `{"read_only":true}`},
		{"concurrent-writer-emergency", `{"emergency_disabled":true}`},
	}
	for _, input := range requests {
		input := input
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			recorder := performAdminContractRequest(
				fixture.router,
				http.MethodPut,
				path,
				input.body,
				input.key,
				httpcontract.FormatETag(1),
				input.key,
			)
			responses <- response{
				code: recorder.Code,
				body: recorder.Body.String(),
				etag: recorder.Header().Get("ETag"),
			}
		}()
	}
	close(start)
	wait.Wait()
	close(responses)

	successes, conflicts := 0, 0
	for result := range responses {
		switch result.code {
		case http.StatusOK:
			successes++
			if result.etag != httpcontract.FormatETag(2) {
				t.Fatalf("successful CAS ETag=%q body=%s", result.etag, result.body)
			}
		case http.StatusConflict:
			conflicts++
			if result.etag != httpcontract.FormatETag(2) ||
				!strings.Contains(result.body, ProblemVersionConflict) {
				t.Fatalf("conflicting CAS ETag=%q body=%s", result.etag, result.body)
			}
		default:
			t.Fatalf("unexpected concurrent status=%d body=%s", result.code, result.body)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("concurrent outcomes success=%d conflict=%d", successes, conflicts)
	}
	var mutationEvents int64
	if err := fixture.db.Model(&models.DomainEvent{}).
		Where("subject = ? AND type = ?", "service-principal/"+principal.ID, "io.chronodesk.admin.service_principal.controls.updated.v1").
		Count(&mutationEvents).Error; err != nil {
		t.Fatal(err)
	}
	if mutationEvents != 1 {
		t.Fatalf("concurrent CAS produced %d domain events", mutationEvents)
	}
	var persisted models.ServicePrincipal
	if err := fixture.db.First(&persisted, "id = ?", principal.ID).Error; err != nil {
		t.Fatal(err)
	}
	if persisted.ReadOnly == persisted.EmergencyDisabled {
		t.Fatalf(
			"expected exactly one control mutation, read_only=%v emergency_disabled=%v",
			persisted.ReadOnly,
			persisted.EmergencyDisabled,
		)
	}
}

func TestAdminMutationsRollbackWhenEventOutboxAppendFails(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(
		&models.User{},
		&models.Category{},
		&models.SystemConfig{},
		&models.ServicePrincipal{},
		&models.AgentCredential{},
		&models.AgentPolicy{},
		&models.Ticket{},
		&models.TicketAttachment{},
		&models.DomainEvent{},
		&models.OutboxDelivery{},
		&models.IdempotencyRecord{},
	); err != nil {
		t.Fatal(err)
	}
	admin := models.User{
		Username:     "rollback-admin",
		Email:        "rollback-admin@example.com",
		PasswordHash: "not-a-real-password",
		PlatformRole: models.PlatformRolePlatformAdmin,
		Status:       models.UserStatusActive,
	}
	if err := db.Create(&admin).Error; err != nil {
		t.Fatal(err)
	}
	projectFixture := ensureAPIHandlerTestProject(t, db)
	healthy := services.NewAgentNativeService(db, services.AgentNativeOptions{
		CredentialPepper: []byte("rollback-test-pepper"),
	})
	failing := services.NewAgentNativeService(db, services.AgentNativeOptions{
		CredentialPepper: []byte("rollback-test-pepper"),
		DefaultOutboxTargets: []services.OutboxTarget{{
			Type: "",
			ID:   "",
		}},
	})
	control := newTestRuntimeControl(t, db, failing, false)
	handler := NewAdminHandler(
		db,
		failing,
		control,
		time.Hour,
		[]byte("rollback-stable-replay-encryption-key"),
	)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("user_id", admin.ID)
		c.Set("user_role", "admin")
		c.Set("request_id", "rollback-request")
		c.Next()
	})
	router.Use(bindAdminTestProjectScope(projectFixture.project.Scope(), admin.ID))
	handler.RegisterRoutes(
		router.Group("/api/projects/:projectKey/admin/agents"),
	)

	var failedRequestCounter int
	requestFailure := func(method, path, body string, expectedVersion uint64) {
		t.Helper()
		failedRequestCounter++
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(method, path, bytes.NewBufferString(body))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Idempotency-Key", fmt.Sprintf("rollback-key-%04d", failedRequestCounter))
		if expectedVersion > 0 {
			request.Header.Set("If-Match", httpcontract.FormatETag(expectedVersion))
		}
		router.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusInternalServerError {
			t.Fatalf("%s %s status=%d want=500 body=%s", method, path, recorder.Code, recorder.Body.String())
		}
		if strings.Contains(recorder.Body.String(), "client_secret") ||
			recorder.Header().Get("Cache-Control") != "" ||
			recorder.Header().Get("Pragma") != "" {
			t.Fatalf("%s %s exposed or cache-configured an uncommitted secret: headers=%v body=%s", method, path, recorder.Header(), recorder.Body.String())
		}
	}

	requestFailure(
		http.MethodPost,
		"/api/projects/TEST/admin/agents/service-principals",
		`{"name":"rolled-back-principal","scopes":["tickets:read"]}`,
		0,
	)
	var principalCount int64
	if err := db.Model(&models.ServicePrincipal{}).
		Where("name = ?", "rolled-back-principal").
		Count(&principalCount).Error; err != nil {
		t.Fatal(err)
	}
	var credentialCount int64
	if err := db.Model(&models.AgentCredential{}).Count(&credentialCount).Error; err != nil {
		t.Fatal(err)
	}
	if principalCount != 0 || credentialCount != 0 {
		t.Fatalf("principal create was not rolled back: principals=%d credentials=%d", principalCount, credentialCount)
	}

	principal, err := healthy.CreateServicePrincipal(context.Background(), services.CreateServicePrincipalInput{
		Name:   "rollback-existing-principal",
		Scopes: []string{models.ScopeTicketsRead},
	})
	if err != nil {
		t.Fatal(err)
	}
	grantAPIHandlerTestProject(
		t,
		db,
		projectFixture.project,
		principal.ID,
		principal.ScopeList(),
	)
	issued, err := healthy.IssueCredential(context.Background(), principal.ID, "existing", time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	requestFailure(
		http.MethodPost,
		"/api/projects/TEST/admin/agents/service-principals/"+principal.ID+"/credentials/rotate",
		"",
		1,
	)
	var credentials []models.AgentCredential
	if err := db.Where("service_principal_id = ?", principal.ID).Find(&credentials).Error; err != nil {
		t.Fatal(err)
	}
	if len(credentials) != 1 ||
		credentials[0].ID != issued.Credential.ID ||
		credentials[0].Status != models.AgentCredentialStatusActive ||
		credentials[0].RevokedAt != nil {
		t.Fatalf("credential rotation was not rolled back: %+v", credentials)
	}

	requestFailure(
		http.MethodDelete,
		"/api/projects/TEST/admin/agents/service-principals/"+principal.ID+"/credentials/"+issued.Credential.ID,
		"",
		1,
	)
	var originalCredential models.AgentCredential
	if err := db.First(&originalCredential, "id = ?", issued.Credential.ID).Error; err != nil {
		t.Fatal(err)
	}
	if originalCredential.Status != models.AgentCredentialStatusActive || originalCredential.RevokedAt != nil {
		t.Fatalf("credential revocation was not rolled back: %+v", originalCredential)
	}

	requestFailure(
		http.MethodPut,
		"/api/projects/TEST/admin/agents/service-principals/"+principal.ID+"/status",
		`{"read_only":true}`,
		1,
	)
	var unchangedPrincipal models.ServicePrincipal
	if err := db.First(&unchangedPrincipal, "id = ?", principal.ID).Error; err != nil {
		t.Fatal(err)
	}
	if unchangedPrincipal.ReadOnly {
		t.Fatal("service-principal controls changed despite event failure")
	}

	requestFailure(
		http.MethodPost,
		"/api/projects/TEST/admin/agents/service-principals/"+principal.ID+"/policies",
		`{"effect":"allow","scope":"tickets:read","action":"ticket.read"}`,
		1,
	)
	var policyCount int64
	if err := db.Model(&models.AgentPolicy{}).
		Where("service_principal_id = ?", principal.ID).
		Count(&policyCount).Error; err != nil {
		t.Fatal(err)
	}
	if policyCount != 0 {
		t.Fatalf("policy create was not rolled back: count=%d", policyCount)
	}
	policy, err := healthy.CreateAgentPolicy(context.Background(), services.CreateAgentPolicyInput{
		ServicePrincipalID: principal.ID,
		Name:               "existing rollback policy",
		Effect:             models.AgentPolicyEffectAllow,
		Scope:              models.ScopeTicketsRead,
		Action:             "ticket.read",
	})
	if err != nil {
		t.Fatal(err)
	}
	requestFailure(
		http.MethodDelete,
		"/api/projects/TEST/admin/agents/service-principals/"+principal.ID+"/policies/"+policy.ID,
		"",
		1,
	)
	var unchangedPolicy models.AgentPolicy
	if err := db.First(&unchangedPolicy, "id = ?", policy.ID).Error; err != nil {
		t.Fatal(err)
	}
	if !unchangedPolicy.IsActive {
		t.Fatal("policy disable was not rolled back")
	}

	if control.ReadOnly() || control.EmergencyStop() {
		t.Fatalf("runtime memory changed before rollback: read_only=%v emergency=%v", control.ReadOnly(), control.EmergencyStop())
	}
	var controlRows []models.SystemConfig
	if err := db.
		Where("key IN ?", []string{agentReadOnlyConfigKey, agentEmergencyConfigKey}).
		Order("key ASC").
		Find(&controlRows).Error; err != nil {
		t.Fatal(err)
	}
	if len(controlRows) != 2 {
		t.Fatalf(
			"runtime control bootstrap rows = %d, want 2",
			len(controlRows),
		)
	}
	for _, row := range controlRows {
		if row.Value != "false" ||
			row.ValueType != "bool" ||
			!row.IsActive ||
			row.Version != 1 ||
			row.UpdatedBy != nil {
			t.Fatalf(
				"administrator rollback changed safety baseline: %+v",
				row,
			)
		}
	}

	ticket := models.Ticket{
		OrganizationID:     projectFixture.organization.ID,
		ProjectID:          projectFixture.project.ID,
		QueueID:            projectFixture.queue.ID,
		TicketNumber:       "ADMIN-ROLLBACK-1",
		Title:              "Rollback test",
		Description:        "Safe test data",
		Type:               models.TicketTypeRequest,
		Priority:           models.TicketPriorityNormal,
		Status:             models.TicketStatusOpen,
		Source:             models.TicketSourceAgent,
		Version:            1,
		TrustLevel:         models.TicketTrustLevelSystem,
		CreatedByID:        &admin.ID,
		CreatedByActorType: models.ActorTypeHuman,
		CreatedByActorID:   strconv.FormatUint(uint64(admin.ID), 10),
	}
	if err := db.Create(&ticket).Error; err != nil {
		t.Fatal(err)
	}
	attachment := models.TicketAttachment{
		TicketID:     ticket.ID,
		UploadedBy:   &admin.ID,
		ActorType:    models.ActorTypeHuman,
		ActorID:      strconv.FormatUint(uint64(admin.ID), 10),
		FileName:     "rollback.txt",
		OriginalName: "rollback.txt",
		FileSize:     4,
		MimeType:     "text/plain",
		StoragePath:  "test/rollback.txt",
		VirusScan:    models.VirusScanPending,
	}
	if err := db.Create(&attachment).Error; err != nil {
		t.Fatal(err)
	}
	requestFailure(
		http.MethodPost,
		fmt.Sprintf("/api/projects/TEST/admin/agents/attachments/%d/scan", attachment.ID),
		`{"status":"clean","details":"must roll back"}`,
		1,
	)
	var unchangedAttachment models.TicketAttachment
	if err := db.First(&unchangedAttachment, attachment.ID).Error; err != nil {
		t.Fatal(err)
	}
	if unchangedAttachment.VirusScan != models.VirusScanPending ||
		unchangedAttachment.ScanDetails != "" ||
		unchangedAttachment.ScannedAt != nil {
		t.Fatalf("attachment scan was not rolled back: %+v", unchangedAttachment)
	}

	publishedAt := time.Now().UTC().Add(-time.Minute)
	event, err := appendTestDomainEvent(context.Background(), healthy, services.DomainEventInput{
		Type:            "io.chronodesk.test.rollback.v1",
		Subject:         "rollback/source",
		Actor:           models.SystemActor("rollback-test"),
		Scope:           projectFixture.project.Scope(),
		ResourceVersion: 1,
		Data:            gin.H{"test": true},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&models.DomainEvent{}).
		Where("id = ?", event.ID).
		Update("published_at", &publishedAt).Error; err != nil {
		t.Fatal(err)
	}
	var delivery models.OutboxDelivery
	if err := db.First(&delivery, "event_id = ?", event.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&delivery).Updates(map[string]any{
		"status":     models.OutboxDeliveryFailed,
		"attempts":   3,
		"last_error": "previous failure",
	}).Error; err != nil {
		t.Fatal(err)
	}
	requestFailure(
		http.MethodPost,
		"/api/projects/TEST/admin/agents/outbox/"+delivery.ID+"/replay",
		"",
		1,
	)
	var unchangedDelivery models.OutboxDelivery
	if err := db.First(&unchangedDelivery, "id = ?", delivery.ID).Error; err != nil {
		t.Fatal(err)
	}
	if unchangedDelivery.Status != models.OutboxDeliveryFailed ||
		unchangedDelivery.Attempts != 3 ||
		unchangedDelivery.LastError != "previous failure" {
		t.Fatalf("outbox replay was not rolled back: %+v", unchangedDelivery)
	}
	var unchangedEvent models.DomainEvent
	if err := db.First(&unchangedEvent, "id = ?", event.ID).Error; err != nil {
		t.Fatal(err)
	}
	if unchangedEvent.PublishedAt == nil {
		t.Fatal("outbox replay publication reset survived rollback")
	}

	var eventCount int64
	if err := db.Model(&models.DomainEvent{}).Count(&eventCount).Error; err != nil {
		t.Fatal(err)
	}
	if eventCount != 1 {
		t.Fatalf("failed administrator mutations left domain events: count=%d", eventCount)
	}
	var failedIdempotency int64
	if err := db.Model(&models.IdempotencyRecord{}).
		Where("state = ?", models.IdempotencyStateFailed).
		Count(&failedIdempotency).Error; err != nil {
		t.Fatal(err)
	}
	if failedIdempotency != int64(failedRequestCounter) {
		t.Fatalf(
			"failed mutations did not release idempotency reservations: got=%d want=%d",
			failedIdempotency,
			failedRequestCounter,
		)
	}
	var versionRows int64
	if err := db.Model(&models.SystemConfig{}).
		Where(map[string]any{"group": "agent-resource-version"}).
		Count(&versionRows).Error; err != nil {
		t.Fatal(err)
	}
	if versionRows != 0 {
		t.Fatalf("failed mutations left %d resource-version rows", versionRows)
	}
}
