package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestCreateWebhookRequestRejectsGhostStatus(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, test := range []struct {
		name    string
		body    string
		wantErr bool
	}{
		{
			name: "canonical create",
			body: `{
				"name":"operator alerts",
				"provider":"custom",
				"webhook_url":"https://example.invalid/webhook",
				"enabled_events":["io.chronodesk.system.alert.v1"]
			}`,
		},
		{
			name: "create cannot smuggle update-only status",
			body: `{
				"name":"operator alerts",
				"provider":"custom",
				"webhook_url":"https://example.invalid/webhook",
				"enabled_events":["io.chronodesk.system.alert.v1"],
				"status":"inactive"
			}`,
			wantErr: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			context, _ := gin.CreateTestContext(recorder)
			context.Request = httptest.NewRequest(
				http.MethodPost,
				"/api/projects/TEST/webhooks",
				strings.NewReader(test.body),
			)
			var request CreateWebhookRequest
			err := decodeStrictWebhookJSON(context, &request)
			if test.wantErr && err == nil {
				t.Fatal("decodeStrictWebhookJSON accepted an unpublished field")
			}
			if !test.wantErr && err != nil {
				t.Fatalf("decodeStrictWebhookJSON: %v", err)
			}
		})
	}
}

func TestUpdateWebhookRequestAcceptsPublishedStatusOnly(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(
		http.MethodPut,
		"/api/projects/TEST/webhooks/1",
		strings.NewReader(`{"status":"inactive"}`),
	)
	var request UpdateWebhookRequest
	if err := decodeStrictWebhookJSON(context, &request); err != nil {
		t.Fatalf("decodeStrictWebhookJSON: %v", err)
	}
	if request.Status == nil || string(*request.Status) != "inactive" {
		t.Fatalf("status = %v, want inactive", request.Status)
	}

	invalidRecorder := httptest.NewRecorder()
	invalidContext, _ := gin.CreateTestContext(invalidRecorder)
	invalidContext.Request = httptest.NewRequest(
		http.MethodPut,
		"/api/projects/TEST/webhooks/1",
		strings.NewReader(`{"status":"running"}`),
	)
	var invalidRequest UpdateWebhookRequest
	if err := decodeStrictWebhookJSON(
		invalidContext,
		&invalidRequest,
	); err == nil {
		t.Fatal("decodeStrictWebhookJSON accepted an unknown webhook status")
	}
}
