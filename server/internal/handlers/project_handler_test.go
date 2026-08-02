package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/seaworld008/chronodesk/server/internal/middleware"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/scopeddb"
	"github.com/seaworld008/chronodesk/server/internal/services"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestPlatformProjectSummaryNeverExposesTrustedScopeOrProjectRole(
	t *testing.T,
) {
	payload, err := json.Marshal(newPlatformProjectSummary(models.Project{
		ID:             41,
		PublicID:       "019fb344-fa16-7e13-9c5b-08eb95478098",
		OrganizationID: 7,
		BusinessUnitID: 9,
		Key:            "OPS",
		Name:           "Operations",
		Description:    "Operations project",
		Status:         models.ProjectStatusActive,
	}))
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(payload, &fields); err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		"public_id",
		"key",
		"name",
		"description",
		"status",
	} {
		if _, ok := fields[required]; !ok {
			t.Errorf("platform project summary is missing %q: %s", required, payload)
		}
	}
	for _, forbidden := range []string{
		"id",
		"organization_id",
		"business_unit_id",
		"scope",
		"project_role",
		"role",
	} {
		if _, ok := fields[forbidden]; ok {
			t.Errorf("platform project summary exposes %q: %s", forbidden, payload)
		}
	}
}

func TestProjectQueueResponseNeverExposesPersistenceRelations(t *testing.T) {
	teamPublicID := "019fb344-fa16-7e13-9c5b-08eb95478099"
	teamName := "一线支持"
	payload, err := json.Marshal(projectQueueResponse{
		PublicID:     "019fb344-fa16-7e13-9c5b-08eb95478098",
		TeamPublicID: &teamPublicID,
		TeamName:     &teamName,
		Key:          models.QueueKey("support"),
		Name:         "技术支持",
		Description:  "默认支持队列",
		Status:       models.QueueStatusActive,
		IsDefault:    true,
	})
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(payload, &fields); err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		"public_id",
		"created_at",
		"updated_at",
		"team_public_id",
		"team_name",
		"key",
		"name",
		"description",
		"status",
		"is_default",
	} {
		if _, ok := fields[required]; !ok {
			t.Errorf("project queue response is missing %q: %s", required, payload)
		}
	}
	for _, forbidden := range []string{
		"id",
		"project",
		"project_id",
		"team",
		"team_id",
	} {
		if _, ok := fields[forbidden]; ok {
			t.Errorf("project queue response exposes %q: %s", forbidden, payload)
		}
	}
}

func TestProjectDirectoryAuthorizationPrecedesQueryValidation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := NewProjectHandler(nil)
	router := gin.New()
	router.GET("/memberships", func(c *gin.Context) {
		c.Set(projectAccessContextKey, services.ProjectAccess{
			Role: models.ProjectRoleObserver,
		})
		handler.ListMemberships(c)
	})
	router.GET("/queues", handler.ListQueues)

	memberships := httptest.NewRecorder()
	router.ServeHTTP(
		memberships,
		httptest.NewRequest(
			http.MethodGet,
			"/memberships?page_size=101",
			nil,
		),
	)
	if memberships.Code != http.StatusForbidden {
		t.Fatalf(
			"membership status = %d, body=%s",
			memberships.Code,
			memberships.Body.String(),
		)
	}
	queues := httptest.NewRecorder()
	router.ServeHTTP(
		queues,
		httptest.NewRequest(
			http.MethodGet,
			"/queues?page_size=101",
			nil,
		),
	)
	if queues.Code != http.StatusForbidden {
		t.Fatalf(
			"queue status = %d, body=%s",
			queues.Code,
			queues.Body.String(),
		)
	}
}

