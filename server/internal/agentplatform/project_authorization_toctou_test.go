package agentplatform

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/seaworld008/chronodesk/server/internal/a2a"
	"github.com/seaworld008/chronodesk/server/internal/agentauth"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/services"
)

func TestAgentRESTProjectGrantRevokedBetweenResolveAndScopedHandlerFailsClosed(
	t *testing.T,
) {
	fixture := newMCPAdapterFixture(t)
	handler := NewAPIHandler(
		fixture.db,
		fixture.service,
		fixture.manager,
		1<<20,
		nil,
	)

	var (
		scopedResolveQueries atomic.Int64
		revokeErr            error
		revokedRows          int64
	)
	const callbackName = "test:revoke_agent_rest_grant_before_scoped_revalidation"
	if err := fixture.db.Callback().Query().
		Before("gorm:query").
		Register(callbackName, func(tx *gorm.DB) {
			if tx.Statement.Table != (models.Project{}).TableName() ||
				scopedResolveQueries.Add(1) != 1 {
				return
			}
			result := tx.Session(&gorm.Session{NewDB: true}).
				Model(&models.ProjectPrincipalGrant{}).
				Where(
					"project_id = ? AND service_principal_id = ?",
					fixture.project.ID,
					fixture.principal.ID,
				).
				Update("is_active", false)
			revokeErr = result.Error
			revokedRows = result.RowsAffected
		}); err != nil {
		t.Fatalf("register grant revocation barrier: %v", err)
	}
	t.Cleanup(func() {
		_ = fixture.db.Callback().Query().Remove(callbackName)
	})

	gin.SetMode(gin.TestMode)
	var reachedHandler atomic.Bool
	router := gin.New()
	router.GET(
		"/api/v2/projects/:projectKey/probe",
		func(c *gin.Context) {
			c.Set(agentauth.ContextPrincipalID, fixture.principal.ID)
			c.Set(agentauth.ContextCredentialID, fixture.credential.ID)
			c.Set(
				agentauth.ContextScopes,
				append([]string(nil), fixture.actor.Scopes...),
			)
			c.Next()
		},
		handler.bindProjectContext(),
		func(c *gin.Context) {
			reachedHandler.Store(true)
			c.Status(http.StatusNoContent)
		},
	)

	response := httptest.NewRecorder()
	router.ServeHTTP(
		response,
		httptest.NewRequest(
			http.MethodGet,
			"/api/v2/projects/TEST/probe",
			nil,
		),
	)
	if revokeErr != nil {
		t.Fatalf("revoke grant at authorization barrier: %v", revokeErr)
	}
	if scopedResolveQueries.Load() == 0 {
		t.Fatal("scoped project grant revalidation did not run")
	}
	if revokedRows != 1 {
		t.Fatalf("grant revocation affected %d rows, want 1", revokedRows)
	}
	if response.Code != http.StatusForbidden {
		t.Fatalf(
			"revoked grant status=%d body=%s, want 403",
			response.Code,
			response.Body.String(),
		)
	}
	var problem Problem
	if err := json.Unmarshal(response.Body.Bytes(), &problem); err != nil {
		t.Fatalf("decode revoked-grant problem: %v", err)
	}
	if problem.Code != ProblemPolicyDenied {
		t.Fatalf(
			"revoked grant problem code=%q, want %q",
			problem.Code,
			ProblemPolicyDenied,
		)
	}
	if reachedHandler.Load() {
		t.Fatal("Agent REST handler executed after its project grant was revoked")
	}
}

func TestMCPProjectGrantRevokedAfterAuthenticationBeforeScopedCallbackFailsClosed(
	t *testing.T,
) {
	fixture := newMCPAdapterFixture(t)
	if err := revokeAgentplatformProjectGrant(
		fixture.db,
		fixture.project.ID,
		fixture.principal.ID,
	); err != nil {
		t.Fatalf("revoke authenticated MCP project grant: %v", err)
	}

	var reachedCallback atomic.Bool
	_, err := runMCPProjectOperation(
		fixture.adapter,
		context.Background(),
		fixture.actor,
		func(
			context.Context,
			string,
			models.ProjectScope,
		) (struct{}, error) {
			reachedCallback.Store(true)
			return struct{}{}, nil
		},
	)
	if !errors.Is(err, services.ErrProjectAccessDenied) {
		t.Fatalf(
			"revoked MCP grant error=%v, want %v",
			err,
			services.ErrProjectAccessDenied,
		)
	}
	if reachedCallback.Load() {
		t.Fatal("MCP scoped callback executed after its project grant was revoked")
	}
	failure := backendError(err)
	if failure.Code != ProblemPolicyDenied {
		t.Fatalf(
			"revoked MCP grant code=%q, want %q",
			failure.Code,
			ProblemPolicyDenied,
		)
	}
}

func TestA2AProjectGrantRevokedAfterResolveBeforeShortTransactionFailsClosed(
	t *testing.T,
) {
	fixture := newA2AAdapterFixture(t)
	ticket := seedA2AQueryTicket(t, fixture, "A2A-GRANT-TOCTOU")
	projects, err := services.NewProjectService(fixture.db)
	if err != nil {
		t.Fatalf("create A2A project service: %v", err)
	}
	if _, err := projects.ResolvePrincipalProject(
		context.Background(),
		string(fixture.project.Key),
		fixture.principal.ID,
		models.ScopeTicketsRead,
	); err != nil {
		t.Fatalf("initial A2A project resolution: %v", err)
	}
	operationContext := a2aFixtureContext(t, fixture)
	if err := revokeAgentplatformProjectGrant(
		fixture.db,
		fixture.project.ID,
		fixture.principal.ID,
	); err != nil {
		t.Fatalf("revoke resolved A2A project grant: %v", err)
	}

	var ticketQueries atomic.Int64
	const callbackName = "test:reject_a2a_grant_before_business_query"
	if err := fixture.db.Callback().Query().
		Before("gorm:query").
		Register(callbackName, func(tx *gorm.DB) {
			if tx.Statement.Table == (models.Ticket{}).TableName() {
				ticketQueries.Add(1)
			}
		}); err != nil {
		t.Fatalf("register A2A business-query observer: %v", err)
	}
	t.Cleanup(func() {
		_ = fixture.db.Callback().Query().Remove(callbackName)
	})

	reporter := &recordingA2AReporter{}
	processErr := fixture.backend.Process(
		operationContext,
		a2a.Task{
			ID:        "task-grant-toctou",
			ContextID: "context-grant-toctou",
		},
		structuredA2AMessage(t, "ticket-query", map[string]any{
			"ticket_id": ticket.ID,
		}),
		reporter,
	)
	if processErr != nil {
		t.Fatalf("A2A revoked-grant rejection returned transport error: %v", processErr)
	}
	if got := reporter.lastState(); got != a2a.TaskStateRejected {
		t.Fatalf("A2A revoked-grant state=%q, want rejected", got)
	}
	if len(reporter.artifacts) != 0 {
		t.Fatalf("A2A returned business artifacts after grant revocation: %#v", reporter.artifacts)
	}
	if ticketQueries.Load() != 0 {
		t.Fatalf(
			"A2A executed %d Ticket queries after grant revocation",
			ticketQueries.Load(),
		)
	}
}

func revokeAgentplatformProjectGrant(
	db *gorm.DB,
	projectID uint,
	principalID string,
) error {
	return db.Model(&models.ProjectPrincipalGrant{}).
		Where(
			"project_id = ? AND service_principal_id = ?",
			projectID,
			principalID,
		).
		Update("is_active", false).Error
}
