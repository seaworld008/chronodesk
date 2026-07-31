package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/seaworld008/chronodesk/server/internal/services"
)

func TestAutomationRuleListQueryIsStrict(t *testing.T) {
	gin.SetMode(gin.TestMode)
	valid, _ := automationListQueryTestContext(t, "")
	query, ok := requireAutomationRuleListQuery(valid)
	if !ok || query.page != 1 ||
		query.pageSize != services.DefaultAutomationListSize {
		t.Fatalf("defaults = %+v ok=%v", query, ok)
	}
	valid, _ = automationListQueryTestContext(
		t,
		"page=2&page_size=100&is_active=false&rule_type=assignment&"+
			"sort=%5B%22priority%22%2C%22ASC%22%5D",
	)
	query, ok = requireAutomationRuleListQuery(valid)
	if !ok || query.page != 2 || query.pageSize != 100 ||
		query.isActive == nil || *query.isActive {
		t.Fatalf("valid query = %+v ok=%v", query, ok)
	}

	for _, raw := range []string{
		"unknown=value",
		"page=1&page=2",
		"page=",
		"page=0",
		"page=-1",
		"page=abc",
		"page_size=0",
		"page_size=101",
		"is_active=0",
		"is_active=TRUE",
		"rule_type=bad-type",
		"sort=%5B%22created_at%22%2C%22DESC%22%5D",
	} {
		t.Run(raw, func(t *testing.T) {
			ctx, response := automationListQueryTestContext(t, raw)
			if _, ok := requireAutomationRuleListQuery(ctx); ok {
				t.Fatal("invalid query accepted")
			}
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d body=%s", response.Code, response.Body)
			}
		})
	}
}

func TestAutomationExecutionLogQueryIsStrict(t *testing.T) {
	gin.SetMode(gin.TestMode)
	valid, _ := automationListQueryTestContext(
		t,
		"limit=100&rule_id=7&ticket_id=9&success=false&cursor=opaque",
	)
	query, ok := requireAutomationExecutionLogQuery(valid)
	if !ok || query.Limit != 100 || query.RuleID == nil ||
		*query.RuleID != 7 || query.TicketID == nil ||
		*query.TicketID != 9 || query.Success == nil ||
		*query.Success || query.Cursor != "opaque" {
		t.Fatalf("query = %+v ok=%v", query, ok)
	}
	for _, raw := range []string{
		"page=1",
		"limit=25&limit=50",
		"limit=",
		"limit=0",
		"limit=-1",
		"limit=abc",
		"limit=101",
		"rule_id=0",
		"rule_id=-1",
		"rule_id=abc",
		"ticket_id=0",
		"success=0",
		"success=yes",
		"cursor=",
	} {
		t.Run(raw, func(t *testing.T) {
			ctx, response := automationListQueryTestContext(t, raw)
			if _, ok := requireAutomationExecutionLogQuery(ctx); ok {
				t.Fatal("invalid query accepted")
			}
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d body=%s", response.Code, response.Body)
			}
		})
	}
}

func TestWebhookListQueriesAreStrict(t *testing.T) {
	gin.SetMode(gin.TestMode)
	definition, _ := automationListQueryTestContext(
		t,
		"page=2&page_size=100&provider=custom&status=active",
	)
	page, ok := requireWebhookDefinitionQuery(definition)
	if !ok || page.Page != 2 || page.PageSize != 100 {
		t.Fatalf("definition query = %+v ok=%v", page, ok)
	}
	delivery, _ := automationListQueryTestContext(
		t,
		"limit=100&status=failed&event_type="+
			"io.chronodesk.system.alert.v1&cursor=opaque",
	)
	cursor, ok := requireWebhookDeliveryQuery(delivery)
	if !ok || cursor.Limit != 100 || cursor.Status != "failed" ||
		cursor.Cursor != "opaque" {
		t.Fatalf("delivery query = %+v ok=%v", cursor, ok)
	}

	for _, test := range []struct {
		name       string
		raw        string
		definition bool
	}{
		{name: "unknown", raw: "unknown=value", definition: true},
		{name: "duplicate", raw: "page=1&page=2", definition: true},
		{name: "empty", raw: "page=", definition: true},
		{name: "zero", raw: "page=0", definition: true},
		{name: "negative", raw: "page=-1", definition: true},
		{name: "non integer", raw: "page=abc", definition: true},
		{name: "oversize", raw: "page_size=101", definition: true},
		{name: "provider", raw: "provider=unknown", definition: true},
		{name: "definition status", raw: "status=pending", definition: true},
		{name: "old page", raw: "page=1", definition: false},
		{name: "cursor duplicate", raw: "cursor=a&cursor=b", definition: false},
		{name: "cursor empty", raw: "cursor=", definition: false},
		{name: "limit zero", raw: "limit=0", definition: false},
		{name: "limit oversize", raw: "limit=101", definition: false},
		{name: "delivery status", raw: "status=error", definition: false},
		{name: "event type", raw: "event_type=ticket.created", definition: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, response := automationListQueryTestContext(t, test.raw)
			if test.definition {
				_, ok = requireWebhookDefinitionQuery(ctx)
			} else {
				_, ok = requireWebhookDeliveryQuery(ctx)
			}
			if ok {
				t.Fatal("invalid query accepted")
			}
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d body=%s", response.Code, response.Body)
			}
		})
	}
}

func automationListQueryTestContext(
	t *testing.T,
	rawQuery string,
) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	response := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(response)
	target := "/list"
	if rawQuery != "" {
		target += "?" + rawQuery
	}
	context.Request = httptest.NewRequest(http.MethodGet, target, nil)
	return context, response
}
