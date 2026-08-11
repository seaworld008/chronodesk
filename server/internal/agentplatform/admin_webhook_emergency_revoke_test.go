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

func TestWebhookOrdinaryMutationsRequireCurrentIfMatch(t *testing.T) {
	for _, test := range []struct {
		name   string
		method string
		body   string
	}{
		{
			name:   "update",
			method: http.MethodPut,
			body:   `{"description":"must not bypass CAS"}`,
		},
		{
			name:   "delete",
			method: http.MethodDelete,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newAdminContractFixture(t)
			config := seedAdminWebhookCASConfig(
				t,
				fixture,
				"missing-if-match-"+test.name,
			)
			router := newAdminWebhookEmergencyCASRouter(t, fixture)
			response := performAdminWebhookCASRequest(
				router,
				test.method,
				"/api/projects/TEST/webhooks/"+uintString(config.ID),
				test.body,
				"webhook-cas-missing-if-match-"+test.name,
			)
			if response.Code != http.StatusPreconditionRequired ||
				!strings.Contains(
					response.Body.String(),
					"precondition_required",
				) {
				t.Fatalf(
					"%s without If-Match status=%d body=%s",
					test.method,
					response.Code,
					response.Body.String(),
				)
			}
		})
	}
}

func TestWebhookEmergencyRevokeTombstonePreflightAfterDelete(
	t *testing.T,
) {
	fixture := newAdminContractFixture(t)
	config := seedAdminWebhookCASConfig(t, fixture, "tombstone-preflight")
	router := newAdminWebhookEmergencyCASRouter(t, fixture)
	definitionPath := "/api/projects/TEST/webhooks/" + uintString(config.ID)
	initial := getAdminWebhookResourceVersion(t, router, definitionPath)

	deleted := performAdminWebhookCASRequestWithIfMatch(
		router,
		http.MethodDelete,
		definitionPath,
		"",
		"webhook-tombstone-delete",
		httpcontract.FormatETag(initial),
	)
	if deleted.Code != http.StatusOK {
		t.Fatalf(
			"ordinary delete status=%d body=%s",
			deleted.Code,
			deleted.Body.String(),
		)
	}

	preflightPath := "/api/projects/TEST/admin/agents/webhooks/" +
		uintString(config.ID) + "/emergency-revoke"
	preflight := performAdminWebhookCASRequest(
		router,
		http.MethodGet,
		preflightPath,
		"",
		"webhook-tombstone-preflight",
	)
	if preflight.Code != http.StatusOK ||
		preflight.Header().Get("ETag") !=
			httpcontract.FormatETag(initial+1) ||
		preflight.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf(
			"tombstone preflight status=%d ETag=%q cache=%q body=%s",
			preflight.Code,
			preflight.Header().Get("ETag"),
			preflight.Header().Get("Cache-Control"),
			preflight.Body.String(),
		)
	}
	var envelope struct {
		Data struct {
			ConfigID         uint   `json:"config_id"`
			Status           string `json:"status"`
			Deleted          bool   `json:"deleted"`
			EmergencyRevoked bool   `json:"emergency_revoked"`
			ResourceVersion  uint64 `json:"resource_version"`
		} `json:"data"`
	}
	if err := json.Unmarshal(preflight.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Data.ConfigID != config.ID ||
		envelope.Data.Status != string(models.WebhookStatusActive) ||
		!envelope.Data.Deleted ||
		envelope.Data.EmergencyRevoked ||
		envelope.Data.ResourceVersion != initial+1 {
		t.Fatalf("unexpected tombstone preflight: %+v", envelope.Data)
	}
	assertAdminWebhookEmergencyOutputSafe(
		t,
		preflight.Body.String()+preflight.Header().Get("ETag"),
	)
	if strings.Contains(
		preflight.Body.String(),
		"cas.invalid.example",
	) {
		t.Fatalf("tombstone preflight leaked URL: %s", preflight.Body.String())
	}
	listPath := "/api/projects/TEST/admin/agents/webhooks/tombstones" +
		"?page=1&page_size=25"
	listed := performAdminWebhookCASRequest(
		router,
		http.MethodGet,
		listPath,
		"",
		"webhook-tombstone-list",
	)
	if listed.Code != http.StatusOK ||
		listed.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf(
			"tombstone list status=%d cache=%q body=%s",
			listed.Code,
			listed.Header().Get("Cache-Control"),
			listed.Body.String(),
		)
	}
	var tombstones struct {
		Data services.WebhookEmergencyTombstonePage `json:"data"`
	}
	if err := json.Unmarshal(listed.Body.Bytes(), &tombstones); err != nil {
		t.Fatal(err)
	}
	if tombstones.Data.Total != 1 ||
		len(tombstones.Data.Items) != 1 ||
		tombstones.Data.Items[0].ConfigID != config.ID ||
		!tombstones.Data.Items[0].Deleted ||
		tombstones.Data.Items[0].EmergencyRevoked ||
		tombstones.Data.Items[0].ResourceVersion != initial+1 {
		t.Fatalf("unexpected tombstone list: %+v", tombstones.Data)
	}
	assertAdminWebhookEmergencyOutputSafe(t, listed.Body.String())

	revoked := performAdminContractRequest(
		router,
		http.MethodPost,
		preflightPath,
		"",
		"webhook-tombstone-revoke",
		preflight.Header().Get("ETag"),
		"webhook-tombstone-revoke",
	)
	if revoked.Code != http.StatusOK ||
		revoked.Header().Get("ETag") !=
			httpcontract.FormatETag(initial+2) {
		t.Fatalf(
			"tombstone revoke status=%d ETag=%q body=%s",
			revoked.Code,
			revoked.Header().Get("ETag"),
			revoked.Body.String(),
		)
	}
	terminal := performAdminWebhookCASRequest(
		router,
		http.MethodGet,
		listPath,
		"",
		"webhook-tombstone-list-terminal",
	)
	if terminal.Code != http.StatusOK {
		t.Fatalf(
			"terminal tombstone list status=%d body=%s",
			terminal.Code,
			terminal.Body.String(),
		)
	}
	if err := json.Unmarshal(terminal.Body.Bytes(), &tombstones); err != nil {
		t.Fatal(err)
	}
	if len(tombstones.Data.Items) != 1 ||
		!tombstones.Data.Items[0].EmergencyRevoked ||
		tombstones.Data.Items[0].ResourceVersion != initial+2 {
		t.Fatalf("terminal tombstone list = %+v", tombstones.Data)
	}
}

