package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/seaworld008/chronodesk/server/internal/agentplatform"
	"github.com/seaworld008/chronodesk/server/internal/auth"
	"github.com/seaworld008/chronodesk/server/internal/handlers"
	"github.com/seaworld008/chronodesk/server/internal/middleware"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/services"
)

func TestPlatformEmergencyRoutesUseExactEmergencyOperatorMatrix(
	t *testing.T,
) {
	gin.SetMode(gin.TestMode)
	for _, method := range []string{http.MethodGet, http.MethodPut} {
		for _, test := range []struct {
			name       string
			role       any
			setRole    bool
			wantStatus int
		}{
			{
				name:       "emergency operator reaches dedicated handler",
				role:       auth.PlatformRoleEmergencyOperator,
				setRole:    true,
				wantStatus: http.StatusServiceUnavailable,
			},
			{
				name:       "platform administrator denied",
				role:       auth.PlatformRolePlatformAdmin,
				setRole:    true,
				wantStatus: http.StatusForbidden,
			},
			{
				name:       "security auditor denied",
				role:       auth.PlatformRoleSecurityAuditor,
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
				name:       "untyped emergency role denied",
				role:       string(auth.PlatformRoleEmergencyOperator),
				setRole:    true,
				wantStatus: http.StatusForbidden,
			},
			{
				name:       "missing role denied",
				wantStatus: http.StatusForbidden,
			},
		} {
			t.Run(method+"/"+test.name, func(t *testing.T) {
				router := gin.New()
				routes := router.Group("/api/platform")
				routes.Use(func(c *gin.Context) {
					c.Set("user_id", uint(7))
					if test.setRole {
						c.Set("platform_role", test.role)
					}
					c.Next()
				})
				routes.Use(ginAdapter(
					(&auth.AuthHandler{}).RequirePlatformRoles(
						auth.PlatformRoleEmergencyOperator,
					),
				))
				registerPlatformEmergencyControlRoutes(
					routes,
					handlers.NewEmergencyControlHandler(nil),
				)

				response := httptest.NewRecorder()
				request := httptest.NewRequest(
					method,
					"/api/platform/emergency-controls",
					strings.NewReader(`{"emergency_stop":true}`),
				)
				request.Header.Set("Content-Type", "application/json")
				request.Header.Set("If-Match", `"v1"`)
				router.ServeHTTP(response, request)
				if response.Code != test.wantStatus {
					t.Fatalf(
						"status=%d want=%d body=%s",
						response.Code,
						test.wantStatus,
						response.Body,
					)
				}
			})
		}
	}
}

func TestRegisterPlatformEmergencyRoutesPublishesOnlyDedicatedResource(
	t *testing.T,
) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	group := router.Group("/api/platform")
	registerPlatformEmergencyControlRoutes(
		group,
		handlers.NewEmergencyControlHandler(nil),
	)
	routes := make(map[string]struct{})
	for _, route := range router.Routes() {
		routes[route.Method+" "+route.Path] = struct{}{}
	}
	for _, expected := range []string{
		"GET /api/platform/emergency-controls",
		"PUT /api/platform/emergency-controls",
	} {
		if _, ok := routes[expected]; !ok {
			t.Errorf("route %s is missing", expected)
		}
	}
	if len(routes) != 2 {
		t.Fatalf("dedicated route count=%d, want 2: %+v", len(routes), routes)
	}
}

