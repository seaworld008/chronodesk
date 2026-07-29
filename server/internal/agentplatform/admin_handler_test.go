package agentplatform

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/services"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestRuntimeSafetyControlsSurviveRestart(t *testing.T) {
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.SystemConfig{}); err != nil {
		t.Fatal(err)
	}
	first := NewRuntimeControl(nil, false, db)
	if err := first.PersistReadOnly(context.Background(), true, 0); err != nil {
		t.Fatal(err)
	}
	if err := first.PersistEmergencyStop(context.Background(), true, 0); err != nil {
		t.Fatal(err)
	}

	restarted := NewRuntimeControl(nil, false, db)
	if !restarted.ReadOnly() || !restarted.EmergencyStop() {
		t.Fatalf(
			"persisted controls were lost: read_only=%v emergency=%v",
			restarted.ReadOnly(),
			restarted.EmergencyStop(),
		)
	}
}

type adminWriteEnvelope struct {
	Data    json.RawMessage `json:"data"`
	Receipt *Receipt        `json:"receipt"`
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
	); err != nil {
		t.Fatal(err)
	}
	admin := models.User{
		Username:     "control-admin",
		Email:        "control-admin@example.com",
		PasswordHash: "not-a-real-password",
		Role:         models.RoleAdmin,
		Status:       models.UserStatusActive,
	}
	if err := db.Create(&admin).Error; err != nil {
		t.Fatal(err)
	}

	native := services.NewAgentNativeService(db, services.AgentNativeOptions{
		CredentialPepper: []byte("admin-handler-test-pepper"),
	})
	control := NewRuntimeControl(native, false, db)
	handler := NewAdminHandler(
		db,
		native,
		control,
		time.Hour,
		0,
		[]byte("admin-handler-stable-replay-encryption-key"),
	)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("user_id", admin.ID)
		c.Set("user_role", "admin")
		c.Set("request_id", "req-"+strings.ReplaceAll(c.FullPath(), "/", "-"))
		c.Next()
	})
	group := router.Group("/api/v1/admin")
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
			request.Header.Set("If-Match", FormatETag(expectedVersion))
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
		if recorder.Header().Get("ETag") != FormatETag(receipt.ResourceVersion) {
			tt.Fatalf(
				"%s %s ETag=%q, want %q",
				method,
				path,
				recorder.Header().Get("ETag"),
				FormatETag(receipt.ResourceVersion),
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

	t.Run("global read-only", func(t *testing.T) {
		doWrite(t, http.MethodPut, "/api/v1/admin/agent-control/read-only", `{"enabled":true}`, http.StatusOK, 1, true)
	})
	t.Run("global emergency stop", func(t *testing.T) {
		doWrite(t, http.MethodPut, "/api/v1/admin/agent-control/emergency-stop", `{"enabled":false}`, http.StatusOK, 1, true)
	})

	var principalID string
	var initialSecret string
	t.Run("principal create", func(t *testing.T) {
		recorder, envelope, _ := doWrite(
			t,
			http.MethodPost,
			"/api/v1/admin/service-principals",
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
		}
		if err := json.Unmarshal(envelope.Data, &data); err != nil {
			t.Fatal(err)
		}
		if data.ClientID == "" || data.ClientSecret == "" {
			t.Fatalf("missing issued credential: %s", envelope.Data)
		}
		principalID, initialSecret = data.ClientID, data.ClientSecret
	})
	t.Run("principal status", func(t *testing.T) {
		doWrite(
			t,
			http.MethodPut,
			"/api/v1/admin/service-principals/"+principalID+"/status",
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
			"/api/v1/admin/service-principals/"+principalID+"/credentials/rotate",
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
			"/api/v1/admin/service-principals/"+principalID+"/credentials/"+rotatedCredentialID,
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
			"/api/v1/admin/service-principals/"+principalID+"/policies",
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
			"/api/v1/admin/service-principals/"+principalID+"/policies/"+policyID,
			"",
			http.StatusOK,
			1,
			true,
		)
	})

	ticket := models.Ticket{
		TicketNumber:       "ADMIN-CONTROL-1",
		Title:              "Admin control test",
		Description:        "Safe test ticket",
		Type:               models.TicketTypeRequest,
		Priority:           models.TicketPriorityNormal,
		Status:             models.TicketStatusOpen,
		Source:             models.TicketSourceAgent,
		Version:            1,
		TrustLevel:         models.TicketTrustLevelSystem,
		CreatedByID:        admin.ID,
		CreatedByActorType: models.ActorTypeHuman,
		CreatedByActorID:   strconv.FormatUint(uint64(admin.ID), 10),
	}
	if err := db.Create(&ticket).Error; err != nil {
		t.Fatal(err)
	}
	attachment := models.TicketAttachment{
		TicketID:     ticket.ID,
		UploadedBy:   admin.ID,
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
			fmt.Sprintf("/api/v1/admin/attachments/%d/scan", attachment.ID),
			`{"status":"clean","details":"scanner verified"}`,
			http.StatusOK,
			1,
			true,
		)
	})

	var replayDelivery models.OutboxDelivery
	if err := db.Order("created_at ASC").First(&replayDelivery).Error; err != nil {
		t.Fatal(err)
	}
	t.Run("outbox replay", func(t *testing.T) {
		doWrite(
			t,
			http.MethodPost,
			"/api/v1/admin/outbox/"+replayDelivery.ID+"/replay",
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
			"/api/v1/admin/leases/"+lease.ID+"/force-release",
			"",
			http.StatusOK,
			1,
			true,
		)
		if event.Type != "io.chronodesk.admin.ticket.lease.force_released.v1" {
			t.Fatalf("force release did not record administrator event: %q", event.Type)
		}
	})

	overviewRecorder := httptest.NewRecorder()
	overviewRequest := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/admin/agent-control/overview",
		nil,
	)
	router.ServeHTTP(overviewRecorder, overviewRequest)
	if overviewRecorder.Code != http.StatusOK {
		t.Fatalf("overview status=%d body=%s", overviewRecorder.Code, overviewRecorder.Body.String())
	}
	var overviewEnvelope struct {
		Data struct {
			GlobalReadOnlyVersion uint64 `json:"global_read_only_version"`
			EmergencyStopVersion  uint64 `json:"emergency_stop_version"`
			Principals            []struct {
				ID              string `json:"id"`
				ResourceVersion uint64 `json:"resource_version"`
			} `json:"principals"`
			Outbox []struct {
				ID              string `json:"id"`
				ResourceVersion uint64 `json:"resource_version"`
			} `json:"outbox"`
			Attachments []struct {
				ID              uint   `json:"id"`
				ResourceVersion uint64 `json:"resource_version"`
			} `json:"attachments"`
		} `json:"data"`
	}
	if err := json.Unmarshal(overviewRecorder.Body.Bytes(), &overviewEnvelope); err != nil {
		t.Fatal(err)
	}
	if overviewEnvelope.Data.GlobalReadOnlyVersion != 2 ||
		overviewEnvelope.Data.EmergencyStopVersion != 2 {
		t.Fatalf("control versions missing from overview: %+v", overviewEnvelope.Data)
	}
	assertVersion := func(name string, want uint64, found bool, got uint64) {
		t.Helper()
		if !found || got != want {
			t.Fatalf("%s resource version found=%v got=%d want=%d", name, found, got, want)
		}
	}
	var principalFound, deliveryFound, attachmentFound bool
	var principalVersion, deliveryVersion, attachmentVersion uint64
	for _, row := range overviewEnvelope.Data.Principals {
		if row.ID == principalID {
			principalFound, principalVersion = true, row.ResourceVersion
		}
	}
	for _, row := range overviewEnvelope.Data.Outbox {
		if row.ID == replayDelivery.ID {
			deliveryFound, deliveryVersion = true, row.ResourceVersion
		}
	}
	for _, row := range overviewEnvelope.Data.Attachments {
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
		"/api/v1/admin/service-principals/"+principalID+"/policies",
		nil,
	)
	router.ServeHTTP(policyRecorder, policyRequest)
	if policyRecorder.Code != http.StatusOK {
		t.Fatalf("policy list status=%d body=%s", policyRecorder.Code, policyRecorder.Body.String())
	}
	var policyEnvelope struct {
		Data []struct {
			ID              string `json:"id"`
			ResourceVersion uint64 `json:"resource_version"`
		} `json:"data"`
	}
	if err := json.Unmarshal(policyRecorder.Body.Bytes(), &policyEnvelope); err != nil {
		t.Fatal(err)
	}
	var listedPolicy bool
	var listedPolicyVersion uint64
	for _, row := range policyEnvelope.Data {
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
}

