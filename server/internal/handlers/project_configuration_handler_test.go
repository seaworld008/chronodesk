package handlers

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/services"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type projectConfigurationHandlerEnvironment struct {
	db             *gorm.DB
	projectService *services.ProjectService
	configService  *services.ProjectConfigurationService
	operations     models.Project
	security       models.Project
	manager        models.User
	agent          models.User
}

type projectConfigurationHandlerResponse[T any] struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
	Data T      `json:"data"`
}

func TestProjectConfigurationHandlerPublishesScopedConfiguration(
	t *testing.T,
) {
	environment := newProjectConfigurationHandlerEnvironment(t)
	router := projectConfigurationHandlerRouter(
		environment,
		environment.manager,
	)

	requestTypeResponse := performProjectConfigurationRequest[models.RequestTypeVersion](
		t,
		router,
		http.MethodPost,
		"/api/projects/OPS/configuration/request-type-versions",
		requestTypeDraftRequest{
			Key:        "incident",
			Name:       "事件",
			WorkClass:  models.WorkClassIncident,
			JSONSchema: projectConfigurationHandlerJSONSchema(false),
			UISchema:   json.RawMessage(`{}`),
		},
		http.StatusCreated,
	)
	if requestTypeResponse.Data.OrganizationID !=
		environment.operations.OrganizationID ||
		requestTypeResponse.Data.ProjectID != environment.operations.ID ||
		requestTypeResponse.Data.CreatedByID !=
			strconv.FormatUint(uint64(environment.manager.ID), 10) {
		t.Fatalf(
			"request type did not use trusted context: %+v",
			requestTypeResponse.Data,
		)
	}

	workflowResponse := performProjectConfigurationRequest[models.WorkflowVersion](
		t,
		router,
		http.MethodPost,
		"/api/projects/OPS/configuration/workflow-versions",
		workflowDraftRequest{
			Key:         "default",
			Name:        "默认工作流",
			States:      projectConfigurationHandlerWorkflowStates(),
			Transitions: projectConfigurationHandlerWorkflowTransitions(),
		},
		http.StatusCreated,
	)
	if workflowResponse.Data.OrganizationID !=
		environment.operations.OrganizationID ||
		workflowResponse.Data.ProjectID != environment.operations.ID {
		t.Fatalf(
			"workflow did not use trusted context: %+v",
			workflowResponse.Data,
		)
	}

	releaseResponse := performProjectConfigurationRequest[models.ConfigurationRelease](
		t,
		router,
		http.MethodPost,
		"/api/projects/OPS/configuration/releases",
		configurationReleaseDraftRequest{
			Snapshot: models.ConfigurationSnapshot{
				RequestTypeVersionIDs: []string{
					requestTypeResponse.Data.ID,
				},
				WorkflowVersionIDs: []string{
					workflowResponse.Data.ID,
				},
			},
		},
		http.StatusCreated,
	)
	performProjectConfigurationRequest[models.ConfigurationSimulationReport](
		t,
		router,
		http.MethodPost,
		fmt.Sprintf(
			"/api/projects/OPS/configuration/releases/%s/simulations",
			releaseResponse.Data.ID,
		),
		nil,
		http.StatusOK,
	)
	publishedResponse := performProjectConfigurationRequest[models.ConfigurationRelease](
		t,
		router,
		http.MethodPost,
		fmt.Sprintf(
			"/api/projects/OPS/configuration/releases/%s/publication",
			releaseResponse.Data.ID,
		),
		nil,
		http.StatusOK,
	)
	if publishedResponse.Data.Status != models.ConfigurationStatusPublished ||
		publishedResponse.Data.ApprovedByID !=
			strconv.FormatUint(uint64(environment.manager.ID), 10) {
		t.Fatalf("published release = %+v", publishedResponse.Data)
	}

	currentResponse := performProjectConfigurationRequest[models.ConfigurationRelease](
		t,
		router,
		http.MethodGet,
		"/api/projects/OPS/configuration/releases/current",
		nil,
		http.StatusOK,
	)
	if currentResponse.Data.ID != releaseResponse.Data.ID {
		t.Fatalf(
			"current release id = %q, want %q",
			currentResponse.Data.ID,
			releaseResponse.Data.ID,
		)
	}
	intakeResponse := performProjectConfigurationRequest[services.ProjectIntakeConfiguration](
		t,
		projectConfigurationHandlerRouter(
			environment,
			environment.agent,
		),
		http.MethodGet,
		"/api/projects/OPS/configuration/intake",
		nil,
		http.StatusOK,
	)
	if intakeResponse.Data.ReleaseID != publishedResponse.Data.ID ||
		intakeResponse.Data.ReleaseVersion != publishedResponse.Data.Version ||
		len(intakeResponse.Data.RequestTypes) != 1 ||
		intakeResponse.Data.RequestTypes[0].ID != requestTypeResponse.Data.ID ||
		len(intakeResponse.Data.Workflows) != 1 ||
		intakeResponse.Data.Workflows[0].ID != workflowResponse.Data.ID {
		t.Fatalf("project member intake response = %+v", intakeResponse.Data)
	}

	var persistedRequestType models.RequestTypeVersion
	if err := environment.db.First(
		&persistedRequestType,
		"id = ?",
		requestTypeResponse.Data.ID,
	).Error; err != nil {
		t.Fatal(err)
	}
	var persistedWorkflow models.WorkflowVersion
	if err := environment.db.First(
		&persistedWorkflow,
		"id = ?",
		workflowResponse.Data.ID,
	).Error; err != nil {
		t.Fatal(err)
	}
	if persistedRequestType.Status != models.ConfigurationStatusPublished ||
		persistedWorkflow.Status != models.ConfigurationStatusPublished {
		t.Fatalf(
			"published child statuses: request=%q workflow=%q",
			persistedRequestType.Status,
			persistedWorkflow.Status,
		)
	}

	crossProject := performProjectConfigurationRequest[json.RawMessage](
		t,
		router,
		http.MethodPost,
		fmt.Sprintf(
			"/api/projects/SEC/configuration/releases/%s/simulations",
			releaseResponse.Data.ID,
		),
		nil,
		http.StatusNotFound,
	)
	if crossProject.Msg != "项目配置资源不存在" {
		t.Fatalf("cross-project error message = %q", crossProject.Msg)
	}
}

