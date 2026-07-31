package humanopenapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	authcontract "github.com/seaworld008/chronodesk/server/internal/auth"
	"github.com/seaworld008/chronodesk/server/internal/middleware"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/services"
)

func TestRegisterRoutesPublishesEmbeddedHumanContract(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	RegisterRoutes(router)

	request := httptest.NewRequest(http.MethodGet, "/human-openapi.json", nil)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	if got := response.Header().Get("Content-Type"); got != "application/vnd.oai.openapi+json;version=3.2.0" {
		t.Fatalf("Content-Type = %q", got)
	}
	if !bytes.Equal(response.Body.Bytes(), Document()) {
		t.Fatal("served Human Web contract differs from embedded document")
	}
}

func TestHumanWebContractPublishesClosedRoleAndSessionSchemas(t *testing.T) {
	var document map[string]any
	if err := json.Unmarshal(Document(), &document); err != nil {
		t.Fatalf("decode Human Web OpenAPI: %v", err)
	}
	if got := document["openapi"]; got != "3.2.0" {
		t.Fatalf("openapi = %v, want 3.2.0", got)
	}
	if got := document["x-chronodesk-types-generator"]; got != "2.0.0" {
		t.Fatalf("types generator = %v, want 2.0.0", got)
	}

	components := objectAt(t, document, "components")
	schemas := objectAt(t, components, "schemas")
	for _, name := range []string{
		"PlatformRole",
		"ProjectRole",
		"LoginRequest",
		"RefreshTokenRequest",
		"LogoutRequest",
		"HumanSessionUser",
		"AuthSession",
		"AuthorizedProject",
		"AuthorizedProjectAccess",
		"ProjectMembership",
		"AdminUser",
		"PlatformProjectSummary",
		"PlatformProjectSummaryListEnvelope",
	} {
		if _, ok := schemas[name]; !ok {
			t.Errorf("components.schemas.%s is missing", name)
		}
	}

	assertStringEnum(t, schemas, "PlatformRole", []string{
		"platform_admin",
		"security_auditor",
		"emergency_operator",
		"member",
	})
	assertStringEnum(t, schemas, "ProjectRole", []string{
		"project_admin",
		"manager",
		"agent",
		"requester",
		"observer",
	})
	if got := objectAt(t, schemas, "PlatformRole")["x-chronodesk-runtime-values"]; got != "platformRoleValues" {
		t.Errorf("PlatformRole runtime values export = %v", got)
	}
	if got := objectAt(t, schemas, "ProjectRole")["x-chronodesk-runtime-values"]; got != "projectRoleValues" {
		t.Errorf("ProjectRole runtime values export = %v", got)
	}

	for _, name := range []string{"HumanSessionUser", "AdminUser"} {
		properties := objectAt(t, objectAt(t, schemas, name), "properties")
		if _, ok := properties["platform_role"]; !ok {
			t.Errorf("%s.platform_role is missing", name)
		}
		if _, ok := properties["role"]; ok {
			t.Errorf("%s exposes removed global role", name)
		}
	}
}

func TestHumanSessionRequestsMatchStrictRuntimeDTOs(t *testing.T) {
	document := decodeDocument(t)
	paths := objectAt(t, document, "paths")
	schemas := objectAt(
		t,
		objectAt(t, document, "components"),
		"schemas",
	)
	for _, test := range []struct {
		path       string
		schemaName string
		required   []string
		properties []string
	}{
		{
			path:       "/auth/login",
			schemaName: "LoginRequest",
			required:   []string{"email", "password"},
			properties: []string{
				"email",
				"password",
				"otp_code",
				"remember_device",
				"device_name",
			},
		},
		{
			path:       "/auth/refresh",
			schemaName: "RefreshTokenRequest",
			required:   []string{"refresh_token"},
			properties: []string{"refresh_token"},
		},
		{
			path:       "/auth/forgot-password",
			schemaName: "ForgotPasswordRequest",
			required:   []string{"email"},
			properties: []string{"email"},
		},
		{
			path:       "/auth/reset-password",
			schemaName: "ResetHumanPasswordRequest",
			required:   []string{"token", "new_password"},
			properties: []string{"token", "new_password"},
		},
	} {
		t.Run(test.schemaName, func(t *testing.T) {
			operation := objectAt(
				t,
				objectAt(t, paths, test.path),
				"post",
			)
			if got := requestSchemaRef(t, operation); got != "#/components/schemas/"+test.schemaName {
				t.Fatalf("%s request schema = %q", test.path, got)
			}
			schema := objectAt(t, schemas, test.schemaName)
			if schema["additionalProperties"] != false {
				t.Fatalf("%s must reject unknown fields", test.schemaName)
			}
			assertExactStringArray(t, schema["required"], test.required)
			assertExactObjectKeys(
				t,
				objectAt(t, schema, "properties"),
				test.properties,
			)
		})
	}
	if _, exposed := objectAt(
		t,
		objectAt(t, schemas, "LoginRequest"),
		"properties",
	)["device_token"]; exposed {
		t.Fatal("LoginRequest exposes server-managed device_token")
	}
	logout := objectAt(t, objectAt(t, paths, "/auth/logout"), "post")
	requestBody := objectAt(t, logout, "requestBody")
	if requestBody["required"] != false {
		t.Fatal("logout request body must remain optional")
	}
	content := objectAt(t, requestBody, "content")
	media := objectAt(t, content, "application/json")
	if got, _ := objectAt(t, media, "schema")["$ref"].(string); got != "#/components/schemas/LogoutRequest" {
		t.Fatalf("logout request schema = %q", got)
	}
	logoutSchema := objectAt(t, schemas, "LogoutRequest")
	if logoutSchema["additionalProperties"] != false {
		t.Fatal("LogoutRequest must reject unknown fields when a body is supplied")
	}
	assertExactObjectKeys(
		t,
		objectAt(t, logoutSchema, "properties"),
		[]string{"refresh_token"},
	)
}

func TestAuthorizedProjectPublishesStableScalarProjection(t *testing.T) {
	document := decodeDocument(t)
	schemas := objectAt(
		t,
		objectAt(t, document, "components"),
		"schemas",
	)
	project := objectAt(t, schemas, "AuthorizedProject")
	if project["additionalProperties"] != false {
		t.Fatal("AuthorizedProject must be a closed response DTO")
	}
	want := []string{
		"id",
		"public_id",
		"created_at",
		"updated_at",
		"organization_id",
		"business_unit_id",
		"key",
		"name",
		"description",
		"status",
	}
	assertExactStringArray(t, project["required"], want)
	assertExactObjectKeys(t, objectAt(t, project, "properties"), want)
	for _, unloaded := range []string{
		"organization",
		"business_unit",
		"ticket_sequence",
	} {
		if _, exposed := objectAt(t, project, "properties")[unloaded]; exposed {
			t.Errorf("AuthorizedProject exposes unstable field %q", unloaded)
		}
	}
}

func TestHumanErrorEnvelopeIsAClosedRuntimeUnion(t *testing.T) {
	document := decodeDocument(t)
	schemas := objectAt(
		t,
		objectAt(t, document, "components"),
		"schemas",
	)
	for _, name := range []string{
		"StandardErrorEnvelope",
		"AuthErrorEnvelope",
		"CodedErrorEnvelope",
		"RecoveryErrorEnvelope",
	} {
		if objectAt(t, schemas, name)["additionalProperties"] != false {
			t.Errorf("%s must reject unknown fields", name)
		}
	}
	errorEnvelope := objectAt(t, schemas, "ErrorEnvelope")
	oneOf, ok := errorEnvelope["oneOf"].([]any)
	if !ok || len(oneOf) != 4 {
		t.Fatalf("ErrorEnvelope.oneOf = %v, want four runtime shapes", oneOf)
	}
	got := make([]string, 0, len(oneOf))
	for _, raw := range oneOf {
		reference, _ := raw.(map[string]any)["$ref"].(string)
		got = append(got, reference)
	}
	want := []string{
		"#/components/schemas/StandardErrorEnvelope",
		"#/components/schemas/AuthErrorEnvelope",
		"#/components/schemas/CodedErrorEnvelope",
		"#/components/schemas/RecoveryErrorEnvelope",
	}
	authErrorProperties := objectAt(
		t,
		objectAt(t, schemas, "AuthErrorEnvelope"),
		"properties",
	)
	if code := objectAt(t, authErrorProperties, "code"); code["type"] != "string" {
		t.Errorf("AuthErrorEnvelope.code = %v, want string", code)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Errorf("ErrorEnvelope.oneOf[%d] = %q, want %q", index, got[index], want[index])
		}
	}
}

