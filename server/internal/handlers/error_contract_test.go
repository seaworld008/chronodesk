package handlers

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"unicode"

	"github.com/gin-gonic/gin"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/services"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

const handlerLeakSentinel = "password=PRIVATE SQLSTATE 08006 /srv/private/path"

type failingTicketStatisticsService struct {
	services.TicketServiceInterface
}

func (failingTicketStatisticsService) GetTicketStatistics(
	context.Context,
	uint,
	string,
) (*services.TicketStatisticsResponse, error) {
	return nil, errors.New(handlerLeakSentinel)
}

type emailConfigServiceStub struct {
	services.EmailConfigServiceInterface
}

func (emailConfigServiceStub) UpdateEmailConfig(
	context.Context,
	*models.EmailConfigUpdateRequest,
	uint,
) (*models.EmailConfig, error) {
	return nil, errors.New(handlerLeakSentinel)
}

func TestHandlerSourcesNeverWriteRawErrorTextToResponses(t *testing.T) {
	t.Helper()
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("无法定位 Handler 测试目录")
	}
	entries, err := os.ReadDir(filepath.Dir(currentFile))
	if err != nil {
		t.Fatalf("读取 Handler 目录失败: %v", err)
	}

	for _, entry := range entries {
		if entry.IsDir() ||
			!strings.HasSuffix(entry.Name(), ".go") ||
			strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		source, err := os.ReadFile(filepath.Join(filepath.Dir(currentFile), entry.Name()))
		if err != nil {
			t.Fatalf("读取 %s 失败: %v", entry.Name(), err)
		}
		for lineNumber, line := range strings.Split(string(source), "\n") {
			if !strings.Contains(line, "err.Error()") {
				continue
			}
			// Error text may classify a small, explicitly mapped legacy domain
			// error. It must never become response content.
			if strings.Contains(line, "strings.Contains(err.Error(),") ||
				strings.Contains(line, "err.Error() ==") {
				continue
			}
			t.Errorf(
				"%s:%d 直接使用 err.Error()；请映射为稳定中文响应并调用 logHandlerFailure",
				entry.Name(),
				lineNumber+1,
			)
		}
	}
}

func TestInternalHandlerErrorsStayInServerLogs(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("ticket workflow", func(t *testing.T) {
		handler := NewTicketWorkflowHandler(failingTicketStatisticsService{})
		router := gin.New()
		router.GET("/tickets/stats", func(c *gin.Context) {
			c.Set("user_id", uint(7))
			c.Set("platform_role", models.PlatformRolePlatformAdmin)
			handler.GetTicketStats(c)
		})

		response := httptest.NewRecorder()
		router.ServeHTTP(
			response,
			httptest.NewRequest(http.MethodGet, "/tickets/stats", nil),
		)
		assertSafeChineseHandlerError(t, response, http.StatusInternalServerError)
		if !strings.Contains(response.Body.String(), `"error":"internal_error"`) {
			t.Fatalf("缺少稳定机器错误码: %s", response.Body.String())
		}
	})

	t.Run("config database", func(t *testing.T) {
		db := openErrorContractDB(t, "config")
		handler := NewConfigHandler(db)
		router := gin.New()
		router.GET("/configs", handler.GetAllConfigs)

		response := httptest.NewRecorder()
		router.ServeHTTP(
			response,
			httptest.NewRequest(http.MethodGet, "/configs", nil),
		)
		assertSafeChineseHandlerError(t, response, http.StatusInternalServerError)
	})

	t.Run("admin user database", func(t *testing.T) {
		db := openErrorContractDB(t, "admin-user")
		handler := NewAdminUserHandler(services.NewAdminUserService(db))
		router := gin.New()
		router.GET("/users", handler.GetUserList)

		response := httptest.NewRecorder()
		router.ServeHTTP(
			response,
			httptest.NewRequest(http.MethodGet, "/users?page=1&page_size=20", nil),
		)
		assertSafeChineseHandlerError(t, response, http.StatusInternalServerError)
	})

	t.Run("webhook database", func(t *testing.T) {
		db := openErrorContractDB(t, "webhook")
		handler := NewWebhookHandlerWithProtector(db, nil)
		router := gin.New()
		router.GET("/webhooks", func(c *gin.Context) {
			bindWebhookProjectTestContext(t, c)
			handler.ListWebhooks(c)
		})

		response := httptest.NewRecorder()
		router.ServeHTTP(
			response,
			httptest.NewRequest(http.MethodGet, "/webhooks", nil),
		)
		assertSafeChineseHandlerError(t, response, http.StatusInternalServerError)
	})
}

func TestBindingErrorsUseStableChineseMessages(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("email config", func(t *testing.T) {
		handler := NewEmailConfigHandler(emailConfigServiceStub{})
		router := gin.New()
		router.PUT("/email-config", func(c *gin.Context) {
			c.Set("user_id", uint(7))
			handler.UpdateEmailConfig(c)
		})

		response := httptest.NewRecorder()
		request := httptest.NewRequest(
			http.MethodPut,
			"/email-config",
			bytes.NewBufferString(`{"smtp_host":`),
		)
		request.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(response, request)
		assertSafeChineseHandlerError(t, response, http.StatusBadRequest)
		if !strings.Contains(response.Body.String(), `"msg":"invalid_request"`) {
			t.Fatalf("缺少稳定机器错误码: %s", response.Body.String())
		}
	})

	t.Run("webhook", func(t *testing.T) {
		handler := NewWebhookHandlerWithProtector(openErrorContractDB(t, "webhook-binding"), nil)
		router := gin.New()
		router.POST("/webhooks", func(c *gin.Context) {
			bindWebhookProjectTestContext(t, c)
			handler.CreateWebhook(c)
		})

		response := httptest.NewRecorder()
		request := httptest.NewRequest(
			http.MethodPost,
			"/webhooks",
			bytes.NewBufferString(`{"name":`),
		)
		request.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(response, request)
		assertSafeChineseHandlerError(t, response, http.StatusBadRequest)
	})
}

func openErrorContractDB(t *testing.T, name string) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(
		sqlite.Open("file:"+name+"-"+strings.ReplaceAll(t.Name(), "/", "-")+"?mode=memory&cache=shared"),
		&gorm.Config{},
	)
	if err != nil {
		t.Fatalf("创建测试数据库失败: %v", err)
	}
	return db
}

func assertSafeChineseHandlerError(
	t *testing.T,
	response *httptest.ResponseRecorder,
	wantStatus int,
) {
	t.Helper()
	if response.Code != wantStatus {
		t.Fatalf("状态码=%d，期望=%d；响应=%s", response.Code, wantStatus, response.Body.String())
	}
	body := response.Body.String()
	for _, forbidden := range []string{
		handlerLeakSentinel,
		"PRIVATE",
		"SQLSTATE",
		"no such table",
		"unexpected EOF",
		"invalid character",
		"/srv/private/path",
	} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("响应泄漏内部信息 %q: %s", forbidden, body)
		}
	}
	if !strings.ContainsFunc(body, func(r rune) bool {
		return unicode.Is(unicode.Han, r)
	}) {
		t.Fatalf("错误响应缺少中文提示: %s", body)
	}
}
