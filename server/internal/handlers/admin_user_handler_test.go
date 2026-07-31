package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/services"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type adminUserActorEventAppender struct {
	input     services.DomainEventInput
	operation services.OperationContext
	calls     int
}

func (appender *adminUserActorEventAppender) AppendDomainEventTx(
	ctx context.Context,
	_ *gorm.DB,
	input services.DomainEventInput,
	_ []services.OutboxTarget,
) (*models.DomainEvent, error) {
	appender.calls++
	appender.input = input
	if operation, err := services.OperationContextFromContext(ctx); err == nil {
		appender.operation = operation
	}
	return &models.DomainEvent{ID: "handler-user-access-event"}, nil
}

func TestAdminUserUpdateAllowsClearingPhoneAndEmailVerification(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := openHandlerTestDB(t)
	if err := db.AutoMigrate(&models.User{}, &models.UserProfile{}); err != nil {
		t.Fatalf("migrate users: %v", err)
	}
	user := models.User{
		Username: "update-user", Email: "update-user@example.com",
		Phone: "+8613800138000", PasswordHash: "hashed",
		PlatformRole: models.PlatformRoleMember, Status: models.UserStatusActive,
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}

	handler := NewAdminUserHandler(services.NewAdminUserService(db))
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("user_id", uint(77))
		c.Next()
	})
	router.PUT("/users/:id", handler.UpdateUser)

	body, _ := json.Marshal(map[string]any{
		"phone":          "   ",
		"display_name":   "Updated Agent",
		"email_verified": true,
	})
	request := httptest.NewRequest(
		http.MethodPut,
		fmt.Sprintf("/users/%d", user.ID),
		bytes.NewReader(body),
	)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("update status = %d, body=%s", response.Code, response.Body.String())
	}

	var persisted models.User
	if err := db.First(&persisted, user.ID).Error; err != nil {
		t.Fatalf("reload user: %v", err)
	}
	if persisted.Phone != "" {
		t.Fatalf("phone = %q, want empty", persisted.Phone)
	}
	if persisted.DisplayName != "Updated Agent" {
		t.Fatalf("display_name = %q", persisted.DisplayName)
	}
	if !persisted.EmailVerified {
		t.Fatal("email_verified was not updated")
	}

	invalidRequest := httptest.NewRequest(
		http.MethodPut,
		fmt.Sprintf("/users/%d", user.ID),
		bytes.NewBufferString(`{"phone":"13800138000"}`),
	)
	invalidRequest.Header.Set("Content-Type", "application/json")
	invalidResponse := httptest.NewRecorder()
	router.ServeHTTP(invalidResponse, invalidRequest)
	if invalidResponse.Code != http.StatusBadRequest {
		t.Fatalf("invalid phone status = %d, want %d", invalidResponse.Code, http.StatusBadRequest)
	}

	validPhoneRequest := httptest.NewRequest(
		http.MethodPut,
		fmt.Sprintf("/users/%d", user.ID),
		bytes.NewBufferString(`{"phone":"+8613900139000"}`),
	)
	validPhoneRequest.Header.Set("Content-Type", "application/json")
	validPhoneResponse := httptest.NewRecorder()
	router.ServeHTTP(validPhoneResponse, validPhoneRequest)
	if validPhoneResponse.Code != http.StatusOK {
		t.Fatalf("valid phone status = %d, body=%s", validPhoneResponse.Code, validPhoneResponse.Body.String())
	}
	nullPhoneRequest := httptest.NewRequest(
		http.MethodPut,
		fmt.Sprintf("/users/%d", user.ID),
		bytes.NewBufferString(`{"phone":null}`),
	)
	nullPhoneRequest.Header.Set("Content-Type", "application/json")
	nullPhoneResponse := httptest.NewRecorder()
	router.ServeHTTP(nullPhoneResponse, nullPhoneRequest)
	if nullPhoneResponse.Code != http.StatusOK {
		t.Fatalf("null phone status = %d, body=%s", nullPhoneResponse.Code, nullPhoneResponse.Body.String())
	}
	if err := db.First(&persisted, user.ID).Error; err != nil {
		t.Fatal(err)
	}
	if persisted.Phone != "+8613900139000" {
		t.Fatalf("null phone changed persisted value to %q", persisted.Phone)
	}

	invalidManagerRequest := httptest.NewRequest(
		http.MethodPut,
		fmt.Sprintf("/users/%d", user.ID),
		bytes.NewBufferString(`{"manager_id":0}`),
	)
	invalidManagerRequest.Header.Set("Content-Type", "application/json")
	invalidManagerResponse := httptest.NewRecorder()
	router.ServeHTTP(invalidManagerResponse, invalidManagerRequest)
	if invalidManagerResponse.Code != http.StatusBadRequest {
		t.Fatalf(
			"invalid manager_id status = %d, want %d",
			invalidManagerResponse.Code,
			http.StatusBadRequest,
		)
	}

	avatarRequest := httptest.NewRequest(
		http.MethodPut,
		fmt.Sprintf("/users/%d", user.ID),
		bytes.NewBufferString(`{"avatar":"https://example.test/untrusted.png"}`),
	)
	avatarRequest.Header.Set("Content-Type", "application/json")
	avatarResponse := httptest.NewRecorder()
	router.ServeHTTP(avatarResponse, avatarRequest)
	if avatarResponse.Code != http.StatusBadRequest {
		t.Fatalf(
			"avatar status = %d, want %d; body=%s",
			avatarResponse.Code,
			http.StatusBadRequest,
			avatarResponse.Body.String(),
		)
	}

	validAvatar := fmt.Sprintf(
		"/uploads/avatars/%d/00000000-0000-4000-8000-000000000001.png",
		user.ID,
	)
	validAvatarRequest := httptest.NewRequest(
		http.MethodPut,
		fmt.Sprintf("/users/%d", user.ID),
		bytes.NewBufferString(fmt.Sprintf(`{"avatar":%q}`, validAvatar)),
	)
	validAvatarRequest.Header.Set("Content-Type", "application/json")
	validAvatarResponse := httptest.NewRecorder()
	router.ServeHTTP(validAvatarResponse, validAvatarRequest)
	if validAvatarResponse.Code != http.StatusOK {
		t.Fatalf(
			"valid avatar status = %d, body=%s",
			validAvatarResponse.Code,
			validAvatarResponse.Body.String(),
		)
	}

	for _, historicalRole := range []string{"user", "superuser"} {
		roleRequest := httptest.NewRequest(
			http.MethodPut,
			fmt.Sprintf("/users/%d", user.ID),
			bytes.NewBufferString(fmt.Sprintf(`{"role":%q}`, historicalRole)),
		)
		roleRequest.Header.Set("Content-Type", "application/json")
		roleResponse := httptest.NewRecorder()
		router.ServeHTTP(roleResponse, roleRequest)
		if roleResponse.Code != http.StatusBadRequest {
			t.Errorf(
				"historical role %q status = %d, want %d; body=%s",
				historicalRole,
				roleResponse.Code,
				http.StatusBadRequest,
				roleResponse.Body.String(),
			)
		}
	}
}