func TestRecoveryResponseConformsToPublishedClosedBranch(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(middleware.WrapGinMiddleware(middleware.RequestIDMiddleware()))
	router.Use(middleware.WrapGinMiddleware(middleware.RecoveryMiddleware(
		&middleware.RecoveryConfig{
			Logger:            middleware.NewSimpleLogger(nil, middleware.LogLevelError),
			EnableStackTrace:  false,
			DisablePrintStack: true,
		},
	)))
	router.GET("/panic", func(*gin.Context) {
		panic("must-not-leak")
	})

	response := httptest.NewRecorder()
	router.ServeHTTP(
		response,
		httptest.NewRequest(http.MethodGet, "/panic", nil),
	)
	if response.Code != http.StatusInternalServerError {
		t.Fatalf(
			"recovery status = %d, want %d; body=%s",
			response.Code,
			http.StatusInternalServerError,
			response.Body.String(),
		)
	}
	if bytes.Contains(response.Body.Bytes(), []byte("must-not-leak")) {
		t.Fatalf("panic detail leaked in response: %s", response.Body.String())
	}

	var body map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode recovery response: %v", err)
	}
	schemas := objectAt(
		t,
		objectAt(t, decodeDocument(t), "components"),
		"schemas",
	)
	schema := objectAt(t, schemas, "RecoveryErrorEnvelope")
	assertRuntimeObjectMatchesClosedSchema(t, schema, body)
	if body["success"] != false {
		t.Fatalf("recovery success = %v, want false", body["success"])
	}

	errorBody, ok := body["error"].(map[string]any)
	if !ok {
		t.Fatalf("recovery error = %T, want object", body["error"])
	}
	errorSchema := objectAt(
		t,
		objectAt(t, schema, "properties"),
		"error",
	)
	assertRuntimeObjectMatchesClosedSchema(t, errorSchema, errorBody)
	if errorBody["code"] != "internal_error" {
		t.Errorf("recovery error code = %v, want internal_error", errorBody["code"])
	}
	requestID, _ := errorBody["request_id"].(string)
	if requestID == "" {
		t.Fatal("recovery response omitted request_id installed by global middleware")
	}
	if got := response.Header().Get("X-Request-ID"); got != requestID {
		t.Errorf("X-Request-ID = %q, response request_id = %q", got, requestID)
	}
}

func TestRuntimeErrorResponsesConformToPublishedClosedBranches(t *testing.T) {
	document := decodeDocument(t)
	schemas := objectAt(
		t,
		objectAt(t, document, "components"),
		"schemas",
	)
	for _, test := range []struct {
		schemaName string
		value      any
	}{
		{
			schemaName: "StandardErrorEnvelope",
			value: middleware.StandardResponse{
				Code: http.StatusBadRequest,
				Msg:  "请求无效",
			},
		},
		{
			schemaName: "AuthErrorEnvelope",
			value: authcontract.ErrorResponse{
				Error:   "request_timeout",
				Message: "认证请求超时，请重试",
				Code:    "request_timeout",
			},
		},
		{
			schemaName: "CodedErrorEnvelope",
			value: map[string]any{
				"code": "project_access_revoked",
				"msg":  "当前项目访问权限已失效",
			},
		},
	} {
		t.Run(test.schemaName, func(t *testing.T) {
			payload, err := json.Marshal(test.value)
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]any
			if err := json.Unmarshal(payload, &fields); err != nil {
				t.Fatal(err)
			}
			schema := objectAt(t, schemas, test.schemaName)
			properties := objectAt(t, schema, "properties")
			for name := range fields {
				if _, published := properties[name]; !published {
					t.Errorf(
						"%s runtime adds unpublished field %q: %s",
						test.schemaName,
						name,
						payload,
					)
				}
			}
			for _, raw := range schema["required"].([]any) {
				name, _ := raw.(string)
				if _, present := fields[name]; !present {
					t.Errorf(
						"%s runtime omits required field %q: %s",
						test.schemaName,
						name,
						payload,
					)
				}
			}
		})
	}
}

func assertRuntimeObjectMatchesClosedSchema(
	t *testing.T,
	schema map[string]any,
	value map[string]any,
) {
	t.Helper()
	if schema["additionalProperties"] != false {
		t.Fatal("runtime object schema must reject unknown fields")
	}
	properties := objectAt(t, schema, "properties")
	for name := range value {
		if _, published := properties[name]; !published {
			t.Errorf("runtime adds unpublished field %q: %v", name, value)
		}
	}
	required, ok := schema["required"].([]any)
	if !ok {
		t.Fatalf("schema.required = %T, want array", schema["required"])
	}
	for _, raw := range required {
		name, _ := raw.(string)
		if _, present := value[name]; !present {
			t.Errorf("runtime omits required field %q: %v", name, value)
		}
	}
}

func TestPlatformProjectSummaryNeverPublishesTrustedNumericScope(t *testing.T) {
	var document map[string]any
	if err := json.Unmarshal(Document(), &document); err != nil {
		t.Fatalf("decode Human Web OpenAPI: %v", err)
	}
	schemas := objectAt(
		t,
		objectAt(t, document, "components"),
		"schemas",
	)
	for _, name := range []string{"PlatformProjectSummary"} {
		properties := objectAt(t, objectAt(t, schemas, name), "properties")
		for _, forbidden := range []string{
			"id",
			"project_id",
			"organization_id",
			"business_unit_id",
			"scope",
		} {
			if _, ok := properties[forbidden]; ok {
				t.Errorf("%s exposes trusted numeric field %q", name, forbidden)
			}
		}
	}

	access := objectAt(t, schemas, "AuthorizedProjectAccess")
	accessProperties := objectAt(t, access, "properties")
	for _, required := range []string{"project", "project_role", "scope"} {
		if _, ok := accessProperties[required]; !ok {
			t.Errorf("AuthorizedProjectAccess.%s is missing", required)
		}
	}
}

func TestHumanSessionUserIncludesOptionalProfile(t *testing.T) {
	var document map[string]any
	if err := json.Unmarshal(Document(), &document); err != nil {
		t.Fatalf("decode Human Web OpenAPI: %v", err)
	}
	schemas := objectAt(
		t,
		objectAt(t, document, "components"),
		"schemas",
	)
	sessionUser := objectAt(t, schemas, "HumanSessionUser")
	properties := objectAt(t, sessionUser, "properties")
	if _, ok := properties["profile"]; !ok {
		t.Fatal("HumanSessionUser.profile is missing")
	}
	for _, raw := range sessionUser["required"].([]any) {
		if raw == "profile" {
			t.Fatal("HumanSessionUser.profile must remain optional")
		}
	}
	profile := objectAt(t, schemas, "HumanUserProfile")
	if profile["additionalProperties"] != false {
		t.Fatal("HumanUserProfile must be an explicit closed DTO")
	}
}

func TestPublishedUserRequiredFieldsMatchRuntimeMarshal(t *testing.T) {
	document := decodeDocument(t)
	schemas := objectAt(
		t,
		objectAt(t, document, "components"),
		"schemas",
	)
	for _, test := range []struct {
		schemaName string
		value      any
	}{
		{
			schemaName: "HumanSessionUser",
			value:      authcontract.UserInfo{},
		},
		{
			schemaName: "HumanUserProfile",
			value:      authcontract.UserProfile{},
		},
		{
			schemaName: "AdminUser",
			value:      models.UserResponse{},
		},
	} {
		t.Run(test.schemaName, func(t *testing.T) {
			payload, err := json.Marshal(test.value)
			if err != nil {
				t.Fatalf("marshal runtime DTO: %v", err)
			}
			var fields map[string]any
			if err := json.Unmarshal(payload, &fields); err != nil {
				t.Fatalf("decode runtime DTO: %v", err)
			}
			schema := objectAt(t, schemas, test.schemaName)
			required, ok := schema["required"].([]any)
			if !ok {
				t.Fatalf("%s.required = %T", test.schemaName, schema["required"])
			}
			if len(required) != len(fields) {
				t.Fatalf(
					"%s required=%v, runtime fields=%v",
					test.schemaName,
					required,
					fields,
				)
			}
			for _, raw := range required {
				name, _ := raw.(string)
				if _, exists := fields[name]; !exists {
					t.Errorf(
						"%s runtime marshal omits required field %q",
						test.schemaName,
						name,
					)
				}
			}
			for name := range fields {
				found := false
				for _, raw := range required {
					if raw == name {
						found = true
						break
					}
				}
				if !found {
					t.Errorf(
						"%s runtime marshal adds non-required field %q",
						test.schemaName,
						name,
					)
				}
			}
			if test.schemaName == "HumanSessionUser" {
				if value, ok := fields["last_login_at"]; !ok || value != nil {
					t.Errorf("HumanSessionUser.last_login_at = %v, want required null", value)
				}
				if _, ok := fields["profile"]; ok {
					t.Error("nil HumanSessionUser.profile must be omitted")
				}
			}
			if test.schemaName == "AdminUser" {
				for _, nullable := range []string{"last_login_at", "manager_id"} {
					if value, ok := fields[nullable]; !ok || value != nil {
						t.Errorf("AdminUser.%s = %v, want required null", nullable, value)
					}
				}
			}
		})
	}
}

