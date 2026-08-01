package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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
		"rule_type=unknown",
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

func TestAutomationConfigurationListQueriesAreStrict(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name  string
		raw   string
		parse func(*gin.Context) (automationConfigListQuery, bool)
		check func(*testing.T, automationConfigListQuery)
	}{
		{
			name:  "SLA defaults",
			parse: requireSLAConfigListQuery,
			check: func(t *testing.T, query automationConfigListQuery) {
				t.Helper()
				if query.page != 1 ||
					query.pageSize != services.DefaultAutomationListSize {
					t.Fatalf("defaults = %+v", query)
				}
			},
		},
		{
			name:  "SLA filters",
			raw:   "page=2&page_size=100&is_active=false",
			parse: requireSLAConfigListQuery,
			check: func(t *testing.T, query automationConfigListQuery) {
				t.Helper()
				if query.page != 2 || query.pageSize != 100 ||
					query.isActive == nil || *query.isActive {
					t.Fatalf("query = %+v", query)
				}
			},
		},
		{
			name:  "template filters",
			raw:   "category=incident&is_active=true&page=3&page_size=50",
			parse: requireTicketTemplateListQuery,
			check: func(t *testing.T, query automationConfigListQuery) {
				t.Helper()
				if query.category != "incident" ||
					query.isActive == nil || !*query.isActive ||
					query.page != 3 || query.pageSize != 50 {
					t.Fatalf("query = %+v", query)
				}
			},
		},
		{
			name:  "quick reply filters",
			raw:   "category=network&keyword=timeout&is_public=true",
			parse: requireQuickReplyListQuery,
			check: func(t *testing.T, query automationConfigListQuery) {
				t.Helper()
				if query.category != "network" ||
					query.keyword != "timeout" ||
					query.isPublic == nil || !*query.isPublic {
					t.Fatalf("query = %+v", query)
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			context, response := automationListQueryTestContext(t, test.raw)
			query, ok := test.parse(context)
			if !ok || response.Code != http.StatusOK {
				t.Fatalf("query = %+v ok=%v status=%d body=%s", query, ok, response.Code, response.Body)
			}
			test.check(t, query)
		})
	}

	for _, test := range []struct {
		name  string
		raw   string
		parse func(*gin.Context) (automationConfigListQuery, bool)
	}{
		{name: "unknown", raw: "unknown=value", parse: requireSLAConfigListQuery},
		{name: "duplicate", raw: "page=1&page=2", parse: requireSLAConfigListQuery},
		{name: "empty", raw: "page=", parse: requireSLAConfigListQuery},
		{name: "zero", raw: "page=0", parse: requireSLAConfigListQuery},
		{name: "negative", raw: "page=-1", parse: requireSLAConfigListQuery},
		{name: "oversize", raw: "page_size=101", parse: requireSLAConfigListQuery},
		{name: "invalid boolean", raw: "is_active=TRUE", parse: requireSLAConfigListQuery},
		{name: "empty filter", raw: "category=", parse: requireTicketTemplateListQuery},
		{name: "trimmed filter", raw: "category=%20incident", parse: requireTicketTemplateListQuery},
		{name: "control filter", raw: "keyword=line%0Abreak", parse: requireQuickReplyListQuery},
		{name: "invalid UTF8", raw: "keyword=%FF", parse: requireQuickReplyListQuery},
		{
			name:  "category too long",
			raw:   "category=" + strings.Repeat("x", services.MaxAutomationCategoryFilterLength+1),
			parse: requireTicketTemplateListQuery,
		},
		{
			name:  "keyword too long",
			raw:   "keyword=" + strings.Repeat("x", services.MaxAutomationKeywordFilterLength+1),
			parse: requireQuickReplyListQuery,
		},
		{
			name:  "offset overflow",
			raw:   "page=999999999999999999999999&page_size=100",
			parse: requireQuickReplyListQuery,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			context, response := automationListQueryTestContext(t, test.raw)
			if _, ok := test.parse(context); ok {
				t.Fatal("invalid query accepted")
			}
			if response.Code != http.StatusBadRequest {
				t.Fatalf(
					"status=%d body=%s",
					response.Code,
					response.Body.String(),
				)
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

func TestWebhookStatsDaysQueryIsStrict(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, test := range []struct {
		raw  string
		want int
	}{
		{raw: "", want: 7},
		{raw: "days=1", want: 1},
		{raw: "days=90", want: 90},
	} {
		t.Run("valid "+test.raw, func(t *testing.T) {
			ctx, _ := automationListQueryTestContext(t, test.raw)
			days, ok := requireWebhookStatsDays(ctx)
			if !ok || days != test.want {
				t.Fatalf("days=%d ok=%v, want %d", days, ok, test.want)
			}
		})
	}

	for _, raw := range []string{
		"days=",
		"days=0",
		"days=-1",
		"days=91",
		"days=abc",
		"days=%207",
		"days=7&days=8",
		"page=1",
		"days=%ZZ",
	} {
		t.Run("invalid "+raw, func(t *testing.T) {
			ctx, response := automationListQueryTestContext(t, raw)
			if _, ok := requireWebhookStatsDays(ctx); ok {
				t.Fatal("invalid stats query accepted")
			}
			if response.Code != http.StatusBadRequest {
				t.Fatalf(
					"status=%d body=%s",
					response.Code,
					response.Body.String(),
				)
			}
		})
	}
}

func TestWebhookListQueryErrorUsesClosedStandardEnvelope(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, response := automationListQueryTestContext(t, "unknown=value")
	if _, ok := requireWebhookDefinitionQuery(ctx); ok {
		t.Fatal("invalid Webhook query accepted")
	}
	var payload map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload) != 3 ||
		payload["code"] != float64(1) ||
		payload["msg"] != "列表查询参数无效" {
		t.Fatalf("Webhook error envelope = %#v", payload)
	}
	if _, present := payload["data"]; !present {
		t.Fatalf("Webhook error envelope omits data: %#v", payload)
	}
	if _, present := payload["error"]; present {
		t.Fatalf("Webhook error envelope exposes unpublished error: %#v", payload)
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