func TestAdminUserUpdateUsesAuthenticatedHumanActorForRevocationEvent(
	t *testing.T,
) {
	gin.SetMode(gin.TestMode)
	db := openHandlerTestDB(t)
	if err := db.AutoMigrate(
		&models.User{},
		&models.Organization{},
		&models.BusinessUnit{},
		&models.Project{},
	); err != nil {
		t.Fatal(err)
	}
	organization := models.Organization{
		Slug:   "admin-user-actor",
		Name:   "Admin User Actor",
		Status: models.OrganizationStatusActive,
	}
	if err := db.Create(&organization).Error; err != nil {
		t.Fatal(err)
	}
	unit := models.BusinessUnit{
		OrganizationID: organization.ID,
		Key:            "ADMIN",
		Name:           "Admin",
		Status:         models.BusinessUnitStatusActive,
	}
	if err := db.Create(&unit).Error; err != nil {
		t.Fatal(err)
	}
	project := models.Project{
		OrganizationID: organization.ID,
		BusinessUnitID: unit.ID,
		Key:            models.ProjectKey("DEFAULT"),
		Name:           "Default",
		Status:         models.ProjectStatusActive,
	}
	if err := db.Create(&project).Error; err != nil {
		t.Fatal(err)
	}
	target := models.User{
		Username:     "actor-target",
		Email:        "actor-target@example.test",
		PasswordHash: "hash",
		PlatformRole: models.PlatformRoleMember,
		Status:       models.UserStatusActive,
	}
	if err := db.Create(&target).Error; err != nil {
		t.Fatal(err)
	}
	appender := &adminUserActorEventAppender{}
	service, err := services.NewAdminUserServiceWithAccessRevocationOutbox(
		db,
		appender,
	)
	if err != nil {
		t.Fatal(err)
	}
	handler := NewAdminUserHandler(service)
	const operatorID = uint(77)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("user_id", operatorID)
		c.Next()
	})
	router.PUT("/users/:id", handler.UpdateUser)

	request := httptest.NewRequest(
		http.MethodPut,
		fmt.Sprintf("/users/%d", target.ID),
		bytes.NewBufferString(`{"status":"suspended"}`),
	)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf(
			"status = %d, want 200; body=%s",
			response.Code,
			response.Body.String(),
		)
	}
	wantActor := models.HumanActor(operatorID)
	if appender.calls != 1 ||
		appender.input.Actor != wantActor ||
		appender.operation.Actor != wantActor {
		t.Fatalf(
			"revocation actor input=%+v operation=%+v calls=%d, want %+v",
			appender.input.Actor,
			appender.operation.Actor,
			appender.calls,
			wantActor,
		)
	}
}

