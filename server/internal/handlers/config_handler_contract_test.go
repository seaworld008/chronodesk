package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/seaworld008/chronodesk/server/internal/middleware"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/services"
)

type configRouteAuditCapture struct {
	records []*services.AdminAuditRecord
}

func TestPlatformConfigListRejectsInvalidDirectoryQueries(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := openHandlerTestDB(t)
	handler := NewConfigHandler(db)
	router := gin.New()
	router.GET("/configs", handler.GetAllConfigs)
	for _, query := range []string{
		"page=0",
		"page_size=101",
		"sort_by=value",
		"sort_order=ASC",
		"category=unknown",
		"category=system&category=security",
		"unknown=value",
	} {
		response := httptest.NewRecorder()
		router.ServeHTTP(
			response,
			httptest.NewRequest(
				http.MethodGet,
				"/configs?"+query,
				nil,
			),
		)
		if response.Code != http.StatusBadRequest {
			t.Fatalf(
				"query %q status = %d, body=%s",
				query,
				response.Code,
				response.Body.String(),
			)
		}
	}
}

func (capture *configRouteAuditCapture) Record(
	_ context.Context,
	record *services.AdminAuditRecord,
) error {
	record.ID = uint(len(capture.records) + 1)
	capture.records = append(capture.records, record)
	return nil
}

func (*configRouteAuditCapture) Finalize(
	context.Context,
	*services.AdminAuditRecord,
) error {
	return nil
}

func (*configRouteAuditCapture) List(
	context.Context,
	*services.AdminAuditFilter,
) ([]*models.AdminAuditLog, int64, error) {
	return nil, 0, nil
}

func TestPlatformConfigRoutesPreserveValidUnicodeAuditKeys(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := openHandlerTestDB(t)
	if err := db.AutoMigrate(&models.SystemConfig{}); err != nil {
		t.Fatal(err)
	}
	handler := NewConfigHandler(db)
	audit := &configRouteAuditCapture{}
	router := gin.New()
	router.Use(middleware.LogAdminOperation(audit))
	router.PUT(
		"/api/platform/configs/:key",
		handler.UpdateConfig,
	)
	router.DELETE(
		"/api/platform/configs/:key",
		handler.DeleteConfig,
	)

	hundredChinese := strings.Repeat("配", 100)
	distinctKeys := []string{"配置.é", "配置.e\u0301"}
	for _, key := range append([]string{hundredChinese}, distinctKeys...) {
		response := performPlatformConfigWrite(
			t,
			router,
			http.MethodPut,
			key,
			[]byte(`{"value":"已启用","value_type":"string"}`),
			"application/json",
		)
		if response.Code != http.StatusOK {
			t.Fatalf(
				"PUT key %q status = %d, body=%s",
				key,
				response.Code,
				response.Body.String(),
			)
		}
	}

	response := performPlatformConfigWrite(
		t,
		router,
		http.MethodDelete,
		hundredChinese,
		nil,
		"",
	)
	if response.Code != http.StatusOK {
		t.Fatalf(
			"DELETE 100-code-point key status = %d, body=%s",
			response.Code,
			response.Body.String(),
		)
	}

	if len(audit.records) != 4 {
		t.Fatalf("audit records = %d, want 4", len(audit.records))
	}
	for index, key := range []string{
		hundredChinese,
		distinctKeys[0],
		distinctKeys[1],
		hundredChinese,
	} {
		if audit.records[index].ResourcePublicID != key {
			t.Fatalf(
				"audit record %d public ID = %q, want %q",
				index,
				audit.records[index].ResourcePublicID,
				key,
			)
		}
	}
	if audit.records[1].ResourcePublicID ==
		audit.records[2].ResourcePublicID {
		t.Fatalf(
			"distinct legal Unicode keys collided: %q",
			audit.records[1].ResourcePublicID,
		)
	}
}