func TestHumanWebContractCoversRequiredBrowserOperations(t *testing.T) {
	document := decodeDocument(t)
	paths := objectAt(t, document, "paths")
	for _, expected := range []struct {
		path   string
		method string
	}{
		{"/auth/logout", "post"},
		{"/auth/logout-all", "post"},
		{"/auth/me", "get"},
		{"/auth/profile", "put"},
		{"/projects/{projectKey}/context", "get"},
		{"/projects/{projectKey}/memberships", "get"},
		{"/projects/{projectKey}/memberships", "post"},
		{"/projects/{projectKey}/memberships/{userID}", "delete"},
		{"/platform/projects", "get"},
		{"/platform/projects", "post"},
		{"/platform/projects/{projectPublicID}/archive", "post"},
		{"/platform/users", "get"},
		{"/platform/users", "post"},
		{"/platform/users/stats", "get"},
		{"/platform/users/{userID}", "get"},
		{"/platform/users/{userID}", "put"},
		{"/platform/users/{userID}", "delete"},
		{"/platform/users/{userID}/reset-password", "post"},
		{"/platform/audit-logs", "get"},
		{"/platform/audit-logs/{auditLogID}", "get"},
	} {
		pathItem := objectAt(t, paths, expected.path)
		if _, ok := pathItem[expected.method]; !ok {
			t.Errorf("%s %s is missing", expected.method, expected.path)
		}
	}
}

func TestProtectedOperationsDeclareExactRoleAllowlist(t *testing.T) {
	document := decodeDocument(t)
	paths := objectAt(t, document, "paths")
	validPlatformRoles := stringSet(
		"platform_admin",
		"security_auditor",
		"emergency_operator",
		"member",
	)
	validProjectRoles := stringSet(
		"project_admin",
		"manager",
		"agent",
		"requester",
		"observer",
	)
	for path, rawPathItem := range paths {
		pathItem := rawPathItem.(map[string]any)
		for _, method := range []string{"get", "post", "put", "patch", "delete"} {
			rawOperation, ok := pathItem[method]
			if !ok {
				continue
			}
			operation := rawOperation.(map[string]any)
			security, protected := operation["security"].([]any)
			if !protected || len(security) == 0 {
				continue
			}
			platform, hasPlatform := operation["x-chronodesk-platform-roles"]
			project, hasProject := operation["x-chronodesk-project-roles"]
			if path == "/projects" && method == "get" {
				if hasPlatform || hasProject {
					t.Error("GET /projects must filter memberships without a role precondition")
				}
				continue
			}
			if path == "/workbench/tickets" && method == "get" {
				if hasPlatform || hasProject {
					t.Error("GET /workbench/tickets must filter memberships without a role precondition")
				}
				if operation["x-chronodesk-project-membership-filtered"] != true {
					t.Error("GET /workbench/tickets must declare membership filtering")
				}
				continue
			}
			if path == "/workbench/dashboard" && method == "get" {
				if hasPlatform || hasProject {
					t.Error("GET /workbench/dashboard must filter memberships without a role precondition")
				}
				if operation["x-chronodesk-project-membership-filtered"] != true {
					t.Error("GET /workbench/dashboard must declare membership filtering")
				}
				continue
			}
			if hasPlatform == hasProject {
				t.Errorf(
					"%s %s must declare exactly one role allowlist",
					method,
					path,
				)
				continue
			}
			if hasPlatform {
				assertRoleAllowlist(
					t,
					method+" "+path,
					platform,
					validPlatformRoles,
				)
			} else {
				assertRoleAllowlist(
					t,
					method+" "+path,
					project,
					validProjectRoles,
				)
			}
		}
	}
}

func TestProtectedOperationsMatchRuntimeRoleAllowlists(t *testing.T) {
	document := decodeDocument(t)
	paths := objectAt(t, document, "paths")
	allPlatformRoles := []string{
		"platform_admin",
		"security_auditor",
		"emergency_operator",
		"member",
	}
	allProjectRoles := []string{
		"project_admin",
		"manager",
		"agent",
		"requester",
		"observer",
	}
	for _, test := range []struct {
		path      string
		method    string
		extension string
		want      []string
	}{
		{
			"/auth/logout-all",
			"post",
			"x-chronodesk-platform-roles",
			allPlatformRoles,
		},
		{
			"/auth/me",
			"get",
			"x-chronodesk-platform-roles",
			allPlatformRoles,
		},
		{
			"/auth/profile",
			"put",
			"x-chronodesk-platform-roles",
			allPlatformRoles,
		},
		{
			"/projects/{projectKey}/context",
			"get",
			"x-chronodesk-project-roles",
			allProjectRoles,
		},
		{
			"/projects/{projectKey}/tickets",
			"get",
			"x-chronodesk-project-roles",
			allProjectRoles,
		},
		{
			"/projects/{projectKey}/tickets",
			"post",
			"x-chronodesk-project-roles",
			[]string{"project_admin", "manager", "agent", "requester"},
		},
		{
			"/projects/{projectKey}/tickets/{ticketID}",
			"get",
			"x-chronodesk-project-roles",
			allProjectRoles,
		},
		{
			"/projects/{projectKey}/tickets/{ticketID}",
			"put",
			"x-chronodesk-project-roles",
			[]string{"project_admin", "manager", "agent", "requester"},
		},
		{
			"/projects/{projectKey}/memberships",
			"get",
			"x-chronodesk-project-roles",
			[]string{"project_admin", "manager"},
		},
		{
			"/projects/{projectKey}/memberships",
			"post",
			"x-chronodesk-project-roles",
			[]string{"project_admin"},
		},
		{
			"/projects/{projectKey}/memberships/{userID}",
			"delete",
			"x-chronodesk-project-roles",
			[]string{"project_admin"},
		},
		{
			"/projects/{projectKey}/webhooks",
			"get",
			"x-chronodesk-project-roles",
			[]string{"project_admin", "manager"},
		},
		{
			"/projects/{projectKey}/webhooks",
			"post",
			"x-chronodesk-project-roles",
			[]string{"project_admin", "manager"},
		},
		{
			"/projects/{projectKey}/webhooks/{webhookID}",
			"get",
			"x-chronodesk-project-roles",
			[]string{"project_admin", "manager"},
		},
		{
			"/projects/{projectKey}/webhooks/{webhookID}",
			"put",
			"x-chronodesk-project-roles",
			[]string{"project_admin", "manager"},
		},
		{
			"/projects/{projectKey}/webhooks/{webhookID}",
			"delete",
			"x-chronodesk-project-roles",
			[]string{"project_admin", "manager"},
		},
		{
			"/projects/{projectKey}/webhooks/{webhookID}/test",
			"post",
			"x-chronodesk-project-roles",
			[]string{"project_admin", "manager"},
		},
		{
			"/projects/{projectKey}/webhooks/{webhookID}/logs",
			"get",
			"x-chronodesk-project-roles",
			[]string{"project_admin", "manager"},
		},
		{
			"/projects/{projectKey}/webhooks/{webhookID}/stats",
			"get",
			"x-chronodesk-project-roles",
			[]string{"project_admin", "manager"},
		},
		{
			"/platform/projects",
			"get",
			"x-chronodesk-platform-roles",
			[]string{"platform_admin"},
		},
		{
			"/platform/projects",
			"post",
			"x-chronodesk-platform-roles",
			[]string{"platform_admin"},
		},
		{
			"/platform/projects/{projectPublicID}/archive",
			"post",
			"x-chronodesk-platform-roles",
			[]string{"platform_admin"},
		},
		{
			"/platform/users",
			"get",
			"x-chronodesk-platform-roles",
			[]string{"platform_admin"},
		},
		{
			"/platform/users",
			"post",
			"x-chronodesk-platform-roles",
			[]string{"platform_admin"},
		},
		{
			"/platform/users/stats",
			"get",
			"x-chronodesk-platform-roles",
			[]string{"platform_admin"},
		},
		{
			"/platform/users/{userID}",
			"get",
			"x-chronodesk-platform-roles",
			[]string{"platform_admin"},
		},
		{
			"/platform/users/{userID}",
			"put",
			"x-chronodesk-platform-roles",
			[]string{"platform_admin"},
		},
		{
			"/platform/users/{userID}",
			"delete",
			"x-chronodesk-platform-roles",
			[]string{"platform_admin"},
		},
		{
			"/platform/users/{userID}/reset-password",
			"post",
			"x-chronodesk-platform-roles",
			[]string{"platform_admin"},
		},
		{
			"/platform/audit-logs",
			"get",
			"x-chronodesk-platform-roles",
			[]string{"platform_admin", "security_auditor"},
		},
		{
			"/platform/audit-logs/{auditLogID}",
			"get",
			"x-chronodesk-platform-roles",
			[]string{"platform_admin", "security_auditor"},
		},
	} {
		t.Run(test.method+" "+test.path, func(t *testing.T) {
			operation := objectAt(
				t,
				objectAt(t, paths, test.path),
				test.method,
			)
			assertExactRoleAllowlist(
				t,
				operation[test.extension],
				test.want,
			)
		})
	}
}

