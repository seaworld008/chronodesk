package agentplatform

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/services"
)

func TestAdminListHandlersRejectStrictQueryErrors(t *testing.T) {
	fixture := newAdminContractFixture(t)
	base := "/api/projects/TEST/admin/agents"

	for _, test := range []struct {
		name string
		path string
	}{
		{
			name: "overview unknown",
			path: base + "/agent-control/overview?unknown=1",
		},
		{
			name: "page zero",
			path: base + "/service-principals?page=0",
		},
		{
			name: "page size negative",
			path: base + "/service-principals?page_size=-1",
		},
		{
			name: "page size non integer",
			path: base + "/service-principals?page_size=twenty-five",
		},
		{
			name: "page size over maximum",
			path: base + "/service-principals?page_size=101",
		},
		{
			name: "page duplicate",
			path: base + "/service-principals?page=1&page=2",
		},
		{
			name: "page unknown",
			path: base + "/service-principals?offset=0",
		},
		{
			name: "principal sort is immutable",
			path: base + "/service-principals?sort_by=name&sort_order=asc",
		},
		{
			name: "policy sort is immutable",
			path: base + "/service-principals/missing/policies?sort_by=created_at&sort_order=desc",
		},
		{
			name: "lease sort is immutable",
			path: base + "/leases?sort_by=created_at&sort_order=desc",
		},
		{
			name: "cursor zero",
			path: base + "/events?limit=0",
		},
		{
			name: "cursor negative",
			path: base + "/events?limit=-1",
		},
		{
			name: "cursor non integer",
			path: base + "/events?limit=twenty-five",
		},
		{
			name: "cursor over maximum",
			path: base + "/events?limit=101",
		},
		{
			name: "cursor duplicate",
			path: base + "/events?limit=25&limit=50",
		},
		{
			name: "cursor unknown",
			path: base + "/events?sort_by=created_at",
		},
		{
			name: "decision cursor page mix",
			path: base + "/policy-decisions?page=1",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := performAdminContractRequest(
				fixture.router,
				http.MethodGet,
				test.path,
				"",
				"",
				"",
				"admin-list-strict-query",
			)
			if response.Code != http.StatusBadRequest {
				t.Fatalf(
					"GET %s status=%d body=%s, want 400",
					test.path,
					response.Code,
					response.Body.String(),
				)
			}
		})
	}
}

func TestAdminListHandlersServeAllSevenBoundedListContracts(t *testing.T) {
	fixture := newAdminContractFixture(t)
	principal, err := fixture.native.CreateServicePrincipal(
		context.Background(),
		services.CreateServicePrincipalInput{
			Name:   "administrator-list-contract-agent",
			Scopes: []string{models.ScopeTicketsRead},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	grantAPIHandlerTestProject(
		t,
		fixture.db,
		fixture.project,
		principal.ID,
		principal.ScopeList(),
	)
	if err := fixture.db.Create(&models.AgentPolicy{
		ID:                 "00000000-0000-7000-8e00-000000000001",
		ServicePrincipalID: principal.ID,
		Name:               "administrator list contract policy",
		Effect:             models.AgentPolicyEffectAllow,
		Scope:              models.ScopeTicketsRead,
		Action:             "ticket.read",
		ResourceType:       "ticket",
		Priority:           10,
		IsActive:           true,
	}).Error; err != nil {
		t.Fatal(err)
	}

	base := "/api/projects/TEST/admin/agents"
	for _, test := range []struct {
		name string
		path string
	}{
		{
			name: "principals page",
			path: base + "/service-principals?page=1&page_size=25&sort_by=created_at&sort_order=desc",
		},
		{
			name: "policies page",
			path: base + "/service-principals/" + principal.ID +
				"/policies?page=1&page_size=25&sort_by=priority&sort_order=desc",
		},
		{
			name: "leases page",
			path: base + "/leases?page=1&page_size=25&sort_by=expires_at&sort_order=asc",
		},
		{
			name: "attachments page",
			path: base + "/attachments?page=1&page_size=25&sort_by=created_at&sort_order=desc",
		},
		{
			name: "events cursor",
			path: base + "/events?limit=25",
		},
		{
			name: "outbox page",
			path: base + "/outbox?page=1&page_size=25&sort_by=created_at&sort_order=desc",
		},
		{
			name: "policy decisions cursor",
			path: base + "/policy-decisions?limit=25",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := performAdminContractRequest(
				fixture.router,
				http.MethodGet,
				test.path,
				"",
				"",
				"",
				"admin-list-success",
			)
			if response.Code != http.StatusOK {
				t.Fatalf(
					"GET %s status=%d body=%s, want 200",
					test.path,
					response.Code,
					response.Body.String(),
				)
			}
			var envelope struct {
				Data json.RawMessage `json:"data"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
				t.Fatalf("decode GET %s response: %v", test.path, err)
			}
			if len(envelope.Data) == 0 || string(envelope.Data) == "null" {
				t.Fatalf("GET %s returned no data envelope", test.path)
			}
		})
	}
}

func TestAdminListHandlerValidatesTrustedScopeBeforePagination(t *testing.T) {
	fixture := newAdminContractFixture(t)
	handler := NewAdminHandler(
		fixture.db,
		fixture.native,
		newTestRuntimeControl(t, fixture.db, fixture.native, false),
		0,
		[]byte("admin-list-scope-before-query-replay-key"),
	)
	lists, err := NewAdminListService(
		fixture.db,
		[]byte("admin-list-scope-before-query-cursor-key"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := handler.ConfigureListService(lists); err != nil {
		t.Fatal(err)
	}

	response := httptest.NewRecorder()
	context, _ := createAdminListTestContext(
		response,
		httptest.NewRequest(
			http.MethodGet,
			"/api/projects/TEST/admin/agents/events?limit=0",
			nil,
		),
		fixture.admin.ID,
	)
	handler.ListDomainEventsPage(context)
	if response.Code != http.StatusForbidden {
		t.Fatalf(
			"untrusted request status=%d body=%s, want scope rejection 403 before query 400",
			response.Code,
			response.Body.String(),
		)
	}
}

func createAdminListTestContext(
	response *httptest.ResponseRecorder,
	request *http.Request,
	userID uint,
) (*gin.Context, *gin.Engine) {
	context, engine := gin.CreateTestContext(response)
	context.Request = request
	context.Set("user_id", userID)
	context.Params = gin.Params{
		{Key: "projectKey", Value: "TEST"},
	}
	return context, engine
}