func TestPlatformEmergencyControlUpdatePersistsHumanAuditAttempt(
	t *testing.T,
) {
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(
		sqlite.Open("file:platform_emergency_audit?mode=memory&cache=shared"),
		&gorm.Config{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(
		&models.User{},
		&models.SystemConfig{},
		&models.AdminAuditLog{},
	); err != nil {
		t.Fatal(err)
	}
	operator := models.User{
		Username:     "emergency-operator",
		Email:        "emergency-operator@example.test",
		PasswordHash: "not-a-real-password",
		PlatformRole: models.PlatformRoleEmergencyOperator,
		Status:       models.UserStatusActive,
	}
	if err := db.Create(&operator).Error; err != nil {
		t.Fatal(err)
	}
	control, err := agentplatform.NewRuntimeControl(
		context.Background(),
		services.NewAgentNativeService(
			db,
			services.AgentNativeOptions{},
		),
		db,
		false,
	)
	if err != nil {
		t.Fatal(err)
	}
	auditService := services.NewAdminAuditService(db)
	router := gin.New()
	routes := router.Group("/api/platform")
	routes.Use(func(c *gin.Context) {
		c.Set("user_id", operator.ID)
		c.Set("platform_role", models.PlatformRoleEmergencyOperator)
		c.Set("request_id", "emergency-audit-request")
		c.Next()
	})
	routes.Use(middleware.LogAdminOperation(auditService))
	routes.Use(ginAdapter(
		(&auth.AuthHandler{}).RequirePlatformRoles(
			auth.PlatformRoleEmergencyOperator,
		),
	))
	registerPlatformEmergencyControlRoutes(
		routes,
		handlers.NewEmergencyControlHandler(control),
	)

	response := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPut,
		"/api/platform/emergency-controls",
		strings.NewReader(`{"global_read_only":true}`),
	)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("If-Match", `"v1"`)
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK ||
		response.Header().Get("ETag") != `"v2"` {
		t.Fatalf(
			"status=%d ETag=%q body=%s",
			response.Code,
			response.Header().Get("ETag"),
			response.Body,
		)
	}
	logs, total, err := auditService.List(
		context.Background(),
		&services.AdminAuditFilter{
			Path: "/api/platform/emergency-controls",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(logs) != 1 {
		t.Fatalf("audit total=%d rows=%d", total, len(logs))
	}
	record := logs[0]
	if record.ActorType != models.ActorTypeHuman ||
		record.ActorID != models.HumanActor(operator.ID).ID ||
		record.PlatformRole == nil ||
		*record.PlatformRole != models.PlatformRoleEmergencyOperator ||
		record.ActionCode != "platform.emergency_controls.update" ||
		record.ResourceType != "emergency_controls" ||
		record.ResourcePublicID != "global" ||
		record.StatusCode != http.StatusOK ||
		record.Result != "success" {
		t.Fatalf("emergency audit record = %+v", record)
	}

	for index, deniedRole := range []models.PlatformRole{
		models.PlatformRolePlatformAdmin,
		models.PlatformRoleSecurityAuditor,
		models.PlatformRoleMember,
	} {
		deniedRouter := gin.New()
		deniedRoutes := deniedRouter.Group("/api/platform")
		deniedRoutes.Use(func(c *gin.Context) {
			c.Set("user_id", operator.ID)
			c.Set("platform_role", deniedRole)
			c.Set("request_id", "emergency-denied-request")
			c.Next()
		})
		deniedRoutes.Use(middleware.LogAdminOperation(auditService))
		deniedRoutes.Use(ginAdapter(
			(&auth.AuthHandler{}).RequirePlatformRoles(
				auth.PlatformRoleEmergencyOperator,
			),
		))
		registerPlatformEmergencyControlRoutes(
			deniedRoutes,
			handlers.NewEmergencyControlHandler(control),
		)
		deniedResponse := httptest.NewRecorder()
		deniedRequest := httptest.NewRequest(
			http.MethodPut,
			"/api/platform/emergency-controls",
			strings.NewReader(`{"emergency_stop":true}`),
		)
		deniedRequest.Header.Set("Content-Type", "application/json")
		deniedRequest.Header.Set("If-Match", `"v2"`)
		deniedRouter.ServeHTTP(deniedResponse, deniedRequest)
		if deniedResponse.Code != http.StatusForbidden {
			t.Fatalf(
				"denied role %s status=%d body=%s",
				deniedRole,
				deniedResponse.Code,
				deniedResponse.Body,
			)
		}

		logs, total, err = auditService.List(
			context.Background(),
			&services.AdminAuditFilter{
				Path: "/api/platform/emergency-controls",
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		wantTotal := int64(index + 2)
		if total != wantTotal || len(logs) != int(wantTotal) {
			t.Fatalf(
				"denied audit total=%d rows=%d want=%d",
				total,
				len(logs),
				wantTotal,
			)
		}
		deniedRecord := logs[0]
		if deniedRecord.PlatformRole == nil ||
			*deniedRecord.PlatformRole != deniedRole ||
			deniedRecord.StatusCode != http.StatusForbidden ||
			deniedRecord.Result != "error" ||
			deniedRecord.ActionCode !=
				"platform.emergency_controls.update" {
			t.Fatalf(
				"denied role %s audit record = %+v",
				deniedRole,
				deniedRecord,
			)
		}
	}
}
