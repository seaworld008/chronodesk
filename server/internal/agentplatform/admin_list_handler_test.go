package agentplatform

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
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