func TestSessionAndPlatformProjectResponsesMatchRuntimeEnvelopes(t *testing.T) {
	document := decodeDocument(t)
	paths := objectAt(t, document, "paths")
	schemas := objectAt(t, objectAt(t, document, "components"), "schemas")
	login := responseSchemaRef(
		t,
		objectAt(t, objectAt(t, paths, "/auth/login"), "post"),
		"200",
	)
	if login != "#/components/schemas/AuthSessionEnvelope" {
		t.Fatalf("login response schema = %q", login)
	}
	refresh := responseSchemaRef(
		t,
		objectAt(t, objectAt(t, paths, "/auth/refresh"), "post"),
		"200",
	)
	if refresh != "#/components/schemas/AuthSessionSuccessEnvelope" {
		t.Fatalf("refresh response schema = %q", refresh)
	}
	list := responseSchemaRef(
		t,
		objectAt(t, objectAt(t, paths, "/platform/projects"), "get"),
		"200",
	)
	if list != "#/components/schemas/PlatformProjectSummaryListEnvelope" {
		t.Fatalf("platform project list response schema = %q", list)
	}

	for _, test := range []struct {
		name   string
		path   string
		status string
	}{
		{
			name:   "create",
			path:   "/platform/projects",
			status: "201",
		},
		{
			name:   "archive",
			path:   "/platform/projects/{projectPublicID}/archive",
			status: "200",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			reference := responseSchemaRef(
				t,
				objectAt(t, objectAt(t, paths, test.path), "post"),
				test.status,
			)
			if reference != "#/components/schemas/PlatformProjectSummaryEnvelope" {
				t.Fatalf("platform project response schema = %q", reference)
			}
		})
	}

	envelope := objectAt(t, schemas, "PlatformProjectSummaryEnvelope")
	if envelope["type"] != "object" {
		t.Fatalf("PlatformProjectSummaryEnvelope.type = %v", envelope["type"])
	}
	if envelope["additionalProperties"] != false {
		t.Fatalf(
			"PlatformProjectSummaryEnvelope.additionalProperties = %v",
			envelope["additionalProperties"],
		)
	}
	assertExactStringArray(
		t,
		envelope["required"],
		[]string{"code", "msg", "data"},
	)
	properties := objectAt(t, envelope, "properties")
	data := objectAt(t, properties, "data")
	if reference, _ := data["$ref"].(string); reference != "#/components/schemas/PlatformProjectSummary" {
		t.Fatalf("PlatformProjectSummaryEnvelope.data schema = %q", reference)
	}

	listEnvelope := objectAt(
		t,
		schemas,
		"PlatformProjectSummaryListEnvelope",
	)
	if listEnvelope["type"] != "object" ||
		listEnvelope["additionalProperties"] != false {
		t.Fatalf(
			"PlatformProjectSummaryListEnvelope = %v",
			listEnvelope,
		)
	}
	assertExactStringArray(
		t,
		listEnvelope["required"],
		[]string{"code", "msg", "data"},
	)
	listData := objectAt(
		t,
		objectAt(t, listEnvelope, "properties"),
		"data",
	)
	if listData["type"] != "array" {
		t.Fatalf("platform project list data = %v", listData)
	}
	items := objectAt(t, listData, "items")
	if reference, _ := items["$ref"].(string); reference != "#/components/schemas/PlatformProjectSummary" {
		t.Fatalf("platform project list item schema = %q", reference)
	}
}

func TestPlatformProjectListPublishesExactReadContract(t *testing.T) {
	document := decodeDocument(t)
	operation := objectAt(
		t,
		objectAt(
			t,
			objectAt(t, document, "paths"),
			"/platform/projects",
		),
		"get",
	)
	if operation["operationId"] != "listPlatformProjects" {
		t.Fatalf(
			"GET /platform/projects operationId = %v",
			operation["operationId"],
		)
	}
	if _, published := operation["parameters"]; published {
		t.Fatalf(
			"GET /platform/projects publishes unsupported parameters: %v",
			operation["parameters"],
		)
	}
	assertExactRoleAllowlist(
		t,
		operation["x-chronodesk-platform-roles"],
		[]string{"platform_admin"},
	)
	assertExactObjectKeys(
		t,
		objectAt(t, operation, "responses"),
		[]string{"200", "400", "401", "403", "429", "500", "503"},
	)
}

func TestPlatformProjectArchivePublishesUUIDv7AndStableStatuses(t *testing.T) {
	document := decodeDocument(t)
	components := objectAt(t, document, "components")
	parameters := objectAt(t, components, "parameters")
	parameter := objectAt(t, parameters, "ProjectPublicID")
	if parameter["name"] != "projectPublicID" ||
		parameter["in"] != "path" ||
		parameter["required"] != true {
		t.Fatalf("ProjectPublicID parameter = %v", parameter)
	}
	schema := objectAt(t, parameter, "schema")
	if schema["type"] != "string" ||
		schema["format"] != "uuid" ||
		schema["pattern"] != "^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$" {
		t.Fatalf("ProjectPublicID schema = %v", schema)
	}

	operation := objectAt(
		t,
		objectAt(
			t,
			objectAt(t, document, "paths"),
			"/platform/projects/{projectPublicID}/archive",
		),
		"post",
	)
	rawParameters, ok := operation["parameters"].([]any)
	if !ok || len(rawParameters) != 1 {
		t.Fatalf("archive operation parameters = %v", operation["parameters"])
	}
	if reference, _ := rawParameters[0].(map[string]any)["$ref"].(string); reference != "#/components/parameters/ProjectPublicID" {
		t.Fatalf("archive project public ID parameter = %q", reference)
	}
	assertExactObjectKeys(
		t,
		objectAt(t, operation, "responses"),
		[]string{"200", "400", "401", "403", "404", "409", "429", "500", "503"},
	)
	schemas := objectAt(t, components, "schemas")
	for _, schemaName := range []string{
		"PlatformProjectSummary",
		"AuthorizedProject",
	} {
		publicID := objectAt(
			t,
			objectAt(
				t,
				objectAt(t, schemas, schemaName),
				"properties",
			),
			"public_id",
		)
		if publicID["format"] != "uuid" ||
			publicID["pattern"] != schema["pattern"] {
			t.Errorf("%s.public_id = %v", schemaName, publicID)
		}
	}
}

func TestAuthOperationsPublishEveryRuntimeStatus(t *testing.T) {
	document := decodeDocument(t)
	paths := objectAt(t, document, "paths")
	for _, test := range []struct {
		path string
		want []string
	}{
		{
			path: "/auth/forgot-password",
			want: []string{"200", "400", "429", "500", "503"},
		},
		{
			path: "/auth/reset-password",
			want: []string{"200", "400", "429", "500", "503"},
		},
		{
			path: "/auth/login",
			want: []string{"200", "400", "401", "403", "429", "503"},
		},
		{
			path: "/auth/refresh",
			want: []string{"200", "400", "401", "408", "429", "503"},
		},
		{
			path: "/auth/logout",
			want: []string{"200", "400", "429", "503"},
		},
		{
			path: "/auth/logout-all",
			want: []string{"200", "401", "429", "500", "503"},
		},
	} {
		t.Run(test.path, func(t *testing.T) {
			operation := objectAt(
				t,
				objectAt(t, paths, test.path),
				"post",
			)
			assertExactObjectKeys(
				t,
				objectAt(t, operation, "responses"),
				test.want,
			)
		})
	}
}

func TestHumanQueryParametersMatchRuntimeAdapters(t *testing.T) {
	document := decodeDocument(t)
	paths := objectAt(t, document, "paths")
	parameters := objectAt(
		t,
		objectAt(t, document, "components"),
		"parameters",
	)
	for _, test := range []struct {
		path string
		want []string
	}{
		{
			path: "/platform/users",
			want: []string{
				"page",
				"page_size",
				"platform_role",
				"status",
				"search",
				"order_by",
				"order",
			},
		},
		{
			path: "/platform/audit-logs",
			want: []string{
				"user_id",
				"actor",
				"platform_role",
				"action",
				"method",
				"path",
				"path_prefix",
				"status",
				"keyword",
				"result",
				"time_preset",
				"start_time",
				"end_time",
				"page",
				"limit",
				"cursor",
			},
		},
	} {
		t.Run(test.path, func(t *testing.T) {
			operation := objectAt(
				t,
				objectAt(t, paths, test.path),
				"get",
			)
			raw, ok := operation["parameters"].([]any)
			if !ok {
				t.Fatalf("parameters = %T, want array", operation["parameters"])
			}
			got := make([]string, 0, len(raw))
			for _, item := range raw {
				reference, _ := item.(map[string]any)["$ref"].(string)
				const prefix = "#/components/parameters/"
				if len(reference) <= len(prefix) || reference[:len(prefix)] != prefix {
					t.Fatalf("query parameter reference = %q", reference)
				}
				parameter := objectAt(t, parameters, reference[len(prefix):])
				if parameter["in"] != "query" {
					t.Errorf("%s in = %v, want query", reference, parameter["in"])
				}
				name, _ := parameter["name"].(string)
				got = append(got, name)
			}
			if len(got) != len(test.want) {
				t.Fatalf("query parameters = %v, want %v", got, test.want)
			}
			for index := range test.want {
				if got[index] != test.want[index] {
					t.Errorf(
						"query parameter[%d] = %q, want %q",
						index,
						got[index],
						test.want[index],
					)
				}
			}
		})
	}
}

