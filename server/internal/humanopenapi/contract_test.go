package humanopenapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
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
	"github.com/seaworld008/chronodesk/server/internal/routeinventory"
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
	if got := document["x-chronodesk-types-generator"]; got != "2.1.0" {
		t.Fatalf("types generator = %v, want 2.1.0", got)
	}

	components := objectAt(t, document, "components")
	schemas := objectAt(t, components, "schemas")
	for _, name := range []string{
		"PlatformRole",
		"ProjectRole",
		"RegisterHumanRequest",
		"LoginRequest",
		"RefreshTokenRequest",
		"LogoutRequest",
		"ForgotPasswordRequest",
		"ResetHumanPasswordRequest",
		"VerifyHumanEmailRequest",
		"ResendHumanEmailVerificationRequest",
		"HumanSessionUser",
		"AuthSession",
		"HumanRegistrationResult",
		"HumanRegistrationEnvelope",
		"AuthorizedProject",
		"AuthorizedProjectAccess",
		"ProjectMembership",
		"AdminUser",
		"PlatformProjectSummary",
		"PlatformProjectPageEnvelope",
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

func TestProjectMembershipWritesRequireOptimisticVersionPreconditions(
	t *testing.T,
) {
	document := decodeDocument(t)
	components := objectAt(t, document, "components")
	schemas := objectAt(t, components, "schemas")
	request := objectAt(t, schemas, "UpsertProjectMembershipRequest")
	required, ok := request["required"].([]any)
	if !ok {
		t.Fatalf("membership request required = %T", request["required"])
	}
	requiredFields := make(map[string]struct{}, len(required))
	for _, raw := range required {
		value, ok := raw.(string)
		if !ok {
			t.Fatalf("membership required field = %T", raw)
		}
		requiredFields[value] = struct{}{}
	}
	if _, ok := requiredFields["expected_version"]; !ok {
		t.Fatal("membership upsert does not require expected_version")
	}
	expectedVersion := objectAt(
		t,
		objectAt(t, request, "properties"),
		"expected_version",
	)
	if expectedVersion["minimum"] != float64(0) {
		t.Fatalf(
			"membership upsert expected_version minimum = %v",
			expectedVersion["minimum"],
		)
	}

	paths := objectAt(t, document, "paths")
	upsert := objectAt(
		t,
		objectAt(t, paths, "/projects/{projectKey}/memberships"),
		"post",
	)
	upsertResponses := objectAt(t, upsert, "responses")
	for _, status := range []string{"409", "428"} {
		if _, ok := upsertResponses[status]; !ok {
			t.Errorf("membership upsert response %s is missing", status)
		}
	}

	deactivate := objectAt(
		t,
		objectAt(
			t,
			paths,
			"/projects/{projectKey}/memberships/{userID}",
		),
		"delete",
	)
	query := operationQueryParameters(t, document, deactivate)
	deactivateVersion, ok := query["expected_version"]
	if !ok {
		t.Fatal("membership deactivate expected_version query is missing")
	}
	if deactivateVersion["required"] != true {
		t.Fatal("membership deactivate expected_version is not required")
	}
	deactivateVersionSchema := objectAt(
		t,
		deactivateVersion,
		"schema",
	)
	if deactivateVersionSchema["minimum"] != float64(1) {
		t.Fatalf(
			"membership deactivate expected_version minimum = %v",
			deactivateVersionSchema["minimum"],
		)
	}
	deactivateResponses := objectAt(t, deactivate, "responses")
	for _, status := range []string{"409", "428"} {
		if _, ok := deactivateResponses[status]; !ok {
			t.Errorf("membership deactivate response %s is missing", status)
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
			path:       "/auth/register",
			schemaName: "RegisterHumanRequest",
			required: []string{
				"username",
				"email",
				"password",
				"confirm_password",
			},
			properties: []string{
				"username",
				"email",
				"password",
				"confirm_password",
				"first_name",
				"last_name",
				"department",
				"position",
			},
		},
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
		{
			path:       "/auth/verify-email",
			schemaName: "VerifyHumanEmailRequest",
			required:   []string{"token"},
			properties: []string{"token"},
		},
		{
			path:       "/auth/resend-verification",
			schemaName: "ResendHumanEmailVerificationRequest",
			required:   []string{"email"},
			properties: []string{"email"},
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
	for _, field := range []struct {
		schema string
		name   string
	}{
		{schema: "RegisterHumanRequest", name: "email"},
		{schema: "LoginRequest", name: "email"},
		{schema: "LoginRequest", name: "device_name"},
		{schema: "ForgotPasswordRequest", name: "email"},
		{schema: "ResendHumanEmailVerificationRequest", name: "email"},
	} {
		property := objectAt(
			t,
			objectAt(t, objectAt(t, schemas, field.schema), "properties"),
			field.name,
		)
		if property["maxLength"] != float64(100) {
			t.Errorf(
				"%s.%s maxLength = %v, want 100",
				field.schema,
				field.name,
				property["maxLength"],
			)
		}
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
	for _, required := range []string{
		"project",
		"project_role",
		"can_create_knowledge_drafts",
		"scope",
	} {
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
		{"/auth/register", "post"},
		{"/auth/verify-email", "post"},
		{"/auth/resend-verification", "post"},
		{"/auth/logout", "post"},
		{"/auth/logout-all", "post"},
		{"/auth/me", "get"},
		{"/auth/profile", "put"},
		{"/projects/{projectKey}/context", "get"},
		{"/projects/{projectKey}/memberships", "get"},
		{"/projects/{projectKey}/memberships", "post"},
		{"/projects/{projectKey}/membership-candidates", "get"},
		{"/projects/{projectKey}/memberships/{userID}", "delete"},
		{"/platform/projects", "get"},
		{"/platform/projects", "post"},
		{"/platform/project-creation-context", "get"},
		{"/platform/project-business-units", "get"},
		{"/platform/projects/{projectPublicID}/archive", "post"},
		{"/platform/users", "get"},
		{"/platform/users", "post"},
		{"/platform/users/stats", "get"},
		{"/platform/users/{userID}", "get"},
		{"/platform/users/{userID}", "put"},
		{"/platform/users/{userID}", "delete"},
		{"/platform/users/{userID}/reset-password", "post"},
		{"/platform/emergency-controls", "get"},
		{"/platform/emergency-controls", "put"},
		{"/platform/audit-logs", "get"},
		{"/platform/audit-logs/{auditLogID}", "get"},
	} {
		pathItem := objectAt(t, paths, expected.path)
		if _, ok := pathItem[expected.method]; !ok {
			t.Errorf("%s %s is missing", expected.method, expected.path)
		}
	}
}

func TestProjectGovernanceContractUsesPublicScopeBoundedQueriesAndExplicitAdmins(
	t *testing.T,
) {
	document := decodeDocument(t)
	paths := objectAt(t, document, "paths")
	schemas := objectAt(
		t,
		objectAt(t, document, "components"),
		"schemas",
	)

	list := objectAt(t, objectAt(t, paths, "/platform/projects"), "get")
	listParameters, ok := list["parameters"].([]any)
	if !ok {
		t.Fatal("platform project list parameters are missing")
	}
	listNames := make([]string, 0, len(listParameters))
	for _, rawParameter := range listParameters {
		parameter := rawParameter.(map[string]any)
		listNames = append(listNames, parameter["name"].(string))
		if parameter["in"] != "query" {
			t.Fatalf("platform project list parameter = %v", parameter)
		}
	}
	if want := []string{
		"page",
		"page_size",
		"search",
		"status",
		"business_unit_public_id",
		"order_by",
		"order",
	}; !reflect.DeepEqual(listNames, want) {
		t.Fatalf("platform project list parameters = %v, want %v", listNames, want)
	}
	pageSizeSchema := objectAt(
		t,
		listParameters[1].(map[string]any),
		"schema",
	)
	if pageSizeSchema["default"] != float64(25) ||
		pageSizeSchema["minimum"] != float64(1) ||
		pageSizeSchema["maximum"] != float64(100) {
		t.Fatalf("platform project page_size schema = %v", pageSizeSchema)
	}

	request := objectAt(t, schemas, "CreatePlatformProjectRequest")
	if request["additionalProperties"] != false {
		t.Fatal("CreatePlatformProjectRequest must reject numeric trusted scope")
	}
	required := []string{
		"business_unit_public_id",
		"key",
		"name",
		"initial_project_admin_user_ids",
		"default_queue_key",
		"default_queue_name",
	}
	assertExactStringArray(t, request["required"], required)
	assertExactObjectKeys(
		t,
		objectAt(t, request, "properties"),
		append(required, "description"),
	)
	for _, forbidden := range []string{
		"organization_id",
		"business_unit_id",
		"administrator_id",
	} {
		if _, ok := objectAt(t, request, "properties")[forbidden]; ok {
			t.Errorf(
				"CreatePlatformProjectRequest exposes trusted field %q",
				forbidden,
			)
		}
	}
	initialAdmins := objectAt(
		t,
		objectAt(t, request, "properties"),
		"initial_project_admin_user_ids",
	)
	if initialAdmins["minItems"] != float64(1) ||
		initialAdmins["uniqueItems"] != true {
		t.Fatalf("initial administrator schema = %v", initialAdmins)
	}

	for _, schemaName := range []string{
		"ProjectCreationContext",
		"PlatformProjectPage",
		"PlatformProjectSummary",
		"PlatformBusinessUnitPageEnvelope",
		"PlatformBusinessUnitPage",
		"PlatformBusinessUnitSummary",
		"ProjectUserOptionPage",
	} {
		if objectAt(t, schemas, schemaName)["additionalProperties"] != false {
			t.Errorf("%s must be a closed DTO", schemaName)
		}
	}

	pageFields := []string{
		"items",
		"total",
		"page",
		"page_size",
		"total_pages",
	}
	for _, schemaName := range []string{
		"PlatformProjectPage",
		"PlatformBusinessUnitPage",
		"ProjectUserOptionPage",
	} {
		page := objectAt(t, schemas, schemaName)
		assertExactStringArray(t, page["required"], pageFields)
		assertExactObjectKeys(
			t,
			objectAt(t, page, "properties"),
			pageFields,
		)
	}

	contextOperation := objectAt(
		t,
		objectAt(t, paths, "/platform/project-creation-context"),
		"get",
	)
	contextParameters, ok := contextOperation["parameters"].([]any)
	if !ok {
		t.Fatal("project creation context parameters are missing")
	}
	contextNames := make([]string, 0, len(contextParameters))
	for _, rawParameter := range contextParameters {
		parameter := rawParameter.(map[string]any)
		contextNames = append(
			contextNames,
			parameter["name"].(string),
		)
		if parameter["in"] != "query" {
			t.Fatalf("project creation context parameter = %v", parameter)
		}
	}
	if want := []string{
		"page",
		"page_size",
		"search",
		"business_unit_page",
		"business_unit_page_size",
		"business_unit_search",
	}; !reflect.DeepEqual(contextNames, want) {
		t.Fatalf(
			"project creation context parameters = %v, want %v",
			contextNames,
			want,
		)
	}
	for _, index := range []int{1, 4} {
		pageSize := objectAt(
			t,
			contextParameters[index].(map[string]any),
			"schema",
		)
		if pageSize["default"] != float64(25) ||
			pageSize["minimum"] != float64(1) ||
			pageSize["maximum"] != float64(100) {
			t.Errorf(
				"project creation context page size = %v",
				pageSize,
			)
		}
	}

	contextProperties := objectAt(
		t,
		objectAt(t, schemas, "ProjectCreationContext"),
		"properties",
	)
	for propertyName, wantReference := range map[string]string{
		"business_units": "#/components/schemas/PlatformBusinessUnitPage",
		"users":          "#/components/schemas/ProjectUserOptionPage",
	} {
		property := objectAt(t, contextProperties, propertyName)
		if got, _ := property["$ref"].(string); got != wantReference {
			t.Errorf(
				"ProjectCreationContext.%s = %q, want %q",
				propertyName,
				got,
				wantReference,
			)
		}
	}

	businessUnitsOperation := objectAt(
		t,
		objectAt(t, paths, "/platform/project-business-units"),
		"get",
	)
	assertExactStringArray(
		t,
		businessUnitsOperation["x-chronodesk-platform-roles"],
		[]string{"platform_admin"},
	)
	businessUnitParameters :=
		businessUnitsOperation["parameters"].([]any)
	businessUnitNames := make([]string, 0, len(businessUnitParameters))
	for _, rawParameter := range businessUnitParameters {
		parameter := rawParameter.(map[string]any)
		businessUnitNames = append(
			businessUnitNames,
			parameter["name"].(string),
		)
	}
	if want := []string{"page", "page_size", "search"}; !reflect.DeepEqual(
		businessUnitNames,
		want,
	) {
		t.Fatalf(
			"platform Business Unit query = %v, want %v",
			businessUnitNames,
			want,
		)
	}
	if got := responseSchemaRef(
		t,
		businessUnitsOperation,
		"200",
	); got != "#/components/schemas/PlatformBusinessUnitPageEnvelope" {
		t.Fatalf("platform Business Unit page response = %q", got)
	}
	businessUnitEnvelope := objectAt(
		t,
		schemas,
		"PlatformBusinessUnitPageEnvelope",
	)
	if businessUnitEnvelope["additionalProperties"] != false {
		t.Fatal("PlatformBusinessUnitPageEnvelope must be closed")
	}
	businessUnitData := objectAt(
		t,
		objectAt(t, businessUnitEnvelope, "properties"),
		"data",
	)
	if got, _ := businessUnitData["$ref"].(string); got !=
		"#/components/schemas/PlatformBusinessUnitPage" {
		t.Fatalf("platform Business Unit envelope data = %q", got)
	}

	createOperation := objectAt(
		t,
		objectAt(t, paths, "/platform/projects"),
		"post",
	)
	assertExactObjectKeys(
		t,
		objectAt(t, createOperation, "responses"),
		[]string{"201", "400", "401", "403", "429", "500", "503"},
	)
}

func TestProjectMembershipCandidateContractIsRemoteBoundedAndAdminOnly(
	t *testing.T,
) {
	document := decodeDocument(t)
	paths := objectAt(t, document, "paths")
	operation := objectAt(
		t,
		objectAt(
			t,
			paths,
			"/projects/{projectKey}/membership-candidates",
		),
		"get",
	)
	assertExactStringArray(
		t,
		operation["x-chronodesk-project-roles"],
		[]string{"project_admin"},
	)
	parameters := operation["parameters"].([]any)
	if len(parameters) != 4 {
		t.Fatalf("membership candidate parameters = %v", parameters)
	}
	queryNames := make([]string, 0, 3)
	for _, rawParameter := range parameters[1:] {
		parameter := rawParameter.(map[string]any)
		queryNames = append(queryNames, parameter["name"].(string))
	}
	if want := []string{"page", "page_size", "search"}; !reflect.DeepEqual(
		queryNames,
		want,
	) {
		t.Fatalf("membership candidate query = %v, want %v", queryNames, want)
	}
	pageSize := objectAt(t, parameters[2].(map[string]any), "schema")
	if pageSize["default"] != float64(25) ||
		pageSize["maximum"] != float64(100) {
		t.Fatalf("membership candidate page size = %v", pageSize)
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
	if list != "#/components/schemas/PlatformProjectPageEnvelope" {
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
		"PlatformProjectPageEnvelope",
	)
	if listEnvelope["type"] != "object" ||
		listEnvelope["additionalProperties"] != false {
		t.Fatalf(
			"PlatformProjectPageEnvelope = %v",
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
	if reference, _ := listData["$ref"].(string); reference !=
		"#/components/schemas/PlatformProjectPage" {
		t.Fatalf("platform project list data = %v", listData)
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
	parameters, published := operation["parameters"].([]any)
	if !published || len(parameters) != 7 {
		t.Fatalf(
			"GET /platform/projects parameters = %v",
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
		publicID := resolveComponentObject(
			t,
			document,
			objectAt(
				t,
				objectAt(
					t,
					objectAt(t, schemas, schemaName),
					"properties",
				),
				"public_id",
			),
			"schemas",
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
			path: "/auth/register",
			want: []string{"201", "400", "409", "413", "429", "500", "503"},
		},
		{
			path: "/auth/forgot-password",
			want: []string{"200", "400", "413", "429", "503"},
		},
		{
			path: "/auth/reset-password",
			want: []string{"200", "400", "413", "429", "500", "503"},
		},
		{
			path: "/auth/verify-email",
			want: []string{"200", "400", "413", "429", "500", "503"},
		},
		{
			path: "/auth/resend-verification",
			want: []string{"200", "400", "413", "429", "503"},
		},
		{
			path: "/auth/login",
			want: []string{"200", "400", "401", "403", "413", "429", "503"},
		},
		{
			path: "/auth/refresh",
			want: []string{"200", "400", "401", "408", "413", "429", "503"},
		},
		{
			path: "/auth/logout",
			want: []string{"200", "400", "413", "429", "503"},
		},
		{
			path: "/auth/logout-all",
			want: []string{"200", "401", "429", "500", "503"},
		},
		{
			path: "/auth/profile",
			want: []string{"200", "400", "401", "413"},
		},
	} {
		t.Run(test.path, func(t *testing.T) {
			method := "post"
			if test.path == "/auth/profile" {
				method = "put"
			}
			operation := objectAt(
				t,
				objectAt(t, paths, test.path),
				method,
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
		{"/auth/register", "post"},
		{"/auth/forgot-password", "post"},
		{"/auth/reset-password", "post"},
		{"/auth/verify-email", "post"},
		{"/auth/resend-verification", "post"},
		{"/platform/projects/{projectPublicID}/archive", "post"},
		{"/workbench/tickets", "get"},
		{"/workbench/dashboard", "get"},
		{"/projects/{projectKey}/tickets", "get"},
		{"/projects/{projectKey}/tickets", "post"},
		{"/projects/{projectKey}/tickets/my-tickets", "get"},
		{"/projects/{projectKey}/tickets/unassigned", "get"},
		{"/projects/{projectKey}/tickets/overdue", "get"},
		{"/projects/{projectKey}/tickets/sla-breach", "get"},
		{"/projects/{projectKey}/tickets/{ticketID}", "get"},
		{"/projects/{projectKey}/tickets/{ticketID}", "put"},
		{"/projects/{projectKey}/tickets/{ticketID}", "delete"},
		{"/projects/{projectKey}/tickets/{ticketID}/assign", "post"},
		{"/projects/{projectKey}/tickets/{ticketID}/transfer", "post"},
		{"/projects/{projectKey}/tickets/{ticketID}/escalate", "post"},
		{"/projects/{projectKey}/tickets/{ticketID}/status", "post"},
		{"/projects/{projectKey}/tickets/{ticketID}/transitions", "get"},
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
		{"/projects/{projectKey}/knowledge/articles", "get"},
		{"/projects/{projectKey}/knowledge/articles", "post"},
		{"/projects/{projectKey}/knowledge/articles/{articleID}/drafts", "post"},
		{"/projects/{projectKey}/knowledge/articles/{articleID}/document", "get"},
		{"/projects/{projectKey}/knowledge/versions/{versionID}/publication", "post"},
		{"/projects/{projectKey}/knowledge/searches", "post"},
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

func TestNotificationPreferenceArrayBoundMatchesRuntimeTypes(t *testing.T) {
	document := decodeDocument(t)
	schemas := objectAt(
		t,
		objectAt(t, document, "components"),
		"schemas",
	)
	request := objectAt(t, schemas, "UpdateNotificationPreferencesRequest")
	properties := objectAt(t, request, "properties")
	preferences := objectAt(t, properties, "preferences")
	if preferences["minItems"] != float64(1) ||
		preferences["maxItems"] != float64(len(models.NotificationTypes())) {
		t.Fatalf(
			"notification preference bounds=%v, runtime types=%d",
			preferences,
			len(models.NotificationTypes()),
		)
	}
}

func TestTicketUpdateDueDateIsOptionalAndNullable(t *testing.T) {
	document := decodeDocument(t)
	schemas := objectAt(
		t,
		objectAt(t, document, "components"),
		"schemas",
	)
	request := objectAt(t, schemas, "UpdateTicketRequest")
	required, ok := request["required"].([]any)
	if !ok {
		t.Fatalf("UpdateTicketRequest.required = %T, want array", request["required"])
	}
	for _, field := range required {
		if field == "due_date" {
			t.Fatal("UpdateTicketRequest.due_date must remain optional")
		}
	}

	dueDate := objectAt(t, objectAt(t, request, "properties"), "due_date")
	if got, want := dueDate["type"], []any{"string", "null"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("UpdateTicketRequest.due_date.type = %v, want %v", got, want)
	}
	if dueDate["format"] != "date-time" {
		t.Fatalf("UpdateTicketRequest.due_date.format = %v, want date-time", dueDate["format"])
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

func TestEmergencyControlContractIsExactRoleStrictAndCASProtected(t *testing.T) {
	document := decodeDocument(t)
	paths := objectAt(t, document, "paths")
	components := objectAt(t, document, "components")
	schemas := objectAt(t, components, "schemas")
	path := objectAt(t, paths, "/platform/emergency-controls")

	for _, method := range []string{"get", "put"} {
		operation := objectAt(t, path, method)
		assertExactStringArray(
			t,
			operation["x-chronodesk-platform-roles"],
			[]string{"emergency_operator"},
		)
		responses := objectAt(t, operation, "responses")
		success := objectAt(t, responses, "200")
		headers := objectAt(t, success, "headers")
		if objectAt(t, headers, "ETag")["$ref"] !=
			"#/components/headers/ETag" {
			t.Errorf("%s emergency-control response omits strong ETag", method)
		}
	}

	update := objectAt(t, path, "put")
	parameters, ok := update["parameters"].([]any)
	if !ok || len(parameters) != 1 {
		t.Fatalf("emergency-control PUT parameters = %v", update["parameters"])
	}
	if parameters[0].(map[string]any)["$ref"] !=
		"#/components/parameters/IfMatch" {
		t.Fatalf("emergency-control PUT must require shared If-Match")
	}
	if got := requestSchemaRef(t, update); got !=
		"#/components/schemas/UpdateEmergencyControlsRequest" {
		t.Fatalf("emergency-control request schema = %q", got)
	}
	for _, status := range []string{"400", "401", "403", "412", "428", "429", "503"} {
		if _, ok := objectAt(t, update, "responses")[status]; !ok {
			t.Errorf("emergency-control PUT response %s is missing", status)
		}
	}
	stale := objectAt(t, objectAt(t, update, "responses"), "412")
	if objectAt(t, objectAt(t, stale, "headers"), "ETag")["$ref"] !=
		"#/components/headers/ETag" {
		t.Error("stale emergency-control response omits current ETag")
	}

	request := objectAt(t, schemas, "UpdateEmergencyControlsRequest")
	if request["additionalProperties"] != false ||
		request["minProperties"] != float64(1) {
		t.Fatalf("emergency-control request is not strict: %v", request)
	}
	assertExactObjectKeys(
		t,
		objectAt(t, request, "properties"),
		[]string{"global_read_only", "emergency_stop"},
	)
	if required, exists := request["required"]; exists {
		t.Fatalf(
			"independent emergency-control fields must remain optional: %v",
			required,
		)
	}

	snapshot := objectAt(t, schemas, "EmergencyControlSnapshot")
	if snapshot["additionalProperties"] != false {
		t.Fatal("EmergencyControlSnapshot must be a closed DTO")
	}
	fields := []string{
		"global_read_only",
		"emergency_stop",
		"version",
		"updated_at",
	}
	assertExactStringArray(t, snapshot["required"], fields)
	assertExactObjectKeys(t, objectAt(t, snapshot, "properties"), fields)
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
			"webhook_url_masked",
			"has_webhook_url",
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
	for _, forbidden := range []string{
		"webhook_url",
		"secret",
		"previous_secret",
		"access_token",
	} {
		if _, exposed := objectAt(t, webhookConfig, "properties")[forbidden]; exposed {
			t.Errorf("WebhookConfig exposes sensitive field %s", forbidden)
		}
	}

	paths := objectAt(t, document, "paths")
	logData := objectAt(t, schemas, "WebhookLogPage")
	logItems := objectAt(
		t,
		objectAt(t, objectAt(t, logData, "properties"), "items"),
		"items",
	)
	logItemSchema := objectAt(t, schemas, "WebhookLog")
	if logData["additionalProperties"] != false ||
		logItemSchema["additionalProperties"] != false {
		t.Fatal("Webhook log page and items must be closed")
	}
	assertExactObjectKeys(
		t,
		objectAt(t, logItemSchema, "properties"),
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
	if logItems["$ref"] != "#/components/schemas/WebhookLog" {
		t.Fatalf("WebhookLogPage.items ref = %v", logItems)
	}

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

func TestAutomationAndWebhookListErrorsUseRuntimeEnvelopes(t *testing.T) {
	document := decodeDocument(t)
	paths := objectAt(t, document, "paths")
	for _, test := range []struct {
		path     string
		statuses []string
		response string
	}{
		{
			path:     "/projects/{projectKey}/admin/automation/rules",
			statuses: []string{"400", "500"},
			response: "#/components/responses/LegacyError",
		},
		{
			path:     "/projects/{projectKey}/admin/automation/logs",
			statuses: []string{"400", "500", "503"},
			response: "#/components/responses/LegacyError",
		},
		{
			path:     "/projects/{projectKey}/webhooks",
			statuses: []string{"400", "500"},
			response: "#/components/responses/StandardError",
		},
		{
			path:     "/projects/{projectKey}/webhooks/{webhookID}/logs",
			statuses: []string{"400", "404", "500", "503"},
			response: "#/components/responses/StandardError",
		},
	} {
		t.Run(test.path, func(t *testing.T) {
			operation := objectAt(
				t,
				objectAt(t, paths, test.path),
				"get",
			)
			responses := objectAt(t, operation, "responses")
			for _, status := range test.statuses {
				response := objectAt(t, responses, status)
				if response["$ref"] != test.response {
					t.Errorf(
						"response %s = %v, want %q",
						status,
						response["$ref"],
						test.response,
					)
				}
			}
		})
	}

	responses := objectAt(
		t,
		objectAt(t, document, "components"),
		"responses",
	)
	for name, schemaReference := range map[string]string{
		"LegacyError":   "#/components/schemas/LegacyErrorEnvelope",
		"StandardError": "#/components/schemas/StandardErrorEnvelope",
	} {
		response := objectAt(t, responses, name)
		content := objectAt(t, response, "content")
		media := objectAt(t, content, "application/json")
		schema := objectAt(t, media, "schema")
		if schema["$ref"] != schemaReference {
			t.Errorf("%s schema = %v, want %q", name, schema, schemaReference)
		}
	}
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
	if count < 32 {
		t.Fatalf("closed request body count = %d, want at least 32", count)
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

func TestAgentControlListsPublishStrictStrategiesAndSafeEnvelopes(
	t *testing.T,
) {
	document := decodeDocument(t)
	paths := objectAt(t, document, "paths")
	components := objectAt(t, document, "components")
	parameters := objectAt(t, components, "parameters")
	schemas := objectAt(t, components, "schemas")

	for _, test := range []struct {
		path         string
		operationID  string
		strategy     string
		queryNames   []string
		responseName string
	}{
		{
			path:         "/projects/{projectKey}/admin/agents/service-principals",
			operationID:  "listAgentServicePrincipals",
			strategy:     "page",
			queryNames:   []string{"page", "page_size", "sort_by", "sort_order"},
			responseName: "AdminPrincipalPageEnvelope",
		},
		{
			path:         "/projects/{projectKey}/admin/agents/service-principals/{principalId}/policies",
			operationID:  "listServicePrincipalPoliciesV2",
			strategy:     "page",
			queryNames:   []string{"page", "page_size", "sort_by", "sort_order"},
			responseName: "AdminPolicyPageEnvelope",
		},
		{
			path:         "/projects/{projectKey}/admin/agents/leases",
			operationID:  "listAgentTicketLeases",
			strategy:     "page",
			queryNames:   []string{"page", "page_size", "sort_by", "sort_order"},
			responseName: "AdminLeasePageEnvelope",
		},
		{
			path:         "/projects/{projectKey}/admin/agents/attachments",
			operationID:  "listAgentAttachmentScans",
			strategy:     "page",
			queryNames:   []string{"page", "page_size", "sort_by", "sort_order"},
			responseName: "AdminAttachmentPageEnvelope",
		},
		{
			path:         "/projects/{projectKey}/admin/agents/outbox",
			operationID:  "listAgentOutboxDeliveries",
			strategy:     "page",
			queryNames:   []string{"page", "page_size", "sort_by", "sort_order"},
			responseName: "AdminOutboxPageEnvelope",
		},
		{
			path:         "/projects/{projectKey}/admin/agents/events",
			operationID:  "listAgentDomainEvents",
			strategy:     "cursor",
			queryNames:   []string{"cursor", "limit"},
			responseName: "AdminDomainEventCursorEnvelope",
		},
		{
			path:         "/projects/{projectKey}/admin/agents/policy-decisions",
			operationID:  "listAgentPolicyDecisions",
			strategy:     "cursor",
			queryNames:   []string{"cursor", "limit"},
			responseName: "AdminPolicyDecisionCursorEnvelope",
		},
	} {
		t.Run(test.operationID, func(t *testing.T) {
			pathItem := objectAt(t, paths, test.path)
			operation := objectAt(t, pathItem, "get")
			if operation["operationId"] != test.operationID {
				t.Fatalf("operationId=%v", operation["operationId"])
			}
			if operation["x-list-strategy"] != test.strategy {
				t.Fatalf(
					"x-list-strategy=%v, want %s",
					operation["x-list-strategy"],
					test.strategy,
				)
			}
			names := make([]string, 0, len(test.queryNames))
			for _, raw := range operation["parameters"].([]any) {
				parameter := raw.(map[string]any)
				if reference, ok := parameter["$ref"].(string); ok {
					const prefix = "#/components/parameters/"
					parameter = objectAt(
						t,
						parameters,
						strings.TrimPrefix(reference, prefix),
					)
				}
				if parameter["in"] == "query" {
					name := parameter["name"].(string)
					names = append(names, name)
					schema := objectAt(t, parameter, "schema")
					switch name {
					case "page":
						if schema["minimum"] != float64(1) ||
							schema["default"] != float64(1) {
							t.Errorf("page schema=%v", schema)
						}
					case "page_size", "limit":
						if schema["minimum"] != float64(1) ||
							schema["maximum"] != float64(100) ||
							schema["default"] != float64(25) {
							t.Errorf("%s schema=%v", name, schema)
						}
					case "cursor":
						if schema["maxLength"] != float64(2048) {
							t.Errorf("cursor schema=%v", schema)
						}
					case "sort_by", "sort_order":
						if schema["const"] == nil ||
							schema["default"] != schema["const"] {
							t.Errorf("%s schema=%v", name, schema)
						}
					}
				}
			}
			sort.Strings(names)
			sort.Strings(test.queryNames)
			if !reflect.DeepEqual(names, test.queryNames) {
				t.Fatalf("query parameters=%v, want %v", names, test.queryNames)
			}
			if got := responseSchemaRef(t, operation, "200"); got !=
				"#/components/schemas/"+test.responseName {
				t.Fatalf("response schema=%q", got)
			}
		})
	}
	for schemaName, fields := range map[string][]string{
		"AdminPrincipalPage": {
			"items", "total", "page", "page_size", "total_pages",
		},
		"AdminPolicyPage": {
			"items", "total", "page", "page_size", "total_pages",
		},
		"AdminLeasePage": {
			"items", "total", "page", "page_size", "total_pages",
		},
		"AdminAttachmentPage": {
			"items", "total", "page", "page_size", "total_pages",
		},
		"AdminOutboxPage": {
			"items", "total", "page", "page_size", "total_pages",
		},
		"AdminDomainEventCursorPage": {
			"items", "next_cursor", "has_more",
		},
		"AdminPolicyDecisionCursorPage": {
			"items", "next_cursor", "has_more",
		},
	} {
		schema := objectAt(t, schemas, schemaName)
		assertExactStringArray(t, schema["required"], fields)
		assertExactObjectKeys(t, objectAt(t, schema, "properties"), fields)
	}

	overview := objectAt(t, schemas, "AdminOverview")
	for name, raw := range objectAt(t, overview, "properties") {
		property := raw.(map[string]any)
		if property["type"] == "array" {
			t.Errorf("AdminOverview.%s remains a dynamic array", name)
		}
	}
	for schemaName, forbidden := range map[string][]string{
		"AdminAgentPolicy": {
			"conditions",
		},
		"AdminAttachmentSummary": {
			"storage_path",
			"storage_url",
			"access_token",
			"hash",
			"metadata",
			"scan_details",
		},
		"AdminDomainEventSummary": {
			"data",
		},
		"AdminPolicyDecisionSummary": {
			"context",
			"request_digest",
		},
		"AdminOutboxDeliverySummary": {
			"destination_id",
			"locked_by",
			"locked_at",
		},
	} {
		properties := objectAt(
			t,
			objectAt(t, schemas, schemaName),
			"properties",
		)
		for _, field := range forbidden {
			if _, exists := properties[field]; exists {
				t.Errorf("%s exposes forbidden field %s", schemaName, field)
			}
		}
	}
}

func TestHumanListClassificationInventoryBlocksNewUnclassifiedLists(
	t *testing.T,
) {
	document := decodeDocument(t)
	rawInventory, ok := document["x-chronodesk-legacy-list-inventory"].([]any)
	if !ok {
		t.Fatal("x-chronodesk-legacy-list-inventory must be an explicit array")
	}
	if len(rawInventory) != 0 {
		t.Fatalf(
			"x-chronodesk-legacy-list-inventory must be empty, got %v",
			rawInventory,
		)
	}

	paths := objectAt(t, document, "paths")
	for path, rawPathItem := range paths {
		pathItem := rawPathItem.(map[string]any)
		for _, method := range []string{"get", "post", "put", "patch", "delete"} {
			rawOperation, exists := pathItem[method]
			if !exists {
				continue
			}
			operation := rawOperation.(map[string]any)
			operationID, _ := operation["operationId"].(string)
			hasArray := operationHasResponseArray(t, document, operation)
			unboundedArray := operationHasUnboundedResponseArray(
				t,
				document,
				operation,
			)
			if !strings.HasPrefix(operationID, "list") &&
				!strings.HasPrefix(operationID, "search") &&
				!hasArray {
				continue
			}
			strategy, classified := operation["x-list-strategy"].(string)
			if !classified {
				t.Errorf(
					"%s %s (%s) has a response list but no x-list-strategy",
					strings.ToUpper(method),
					path,
					operationID,
				)
				continue
			}
			if strategy != "page" &&
				strategy != "cursor" &&
				strategy != "bounded" {
				t.Errorf("%s has invalid x-list-strategy %q", operationID, strategy)
			}
			if strategy == "bounded" && unboundedArray {
				t.Errorf(
					"%s claims bounded but contains an array without maxItems <= 100",
					operationID,
				)
			}
			if strategy == "page" || strategy == "cursor" {
				rawStable, stable := operation["x-stable-sort"].([]any)
				if !stable || len(rawStable) < 2 {
					t.Errorf(
						"%s must publish a multi-column x-stable-sort",
						operationID,
					)
					continue
				}
				last, ok := rawStable[len(rawStable)-1].(string)
				if !ok || !strings.Contains(strings.ToLower(last), "id") {
					t.Errorf(
						"%s x-stable-sort must end in a unique id, got %v",
						operationID,
						rawStable,
					)
				}
			}
		}
	}
	workbench := objectAt(
		t,
		objectAt(
			t,
			paths,
			"/workbench/dashboard",
		),
		"get",
	)
	if !operationHasResponseArray(t, document, workbench) ||
		operationHasUnboundedResponseArray(t, document, workbench) {
		t.Fatal("getWorkbenchDashboard must exercise bounded non-list arrays")
	}
}

func TestRuntimeGETRegistrationsAreClassifiedAndHumanListsPublished(
	t *testing.T,
) {
	serverRoot := filepath.Clean(filepath.Join("..", ".."))
	registrations, err := routeinventory.ScanRuntimeGETRoutes(serverRoot)
	if err != nil {
		t.Fatalf("scan runtime GET registrations: %v", err)
	}
	declarations := routeinventory.HumanGETDeclarations()
	if err := routeinventory.ValidateCoverage(
		registrations,
		declarations,
	); err != nil {
		t.Fatalf("runtime GET classification drift:\n%v", err)
	}

	document := decodeDocument(t)
	paths := objectAt(t, document, "paths")
	classificationCounts := make(map[routeinventory.Classification]int)
	for _, registration := range registrations {
		declaration := declarations[registration.Fingerprint]
		classificationCounts[declaration.Classification]++
		if declaration.Classification ==
			routeinventory.ClassificationMachinePublic {
			continue
		}
		if declaration.OpenAPIPath == "" {
			if declaration.Classification !=
				routeinventory.ClassificationNonList {
				t.Errorf(
					"%s is a Human list without an OpenAPI binding",
					registration.Fingerprint,
				)
			}
			continue
		}

		rawPathItem, published := paths[declaration.OpenAPIPath]
		if !published {
			t.Errorf(
				"%s maps to missing OpenAPI path %s",
				registration.Fingerprint,
				declaration.OpenAPIPath,
			)
			continue
		}
		pathItem, ok := rawPathItem.(map[string]any)
		if !ok {
			t.Errorf(
				"OpenAPI path %s has invalid item %T",
				declaration.OpenAPIPath,
				rawPathItem,
			)
			continue
		}
		rawOperation, published := pathItem["get"]
		if !published {
			t.Errorf(
				"%s has no published GET operation",
				declaration.OpenAPIPath,
			)
			continue
		}
		operation, ok := rawOperation.(map[string]any)
		if !ok {
			t.Errorf(
				"GET %s has invalid operation %T",
				declaration.OpenAPIPath,
				rawOperation,
			)
			continue
		}
		if operation["operationId"] != declaration.OperationID {
			t.Errorf(
				"GET %s operationId = %v, want %s",
				declaration.OpenAPIPath,
				operation["operationId"],
				declaration.OperationID,
			)
		}

		switch declaration.Classification {
		case routeinventory.ClassificationPage:
			assertRuntimeListOperation(
				t,
				document,
				declaration,
				operation,
				"page_size",
			)
		case routeinventory.ClassificationCursor:
			assertRuntimeListOperation(
				t,
				document,
				declaration,
				operation,
				"limit",
			)
		case routeinventory.ClassificationBounded:
			if operation["x-list-strategy"] != "bounded" {
				t.Errorf(
					"GET %s x-list-strategy = %v, want bounded",
					declaration.OpenAPIPath,
					operation["x-list-strategy"],
				)
			}
			if operationHasUnboundedResponseArray(
				t,
				document,
				operation,
			) {
				t.Errorf(
					"GET %s claims bounded but publishes an unbounded array",
					declaration.OpenAPIPath,
				)
			}
		case routeinventory.ClassificationNonList:
			if strategy, classified :=
				operation["x-list-strategy"]; classified {
				t.Errorf(
					"GET %s is non-list but publishes x-list-strategy=%v",
					declaration.OpenAPIPath,
					strategy,
				)
			}
		default:
			t.Errorf(
				"%s has unexpected classification %q",
				registration.Fingerprint,
				declaration.Classification,
			)
		}
	}

	for _, classification := range []routeinventory.Classification{
		routeinventory.ClassificationPage,
		routeinventory.ClassificationCursor,
		routeinventory.ClassificationBounded,
		routeinventory.ClassificationNonList,
		routeinventory.ClassificationMachinePublic,
	} {
		if classificationCounts[classification] == 0 {
			t.Errorf("runtime inventory has no %q registration", classification)
		}
	}

	// These registrations were previously easy to omit because their path
	// names or delegated handler groups were not inferred from OpenAPI itself.
	assertRuntimeRegistrationClassification(
		t,
		registrations,
		declarations,
		"internal/app/app.go",
		"tickets",
		`"/my-tickets"`,
		routeinventory.ClassificationPage,
	)
	assertRuntimeRegistrationClassification(
		t,
		registrations,
		declarations,
		"internal/app/app.go",
		"tickets",
		`"/unassigned"`,
		routeinventory.ClassificationPage,
	)
	assertRuntimeRegistrationClassification(
		t,
		registrations,
		declarations,
		"internal/handlers/integration_handler.go",
		"integrations",
		`"/connections"`,
		routeinventory.ClassificationPage,
	)
	assertRuntimeRegistrationClassification(
		t,
		registrations,
		declarations,
		"internal/handlers/integration_handler.go",
		"integrations",
		`"/domain-events"`,
		routeinventory.ClassificationCursor,
	)
}

func TestAnalyticsByCategoryMapHasAnExplicitRuntimeBound(t *testing.T) {
	if services.AnalyticsMaxCategoryValues <= 0 ||
		services.AnalyticsMaxCategoryValues > 1_000 {
		t.Fatalf(
			"Analytics by_category bound = %d, want 1..1000",
			services.AnalyticsMaxCategoryValues,
		)
	}
	field, exists := reflect.TypeOf(
		services.AnalyticsTicketStats{},
	).FieldByName("ByCategory")
	if !exists {
		t.Fatal("AnalyticsTicketStats.ByCategory runtime collection is missing")
	}
	if got := field.Tag.Get("json"); got != "by_category" {
		t.Fatalf("ByCategory JSON name = %q, want by_category", got)
	}
	if field.Type.Kind() != reflect.Map {
		t.Fatalf("ByCategory kind = %s, want map", field.Type.Kind())
	}

	document := decodeDocument(t)
	schemas := objectAt(
		t,
		objectAt(t, document, "components"),
		"schemas",
	)
	publishedByCategory := false
	for name, rawSchema := range schemas {
		schema, ok := rawSchema.(map[string]any)
		if !ok {
			continue
		}
		properties, ok := schema["properties"].(map[string]any)
		if !ok {
			continue
		}
		rawByCategory, exists := properties["by_category"]
		if !exists {
			continue
		}
		publishedByCategory = true
		byCategory, ok := rawByCategory.(map[string]any)
		if !ok {
			t.Errorf("%s.by_category schema = %T", name, rawByCategory)
			continue
		}
		if byCategory["maxProperties"] !=
			float64(services.AnalyticsMaxCategoryValues) {
			t.Errorf(
				"%s.by_category maxProperties = %v, want %d",
				name,
				byCategory["maxProperties"],
				services.AnalyticsMaxCategoryValues,
			)
		}
	}
	paths := objectAt(t, document, "paths")
	if rawPath, published := paths["/platform/analytics/business"]; published {
		pathItem, ok := rawPath.(map[string]any)
		if !ok {
			t.Fatalf("analytics business path item = %T", rawPath)
		}
		operation := objectAt(t, pathItem, "get")
		if operation["x-list-strategy"] != "bounded" {
			t.Errorf(
				"analytics business x-list-strategy = %v, want bounded",
				operation["x-list-strategy"],
			)
		}
		if !publishedByCategory {
			t.Error(
				"analytics business was published without a bounded by_category schema",
			)
		}
	}
}

func assertRuntimeListOperation(
	t *testing.T,
	document map[string]any,
	declaration routeinventory.Declaration,
	operation map[string]any,
	sizeParameter string,
) {
	t.Helper()
	if operation["x-list-strategy"] !=
		string(declaration.Classification) {
		t.Errorf(
			"GET %s x-list-strategy = %v, want %s",
			declaration.OpenAPIPath,
			operation["x-list-strategy"],
			declaration.Classification,
		)
	}
	query := operationQueryParameters(t, document, operation)
	rawSize, published := query[sizeParameter]
	if !published {
		t.Errorf(
			"GET %s has no %s parameter",
			declaration.OpenAPIPath,
			sizeParameter,
		)
	} else {
		schema := objectAt(t, rawSize, "schema")
		if schema["minimum"] != float64(1) ||
			schema["maximum"] != float64(100) ||
			schema["default"] != float64(25) {
			t.Errorf(
				"GET %s %s schema = %v, want min=1 default=25 max=100",
				declaration.OpenAPIPath,
				sizeParameter,
				schema,
			)
		}
	}

	rawSort, ok := operation["x-stable-sort"].([]any)
	if !ok || len(rawSort) < 2 {
		t.Errorf(
			"GET %s x-stable-sort = %v, want at least two fields",
			declaration.OpenAPIPath,
			operation["x-stable-sort"],
		)
		return
	}
	last, ok := rawSort[len(rawSort)-1].(string)
	if !ok || !strings.Contains(strings.ToLower(last), "id") {
		t.Errorf(
			"GET %s stable sort must end in a unique id, got %v",
			declaration.OpenAPIPath,
			rawSort,
		)
	}
}

func assertRuntimeRegistrationClassification(
	t *testing.T,
	registrations []routeinventory.Registration,
	declarations map[string]routeinventory.Declaration,
	file string,
	receiver string,
	pathExpression string,
	want routeinventory.Classification,
) {
	t.Helper()
	for _, registration := range registrations {
		if registration.File != file ||
			registration.Receiver != receiver ||
			registration.PathExpression != pathExpression {
			continue
		}
		if got := declarations[registration.Fingerprint].Classification; got !=
			want {
			t.Errorf(
				"%s classification = %q, want %q",
				registration.Fingerprint,
				got,
				want,
			)
		}
		return
	}
	t.Errorf(
		"runtime registration %s %s.GET(%s) was not discovered",
		file,
		receiver,
		pathExpression,
	)
}

func TestRegisteredRuntimeListInventoryIsExplicitlyPublished(t *testing.T) {
	document := decodeDocument(t)
	paths := objectAt(t, document, "paths")
	for _, test := range []struct {
		path        string
		operationID string
		strategy    string
		stableSort  []string
	}{
		{
			path:        "/user/trusted-devices",
			operationID: "listTrustedDevices",
			strategy:    "page",
			stableSort: []string{
				"revoked ASC",
				"expires_at DESC",
				"id DESC",
			},
		},
		{
			path:        "/user/login-history",
			operationID: "listLoginHistory",
			strategy:    "page",
			stableSort:  []string{"login_time DESC", "id DESC"},
		},
		{
			path:        "/projects",
			operationID: "listAuthorizedHumanProjects",
			strategy:    "page",
			stableSort:  []string{"name ASC", "id ASC"},
		},
		{
			path:        "/projects/{projectKey}/queues",
			operationID: "listProjectQueues",
			strategy:    "page",
			stableSort: []string{
				"is_default DESC",
				"name ASC",
				"id ASC",
			},
		},
		{
			path:        "/projects/{projectKey}/memberships",
			operationID: "listProjectMemberships",
			strategy:    "page",
			stableSort: []string{
				"is_active DESC",
				"role ASC",
				"user_id ASC",
				"id ASC",
			},
		},
		{
			path:        "/projects/{projectKey}/membership-candidates",
			operationID: "searchProjectMembershipCandidates",
			strategy:    "page",
			stableSort: []string{
				"display_name ASC",
				"username ASC",
				"id ASC",
			},
		},
		{
			path:        "/platform/projects",
			operationID: "listPlatformProjects",
			strategy:    "page",
			stableSort:  []string{"name ASC", "id ASC"},
		},
		{
			path:        "/platform/users",
			operationID: "listPlatformUsers",
			strategy:    "page",
			stableSort:  []string{"created_at DESC", "id DESC"},
		},
		{
			path:        "/platform/audit-logs",
			operationID: "listPlatformAuditLogs",
			strategy:    "cursor",
			stableSort:  []string{"created_at DESC", "id DESC"},
		},
		{
			path:        "/workbench/tickets",
			operationID: "listCrossProjectWorkbenchTickets",
			strategy:    "page",
			stableSort:  []string{"updated_at DESC", "id DESC"},
		},
		{
			path:        "/projects/{projectKey}/tickets",
			operationID: "listProjectTickets",
			strategy:    "page",
			stableSort:  []string{"created_at DESC", "id DESC"},
		},
		{
			path:        "/projects/{projectKey}/tickets/my-tickets",
			operationID: "listMyProjectTickets",
			strategy:    "page",
			stableSort:  []string{"created_at DESC", "id DESC"},
		},
		{
			path:        "/projects/{projectKey}/tickets/unassigned",
			operationID: "listUnassignedProjectTickets",
			strategy:    "page",
			stableSort:  []string{"created_at DESC", "id DESC"},
		},
		{
			path:        "/projects/{projectKey}/notifications",
			operationID: "listProjectNotifications",
			strategy:    "page",
			stableSort:  []string{"created_at DESC", "id DESC"},
		},
		{
			path:        "/projects/{projectKey}/categories",
			operationID: "listProjectCategories",
			strategy:    "page",
			stableSort: []string{
				"sort_order ASC",
				"name ASC",
				"id ASC",
			},
		},
		{
			path:        "/projects/{projectKey}/assignees",
			operationID: "listProjectAssignees",
			strategy:    "page",
			stableSort:  []string{"username ASC", "id ASC"},
		},
		{
			path:        "/projects/{projectKey}/tickets/{ticketID}/entity-links",
			operationID: "listProjectTicketEntityLinks",
			strategy:    "page",
			stableSort:  []string{"created_at DESC", "id DESC"},
		},
		{
			path:        "/projects/{projectKey}/tickets/{ticketID}/relations",
			operationID: "listProjectTicketRelations",
			strategy:    "page",
			stableSort:  []string{"created_at DESC", "id DESC"},
		},
		{
			path:        "/projects/{projectKey}/agent-collaboration/runs",
			operationID: "listProjectAgentRuns",
			strategy:    "page",
			stableSort:  []string{"created_at DESC", "id DESC"},
		},
		{
			path:        "/projects/{projectKey}/agent-collaboration/proposals",
			operationID: "listProjectActionProposals",
			strategy:    "page",
			stableSort:  []string{"created_at DESC", "id DESC"},
		},
		{
			path:        "/projects/{projectKey}/agent-collaboration/approvals",
			operationID: "listProjectApprovalTasks",
			strategy:    "page",
			stableSort:  []string{"created_at DESC", "id DESC"},
		},
		{
			path:        "/projects/{projectKey}/agent-collaboration/handoffs",
			operationID: "listProjectHandoffs",
			strategy:    "page",
			stableSort:  []string{"created_at DESC", "id DESC"},
		},
		{
			path:        "/projects/{projectKey}/admin/automation/rules",
			operationID: "listProjectAutomationRules",
			strategy:    "page",
			stableSort: []string{
				"priority ASC",
				"created_at DESC",
				"id DESC",
			},
		},
		{
			path:        "/projects/{projectKey}/admin/automation/sla",
			operationID: "listProjectSLAConfigs",
			strategy:    "page",
			stableSort: []string{
				"is_default DESC",
				"created_at DESC",
				"id DESC",
			},
		},
		{
			path:        "/projects/{projectKey}/admin/automation/templates",
			operationID: "listProjectTicketTemplates",
			strategy:    "page",
			stableSort:  []string{"created_at DESC", "id DESC"},
		},
		{
			path:        "/projects/{projectKey}/admin/automation/quick-replies",
			operationID: "listProjectQuickReplies",
			strategy:    "page",
			stableSort:  []string{"created_at DESC", "id DESC"},
		},
		{
			path:        "/projects/{projectKey}/knowledge/articles",
			operationID: "listProjectKnowledgeArticles",
			strategy:    "page",
			stableSort:  []string{"updated_at DESC", "id DESC"},
		},
		{
			path:        "/projects/{projectKey}/knowledge/articles/{articleID}/versions",
			operationID: "listProjectKnowledgeVersions",
			strategy:    "page",
			stableSort:  []string{"version DESC", "id DESC"},
		},
		{
			path:        "/projects/{projectKey}/knowledge/ingestions",
			operationID: "listProjectKnowledgeIngestions",
			strategy:    "page",
			stableSort:  []string{"created_at DESC", "id DESC"},
		},
		{
			path:        "/projects/{projectKey}/integrations/connector-definitions",
			operationID: "listProjectIntegrationConnectorDefinitions",
			strategy:    "page",
			stableSort:  []string{"created_at DESC", "id DESC"},
		},
		{
			path:        "/projects/{projectKey}/integrations/connections",
			operationID: "listProjectIntegrationConnections",
			strategy:    "page",
			stableSort:  []string{"created_at DESC", "id DESC"},
		},
		{
			path:        "/projects/{projectKey}/integrations/connections/{connectionID}/mappings",
			operationID: "listProjectIntegrationMappings",
			strategy:    "page",
			stableSort:  []string{"created_at DESC", "id DESC"},
		},
		{
			path:        "/projects/{projectKey}/integrations/inbox",
			operationID: "listProjectIntegrationInboxMessages",
			strategy:    "page",
			stableSort:  []string{"received_at DESC", "id DESC"},
		},
		{
			path:        "/projects/{projectKey}/integrations/inbox/{messageID}/receipts",
			operationID: "listProjectIntegrationInboxReceipts",
			strategy:    "page",
			stableSort:  []string{"created_at DESC", "id DESC"},
		},
		{
			path:        "/projects/{projectKey}/integrations/sync-runs",
			operationID: "listProjectIntegrationSyncRuns",
			strategy:    "page",
			stableSort:  []string{"created_at DESC", "id DESC"},
		},
		{
			path:        "/projects/{projectKey}/integrations/conflicts",
			operationID: "listProjectIntegrationConflicts",
			strategy:    "page",
			stableSort:  []string{"created_at DESC", "id DESC"},
		},
		{
			path:        "/projects/{projectKey}/integrations/dead-letters",
			operationID: "listProjectIntegrationDeadLetters",
			strategy:    "page",
			stableSort:  []string{"created_at DESC", "id DESC"},
		},
		{
			path:        "/projects/{projectKey}/integrations/domain-events",
			operationID: "listProjectIntegrationDomainEvents",
			strategy:    "cursor",
			stableSort:  []string{"created_at DESC", "id DESC"},
		},
		{
			path:        "/projects/{projectKey}/integrations/outbox",
			operationID: "listProjectIntegrationOutboxDeliveries",
			strategy:    "page",
			stableSort:  []string{"created_at DESC", "id DESC"},
		},
		{
			path:        "/projects/{projectKey}/admin/automation/logs",
			operationID: "listProjectAutomationLogs",
			strategy:    "cursor",
			stableSort:  []string{"executed_at DESC", "id DESC"},
		},
		{
			path:        "/platform/system/cleanup/logs",
			operationID: "listPlatformCleanupLogs",
			strategy:    "page",
			stableSort:  []string{"created_at DESC", "id DESC"},
		},
		{
			path:        "/platform/configs",
			operationID: "listPlatformConfigs",
			strategy:    "page",
			stableSort: []string{
				"category ASC",
				"group ASC",
				"key ASC",
				"id ASC",
			},
		},
		{
			path:        "/projects/{projectKey}/webhooks",
			operationID: "listProjectWebhooks",
			strategy:    "page",
			stableSort:  []string{"created_at DESC", "id DESC"},
		},
		{
			path:        "/projects/{projectKey}/webhooks/{webhookID}/logs",
			operationID: "listProjectWebhookLogs",
			strategy:    "cursor",
			stableSort:  []string{"created_at DESC", "id DESC"},
		},
	} {
		t.Run(test.operationID, func(t *testing.T) {
			operation := objectAt(
				t,
				objectAt(t, paths, test.path),
				"get",
			)
			if operation["operationId"] != test.operationID {
				t.Fatalf(
					"operationId = %v, want %q",
					operation["operationId"],
					test.operationID,
				)
			}
			if operation["x-list-strategy"] != test.strategy {
				t.Fatalf(
					"x-list-strategy = %v, want %q",
					operation["x-list-strategy"],
					test.strategy,
				)
			}
			assertExactStringArray(
				t,
				operation["x-stable-sort"],
				test.stableSort,
			)
		})
	}
}

func TestExperiencePhaseHumanContractsAreClosedBoundedAndRedacted(
	t *testing.T,
) {
	document := decodeDocument(t)
	paths := objectAt(t, document, "paths")
	schemas := objectAt(
		t,
		objectAt(t, document, "components"),
		"schemas",
	)

	for _, test := range []struct {
		path        string
		operationID string
	}{
		{"/user/login-history", "listLoginHistory"},
		{"/projects/{projectKey}/categories", "listProjectCategories"},
		{"/projects/{projectKey}/assignees", "listProjectAssignees"},
		{
			"/projects/{projectKey}/tickets/{ticketID}/entity-links",
			"listProjectTicketEntityLinks",
		},
		{
			"/projects/{projectKey}/tickets/{ticketID}/relations",
			"listProjectTicketRelations",
		},
		{
			"/projects/{projectKey}/agent-collaboration/runs",
			"listProjectAgentRuns",
		},
		{
			"/projects/{projectKey}/agent-collaboration/proposals",
			"listProjectActionProposals",
		},
		{
			"/projects/{projectKey}/agent-collaboration/approvals",
			"listProjectApprovalTasks",
		},
		{
			"/projects/{projectKey}/agent-collaboration/handoffs",
			"listProjectHandoffs",
		},
		{
			"/projects/{projectKey}/admin/automation/sla",
			"listProjectSLAConfigs",
		},
		{
			"/projects/{projectKey}/admin/automation/templates",
			"listProjectTicketTemplates",
		},
		{
			"/projects/{projectKey}/admin/automation/quick-replies",
			"listProjectQuickReplies",
		},
		{
			"/projects/{projectKey}/knowledge/articles",
			"listProjectKnowledgeArticles",
		},
		{
			"/projects/{projectKey}/knowledge/articles/{articleID}/versions",
			"listProjectKnowledgeVersions",
		},
		{
			"/projects/{projectKey}/knowledge/ingestions",
			"listProjectKnowledgeIngestions",
		},
	} {
		operation := objectAt(t, objectAt(t, paths, test.path), "get")
		if operation["operationId"] != test.operationID {
			t.Errorf(
				"%s operationId = %v, want %s",
				test.path,
				operation["operationId"],
				test.operationID,
			)
		}
		if operation["x-list-strategy"] != "page" {
			t.Errorf("%s is not a page list", test.path)
		}
		query := operationQueryParameters(t, document, operation)
		pageSize := objectAt(t, query["page_size"], "schema")
		if pageSize["default"] != float64(25) ||
			pageSize["maximum"] != float64(100) {
			t.Errorf("%s page_size = %v", test.path, pageSize)
		}
	}

	for _, test := range []struct {
		schema    string
		forbidden []string
	}{
		{
			schema: "LoginHistoryRecord",
			forbidden: []string{
				"user_id",
				"username",
				"email",
				"session_id",
			},
		},
		{
			schema: "ProjectCategory",
			forbidden: []string{
				"organization_id",
				"project_id",
				"created_by",
				"children",
			},
		},
		{
			schema: "ProjectAssignee",
			forbidden: []string{
				"email",
				"password",
				"password_hash",
				"platform_role",
			},
		},
		{
			schema:    "SLAConfig",
			forbidden: []string{"organization_id", "project_id"},
		},
		{
			schema: "TicketTemplate",
			forbidden: []string{
				"organization_id",
				"project_id",
				"created_user",
				"assign_to_user",
			},
		},
		{
			schema: "QuickReply",
			forbidden: []string{
				"organization_id",
				"project_id",
				"created_user",
			},
		},
		{
			schema: "IntakeRequestTypeVersion",
			forbidden: []string{
				"organization_id",
				"project_id",
				"created_by_type",
				"created_by_id",
				"content_hash",
			},
		},
		{
			schema: "IntakeWorkflowVersion",
			forbidden: []string{
				"organization_id",
				"project_id",
				"created_by_type",
				"created_by_id",
				"content_hash",
			},
		},
		{
			schema:    "AgentRunDetail",
			forbidden: []string{"policy_snapshot", "principal_id", "agent_task_id"},
		},
		{
			schema: "ActionProposalDetail",
			forbidden: []string{
				"action_payload",
				"proposal_digest",
				"evidence_digest",
			},
		},
	} {
		schema := objectAt(t, schemas, test.schema)
		if schema["additionalProperties"] != false {
			t.Errorf("%s is not closed", test.schema)
		}
		properties := objectAt(t, schema, "properties")
		for _, field := range test.forbidden {
			if _, exposed := properties[field]; exposed {
				t.Errorf("%s exposes forbidden field %s", test.schema, field)
			}
		}
	}

	intake := objectAt(
		t,
		objectAt(t, paths, "/projects/{projectKey}/configuration/intake"),
		"get",
	)
	if intake["x-list-strategy"] != "bounded" {
		t.Errorf("configuration intake strategy = %v", intake["x-list-strategy"])
	}
	intakeSchema := objectAt(t, schemas, "ProjectIntakeConfiguration")
	for _, field := range []string{"request_types", "workflows"} {
		property := objectAt(
			t,
			objectAt(t, intakeSchema, "properties"),
			field,
		)
		if property["maxItems"] != float64(100) {
			t.Errorf("%s maxItems = %v", field, property["maxItems"])
		}
	}
}

func TestIntegrationHumanContractsMirrorStrictScopedRuntime(
	t *testing.T,
) {
	document := decodeDocument(t)
	paths := objectAt(t, document, "paths")
	schemas := objectAt(
		t,
		objectAt(t, document, "components"),
		"schemas",
	)

	for _, test := range []struct {
		path         string
		operationID  string
		responseName string
	}{
		{
			"/projects/{projectKey}/integrations/connector-definitions",
			"listProjectIntegrationConnectorDefinitions",
			"IntegrationConnectorDefinitionPageEnvelope",
		},
		{
			"/projects/{projectKey}/integrations/connections",
			"listProjectIntegrationConnections",
			"IntegrationConnectionPageEnvelope",
		},
		{
			"/projects/{projectKey}/integrations/connections/{connectionID}/mappings",
			"listProjectIntegrationMappings",
			"IntegrationMappingPageEnvelope",
		},
		{
			"/projects/{projectKey}/integrations/inbox",
			"listProjectIntegrationInboxMessages",
			"IntegrationInboxMessagePageEnvelope",
		},
		{
			"/projects/{projectKey}/integrations/inbox/{messageID}/receipts",
			"listProjectIntegrationInboxReceipts",
			"IntegrationInboxReceiptPageEnvelope",
		},
		{
			"/projects/{projectKey}/integrations/sync-runs",
			"listProjectIntegrationSyncRuns",
			"IntegrationSyncRunPageEnvelope",
		},
		{
			"/projects/{projectKey}/integrations/conflicts",
			"listProjectIntegrationConflicts",
			"IntegrationConflictPageEnvelope",
		},
		{
			"/projects/{projectKey}/integrations/dead-letters",
			"listProjectIntegrationDeadLetters",
			"IntegrationDeadLetterPageEnvelope",
		},
		{
			"/projects/{projectKey}/integrations/outbox",
			"listProjectIntegrationOutboxDeliveries",
			"IntegrationOutboxPageEnvelope",
		},
	} {
		t.Run(test.operationID, func(t *testing.T) {
			operation := objectAt(
				t,
				objectAt(t, paths, test.path),
				"get",
			)
			if operation["operationId"] != test.operationID {
				t.Fatalf("operationId = %v, want %s", operation["operationId"], test.operationID)
			}
			if operation["x-list-strategy"] != "page" {
				t.Fatalf("x-list-strategy = %v", operation["x-list-strategy"])
			}
			assertExactRoleAllowlist(
				t,
				operation["x-chronodesk-project-roles"],
				[]string{"project_admin", "manager", "observer"},
			)
			query := operationQueryParameters(t, document, operation)
			page := objectAt(t, query["page"], "schema")
			pageSize := objectAt(t, query["page_size"], "schema")
			if page["minimum"] != float64(1) ||
				page["default"] != float64(1) {
				t.Errorf("page = %v", page)
			}
			if pageSize["minimum"] != float64(1) ||
				pageSize["maximum"] != float64(100) ||
				pageSize["default"] != float64(25) {
				t.Errorf("page_size = %v", pageSize)
			}
			if got := responseSchemaRef(t, operation, "200"); got !=
				"#/components/schemas/"+test.responseName {
				t.Errorf("response schema = %q", got)
			}
		})
	}

	events := objectAt(
		t,
		objectAt(
			t,
			paths,
			"/projects/{projectKey}/integrations/domain-events",
		),
		"get",
	)
	if events["x-list-strategy"] != "cursor" {
		t.Fatalf("domain event strategy = %v", events["x-list-strategy"])
	}
	eventQuery := operationQueryParameters(t, document, events)
	limit := objectAt(t, eventQuery["limit"], "schema")
	if limit["minimum"] != float64(1) ||
		limit["maximum"] != float64(100) ||
		limit["default"] != float64(25) {
		t.Errorf("domain event limit = %v", limit)
	}
	assertExactRoleAllowlist(
		t,
		events["x-chronodesk-project-roles"],
		[]string{"project_admin", "manager", "observer"},
	)

	overview := objectAt(
		t,
		objectAt(
			t,
			paths,
			"/projects/{projectKey}/integrations/overview",
		),
		"get",
	)
	if overview["x-list-strategy"] != "bounded" {
		t.Errorf("integration overview strategy = %v", overview["x-list-strategy"])
	}
	overviewSchema := objectAt(t, schemas, "IntegrationOverview")
	for _, field := range []string{"recent_runs", "connection_health"} {
		property := objectAt(
			t,
			objectAt(t, overviewSchema, "properties"),
			field,
		)
		if property["maxItems"] != float64(100) {
			t.Errorf("IntegrationOverview.%s maxItems = %v", field, property["maxItems"])
		}
	}
	dryRun := objectAt(
		t,
		objectAt(
			t,
			paths,
			"/projects/{projectKey}/integrations/mappings/{mappingID}/dry-runs",
		),
		"post",
	)
	if dryRun["x-list-strategy"] != "bounded" {
		t.Errorf("mapping dry-run strategy = %v", dryRun["x-list-strategy"])
	}
	warnings := objectAt(
		t,
		objectAt(
			t,
			objectAt(t, schemas, "IntegrationMappingDryRunResult"),
			"properties",
		),
		"warnings",
	)
	if warnings["maxItems"] != float64(100) {
		t.Errorf("mapping dry-run warnings maxItems = %v", warnings["maxItems"])
	}

	for _, test := range []struct {
		path       string
		method     string
		operation  string
		requestDTO string
	}{
		{
			"/projects/{projectKey}/integrations/connector-definitions",
			"post",
			"createProjectIntegrationConnectorDefinition",
			"CreateIntegrationConnectorDefinitionRequest",
		},
		{
			"/projects/{projectKey}/integrations/connector-definitions/{definitionID}",
			"put",
			"updateProjectIntegrationConnectorDefinition",
			"UpdateIntegrationConnectorDefinitionRequest",
		},
		{
			"/projects/{projectKey}/integrations/connections",
			"post",
			"createProjectIntegrationConnection",
			"CreateIntegrationConnectionRequest",
		},
		{
			"/projects/{projectKey}/integrations/connections/{connectionID}",
			"put",
			"updateProjectIntegrationConnection",
			"UpdateIntegrationConnectionRequest",
		},
		{
			"/projects/{projectKey}/integrations/connections/{connectionID}/mappings",
			"post",
			"createProjectIntegrationMapping",
			"CreateIntegrationMappingRequest",
		},
		{
			"/projects/{projectKey}/integrations/mappings/{mappingID}",
			"put",
			"updateProjectIntegrationMapping",
			"UpdateIntegrationMappingRequest",
		},
		{
			"/projects/{projectKey}/integrations/mappings/{mappingID}/dry-runs",
			"post",
			"dryRunProjectIntegrationMapping",
			"DryRunIntegrationMappingRequest",
		},
		{
			"/projects/{projectKey}/integrations/mappings/{mappingID}/publication",
			"post",
			"publishProjectIntegrationMapping",
			"PublishIntegrationMappingRequest",
		},
		{
			"/projects/{projectKey}/integrations/conflicts/{conflictID}/resolution",
			"post",
			"resolveProjectIntegrationConflict",
			"ResolveIntegrationConflictRequest",
		},
		{
			"/projects/{projectKey}/integrations/dead-letters/{deadLetterID}/replays",
			"post",
			"replayProjectIntegrationDeadLetter",
			"ReplayIntegrationDeadLetterRequest",
		},
	} {
		t.Run(test.operation, func(t *testing.T) {
			operation := objectAt(
				t,
				objectAt(t, paths, test.path),
				test.method,
			)
			if operation["operationId"] != test.operation {
				t.Fatalf("operationId = %v", operation["operationId"])
			}
			assertExactRoleAllowlist(
				t,
				operation["x-chronodesk-project-roles"],
				[]string{"project_admin", "manager"},
			)
			if got := requestSchemaRef(t, operation); got !=
				"#/components/schemas/"+test.requestDTO {
				t.Errorf("request schema = %q", got)
			}
			if objectAt(t, schemas, test.requestDTO)["additionalProperties"] != false {
				t.Errorf("%s is not closed", test.requestDTO)
			}
		})
	}

	for schemaName, forbidden := range map[string][]string{
		"IntegrationConnectorDefinitionSummary": {
			"configuration_schema",
			"mapping_schema",
			"organization_id",
			"project_id",
		},
		"IntegrationConnectionSummary": {
			"configuration",
			"verification_key_ref",
			"organization_id",
			"project_id",
		},
		"IntegrationMappingSummary": {
			"source_schema",
			"definition",
			"organization_id",
			"project_id",
		},
		"IntegrationInboxMessageSummary": {
			"payload",
			"signature",
			"organization_id",
			"project_id",
		},
		"IntegrationOutboxSummary": {
			"destination_id",
			"locked_by",
			"locked_at",
			"organization_id",
			"project_id",
		},
	} {
		schema := objectAt(t, schemas, schemaName)
		if schema["additionalProperties"] != false {
			t.Errorf("%s is not closed", schemaName)
		}
		properties := objectAt(t, schema, "properties")
		for _, field := range forbidden {
			if _, exposed := properties[field]; exposed {
				t.Errorf("%s exposes forbidden field %s", schemaName, field)
			}
		}
	}
}

func TestNewRuntimeDirectoryContractsMatchAdapters(t *testing.T) {
	document := decodeDocument(t)
	paths := objectAt(t, document, "paths")
	schemas := objectAt(
		t,
		objectAt(t, document, "components"),
		"schemas",
	)
	for _, test := range []struct {
		path          string
		queryNames    []string
		sortByDefault string
		sortDefault   string
		sortFields    []string
		responseRef   string
	}{
		{
			path: "/user/trusted-devices",
			queryNames: []string{
				"page",
				"page_size",
				"sort_by",
				"sort_order",
			},
			sortByDefault: "revoked",
			sortDefault:   "asc",
			sortFields: []string{
				"created_at",
				"updated_at",
				"last_used_at",
				"expires_at",
				"revoked",
				"device_name",
			},
			responseRef: "#/components/schemas/TrustedDevicePageEnvelope",
		},
		{
			path: "/projects/{projectKey}/queues",
			queryNames: []string{
				"page",
				"page_size",
				"sort_by",
				"sort_order",
			},
			sortByDefault: "is_default",
			sortDefault:   "desc",
			sortFields: []string{
				"created_at",
				"updated_at",
				"name",
				"key",
				"is_default",
			},
			responseRef: "#/components/schemas/ProjectQueuePageEnvelope",
		},
		{
			path: "/platform/system/cleanup/logs",
			queryNames: []string{
				"page",
				"page_size",
				"sort_by",
				"sort_order",
				"task_type",
			},
			sortByDefault: "created_at",
			sortDefault:   "desc",
			sortFields: []string{
				"created_at",
				"start_time",
				"end_time",
				"status",
				"task_type",
				"records_deleted",
			},
			responseRef: "#/components/schemas/CleanupLogPageEnvelope",
		},
	} {
		t.Run(test.path, func(t *testing.T) {
			operation := objectAt(
				t,
				objectAt(t, paths, test.path),
				"get",
			)
			parameters := operationQueryParameters(t, document, operation)
			names := make([]string, 0, len(parameters))
			for name := range parameters {
				names = append(names, name)
			}
			sort.Strings(names)
			sort.Strings(test.queryNames)
			if !reflect.DeepEqual(names, test.queryNames) {
				t.Fatalf("query parameters = %v, want %v", names, test.queryNames)
			}
			page := objectAt(t, parameters["page"], "schema")
			if page["minimum"] != float64(1) ||
				page["default"] != float64(1) {
				t.Errorf("page schema = %v", page)
			}
			pageSize := objectAt(t, parameters["page_size"], "schema")
			if pageSize["minimum"] != float64(1) ||
				pageSize["maximum"] != float64(100) ||
				pageSize["default"] != float64(25) {
				t.Errorf("page_size schema = %v", pageSize)
			}
			sortBy := objectAt(t, parameters["sort_by"], "schema")
			if sortBy["default"] != test.sortByDefault {
				t.Errorf("sort_by default = %v", sortBy["default"])
			}
			assertExactStringArray(t, sortBy["enum"], test.sortFields)
			sortOrder := objectAt(t, parameters["sort_order"], "schema")
			if sortOrder["default"] != test.sortDefault {
				t.Errorf("sort_order default = %v", sortOrder["default"])
			}
			assertExactStringArray(
				t,
				sortOrder["enum"],
				[]string{"asc", "desc"},
			)
			if got := responseSchemaRef(t, operation, "200"); got !=
				test.responseRef {
				t.Fatalf("response schema = %q, want %q", got, test.responseRef)
			}
			for _, status := range []string{"400", "401", "500"} {
				if _, ok := objectAt(t, operation, "responses")[status]; !ok {
					t.Errorf("response %s is missing", status)
				}
			}
		})
	}

	assertExactObjectKeys(
		t,
		objectAt(t, objectAt(t, schemas, "TrustedDevice"), "properties"),
		[]string{
			"id",
			"device_name",
			"last_used_at",
			"last_ip",
			"user_agent",
			"expires_at",
			"revoked",
			"created_at",
			"updated_at",
		},
	)
	projectQueueProperties := objectAt(
		t,
		objectAt(t, schemas, "ProjectQueue"),
		"properties",
	)
	assertExactObjectKeys(
		t,
		projectQueueProperties,
		[]string{
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
		},
	)
	for _, forbidden := range []string{
		"id",
		"project_id",
		"project",
		"team_id",
		"team",
	} {
		if _, exposed := projectQueueProperties[forbidden]; exposed {
			t.Errorf("ProjectQueue exposes internal relation field %q", forbidden)
		}
	}
	assertExactObjectKeys(
		t,
		objectAt(t, objectAt(t, schemas, "CleanupLog"), "properties"),
		[]string{
			"id",
			"created_at",
			"task_type",
			"status",
			"start_time",
			"end_time",
			"duration",
			"records_processed",
			"records_deleted",
			"error_message",
			"retention_days",
			"cutoff_date",
			"trigger_type",
			"trigger_by",
		},
	)
}

func TestHumanOpenAPIRequiredFieldsExistInSameObjectProperties(
	t *testing.T,
) {
	document := decodeDocument(t)
	assertRequiredFieldsExistInSameObjectProperties(t, "$", document)
}

func TestSuccessEnvelopeCompositionIsSatisfiableAndClosed(t *testing.T) {
	document := decodeDocument(t)
	schemas := objectAt(
		t,
		objectAt(t, document, "components"),
		"schemas",
	)
	for name, rawSchema := range schemas {
		schema, ok := rawSchema.(map[string]any)
		if !ok {
			continue
		}
		allOf, ok := schema["allOf"].([]any)
		if !ok {
			continue
		}
		extendsSuccessEnvelope := false
		for _, rawBranch := range allOf {
			branch, branchOK := rawBranch.(map[string]any)
			if !branchOK {
				continue
			}
			if branch["$ref"] == "#/components/schemas/SuccessEnvelope" {
				extendsSuccessEnvelope = true
			}
		}
		if !extendsSuccessEnvelope {
			continue
		}
		if schema["unevaluatedProperties"] != false {
			t.Errorf(
				"%s must close the composed success envelope with unevaluatedProperties=false",
				name,
			)
		}
		for _, rawBranch := range allOf {
			branch, branchOK := rawBranch.(map[string]any)
			if !branchOK {
				continue
			}
			if branch["additionalProperties"] == false {
				t.Errorf(
					"%s has an unsatisfiable allOf branch that rejects fields from SuccessEnvelope",
					name,
				)
			}
		}
	}
}

func TestKnowledgeWorkbenchRoutesRolesAndSchemasAreDurable(
	t *testing.T,
) {
	document := decodeDocument(t)
	paths := objectAt(t, document, "paths")
	schemas := objectAt(
		t,
		objectAt(t, document, "components"),
		"schemas",
	)
	allProjectRoles := []string{
		"project_admin",
		"manager",
		"agent",
		"requester",
		"observer",
	}
	managerRoles := []string{"project_admin", "manager"}

	for _, test := range []struct {
		path        string
		method      string
		operationID string
		roles       []string
		strategy    string
		status      string
		response    string
		request     string
		visibility  string
	}{
		{
			path:        "/projects/{projectKey}/knowledge/articles",
			method:      "get",
			operationID: "listProjectKnowledgeArticles",
			roles:       allProjectRoles,
			strategy:    "page",
			status:      "200",
			response:    "#/components/schemas/KnowledgeArticlePageEnvelope",
			visibility:  "published-live-acl-or-explicit-management-view",
		},
		{
			path:        "/projects/{projectKey}/knowledge/articles",
			method:      "post",
			operationID: "createProjectKnowledgeArticle",
			roles:       allProjectRoles,
			strategy:    "bounded",
			status:      "201",
			response:    "#/components/schemas/KnowledgeAuthoredEnvelope",
			request:     "#/components/schemas/CreateKnowledgeArticleRequest",
		},
		{
			path:        "/projects/{projectKey}/knowledge/articles/{articleID}/drafts",
			method:      "post",
			operationID: "createProjectKnowledgeArticleDraft",
			roles:       allProjectRoles,
			strategy:    "bounded",
			status:      "201",
			response:    "#/components/schemas/KnowledgeAuthoredEnvelope",
			request:     "#/components/schemas/CreateKnowledgeDraftRequest",
		},
		{
			path:        "/projects/{projectKey}/knowledge/articles/{articleID}/document",
			method:      "get",
			operationID: "getProjectKnowledgeArticleDocument",
			roles:       allProjectRoles,
			strategy:    "bounded",
			status:      "200",
			response:    "#/components/schemas/KnowledgeDocumentEnvelope",
			visibility:  "manager-or-published-read-acl-or-draft-manage-acl",
		},
		{
			path:        "/projects/{projectKey}/knowledge/versions/{versionID}/publication",
			method:      "post",
			operationID: "publishProjectKnowledgeVersion",
			roles:       managerRoles,
			status:      "200",
			response:    "#/components/schemas/KnowledgeVersionEnvelope",
		},
		{
			path:        "/projects/{projectKey}/knowledge/searches",
			method:      "post",
			operationID: "searchProjectKnowledge",
			roles:       allProjectRoles,
			strategy:    "bounded",
			status:      "200",
			response:    "#/components/schemas/KnowledgeSearchEnvelope",
			request:     "#/components/schemas/KnowledgeSearchRequest",
			visibility:  "published-live-acl",
		},
	} {
		t.Run(test.operationID, func(t *testing.T) {
			operation := objectAt(
				t,
				objectAt(t, paths, test.path),
				test.method,
			)
			if operation["operationId"] != test.operationID {
				t.Errorf(
					"operationId = %v, want %s",
					operation["operationId"],
					test.operationID,
				)
			}
			assertExactRoleAllowlist(
				t,
				operation["x-chronodesk-project-roles"],
				test.roles,
			)
			if test.strategy != "" &&
				operation["x-list-strategy"] != test.strategy {
				t.Errorf(
					"x-list-strategy = %v, want %s",
					operation["x-list-strategy"],
					test.strategy,
				)
			}
			if got := responseSchemaRef(
				t,
				operation,
				test.status,
			); got != test.response {
				t.Errorf("response = %q, want %q", got, test.response)
			}
			if test.request != "" {
				if got := requestSchemaRef(t, operation); got != test.request {
					t.Errorf("request = %q, want %q", got, test.request)
				}
			}
			if test.visibility != "" &&
				operation["x-chronodesk-knowledge-visibility"] !=
					test.visibility {
				t.Errorf(
					"knowledge visibility = %v, want %s",
					operation["x-chronodesk-knowledge-visibility"],
					test.visibility,
				)
			}
		})
	}

	articleList := objectAt(
		t,
		objectAt(
			t,
			paths,
			"/projects/{projectKey}/knowledge/articles",
		),
		"get",
	)
	assertExactStringArray(
		t,
		articleList["x-stable-sort"],
		[]string{"updated_at DESC", "id DESC"},
	)
	query := operationQueryParameters(t, document, articleList)
	if objectAt(t, query["page_size"], "schema")["maximum"] != float64(100) {
		t.Error("knowledge article page_size must be capped at 100")
	}
	documentOperation := objectAt(
		t,
		objectAt(
			t,
			paths,
			"/projects/{projectKey}/knowledge/articles/{articleID}/document",
		),
		"get",
	)
	documentQuery := operationQueryParameters(t, document, documentOperation)
	preferLatestDraft := objectAt(
		t,
		documentQuery["prefer_latest_draft"],
		"schema",
	)
	if preferLatestDraft["type"] != "boolean" ||
		preferLatestDraft["default"] != false {
		t.Errorf(
			"prefer_latest_draft schema = %v, want optional false boolean",
			preferLatestDraft,
		)
	}

	for _, test := range []struct {
		schema     string
		required   []string
		properties []string
	}{
		{
			schema:   "CreateKnowledgeArticleRequest",
			required: []string{"key", "title", "markdown"},
			properties: []string{
				"key",
				"title",
				"summary",
				"markdown",
				"source_ticket_id",
				"source_attachment_ids",
			},
		},
		{
			schema:   "CreateKnowledgeDraftRequest",
			required: []string{"title", "markdown"},
			properties: []string{
				"title",
				"markdown",
				"source_ticket_id",
				"source_attachment_ids",
			},
		},
		{
			schema:     "KnowledgeSearchRequest",
			required:   []string{"query"},
			properties: []string{"query", "limit"},
		},
	} {
		schema := objectAt(t, schemas, test.schema)
		if schema["additionalProperties"] != false {
			t.Errorf("%s must reject unknown JSON fields", test.schema)
		}
		assertExactStringArray(t, schema["required"], test.required)
		assertExactObjectKeys(
			t,
			objectAt(t, schema, "properties"),
			test.properties,
		)
	}

	for _, requestName := range []string{
		"CreateKnowledgeArticleRequest",
		"CreateKnowledgeDraftRequest",
	} {
		request := objectAt(t, schemas, requestName)
		properties := objectAt(t, request, "properties")
		markdown := objectAt(t, properties, "markdown")
		if markdown["maxLength"] != float64(128<<10) ||
			markdown["x-max-utf8-bytes"] != float64(128<<10) {
			t.Errorf("%s Markdown is not capped at 128 KiB", requestName)
		}
		attachments := objectAt(t, properties, "source_attachment_ids")
		if attachments["maxItems"] != float64(20) ||
			attachments["uniqueItems"] != true {
			t.Errorf("%s source attachments are not bounded and unique", requestName)
		}
		if request["x-chronodesk-source-constraint"] == nil {
			t.Errorf("%s does not publish its ticket/source constraint", requestName)
		}
	}

	source := objectAt(t, schemas, "KnowledgeSource")
	if source["additionalProperties"] != false {
		t.Fatal("KnowledgeSource must be a closed public provenance DTO")
	}
	sourceProperties := objectAt(t, source, "properties")
	assertExactObjectKeys(t, sourceProperties, []string{
		"ordinal",
		"kind",
		"visibility",
		"reference_label",
		"source_ticket_id",
		"source_attachment_id",
		"ticket_number",
		"ticket_title",
		"attachment_name",
		"attachment_hash",
	})
	assertExactStringArray(t, source["required"], []string{
		"ordinal",
		"kind",
		"visibility",
		"reference_label",
	})
	assertExactStringArray(
		t,
		objectAt(t, sourceProperties, "visibility")["enum"],
		[]string{"full", "restricted", "unavailable"},
	)
	for _, forbidden := range []string{
		"id",
		"article_id",
		"version_id",
		"created_at",
		"organization_id",
		"project_id",
		"created_by",
		"created_by_type",
		"created_by_id",
		"object_bucket",
		"object_key",
	} {
		if _, exposed := sourceProperties[forbidden]; exposed {
			t.Errorf("KnowledgeSource exposes internal field %s", forbidden)
		}
	}

	authored := objectAt(t, schemas, "KnowledgeAuthoredResult")
	assertExactObjectKeys(
		t,
		objectAt(t, authored, "properties"),
		[]string{"article", "version", "sources", "receipt"},
	)
	authoredProperties := objectAt(t, authored, "properties")
	if objectAt(t, authoredProperties, "sources")["maxItems"] != float64(20) {
		t.Error("KnowledgeAuthoredResult.sources must be capped at 20")
	}
	if objectAt(t, authoredProperties, "receipt")["$ref"] !=
		"#/components/schemas/Receipt" {
		t.Error("KnowledgeAuthoredResult must expose the standard receipt")
	}

	documentSchema := objectAt(t, schemas, "KnowledgeDocument")
	documentProperties := objectAt(t, documentSchema, "properties")
	assertExactObjectKeys(t, documentProperties, []string{
		"article",
		"version",
		"markdown",
		"sections",
		"sources",
	})
	if objectAt(t, documentProperties, "sections")["maxItems"] !=
		float64(100) {
		t.Error("KnowledgeDocument.sections must be capped at 100")
	}
	if objectAt(t, documentProperties, "sources")["maxItems"] !=
		float64(20) {
		t.Error("KnowledgeDocument.sources must be capped at 20")
	}
	documentSection := objectAt(t, schemas, "KnowledgeDocumentSection")
	assertExactObjectKeys(
		t,
		objectAt(t, documentSection, "properties"),
		[]string{
			"ordinal",
			"heading",
			"level",
			"section_path",
			"markdown",
			"content_hash",
		},
	)

	searchResult := objectAt(t, schemas, "KnowledgeSearchResult")
	if objectAt(
		t,
		objectAt(t, searchResult, "properties"),
		"items",
	)["maxItems"] != float64(50) {
		t.Error("KnowledgeSearchResult.items must be capped at 50")
	}
	citation := objectAt(t, schemas, "KnowledgeCitation")
	if citation["additionalProperties"] != false {
		t.Error("KnowledgeCitation must be a closed public DTO")
	}
}

func TestKnowledgeHumanSurfaceOmitsAdvancedUncontractedOperations(
	t *testing.T,
) {
	paths := objectAt(t, decodeDocument(t), "paths")
	versionPath := objectAt(
		t,
		paths,
		"/projects/{projectKey}/knowledge/articles/{articleID}/versions",
	)
	if _, published := versionPath["post"]; published {
		t.Error("raw knowledge version registration must not be a Human operation")
	}
	for _, path := range []string{
		"/projects/{projectKey}/knowledge/articles/{articleID}/access-grants",
		"/projects/{projectKey}/knowledge/versions/{versionID}/ingestions",
		"/projects/{projectKey}/knowledge/citations/{citationID}/feedback",
		"/projects/{projectKey}/knowledge/model-policy",
	} {
		if _, published := paths[path]; published {
			t.Errorf("advanced knowledge operation unexpectedly published: %s", path)
		}
	}
}

func assertRequiredFieldsExistInSameObjectProperties(
	t *testing.T,
	path string,
	value any,
) {
	t.Helper()
	switch typed := value.(type) {
	case map[string]any:
		if required, ok := typed["required"].([]any); ok {
			properties, propertiesOK := typed["properties"].(map[string]any)
			if !propertiesOK {
				t.Errorf("%s declares required fields without properties", path)
			} else {
				for _, rawName := range required {
					name, nameOK := rawName.(string)
					if !nameOK {
						t.Errorf("%s has non-string required field %T", path, rawName)
						continue
					}
					if _, exists := properties[name]; !exists {
						t.Errorf(
							"%s requires %q but does not declare it in properties",
							path,
							name,
						)
					}
				}
			}
		}
		for key, nested := range typed {
			assertRequiredFieldsExistInSameObjectProperties(
				t,
				path+"."+key,
				nested,
			)
		}
	case []any:
		for index, nested := range typed {
			assertRequiredFieldsExistInSameObjectProperties(
				t,
				fmt.Sprintf("%s[%d]", path, index),
				nested,
			)
		}
	}
}

func operationHasResponseArray(
	t *testing.T,
	document map[string]any,
	operation map[string]any,
) bool {
	t.Helper()
	schema, ok := operationResponseJSONSchema(t, document, operation)
	if !ok {
		return false
	}
	return schemaHasArray(t, document, schema, make(map[string]bool))
}

func operationHasUnboundedResponseArray(
	t *testing.T,
	document map[string]any,
	operation map[string]any,
) bool {
	t.Helper()
	schema, ok := operationResponseJSONSchema(t, document, operation)
	if !ok {
		return false
	}
	return schemaHasUnboundedArray(
		t,
		document,
		schema,
		make(map[string]bool),
	)
}

func operationResponseJSONSchema(
	t *testing.T,
	document map[string]any,
	operation map[string]any,
) (map[string]any, bool) {
	t.Helper()
	responses := objectAt(t, operation, "responses")
	statuses := make([]string, 0, len(responses))
	for status := range responses {
		if regexp.MustCompile(`^2[0-9][0-9]$`).MatchString(status) {
			statuses = append(statuses, status)
		}
	}
	sort.Strings(statuses)
	if len(statuses) == 0 {
		return nil, false
	}
	response, ok := responses[statuses[0]].(map[string]any)
	if !ok {
		return nil, false
	}
	if reference, ok := response["$ref"].(string); ok {
		const prefix = "#/components/responses/"
		response = objectAt(
			t,
			objectAt(t, objectAt(t, document, "components"), "responses"),
			strings.TrimPrefix(reference, prefix),
		)
	}
	content, ok := response["content"].(map[string]any)
	if !ok {
		return nil, false
	}
	media, ok := content["application/json"].(map[string]any)
	if !ok {
		return nil, false
	}
	schema, ok := media["schema"].(map[string]any)
	if !ok {
		return nil, false
	}
	return schema, true
}

func schemaHasArray(
	t *testing.T,
	document map[string]any,
	schema map[string]any,
	visiting map[string]bool,
) bool {
	t.Helper()
	if reference, ok := schema["$ref"].(string); ok {
		const prefix = "#/components/schemas/"
		if !strings.HasPrefix(reference, prefix) || visiting[reference] {
			return false
		}
		visiting[reference] = true
		defer delete(visiting, reference)
		return schemaHasArray(
			t,
			document,
			objectAt(
				t,
				objectAt(
					t,
					objectAt(t, document, "components"),
					"schemas",
				),
				strings.TrimPrefix(reference, prefix),
			),
			visiting,
		)
	}
	if schema["type"] == "array" {
		return true
	}
	for _, keyword := range []string{"allOf", "oneOf", "anyOf"} {
		if branches, ok := schema[keyword].([]any); ok {
			for _, raw := range branches {
				if branch, ok := raw.(map[string]any); ok &&
					schemaHasArray(t, document, branch, visiting) {
					return true
				}
			}
		}
	}
	if properties, ok := schema["properties"].(map[string]any); ok {
		for _, raw := range properties {
			if property, ok := raw.(map[string]any); ok &&
				schemaHasArray(t, document, property, visiting) {
				return true
			}
		}
	}
	return false
}

func schemaHasUnboundedArray(
	t *testing.T,
	document map[string]any,
	schema map[string]any,
	visiting map[string]bool,
) bool {
	t.Helper()
	if reference, ok := schema["$ref"].(string); ok {
		const prefix = "#/components/schemas/"
		if !strings.HasPrefix(reference, prefix) || visiting[reference] {
			return false
		}
		visiting[reference] = true
		defer delete(visiting, reference)
		return schemaHasUnboundedArray(
			t,
			document,
			objectAt(
				t,
				objectAt(
					t,
					objectAt(t, document, "components"),
					"schemas",
				),
				strings.TrimPrefix(reference, prefix),
			),
			visiting,
		)
	}
	if schema["type"] == "array" {
		maxItems, bounded := schema["maxItems"].(float64)
		if !bounded || maxItems > 100 {
			return true
		}
		if items, ok := schema["items"].(map[string]any); ok {
			return schemaHasUnboundedArray(t, document, items, visiting)
		}
		return false
	}
	for _, keyword := range []string{"allOf", "oneOf", "anyOf"} {
		if branches, ok := schema[keyword].([]any); ok {
			for _, raw := range branches {
				if branch, ok := raw.(map[string]any); ok &&
					schemaHasUnboundedArray(t, document, branch, visiting) {
					return true
				}
			}
		}
	}
	if properties, ok := schema["properties"].(map[string]any); ok {
		for _, raw := range properties {
			if property, ok := raw.(map[string]any); ok &&
				schemaHasUnboundedArray(t, document, property, visiting) {
				return true
			}
		}
	}
	return false
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

func operationQueryParameters(
	t *testing.T,
	document map[string]any,
	operation map[string]any,
) map[string]map[string]any {
	t.Helper()
	result := make(map[string]map[string]any)
	rawParameters, ok := operation["parameters"].([]any)
	if !ok {
		return result
	}
	components := objectAt(t, document, "components")
	componentParameters := objectAt(t, components, "parameters")
	const prefix = "#/components/parameters/"
	for _, raw := range rawParameters {
		parameter, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("operation parameter is %T, want object", raw)
		}
		if reference, hasReference := parameter["$ref"].(string); hasReference {
			if !strings.HasPrefix(reference, prefix) {
				t.Fatalf("unsupported parameter reference %q", reference)
			}
			parameter = objectAt(
				t,
				componentParameters,
				strings.TrimPrefix(reference, prefix),
			)
		}
		if parameter["in"] != "query" {
			continue
		}
		name, ok := parameter["name"].(string)
		if !ok || name == "" {
			t.Fatalf("query parameter has invalid name %v", parameter["name"])
		}
		if _, duplicate := result[name]; duplicate {
			t.Fatalf("duplicate query parameter %q", name)
		}
		result[name] = parameter
	}
	return result
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
