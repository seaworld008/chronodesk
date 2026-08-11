package agentplatform

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/seaworld008/chronodesk/server/internal/handlers"
	"github.com/seaworld008/chronodesk/server/internal/httpcontract"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/services"
	"gorm.io/datatypes"
)

func TestWebhookEmergencyRevokeRejectsVersionBeforeOrdinaryUpdate(
	t *testing.T,
) {
	fixture := newAdminContractFixture(t)
	config := seedAdminWebhookCASConfig(t, fixture, "stale-update")
	router := newAdminWebhookEmergencyCASRouter(t, fixture)
	path := "/api/projects/TEST/webhooks/" + uintString(config.ID)
	initial := getAdminWebhookResourceVersion(t, router, path)
	update := performAdminWebhookCASRequest(
		router,
		http.MethodPut,
		path,
		`{"description":"ordinary update advanced the version"}`,
		"webhook-cas-put",
	)
	if update.Code != http.StatusOK {
		t.Fatalf("ordinary update status=%d body=%s", update.Code, update.Body.String())
	}

	stale := performAdminContractRequest(
		router,
		http.MethodPost,
		"/api/projects/TEST/admin/agents/webhooks/"+
			uintString(config.ID)+"/emergency-revoke",
		"",
		"webhook-cas-stale-after-put",
		httpcontract.FormatETag(initial),
		"webhook-cas-stale-after-put",
	)
	if stale.Code != http.StatusConflict ||
		stale.Header().Get("ETag") != httpcontract.FormatETag(initial+1) ||
		!strings.Contains(stale.Body.String(), ProblemVersionConflict) {
		t.Fatalf(
			"stale revoke after PUT status=%d ETag=%q body=%s",
			stale.Code,
			stale.Header().Get("ETag"),
			stale.Body.String(),
		)
	}
}

func TestWebhookEmergencyRevokeRejectsVersionBeforeOrdinaryDelete(
	t *testing.T,
) {
	fixture := newAdminContractFixture(t)
	config := seedAdminWebhookCASConfig(t, fixture, "stale-delete")
	router := newAdminWebhookEmergencyCASRouter(t, fixture)
	path := "/api/projects/TEST/webhooks/" + uintString(config.ID)
	initial := getAdminWebhookResourceVersion(t, router, path)
	deleted := performAdminWebhookCASRequest(
		router,
		http.MethodDelete,
		path,
		"",
		"webhook-cas-delete",
	)
	if deleted.Code != http.StatusOK {
		t.Fatalf(
			"ordinary delete status=%d body=%s",
			deleted.Code,
			deleted.Body.String(),
		)
	}

	stale := performAdminContractRequest(
		router,
		http.MethodPost,
		"/api/projects/TEST/admin/agents/webhooks/"+
			uintString(config.ID)+"/emergency-revoke",
		"",
		"webhook-cas-stale-after-delete",
		httpcontract.FormatETag(initial),
		"webhook-cas-stale-after-delete",
	)
	if stale.Code != http.StatusConflict ||
		stale.Header().Get("ETag") != httpcontract.FormatETag(initial+1) ||
		!strings.Contains(stale.Body.String(), ProblemVersionConflict) {
		t.Fatalf(
			"stale revoke after delete status=%d ETag=%q body=%s",
			stale.Code,
			stale.Header().Get("ETag"),
			stale.Body.String(),
		)
	}
}

func TestWebhookEmergencyRevokeUsesCurrentConfigVersionAndExactReplay(
	t *testing.T,
) {
	fixture := newAdminContractFixture(t)
	config := seedAdminWebhookCASConfig(t, fixture, "current-version")
	router := newAdminWebhookEmergencyCASRouter(t, fixture)
	path := "/api/projects/TEST/webhooks/" + uintString(config.ID)
	initial := getAdminWebhookResourceVersion(t, router, path)
	update := performAdminWebhookCASRequest(
		router,
		http.MethodPut,
		path,
		`{"description":"advance before legal revoke"}`,
		"webhook-cas-current-put",
	)
	if update.Code != http.StatusOK {
		t.Fatalf("ordinary update status=%d body=%s", update.Code, update.Body.String())
	}
	current := getAdminWebhookResourceVersion(t, router, path)
	if current != initial+1 {
		t.Fatalf("resource version after PUT = %d, want %d", current, initial+1)
	}

	revokePath := "/api/projects/TEST/admin/agents/webhooks/" +
		uintString(config.ID) + "/emergency-revoke"
	const idempotencyKey = "webhook-cas-current-exact-replay"
	first := performAdminContractRequest(
		router,
		http.MethodPost,
		revokePath,
		"",
		idempotencyKey,
		httpcontract.FormatETag(current),
		"webhook-cas-current-first",
	)
	if first.Code != http.StatusOK ||
		first.Header().Get("ETag") != httpcontract.FormatETag(current+1) {
		t.Fatalf(
			"current revoke status=%d ETag=%q body=%s",
			first.Code,
			first.Header().Get("ETag"),
			first.Body.String(),
		)
	}
	replay := performAdminContractRequest(
		router,
		http.MethodPost,
		revokePath,
		"",
		idempotencyKey,
		httpcontract.FormatETag(current),
		"webhook-cas-current-replay",
	)
	if replay.Code != first.Code ||
		replay.Header().Get("ETag") != first.Header().Get("ETag") ||
		replay.Body.String() != first.Body.String() {
		t.Fatalf(
			"current exact replay drifted first=%d/%q/%s replay=%d/%q/%s",
			first.Code,
			first.Header().Get("ETag"),
			first.Body.String(),
			replay.Code,
			replay.Header().Get("ETag"),
			replay.Body.String(),
		)
	}
}