func TestProjectConfigurationHandlerRejectsUntrustedBodyScopeAndRole(
	t *testing.T,
) {
	environment := newProjectConfigurationHandlerEnvironment(t)
	managerRouter := projectConfigurationHandlerRouter(
		environment,
		environment.manager,
	)
	raw := json.RawMessage(`{
		"key":"incident",
		"name":"事件",
		"work_class":"incident",
		"json_schema":{
			"$schema":"https://json-schema.org/draft/2020-12/schema",
			"type":"object"
		},
		"ui_schema":{},
		"organization_id":999,
		"project_id":999,
		"created_by_id":"attacker"
	}`)
	response := performProjectConfigurationRequest[json.RawMessage](
		t,
		managerRouter,
		http.MethodPost,
		"/api/projects/OPS/configuration/request-type-versions",
		raw,
		http.StatusBadRequest,
	)
	if response.Msg != "项目配置请求参数无效" {
		t.Fatalf("untrusted scope error message = %q", response.Msg)
	}
	var count int64
	if err := environment.db.Model(&models.RequestTypeVersion{}).
		Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("request types created after scope injection attempt = %d", count)
	}

	agentRouter := projectConfigurationHandlerRouter(
		environment,
		environment.agent,
	)
	response = performProjectConfigurationRequest[json.RawMessage](
		t,
		agentRouter,
		http.MethodPost,
		"/api/projects/OPS/configuration/request-type-versions",
		requestTypeDraftRequest{
			Key:        "incident",
			Name:       "事件",
			WorkClass:  models.WorkClassIncident,
			JSONSchema: projectConfigurationHandlerJSONSchema(false),
			UISchema:   json.RawMessage(`{}`),
		},
		http.StatusForbidden,
	)
	if response.Msg != "仅项目管理员或经理可管理项目配置" {
		t.Fatalf("agent role error message = %q", response.Msg)
	}
	response = performProjectConfigurationRequest[json.RawMessage](
		t,
		agentRouter,
		http.MethodGet,
		"/api/projects/OPS/configuration/intake",
		nil,
		http.StatusNotFound,
	)
	if response.Msg != "项目配置资源不存在" {
		t.Fatalf("missing intake configuration error message = %q", response.Msg)
	}
}