func TestWebhookEmergencyTombstonesAreBoundedScopedAndExact(
	t *testing.T,
) {
	fixture := newAdminContractFixture(t)
	router := newAdminWebhookEmergencyCASRouter(t, fixture)
	base := time.Date(
		2026,
		time.August,
		11,
		15,
		0,
		0,
		0,
		time.UTC,
	)
	ids := make([]uint, 0, 3)
	for index := 0; index < 3; index++ {
		config := seedAdminWebhookCASConfig(
			t,
			fixture,
			"directory-"+strconv.Itoa(index),
		)
		deletedAt := base.Add(time.Duration(index) * time.Minute)
		if err := fixture.db.Model(&models.WebhookConfig{}).
			Where("id = ?", config.ID).
			Update("deleted_at", deletedAt).Error; err != nil {
			t.Fatal(err)
		}
		ids = append(ids, config.ID)
	}

	otherProject := fixture.project
	otherProject.ID = 0
	otherProject.PublicID = ""
	otherProject.Key = models.ProjectKey("OTHER")
	otherProject.Name = "Other project"
	if err := fixture.db.Create(&otherProject).Error; err != nil {
		t.Fatal(err)
	}
	crossProject := models.WebhookConfig{
		OrganizationID: fixture.scope.OrganizationID,
		ProjectID:      otherProject.ID,
		Name:           "must never appear",
		Provider:       models.WebhookProviderCustom,
		WebhookURL:     "https://cross-project.invalid.example/secret",
		Secret:         "cross-project-secret",
		Status:         models.WebhookStatusActive,
		CreatedBy:      fixture.admin.ID,
	}
	if err := fixture.db.Create(&crossProject).Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.Delete(&crossProject).Error; err != nil {
		t.Fatal(err)
	}

	type pageEnvelope struct {
		Data struct {
			Items      []map[string]any `json:"items"`
			Total      int64            `json:"total"`
			Page       int              `json:"page"`
			PageSize   int              `json:"page_size"`
			TotalPages int              `json:"total_pages"`
		} `json:"data"`
	}
	load := func(page int) pageEnvelope {
		t.Helper()
		response := performAdminWebhookCASRequest(
			router,
			http.MethodGet,
			"/api/projects/TEST/admin/agents/webhooks/tombstones"+
				"?page="+strconv.Itoa(page)+"&page_size=2",
			"",
			"webhook-tombstone-page-"+strconv.Itoa(page),
		)
		if response.Code != http.StatusOK ||
			response.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf(
				"page %d status=%d cache=%q body=%s",
				page,
				response.Code,
				response.Header().Get("Cache-Control"),
				response.Body.String(),
			)
		}
		assertAdminWebhookEmergencyOutputSafe(t, response.Body.String())
		var envelope pageEnvelope
		if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
			t.Fatal(err)
		}
		for _, item := range envelope.Data.Items {
			wantKeys := map[string]struct{}{
				"config_id":         {},
				"status":            {},
				"deleted":           {},
				"emergency_revoked": {},
				"resource_version":  {},
			}
			if len(item) != len(wantKeys) {
				t.Fatalf("tombstone keys = %v", item)
			}
			for key := range item {
				if _, ok := wantKeys[key]; !ok {
					t.Fatalf("unexpected tombstone field %q: %v", key, item)
				}
			}
		}
		return envelope
	}
	first := load(1)
	second := load(2)
	if first.Data.Total != 3 ||
		first.Data.Page != 1 ||
		first.Data.PageSize != 2 ||
		first.Data.TotalPages != 2 ||
		len(first.Data.Items) != 2 ||
		uint(first.Data.Items[0]["config_id"].(float64)) != ids[2] ||
		uint(first.Data.Items[1]["config_id"].(float64)) != ids[1] {
		t.Fatalf("first tombstone page = %+v", first.Data)
	}
	if second.Data.Total != 3 ||
		second.Data.Page != 2 ||
		second.Data.PageSize != 2 ||
		second.Data.TotalPages != 2 ||
		len(second.Data.Items) != 1 ||
		uint(second.Data.Items[0]["config_id"].(float64)) != ids[0] {
		t.Fatalf("second tombstone page = %+v", second.Data)
	}
	for _, rawQuery := range []string{
		"page=1&page_size=101",
		"page=1&page_size=2&unknown=true",
	} {
		response := performAdminWebhookCASRequest(
			router,
			http.MethodGet,
			"/api/projects/TEST/admin/agents/webhooks/tombstones?"+
				rawQuery,
			"",
			"webhook-tombstone-invalid",
		)
		if response.Code != http.StatusBadRequest {
			t.Fatalf(
				"invalid query %q status=%d body=%s",
				rawQuery,
				response.Code,
				response.Body.String(),
			)
		}
	}

	if err := fixture.db.Model(&models.ProjectMembership{}).
		Where(
			"project_id = ? AND user_id = ?",
			fixture.project.ID,
			fixture.admin.ID,
		).
		Update("role", models.ProjectRoleManager).Error; err != nil {
		t.Fatal(err)
	}
	forbidden := performAdminWebhookCASRequest(
		router,
		http.MethodGet,
		"/api/projects/TEST/admin/agents/webhooks/tombstones",
		"",
		"webhook-tombstone-manager",
	)
	if forbidden.Code != http.StatusForbidden {
		t.Fatalf(
			"manager tombstones status=%d body=%s",
			forbidden.Code,
			forbidden.Body.String(),
		)
	}
}

