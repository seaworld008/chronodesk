package app

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/seaworld008/chronodesk/server/internal/handlers"
	"github.com/seaworld008/chronodesk/server/internal/middleware"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/services"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestProjectAgentAdminFailuresRetainAuditOutsideProjectTransaction(
	t *testing.T,
) {
	gin.SetMode(gin.TestMode)
	for _, test := range []struct {
		name          string
		projectRole   models.ProjectRole
		handlerStatus int
		wantStatus    int
	}{
		{
			name:          "handler failure",
			projectRole:   models.ProjectRoleAdmin,
			handlerStatus: http.StatusInternalServerError,
			wantStatus:    http.StatusInternalServerError,
		},
		{
			name:          "project role denied",
			projectRole:   models.ProjectRoleManager,
			handlerStatus: http.StatusNoContent,
			wantStatus:    http.StatusForbidden,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			db := openProjectAgentAdminAuditTestDB(t)
			project, user := seedProjectAgentAdminAuditTest(
				t,
				db,
				test.projectRole,
			)
			projectService, err := services.NewProjectService(db)
			if err != nil {
				t.Fatal(err)
			}
			auditService := services.NewAdminAuditService(db)
			router := gin.New()
			projects := router.Group("/api/projects")
			projects.Use(func(c *gin.Context) {
				c.Set("user_id", user.ID)
				c.Set("platform_role", models.PlatformRoleMember)
				c.Next()
			})
			routes := projects.Group("/:projectKey/admin/agents")
			configureProjectAgentAdminMiddleware(
				routes,
				middleware.LogAdminOperation(auditService),
				handlers.ProjectScopeMiddleware(projectService, db),
				handlers.RequireProjectRoles(models.ProjectRoleAdmin),
			)
			handlerCalled := false
			routes.POST("/outbox/:id/replay", func(c *gin.Context) {
				handlerCalled = true
				c.Status(test.handlerStatus)
			})

			path := fmt.Sprintf(
				"/api/projects/%s/admin/agents/outbox/outbox-failure/replay",
				project.Key,
			)
			response := httptest.NewRecorder()
			router.ServeHTTP(
				response,
				httptest.NewRequest(http.MethodPost, path, nil),
			)
			if response.Code != test.wantStatus {
				t.Fatalf(
					"status = %d, want %d; body=%s",
					response.Code,
					test.wantStatus,
					response.Body.String(),
				)
			}
			wantHandlerCall := test.projectRole == models.ProjectRoleAdmin
			if handlerCalled != wantHandlerCall {
				t.Fatalf(
					"handler called = %t, want %t",
					handlerCalled,
					wantHandlerCall,
				)
			}

			logs, total, err := auditService.List(
				context.Background(),
				&services.AdminAuditFilter{Path: path},
			)
			if err != nil {
				t.Fatalf("list retained audit attempt: %v", err)
			}
			if total != 1 || len(logs) != 1 {
				t.Fatalf(
					"retained audit attempts total=%d rows=%d",
					total,
					len(logs),
				)
			}
			if logs[0].StatusCode != test.wantStatus ||
				logs[0].Result != "error" ||
				logs[0].UserID == nil ||
				*logs[0].UserID != user.ID ||
				logs[0].PlatformRole != models.PlatformRoleMember {
				t.Fatalf("retained audit attempt = %+v", logs[0])
			}
		})
	}
}

func openProjectAgentAdminAuditTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(
		sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"),
		&gorm.Config{},
	)
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := sqlDB.Close(); err != nil {
			t.Errorf("close audit test database: %v", err)
		}
	})
	if err := db.AutoMigrate(
		&models.User{},
		&models.Organization{},
		&models.BusinessUnit{},
		&models.Project{},
		&models.ProjectMembership{},
		&models.AdminAuditLog{},
	); err != nil {
		t.Fatal(err)
	}
	return db
}

func seedProjectAgentAdminAuditTest(
	t *testing.T,
	db *gorm.DB,
	role models.ProjectRole,
) (models.Project, models.User) {
	t.Helper()
	organization := models.Organization{
		Slug:   "agent-admin-audit",
		Name:   "Agent Admin Audit",
		Status: models.OrganizationStatusActive,
	}
	if err := db.Create(&organization).Error; err != nil {
		t.Fatal(err)
	}
	unit := models.BusinessUnit{
		OrganizationID: organization.ID,
		Key:            "AUDIT",
		Name:           "Audit",
		Status:         models.BusinessUnitStatusActive,
	}
	if err := db.Create(&unit).Error; err != nil {
		t.Fatal(err)
	}
	project := models.Project{
		OrganizationID: organization.ID,
		BusinessUnitID: unit.ID,
		Key:            models.ProjectKey("AUDIT"),
		Name:           "Audit",
		Status:         models.ProjectStatusActive,
	}
	if err := db.Create(&project).Error; err != nil {
		t.Fatal(err)
	}
	user := models.User{
		Username:     "agent-admin-auditor",
		Email:        "agent-admin-auditor@example.test",
		PasswordHash: "hash",
		PlatformRole: models.PlatformRoleMember,
		Status:       models.UserStatusActive,
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.ProjectMembership{
		ProjectID: project.ID,
		UserID:    user.ID,
		Role:      role,
		IsActive:  true,
		Version:   1,
	}).Error; err != nil {
		t.Fatal(err)
	}
	return project, user
}
