package agentplatform

import (
	"encoding/json"
	"net/http"
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