func TestProjectConfigurationHandlerSolutionPreviewInstallAndRollback(
	t *testing.T,
) {
	environment := newProjectConfigurationHandlerEnvironment(t)
	router := projectConfigurationHandlerRouter(
		environment,
		environment.manager,
	)
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	solution := projectConfigurationHandlerSignedSolution(t, privateKey)
	packageRequest := solutionPackageRequest{
		Package:   *solution,
		PublicKey: base64.StdEncoding.EncodeToString(publicKey),
	}

	preview := performProjectConfigurationRequest[services.SolutionUpgradePreview](
		t,
		router,
		http.MethodPost,
		"/api/projects/OPS/configuration/solution-upgrade-previews",
		packageRequest,
		http.StatusOK,
	)
	if preview.Data.PackageKey != solution.Manifest.PackageKey ||
		preview.Data.PackageVersion != solution.Manifest.Version {
		t.Fatalf("solution preview = %+v", preview.Data)
	}

	installation := performProjectConfigurationRequest[models.ProjectSolutionInstallation](
		t,
		router,
		http.MethodPost,
		"/api/projects/OPS/configuration/solution-installations",
		packageRequest,
		http.StatusCreated,
	)
	if installation.Data.Status != models.SolutionInstallationPending ||
		installation.Data.ProjectID != environment.operations.ID {
		t.Fatalf("prepared installation = %+v", installation.Data)
	}

	performProjectConfigurationRequest[models.ConfigurationSimulationReport](
		t,
		router,
		http.MethodPost,
		fmt.Sprintf(
			"/api/projects/OPS/configuration/solution-installations/%s/simulations",
			installation.Data.ID,
		),
		nil,
		http.StatusOK,
	)
	approved := performProjectConfigurationRequest[models.ProjectSolutionInstallation](
		t,
		router,
		http.MethodPost,
		fmt.Sprintf(
			"/api/projects/OPS/configuration/solution-installations/%s/publication",
			installation.Data.ID,
		),
		nil,
		http.StatusOK,
	)
	if approved.Data.Status != models.SolutionInstallationActive {
		t.Fatalf("approved installation = %+v", approved.Data)
	}

	rollback := performProjectConfigurationRequest[models.ConfigurationRelease](
		t,
		router,
		http.MethodPost,
		fmt.Sprintf(
			"/api/projects/OPS/configuration/releases/%s/rollbacks",
			approved.Data.ReleaseID,
		),
		nil,
		http.StatusCreated,
	)
	if rollback.Data.RollbackOfReleaseID == nil ||
		*rollback.Data.RollbackOfReleaseID != approved.Data.ReleaseID ||
		rollback.Data.Status != models.ConfigurationStatusPublished {
		t.Fatalf("rollback release = %+v", rollback.Data)
	}
}