func TestAdminWebhookEmergencyRevokeContractAndExactReplay(
	t *testing.T,
) {
	fixture := newAdminContractFixture(t)
	seeded := seedAdminWebhookEmergencyRevoke(t, fixture)
	router := newAdminWebhookEmergencyContractRouter(t, fixture)
	path := "/api/projects/TEST/admin/agents/webhooks/" +
		uintString(seeded.config.ID) +
		"/emergency-revoke"

	missingKey := performAdminContractRequest(
		router,
		http.MethodPost,
		path,
		"",
		"",
		httpcontract.FormatETag(1),
		"webhook-revoke-missing-key",
	)
	if missingKey.Code != http.StatusBadRequest ||
		!strings.Contains(missingKey.Body.String(), "Idempotency-Key") {
		t.Fatalf(
			"missing key status=%d body=%s",
			missingKey.Code,
			missingKey.Body.String(),
		)
	}
	missingVersion := performAdminContractRequest(
		router,
		http.MethodPost,
		path,
		"",
		"webhook-emergency-missing-version",
		"",
		"webhook-revoke-missing-version",
	)
	if missingVersion.Code != http.StatusPreconditionRequired ||
		!strings.Contains(
			missingVersion.Body.String(),
			"precondition_required",
		) {
		t.Fatalf(
			"missing version status=%d body=%s",
			missingVersion.Code,
			missingVersion.Body.String(),
		)
	}

	const idempotencyKey = "webhook-emergency-exact-replay"
	first := performAdminContractRequest(
		router,
		http.MethodPost,
		path,
		"",
		idempotencyKey,
		httpcontract.FormatETag(1),
		"webhook-revoke-first",
	)
	if first.Code != http.StatusOK {
		t.Fatalf(
			"first revoke status=%d body=%s",
			first.Code,
			first.Body.String(),
		)
	}
	var envelope adminWriteEnvelope
	if err := json.Unmarshal(first.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Receipt == nil ||
		envelope.Receipt.ResourceID != uintString(seeded.config.ID) ||
		envelope.Receipt.ResourceVersion != 2 ||
		first.Header().Get("ETag") != httpcontract.FormatETag(2) {
		t.Fatalf(
			"revoke receipt=%+v ETag=%q",
			envelope.Receipt,
			first.Header().Get("ETag"),
		)
	}
	var outcome services.WebhookEmergencyRevokeResult
	if err := json.Unmarshal(envelope.Data, &outcome); err != nil {
		t.Fatal(err)
	}
	if outcome.ConfigID != seeded.config.ID ||
		outcome.Status != models.WebhookStatusDisabled ||
		outcome.ExpiredDeliveries != 1 ||
		outcome.InFlightDeliveries != 0 ||
		outcome.ShreddedSnapshots != 1 ||
		outcome.CredentialShredReason != "revoked" {
		t.Fatalf("revoke outcome = %+v", outcome)
	}
	assertAdminWebhookEmergencyOutputSafe(t, first.Body.String())

	var event models.DomainEvent
	if err := fixture.db.First(
		&event,
		"id = ?",
		envelope.Receipt.EventID,
	).Error; err != nil {
		t.Fatal(err)
	}
	if event.Type !=
		"io.chronodesk.admin.webhook.emergency_revoked.v1" ||
		event.Subject != services.WebhookAdminSubject(seeded.config.ID) ||
		event.PublishedAt != nil {
		t.Fatalf("emergency revoke event = %+v", event)
	}
	assertAdminWebhookEmergencyOutputSafe(t, string(event.Data))

	replay := performAdminContractRequest(
		router,
		http.MethodPost,
		path,
		"",
		idempotencyKey,
		httpcontract.FormatETag(1),
		"webhook-revoke-replay",
	)
	if replay.Code != first.Code ||
		replay.Body.String() != first.Body.String() ||
		replay.Header().Get("ETag") != first.Header().Get("ETag") {
		t.Fatalf(
			"exact replay drifted first=%d/%q/%s replay=%d/%q/%s",
			first.Code,
			first.Header().Get("ETag"),
			first.Body.String(),
			replay.Code,
			replay.Header().Get("ETag"),
			replay.Body.String(),
		)
	}

	stale := performAdminContractRequest(
		router,
		http.MethodPost,
		path,
		"",
		"webhook-emergency-stale-version",
		httpcontract.FormatETag(1),
		"webhook-revoke-stale",
	)
	if stale.Code != http.StatusConflict ||
		stale.Header().Get("ETag") != httpcontract.FormatETag(2) ||
		!strings.Contains(stale.Body.String(), ProblemVersionConflict) {
		t.Fatalf(
			"stale revoke status=%d ETag=%q body=%s",
			stale.Code,
			stale.Header().Get("ETag"),
			stale.Body.String(),
		)
	}

	var eventCount int64
	if err := fixture.db.Model(&models.DomainEvent{}).
		Where(
			"subject = ? AND type = ?",
			services.WebhookAdminSubject(seeded.config.ID),
			"io.chronodesk.admin.webhook.emergency_revoked.v1",
		).
		Count(&eventCount).Error; err != nil {
		t.Fatal(err)
	}
	if eventCount != 1 {
		t.Fatalf("emergency revoke event count = %d, want 1", eventCount)
	}
}

func newAdminWebhookEmergencyCASRouter(
	t *testing.T,
	fixture *adminContractFixture,
) *gin.Engine {
	t.Helper()
	projects, err := services.NewProjectService(fixture.db)
	if err != nil {
		t.Fatal(err)
	}
	admin := NewAdminHandler(
		fixture.db,
		fixture.native,
		newTestRuntimeControl(
			t,
			fixture.db,
			fixture.native,
			false,
		),
		time.Hour,
		[]byte("webhook-emergency-cas-replay-encryption-key"),
	)
	webhooks := handlers.NewWebhookHandlerWithProtector(
		fixture.db,
		nil,
	)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("user_id", fixture.admin.ID)
		c.Set("platform_role", models.PlatformRolePlatformAdmin)
		c.Set("request_id", c.GetHeader("X-Request-ID"))
		c.Next()
	})
	project := router.Group("/api/projects/:projectKey")
	project.Use(handlers.ProjectScopeMiddleware(projects, fixture.db))
	project.GET("/webhooks/:id", webhooks.GetWebhook)
	project.PUT("/webhooks/:id", webhooks.UpdateWebhook)
	project.DELETE("/webhooks/:id", webhooks.DeleteWebhook)
	adminGroup := project.Group("/admin/agents")
	adminGroup.Use(handlers.RequireProjectRoles(models.ProjectRoleAdmin))
	admin.RegisterRoutes(adminGroup)
	return router
}

