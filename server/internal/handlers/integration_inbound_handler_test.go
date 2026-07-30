package handlers

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/seaworld008/chronodesk/server/internal/middleware"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/services"
	"gorm.io/datatypes"
)

const (
	inboundTestConnectionID = "019fb142-fbf0-7c1e-bf41-7e6c3641aa01"
	inboundTestMappingID    = "019fb142-fbf0-7c1e-bf41-7e6c3641aa02"
)

type inboundReceiverStub struct {
	target     services.IntegrationInboundTarget
	resolveErr error
	result     *services.IntegrationInboundResult
	receiveErr error

	resolveCalls int
	receiveCalls int
	input        services.IntegrationInboundInput
}

func (stub *inboundReceiverStub) ResolvePublicInboundTarget(
	_ context.Context,
	projectKey string,
	connectionID string,
	mappingID string,
) (services.IntegrationInboundTarget, error) {
	stub.resolveCalls++
	if projectKey != "TEST" ||
		connectionID != inboundTestConnectionID ||
		mappingID != inboundTestMappingID {
		return services.IntegrationInboundTarget{},
			services.ErrIntegrationConnectionNotFound
	}
	return stub.target, stub.resolveErr
}

func (stub *inboundReceiverStub) Receive(
	_ context.Context,
	input services.IntegrationInboundInput,
) (*services.IntegrationInboundResult, error) {
	stub.receiveCalls++
	stub.input = input
	return stub.result, stub.receiveErr
}

func TestIntegrationInboundHandlerUsesOpaquePathAndTrustedTelemetry(t *testing.T) {
	body := []byte(`{"title":"untrusted-secret-payload"}`)
	receipt := &models.InboxReceipt{
		PublicID:        "019fb142-fbf0-7c1e-bf41-7e6c3641aa04",
		Status:          models.InboxReceiptStatusApplied,
		ResourceType:    "ticket",
		ResourceID:      "019fb142-fbf0-7c1e-bf41-7e6c3641aa05",
		ResourceVersion: 1,
		EventID:         "event-safe",
		OperationID:     "operation-safe",
		Result:          datatypes.JSON([]byte(`{"must_not":"be_returned"}`)),
	}
	stub := &inboundReceiverStub{
		target: services.IntegrationInboundTarget{
			Scope:            models.ProjectScope{OrganizationID: 9, ProjectID: 12},
			ConnectionID:     21,
			MappingVersionID: 22,
		},
		result: &services.IntegrationInboundResult{
			Message: &models.InboxMessage{
				PublicID: "019fb142-fbf0-7c1e-bf41-7e6c3641aa03",
				Status:   models.InboxMessageStatusCompleted,
			},
			Receipt: receipt,
		},
	}
	router := inboundTestRouter(stub, true)
	request := inboundTestRequest(t, body)
	request.Header.Set("X-Trace-ID", "client-forged-trace")
	request.Header.Set("X-Correlation-ID", "client-forged-correlation")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if stub.resolveCalls != 1 || stub.receiveCalls != 1 {
		t.Fatalf(
			"calls resolve=%d receive=%d",
			stub.resolveCalls,
			stub.receiveCalls,
		)
	}
	if stub.input.Scope != stub.target.Scope ||
		stub.input.ConnectionID != stub.target.ConnectionID ||
		stub.input.MappingVersionID != stub.target.MappingVersionID ||
		stub.input.ExternalMessageID != "message-safe-1" ||
		stub.input.ExternalResourceType != "case" ||
		stub.input.ExternalResourceID != "CASE-42" {
		t.Fatalf("received input = %+v", stub.input)
	}
	if stub.input.TrustedTraceID != "trusted-server-trace" ||
		stub.input.TrustedCorrelationID != "trusted-server-correlation" {
		t.Fatalf(
			"untrusted telemetry reached service: trace=%q correlation=%q",
			stub.input.TrustedTraceID,
			stub.input.TrustedCorrelationID,
		)
	}
	responseBody := recorder.Body.String()
	for _, forbidden := range []string{
		"untrusted-secret-payload",
		"must_not",
		"be_returned",
		"client-forged",
	} {
		if strings.Contains(responseBody, forbidden) {
			t.Fatalf("response leaked %q: %s", forbidden, responseBody)
		}
	}
	for _, expected := range []string{
		receipt.PublicID,
		receipt.ResourceID,
		`"state":"applied"`,
	} {
		if !strings.Contains(responseBody, expected) {
			t.Fatalf("response missing %q: %s", expected, responseBody)
		}
	}
}