func TestHumanProjectKeyUsesDomainPatternEverywhere(t *testing.T) {
	document := decodeDocument(t)
	components := objectAt(t, document, "components")
	parameters := objectAt(t, components, "parameters")
	schemas := objectAt(t, components, "schemas")
	const pattern = "^[A-Z][A-Z0-9_-]{0,31}$"

	projectKey := objectAt(t, objectAt(t, parameters, "ProjectKey"), "schema")
	assertProjectKeySchema(t, "ProjectKey parameter", projectKey, pattern)
	for _, schemaName := range []string{
		"CreatePlatformProjectRequest",
		"PlatformProjectSummary",
		"AuthorizedProject",
	} {
		properties := objectAt(
			t,
			objectAt(t, schemas, schemaName),
			"properties",
		)
		assertProjectKeySchema(
			t,
			schemaName+".key",
			objectAt(t, properties, "key"),
			pattern,
		)
	}
}

func TestP1HumanWebOperationsAreTypedAndMachineAddressable(t *testing.T) {
	document := decodeDocument(t)
	paths := objectAt(t, document, "paths")
	required := []struct {
		path   string
		method string
	}{
		{"/auth/forgot-password", "post"},
		{"/auth/reset-password", "post"},
		{"/platform/projects/{projectPublicID}/archive", "post"},
		{"/workbench/tickets", "get"},
		{"/workbench/dashboard", "get"},
		{"/projects/{projectKey}/tickets", "get"},
		{"/projects/{projectKey}/tickets", "post"},
		{"/projects/{projectKey}/tickets/overdue", "get"},
		{"/projects/{projectKey}/tickets/sla-breach", "get"},
		{"/projects/{projectKey}/tickets/{ticketID}", "get"},
		{"/projects/{projectKey}/tickets/{ticketID}", "put"},
		{"/projects/{projectKey}/tickets/{ticketID}", "delete"},
		{"/projects/{projectKey}/tickets/{ticketID}/assign", "post"},
		{"/projects/{projectKey}/tickets/{ticketID}/transfer", "post"},
		{"/projects/{projectKey}/tickets/{ticketID}/escalate", "post"},
		{"/projects/{projectKey}/tickets/{ticketID}/status", "post"},
		{"/projects/{projectKey}/tickets/{ticketID}/history", "get"},
		{"/projects/{projectKey}/tickets/{ticketID}/comments", "get"},
		{"/projects/{projectKey}/tickets/{ticketID}/comments", "post"},
		{"/projects/{projectKey}/tickets/{ticketID}/comments/{commentID}/replies", "get"},
		{"/projects/{projectKey}/tickets/{ticketID}/attachments", "get"},
		{"/projects/{projectKey}/tickets/{ticketID}/attachments", "post"},
		{
			"/projects/{projectKey}/tickets/{ticketID}/attachments/{attachmentID}/content",
			"get",
		},
		{"/projects/{projectKey}/notifications", "get"},
		{"/projects/{projectKey}/notifications", "post"},
		{"/projects/{projectKey}/notifications/{notificationID}", "delete"},
		{"/projects/{projectKey}/notifications/{notificationID}/read", "put"},
		{"/projects/{projectKey}/notifications/read-all", "put"},
		{"/projects/{projectKey}/notifications/unread-count", "get"},
		{"/notification-preferences", "get"},
		{"/notification-preferences", "put"},
		{"/projects/{projectKey}/admin/automation/rules", "get"},
		{"/projects/{projectKey}/admin/automation/rules", "post"},
		{"/projects/{projectKey}/admin/automation/rules/{ruleID}", "get"},
		{"/projects/{projectKey}/admin/automation/rules/{ruleID}", "put"},
		{"/projects/{projectKey}/admin/automation/rules/{ruleID}", "delete"},
		{"/projects/{projectKey}/admin/automation/logs", "get"},
		{"/platform/email-config", "get"},
		{"/platform/email-config", "put"},
		{"/platform/email-config/test", "post"},
		{"/platform/configs", "get"},
		{"/platform/configs/{configKey}", "put"},
		{"/projects/{projectKey}/webhooks", "get"},
		{"/projects/{projectKey}/webhooks", "post"},
		{"/projects/{projectKey}/webhooks/{webhookID}", "get"},
		{"/projects/{projectKey}/webhooks/{webhookID}", "put"},
		{"/projects/{projectKey}/webhooks/{webhookID}", "delete"},
		{"/projects/{projectKey}/webhooks/{webhookID}/test", "post"},
		{"/projects/{projectKey}/webhooks/{webhookID}/logs", "get"},
		{"/projects/{projectKey}/webhooks/{webhookID}/stats", "get"},
		{"/projects/{projectKey}/admin/agents/agent-control/overview", "get"},
		{"/projects/{projectKey}/admin/agents/service-principals", "post"},
		{"/projects/{projectKey}/admin/agents/service-principals/{principalId}/status", "put"},
		{"/projects/{projectKey}/admin/agents/service-principals/{principalId}/credentials/rotate", "post"},
		{"/projects/{projectKey}/admin/agents/service-principals/{principalId}/credentials/{credentialId}", "delete"},
		{"/projects/{projectKey}/admin/agents/service-principals/{principalId}/policies", "get"},
		{"/projects/{projectKey}/admin/agents/service-principals/{principalId}/policies", "post"},
		{"/projects/{projectKey}/admin/agents/service-principals/{principalId}/policies/{policyId}", "delete"},
		{"/projects/{projectKey}/admin/agents/leases/{leaseId}/force-release", "post"},
		{"/projects/{projectKey}/admin/agents/attachments/{attachmentId}/scan", "post"},
		{"/projects/{projectKey}/admin/agents/outbox/{deliveryId}/replay", "post"},
	}
	for _, expected := range required {
		pathItem := objectAt(t, paths, expected.path)
		if _, ok := pathItem[expected.method]; !ok {
			t.Errorf("%s %s is missing", expected.method, expected.path)
		}
	}

	operationIDs := make(map[string]string)
	operationCount := 0
	for path, rawPathItem := range paths {
		pathItem := rawPathItem.(map[string]any)
		for _, method := range []string{"get", "post", "put", "patch", "delete"} {
			rawOperation, ok := pathItem[method]
			if !ok {
				continue
			}
			operationCount++
			operation := rawOperation.(map[string]any)
			operationID, _ := operation["operationId"].(string)
			if strings.TrimSpace(operationID) == "" {
				t.Errorf("%s %s has no operationId", method, path)
			} else if previous, duplicate := operationIDs[operationID]; duplicate {
				t.Errorf(
					"operationId %q is shared by %s and %s %s",
					operationID,
					previous,
					method,
					path,
				)
			} else {
				operationIDs[operationID] = method + " " + path
			}
			assertOperationHasTypedSuccessResponse(t, document, method, path, operation)
			assertOperationPathParametersMatch(t, document, path, pathItem, operation)
			if rawRequestBody, exists := operation["requestBody"]; exists {
				requestBody := rawRequestBody.(map[string]any)
				content := objectAt(t, requestBody, "content")
				media := firstMediaObject(t, content)
				schema := objectAt(t, media, "schema")
				if len(schema) == 0 {
					t.Errorf("%s %s has an empty request schema", method, path)
				}
			}
		}
	}
	if operationCount < 77 {
		t.Fatalf("operation count = %d, want at least 77", operationCount)
	}
}

