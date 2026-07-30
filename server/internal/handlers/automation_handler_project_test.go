package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/seaworld008/chronodesk/server/internal/eventcontract"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/services"
)

type automationHandlerSchedulerRedis struct{}

func (automationHandlerSchedulerRedis) Eval(
	context.Context,
	string,
	[]string,
	...interface{},
) (interface{}, error) {
	return int64(1), nil
}

func TestAutomationHandlerUsesOnlyProjectScopedAdminRoutes(t *testing.T) {
	environment := newProjectConfigurationHandlerEnvironment(t)
	if err := environment.db.AutoMigrate(
		&models.AutomationRule{},
		&models.AutomationLog{},
		&models.SLAConfig{},
		&models.TicketTemplate{},
		&models.QuickReply{},
	); err != nil {
		t.Fatal(err)
	}
	automationService := services.NewAutomationService(environment.db)
	schedulerService, err := services.NewSchedulerService(
		environment.db,
		automationHandlerSchedulerRedis{},
	)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewAutomationHandler(automationService, schedulerService)
	if err != nil {
		t.Fatal(err)
	}

	managerRouter := automationHandlerProjectRouter(
		environment,
		environment.manager,
		handler,
	)
	active := true
	body, err := json.Marshal(models.AutomationRuleRequest{
		Name:         "OPS 自动化",
		RuleType:     "assignment",
		TriggerEvent: eventcontract.TicketCreatedEventType,
		IsActive:     &active,
	})
	if err != nil {
		t.Fatal(err)
	}
	create := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/projects/OPS/admin/automation/rules",
		bytes.NewReader(body),
	)
	request.Header.Set("Content-Type", "application/json")
	managerRouter.ServeHTTP(create, request)
	if create.Code != http.StatusCreated {
		t.Fatalf("project automation create status=%d body=%s", create.Code, create.Body)
	}
	var created struct {
		Data models.AutomationRule `json:"data"`
	}
	if err := json.Unmarshal(create.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.Data.OrganizationID != environment.operations.OrganizationID ||
		created.Data.ProjectID != environment.operations.ID {
		t.Fatalf("created rule escaped route project: %+v", created.Data)
	}

	securityList := httptest.NewRecorder()
	managerRouter.ServeHTTP(
		securityList,
		httptest.NewRequest(
			http.MethodGet,
			"/api/projects/SEC/admin/automation/rules",
			nil,
		),
	)
	if securityList.Code != http.StatusOK {
		t.Fatalf("SEC list status=%d body=%s", securityList.Code, securityList.Body)
	}
	var listed struct {
		Data struct {
			Rules []models.AutomationRule `json:"rules"`
			Total int64                   `json:"total"`
		} `json:"data"`
	}
	if err := json.Unmarshal(securityList.Body.Bytes(), &listed); err != nil {
		t.Fatal(err)
	}
	if listed.Data.Total != 0 || len(listed.Data.Rules) != 0 {
		t.Fatalf("SEC list leaked OPS rules: %+v", listed.Data)
	}

	oldRoute := httptest.NewRecorder()
	managerRouter.ServeHTTP(
		oldRoute,
		httptest.NewRequest(
			http.MethodGet,
			"/api/admin/automation/rules",
			nil,
		),
	)
	if oldRoute.Code != http.StatusNotFound {
		t.Fatalf("legacy global automation route status=%d, want 404", oldRoute.Code)
	}

	agentRouter := automationHandlerProjectRouter(
		environment,
		environment.agent,
		handler,
	)
	forbidden := httptest.NewRecorder()
	agentRouter.ServeHTTP(
		forbidden,
		httptest.NewRequest(
			http.MethodGet,
			"/api/projects/OPS/admin/automation/rules",
			nil,
		),
	)
	if forbidden.Code != http.StatusForbidden {
		t.Fatalf("project agent automation status=%d body=%s", forbidden.Code, forbidden.Body)
	}
}

func automationHandlerProjectRouter(
	environment projectConfigurationHandlerEnvironment,
	user models.User,
	handler *AutomationHandler,
) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	projectGroup := router.Group("/api/projects/:projectKey")
	projectGroup.Use(func(c *gin.Context) {
		c.Set("user_id", user.ID)
		c.Set("platform_role", user.PlatformRole)
		c.Next()
	})
	projectGroup.Use(ProjectScopeMiddleware(
		environment.projectService,
		environment.db,
	))
	handler.RegisterProjectRoutes(projectGroup)
	return router
}