func TestIntegrationInboundHandlerReturnsStableReplayConflictAndDeadLetter(
	t *testing.T,
) {
	tests := []struct {
		name       string
		result     *services.IntegrationInboundResult
		err        error
		wantStatus int
		wantState  string
	}{
		{
			name: "replay",
			result: &services.IntegrationInboundResult{
				Replayed: true,
				Message: &models.InboxMessage{
					PublicID: "019fb142-fbf0-7c1e-bf41-7e6c3641ab01",
					Status:   models.InboxMessageStatusCompleted,
				},
				Receipt: &models.InboxReceipt{
					PublicID:        "019fb142-fbf0-7c1e-bf41-7e6c3641ab02",
					Status:          models.InboxReceiptStatusNoop,
					ResourceType:    "ticket",
					ResourceID:      "019fb142-fbf0-7c1e-bf41-7e6c3641ab03",
					ResourceVersion: 2,
					OperationID:     "operation-replay",
				},
			},
			wantStatus: http.StatusOK,
			wantState:  "noop",
		},
		{
			name: "conflict",
			result: &services.IntegrationInboundResult{
				Message: &models.InboxMessage{
					PublicID: "019fb142-fbf0-7c1e-bf41-7e6c3641ac01",
					Status:   models.InboxMessageStatusConflict,
				},
				Conflict: &models.IntegrationConflict{
					PublicID: "019fb142-fbf0-7c1e-bf41-7e6c3641ac02",
					Type:     models.IntegrationConflictMessageIdentityReuse,
					Status:   models.IntegrationConflictStatusOpen,
					Details: datatypes.JSON(
						`{"payload":"must-not-leak"}`,
					),
				},
			},
			err:        services.ErrIntegrationConflict,
			wantStatus: http.StatusConflict,
			wantState:  "conflict",
		},
		{
			name: "dead letter",
			result: &services.IntegrationInboundResult{
				Message: &models.InboxMessage{
					PublicID: "019fb142-fbf0-7c1e-bf41-7e6c3641ad01",
					Status:   models.InboxMessageStatusDeadLetter,
				},
				DeadLetter: &models.DeadLetter{
					PublicID:     "019fb142-fbf0-7c1e-bf41-7e6c3641ad02",
					Status:       models.DeadLetterStatusOpen,
					ReasonCode:   "domain_command_failed",
					ErrorSummary: "secret database detail",
				},
			},
			err:        services.ErrIntegrationCommandFailed,
			wantStatus: http.StatusUnprocessableEntity,
			wantState:  "dead_letter",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stub := inboundDefaultStub()
			stub.result = test.result
			stub.receiveErr = test.err
			recorder := httptest.NewRecorder()
			inboundTestRouter(stub, false).ServeHTTP(
				recorder,
				inboundTestRequest(t, []byte(`{"event":"safe"}`)),
			)
			if recorder.Code != test.wantStatus ||
				!strings.Contains(
					recorder.Body.String(),
					`"state":"`+test.wantState+`"`,
				) {
				t.Fatalf(
					"status=%d body=%s",
					recorder.Code,
					recorder.Body.String(),
				)
			}
			for _, forbidden := range []string{
				"must-not-leak",
				"secret database detail",
				"error_summary",
				"details",
			} {
				if strings.Contains(recorder.Body.String(), forbidden) {
					t.Fatalf(
						"response leaked %q: %s",
						forbidden,
						recorder.Body.String(),
					)
				}
			}
		})
	}
}

func TestIntegrationInboundHandlerMakesTargetAndSignatureFailuresIndistinguishable(
	t *testing.T,
) {
	tests := []struct {
		name       string
		resolveErr error
		receiveErr error
	}{
		{
			name:       "cross-project public id",
			resolveErr: services.ErrIntegrationConnectionNotFound,
		},
		{
			name:       "bad signature",
			receiveErr: services.ErrIntegrationSignatureRejected,
		},
		{
			name:       "expired timestamp",
			receiveErr: services.ErrIntegrationReplayWindow,
		},
	}
	var baseline string
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stub := inboundDefaultStub()
			stub.resolveErr = test.resolveErr
			stub.receiveErr = test.receiveErr
			recorder := httptest.NewRecorder()
			inboundTestRouter(stub, false).ServeHTTP(
				recorder,
				inboundTestRequest(t, []byte(`{"event":"safe"}`)),
			)
			if recorder.Code != http.StatusUnauthorized {
				t.Fatalf(
					"status=%d body=%s",
					recorder.Code,
					recorder.Body.String(),
				)
			}
			if baseline == "" {
				baseline = recorder.Body.String()
			} else if recorder.Body.String() != baseline {
				t.Fatalf(
					"authentication failure is distinguishable:\nbase=%s\ngot=%s",
					baseline,
					recorder.Body.String(),
				)
			}
		})
	}
}