func TestP0ListPaginationContractUsesStrictTwentyFiveToOneHundredBounds(t *testing.T) {
	document := decodeDocument(t)
	paths := objectAt(t, document, "paths")
	notifications := objectAt(
		t,
		objectAt(t, paths, "/projects/{projectKey}/notifications"),
		"get",
	)
	var notificationPageSize map[string]any
	var notificationPageReference string
	for _, raw := range notifications["parameters"].([]any) {
		parameter := raw.(map[string]any)
		if reference, _ := parameter["$ref"].(string); reference == "#/components/parameters/ContentPage" {
			notificationPageReference = reference
		}
		if parameter["name"] == "page_size" {
			notificationPageSize = parameter
		}
	}
	if notificationPageReference != "#/components/parameters/ContentPage" {
		t.Fatalf("notification page parameter ref=%q", notificationPageReference)
	}
	if notificationPageSize == nil {
		t.Fatal("notification page_size parameter is missing")
	}
	notificationPageSizeSchema := objectAt(t, notificationPageSize, "schema")
	if notificationPageSizeSchema["default"] != float64(25) ||
		notificationPageSizeSchema["maximum"] != float64(100) {
		t.Fatalf("notification page_size schema=%v", notificationPageSizeSchema)
	}

	components := objectAt(t, document, "components")
	parameters := objectAt(t, components, "parameters")
	contentPage := objectAt(t, objectAt(t, parameters, "ContentPage"), "schema")
	if contentPage["default"] != float64(1) ||
		contentPage["minimum"] != float64(1) ||
		contentPage["maximum"] != float64(1_000_000) {
		t.Fatalf("ContentPage schema=%v", contentPage)
	}
	contentPageSize := objectAt(t, objectAt(t, parameters, "ContentPageSize"), "schema")
	if contentPageSize["default"] != float64(25) ||
		contentPageSize["maximum"] != float64(100) {
		t.Fatalf("ContentPageSize schema=%v", contentPageSize)
	}
	schemas := objectAt(t, components, "schemas")
	notificationPage := objectAt(t, objectAt(t, schemas, "NotificationPage"), "properties")
	pageSize := objectAt(t, notificationPage, "page_size")
	if pageSize["maximum"] != float64(100) {
		t.Fatalf("NotificationPage.page_size=%v", pageSize)
	}

	for _, path := range []string{
		"/projects/{projectKey}/tickets/overdue",
		"/projects/{projectKey}/tickets/sla-breach",
	} {
		operation := objectAt(t, objectAt(t, paths, path), "get")
		rawParameters := operation["parameters"].([]any)
		refs := make(map[string]bool, len(rawParameters))
		for _, raw := range rawParameters {
			parameter := raw.(map[string]any)
			reference, _ := parameter["$ref"].(string)
			refs[reference] = true
		}
		for _, required := range []string{
			"#/components/parameters/ProjectKey",
			"#/components/parameters/ContentPage",
			"#/components/parameters/ContentPageSize",
		} {
			if !refs[required] {
				t.Errorf("%s is missing %s", path, required)
			}
		}
		responses := objectAt(t, operation, "responses")
		okResponse := objectAt(t, responses, "200")
		content := objectAt(t, okResponse, "content")
		jsonContent := objectAt(t, content, "application/json")
		schema := objectAt(t, jsonContent, "schema")
		if schema["$ref"] != "#/components/schemas/TicketListPageEnvelope" {
			t.Errorf("%s response schema=%v", path, schema)
		}
	}
}

func TestP1RuntimeDTOFieldsMatchPublishedSchemas(t *testing.T) {
	document := decodeDocument(t)
	schemas := objectAt(
		t,
		objectAt(t, document, "components"),
		"schemas",
	)
	for _, test := range []struct {
		schemaName string
		value      any
	}{
		{"CreateTicketRequest", models.TicketCreateRequest{}},
		{"UpdateTicketRequest", models.TicketUpdateRequest{}},
		{"Ticket", models.TicketResponse{}},
		{
			"CrossProjectWorkbenchTicket",
			services.CrossProjectWorkbenchTicket{},
		},
		{
			"CrossProjectWorkbenchPage",
			services.CrossProjectWorkbenchPage{},
		},
		{
			"WorkbenchDashboard",
			services.WorkbenchDashboard{},
		},
		{"WebhookTestReceipt", services.WebhookTestReceipt{}},
	} {
		t.Run(test.schemaName, func(t *testing.T) {
			schema := objectAt(t, schemas, test.schemaName)
			properties := objectAt(t, schema, "properties")
			want := jsonFieldNames(t, reflect.TypeOf(test.value))
			assertExactObjectKeys(t, properties, want)
		})
	}
}

func TestAdminAuditDetailRuntimeProjectionMatchesClosedSchema(t *testing.T) {
	document := decodeDocument(t)
	schema := objectAt(
		t,
		objectAt(t, objectAt(t, document, "components"), "schemas"),
		"AdminAuditLogDetail",
	)
	userID := uint(42)
	detail := services.AdminAuditDetail{
		AdminAuditListItem: services.AdminAuditListItem{
			ID:               7,
			CreatedAt:        time.Date(2026, 7, 31, 8, 0, 0, 0, time.UTC),
			UserID:           &userID,
			Username:         "security-auditor",
			PlatformRole:     models.PlatformRoleSecurityAuditor,
			Action:           "platform.user.update",
			ActionCode:       "platform.user.update",
			ResourceType:     "user",
			ResourcePublicID: "42",
			Method:           "PUT",
			Path:             "/api/platform/users/42",
			StatusCode:       200,
			MaskedIP:         "192.0.*.*",
			LatencyMs:        12,
			Result:           "success",
		},
		Query:         "view=compact",
		UserAgent:     "browser",
		Notes:         "",
		RequestID:     "request-1",
		TraceID:       "trace-1",
		CorrelationID: "correlation-1",
	}
	encoded, err := json.Marshal(detail)
	if err != nil {
		t.Fatal(err)
	}
	var instance map[string]any
	if err := json.Unmarshal(encoded, &instance); err != nil {
		t.Fatal(err)
	}
	assertClosedObjectInstance(t, schema, instance)

	instance["unpublished_secret"] = "must be rejected"
	properties := objectAt(t, schema, "properties")
	if _, published := properties["unpublished_secret"]; published ||
		schema["additionalProperties"] != false {
		t.Fatal("detail schema accepted an unpublished property")
	}
}

func TestPlatformAuditContractCoversRuntimeFailureStatuses(t *testing.T) {
	document := decodeDocument(t)
	operation := objectAt(
		t,
		objectAt(
			t,
			objectAt(t, document, "paths"),
			"/platform/audit-logs",
		),
		"get",
	)
	responses := objectAt(t, operation, "responses")
	for _, status := range []string{"500", "503", "default"} {
		raw, ok := responses[status].(map[string]any)
		if !ok {
			t.Fatalf("GET /platform/audit-logs response %s is missing", status)
		}
		reference, _ := raw["$ref"].(string)
		if reference == "" {
			t.Errorf(
				"GET /platform/audit-logs response %s has no error envelope reference",
				status,
			)
		}
	}
}

func TestAutomationLogAndWebhookSchemasAreClosedRuntimeDTOs(t *testing.T) {
	document := decodeDocument(t)
	schemas := objectAt(
		t,
		objectAt(t, document, "components"),
		"schemas",
	)
	automationLog := objectAt(t, schemas, "AutomationLog")
	if automationLog["additionalProperties"] != false {
		t.Fatal("AutomationLog must not rely on undeclared runtime properties")
	}
	automationProperties := objectAt(t, automationLog, "properties")
	for name, reference := range map[string]string{
		"rule":   "#/components/schemas/AutomationRuleLogSummary",
		"ticket": "#/components/schemas/AutomationTicketLogSummary",
	} {
		property := objectAt(t, automationProperties, name)
		if property["$ref"] != reference {
			t.Errorf("AutomationLog.%s = %v, want %s", name, property, reference)
		}
	}

	createWebhook := objectAt(t, schemas, "CreateWebhookRequest")
	updateWebhook := objectAt(t, schemas, "UpdateWebhookRequest")
	for name, schema := range map[string]map[string]any{
		"CreateWebhookRequest": createWebhook,
		"UpdateWebhookRequest": updateWebhook,
	} {
		if schema["additionalProperties"] != false {
			t.Errorf("%s must reject unpublished fields", name)
		}
	}
	if _, exposed := objectAt(t, createWebhook, "properties")["status"]; exposed {
		t.Error("CreateWebhookRequest exposes update-only status")
	}
	if _, exposed := objectAt(t, updateWebhook, "properties")["status"]; !exposed {
		t.Error("UpdateWebhookRequest omits persisted status control")
	}

	webhookConfig := objectAt(t, schemas, "WebhookConfig")
	if webhookConfig["additionalProperties"] != false {
		t.Fatal("WebhookConfig must not rely on persistence-model properties")
	}
	assertExactObjectKeys(
		t,
		objectAt(t, webhookConfig, "properties"),
		[]string{
			"id",
			"created_at",
			"updated_at",
			"organization_id",
			"project_id",
			"name",
			"description",
			"provider",
			"webhook_url",
			"status",
			"previous_secret_expires_at",
			"enabled_events",
			"enabled_events_list",
			"message_template",
			"message_format",
			"filter_rules",
			"filter_rules_obj",
			"retry_count",
			"retry_interval",
			"timeout_seconds",
			"is_async",
			"rate_limit",
			"rate_limit_window",
			"last_triggered_at",
			"last_success_at",
			"last_error_at",
			"last_error",
			"total_sent",
			"total_success",
			"total_failed",
			"created_by",
			"updated_by",
		},
	)

	paths := objectAt(t, document, "paths")
	logSchema := successResponseSchema(
		t,
		objectAt(
			t,
			objectAt(
				t,
				paths,
				"/projects/{projectKey}/webhooks/{webhookID}/logs",
			),
			"get",
		),
		"200",
	)
	logData := objectAt(t, objectAt(t, logSchema, "properties"), "data")
	logItems := objectAt(
		t,
		objectAt(t, objectAt(t, logData, "properties"), "items"),
		"items",
	)
	if logData["additionalProperties"] != false ||
		logItems["additionalProperties"] != false {
		t.Fatal("Webhook log page and items must be closed")
	}
	assertExactObjectKeys(
		t,
		objectAt(t, logItems, "properties"),
		[]string{
			"id",
			"created_at",
			"config_id",
			"event_type",
			"status",
			"response_status",
			"response_time",
			"error_message",
		},
	)

	statsSchema := successResponseSchema(
		t,
		objectAt(
			t,
			objectAt(
				t,
				paths,
				"/projects/{projectKey}/webhooks/{webhookID}/stats",
			),
			"get",
		),
		"200",
	)
	statsData := objectAt(t, objectAt(t, statsSchema, "properties"), "data")
	if statsData["additionalProperties"] != false {
		t.Fatal("Webhook stats data must be closed")
	}
	assertExactObjectKeys(
		t,
		objectAt(t, statsData, "properties"),
		[]string{"summary", "daily_stats", "period"},
	)
	statsProperties := objectAt(t, statsData, "properties")
	summary := objectAt(t, statsProperties, "summary")
	if summary["additionalProperties"] != false {
		t.Fatal("Webhook stats summary must be closed")
	}
	assertExactObjectKeys(
		t,
		objectAt(t, summary, "properties"),
		[]string{"total_sent", "total_success", "total_failed"},
	)
	dailyStats := objectAt(
		t,
		objectAt(t, statsProperties, "daily_stats"),
		"items",
	)
	if dailyStats["additionalProperties"] != false {
		t.Fatal("Webhook daily stats items must be closed")
	}
	assertExactObjectKeys(
		t,
		objectAt(t, dailyStats, "properties"),
		[]string{"date", "sent", "success", "failed"},
	)
}