func TestAuthorizedProjectListUsesStrictBoundedDirectoryContract(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service, project, user, _ := projectHandlerTestService(t)
	handler := NewProjectHandler(service)
	router := gin.New()
	router.GET("/projects", func(c *gin.Context) {
		c.Set("user_id", user.ID)
		handler.List(c)
	})

	response := httptest.NewRecorder()
	router.ServeHTTP(
		response,
		httptest.NewRequest(
			http.MethodGet,
			"/projects?page=1&page_size=25&sort_by=name&sort_order=asc&search=Oper",
			nil,
		),
	)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", response.Code, response.Body.String())
	}
	var body struct {
		Data services.DirectoryPage[services.ProjectAccess] `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Data.Total != 1 || body.Data.TotalPages != 1 ||
		len(body.Data.Items) != 1 ||
		body.Data.Items[0].Project.PublicID != project.PublicID {
		t.Fatalf("unexpected authorized project page: %+v", body.Data)
	}

	for _, query := range []string{
		"?page=0",
		"?page_size=101",
		"?sort_by=id",
		"?sort_order=sideways",
		"?unknown=true",
		"?search=first&search=second",
		"?page=%ZZ",
		"?search=%FF",
	} {
		invalid := httptest.NewRecorder()
		router.ServeHTTP(
			invalid,
			httptest.NewRequest(http.MethodGet, "/projects"+query, nil),
		)
		if invalid.Code != http.StatusBadRequest {
			t.Fatalf(
				"query %q status = %d, want 400; body=%s",
				query,
				invalid.Code,
				invalid.Body.String(),
			)
		}
	}
}

func TestProjectArchiveHandlerRequiresCanonicalRFCUUIDv7(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := NewProjectHandler(nil)
	for _, projectPublicID := range []string{
		"not-a-uuid",
		"550e8400-e29b-41d4-a716-446655440000",
		"019FB344-FA16-7E13-9C5B-08EB95478098",
		"019fb344-fa16-7e13-7c5b-08eb95478098",
		"%20019fb344-fa16-7e13-9c5b-08eb95478098%20",
	} {
		t.Run(projectPublicID, func(t *testing.T) {
			router := gin.New()
			router.POST(
				"/projects/:projectPublicID/archive",
				handler.Archive,
			)
			response := httptest.NewRecorder()
			request := httptest.NewRequest(
				http.MethodPost,
				"/projects/"+projectPublicID+"/archive",
				nil,
			)
			router.ServeHTTP(response, request)
			if response.Code != http.StatusBadRequest {
				t.Fatalf(
					"status = %d, want 400; body=%s",
					response.Code,
					response.Body.String(),
				)
			}
		})
	}
}

func TestProjectArchiveHandlerReturnsClosedPlatformSummary(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service, project, user, db := projectHandlerTestService(t)
	if err := db.Model(&models.User{}).
		Where("id = ?", user.ID).
		Update(
			"platform_role",
			models.PlatformRolePlatformAdmin,
		).Error; err != nil {
		t.Fatal(err)
	}
	handler := NewProjectHandler(service)
	router := gin.New()
	router.POST(
		"/projects/:projectPublicID/archive",
		func(c *gin.Context) {
			c.Set("user_id", user.ID)
			handler.Archive(c)
		},
	)

	for attempt := 1; attempt <= 2; attempt++ {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(
			http.MethodPost,
			"/projects/"+project.PublicID+"/archive",
			nil,
		)
		router.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf(
				"attempt %d status = %d, body=%s",
				attempt,
				response.Code,
				response.Body.String(),
			)
		}
		var body struct {
			Code int                        `json:"code"`
			Msg  string                     `json:"msg"`
			Data map[string]json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if body.Code != 0 || body.Msg == "" {
			t.Fatalf("attempt %d response = %+v", attempt, body)
		}
		for _, required := range []string{
			"public_id",
			"key",
			"name",
			"description",
			"status",
		} {
			if _, ok := body.Data[required]; !ok {
				t.Errorf("response data is missing %q: %s", required, response.Body)
			}
		}
		for _, forbidden := range []string{
			"id",
			"organization_id",
			"business_unit_id",
			"scope",
			"project_role",
		} {
			if _, ok := body.Data[forbidden]; ok {
				t.Errorf("response data exposes %q: %s", forbidden, response.Body)
			}
		}
		var status models.ProjectStatus
		if err := json.Unmarshal(body.Data["status"], &status); err != nil {
			t.Fatal(err)
		}
		if status != models.ProjectStatusArchived {
			t.Errorf("attempt %d status = %q", attempt, status)
		}
	}

	var persisted models.Project
	if err := db.First(&persisted, project.ID).Error; err != nil {
		t.Fatal(err)
	}
	if persisted.Status != models.ProjectStatusArchived {
		t.Fatalf("persisted project status = %q", persisted.Status)
	}
}

func TestProjectArchiveHandlerReturnsNotFoundForUnknownPublicID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service, _, user, db := projectHandlerTestService(t)
	if err := db.Model(&models.User{}).
		Where("id = ?", user.ID).
		Update(
			"platform_role",
			models.PlatformRolePlatformAdmin,
		).Error; err != nil {
		t.Fatal(err)
	}
	handler := NewProjectHandler(service)
	router := gin.New()
	router.POST(
		"/projects/:projectPublicID/archive",
		func(c *gin.Context) {
			c.Set("user_id", user.ID)
			handler.Archive(c)
		},
	)
	response := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPost,
		"/projects/019fb344-fa16-7e13-9c5b-08eb95478099/archive",
		nil,
	)
	router.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf(
			"status = %d, want 404; body=%s",
			response.Code,
			response.Body.String(),
		)
	}
}

func TestProjectArchiveHandlerRevalidatesPlatformAdministrator(t *testing.T) {
	gin.SetMode(gin.TestMode)
	_, project, member, db := projectHandlerTestService(t)
	events := &countingProjectHandlerEventAppender{}
	service, err := services.NewProjectService(db, events)
	if err != nil {
		t.Fatal(err)
	}
	handler := NewProjectHandler(service)
	router := gin.New()
	router.POST(
		"/projects/:projectPublicID/archive",
		func(c *gin.Context) {
			c.Set("user_id", member.ID)
			handler.Archive(c)
		},
	)
	response := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPost,
		"/projects/"+project.PublicID+"/archive",
		nil,
	)
	router.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf(
			"status = %d, want 403; body=%s",
			response.Code,
			response.Body.String(),
		)
	}
	var persisted models.Project
	if err := db.First(&persisted, project.ID).Error; err != nil {
		t.Fatal(err)
	}
	if persisted.Status != models.ProjectStatusActive {
		t.Fatalf("unauthorized archive changed status to %q", persisted.Status)
	}
	if events.calls != 0 {
		t.Fatalf("unauthorized archive appended %d events", events.calls)
	}
}

func TestProjectArchiveErrorMappingIsStable(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, test := range []struct {
		name       string
		err        error
		wantStatus int
	}{
		{
			name:       "invalid public id",
			err:        services.ErrProjectPublicID,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "access denied",
			err:        services.ErrProjectAccessDenied,
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "not found",
			err:        services.ErrProjectNotFound,
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "inactive conflict",
			err:        services.ErrProjectInactive,
			wantStatus: http.StatusConflict,
		},
		{
			name:       "default project conflict",
			err:        services.ErrDefaultProjectArchive,
			wantStatus: http.StatusConflict,
		},
		{
			name:       "event writer unavailable",
			err:        services.ErrProjectEventWriter,
			wantStatus: http.StatusInternalServerError,
		},
		{
			name:       "wrapped internal error",
			err:        fmt.Errorf("archive failed: %w", context.DeadlineExceeded),
			wantStatus: http.StatusInternalServerError,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			router := gin.New()
			router.POST("/", func(c *gin.Context) {
				writeProjectArchiveError(
					c,
					middleware.NewResponseHelper(),
					fmt.Errorf("wrapped: %w", test.err),
				)
			})
			response := httptest.NewRecorder()
			router.ServeHTTP(
				response,
				httptest.NewRequest(http.MethodPost, "/", nil),
			)
			if response.Code != test.wantStatus {
				t.Fatalf(
					"status = %d, want %d; body=%s",
					response.Code,
					test.wantStatus,
					response.Body.String(),
				)
			}
			var body struct {
				Code int    `json:"code"`
				Msg  string `json:"msg"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body.Code != test.wantStatus || body.Msg == "" {
				t.Fatalf("error response = %+v", body)
			}
		})
	}
}

