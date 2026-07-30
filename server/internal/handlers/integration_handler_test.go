package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/services"
	"gorm.io/gorm"
)

type integrationHandlerEnvironment struct {
	db      *gorm.DB
	service *services.IntegrationManagementService
	project models.Project
	user    models.User
}

func TestIntegrationHandlerUsesStrictDTOsAndRedactsConnectionSecrets(t *testing.T) {
	environment := newIntegrationHandlerEnvironment(t)
	router := integrationHandlerRouter(
		t,
		environment,
		models.ProjectRoleManager,
	)

	for index, forbiddenField := range []string{
		"organization_id",
		"project_id",
		"actor_id",
	} {
		body := map[string]any{
			"key":                  "forged-" + strconv.Itoa(index),
			"name":                 "Forged",
			"kind":                 "webhook",
			"direction":            "inbound",
			"signature_scheme":     "hmac-sha256",
			"configuration_schema": map[string]any{},
			"mapping_schema":       map[string]any{},
			forbiddenField:         999,
		}
		response := performIntegrationHandlerRequest(
			t,
			router,
			http.MethodPost,
			"/api/projects/INT/integrations/connector-definitions",
			body,
		)
		if response.Code != http.StatusBadRequest {
			t.Fatalf(
				"forged %s status=%d body=%s",
				forbiddenField,
				response.Code,
				response.Body.String(),
			)
		}
	}
	var definitions int64
	if err := environment.db.Model(&models.ConnectorDefinition{}).Count(&definitions).Error; err != nil {
		t.Fatal(err)
	}
	if definitions != 0 {
		t.Fatalf("strict DTO wrote %d connector definitions", definitions)
	}

	definitionResponse := performIntegrationHandlerRequest(
		t,
		router,
		http.MethodPost,
		"/api/projects/INT/integrations/connector-definitions",
		map[string]any{
			"key":                  "webhook",
			"name":                 "Webhook",
			"kind":                 "webhook",
			"direction":            "inbound",
			"signature_scheme":     "hmac-sha256",
			"configuration_schema": map[string]any{"type": "object"},
			"mapping_schema":       map[string]any{"type": "object"},
		},
	)
	if definitionResponse.Code != http.StatusCreated {
		t.Fatalf(
			"create definition status=%d body=%s",
			definitionResponse.Code,
			definitionResponse.Body.String(),
		)
	}
	definitionID := integrationResponseDataID(t, definitionResponse.Body.Bytes())
	connectionResponse := performIntegrationHandlerRequest(
		t,
		router,
		http.MethodPost,
		"/api/projects/INT/integrations/connections",
		map[string]any{
			"connector_definition_id": definitionID,
			"key":                     "primary",
			"name":                    "Primary",
			"configuration": map[string]any{
				"base_url": "https://connector.example.test",
			},
			"verification_key_ref":  "vault://chronodesk/integration-key",
			"replay_window_seconds": 300,
		},
	)
	if connectionResponse.Code != http.StatusCreated {
		t.Fatalf(
			"create connection status=%d body=%s",
			connectionResponse.Code,
			connectionResponse.Body.String(),
		)
	}
	body := connectionResponse.Body.String()
	for _, forbidden := range []string{
		"vault://chronodesk/integration-key",
		"https://connector.example.test",
		"verification_key_ref",
		`"configuration"`,
		`"organization_id"`,
		`"project_id"`,
		`"actor_id"`,
	} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("connection response leaked %q: %s", forbidden, body)
		}
	}
	if !strings.Contains(body, `"has_verification_key":true`) ||
		!strings.Contains(body, `"has_configuration":true`) {
		t.Fatalf("connection response omitted safe secret-presence flags: %s", body)
	}

	inlineSecret := performIntegrationHandlerRequest(
		t,
		router,
		http.MethodPost,
		"/api/projects/INT/integrations/connections",
		map[string]any{
			"connector_definition_id": definitionID,
			"key":                     "unsafe",
			"name":                    "Unsafe",
			"configuration": map[string]any{
				"password": "plaintext",
			},
			"verification_key_ref": "vault://chronodesk/integration-key",
		},
	)
	if inlineSecret.Code != http.StatusBadRequest {
		t.Fatalf("inline secret status=%d body=%s", inlineSecret.Code, inlineSecret.Body.String())
	}
}

