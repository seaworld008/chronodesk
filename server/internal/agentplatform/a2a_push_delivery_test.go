package agentplatform

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/a2a"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/security"
	"github.com/seaworld008/chronodesk/server/internal/services"
)

type a2aPushRoundTripper func(*http.Request) (*http.Response, error)

func (roundTrip a2aPushRoundTripper) RoundTrip(
	request *http.Request,
) (*http.Response, error) {
	return roundTrip(request)
}

func newA2APushTestProtector(t *testing.T) security.Protector {
	t.Helper()
	protector, err := security.NewKeyring(
		"a2a-push-test-v1",
		map[string][]byte{
			"a2a-push-test-v1": []byte(
				"0123456789abcdef0123456789abcdef",
			),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return protector
}

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

func TestA2APushDispatcherRequiresSecretProtector(t *testing.T) {
	fixture := newA2AAdapterFixture(t)
	_, err := NewA2AOutboxPushDispatcher(
		A2AOutboxPushDispatcherOptions{
			DB:          fixture.db,
			Native:      fixture.native,
			MaxAttempts: 4,
		},
	)
	if err == nil ||
		!strings.Contains(err.Error(), "secret protector") {
		t.Fatalf("missing A2A push protector error = %v", err)
	}
}

func TestA2APushDeliveryUsesFrozenDestinationAfterReplaceOrDelete(
	t *testing.T,
) {
	for _, mutation := range []string{"replace", "delete"} {
		t.Run(mutation, func(t *testing.T) {
			fixture := newA2AAdapterFixture(t)
			protector := newA2APushTestProtector(t)
			store := a2a.NewGormStoreWithProtector(
				fixture.db,
				protector,
			)
			if err := store.AutoMigrate(); err != nil {
				t.Fatal(err)
			}
			ctx := a2aFixtureContext(t, fixture)
			now := time.Now().UTC()
			task := a2a.Task{
				ID:        "task-frozen-" + mutation,
				ContextID: "context-frozen-" + mutation,
				Status: a2a.TaskStatus{
					State:     a2a.TaskStateWorking,
					Timestamp: now,
				},
				StatusHistory: []a2a.TaskStatus{{
					State:     a2a.TaskStateWorking,
					Timestamp: now,
				}},
				CreatedAt:    now,
				LastModified: now,
				Version:      1,
			}
			if err := store.CreateTask(ctx, task); err != nil {
				t.Fatal(err)
			}
			const configID = "push-frozen-config"
			oldConfig := a2a.PushNotificationConfig{
				ID:     configID,
				TaskID: task.ID,
				URL: "https://old.example.test/a2a?" +
					"opaque=old-query-value",
				Token: "old-token",
				Authentication: &a2a.AuthenticationInfo{
					Scheme:      "Bearer",
					Credentials: "old-credential",
				},
				CreatedAt: now,
			}
			if err := store.CreatePushConfig(
				ctx,
				oldConfig,
			); err != nil {
				t.Fatal(err)
			}
			dispatcher, err := NewA2AOutboxPushDispatcher(
				A2AOutboxPushDispatcherOptions{
					DB:              fixture.db,
					Native:          fixture.native,
					SecretProtector: protector,
					MaxAttempts:     4,
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			persisted, err := store.AppendEventWithPush(
				ctx,
				a2a.StoredEvent{
					TaskID:    task.ID,
					ContextID: task.ContextID,
					Payload: a2a.StreamResponse{
						StatusUpdate: &a2a.TaskStatusUpdateEvent{
							TaskID:    task.ID,
							ContextID: task.ContextID,
							Status: a2a.TaskStatus{
								State:     a2a.TaskStateWorking,
								Timestamp: now,
							},
						},
					},
					CreatedAt: now,
				},
				dispatcher,
			)
			if err != nil {
				t.Fatalf("commit A2A push intent: %v", err)
			}
			eventID := stableA2APushEventID(
				task.ID,
				persisted.Cursor,
				configID,
			)
			var event models.DomainEvent
			if err := fixture.db.First(
				&event,
				"id = ?",
				eventID,
			).Error; err != nil {
				t.Fatal(err)
			}
			var delivery models.OutboxDelivery
			if err := fixture.db.First(
				&delivery,
				"event_id = ? AND destination_type = ?",
				event.ID,
				"a2a_push",
			).Error; err != nil {
				t.Fatal(err)
			}
			snapshotID, err :=
				parseA2APushSnapshotDestinationID(
					delivery.DestinationID,
				)
			if err != nil {
				t.Fatal(err)
			}
			var snapshot models.A2APushDeliverySnapshot
			if err := fixture.db.First(
				&snapshot,
				"id = ?",
				snapshotID,
			).Error; err != nil {
				t.Fatal(err)
			}

			if err := store.DeletePushConfig(
				ctx,
				task.ID,
				configID,
			); err != nil {
				t.Fatal(err)
			}
			if mutation == "replace" {
				if err := store.CreatePushConfig(
					ctx,
					a2a.PushNotificationConfig{
						ID:     configID,
						TaskID: task.ID,
						URL: "https://new.example.test/" +
							"a2a",
						Token: "new-token",
						Authentication: &a2a.AuthenticationInfo{
							Scheme:      "Basic",
							Credentials: "new-credential",
						},
						CreatedAt: now.Add(time.Minute),
					},
				); err != nil {
					t.Fatal(err)
				}
			}

			type capturedRequest struct {
				host          string
				token         string
				authorization string
				body          string
			}
			var (
				requestMu sync.Mutex
				requests  []capturedRequest
			)
			deliverer, err := NewNativeOutboxDeliverer(
				NativeOutboxDelivererOptions{
					DB:              fixture.db,
					SecretProtector: protector,
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			deliverer.a2aPushClient = func(
				context.Context,
				*url.URL,
				*net.Resolver,
				time.Duration,
			) (*http.Client, error) {
				return &http.Client{
					Transport: a2aPushRoundTripper(
						func(
							request *http.Request,
						) (*http.Response, error) {
							body, readErr := io.ReadAll(
								request.Body,
							)
							if readErr != nil {
								return nil, readErr
							}
							requestMu.Lock()
							requests = append(
								requests,
								capturedRequest{
									host: request.URL.Host,
									token: request.Header.Get(
										"X-A2A-Notification-Token",
									),
									authorization: request.Header.Get(
										"Authorization",
									),
									body: string(body),
								},
							)
							requestMu.Unlock()
							return &http.Response{
								StatusCode: http.StatusNoContent,
								Body: io.NopCloser(
									strings.NewReader(""),
								),
								Header: make(http.Header),
							}, nil
						},
					),
				}, nil
			}
			if err := deliverer.Deliver(
				agentplatformTestOutboxWorkerContext(
					t,
					fixture.project.Scope(),
				),
				&delivery,
				services.CloudEventFromModel(&event),
			); err != nil {
				t.Fatalf("deliver frozen A2A push: %v", err)
			}
			requestMu.Lock()
			defer requestMu.Unlock()
			if len(requests) != 1 ||
				requests[0].host != "old.example.test" ||
				requests[0].token != "old-token" ||
				requests[0].authorization !=
					"Bearer old-credential" ||
				requests[0].body !=
					string(snapshot.RequestBody) {
				t.Fatalf(
					"frozen A2A push request = %+v",
					requests,
				)
			}
			for _, request := range requests {
				if request.host == "new.example.test" ||
					request.token == "new-token" ||
					strings.Contains(
						request.authorization,
						"new-credential",
					) {
					t.Fatalf(
						"committed push followed mutable config: %+v",
						request,
					)
				}
			}
		})
	}
}

func TestA2APushCallbackPolicyFailureNeverReturnsURLQueryToken(t *testing.T) {
	fixture := newA2AAdapterFixture(t)
	if err := fixture.db.AutoMigrate(
		&models.A2APushDeliverySnapshot{},
	); err != nil {
		t.Fatal(err)
	}
	const sensitiveToken = "callback-query-token-must-not-return"
	protector := newA2APushTestProtector(t)
	deliverer, err := NewNativeOutboxDeliverer(NativeOutboxDelivererOptions{
		DB:              fixture.db,
		SecretProtector: protector,
	})
	if err != nil {
		t.Fatal(err)
	}
	streamResponse, err := json.Marshal(a2a.StreamResponse{
		StatusUpdate: &a2a.TaskStatusUpdateEvent{
			TaskID:    "task-safe-error",
			ContextID: "context-safe-error",
			Status: a2a.TaskStatus{
				State: a2a.TaskStateWorking,
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := models.NewA2APushDeliverySnapshot(
		fixture.project.Scope(),
		"event-safe-error",
		"task-safe-error",
		"push-safe-error",
		time.Now().UTC(),
		"https://192.0.2.1/a2a?access_token="+sensitiveToken,
		streamResponse,
		"application/a2a+json",
		a2a.ProtocolVersion,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.Create(snapshot).Error; err != nil {
		t.Fatal(err)
	}
	eventData, err := json.Marshal(map[string]any{
		"a2a_task_id":      snapshot.TaskID,
		"push_snapshot_id": snapshot.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	err = deliverer.deliverA2APush(
		context.Background(),
		&models.OutboxDelivery{
			OrganizationID:  1,
			ProjectID:       1,
			EventID:         "event-safe-error",
			DestinationType: "a2a_push",
			DestinationID: a2aPushSnapshotDestinationPrefix +
				snapshot.ID,
		},
		services.CloudEventEnvelope{
			ID:             "event-safe-error",
			OrganizationID: 1,
			ProjectID:      1,
			Data:           eventData,
		},
	)
	if err == nil || err.Error() != "A2A Push 回调地址不可用" {
		t.Fatalf("push policy error=%v", err)
	}
	if strings.Contains(err.Error(), sensitiveToken) ||
		strings.Contains(err.Error(), snapshot.CallbackURL) {
		t.Fatalf("push policy error leaked callback credentials: %v", err)
	}
}
