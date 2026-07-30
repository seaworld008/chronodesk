package handlers

import (
	"bytes"
	"context"
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

type projectHandlerEventAppender struct{}

func (projectHandlerEventAppender) AppendDomainEventTx(
	_ context.Context,
	_ *gorm.DB,
	_ services.DomainEventInput,
	_ []services.OutboxTarget,
) (*models.DomainEvent, error) {
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
		Username: "agent",
		Email:    "agent@example.test",
		Role:     models.RoleAgent,
		Status:   models.UserStatusActive,
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
		c.Set("user_role", string(models.RoleAgent))
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

func TestProjectScopeMiddlewareRejectsCrossProjectAccess(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service, _, user, db := projectHandlerTestService(t)
	router := gin.New()
	router.GET(
		"/api/projects/:projectKey/context",
		func(c *gin.Context) {
			c.Set("user_id", user.ID)
			c.Set("user_role", string(models.RoleAgent))
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
	if body := response.Body.String(); !strings.Contains(body, `"code":403`) ||
		!strings.Contains(body, `"msg":"无权访问该项目"`) {
		t.Fatalf("unexpected cross-project error contract: %s", body)
	}
}

func TestProjectScopeMiddlewareRollsBackUnsuccessfulRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service, project, user, db := projectHandlerTestService(t)
	target := models.User{
		Username: "rollback-target",
		Email:    "rollback-target@example.test",
		Role:     models.RoleCustomer,
		Status:   models.UserStatusActive,
	}
	if err := db.Create(&target).Error; err != nil {
		t.Fatal(err)
	}

	router := gin.New()
	group := router.Group("/api/projects/:projectKey")
	group.Use(func(c *gin.Context) {
		c.Set("user_id", user.ID)
		c.Set("user_role", string(models.RoleAgent))
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

	router := gin.New()
	group := router.Group("/api/projects/:projectKey")
	group.Use(func(c *gin.Context) {
		c.Set("user_id", user.ID)
		c.Set("user_role", string(models.RoleAgent))
		c.Next()
	})
	group.Use(ProjectScopeMiddleware(service, db))
	group.POST("/commit-failure", func(c *gin.Context) {
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
	var count int64
	if err := db.Table("project_commit_children").Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("failed commit persisted %d child rows", count)
	}
}

func TestProjectMembershipHandlerCreatesExplicitGrant(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service, project, administrator, db := projectHandlerTestService(t)
	target := models.User{
		Username: "project-target",
		Email:    "project-target@example.test",
		Role:     models.RoleCustomer,
		Status:   models.UserStatusActive,
	}
	if err := db.Create(&target).Error; err != nil {
		t.Fatal(err)
	}
	handler := NewProjectHandler(service)
	router := gin.New()
	group := router.Group("/api/projects/:projectKey")
	group.Use(func(c *gin.Context) {
		c.Set("user_id", administrator.ID)
		c.Set("user_role", string(models.RoleAdmin))
		c.Next()
	})
	group.Use(ProjectScopeMiddleware(service, db))
	group.POST("/memberships", handler.UpsertMembership)

	response := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/projects/OPS/memberships",
		bytes.NewBufferString(
			`{"user_id":`+
				strconv.FormatUint(uint64(target.ID), 10)+
				`,"role":"requester"}`,
		),
	)
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
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
		membership.Version != 1 {
		t.Fatalf("unexpected project membership: %+v", membership)
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
		false,
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
