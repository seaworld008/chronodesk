package agentplatform

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/seaworld008/chronodesk/server/internal/a2a"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/services"
)

func TestA2APushRequestUsesCanonicalMediaTypeAndVersion(t *testing.T) {
	payload := json.RawMessage(`{"statusUpdate":{"taskId":"task-1"}}`)
	request, err := newA2APushRequest(
		context.Background(),
		"https://hooks.example.com/a2a",
		payload,
		"event-1",
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := request.Header.Get("Content-Type"); got != "application/a2a+json" {
		t.Fatalf("Content-Type=%q, want application/a2a+json", got)
	}
	if got := request.Header.Get("A2A-Version"); got != a2a.ProtocolVersion {
		t.Fatalf("A2A-Version=%q, want %q", got, a2a.ProtocolVersion)
	}
	if got := request.Header.Get("X-CloudEvents-ID"); got != "event-1" {
		t.Fatalf("X-CloudEvents-ID=%q, want event-1", got)
	}
	body, err := io.ReadAll(request.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != string(payload) {
		t.Fatalf("push body=%s, want %s", body, payload)
	}
}

func TestA2APushCallbackPolicyFailureNeverReturnsURLQueryToken(t *testing.T) {
	fixture := newA2AAdapterFixture(t)
	if err := fixture.db.AutoMigrate(&models.AgentPushNotificationConfig{}); err != nil {
		t.Fatal(err)
	}
	const sensitiveToken = "callback-query-token-must-not-return"
	config := models.AgentPushNotificationConfig{
		ID:             "push-safe-error",
		OrganizationID: 1,
		ProjectID:      1,
		TaskID:         "task-safe-error",
		URL:            "https://192.0.2.1/a2a?access_token=" + sensitiveToken,
	}
	if err := fixture.db.Create(&config).Error; err != nil {
		t.Fatal(err)
	}
	deliverer, err := NewNativeOutboxDeliverer(NativeOutboxDelivererOptions{
		DB: fixture.db,
	})
	if err != nil {
		t.Fatal(err)
	}
	streamResponse, err := json.Marshal(a2a.StreamResponse{
		StatusUpdate: &a2a.TaskStatusUpdateEvent{
			TaskID:    config.TaskID,
			ContextID: "context-safe-error",
			Status: a2a.TaskStatus{
				State: a2a.TaskStateWorking,
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	eventData, err := json.Marshal(map[string]any{
		"a2a_task_id":     config.TaskID,
		"push_config_id":  config.ID,
		"stream_response": json.RawMessage(streamResponse),
	})
	if err != nil {
		t.Fatal(err)
	}
	err = deliverer.deliverA2APush(context.Background(), services.CloudEventEnvelope{
		ID:             "event-safe-error",
		OrganizationID: 1,
		ProjectID:      1,
		Data:           eventData,
	})
	if err == nil || err.Error() != "A2A Push 回调地址不可用" {
		t.Fatalf("push policy error=%v", err)
	}
	if strings.Contains(err.Error(), sensitiveToken) ||
		strings.Contains(err.Error(), config.URL) {
		t.Fatalf("push policy error leaked callback credentials: %v", err)
	}
}