func TestPublishedHumanWriteSchemasRejectUnpublishedFields(t *testing.T) {
	document := decodeDocument(t)
	schemas := objectAt(
		t,
		objectAt(t, document, "components"),
		"schemas",
	)
	for _, name := range []string{
		"CreateTicketRequest",
		"UpdateTicketRequest",
		"CreateNotificationRequest",
		"AutomationRuleRequest",
		"UpdateEmailConfigRequest",
		"TestEmailRequest",
		"UpdateSystemConfigRequest",
		"ForgotPasswordRequest",
		"ResetHumanPasswordRequest",
		"AssignTicketRequest",
		"TransferTicketRequest",
		"EscalateTicketRequest",
		"UpdateTicketStatusRequest",
		"CreateTicketCommentRequest",
		"UploadTicketAttachmentRequest",
	} {
		if objectAt(t, schemas, name)["additionalProperties"] != false {
			t.Errorf("%s must reject unpublished fields", name)
		}
	}
}

func TestAllPublishedRequestBodiesUseClosedTopLevelSchemas(t *testing.T) {
	document := decodeDocument(t)
	paths := objectAt(t, document, "paths")
	count := 0
	for path, rawPathItem := range paths {
		pathItem := rawPathItem.(map[string]any)
		for _, method := range []string{"post", "put", "patch", "delete"} {
			rawOperation, ok := pathItem[method]
			if !ok {
				continue
			}
			operation := rawOperation.(map[string]any)
			rawBody, hasBody := operation["requestBody"]
			if !hasBody {
				continue
			}
			count++
			body := resolveComponentObject(
				t,
				document,
				rawBody.(map[string]any),
				"requestBodies",
			)
			media := firstMediaObject(t, objectAt(t, body, "content"))
			schema := resolveComponentObject(
				t,
				document,
				objectAt(t, media, "schema"),
				"schemas",
			)
			if schema["type"] != "object" {
				t.Errorf("%s %s request schema type = %v, want object", method, path, schema["type"])
			}
			if schema["additionalProperties"] != false {
				t.Errorf("%s %s request schema must reject unpublished fields", method, path)
			}
		}
	}
	if count != 32 {
		t.Fatalf("closed request body count = %d, want 32", count)
	}
}

func TestAdminUserPhoneAndManagerConstraintsMatchRuntime(t *testing.T) {
	document := decodeDocument(t)
	schemas := objectAt(
		t,
		objectAt(t, document, "components"),
		"schemas",
	)
	create := objectAt(
		t,
		objectAt(t, schemas, "CreateAdminUserRequest"),
		"properties",
	)
	phone := objectAt(t, create, "phone")
	if phone["pattern"] != `^\+[1-9][0-9]{1,14}$` {
		t.Errorf("CreateAdminUserRequest.phone = %v", phone)
	}
	update := objectAt(
		t,
		objectAt(t, schemas, "UpdateAdminUserRequest"),
		"properties",
	)
	adminAvatar := objectAt(t, update, "avatar")
	assertDeprecatedAvatarCompatibilitySchema(t, adminAvatar)
	if pattern, _ := adminAvatar["pattern"].(string); !strings.Contains(
		pattern,
		"-4[0-9a-f]{3}-[89ab]",
	) {
		t.Fatalf("UpdateAdminUserRequest.avatar = %v", adminAvatar)
	}
	updatePhone := objectAt(t, update, "phone")
	branches, ok := updatePhone["oneOf"].([]any)
	if !ok || len(branches) != 3 {
		t.Fatalf("UpdateAdminUserRequest.phone = %v", updatePhone)
	}
	gotPhoneBranches := make(map[string]struct{}, len(branches))
	for _, rawBranch := range branches {
		branch := rawBranch.(map[string]any)
		key, _ := branch["type"].(string)
		if pattern, exists := branch["pattern"].(string); exists {
			key += ":" + pattern
		}
		gotPhoneBranches[key] = struct{}{}
	}
	for _, expected := range []string{
		`string:^\+[1-9][0-9]{1,14}$`,
		`string:^\s*$`,
		"null",
	} {
		if _, exists := gotPhoneBranches[expected]; !exists {
			t.Errorf("UpdateAdminUserRequest.phone omits %q: %v", expected, updatePhone)
		}
	}
	for _, schemaName := range []string{
		"CreateAdminUserRequest",
		"UpdateAdminUserRequest",
	} {
		properties := objectAt(t, objectAt(t, schemas, schemaName), "properties")
		manager := objectAt(t, properties, "manager_id")
		if manager["minimum"] != float64(1) {
			t.Errorf("%s.manager_id = %v", schemaName, manager)
		}
	}
}

func TestHumanProfileUpdatePublishesValidatedCompatibilityFields(t *testing.T) {
	document := decodeDocument(t)
	schemas := objectAt(
		t,
		objectAt(t, document, "components"),
		"schemas",
	)
	properties := objectAt(
		t,
		objectAt(t, schemas, "UpdateHumanProfileRequest"),
		"properties",
	)
	updateSchema := objectAt(t, schemas, "UpdateHumanProfileRequest")
	if required, exists := updateSchema["required"]; exists {
		t.Errorf(
			"UpdateHumanProfileRequest fields must remain optional: %v",
			required,
		)
	}
	for _, name := range []string{"first_name", "last_name"} {
		property := objectAt(t, properties, name)
		if property["maxLength"] != float64(50) {
			t.Errorf("%s maxLength = %v", name, property["maxLength"])
		}
	}
	timezone := objectAt(t, properties, "timezone")
	if timezone["format"] != "iana-timezone" {
		t.Errorf("timezone schema = %v", timezone)
	}
	language := objectAt(t, properties, "language")
	if !reflect.DeepEqual(language["enum"], []any{"zh-CN", "en"}) {
		t.Errorf("language schema = %v", language)
	}
	phone := objectAt(t, properties, "phone_number")
	if len(phone["oneOf"].([]any)) != 2 {
		t.Errorf("phone_number schema = %v", phone)
	}
	avatar := objectAt(t, properties, "avatar")
	assertDeprecatedAvatarCompatibilitySchema(t, avatar)
	if pattern, _ := avatar["pattern"].(string); !strings.Contains(
		pattern,
		"-4[0-9a-f]{3}-[89ab]",
	) {
		t.Errorf("avatar schema = %v", avatar)
	}
}

func assertDeprecatedAvatarCompatibilitySchema(
	t *testing.T,
	schema map[string]any,
) {
	t.Helper()
	if schema["deprecated"] != true {
		t.Errorf("avatar compatibility field is not deprecated: %v", schema)
	}
	description, _ := schema["description"].(string)
	if !strings.Contains(description, "exact current value") ||
		!strings.Contains(description, "upload endpoint") {
		t.Errorf("avatar compatibility description = %q", description)
	}
}

func assertOperationHasTypedSuccessResponse(
	t *testing.T,
	document map[string]any,
	method string,
	path string,
	operation map[string]any,
) {
	t.Helper()
	responses := objectAt(t, operation, "responses")
	successStatuses := make([]string, 0)
	for status := range responses {
		if regexp.MustCompile(`^2[0-9][0-9]$`).MatchString(status) {
			successStatuses = append(successStatuses, status)
		}
	}
	sort.Strings(successStatuses)
	if len(successStatuses) == 0 {
		t.Errorf("%s %s has no 2xx response", method, path)
		return
	}
	response := resolveComponentObject(
		t,
		document,
		responses[successStatuses[0]].(map[string]any),
		"responses",
	)
	content := objectAt(t, response, "content")
	media := firstMediaObject(t, content)
	schema := objectAt(t, media, "schema")
	if len(schema) == 0 {
		t.Errorf("%s %s has an empty success schema", method, path)
	}
}