func TestWebhookManagerCannotReactivateEmergencyRevokedConfig(
	t *testing.T,
) {
	fixture := newAdminContractFixture(t)
	config := seedAdminWebhookCASConfig(t, fixture, "terminal-revoke")
	router := newAdminWebhookEmergencyCASRouter(t, fixture)
	definitionPath := "/api/projects/TEST/webhooks/" + uintString(config.ID)
	initial := getAdminWebhookResourceVersion(t, router, definitionPath)
	revokePath := "/api/projects/TEST/admin/agents/webhooks/" +
		uintString(config.ID) + "/emergency-revoke"
	revoked := performAdminContractRequest(
		router,
		http.MethodPost,
		revokePath,
		"",
		"webhook-terminal-revoke",
		httpcontract.FormatETag(initial),
		"webhook-terminal-revoke",
	)
	if revoked.Code != http.StatusOK {
		t.Fatalf("emergency revoke status=%d body=%s", revoked.Code, revoked.Body.String())
	}
	if err := fixture.db.Model(&models.ProjectMembership{}).
		Where(
			"project_id = ? AND user_id = ?",
			fixture.project.ID,
			fixture.admin.ID,
		).
		Update("role", models.ProjectRoleManager).Error; err != nil {
		t.Fatal(err)
	}
	// Rebuild every service/router object so the terminal decision must come
	// from the durable emergency event rather than process-local state.
	router = newAdminWebhookEmergencyCASRouter(t, fixture)
	current := getAdminWebhookResourceVersion(t, router, definitionPath)
	if current != initial+1 {
		t.Fatalf(
			"resource version after restart = %d, want %d",
			current,
			initial+1,
		)
	}

	update := performAdminWebhookCASRequestWithIfMatch(
		router,
		http.MethodPut,
		definitionPath,
		`{"status":"active"}`,
		"webhook-terminal-manager-reactivate",
		httpcontract.FormatETag(current),
	)
	if update.Code != http.StatusConflict ||
		!strings.Contains(
			update.Body.String(),
			"webhook_emergency_revoked",
		) ||
		strings.Contains(update.Body.String(), `"retryable":true`) {
		t.Fatalf(
			"manager reactivation status=%d body=%s",
			update.Code,
			update.Body.String(),
		)
	}
	var stored models.WebhookConfig
	if err := fixture.db.Unscoped().First(&stored, config.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.Status != models.WebhookStatusDisabled {
		t.Fatalf("manager counteracted emergency revoke: %+v", stored)
	}
	if stored.Secret != "" ||
		stored.PreviousSecret != "" ||
		stored.PreviousSecretExpiresAt != nil ||
		stored.AccessToken != "" {
		t.Fatalf("terminal update restored credential material: %+v", stored)
	}
	after := getAdminWebhookResourceVersion(t, router, definitionPath)
	if after != current {
		t.Fatalf(
			"terminal update advanced version from %d to %d",
			current,
			after,
		)
	}
	var eventCount int64
	if err := fixture.db.Model(&models.DomainEvent{}).
		Where(
			"subject = ? AND type = ?",
			services.WebhookAdminSubject(config.ID),
			services.WebhookEmergencyRevokedAdminEventType,
		).
		Count(&eventCount).Error; err != nil {
		t.Fatal(err)
	}
	if eventCount != 1 {
		t.Fatalf("terminal update changed emergency event count = %d", eventCount)
	}
}

func TestWebhookEmergencyRevokeRejectsVersionBeforeOrdinaryUpdate(
	t *testing.T,
) {
	fixture := newAdminContractFixture(t)
	config := seedAdminWebhookCASConfig(t, fixture, "stale-update")
	router := newAdminWebhookEmergencyCASRouter(t, fixture)
	path := "/api/projects/TEST/webhooks/" + uintString(config.ID)
	initial := getAdminWebhookResourceVersion(t, router, path)
	update := performAdminWebhookCASRequestWithIfMatch(
		router,
		http.MethodPut,
		path,
		`{"description":"ordinary update advanced the version"}`,
		"webhook-cas-put",
		httpcontract.FormatETag(initial),
	)
	if update.Code != http.StatusOK {
		t.Fatalf("ordinary update status=%d body=%s", update.Code, update.Body.String())
	}
	if update.Header().Get("ETag") !=
		httpcontract.FormatETag(initial+1) {
		t.Fatalf("ordinary update ETag = %q", update.Header().Get("ETag"))
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
	deleted := performAdminWebhookCASRequestWithIfMatch(
		router,
		http.MethodDelete,
		path,
		"",
		"webhook-cas-delete",
		httpcontract.FormatETag(initial),
	)
	if deleted.Code != http.StatusOK {
		t.Fatalf(
			"ordinary delete status=%d body=%s",
			deleted.Code,
			deleted.Body.String(),
		)
	}
	if deleted.Header().Get("ETag") !=
		httpcontract.FormatETag(initial+1) {
		t.Fatalf("ordinary delete ETag = %q", deleted.Header().Get("ETag"))
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

func TestWebhookOrdinaryMutationsRejectStaleIfMatchWithoutWrites(
	t *testing.T,
) {
	for _, losingMethod := range []string{
		http.MethodPut,
		http.MethodDelete,
	} {
		t.Run(losingMethod, func(t *testing.T) {
			fixture := newAdminContractFixture(t)
			config := seedAdminWebhookCASConfig(
				t,
				fixture,
				"ordinary-stale-"+strings.ToLower(losingMethod),
			)
			router := newAdminWebhookEmergencyCASRouter(t, fixture)
			path := "/api/projects/TEST/webhooks/" +
				uintString(config.ID)
			initial := getAdminWebhookResourceVersion(t, router, path)
			winner := performAdminWebhookCASRequestWithIfMatch(
				router,
				http.MethodPut,
				path,
				`{"description":"winning generation"}`,
				"webhook-ordinary-cas-winner",
				httpcontract.FormatETag(initial),
			)
			if winner.Code != http.StatusOK ||
				winner.Header().Get("ETag") !=
					httpcontract.FormatETag(initial+1) {
				t.Fatalf(
					"winner status=%d ETag=%q body=%s",
					winner.Code,
					winner.Header().Get("ETag"),
					winner.Body.String(),
				)
			}
			body := ""
			if losingMethod == http.MethodPut {
				body = `{"description":"must not commit"}`
			}
			stale := performAdminWebhookCASRequestWithIfMatch(
				router,
				losingMethod,
				path,
				body,
				"webhook-ordinary-cas-stale",
				httpcontract.FormatETag(initial),
			)
			if stale.Code != http.StatusConflict ||
				stale.Header().Get("ETag") !=
					httpcontract.FormatETag(initial+1) ||
				!strings.Contains(
					stale.Body.String(),
					ProblemVersionConflict,
				) {
				t.Fatalf(
					"stale %s status=%d ETag=%q body=%s",
					losingMethod,
					stale.Code,
					stale.Header().Get("ETag"),
					stale.Body.String(),
				)
			}
			var stored models.WebhookConfig
			if err := fixture.db.Unscoped().First(
				&stored,
				config.ID,
			).Error; err != nil {
				t.Fatal(err)
			}
			if stored.DeletedAt.Valid ||
				stored.Description != "winning generation" {
				t.Fatalf("stale mutation changed config: %+v", stored)
			}
			if current := getAdminWebhookResourceVersion(
				t,
				router,
				path,
			); current != initial+1 {
				t.Fatalf(
					"stale mutation advanced version to %d",
					current,
				)
			}
			var emergencyEvents int64
			if err := fixture.db.Model(&models.DomainEvent{}).
				Where(
					"subject = ? AND type = ?",
					services.WebhookAdminSubject(config.ID),
					services.WebhookEmergencyRevokedAdminEventType,
				).
				Count(&emergencyEvents).Error; err != nil {
				t.Fatal(err)
			}
			if emergencyEvents != 0 {
				t.Fatalf(
					"stale ordinary mutation created %d emergency events",
					emergencyEvents,
				)
			}
		})
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
	update := performAdminWebhookCASRequestWithIfMatch(
		router,
		http.MethodPut,
		path,
		`{"description":"advance before legal revoke"}`,
		"webhook-cas-current-put",
		httpcontract.FormatETag(initial),
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

func performAdminWebhookCASRequestWithIfMatch(
	router *gin.Engine,
	method string,
	path string,
	body string,
	requestID string,
	ifMatch string,
) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Request-ID", requestID)
	request.Header.Set("If-Match", ifMatch)
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