func seedAdminWebhookCASConfig(
	t *testing.T,
	fixture *adminContractFixture,
	suffix string,
) models.WebhookConfig {
	t.Helper()
	config := models.WebhookConfig{
		OrganizationID: fixture.scope.OrganizationID,
		ProjectID:      fixture.scope.ProjectID,
		Name:           "Webhook CAS " + suffix,
		Description:    "safe test configuration",
		Provider:       models.WebhookProviderCustom,
		WebhookURL:     "https://cas.invalid.example/" + suffix,
		Status:         models.WebhookStatusActive,
		EnabledEventsObj: []models.WebhookEventType{
			models.WebhookEventTicketCreated,
		},
		RetryCount:     3,
		RetryInterval:  60,
		TimeoutSeconds: 30,
		RateLimit:      60,
		CreatedBy:      fixture.admin.ID,
	}
	if err := fixture.db.Create(&config).Error; err != nil {
		t.Fatal(err)
	}
	return config
}

func getAdminWebhookResourceVersion(
	t *testing.T,
	router *gin.Engine,
	path string,
) uint64 {
	t.Helper()
	response := performAdminWebhookCASRequest(
		router,
		http.MethodGet,
		path,
		"",
		"webhook-cas-get",
	)
	if response.Code != http.StatusOK {
		t.Fatalf("Webhook GET status=%d body=%s", response.Code, response.Body.String())
	}
	var envelope struct {
		Data handlers.WebhookConfigResponse `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Data.ResourceVersion == 0 {
		t.Fatalf("Webhook GET omitted resource_version: %s", response.Body.String())
	}
	return envelope.Data.ResourceVersion
}

func performAdminWebhookCASRequest(
	router *gin.Engine,
	method string,
	path string,
	body string,
	requestID string,
) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Request-ID", requestID)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

func newAdminWebhookEmergencyContractRouter(
	t *testing.T,
	fixture *adminContractFixture,
) *gin.Engine {
	t.Helper()
	projects, err := services.NewProjectService(fixture.db)
	if err != nil {
		t.Fatal(err)
	}
	handler := NewAdminHandler(
		fixture.db,
		fixture.native,
		newTestRuntimeControl(
			t,
			fixture.db,
			fixture.native,
			false,
		),
		time.Hour,
		[]byte("webhook-emergency-replay-encryption-key"),
	)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("user_id", fixture.admin.ID)
		c.Set("platform_role", models.PlatformRolePlatformAdmin)
		c.Set("request_id", c.GetHeader("X-Request-ID"))
		c.Next()
	})
	group := router.Group("/api/projects/:projectKey/admin/agents")
	group.Use(handlers.ProjectScopeMiddleware(projects, fixture.db))
	group.Use(handlers.RequireProjectRoles(models.ProjectRoleAdmin))
	handler.RegisterRoutes(group)
	return router
}

type adminWebhookEmergencySeed struct {
	config   models.WebhookConfig
	event    models.DomainEvent
	delivery models.OutboxDelivery
	snapshot models.WebhookDeliverySnapshot
}

func seedAdminWebhookEmergencyRevoke(
	t *testing.T,
	fixture *adminContractFixture,
) adminWebhookEmergencySeed {
	t.Helper()
	now := time.Now().UTC()
	config := models.WebhookConfig{
		OrganizationID: fixture.scope.OrganizationID,
		ProjectID:      fixture.scope.ProjectID,
		Name:           "Emergency revoke",
		Provider:       models.WebhookProviderCustom,
		WebhookURL:     "https://must-not-leak.invalid.example/emergency",
		Status:         models.WebhookStatusActive,
		Secret:         "sealed-current-must-not-leak",
		PreviousSecret: "sealed-previous-must-not-leak",
		AccessToken:    "sealed-access-must-not-leak",
		EnabledEventsObj: []models.WebhookEventType{
			models.WebhookEventTicketCreated,
		},
		RetryCount:    3,
		RetryInterval: 60,
		CreatedBy:     fixture.admin.ID,
	}
	if err := fixture.db.Create(&config).Error; err != nil {
		t.Fatal(err)
	}
	event := models.DomainEvent{
		ID:              "00000000-0000-7000-8000-000000009201",
		OrganizationID:  fixture.scope.OrganizationID,
		ProjectID:       fixture.scope.ProjectID,
		SpecVersion:     "1.0",
		Source:          "urn:chronodesk:test:webhook-emergency",
		Type:            "io.chronodesk.ticket.created.v1",
		Subject:         "ticket/emergency",
		Time:            now,
		DataContentType: "application/json",
		Data:            datatypes.JSON(`{"safe":true}`),
		ActorType:       models.ActorTypeSystem,
		ActorID:         "webhook-emergency-test",
		ResourceVersion: 1,
	}
	if err := fixture.db.Create(&event).Error; err != nil {
		t.Fatal(err)
	}
	deadline := now.Add(time.Hour)
	snapshot, err := models.NewWebhookDeliverySnapshot(
		config,
		event.ID,
		deadline,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.Create(snapshot).Error; err != nil {
		t.Fatal(err)
	}
	destinationID, err :=
		models.WebhookDeliverySnapshotDestinationID(snapshot.ID)
	if err != nil {
		t.Fatal(err)
	}
	delivery := models.OutboxDelivery{
		ID:              "00000000-0000-7000-8000-000000009202",
		OrganizationID:  fixture.scope.OrganizationID,
		ProjectID:       fixture.scope.ProjectID,
		EventID:         event.ID,
		DestinationType: "webhook",
		DestinationID:   destinationID,
		Status:          models.OutboxDeliveryPending,
		MaxAttempts:     4,
		NextAttemptAt:   now,
		ExpiresAt:       &deadline,
	}
	if err := fixture.db.Create(&delivery).Error; err != nil {
		t.Fatal(err)
	}
	return adminWebhookEmergencySeed{
		config:   config,
		event:    event,
		delivery: delivery,
		snapshot: *snapshot,
	}
}

func assertAdminWebhookEmergencyOutputSafe(t *testing.T, value string) {
	t.Helper()
	for _, forbidden := range []string{
		"must-not-leak.invalid.example",
		"sealed-current-must-not-leak",
		"sealed-previous-must-not-leak",
		"sealed-access-must-not-leak",
	} {
		if strings.Contains(value, forbidden) {
			t.Fatalf("emergency revoke output leaked %q: %s", forbidden, value)
		}
	}
}

func uintString(value uint) string {
	return strconv.FormatUint(uint64(value), 10)
}
