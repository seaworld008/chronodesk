package agentplatform

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/seaworld008/chronodesk/server/internal/models"
)

func TestAgentTicketContentListQueryIsStrictAndBounded(t *testing.T) {
	tests := []struct {
		name      string
		raw       string
		wantLimit int
		wantErr   bool
	}{
		{name: "defaults", wantLimit: 25},
		{name: "minimum", raw: "limit=1", wantLimit: 1},
		{name: "maximum", raw: "limit=100", wantLimit: 100},
		{name: "cursor", raw: "cursor=opaque", wantLimit: 25},
		{name: "zero", raw: "limit=0", wantErr: true},
		{name: "over maximum", raw: "limit=101", wantErr: true},
		{name: "negative", raw: "limit=-1", wantErr: true},
		{name: "non integer", raw: "limit=many", wantErr: true},
		{name: "surrounding whitespace", raw: "limit=%2025", wantErr: true},
		{name: "duplicate limit", raw: "limit=25&limit=50", wantErr: true},
		{name: "duplicate cursor", raw: "cursor=a&cursor=b", wantErr: true},
		{name: "empty cursor", raw: "cursor=", wantErr: true},
		{name: "unknown", raw: "sort=created_at", wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			query, err := parseAgentTicketContentListQuery(test.raw)
			if test.wantErr {
				if err == nil {
					t.Fatalf("query %#v was accepted: %+v", test.raw, query)
				}
				return
			}
			if err != nil {
				t.Fatalf("parse query %#v: %v", test.raw, err)
			}
			if query.Limit != test.wantLimit {
				t.Fatalf(
					"query %#v limit=%d, want %d",
					test.raw,
					query.Limit,
					test.wantLimit,
				)
			}
		})
	}
	handler := &APIHandler{}
	if _, err := handler.decodeTicketContentListCursor(
		"",
		agentTicketCommentsList,
		models.ProjectScope{OrganizationID: 1, ProjectID: 1},
		1,
		25,
	); !errors.Is(err, errAgentTicketContentCursorKey) {
		t.Fatalf("unconfigured cursor error=%v", err)
	}
	if err := handler.ConfigureTicketContentListCursor(nil); !errors.Is(
		err,
		errAgentTicketContentCursorKey,
	) {
		t.Fatalf("empty cursor key error=%v", err)
	}
}

