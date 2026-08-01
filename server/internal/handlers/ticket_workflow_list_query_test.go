package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestTicketPreviewListQueryIsStrictAndBounded(t *testing.T) {
	gin.SetMode(gin.TestMode)
	allowed := map[string]struct{}{
		"status":      {},
		"priority":    {},
		"category_id": {},
	}
	for _, test := range []struct {
		query    string
		ok       bool
		page     int
		pageSize int
	}{
		{query: "", ok: true, page: 1, pageSize: 25},
		{
			query:    "page=2&page_size=10&sort_by=created_at&sort_order=asc&status=open&priority=high",
			ok:       true,
			page:     2,
			pageSize: 10,
		},
		{query: "category_id=42&page_size=100", ok: true, page: 1, pageSize: 100},
		{query: "page=0"},
		{query: "page=-1"},
		{query: "page_size=101"},
		{query: "page_size="},
		{query: "page_size=25&page_size=50"},
		{query: "page_size=%2025"},
		{query: "sort_by=id"},
		{query: "sort_order=sideways"},
		{query: "limit=10"},
		{query: "status=unknown"},
		{query: "priority=unknown"},
		{query: "category_id=0"},
		{query: "category_id=abc"},
		{query: "unknown=value"},
	} {
		t.Run(test.query, func(t *testing.T) {
			context, _ := gin.CreateTestContext(httptest.NewRecorder())
			context.Request = httptest.NewRequest(
				http.MethodGet,
				"/tickets?"+test.query,
				nil,
			)
			parsed, ok := requireTicketPreviewListQuery(context, allowed)
			if ok != test.ok {
				t.Fatalf("ok=%t, want %t", ok, test.ok)
			}
			if ok &&
				(parsed.Page != test.page || parsed.PageSize != test.pageSize) {
				t.Fatalf(
					"page=%d page_size=%d, want %d/%d",
					parsed.Page,
					parsed.PageSize,
					test.page,
					test.pageSize,
				)
			}
		})
	}
}