func newProjectConfigurationHandlerEnvironment(
	t *testing.T,
) projectConfigurationHandlerEnvironment {
	t.Helper()
	database, err := gorm.Open(
		sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"),
		&gorm.Config{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.AutoMigrate(
		&models.User{},
		&models.Organization{},
		&models.BusinessUnit{},
		&models.Project{},
		&models.ProjectMembership{},
		&models.Team{},
		&models.Queue{},
		&models.RequestTypeVersion{},
		&models.WorkflowVersion{},
		&models.ConfigurationRelease{},
		&models.ProjectSolutionInstallation{},
	); err != nil {
		t.Fatal(err)
	}
	organization := models.Organization{
		Slug:   "handler-" + strings.ToLower(strings.ReplaceAll(t.Name(), "/", "-")),
		Name:   "Handler Test",
		Status: models.OrganizationStatusActive,
	}
	if err := database.Create(&organization).Error; err != nil {
		t.Fatal(err)
	}
	unit := models.BusinessUnit{
		OrganizationID: organization.ID,
		Key:            "OPS",
		Name:           "Operations",
		Status:         models.BusinessUnitStatusActive,
	}
	if err := database.Create(&unit).Error; err != nil {
		t.Fatal(err)
	}
	projects := []models.Project{
		{
			OrganizationID: organization.ID,
			BusinessUnitID: unit.ID,
			Key:            "OPS",
			Name:           "Operations",
			Status:         models.ProjectStatusActive,
		},
		{
			OrganizationID: organization.ID,
			BusinessUnitID: unit.ID,
			Key:            "SEC",
			Name:           "Security",
			Status:         models.ProjectStatusActive,
		},
	}
	if err := database.Create(&projects).Error; err != nil {
		t.Fatal(err)
	}
	for _, project := range projects {
		queue := models.Queue{
			ProjectID: project.ID,
			Key:       "default",
			Name:      "默认队列",
			Status:    models.QueueStatusActive,
			IsDefault: true,
		}
		if err := database.Create(&queue).Error; err != nil {
			t.Fatal(err)
		}
	}
	manager := models.User{
		Username:     "configuration-manager",
		Email:        "configuration-manager@example.test",
		PlatformRole: models.PlatformRoleMember,
		Status:       models.UserStatusActive,
	}
	agent := models.User{
		Username:     "configuration-agent",
		Email:        "configuration-agent@example.test",
		PlatformRole: models.PlatformRoleMember,
		Status:       models.UserStatusActive,
	}
	if err := database.Create(&manager).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Create(&agent).Error; err != nil {
		t.Fatal(err)
	}
	memberships := []models.ProjectMembership{
		{
			ProjectID: projects[0].ID,
			UserID:    manager.ID,
			Role:      models.ProjectRoleManager,
			IsActive:  true,
		},
		{
			ProjectID: projects[1].ID,
			UserID:    manager.ID,
			Role:      models.ProjectRoleManager,
			IsActive:  true,
		},
		{
			ProjectID: projects[0].ID,
			UserID:    agent.ID,
			Role:      models.ProjectRoleAgent,
			IsActive:  true,
		},
	}
	if err := database.Create(&memberships).Error; err != nil {
		t.Fatal(err)
	}
	projectService, err := services.NewProjectService(database)
	if err != nil {
		t.Fatal(err)
	}
	configService, err := services.NewProjectConfigurationService(
		database,
		projectHandlerEventAppender{},
	)
	if err != nil {
		t.Fatal(err)
	}
	return projectConfigurationHandlerEnvironment{
		db:             database,
		projectService: projectService,
		configService:  configService,
		operations:     projects[0],
		security:       projects[1],
		manager:        manager,
		agent:          agent,
	}
}

func projectConfigurationHandlerRouter(
	environment projectConfigurationHandlerEnvironment,
	user models.User,
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
	NewProjectConfigurationHandler(environment.configService).
		RegisterRoutes(projectGroup)
	return router
}

func performProjectConfigurationRequest[T any](
	t *testing.T,
	router http.Handler,
	method string,
	path string,
	body any,
	wantStatus int,
) projectConfigurationHandlerResponse[T] {
	t.Helper()
	var encoded []byte
	var err error
	switch value := body.(type) {
	case nil:
	case json.RawMessage:
		encoded = append([]byte(nil), value...)
	default:
		encoded, err = json.Marshal(body)
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
	if recorder.Code != wantStatus {
		t.Fatalf(
			"%s %s status = %d, want %d, body = %s",
			method,
			path,
			recorder.Code,
			wantStatus,
			recorder.Body.String(),
		)
	}
	var response projectConfigurationHandlerResponse[T]
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response %s: %v", recorder.Body.String(), err)
	}
	return response
}

func projectConfigurationHandlerWorkflowStates() []models.WorkflowStateDefinition {
	return []models.WorkflowStateDefinition{
		{
			Key:               "open",
			Name:              "新建",
			LifecycleCategory: models.LifecycleCategoryNew,
			IsInitial:         true,
		},
		{
			Key:               "in_progress",
			Name:              "处理中",
			LifecycleCategory: models.LifecycleCategoryActive,
		},
		{
			Key:               "resolved",
			Name:              "已解决",
			LifecycleCategory: models.LifecycleCategoryResolved,
			IsTerminal:        true,
		},
	}
}

func projectConfigurationHandlerWorkflowTransitions() []models.WorkflowTransitionDefinition {
	return []models.WorkflowTransitionDefinition{
		{
			Key:  "start",
			Name: "开始处理",
			From: "open",
			To:   "in_progress",
		},
		{
			Key:  "resolve",
			Name: "解决",
			From: "in_progress",
			To:   "resolved",
		},
	}
}

func projectConfigurationHandlerJSONSchema(
	addRequiredImpact bool,
) json.RawMessage {
	required := `["title"]`
	properties := `"title":{"type":"string"}`
	if addRequiredImpact {
		required = `["title","impact"]`
		properties += `,"impact":{"type":"string"}`
	}
	return json.RawMessage(
		`{"$schema":"https://json-schema.org/draft/2020-12/schema",` +
			`"type":"object","properties":{` +
			properties +
			`},"required":` +
			required +
			`,"additionalProperties":false}`,
	)
}

func projectConfigurationHandlerSignedSolution(
	t *testing.T,
	privateKey ed25519.PrivateKey,
) *models.IndustrySolutionPackage {
	t.Helper()
	snapshot := models.IndustrySolutionSnapshot{
		RequestTypes: []models.RequestTypeTemplate{
			{
				Key:        "incident",
				Name:       "事件",
				WorkClass:  models.WorkClassIncident,
				JSONSchema: projectConfigurationHandlerJSONSchema(false),
				UISchema:   json.RawMessage(`{}`),
			},
		},
		Workflows: []models.WorkflowTemplate{
			{
				Key:         "default",
				Name:        "默认工作流",
				States:      projectConfigurationHandlerWorkflowStates(),
				Transitions: projectConfigurationHandlerWorkflowTransitions(),
			},
		},
	}
	manifest := models.IndustrySolutionManifest{
		SchemaVersion: "1.0",
		PackageKey:    "it-operations",
		Name:          "IT 运维",
		Industry:      "technology",
		Version:       "1.0.0",
		Terminology:   map[string]string{"ticket": "工单"},
		TemplateReferences: []models.SolutionTemplateReference{
			{
				Kind: models.SolutionTemplateRequestType,
				Key:  "incident",
			},
			{
				Kind: models.SolutionTemplateWorkflow,
				Key:  "default",
			},
		},
	}
	solution, err := models.SignIndustrySolutionPackage(
		manifest,
		snapshot,
		"handler-test-signer",
		privateKey,
	)
	if err != nil {
		t.Fatal(err)
	}
	return solution
}