func TestIntegrationHandlerRestrictsMutationsAndAllowsObserverLists(t *testing.T) {
	environment := newIntegrationHandlerEnvironment(t)
	observer := integrationHandlerRouter(
		t,
		environment,
		models.ProjectRoleObserver,
	)
	list := performIntegrationHandlerRequest(
		t,
		observer,
		http.MethodGet,
		"/api/projects/INT/integrations/connector-definitions",
		nil,
	)
	if list.Code != http.StatusOK {
		t.Fatalf("observer list status=%d body=%s", list.Code, list.Body.String())
	}
	mutation := performIntegrationHandlerRequest(
		t,
		observer,
		http.MethodPost,
		"/api/projects/INT/integrations/connector-definitions",
		map[string]any{
			"key":                  "observer-write",
			"name":                 "Observer write",
			"kind":                 "webhook",
			"direction":            "inbound",
			"signature_scheme":     "hmac-sha256",
			"configuration_schema": map[string]any{},
			"mapping_schema":       map[string]any{},
		},
	)
	if mutation.Code != http.StatusForbidden {
		t.Fatalf("observer mutation status=%d body=%s", mutation.Code, mutation.Body.String())
	}

	agent := integrationHandlerRouter(t, environment, models.ProjectRoleAgent)
	agentList := performIntegrationHandlerRequest(
		t,
		agent,
		http.MethodGet,
		"/api/projects/INT/integrations/overview",
		nil,
	)
	if agentList.Code != http.StatusForbidden {
		t.Fatalf("agent overview status=%d body=%s", agentList.Code, agentList.Body.String())
	}
}

func newIntegrationHandlerEnvironment(t *testing.T) integrationHandlerEnvironment {
	t.Helper()
	db := openHandlerTestDB(t)
	if err := db.AutoMigrate(
		&models.Organization{},
		&models.BusinessUnit{},
		&models.Project{},
		&models.User{},
		&models.ConnectorDefinition{},
		&models.Connection{},
		&models.MappingVersion{},
		&models.InboxMessage{},
		&models.InboxReceipt{},
		&models.ExternalLink{},
		&models.SyncCursor{},
		&models.SyncRun{},
		&models.IntegrationConflict{},
		&models.DeadLetter{},
	); err != nil {
		t.Fatal(err)
	}
	organization := models.Organization{
		Slug:   "integration-handler",
		Name:   "Integration Handler",
		Status: models.OrganizationStatusActive,
	}
	if err := db.Create(&organization).Error; err != nil {
		t.Fatal(err)
	}
	unit := models.BusinessUnit{
		OrganizationID: organization.ID,
		Key:            "integration",
		Name:           "Integration",
		Status:         models.BusinessUnitStatusActive,
	}
	if err := db.Create(&unit).Error; err != nil {
		t.Fatal(err)
	}
	project := models.Project{
		OrganizationID: organization.ID,
		BusinessUnitID: unit.ID,
		Key:            "INT",
		Name:           "Integration",
		Status:         models.ProjectStatusActive,
	}
	if err := db.Create(&project).Error; err != nil {
		t.Fatal(err)
	}
	user := models.User{
		Username:     "integration-manager",
		Email:        "integration-manager@example.test",
		PasswordHash: "test-only",
		Role:         models.RoleAgent,
		Status:       models.UserStatusActive,
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	service, err := services.NewIntegrationManagementService(db, nil)
	if err != nil {
		t.Fatal(err)
	}
	return integrationHandlerEnvironment{
		db: db, service: service, project: project, user: user,
	}
}

func integrationHandlerRouter(
	t *testing.T,
	environment integrationHandlerEnvironment,
	role models.ProjectRole,
) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	projectGroup := router.Group("/api/projects/:projectKey")
	projectGroup.Use(func(c *gin.Context) {
		c.Set("user_id", environment.user.ID)
		access := services.ProjectAccess{
			Project: environment.project,
			Role:    role,
			Scope:   environment.project.Scope(),
		}
		c.Set(projectAccessContextKey, access)
		c.Set(projectRoleContextKey, string(role))
		ctx, err := services.WithOperationContext(
			c.Request.Context(),
			services.OperationContext{
				Scope:  access.Scope,
				Actor:  models.HumanActor(environment.user.ID),
				Source: services.SourceProtocolHumanREST,
			},
		)
		if err != nil {
			t.Fatalf("bind integration handler context: %v", err)
		}
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	})
	NewIntegrationHandler(environment.service).RegisterRoutes(projectGroup)
	return router
}

func performIntegrationHandlerRequest(
	t *testing.T,
	router http.Handler,
	method string,
	path string,
	body any,
) *httptest.ResponseRecorder {
	t.Helper()
	var encoded []byte
	var err error
	switch value := body.(type) {
	case nil:
	case json.RawMessage:
		encoded = append([]byte(nil), value...)
	default:
		encoded, err = json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
	}
	request := httptest.NewRequest(method, path, bytes.NewReader(encoded))
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	return recorder
}

func integrationResponseDataID(t *testing.T, raw []byte) string {
	t.Helper()
	var envelope struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatalf("decode integration response: %v (%s)", err, raw)
	}
	if envelope.Data.ID == "" {
		t.Fatalf("integration response has no id: %s", raw)
	}
	return envelope.Data.ID
}