type adminContractFixture struct {
	db     *gorm.DB
	native *services.AgentNativeService
	router *gin.Engine
	admin  models.User
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
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	if err := db.AutoMigrate(
		&models.User{},
		&models.SystemConfig{},
		&models.ServicePrincipal{},
		&models.AgentCredential{},
		&models.AgentPolicy{},
		&models.DomainEvent{},
		&models.OutboxDelivery{},
		&models.IdempotencyRecord{},
		&models.TicketLease{},
		&models.TicketAttachment{},
	); err != nil {
		t.Fatal(err)
	}
	admin := models.User{
		Username:     "contract-admin",
		Email:        "contract-admin@example.com",
		PasswordHash: "not-a-real-password",
		Role:         models.RoleAdmin,
		Status:       models.UserStatusActive,
	}
	if err := db.Create(&admin).Error; err != nil {
		t.Fatal(err)
	}
	native := services.NewAgentNativeService(db, services.AgentNativeOptions{
		CredentialPepper: []byte("admin-contract-credential-pepper"),
	})
	router := newAdminContractRouter(
		db,
		native,
		admin,
		[]byte("admin-contract-stable-replay-encryption-key"),
	)
	return &adminContractFixture{db: db, native: native, router: router, admin: admin}
}

func newAdminContractRouter(
	db *gorm.DB,
	native *services.AgentNativeService,
	admin models.User,
	replayKey []byte,
) *gin.Engine {
	handler := NewAdminHandler(
		db,
		native,
		NewRuntimeControl(native, false, db),
		time.Hour,
		0,
		replayKey,
	)
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
	handler.RegisterRoutes(router.Group("/api/v1/admin"))
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

func TestAdminCommandsEnforceHeadersAndEncryptedExactReplay(t *testing.T) {
	fixture := newAdminContractFixture(t)

	writeEndpoints := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{"read-only", http.MethodPut, "/api/v1/admin/agent-control/read-only", `{"enabled":true}`},
		{"emergency-stop", http.MethodPut, "/api/v1/admin/agent-control/emergency-stop", `{"enabled":true}`},
		{"principal-create", http.MethodPost, "/api/v1/admin/service-principals", `{"name":"missing-key-agent","scopes":["tickets:read"]}`},
		{"principal-status", http.MethodPut, "/api/v1/admin/service-principals/missing/status", `{"read_only":true}`},
		{"credential-rotate", http.MethodPost, "/api/v1/admin/service-principals/missing/credentials/rotate", ""},
		{"credential-revoke", http.MethodDelete, "/api/v1/admin/service-principals/missing/credentials/missing", ""},
		{"policy-create", http.MethodPost, "/api/v1/admin/service-principals/missing/policies", `{"effect":"allow","scope":"tickets:read"}`},
		{"policy-disable", http.MethodDelete, "/api/v1/admin/service-principals/missing/policies/missing", ""},
		{"lease-release", http.MethodPost, "/api/v1/admin/leases/missing/force-release", ""},
		{"attachment-scan", http.MethodPost, "/api/v1/admin/attachments/1/scan", `{"status":"clean"}`},
		{"outbox-replay", http.MethodPost, "/api/v1/admin/outbox/missing/replay", ""},
	}
	for _, endpoint := range writeEndpoints {
		t.Run("idempotency-required/"+endpoint.name, func(t *testing.T) {
			response := performAdminContractRequest(
				fixture.router,
				endpoint.method,
				endpoint.path,
				endpoint.body,
				"",
				FormatETag(1),
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
		"/api/v1/admin/service-principals",
		`{"name":"unknown-field-agent","scopes":["tickets:read"],"unexpected":true}`,
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
		"/api/v1/admin/service-principals",
		` { "scopes" : ["tickets:read", "tasks:manage"], "name" : "exact-replay-agent" } `,
		createKey,
		"",
		"first-create-request",
	)
	if first.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", first.Code, first.Body.String())
	}
	if first.Header().Get("ETag") != FormatETag(1) ||
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
		fixture.db,
		fixture.native,
		fixture.admin,
		[]byte("different-admin-replay-encryption-key"),
	)
	wrongKeyReplay := performAdminContractRequest(
		wrongKeyRouter,
		http.MethodPost,
		"/api/v1/admin/service-principals",
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
		fixture.db,
		fixture.native,
		fixture.admin,
		[]byte("admin-contract-stable-replay-encryption-key"),
	)
	replayed := performAdminContractRequest(
		restartedRouter,
		http.MethodPost,
		"/api/v1/admin/service-principals",
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
		"/api/v1/admin/service-principals",
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
	statusPath := "/api/v1/admin/service-principals/" + credentialData.ClientID + "/status"
	statusFirst := performAdminContractRequest(
		fixture.router,
		http.MethodPut,
		statusPath,
		`{"read_only":true}`,
		statusKey,
		FormatETag(1),
		"status-first",
	)
	if statusFirst.Code != http.StatusOK || statusFirst.Header().Get("ETag") != FormatETag(2) {
		t.Fatalf("status update code=%d headers=%v body=%s", statusFirst.Code, statusFirst.Header(), statusFirst.Body.String())
	}
	statusReplay := performAdminContractRequest(
		fixture.router,
		http.MethodPut,
		statusPath,
		`{"read_only":true}`,
		statusKey,
		FormatETag(1),
		"status-retry",
	)
	if statusReplay.Code != statusFirst.Code ||
		statusReplay.Body.String() != statusFirst.Body.String() ||
		statusReplay.Header().Get("ETag") != FormatETag(2) {
		t.Fatalf("status replay differs: first=%s replay=%s", statusFirst.Body.String(), statusReplay.Body.String())
	}
	stale := performAdminContractRequest(
		fixture.router,
		http.MethodPut,
		statusPath,
		`{"emergency_disabled":true}`,
		"stale-version-command",
		FormatETag(1),
		"status-stale",
	)
	if stale.Code != http.StatusConflict ||
		stale.Header().Get("ETag") != FormatETag(2) ||
		!strings.Contains(stale.Body.String(), ProblemVersionConflict) {
		t.Fatalf("stale command status=%d headers=%v body=%s", stale.Code, stale.Header(), stale.Body.String())
	}

	processingBody := []byte(`{"read_only":false}`)
	const processingKey = "administrator-command-processing"
	processingReservation, err := fixture.native.ReserveIdempotency(
		context.Background(),
		models.HumanActor(fixture.admin.ID),
		"admin:PUT:/api/v1/admin/service-principals/:id/status",
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
		FormatETag(2),
		"status-processing",
	)
	if processing.Code != http.StatusConflict ||
		!strings.Contains(processing.Body.String(), ProblemIdempotencyConflict) {
		t.Fatalf("processing command status=%d body=%s", processing.Code, processing.Body.String())
	}

	rotateKey := "credential-rotation-exact-replay"
	rotatePath := "/api/v1/admin/service-principals/" + credentialData.ClientID + "/credentials/rotate"
	rotated := performAdminContractRequest(
		fixture.router,
		http.MethodPost,
		rotatePath,
		"",
		rotateKey,
		FormatETag(2),
		"rotate-first",
	)
	if rotated.Code != http.StatusOK || rotated.Header().Get("ETag") != FormatETag(3) {
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
		FormatETag(2),
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
	path := "/api/v1/admin/service-principals/" + principal.ID + "/status"
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
				FormatETag(1),
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
			if result.etag != FormatETag(2) {
				t.Fatalf("successful CAS ETag=%q body=%s", result.etag, result.body)
			}
		case http.StatusConflict:
			conflicts++
			if result.etag != FormatETag(2) ||
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
		Role:         models.RoleAdmin,
		Status:       models.UserStatusActive,
	}
	if err := db.Create(&admin).Error; err != nil {
		t.Fatal(err)
	}
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
	control := NewRuntimeControl(failing, false, db)
	handler := NewAdminHandler(
		db,
		failing,
		control,
		time.Hour,
		0,
		[]byte("rollback-stable-replay-encryption-key"),
	)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("user_id", admin.ID)
		c.Set("user_role", "admin")
		c.Set("request_id", "rollback-request")
		c.Next()
	})
	handler.RegisterRoutes(router.Group("/api/v1/admin"))

	var failedRequestCounter int
	requestFailure := func(method, path, body string, expectedVersion uint64) {
		t.Helper()
		failedRequestCounter++
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(method, path, bytes.NewBufferString(body))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Idempotency-Key", fmt.Sprintf("rollback-key-%04d", failedRequestCounter))
		if expectedVersion > 0 {
			request.Header.Set("If-Match", FormatETag(expectedVersion))
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
		"/api/v1/admin/service-principals",
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
	issued, err := healthy.IssueCredential(context.Background(), principal.ID, "existing", time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	requestFailure(
		http.MethodPost,
		"/api/v1/admin/service-principals/"+principal.ID+"/credentials/rotate",
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
		"/api/v1/admin/service-principals/"+principal.ID+"/credentials/"+issued.Credential.ID,
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
		"/api/v1/admin/service-principals/"+principal.ID+"/status",
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
		"/api/v1/admin/service-principals/"+principal.ID+"/policies",
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
		"/api/v1/admin/service-principals/"+principal.ID+"/policies/"+policy.ID,
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

	requestFailure(
		http.MethodPut,
		"/api/v1/admin/agent-control/read-only",
		`{"enabled":true}`,
		1,
	)
	requestFailure(
		http.MethodPut,
		"/api/v1/admin/agent-control/emergency-stop",
		`{"enabled":true}`,
		1,
	)
	if control.ReadOnly() || control.EmergencyStop() {
		t.Fatalf("runtime memory changed before rollback: read_only=%v emergency=%v", control.ReadOnly(), control.EmergencyStop())
	}
	var controlRows int64
	if err := db.Model(&models.SystemConfig{}).
		Where("key IN ?", []string{agentReadOnlyConfigKey, agentEmergencyConfigKey}).
		Count(&controlRows).Error; err != nil {
		t.Fatal(err)
	}
	if controlRows != 0 {
		t.Fatalf("runtime control DB rows survived rollback: %d", controlRows)
	}

	ticket := models.Ticket{
		TicketNumber:       "ADMIN-ROLLBACK-1",
		Title:              "Rollback test",
		Description:        "Safe test data",
		Type:               models.TicketTypeRequest,
		Priority:           models.TicketPriorityNormal,
		Status:             models.TicketStatusOpen,
		Source:             models.TicketSourceAgent,
		Version:            1,
		TrustLevel:         models.TicketTrustLevelSystem,
		CreatedByID:        admin.ID,
		CreatedByActorType: models.ActorTypeHuman,
		CreatedByActorID:   strconv.FormatUint(uint64(admin.ID), 10),
	}
	if err := db.Create(&ticket).Error; err != nil {
		t.Fatal(err)
	}
	attachment := models.TicketAttachment{
		TicketID:     ticket.ID,
		UploadedBy:   admin.ID,
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
		fmt.Sprintf("/api/v1/admin/attachments/%d/scan", attachment.ID),
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
	event, err := healthy.CreateDomainEvent(context.Background(), services.DomainEventInput{
		Type:            "io.chronodesk.test.rollback.v1",
		Subject:         "rollback/source",
		Actor:           models.SystemActor("rollback-test"),
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
		"/api/v1/admin/outbox/"+delivery.ID+"/replay",
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