func TestAgentTicketCommentsUseStableBoundSignedCursor(t *testing.T) {
	gin.SetMode(gin.TestMode)
	fixture := newMCPAdapterFixture(t)
	ticket := fixture.seedTicket(t, "REST-COMMENT-CURSOR", "")
	otherTicket := fixture.seedTicket(t, "REST-COMMENT-CURSOR-OTHER", "")
	createdAt := time.Date(2026, time.August, 1, 8, 0, 0, 0, time.UTC)
	comments := make([]models.TicketComment, 151)
	for index := range comments {
		comments[index] = models.TicketComment{
			CreatedAt:      createdAt,
			UpdatedAt:      createdAt,
			TicketID:       ticket.ID,
			UserID:         &fixture.user.ID,
			ActorType:      models.ActorTypeHuman,
			ActorID:        strconv.FormatUint(uint64(fixture.user.ID), 10),
			Content:        fmt.Sprintf("comment-%03d", index+1),
			ContentType:    "text",
			Type:           models.CommentTypePublic,
			IsDeleted:      false,
			OrganizationID: fixture.organization.ID,
			ProjectID:      fixture.project.ID,
		}
	}
	if err := fixture.db.Create(&comments).Error; err != nil {
		t.Fatalf("seed comments: %v", err)
	}

	handler := NewAPIHandler(
		fixture.db,
		fixture.service,
		fixture.manager,
		1<<20,
		nil,
	)
	if err := handler.ConfigureTicketContentListCursor(
		[]byte("agent-ticket-content-pagination-test-key"),
	); err != nil {
		t.Fatalf("configure ticket content cursor: %v", err)
	}
	router := gin.New()
	handler.RegisterRoutes(router.Group("/api/v2/projects/:projectKey"))

	request := func(path string) *httptest.ResponseRecorder {
		t.Helper()
		httpRequest := httptest.NewRequest(http.MethodGet, path, nil)
		httpRequest.Header.Set("Authorization", "Bearer "+fixture.token)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httpRequest)
		return response
	}
	decode := func(response *httptest.ResponseRecorder) agentTicketCommentPage {
		t.Helper()
		if response.Code != http.StatusOK {
			t.Fatalf(
				"comment list status=%d body=%s",
				response.Code,
				response.Body.String(),
			)
		}
		var envelope struct {
			Data agentTicketCommentPage `json:"data"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
			t.Fatalf("decode comment cursor page: %v", err)
		}
		return envelope.Data
	}

	basePath := fmt.Sprintf(
		"/api/v2/projects/TEST/tickets/%d/comments",
		ticket.ID,
	)
	first := decode(request(basePath + "?limit=100"))
	if len(first.Items) != 100 || !first.HasMore || first.NextCursor == "" {
		t.Fatalf("first comment page = %+v", first)
	}
	if first.Items[0].ID != comments[150].ID ||
		first.Items[99].ID != comments[51].ID {
		t.Fatalf(
			"first comment IDs=%d..%d, want %d..%d",
			first.Items[0].ID,
			first.Items[99].ID,
			comments[150].ID,
			comments[51].ID,
		)
	}

	late := models.TicketComment{
		CreatedAt:      createdAt,
		UpdatedAt:      createdAt,
		TicketID:       ticket.ID,
		UserID:         &fixture.user.ID,
		ActorType:      models.ActorTypeHuman,
		ActorID:        strconv.FormatUint(uint64(fixture.user.ID), 10),
		Content:        "concurrent-later-comment",
		ContentType:    "text",
		Type:           models.CommentTypePublic,
		OrganizationID: fixture.organization.ID,
		ProjectID:      fixture.project.ID,
	}
	if err := fixture.db.Create(&late).Error; err != nil {
		t.Fatalf("insert concurrent comment: %v", err)
	}

	second := decode(request(
		basePath + "?limit=100&cursor=" +
			url.QueryEscape(first.NextCursor),
	))
	if len(second.Items) != 51 ||
		second.HasMore ||
		second.NextCursor != "" {
		t.Fatalf("second comment page = %+v", second)
	}
	seen := make(map[uint]struct{}, 151)
	for _, page := range []agentTicketCommentPage{first, second} {
		for _, item := range page.Items {
			if item.ID == late.ID {
				t.Fatal("concurrent later comment leaked into cursor continuation")
			}
			if _, duplicate := seen[item.ID]; duplicate {
				t.Fatalf("comment %d repeated across pages", item.ID)
			}
			seen[item.ID] = struct{}{}
		}
	}
	if len(seen) != 151 {
		t.Fatalf("stable comment pages returned %d unique rows, want 151", len(seen))
	}

	tampered := first.NextCursor
	if strings.HasSuffix(tampered, "A") {
		tampered = tampered[:len(tampered)-1] + "B"
	} else {
		tampered = tampered[:len(tampered)-1] + "A"
	}
	for name, path := range map[string]string{
		"tampered": basePath + "?limit=100&cursor=" +
			url.QueryEscape(tampered),
		"changed limit": basePath + "?limit=25&cursor=" +
			url.QueryEscape(first.NextCursor),
		"changed ticket": fmt.Sprintf(
			"/api/v2/projects/TEST/tickets/%d/comments?limit=100&cursor=%s",
			otherTicket.ID,
			url.QueryEscape(first.NextCursor),
		),
		"cross endpoint": fmt.Sprintf(
			"/api/v2/projects/TEST/tickets/%d/attachments?limit=100&cursor=%s",
			ticket.ID,
			url.QueryEscape(first.NextCursor),
		),
	} {
		response := request(path)
		if response.Code != http.StatusBadRequest {
			t.Errorf(
				"%s cursor status=%d body=%s",
				name,
				response.Code,
				response.Body.String(),
			)
		}
	}
	if _, err := handler.decodeTicketContentListCursor(
		first.NextCursor,
		agentTicketCommentsList,
		models.ProjectScope{
			OrganizationID: fixture.organization.ID,
			ProjectID:      fixture.project.ID + 1,
		},
		ticket.ID,
		100,
	); err == nil {
		t.Fatal("comment cursor was accepted across project scope")
	}
}

func TestAgentTicketAttachmentsReturnBoundedCursorPage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	fixture := newMCPAdapterFixture(t)
	ticket := fixture.seedTicket(t, "REST-ATTACHMENT-CURSOR", "")
	createdAt := time.Date(2026, time.August, 1, 9, 0, 0, 0, time.UTC)
	attachments := make([]models.TicketAttachment, 3)
	for index := range attachments {
		name := fmt.Sprintf("attachment-%d.txt", index+1)
		attachments[index] = models.TicketAttachment{
			CreatedAt:      createdAt,
			UpdatedAt:      createdAt,
			TicketID:       ticket.ID,
			ActorType:      models.ActorTypeServicePrincipal,
			ActorID:        fixture.principal.ID,
			FileName:       name,
			OriginalName:   name,
			FileSize:       1,
			MimeType:       "text/plain",
			FileType:       models.AttachmentTypeDocument,
			Extension:      ".txt",
			StoragePath:    "test/" + name,
			StorageType:    "local",
			Hash:           strings.Repeat(strconv.Itoa(index+1), 64),
			VirusScan:      models.VirusScanClean,
			OrganizationID: fixture.organization.ID,
			ProjectID:      fixture.project.ID,
		}
	}
	if err := fixture.db.Create(&attachments).Error; err != nil {
		t.Fatalf("seed attachments: %v", err)
	}

	handler := NewAPIHandler(
		fixture.db,
		fixture.service,
		fixture.manager,
		1<<20,
		nil,
	)
	if err := handler.ConfigureTicketContentListCursor(
		[]byte("agent-ticket-attachment-pagination-test-key"),
	); err != nil {
		t.Fatalf("configure ticket content cursor: %v", err)
	}
	router := gin.New()
	handler.RegisterRoutes(router.Group("/api/v2/projects/:projectKey"))
	path := fmt.Sprintf(
		"/api/v2/projects/TEST/tickets/%d/attachments?limit=2",
		ticket.ID,
	)
	request := httptest.NewRequest(http.MethodGet, path, nil)
	request.Header.Set("Authorization", "Bearer "+fixture.token)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf(
			"attachment list status=%d body=%s",
			response.Code,
			response.Body.String(),
		)
	}
	var envelope struct {
		Data agentTicketAttachmentPage `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode attachment cursor page: %v", err)
	}
	if len(envelope.Data.Items) != 2 ||
		!envelope.Data.HasMore ||
		envelope.Data.NextCursor == "" ||
		envelope.Data.Items[0].ID != attachments[2].ID ||
		envelope.Data.Items[1].ID != attachments[1].ID {
		t.Fatalf("attachment cursor page = %+v", envelope.Data)
	}
}
