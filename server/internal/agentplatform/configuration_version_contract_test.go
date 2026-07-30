package agentplatform

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/seaworld008/chronodesk/server/internal/agentauth"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/services"
)

func TestNormalizeMachineConfigurationVersionIDRequiresCanonicalUUID(t *testing.T) {
	valid := "018f0f95-9e85-7a2b-8c3d-1234567890ab"
	if normalized, ok := normalizeMachineConfigurationVersionID(valid); !ok ||
		normalized != valid {
		t.Fatalf("normalize canonical UUID = (%q,%t)", normalized, ok)
	}
	for _, invalid := range []string{
		"",
		"not-a-uuid",
		"018f0f959e857a2b8c3d1234567890ab",
		"urn:uuid:018f0f95-9e85-7a2b-8c3d-1234567890ab",
	} {
		if normalized, ok := normalizeMachineConfigurationVersionID(invalid); ok {
			t.Fatalf("accepted non-canonical UUID %q as %q", invalid, normalized)
		}
	}
}

func TestAgentRESTCreateRequiresAndPersistsConfigurationVersions(t *testing.T) {
	gin.SetMode(gin.TestMode)
	fixture := newMCPAdapterFixture(t)
	handler := &APIHandler{db: fixture.db, native: fixture.service}
	operationContext, err := services.WithOperationContext(
		context.Background(),
		services.OperationContext{
			Scope:        fixture.project.Scope(),
			Actor:        models.ServicePrincipalActor(fixture.principal.ID),
			Source:       services.SourceProtocolAgentREST,
			CredentialID: fixture.credential.ID,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(agentauth.ContextPrincipalID, fixture.principal.ID)
		c.Set(agentauth.ContextCredentialID, fixture.credential.ID)
		c.Request = c.Request.WithContext(operationContext)
		c.Next()
	})
	router.POST("/tickets", handler.CreateTicket)

	payload := map[string]any{
		"title":                   "Version-bound REST ticket",
		"description":             "Machine intake selects immutable configuration.",
		"type":                    "request",
		"priority":                "normal",
		"request_type_version_id": fixture.requestTypeVersionID,
		"workflow_version_id":     fixture.workflowVersionID,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(
		http.MethodPost,
		"/tickets",
		bytes.NewReader(body),
	)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "rest-version-create-0001")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var ticket models.Ticket
	if err := fixture.db.Where(
		"title = ?",
		payload["title"],
	).First(&ticket).Error; err != nil {
		t.Fatal(err)
	}
	if ticket.RequestTypeVersionID != fixture.requestTypeVersionID ||
		ticket.WorkflowVersionID != fixture.workflowVersionID {
		t.Fatalf(
			"persisted versions = (%q,%q)",
			ticket.RequestTypeVersionID,
			ticket.WorkflowVersionID,
		)
	}
}

func TestAgentRESTCreateRejectsMissingConfigurationVersionsBeforeWrite(
	t *testing.T,
) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(agentauth.ContextPrincipalID, "service-principal-test")
		c.Set(agentauth.ContextCredentialID, "credential-test")
		c.Next()
	})
	router.POST("/tickets", (&APIHandler{}).CreateTicket)
	request := httptest.NewRequest(
		http.MethodPost,
		"/tickets",
		bytes.NewBufferString(
			`{"title":"Missing versions","description":"Rejected","type":"request","priority":"normal"}`,
		),
	)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "rest-version-missing-0001")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var problem Problem
	if err := json.Unmarshal(response.Body.Bytes(), &problem); err != nil {
		t.Fatal(err)
	}
	if problem.Code != ProblemInvalidRequest {
		t.Fatalf("problem=%+v", problem)
	}
}
