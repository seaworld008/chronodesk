package app

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/seaworld008/chronodesk/server/internal/auth"
	"github.com/seaworld008/chronodesk/server/internal/handlers"
)

const platformProjectArchiveTestID = "019fb344-fa16-7e13-9c5b-08eb95478098"

func TestPlatformProjectArchiveRouteUsesExactPlatformAdminMatrix(
	t *testing.T,
) {
	gin.SetMode(gin.TestMode)
	for _, test := range []struct {
		name       string
		role       any
		setRole    bool
		wantStatus int
	}{
		{
			name:       "platform administrator reaches handler",
			role:       auth.PlatformRolePlatformAdmin,
			setRole:    true,
			wantStatus: http.StatusInternalServerError,
		},
		{
			name:       "security auditor denied",
			role:       auth.PlatformRoleSecurityAuditor,
			setRole:    true,
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "emergency operator denied",
			role:       auth.PlatformRoleEmergencyOperator,
			setRole:    true,
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "member denied",
			role:       auth.PlatformRoleMember,
			setRole:    true,
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "untyped role denied",
			role:       string(auth.PlatformRolePlatformAdmin),
			setRole:    true,
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "missing role denied",
			wantStatus: http.StatusForbidden,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			router := gin.New()
			platformAdmin := router.Group("/api/platform")
			platformAdmin.Use(func(c *gin.Context) {
				c.Set("user_id", uint(7))
				if test.setRole {
					c.Set("platform_role", test.role)
				}
			})
			platformAdmin.Use(ginAdapter(
				(&auth.AuthHandler{}).RequirePlatformRoles(
					auth.PlatformRolePlatformAdmin,
				),
			))
			registerPlatformProjectRoutes(
				platformAdmin,
				handlers.NewProjectHandler(nil),
			)

			response := httptest.NewRecorder()
			request := httptest.NewRequest(
				http.MethodPost,
				"/api/platform/projects/"+
					platformProjectArchiveTestID+
					"/archive",
				nil,
			)
			router.ServeHTTP(response, request)
			if response.Code != test.wantStatus {
				t.Fatalf(
					"status = %d, want %d; body=%s",
					response.Code,
					test.wantStatus,
					response.Body.String(),
				)
			}
			if test.wantStatus == http.StatusInternalServerError &&
				!strings.Contains(response.Body.String(), "项目服务不可用") {
				t.Fatalf(
					"platform administrator did not reach archive handler: %s",
					response.Body.String(),
				)
			}
		})
	}
}

func TestPlatformProjectListRouteUsesExactPlatformAdminMatrix(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, test := range []struct {
		name          string
		platformRole  any
		legacyRole    any
		setPlatform   bool
		setLegacyRole bool
		wantStatus    int
	}{
		{
			name:         "platform administrator reaches list handler",
			platformRole: auth.PlatformRolePlatformAdmin,
			setPlatform:  true,
			wantStatus:   http.StatusInternalServerError,
		},
		{
			name:         "security auditor denied",
			platformRole: auth.PlatformRoleSecurityAuditor,
			setPlatform:  true,
			wantStatus:   http.StatusForbidden,
		},
		{
			name:         "emergency operator denied",
			platformRole: auth.PlatformRoleEmergencyOperator,
			setPlatform:  true,
			wantStatus:   http.StatusForbidden,
		},
		{
			name:         "member denied",
			platformRole: auth.PlatformRoleMember,
			setPlatform:  true,
			wantStatus:   http.StatusForbidden,
		},
		{
			name:          "legacy admin role denied",
			legacyRole:    "admin",
			setLegacyRole: true,
			wantStatus:    http.StatusForbidden,
		},
		{
			name:         "untyped platform role denied",
			platformRole: string(auth.PlatformRolePlatformAdmin),
			setPlatform:  true,
			wantStatus:   http.StatusForbidden,
		},
		{
			name:       "missing roles denied",
			wantStatus: http.StatusForbidden,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			router := gin.New()
			platformAdmin := router.Group("/api/platform")
			platformAdmin.Use(func(c *gin.Context) {
				c.Set("user_id", uint(7))
				if test.setPlatform {
					c.Set("platform_role", test.platformRole)
				}
				if test.setLegacyRole {
					c.Set("user_role", test.legacyRole)
				}
			})
			platformAdmin.Use(ginAdapter(
				(&auth.AuthHandler{}).RequirePlatformRoles(
					auth.PlatformRolePlatformAdmin,
				),
			))
			registerPlatformProjectRoutes(
				platformAdmin,
				handlers.NewProjectHandler(nil),
			)

			response := httptest.NewRecorder()
			request := httptest.NewRequest(
				http.MethodGet,
				"/api/platform/projects",
				nil,
			)
			router.ServeHTTP(response, request)
			if response.Code != test.wantStatus {
				t.Fatalf(
					"status = %d, want %d; body=%s",
					response.Code,
					test.wantStatus,
					response.Body.String(),
				)
			}
			if test.wantStatus == http.StatusInternalServerError &&
				!strings.Contains(
					response.Body.String(),
					"项目服务不可用",
				) {
				t.Fatalf(
					"platform administrator did not reach list handler: %s",
					response.Body.String(),
				)
			}
		})
	}
}

func TestRegisterPlatformProjectRoutesPublishesListCreateAndArchive(
	t *testing.T,
) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	group := router.Group("/api/platform")
	registerPlatformProjectRoutes(group, handlers.NewProjectHandler(nil))

	routes := make(map[string]struct{})
	for _, route := range router.Routes() {
		routes[route.Method+" "+route.Path] = struct{}{}
	}
	for _, expected := range []string{
		"GET /api/platform/projects",
		"POST /api/platform/projects",
		"POST /api/platform/projects/:projectPublicID/archive",
	} {
		if _, ok := routes[expected]; !ok {
			t.Errorf("route %s is missing", expected)
		}
	}
}