func TestIntegrationInboundHandlerEnforcesBodyLimitAndDoesNotEchoErrors(
	t *testing.T,
) {
	t.Run("body limit", func(t *testing.T) {
		stub := inboundDefaultStub()
		body := bytes.Repeat([]byte("x"), int(integrationInboundBodyLimit)+1)
		recorder := httptest.NewRecorder()
		inboundTestRouter(stub, false).ServeHTTP(
			recorder,
			inboundTestRequest(t, body),
		)
		if recorder.Code != http.StatusRequestEntityTooLarge ||
			stub.resolveCalls != 0 ||
			stub.receiveCalls != 0 {
			t.Fatalf(
				"status=%d resolve=%d receive=%d body=%s",
				recorder.Code,
				stub.resolveCalls,
				stub.receiveCalls,
				recorder.Body.String(),
			)
		}
	})
	t.Run("internal error", func(t *testing.T) {
		stub := inboundDefaultStub()
		stub.receiveErr = errors.New(
			"secret-ref=prod/key payload=customer-private-data",
		)
		recorder := httptest.NewRecorder()
		inboundTestRouter(stub, false).ServeHTTP(
			recorder,
			inboundTestRequest(t, []byte(`{"event":"private"}`)),
		)
		if recorder.Code != http.StatusServiceUnavailable {
			t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
		}
		for _, forbidden := range []string{"secret-ref", "prod/key", "customer-private"} {
			if strings.Contains(recorder.Body.String(), forbidden) {
				t.Fatalf(
					"response leaked %q: %s",
					forbidden,
					recorder.Body.String(),
				)
			}
		}
	})
}

func TestIntegrationInboundHandlerRejectsAmbiguousHeaders(t *testing.T) {
	stub := inboundDefaultStub()
	request := inboundTestRequest(t, []byte(`{"event":"safe"}`))
	request.Header.Add(IntegrationInboundMessageIDHeader, "second-message-id")
	recorder := httptest.NewRecorder()
	inboundTestRouter(stub, false).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest ||
		stub.resolveCalls != 0 ||
		stub.receiveCalls != 0 {
		t.Fatalf(
			"status=%d resolve=%d receive=%d body=%s",
			recorder.Code,
			stub.resolveCalls,
			stub.receiveCalls,
			recorder.Body.String(),
		)
	}
}

func inboundDefaultStub() *inboundReceiverStub {
	return &inboundReceiverStub{
		target: services.IntegrationInboundTarget{
			Scope:            models.ProjectScope{OrganizationID: 1, ProjectID: 2},
			ConnectionID:     3,
			MappingVersionID: 4,
		},
		result: &services.IntegrationInboundResult{
			Message: &models.InboxMessage{
				PublicID: "019fb142-fbf0-7c1e-bf41-7e6c3641ae01",
				Status:   models.InboxMessageStatusCompleted,
			},
			Receipt: &models.InboxReceipt{
				PublicID:        "019fb142-fbf0-7c1e-bf41-7e6c3641ae02",
				Status:          models.InboxReceiptStatusApplied,
				ResourceType:    "ticket",
				ResourceID:      "019fb142-fbf0-7c1e-bf41-7e6c3641ae03",
				ResourceVersion: 1,
				EventID:         "event-default",
				OperationID:     "operation-default",
			},
		},
	}
}

func inboundTestRouter(
	receiver IntegrationInboundReceiver,
	trustedTelemetry bool,
) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	if trustedTelemetry {
		router.Use(func(c *gin.Context) {
			c.Set(middleware.TraceIDContextKey, "trusted-server-trace")
			c.Set(
				middleware.CorrelationIDContextKey,
				"trusted-server-correlation",
			)
			c.Next()
		})
	}
	NewIntegrationInboundHandler(receiver).RegisterRoutes(router)
	return router
}

func inboundTestRequest(t *testing.T, body []byte) *http.Request {
	t.Helper()
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v2/projects/TEST/integrations/inbound/"+
			inboundTestConnectionID+
			"/mappings/"+
			inboundTestMappingID+
			"/messages",
		bytes.NewReader(body),
	)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(IntegrationInboundMessageIDHeader, "message-safe-1")
	request.Header.Set(IntegrationInboundExternalResourceTypeHeader, "case")
	request.Header.Set(IntegrationInboundExternalResourceIDHeader, "CASE-42")
	request.Header.Set(
		IntegrationInboundTimestampHeader,
		strconv.FormatInt(time.Now().UTC().Unix(), 10),
	)
	request.Header.Set(
		IntegrationInboundSignatureHeader,
		"v1=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	)
	return request
}