func TestProjectWriteHandlersRejectUnknownAndTrailingJSON(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := NewProjectHandler(nil)
	tests := []struct {
		name    string
		path    string
		handle  gin.HandlerFunc
		setup   gin.HandlerFunc
		payload string
	}{
		{
			name:   "membership rejects historical role",
			path:   "/memberships",
			handle: handler.UpsertMembership,
			setup:  projectAdminAccessForStrictJSON,
			payload: `{
				"user_id":42,
				"role":"admin"
			}`,
		},
		{
			name:   "membership rejects permissions",
			path:   "/memberships",
			handle: handler.UpsertMembership,
			setup:  projectAdminAccessForStrictJSON,
			payload: `{
				"user_id":42,
				"role":"agent",
				"permissions":["admin"]
			}`,
		},
		{
			name:    "membership rejects trailing JSON",
			path:    "/memberships",
			handle:  handler.UpsertMembership,
			setup:   projectAdminAccessForStrictJSON,
			payload: `{"user_id":42,"role":"agent"} {}`,
		},
		{
			name:   "project create rejects historical role",
			path:   "/projects",
			handle: handler.Create,
			payload: `{
				"organization_id":1,
				"business_unit_id":2,
				"key":"OPS",
				"name":"Operations",
				"role":"admin"
			}`,
		},
		{
			name:   "project create rejects permissions",
			path:   "/projects",
			handle: handler.Create,
			payload: `{
				"organization_id":1,
				"business_unit_id":2,
				"key":"OPS",
				"name":"Operations",
				"permissions":["admin"]
			}`,
		},
		{
			name:   "project create rejects trailing JSON",
			path:   "/projects",
			handle: handler.Create,
			payload: `{
				"organization_id":1,
				"business_unit_id":2,
				"key":"OPS",
				"name":"Operations"
			} {}`,
		},
		{
			name:   "project create rejects normalized legacy key",
			path:   "/projects",
			handle: handler.Create,
			payload: `{
				"organization_id":1,
				"business_unit_id":2,
				"key":" ops",
				"name":"Operations"
			}`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			router := gin.New()
			if test.setup != nil {
				router.Use(test.setup)
			}
			router.POST(test.path, test.handle)

			request := httptest.NewRequest(
				http.MethodPost,
				test.path,
				strings.NewReader(test.payload),
			)
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)

			if response.Code != http.StatusBadRequest {
				t.Fatalf(
					"status = %d, want %d; body=%s",
					response.Code,
					http.StatusBadRequest,
					response.Body.String(),
				)
			}
			var body struct {
				Code int    `json:"code"`
				Msg  string `json:"msg"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode project error: %v", err)
			}
			if body.Code != http.StatusBadRequest || body.Msg == "" {
				t.Fatalf("project error = %+v", body)
			}
		})
	}
}

func TestProjectMembershipHandlersRequireStrictExpectedVersion(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := NewProjectHandler(nil)

	for _, testCase := range []struct {
		name       string
		payload    string
		wantStatus int
	}{
		{
			name:       "missing",
			payload:    `{"user_id":42,"role":"requester"}`,
			wantStatus: http.StatusPreconditionRequired,
		},
		{
			name:       "null",
			payload:    `{"user_id":42,"role":"requester","expected_version":null}`,
			wantStatus: http.StatusPreconditionRequired,
		},
		{
			name:       "negative",
			payload:    `{"user_id":42,"role":"requester","expected_version":-1}`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "fractional",
			payload:    `{"user_id":42,"role":"requester","expected_version":1.5}`,
			wantStatus: http.StatusBadRequest,
		},
	} {
		t.Run("upsert_"+testCase.name, func(t *testing.T) {
			router := gin.New()
			router.POST(
				"/memberships",
				projectAdminAccessForStrictJSON,
				handler.UpsertMembership,
			)
			response := httptest.NewRecorder()
			request := httptest.NewRequest(
				http.MethodPost,
				"/memberships",
				bytes.NewBufferString(testCase.payload),
			)
			request.Header.Set("Content-Type", "application/json")
			router.ServeHTTP(response, request)
			if response.Code != testCase.wantStatus {
				t.Fatalf(
					"status = %d, want %d; body=%s",
					response.Code,
					testCase.wantStatus,
					response.Body.String(),
				)
			}
		})
	}

	for _, testCase := range []struct {
		name       string
		query      string
		wantStatus int
	}{
		{
			name:       "missing",
			wantStatus: http.StatusPreconditionRequired,
		},
		{
			name:       "empty",
			query:      "?expected_version=",
			wantStatus: http.StatusPreconditionRequired,
		},
		{
			name:       "negative",
			query:      "?expected_version=-1",
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "non_numeric",
			query:      "?expected_version=latest",
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "duplicate",
			query:      "?expected_version=1&expected_version=2",
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "unknown",
			query:      "?expected_version=1&force=true",
			wantStatus: http.StatusBadRequest,
		},
	} {
		t.Run("deactivate_"+testCase.name, func(t *testing.T) {
			router := gin.New()
			router.DELETE(
				"/memberships/:userID",
				projectAdminAccessForStrictJSON,
				handler.DeactivateMembership,
			)
			response := httptest.NewRecorder()
			router.ServeHTTP(
				response,
				httptest.NewRequest(
					http.MethodDelete,
					"/memberships/42"+testCase.query,
					nil,
				),
			)
			if response.Code != testCase.wantStatus {
				t.Fatalf(
					"status = %d, want %d; body=%s",
					response.Code,
					testCase.wantStatus,
					response.Body.String(),
				)
			}
		})
	}
}

func TestProjectListReturnsEmptySuccessWithoutMembership(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service, _, _, db := projectHandlerTestService(t)
	outsider := models.User{
		Username:     "no-project-membership",
		Email:        "no-project-membership@example.test",
		PlatformRole: models.PlatformRolePlatformAdmin,
		Status:       models.UserStatusActive,
	}
	if err := db.Create(&outsider).Error; err != nil {
		t.Fatal(err)
	}

	handler := NewProjectHandler(service)
	router := gin.New()
	router.GET("/projects", func(c *gin.Context) {
		c.Set("user_id", outsider.ID)
		handler.List(c)
	})
	request := httptest.NewRequest(http.MethodGet, "/projects", nil)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", response.Code, response.Body.String())
	}
	var body struct {
		Code int                                     `json:"code"`
		Msg  string                                  `json:"msg"`
		Data services.DirectoryPage[json.RawMessage] `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Code != 0 || body.Msg == "" ||
		body.Data.Page != 1 ||
		body.Data.PageSize != defaultDirectoryPageSize ||
		body.Data.Total != 0 ||
		body.Data.TotalPages != 0 ||
		len(body.Data.Items) != 0 {
		t.Fatalf("project list response = %+v", body)
	}
}

func TestPlatformProjectListReturnsClosedInventoryWithoutMembership(
	t *testing.T,
) {
	gin.SetMode(gin.TestMode)
	service, _, _, db := projectHandlerTestService(t)
	administrator := models.User{
		Username:     "platform-project-inventory-admin",
		Email:        "platform-project-inventory-admin@example.test",
		PasswordHash: "hash",
		PlatformRole: models.PlatformRolePlatformAdmin,
		Status:       models.UserStatusActive,
	}
	if err := db.Create(&administrator).Error; err != nil {
		t.Fatal(err)
	}

	handler := NewProjectHandler(service)
	router := gin.New()
	router.GET("/platform/projects", func(c *gin.Context) {
		c.Set("user_id", administrator.ID)
		handler.ListPlatform(c)
	})
	response := httptest.NewRecorder()
	router.ServeHTTP(
		response,
		httptest.NewRequest(http.MethodGet, "/platform/projects", nil),
	)
	if response.Code != http.StatusOK {
		t.Fatalf(
			"status = %d, want 200; body=%s",
			response.Code,
			response.Body.String(),
		)
	}
	var body struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			Items      []map[string]json.RawMessage `json:"items"`
			Total      int64                        `json:"total"`
			Page       int                          `json:"page"`
			PageSize   int                          `json:"page_size"`
			TotalPages int                          `json:"total_pages"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Code != 0 ||
		body.Msg == "" ||
		len(body.Data.Items) != 2 ||
		body.Data.Total != 2 ||
		body.Data.Page != 1 ||
		body.Data.PageSize != 25 ||
		body.Data.TotalPages != 1 {
		t.Fatalf("platform project list response = %+v", body)
	}
	for _, item := range body.Data.Items {
		for _, required := range []string{
			"public_id",
			"key",
			"name",
			"description",
			"status",
		} {
			if _, ok := item[required]; !ok {
				t.Errorf(
					"platform project summary is missing %q: %s",
					required,
					response.Body,
				)
			}
		}
		for _, forbidden := range []string{
			"id",
			"project_id",
			"organization_id",
			"business_unit_id",
			"scope",
			"project_role",
			"role",
			"membership",
		} {
			if _, ok := item[forbidden]; ok {
				t.Errorf(
					"platform project summary exposes %q: %s",
					forbidden,
					response.Body,
				)
			}
		}
	}
	var membershipCount int64
	if err := db.Model(&models.ProjectMembership{}).
		Where("user_id = ?", administrator.ID).
		Count(&membershipCount).Error; err != nil {
		t.Fatal(err)
	}
	if membershipCount != 0 {
		t.Fatalf(
			"platform administrator has %d Memberships, want none",
			membershipCount,
		)
	}
}

func TestPlatformProjectListHandlerFailsClosedForCurrentAccountState(
	t *testing.T,
) {
	gin.SetMode(gin.TestMode)
	service, _, _, db := projectHandlerTestService(t)
	handler := NewProjectHandler(service)

	for index, test := range []struct {
		name   string
		role   models.PlatformRole
		status models.UserStatus
	}{
		{
			name:   "member",
			role:   models.PlatformRoleMember,
			status: models.UserStatusActive,
		},
		{
			name:   "security auditor",
			role:   models.PlatformRoleSecurityAuditor,
			status: models.UserStatusActive,
		},
		{
			name:   "emergency operator",
			role:   models.PlatformRoleEmergencyOperator,
			status: models.UserStatusActive,
		},
		{
			name:   "inactive platform administrator",
			role:   models.PlatformRolePlatformAdmin,
			status: models.UserStatusInactive,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			user := models.User{
				Username:     "platform-project-handler-denied-" + strconv.Itoa(index),
				Email:        "platform-project-handler-denied-" + strconv.Itoa(index) + "@example.test",
				PasswordHash: "hash",
				PlatformRole: test.role,
				Status:       test.status,
			}
			if err := db.Create(&user).Error; err != nil {
				t.Fatal(err)
			}
			router := gin.New()
			router.GET("/platform/projects", func(c *gin.Context) {
				c.Set("user_id", user.ID)
				handler.ListPlatform(c)
			})
			response := httptest.NewRecorder()
			router.ServeHTTP(
				response,
				httptest.NewRequest(
					http.MethodGet,
					"/platform/projects",
					nil,
				),
			)
			if response.Code != http.StatusForbidden {
				t.Fatalf(
					"status = %d, want 403; body=%s",
					response.Code,
					response.Body.String(),
				)
			}
		})
	}
}

func TestPlatformProjectListRejectsUndocumentedQueryParameters(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := NewProjectHandler(
		mustProjectHandlerServiceWithoutEvents(t),
	)
	router := gin.New()
	router.GET("/platform/projects", handler.ListPlatform)
	response := httptest.NewRecorder()
	router.ServeHTTP(
		response,
		httptest.NewRequest(
			http.MethodGet,
			"/platform/projects?unknown=1",
			nil,
		),
	)
	if response.Code != http.StatusBadRequest {
		t.Fatalf(
			"status = %d, want 400; body=%s",
			response.Code,
			response.Body.String(),
		)
	}
}

func TestProjectGovernanceParsersRejectOffsetOverflow(t *testing.T) {
	query := url.Values{
		"page":      {strconv.Itoa(math.MaxInt)},
		"page_size": {"100"},
	}
	if _, err := parsePlatformProjectListQuery(query); err == nil {
		t.Fatal("platform project parser accepted an overflowing page offset")
	}
	if _, err := parseProjectUserSearchQuery(query); err == nil {
		t.Fatal("project user parser accepted an overflowing page offset")
	}
}

func mustProjectHandlerServiceWithoutEvents(
	t *testing.T,
) *services.ProjectService {
	t.Helper()
	db, err := gorm.Open(
		sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"),
		&gorm.Config{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(
		&models.User{},
		&models.Project{},
	); err != nil {
		t.Fatal(err)
	}
	service, err := services.NewProjectService(db)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func projectAdminAccessForStrictJSON(c *gin.Context) {
	c.Set(projectAccessContextKey, services.ProjectAccess{
		Role: models.ProjectRoleAdmin,
	})
	c.Next()
}

type projectHandlerEventAppender struct{}

func (projectHandlerEventAppender) AppendDomainEventTx(
	_ context.Context,
	_ *gorm.DB,
	_ services.DomainEventInput,
	_ []services.OutboxTarget,
) (*models.DomainEvent, error) {
	return &models.DomainEvent{ID: "project-handler-event"}, nil
}

type countingProjectHandlerEventAppender struct {
	calls int
}

func (appender *countingProjectHandlerEventAppender) AppendDomainEventTx(
	_ context.Context,
	_ *gorm.DB,
	_ services.DomainEventInput,
	_ []services.OutboxTarget,
) (*models.DomainEvent, error) {
	appender.calls++
	return &models.DomainEvent{ID: "project-handler-event"}, nil
}

func projectHandlerTestService(
	t *testing.T,
) (*services.ProjectService, models.Project, models.User, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(
		sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"),
		&gorm.Config{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(
		&models.User{},
		&models.Organization{},
		&models.BusinessUnit{},
		&models.Project{},
		&models.ProjectMembership{},
		&models.Queue{},
	); err != nil {
		t.Fatal(err)
	}
	organization := models.Organization{
		Slug:   "example",
		Name:   "Example",
		Status: models.OrganizationStatusActive,
	}
	if err := db.Create(&organization).Error; err != nil {
		t.Fatal(err)
	}
	unit := models.BusinessUnit{
		OrganizationID: organization.ID,
		Key:            "OPS",
		Name:           "Operations",
		Status:         models.BusinessUnitStatusActive,
	}
	if err := db.Create(&unit).Error; err != nil {
		t.Fatal(err)
	}
	project := models.Project{
		OrganizationID: organization.ID,
		BusinessUnitID: unit.ID,
		Key:            "OPS",
		Name:           "Operations",
		Status:         models.ProjectStatusActive,
	}
	if err := db.Create(&project).Error; err != nil {
		t.Fatal(err)
	}
	otherProject := models.Project{
		OrganizationID: organization.ID,
		BusinessUnitID: unit.ID,
		Key:            "OTHER",
		Name:           "Other Project",
		Status:         models.ProjectStatusActive,
	}
	if err := db.Create(&otherProject).Error; err != nil {
		t.Fatal(err)
	}
	user := models.User{
		Username:     "agent",
		Email:        "agent@example.test",
		PlatformRole: models.PlatformRoleMember,
		Status:       models.UserStatusActive,
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.ProjectMembership{
		ProjectID: project.ID,
		UserID:    user.ID,
		Role:      models.ProjectRoleAgent,
		IsActive:  true,
	}).Error; err != nil {
		t.Fatal(err)
	}
	service, err := services.NewProjectService(db, projectHandlerEventAppender{})
	if err != nil {
		t.Fatal(err)
	}
	return service, project, user, db
}

func TestProjectScopeMiddlewareBuildsTrustedOperationContext(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service, project, user, db := projectHandlerTestService(t)
	router := gin.New()
	group := router.Group("/api/projects/:projectKey")
	group.Use(func(c *gin.Context) {
		c.Set("user_id", user.ID)
		c.Set("platform_role", models.PlatformRoleMember)
		c.Next()
	})
	group.Use(ProjectScopeMiddleware(service, db))
	group.GET("/context", func(c *gin.Context) {
		operation, err := services.OperationContextFromContext(c.Request.Context())
		if err != nil {
			c.String(http.StatusInternalServerError, err.Error())
			return
		}
		c.JSON(http.StatusOK, operation)
	})

	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/projects/OPS/context", nil)
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	operation, err := services.OperationContextFromContext(
		mustProjectRequestContext(t, service, project, user),
	)
	if err != nil {
		t.Fatal(err)
	}
	if operation.Scope != project.Scope() ||
		operation.Actor != models.HumanActor(user.ID) {
		t.Fatalf("operation = %#v", operation)
	}
}

func TestProjectCommandScopeMiddlewareBuildsContextWithoutTransaction(
	t *testing.T,
) {
	gin.SetMode(gin.TestMode)
	service, project, user, _ := projectHandlerTestService(t)
	router := gin.New()
	group := router.Group("/api/projects/:projectKey")
	group.Use(func(c *gin.Context) {
		c.Set("user_id", user.ID)
		c.Next()
	})
	group.Use(ProjectCommandScopeMiddleware(service))
	group.DELETE("/memberships/:userID", func(c *gin.Context) {
		if scopeddb.HasTransaction(c.Request.Context()) {
			c.String(
				http.StatusInternalServerError,
				"command middleware opened a transaction",
			)
			return
		}
		operation, err := services.OperationContextFromContext(
			c.Request.Context(),
		)
		if err != nil {
			c.String(http.StatusInternalServerError, err.Error())
			return
		}
		access, ok := ProjectAccessFromGin(c)
		if !ok ||
			operation.Scope != project.Scope() ||
			access.Scope != project.Scope() ||
			operation.Actor != models.HumanActor(user.ID) {
			c.Status(http.StatusInternalServerError)
			return
		}
		c.Status(http.StatusNoContent)
	})

	response := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodDelete,
		"/api/projects/OPS/memberships/42",
		nil,
	)
	router.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf(
			"status = %d, body = %s",
			response.Code,
			response.Body.String(),
		)
	}
}

func TestProjectScopeMiddlewareRejectsCrossProjectAccess(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service, _, user, db := projectHandlerTestService(t)
	router := gin.New()
	router.GET(
		"/api/projects/:projectKey/context",
		func(c *gin.Context) {
			c.Set("user_id", user.ID)
			c.Set("platform_role", models.PlatformRoleMember)
		},
		ProjectScopeMiddleware(service, db),
		func(c *gin.Context) { c.Status(http.StatusNoContent) },
	)
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/projects/OTHER/context", nil)
	router.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if body := response.Body.String(); !strings.Contains(
		body,
		`"code":"project_access_revoked"`,
	) || !strings.Contains(
		body,
		`"msg":"当前项目访问权限已失效"`,
	) {
		t.Fatalf("unexpected cross-project error contract: %s", body)
	}
}

func TestProjectScopeResolutionErrorIsDistinctFromDomainDenial(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, test := range []struct {
		name           string
		write          func(*gin.Context)
		wantCode       any
		wantMessage    string
		wantStatusCode int
	}{
		{
			name: "scope access revoked",
			write: func(c *gin.Context) {
				writeProjectScopeResolutionError(
					c,
					middleware.NewResponseHelper(),
					services.ErrProjectAccessDenied,
				)
			},
			wantCode:       "project_access_revoked",
			wantMessage:    "当前项目访问权限已失效",
			wantStatusCode: http.StatusForbidden,
		},
		{
			name: "inactive scope revoked",
			write: func(c *gin.Context) {
				writeProjectScopeResolutionError(
					c,
					middleware.NewResponseHelper(),
					services.ErrProjectInactive,
				)
			},
			wantCode:       "project_access_revoked",
			wantMessage:    "当前项目访问权限已失效",
			wantStatusCode: http.StatusForbidden,
		},
		{
			name: "domain authorization denial",
			write: func(c *gin.Context) {
				writeProjectError(
					c,
					middleware.NewResponseHelper(),
					services.ErrProjectAccessDenied,
				)
			},
			wantCode:       float64(http.StatusForbidden),
			wantMessage:    "无权访问该项目",
			wantStatusCode: http.StatusForbidden,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(response)
			test.write(c)

			if response.Code != test.wantStatusCode {
				t.Fatalf(
					"status = %d, want %d; body=%s",
					response.Code,
					test.wantStatusCode,
					response.Body.String(),
				)
			}
			var body map[string]any
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if body["code"] != test.wantCode ||
				body["msg"] != test.wantMessage {
				t.Fatalf(
					"response = %#v, want code=%#v msg=%q",
					body,
					test.wantCode,
					test.wantMessage,
				)
			}
		})
	}
}

func TestRequireProjectRolesUsesExactResolvedMembershipRole(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, test := range []struct {
		name       string
		role       models.ProjectRole
		setAccess  bool
		wantStatus int
	}{
		{name: "project admin", role: models.ProjectRoleAdmin, setAccess: true, wantStatus: http.StatusNoContent},
		{name: "manager", role: models.ProjectRoleManager, setAccess: true, wantStatus: http.StatusForbidden},
		{name: "agent", role: models.ProjectRoleAgent, setAccess: true, wantStatus: http.StatusForbidden},
		{name: "requester", role: models.ProjectRoleRequester, setAccess: true, wantStatus: http.StatusForbidden},
		{name: "observer", role: models.ProjectRoleObserver, setAccess: true, wantStatus: http.StatusForbidden},
		{name: "unknown", role: models.ProjectRole("unknown"), setAccess: true, wantStatus: http.StatusForbidden},
		{name: "missing access", wantStatus: http.StatusForbidden},
	} {
		t.Run(test.name, func(t *testing.T) {
			router := gin.New()
			router.Use(func(c *gin.Context) {
				if test.setAccess {
					c.Set(projectAccessContextKey, services.ProjectAccess{
						Role: test.role,
					})
				}
				c.Next()
			})
			router.GET(
				"/agent-admin",
				RequireProjectRoles(models.ProjectRoleAdmin),
				func(c *gin.Context) { c.Status(http.StatusNoContent) },
			)

			response := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, "/agent-admin", nil)
			router.ServeHTTP(response, request)
			if response.Code != test.wantStatus {
				t.Fatalf(
					"status = %d, want %d, body=%s",
					response.Code,
					test.wantStatus,
					response.Body.String(),
				)
			}
		})
	}
}

func TestProjectScopeMiddlewareRollsBackUnsuccessfulRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service, project, user, db := projectHandlerTestService(t)
	target := models.User{
		Username:     "rollback-target",
		Email:        "rollback-target@example.test",
		PlatformRole: models.PlatformRoleMember,
		Status:       models.UserStatusActive,
	}
	if err := db.Create(&target).Error; err != nil {
		t.Fatal(err)
	}

	router := gin.New()
	group := router.Group("/api/projects/:projectKey")
	group.Use(func(c *gin.Context) {
		c.Set("user_id", user.ID)
		c.Set("platform_role", models.PlatformRoleMember)
		c.Next()
	})
	group.Use(ProjectScopeMiddleware(service, db))
	group.POST("/rollback", func(c *gin.Context) {
		if err := db.WithContext(c.Request.Context()).
			Create(&models.ProjectMembership{
				ProjectID: project.ID,
				UserID:    target.ID,
				Role:      models.ProjectRoleRequester,
				IsActive:  true,
			}).Error; err != nil {
			c.String(http.StatusInternalServerError, err.Error())
			return
		}
		c.JSON(http.StatusBadRequest, gin.H{"code": "rejected"})
	})

	response := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/projects/OPS/rollback",
		nil,
	)
	router.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf(
			"status = %d, body = %s",
			response.Code,
			response.Body.String(),
		)
	}

	var count int64
	if err := db.Model(&models.ProjectMembership{}).
		Where("project_id = ? AND user_id = ?", project.ID, target.ID).
		Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("unsuccessful project request committed %d memberships", count)
	}
}

func TestProjectScopeMiddlewareNeverEmitsSuccessWhenCommitFails(
	t *testing.T,
) {
	gin.SetMode(gin.TestMode)
	service, _, user, db := projectHandlerTestService(t)
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	if err := db.Exec("PRAGMA foreign_keys = ON").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`
		CREATE TABLE project_commit_parents (
			id INTEGER PRIMARY KEY
		)
	`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`
		CREATE TABLE project_commit_children (
			id INTEGER PRIMARY KEY,
			parent_id INTEGER NOT NULL,
			CONSTRAINT project_commit_parent_fk
				FOREIGN KEY (parent_id)
				REFERENCES project_commit_parents(id)
				DEFERRABLE INITIALLY DEFERRED
		)
	`).Error; err != nil {
		t.Fatal(err)
	}

	callbackCalled := false
	router := gin.New()
	group := router.Group("/api/projects/:projectKey")
	group.Use(func(c *gin.Context) {
		c.Set("user_id", user.ID)
		c.Set("platform_role", models.PlatformRoleMember)
		c.Next()
	})
	group.Use(ProjectScopeMiddleware(service, db))
	group.POST("/commit-failure", func(c *gin.Context) {
		if err := queueProjectAfterCommit(c, func() {
			callbackCalled = true
		}); err != nil {
			c.String(http.StatusInternalServerError, err.Error())
			return
		}
		result := db.WithContext(c.Request.Context()).Exec(`
			INSERT INTO project_commit_children (id, parent_id)
			VALUES (1, 999)
		`)
		if result.Error != nil {
			c.String(http.StatusInternalServerError, result.Error.Error())
			return
		}
		c.JSON(http.StatusOK, gin.H{"success": true})
	})

	response := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/projects/OPS/commit-failure",
		nil,
	)
	router.ServeHTTP(response, request)
	if response.Code != http.StatusInternalServerError {
		t.Fatalf(
			"status = %d, want 500, body=%s",
			response.Code,
			response.Body.String(),
		)
	}
	if strings.Contains(response.Body.String(), `"success":true`) {
		t.Fatalf(
			"commit failure emitted buffered success: %s",
			response.Body.String(),
		)
	}
	if callbackCalled {
		t.Fatal("commit failure executed a project after-commit callback")
	}
	var count int64
	if err := db.Table("project_commit_children").Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("failed commit persisted %d child rows", count)
	}
}

func TestProjectScopeMiddlewareRunsCallbacksOnlyAfterCommit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service, _, user, db := projectHandlerTestService(t)
	callbackCalled := false
	callbackHadTransaction := true

	router := gin.New()
	group := router.Group("/api/projects/:projectKey")
	group.Use(func(c *gin.Context) {
		c.Set("user_id", user.ID)
		c.Set("platform_role", models.PlatformRoleMember)
		c.Next()
	})
	group.Use(ProjectScopeMiddleware(service, db))
	group.POST("/after-commit", func(c *gin.Context) {
		if err := queueProjectAfterCommit(c, func() {
			callbackCalled = true
			callbackHadTransaction = scopeddb.HasTransaction(
				c.Request.Context(),
			)
		}); err != nil {
			c.String(http.StatusInternalServerError, err.Error())
			return
		}
		c.JSON(http.StatusOK, gin.H{"success": true})
	})

	response := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/projects/OPS/after-commit",
		nil,
	)
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf(
			"status = %d, body=%s",
			response.Code,
			response.Body.String(),
		)
	}
	if !callbackCalled || callbackHadTransaction {
		t.Fatalf(
			"after-commit callback state = called:%v transaction:%v",
			callbackCalled,
			callbackHadTransaction,
		)
	}
}

func TestProjectMembershipHandlerCreatesExplicitGrant(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service, project, administrator, db := projectHandlerTestService(t)
	target := models.User{
		Username:     "project-target",
		Email:        "project-target@example.test",
		PlatformRole: models.PlatformRoleMember,
		Status:       models.UserStatusActive,
	}
	if err := db.Create(&target).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&models.ProjectMembership{}).
		Where("project_id = ? AND user_id = ?", project.ID, administrator.ID).
		Update("role", models.ProjectRoleAdmin).Error; err != nil {
		t.Fatal(err)
	}
	handler := NewProjectHandler(service)
	router := gin.New()
	group := router.Group("/api/projects/:projectKey")
	group.Use(func(c *gin.Context) {
		c.Set("user_id", administrator.ID)
		c.Set("platform_role", models.PlatformRoleMember)
		c.Next()
	})
	group.Use(ProjectCommandScopeMiddleware(service))
	group.POST("/memberships", handler.UpsertMembership)

	response := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/projects/OPS/memberships",
		bytes.NewBufferString(
			`{"user_id":`+
				strconv.FormatUint(uint64(target.ID), 10)+
				`,"role":"requester","knowledge_contributor":true,"expected_version":0}`,
		),
	)
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	staleResponse := httptest.NewRecorder()
	staleRequest := httptest.NewRequest(
		http.MethodPost,
		"/api/projects/OPS/memberships",
		bytes.NewBufferString(
			`{"user_id":`+
				strconv.FormatUint(uint64(target.ID), 10)+
				`,"role":"observer","knowledge_contributor":false,"expected_version":0}`,
		),
	)
	staleRequest.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(staleResponse, staleRequest)
	if staleResponse.Code != http.StatusConflict ||
		!strings.Contains(staleResponse.Body.String(), "刷新成员列表后重试") {
		t.Fatalf(
			"stale status = %d, body = %s",
			staleResponse.Code,
			staleResponse.Body.String(),
		)
	}
	var membership models.ProjectMembership
	if err := db.Where(
		"project_id = ? AND user_id = ?",
		project.ID,
		target.ID,
	).First(&membership).Error; err != nil {
		t.Fatal(err)
	}
	if !membership.IsActive ||
		membership.Role != models.ProjectRoleRequester ||
		!membership.KnowledgeContributor ||
		membership.Version != 1 {
		t.Fatalf("unexpected project membership: %+v", membership)
	}
}

func TestProjectMembershipDeactivateRouteUsesCommandOwnedTransaction(
	t *testing.T,
) {
	gin.SetMode(gin.TestMode)
	service, project, administrator, db := projectHandlerTestService(t)
	if err := db.Model(&models.ProjectMembership{}).
		Where(
			"project_id = ? AND user_id = ?",
			project.ID,
			administrator.ID,
		).
		Update("role", models.ProjectRoleAdmin).Error; err != nil {
		t.Fatal(err)
	}
	target := models.User{
		Username:     "project-deactivate-target",
		Email:        "project-deactivate-target@example.test",
		PlatformRole: models.PlatformRoleMember,
		Status:       models.UserStatusActive,
	}
	if err := db.Create(&target).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.ProjectMembership{
		ProjectID: project.ID,
		UserID:    target.ID,
		Role:      models.ProjectRoleAgent,
		IsActive:  true,
		Version:   1,
	}).Error; err != nil {
		t.Fatal(err)
	}

	handler := NewProjectHandler(service)
	router := gin.New()
	group := router.Group("/api/projects/:projectKey")
	group.Use(func(c *gin.Context) {
		c.Set("user_id", administrator.ID)
		c.Next()
	})
	group.Use(ProjectCommandScopeMiddleware(service))
	group.DELETE(
		"/memberships/:userID",
		handler.DeactivateMembership,
	)

	staleResponse := httptest.NewRecorder()
	staleRequest := httptest.NewRequest(
		http.MethodDelete,
		"/api/projects/OPS/memberships/"+
			strconv.FormatUint(uint64(target.ID), 10)+
			"?expected_version=2",
		nil,
	)
	router.ServeHTTP(staleResponse, staleRequest)
	if staleResponse.Code != http.StatusConflict ||
		!strings.Contains(staleResponse.Body.String(), "刷新成员列表后重试") {
		t.Fatalf(
			"stale status = %d, body = %s",
			staleResponse.Code,
			staleResponse.Body.String(),
		)
	}

	response := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodDelete,
		"/api/projects/OPS/memberships/"+
			strconv.FormatUint(uint64(target.ID), 10)+
			"?expected_version=1",
		nil,
	)
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf(
			"status = %d, body = %s",
			response.Code,
			response.Body.String(),
		)
	}
	var membership models.ProjectMembership
	if err := db.Where(
		"project_id = ? AND user_id = ?",
		project.ID,
		target.ID,
	).Take(&membership).Error; err != nil {
		t.Fatal(err)
	}
	if membership.IsActive || membership.Version != 2 {
		t.Fatalf("deactivated membership = %+v", membership)
	}
}

func mustProjectRequestContext(
	t *testing.T,
	service *services.ProjectService,
	project models.Project,
	user models.User,
) context.Context {
	t.Helper()
	access, err := service.ResolveHumanProject(
		context.Background(),
		string(project.Key),
		user.ID,
	)
	if err != nil {
		t.Fatal(err)
	}
	ctx, err := services.WithOperationContext(
		context.Background(),
		services.OperationContext{
			Scope:  access.Scope,
			Actor:  models.HumanActor(user.ID),
			Source: services.SourceProtocolHumanREST,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return ctx
}
