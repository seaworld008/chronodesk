package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestRequirePlatformRolesUsesExactTypedAllowlist(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name      string
		context   any
		allowlist []PlatformRole
		want      int
	}{
		{
			name:      "platform administrator exact match",
			context:   PlatformRolePlatformAdmin,
			allowlist: []PlatformRole{PlatformRolePlatformAdmin},
			want:      http.StatusOK,
		},
		{
			name:    "security auditor exact read allowlist",
			context: PlatformRoleSecurityAuditor,
			allowlist: []PlatformRole{
				PlatformRolePlatformAdmin,
				PlatformRoleSecurityAuditor,
			},
			want: http.StatusOK,
		},
		{
			name:      "platform administrator does not inherit auditor-only capability",
			context:   PlatformRolePlatformAdmin,
			allowlist: []PlatformRole{PlatformRoleSecurityAuditor},
			want:      http.StatusForbidden,
		},
		{
			name:      "emergency operator has no implicit capability",
			context:   PlatformRoleEmergencyOperator,
			allowlist: []PlatformRole{PlatformRolePlatformAdmin},
			want:      http.StatusForbidden,
		},
		{
			name:      "member has no platform capability",
			context:   PlatformRoleMember,
			allowlist: []PlatformRole{PlatformRolePlatformAdmin},
			want:      http.StatusForbidden,
		},
		{
			name:      "string context is not a typed platform role",
			context:   string(PlatformRolePlatformAdmin),
			allowlist: []PlatformRole{PlatformRolePlatformAdmin},
			want:      http.StatusForbidden,
		},
		{
			name:      "unknown platform role fails closed",
			context:   PlatformRole("unknown"),
			allowlist: []PlatformRole{PlatformRolePlatformAdmin},
			want:      http.StatusForbidden,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			router := gin.New()
			router.GET("/", func(c *gin.Context) {
				c.Set("platform_role", test.context)
				(&AuthHandler{}).RequirePlatformRoles(test.allowlist...)(
					NewGinHTTPContext(c),
				)
				if c.IsAborted() {
					return
				}
				c.Status(http.StatusOK)
			})
			response := httptest.NewRecorder()
			router.ServeHTTP(
				response,
				httptest.NewRequest(http.MethodGet, "/", nil),
			)
			if response.Code != test.want {
				t.Fatalf(
					"status = %d, want %d; body=%s",
					response.Code,
					test.want,
					response.Body.String(),
				)
			}
		})
	}
}
