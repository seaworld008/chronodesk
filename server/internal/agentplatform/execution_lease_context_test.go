package agentplatform

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/seaworld008/chronodesk/server/internal/a2a"
	"github.com/seaworld008/chronodesk/server/internal/agentauth"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/services"
)

func TestAgentRESTExecutionLimitPropagatesLeaseCancellationToRequest(
	t *testing.T,
) {
	guard := &renewalFailingExecutionGuard{}
	fixture := newMCPAdapterFixtureWithExecutionGuardAndLeaseTTL(
		t,
		guard,
		750*time.Millisecond,
	)
	handler := &APIHandler{native: fixture.service}
	var observedCause error
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(agentauth.ContextPrincipalID, fixture.principal.ID)
		c.Next()
	})
	router.GET(
		"/lease-context",
		handler.executionLimit(),
		func(c *gin.Context) {
			select {
			case <-c.Request.Context().Done():
				observedCause = context.Cause(
					c.Request.Context(),
				)
				c.Status(http.StatusServiceUnavailable)
			case <-time.After(3 * time.Second):
				c.Status(http.StatusGatewayTimeout)
			}
		},
	)
	request := httptest.NewRequest(
		http.MethodGet,
		"/lease-context",
		nil,
	)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusServiceUnavailable ||
		!errors.Is(
			observedCause,
			services.ErrExecutionGuardUnavailable,
		) {
		t.Fatalf(
			"REST lease context response=%d cause=%v",
			response.Code,
			observedCause,
		)
	}
	assertRenewalFailureGuardCalls(t, guard)
}

func TestA2AProcessPropagatesLeaseCancellationToReporter(
	t *testing.T,
) {
	guard := &renewalFailingExecutionGuard{}
	fixture := newA2AAdapterFixtureWithOptions(
		t,
		[]string{models.ScopeTasksManage},
		services.AgentNativeOptions{
			ExecutionGuard:    guard,
			ExecutionLeaseTTL: 750 * time.Millisecond,
		},
	)
	reporter := &a2aLeaseContextProbeReporter{
		causes: make(chan error, 1),
	}
	err := fixture.backend.Process(
		context.Background(),
		a2a.Task{
			ID:        "a2a-lease-context-task",
			ContextID: "a2a-lease-context",
		},
		a2a.Message{
			MessageID: "a2a-lease-context-message",
			Role:      a2a.RoleUser,
		},
		reporter,
	)
	if !errors.Is(err, services.ErrExecutionGuardUnavailable) {
		t.Fatalf("A2A Process renewal error = %v", err)
	}
	select {
	case cause := <-reporter.causes:
		if !errors.Is(
			cause,
			services.ErrExecutionGuardUnavailable,
		) {
			t.Fatalf("A2A reporter lease cause = %v", cause)
		}
	default:
		t.Fatal("A2A reporter did not observe lease cancellation")
	}
	assertRenewalFailureGuardCalls(t, guard)
}

type a2aLeaseContextProbeReporter struct {
	causes chan error
}

func (reporter *a2aLeaseContextProbeReporter) SetStatus(
	ctx context.Context,
	_ a2a.TaskState,
	_ *a2a.Message,
	_ map[string]any,
) error {
	return reporter.waitForLeaseCancellation(ctx)
}

func (reporter *a2aLeaseContextProbeReporter) AddArtifact(
	ctx context.Context,
	_ a2a.Artifact,
	_ bool,
	_ bool,
	_ map[string]any,
) error {
	return reporter.waitForLeaseCancellation(ctx)
}

func (reporter *a2aLeaseContextProbeReporter) waitForLeaseCancellation(
	ctx context.Context,
) error {
	select {
	case <-ctx.Done():
		cause := context.Cause(ctx)
		reporter.causes <- cause
		return cause
	case <-time.After(3 * time.Second):
		return errors.New(
			"timed out waiting for A2A execution lease cancellation",
		)
	}
}

func assertRenewalFailureGuardCalls(
	t *testing.T,
	guard *renewalFailingExecutionGuard,
) {
	t.Helper()
	if guard.acquireCalls.Load() != 1 ||
		guard.renewCalls.Load() == 0 ||
		guard.releaseCalls.Load() != 1 {
		t.Fatalf(
			"execution lease calls acquire=%d renew=%d release=%d",
			guard.acquireCalls.Load(),
			guard.renewCalls.Load(),
			guard.releaseCalls.Load(),
		)
	}
}