func TestPlatformConfigHandlersRejectInvalidKeysBeforePersistence(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := openHandlerTestDB(t)
	handler := NewConfigHandler(db)
	router := gin.New()
	router.POST("/configs", handler.CreateConfig)
	router.PUT("/configs/batch", handler.BatchUpdateConfigs)
	router.PUT("/configs/:key", handler.UpdateConfig)
	router.DELETE("/configs/:key", handler.DeleteConfig)
	router.POST("/configs/import", handler.ImportConfigs)

	createBody, err := json.Marshal(models.SystemConfig{
		Key:       " 配置",
		Value:     "value",
		ValueType: "string",
	})
	if err != nil {
		t.Fatal(err)
	}
	batchBody, err := json.Marshal([]models.SystemConfig{{
		Key:       "配置\u3000",
		Value:     "value",
		ValueType: "string",
	}})
	if err != nil {
		t.Fatal(err)
	}
	importBody, importContentType := invalidConfigImportBody(
		t,
		strings.Repeat("配", 101),
	)

	tests := []struct {
		name        string
		method      string
		target      string
		body        []byte
		contentType string
	}{
		{
			name:        "create leading whitespace",
			method:      http.MethodPost,
			target:      "/configs",
			body:        createBody,
			contentType: "application/json",
		},
		{
			name:   "update more than one hundred code points",
			method: http.MethodPut,
			target: "/configs/" + url.PathEscape(
				strings.Repeat("配", 101),
			),
			body:        []byte(`{"value":"value","value_type":"string"}`),
			contentType: "application/json",
		},
		{
			name:   "delete control character",
			method: http.MethodDelete,
			target: "/configs/" + url.PathEscape("配置\u0001键"),
		},
		{
			name:        "batch trailing whitespace",
			method:      http.MethodPut,
			target:      "/configs/batch",
			body:        batchBody,
			contentType: "application/json",
		},
		{
			name:        "import invalid key",
			method:      http.MethodPost,
			target:      "/configs/import",
			body:        importBody,
			contentType: importContentType,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(
				test.method,
				test.target,
				bytes.NewReader(test.body),
			)
			if test.contentType != "" {
				request.Header.Set("Content-Type", test.contentType)
			}
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != http.StatusBadRequest {
				t.Fatalf(
					"status = %d, want 400; body=%s",
					response.Code,
					response.Body.String(),
				)
			}
			var payload struct {
				Success bool   `json:"success"`
				Message string `json:"message"`
				Error   string `json:"error"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if payload.Success ||
				payload.Message != "配置键无效" ||
				payload.Error != "invalid_config_key" {
				t.Fatalf("unstable invalid-key response: %+v", payload)
			}
		})
	}
}

func TestPlatformConfigUpdateUsesClosedRuntimeDTO(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := openHandlerTestDB(t)
	if err := db.AutoMigrate(&models.SystemConfig{}); err != nil {
		t.Fatal(err)
	}
	config := models.SystemConfig{
		Key:         "system.contract",
		Value:       "before",
		ValueType:   "string",
		Description: "before",
		Category:    "system",
		Group:       "contract",
		IsActive:    true,
	}
	if err := db.Create(&config).Error; err != nil {
		t.Fatal(err)
	}

	handler := NewConfigHandler(db)
	router := gin.New()
	router.PUT("/configs/:key", handler.UpdateConfig)

	ghost := performConfigUpdate(
		t,
		router,
		`{"value":"after","value_type":"string","description":"after","category":"system","group":"contract","is_active":false}`,
	)
	if ghost.Code != http.StatusBadRequest {
		t.Fatalf("ghost field status = %d, body=%s", ghost.Code, ghost.Body.String())
	}
	var unchanged models.SystemConfig
	if err := db.First(&unchanged, config.ID).Error; err != nil {
		t.Fatal(err)
	}
	if unchanged.Value != "before" {
		t.Fatalf("unpublished field request mutated config: value=%q", unchanged.Value)
	}

	canonical := performConfigUpdate(
		t,
		router,
		`{"value":"after","value_type":"string","description":"after","category":"system","group":"contract"}`,
	)
	if canonical.Code != http.StatusOK {
		t.Fatalf("canonical status = %d, body=%s", canonical.Code, canonical.Body.String())
	}
	var payload struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(canonical.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	expected := []string{"value", "value_type", "description", "category", "group"}
	if len(payload.Data) != len(expected) {
		t.Fatalf("response data keys = %v, want %v", payload.Data, expected)
	}
	for _, key := range expected {
		if _, ok := payload.Data[key]; !ok {
			t.Errorf("response data omits %q: %v", key, payload.Data)
		}
	}
}

func performConfigUpdate(
	t *testing.T,
	router http.Handler,
	body string,
) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(
		http.MethodPut,
		"/configs/system.contract",
		bytes.NewBufferString(body),
	)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

func performPlatformConfigWrite(
	t *testing.T,
	router http.Handler,
	method string,
	key string,
	body []byte,
	contentType string,
) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(
		method,
		"/api/platform/configs/"+url.PathEscape(key),
		bytes.NewReader(body),
	)
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

func invalidConfigImportBody(t *testing.T, key string) ([]byte, string) {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "configs.json")
	if err != nil {
		t.Fatalf("create import part: %v", err)
	}
	payload, err := json.Marshal([]models.SystemConfig{{
		Key:       key,
		Value:     "value",
		ValueType: "string",
	}})
	if err != nil {
		t.Fatalf("marshal import payload: %v", err)
	}
	if _, err := part.Write(payload); err != nil {
		t.Fatalf("write import payload: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	return body.Bytes(), writer.FormDataContentType()
}