func TestAdminUserCreateMapsRetainedIdentityConflictToChinese409(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := openHandlerTestDB(t)
	if err := db.AutoMigrate(&models.User{}); err != nil {
		t.Fatalf("migrate users: %v", err)
	}
	deleted := models.User{
		Username:     "retained-handler-user",
		Email:        "retained-handler-user@example.com",
		PasswordHash: "hashed",
		PlatformRole: models.PlatformRoleMember,
		Status:       models.UserStatusDeleted,
	}
	if err := db.Create(&deleted).Error; err != nil {
		t.Fatalf("create retained user: %v", err)
	}
	if err := db.Delete(&deleted).Error; err != nil {
		t.Fatalf("soft delete retained user: %v", err)
	}

	handler := NewAdminUserHandler(services.NewAdminUserService(db))
	router := gin.New()
	router.POST("/users", handler.CreateUser)

	body, _ := json.Marshal(map[string]any{
		"username":      "new-handler-user",
		"email":         deleted.Email,
		"password":      "StrongPassword123!",
		"platform_role": "member",
	})
	request := httptest.NewRequest(http.MethodPost, "/users", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%s", response.Code, response.Body.String())
	}
	bodyText := response.Body.String()
	if !strings.Contains(bodyText, "用户名或邮箱已被使用") {
		t.Fatalf("response is not the stable Chinese conflict: %s", bodyText)
	}
	if strings.Contains(bodyText, "SQLSTATE") || strings.Contains(bodyText, "unique constraint") {
		t.Fatalf("response leaked database details: %s", bodyText)
	}
}

func TestAdminUserStrictCreateValidationRejectsBeforeServiceMutation(
	t *testing.T,
) {
	gin.SetMode(gin.TestMode)
	db := openHandlerTestDB(t)
	if err := db.AutoMigrate(&models.User{}); err != nil {
		t.Fatal(err)
	}
	handler := NewAdminUserHandler(
		services.NewAdminUserService(db),
	)
	router := gin.New()
	router.POST("/users", handler.CreateUser)

	tests := []struct {
		name string
		body string
	}{
		{
			name: "platform_role",
			body: `{"username":"invalid-role","email":"role@example.test","password":"StrongPassword123!","platform_role":"bogus"}`,
		},
		{
			name: "email",
			body: `{"username":"invalid-email","email":"not-an-email","password":"StrongPassword123!","platform_role":"member"}`,
		},
		{
			name: "username_length",
			body: `{"username":"` + strings.Repeat("u", 51) + `","email":"long@example.test","password":"StrongPassword123!","platform_role":"member"}`,
		},
		{
			name: "phone_e164",
			body: `{"username":"invalid-phone","email":"phone@example.test","phone":"13800138000","password":"StrongPassword123!","platform_role":"member"}`,
		},
		{
			name: "phone_empty",
			body: `{"username":"empty-phone","email":"empty-phone@example.test","phone":"","password":"StrongPassword123!","platform_role":"member"}`,
		},
		{
			name: "phone_whitespace",
			body: `{"username":"space-phone","email":"space-phone@example.test","phone":"   ","password":"StrongPassword123!","platform_role":"member"}`,
		},
		{
			name: "phone_null",
			body: `{"username":"null-phone","email":"null-phone@example.test","phone":null,"password":"StrongPassword123!","platform_role":"member"}`,
		},
		{
			name: "manager_id",
			body: `{"username":"invalid-manager","email":"manager@example.test","password":"StrongPassword123!","platform_role":"member","manager_id":0}`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(
				http.MethodPost,
				"/users",
				bytes.NewBufferString(test.body),
			)
			request.Header.Set(
				"Content-Type",
				"application/json",
			)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != http.StatusBadRequest {
				t.Fatalf(
					"status = %d, want 400; body=%s",
					response.Code,
					response.Body.String(),
				)
			}
			var count int64
			if err := db.Model(&models.User{}).
				Count(&count).Error; err != nil {
				t.Fatal(err)
			}
			if count != 0 {
				t.Fatalf(
					"invalid request reached service mutation; users=%d",
					count,
				)
			}
		})
	}
}

func TestAdminUserHandlerAcceptsAllPublishedListQueryParameters(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := openHandlerTestDB(t)
	if err := db.AutoMigrate(&models.User{}); err != nil {
		t.Fatalf("migrate users: %v", err)
	}
	user := models.User{
		Username:     "query-user",
		Email:        "query-user@example.test",
		DisplayName:  "Query User",
		PasswordHash: "hashed",
		PlatformRole: models.PlatformRoleMember,
		Status:       models.UserStatusActive,
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}

	handler := NewAdminUserHandler(services.NewAdminUserService(db))
	router := gin.New()
	router.GET("/users", handler.GetUserList)
	request := httptest.NewRequest(
		http.MethodGet,
		"/users?page=1"+
			"&page_size=20"+
			"&platform_role=member"+
			"&status=active"+
			"&search=query-user"+
			"&order_by=username"+
			"&order=asc",
		nil,
	)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", response.Code, response.Body.String())
	}
	for _, expected := range []string{
		`"username":"query-user"`,
		`"platform_role":"member"`,
		`"page_size":20`,
	} {
		if !strings.Contains(response.Body.String(), expected) {
			t.Errorf("response is missing %s: %s", expected, response.Body.String())
		}
	}
}