func firstMediaObject(
	t *testing.T,
	content map[string]any,
) map[string]any {
	t.Helper()
	mediaTypes := make([]string, 0, len(content))
	for mediaType := range content {
		mediaTypes = append(mediaTypes, mediaType)
	}
	sort.Strings(mediaTypes)
	if len(mediaTypes) == 0 {
		t.Fatal("operation content has no media types")
	}
	if raw, ok := content["application/json"]; ok {
		return raw.(map[string]any)
	}
	return content[mediaTypes[0]].(map[string]any)
}

func assertOperationPathParametersMatch(
	t *testing.T,
	document map[string]any,
	path string,
	pathItem map[string]any,
	operation map[string]any,
) {
	t.Helper()
	expected := make(map[string]struct{})
	for _, match := range regexp.MustCompile(`\{([^}]+)\}`).FindAllStringSubmatch(path, -1) {
		expected[match[1]] = struct{}{}
	}
	actual := make(map[string]struct{})
	for _, owner := range []map[string]any{pathItem, operation} {
		rawParameters, _ := owner["parameters"].([]any)
		for _, raw := range rawParameters {
			parameter := resolveComponentObject(
				t,
				document,
				raw.(map[string]any),
				"parameters",
			)
			if parameter["in"] == "path" {
				name, _ := parameter["name"].(string)
				actual[name] = struct{}{}
			}
		}
	}
	if !reflect.DeepEqual(actual, expected) {
		t.Errorf("%s path parameters = %v, want %v", path, actual, expected)
	}
}

func resolveComponentObject(
	t *testing.T,
	document map[string]any,
	value map[string]any,
	componentType string,
) map[string]any {
	t.Helper()
	reference, referenced := value["$ref"].(string)
	if !referenced {
		return value
	}
	prefix := "#/components/" + componentType + "/"
	if !strings.HasPrefix(reference, prefix) {
		t.Fatalf("unsupported component reference %q", reference)
	}
	components := objectAt(t, document, "components")
	group := objectAt(t, components, componentType)
	return objectAt(t, group, strings.TrimPrefix(reference, prefix))
}

func jsonFieldNames(t *testing.T, value reflect.Type) []string {
	t.Helper()
	if value.Kind() == reflect.Pointer {
		value = value.Elem()
	}
	if value.Kind() != reflect.Struct {
		t.Fatalf("runtime DTO type = %s, want struct", value)
	}
	result := make([]string, 0, value.NumField())
	for index := 0; index < value.NumField(); index++ {
		field := value.Field(index)
		tag := strings.Split(field.Tag.Get("json"), ",")[0]
		if tag == "" || tag == "-" {
			continue
		}
		result = append(result, tag)
	}
	sort.Strings(result)
	return result
}

func assertProjectKeySchema(
	t *testing.T,
	name string,
	schema map[string]any,
	pattern string,
) {
	t.Helper()
	if schema["type"] != "string" ||
		schema["minLength"] != float64(1) ||
		schema["maxLength"] != float64(32) ||
		schema["pattern"] != pattern {
		t.Errorf("%s = %v", name, schema)
	}
}

func decodeDocument(t *testing.T) map[string]any {
	t.Helper()
	var document map[string]any
	if err := json.Unmarshal(Document(), &document); err != nil {
		t.Fatalf("decode Human Web OpenAPI: %v", err)
	}
	return document
}

func stringSet(values ...string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}

func assertRoleAllowlist(
	t *testing.T,
	operation string,
	raw any,
	valid map[string]struct{},
) {
	t.Helper()
	values, ok := raw.([]any)
	if !ok || len(values) == 0 {
		t.Errorf("%s role allowlist = %T, want non-empty array", operation, raw)
		return
	}
	seen := make(map[string]struct{}, len(values))
	for _, rawValue := range values {
		value, ok := rawValue.(string)
		if !ok {
			t.Errorf("%s role allowlist contains %T", operation, rawValue)
			continue
		}
		if _, ok := valid[value]; !ok {
			t.Errorf("%s role allowlist contains unknown role %q", operation, value)
		}
		if _, duplicate := seen[value]; duplicate {
			t.Errorf("%s role allowlist repeats %q", operation, value)
		}
		seen[value] = struct{}{}
	}
}

func assertExactRoleAllowlist(t *testing.T, raw any, want []string) {
	t.Helper()
	values, ok := raw.([]any)
	if !ok {
		t.Fatalf("role allowlist = %T, want array", raw)
	}
	if len(values) != len(want) {
		t.Fatalf("role allowlist = %v, want %v", values, want)
	}
	for index, wantValue := range want {
		if values[index] != wantValue {
			t.Errorf(
				"role allowlist[%d] = %v, want %q",
				index,
				values[index],
				wantValue,
			)
		}
	}
}

func assertExactStringArray(t *testing.T, raw any, want []string) {
	t.Helper()
	values, ok := raw.([]any)
	if !ok {
		t.Fatalf("value = %T, want array", raw)
	}
	if len(values) != len(want) {
		t.Fatalf("value = %v, want %v", values, want)
	}
	for index, wantValue := range want {
		if values[index] != wantValue {
			t.Errorf(
				"value[%d] = %v, want %q",
				index,
				values[index],
				wantValue,
			)
		}
	}
}

func assertExactObjectKeys(
	t *testing.T,
	object map[string]any,
	want []string,
) {
	t.Helper()
	if len(object) != len(want) {
		t.Fatalf("object has %d keys, want %d: %v", len(object), len(want), object)
	}
	for _, key := range want {
		if _, ok := object[key]; !ok {
			t.Errorf("object is missing %q", key)
		}
	}
}

func assertClosedObjectInstance(
	t *testing.T,
	schema map[string]any,
	instance map[string]any,
) {
	t.Helper()
	if schema["type"] != "object" || schema["additionalProperties"] != false {
		t.Fatalf("schema is not a closed object: %v", schema)
	}
	properties := objectAt(t, schema, "properties")
	for key := range instance {
		if _, ok := properties[key]; !ok {
			t.Errorf("runtime instance has unpublished property %q", key)
		}
	}
	required, ok := schema["required"].([]any)
	if !ok {
		t.Fatal("closed object schema has no required list")
	}
	for _, raw := range required {
		key, ok := raw.(string)
		if !ok {
			t.Fatalf("required field name = %T", raw)
		}
		if _, exists := instance[key]; !exists {
			t.Errorf("runtime instance is missing required property %q", key)
		}
	}
}

func requestSchemaRef(t *testing.T, operation map[string]any) string {
	t.Helper()
	requestBody := objectAt(t, operation, "requestBody")
	if requestBody["required"] != true {
		t.Fatal("requestBody.required must be true")
	}
	content := objectAt(t, requestBody, "content")
	media := objectAt(t, content, "application/json")
	schema := objectAt(t, media, "schema")
	reference, _ := schema["$ref"].(string)
	return reference
}

func responseSchemaRef(
	t *testing.T,
	operation map[string]any,
	status string,
) string {
	t.Helper()
	responses := objectAt(t, operation, "responses")
	response := objectAt(t, responses, status)
	content := objectAt(t, response, "content")
	media := objectAt(t, content, "application/json")
	schema := objectAt(t, media, "schema")
	reference, _ := schema["$ref"].(string)
	return reference
}

func successResponseSchema(
	t *testing.T,
	operation map[string]any,
	status string,
) map[string]any {
	t.Helper()
	responses := objectAt(t, operation, "responses")
	response := objectAt(t, responses, status)
	content := objectAt(t, response, "content")
	media := objectAt(t, content, "application/json")
	return objectAt(t, media, "schema")
}

func responseDataReference(
	t *testing.T,
	operation map[string]any,
	status string,
) string {
	t.Helper()
	responses := objectAt(t, operation, "responses")
	response := objectAt(t, responses, status)
	content := objectAt(t, response, "content")
	media := objectAt(t, content, "application/json")
	schema := objectAt(t, media, "schema")
	allOf := schema["allOf"].([]any)
	extension := allOf[len(allOf)-1].(map[string]any)
	properties := objectAt(t, extension, "properties")
	data := objectAt(t, properties, "data")
	reference, _ := data["$ref"].(string)
	return reference
}

func objectAt(t *testing.T, value map[string]any, key string) map[string]any {
	t.Helper()
	raw, ok := value[key]
	if !ok {
		t.Fatalf("%s is missing", key)
	}
	object, ok := raw.(map[string]any)
	if !ok {
		t.Fatalf("%s is %T, want object", key, raw)
	}
	return object
}

func assertStringEnum(
	t *testing.T,
	schemas map[string]any,
	name string,
	want []string,
) {
	t.Helper()
	schema := objectAt(t, schemas, name)
	if schema["type"] != "string" {
		t.Fatalf("%s.type = %v, want string", name, schema["type"])
	}
	rawValues, ok := schema["enum"].([]any)
	if !ok {
		t.Fatalf("%s.enum = %T, want array", name, schema["enum"])
	}
	if len(rawValues) != len(want) {
		t.Fatalf("%s.enum = %v, want %v", name, rawValues, want)
	}
	for index, wantValue := range want {
		if rawValues[index] != wantValue {
			t.Errorf(
				"%s.enum[%d] = %v, want %q",
				name,
				index,
				rawValues[index],
				wantValue,
			)
		}
	}
}
