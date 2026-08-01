package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/seaworld008/chronodesk/server/internal/agentplatform"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/services"
)

func TestEmergencyControlHandlerUsesStrictJSONAndStrongETagCAS(
	t *testing.T,
) {
	gin.SetMode(gin.TestMode)
	control := newEmergencyHandlerRuntimeControl(t)
	handler := NewEmergencyControlHandler(control)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("user_id", uint(31))
		c.Set("platform_role", models.PlatformRoleEmergencyOperator)
		c.Next()
	})
	router.GET("/api/platform/emergency-controls", handler.Get)
	router.PUT("/api/platform/emergency-controls", handler.Update)

	getResponse := httptest.NewRecorder()
	router.ServeHTTP(
		getResponse,
		httptest.NewRequest(
			http.MethodGet,
			"/api/platform/emergency-controls",
			nil,
		),
	)
	if getResponse.Code != http.StatusOK {
		t.Fatalf("GET status=%d body=%s", getResponse.Code, getResponse.Body)
	}
	if got := getResponse.Header().Get("ETag"); got != `"v1"` {
		t.Fatalf("GET ETag=%q, want \"v1\"", got)
	}
	if got := getResponse.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("GET Cache-Control=%q", got)
	}
	initial := decodeEmergencyControlSnapshot(t, getResponse)
	if initial.Version != 1 ||
		initial.GlobalReadOnly ||
		initial.EmergencyStop {
		t.Fatalf("GET data=%+v", initial)
	}

	for _, test := range []struct {
		name       string
		body       string
		ifMatch    string
		wantStatus int
	}{
		{
			name:       "missing If-Match",
			body:       `{"global_read_only":true}`,
			wantStatus: http.StatusPreconditionRequired,
		},
		{
			name:       "weak If-Match",
			body:       `{"global_read_only":true}`,
			ifMatch:    `W/"v1"`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "unknown field",
			body:       `{"global_read_only":true,"bypass":true}`,
			ifMatch:    `"v1"`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "empty patch",
			body:       `{}`,
			ifMatch:    `"v1"`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "trailing JSON",
			body:       `{"global_read_only":true}{}`,
			ifMatch:    `"v1"`,
			wantStatus: http.StatusBadRequest,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := emergencyControlRequest(
				router,
				test.body,
				test.ifMatch,
			)
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

	readOnlyResponse := emergencyControlRequest(
		router,
		`{"global_read_only":true}`,
		`"v1"`,
	)
	if readOnlyResponse.Code != http.StatusOK {
		t.Fatalf(
			"read-only PUT status=%d body=%s",
			readOnlyResponse.Code,
			readOnlyResponse.Body,
		)
	}
	if got := readOnlyResponse.Header().Get("ETag"); got != `"v2"` {
		t.Fatalf("read-only PUT ETag=%q", got)
	}
	readOnly := decodeEmergencyControlSnapshot(t, readOnlyResponse)
	if !readOnly.GlobalReadOnly || readOnly.EmergencyStop ||
		readOnly.Version != 2 {
		t.Fatalf("read-only PUT data=%+v", readOnly)
	}

	emergencyResponse := emergencyControlRequest(
		router,
		`{"emergency_stop":true}`,
		`"v2"`,
	)
	if emergencyResponse.Code != http.StatusOK {
		t.Fatalf(
			"emergency PUT status=%d body=%s",
			emergencyResponse.Code,
			emergencyResponse.Body,
		)
	}
	emergency := decodeEmergencyControlSnapshot(t, emergencyResponse)
	if !emergency.GlobalReadOnly || !emergency.EmergencyStop ||
		emergency.Version != 3 {
		t.Fatalf("emergency PUT data=%+v", emergency)
	}

	staleResponse := emergencyControlRequest(
		router,
		`{"emergency_stop":false}`,
		`"v1"`,
	)
	if staleResponse.Code != http.StatusPreconditionFailed {
		t.Fatalf(
			"stale PUT status=%d body=%s",
			staleResponse.Code,
			staleResponse.Body,
		)
	}
	if got := staleResponse.Header().Get("ETag"); got != `"v3"` {
		t.Fatalf("stale PUT current ETag=%q", got)
	}
	var coded struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(staleResponse.Body.Bytes(), &coded); err != nil {
		t.Fatal(err)
	}
	if coded.Code != "version_conflict" {
		t.Fatalf("stale PUT code=%q", coded.Code)
	}
}

func TestEmergencyControlHandlerRequiresHumanActor(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := NewEmergencyControlHandler(newEmergencyHandlerRuntimeControl(t))
	router := gin.New()
	router.PUT("/api/platform/emergency-controls", handler.Update)

	response := emergencyControlRequest(
		router,
		`{"emergency_stop":true}`,
		`"v1"`,
	)
	if response.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", response.Code, response.Body)
	}
}

func newEmergencyHandlerRuntimeControl(
	t *testing.T,
) *agentplatform.RuntimeControl {
	t.Helper()
	dsn := fmt.Sprintf(
		"file:%s?mode=memory&cache=shared",
		strings.ReplaceAll(t.Name(), "/", "_"),
	)
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.SystemConfig{}); err != nil {
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
	return control
}

func emergencyControlRequest(
	router http.Handler,
	body string,
	ifMatch string,
) *httptest.ResponseRecorder {
	response := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPut,
		"/api/platform/emergency-controls",
		strings.NewReader(body),
	)
	request.Header.Set("Content-Type", "application/json")
	if ifMatch != "" {
		request.Header.Set("If-Match", ifMatch)
	}
	router.ServeHTTP(response, request)
	return response
}

func decodeEmergencyControlSnapshot(
	t *testing.T,
	response *httptest.ResponseRecorder,
) agentplatform.RuntimeControlSnapshot {
	t.Helper()
	var envelope struct {
		Data agentplatform.RuntimeControlSnapshot `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode response: %v body=%s", err, response.Body)
	}
	return envelope.Data
}
