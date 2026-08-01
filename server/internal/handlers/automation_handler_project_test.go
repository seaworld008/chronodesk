package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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

	publicReply := models.QuickReply{
		OrganizationID: environment.operations.OrganizationID,
		ProjectID:      environment.operations.ID,
		Name:           "公共回复",
		Content:        "public",
		IsPublic:       true,
		CreatedBy:      environment.manager.ID,
	}
	agentReply := models.QuickReply{
		OrganizationID: environment.operations.OrganizationID,
		ProjectID:      environment.operations.ID,
		Name:           "处理人私有回复",
		Content:        "private",
		CreatedBy:      environment.agent.ID,
	}
	managerReply := models.QuickReply{
		OrganizationID: environment.operations.OrganizationID,
		ProjectID:      environment.operations.ID,
		Name:           "经理私有回复",
		Content:        "private",
		CreatedBy:      environment.manager.ID,
	}
	if err := environment.db.Create(
		&[]*models.QuickReply{
			&publicReply,
			&agentReply,
			&managerReply,
		},
	).Error; err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name       string
		replyID    uint
		wantStatus int
	}{
		{
			name:       "public",
			replyID:    publicReply.ID,
			wantStatus: http.StatusOK,
		},
		{
			name:       "own private",
			replyID:    agentReply.ID,
			wantStatus: http.StatusOK,
		},
		{
			name:       "other private",
			replyID:    managerReply.ID,
			wantStatus: http.StatusNotFound,
		},
	} {
		t.Run("agent quick reply "+test.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			agentRouter.ServeHTTP(
				response,
				httptest.NewRequest(
					http.MethodPost,
					fmt.Sprintf(
						"/api/projects/OPS/admin/automation/quick-replies/%d/use",
						test.replyID,
					),
					nil,
				),
			)
			if response.Code != test.wantStatus {
				t.Fatalf(
					"status=%d body=%s want=%d",
					response.Code,
					response.Body,
					test.wantStatus,
				)
			}
		})
	}
}

func TestAutomationConfigurationListsUseUniformBoundedPageEnvelope(
	t *testing.T,
) {
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
	handler, err := NewAutomationHandler(
		automationService,
		schedulerService,
	)
	if err != nil {
		t.Fatal(err)
	}
	router := automationHandlerProjectRouter(
		environment,
		environment.manager,
		handler,
	)
	scope := environment.operations.Scope()
	if err := environment.db.Create(&models.SLAConfig{
		OrganizationID: scope.OrganizationID,
		ProjectID:      scope.ProjectID,
		Name:           "SLA",
		ResponseTime:   30,
		ResolutionTime: 60,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := environment.db.Create(&models.TicketTemplate{
		OrganizationID: scope.OrganizationID,
		ProjectID:      scope.ProjectID,
		Name:           "模板",
		Category:       "incident",
		CreatedBy:      environment.manager.ID,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := environment.db.Create(&models.QuickReply{
		OrganizationID: scope.OrganizationID,
		ProjectID:      scope.ProjectID,
		Name:           "回复",
		Content:        "内容",
		CreatedBy:      environment.manager.ID,
	}).Error; err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{
		"/api/projects/OPS/admin/automation/sla",
		"/api/projects/OPS/admin/automation/templates",
		"/api/projects/OPS/admin/automation/quick-replies",
	} {
		t.Run(path, func(t *testing.T) {
			response := httptest.NewRecorder()
			router.ServeHTTP(
				response,
				httptest.NewRequest(http.MethodGet, path, nil),
			)
			if response.Code != http.StatusOK {
				t.Fatalf(
					"status=%d body=%s",
					response.Code,
					response.Body,
				)
			}
			var payload struct {
				Data map[string]json.RawMessage `json:"data"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
				t.Fatal(err)
			}
			for _, required := range []string{
				"items",
				"total",
				"page",
				"page_size",
				"total_pages",
			} {
				if _, exists := payload.Data[required]; !exists {
					t.Fatalf(
						"response omits %s: %s",
						required,
						response.Body,
					)
				}
			}
			for _, legacy := range []string{
				"configs",
				"templates",
				"replies",
			} {
				if _, exists := payload.Data[legacy]; exists {
					t.Fatalf(
						"response retains legacy %s: %s",
						legacy,
						response.Body,
					)
				}
			}
			var pageSize int
			if err := json.Unmarshal(
				payload.Data["page_size"],
				&pageSize,
			); err != nil {
				t.Fatal(err)
			}
			if pageSize != services.DefaultAutomationListSize {
				t.Fatalf("page_size=%d", pageSize)
			}
		})
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
