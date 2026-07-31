package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/seaworld008/chronodesk/server/internal/models"
)

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
