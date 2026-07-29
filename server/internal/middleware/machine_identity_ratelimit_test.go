package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestMachineIdentityRouteKeyNeverFallsBackToHumanOrIP(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	principalKey := MachineIdentityRouteKeyFunc("agent_principal_id", "service_principal")
	credentialKey := MachineIdentityRouteKeyFunc("agent_credential_id", "credential")
	var observed [][2]string
	router.GET("/a2a/:version", func(c *gin.Context) {
		c.Set("user_id", uint(99))
		if value := c.Query("principal"); value != "" {
			c.Set("agent_principal_id", value)
		}
		if value := c.Query("credential"); value != "" {
			c.Set("agent_credential_id", value)
		}
		adapter := NewGinHTTPContext(c)
		observed = append(observed, [2]string{
			principalKey(adapter),
			credentialKey(adapter),
		})
		c.Status(http.StatusNoContent)
	})

	for _, query := range []string{
		"",
		"?principal=principal-a&credential=credential-a",
		"?principal=principal-a&credential=credential-b",
		"?principal=principal-b&credential=credential-a",
	} {
		request := httptest.NewRequest(http.MethodGet, "/a2a/v1"+query, nil)
		request.RemoteAddr = "192.0.2.44:4321"
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusNoContent {
			t.Fatalf("status=%d", recorder.Code)
		}
	}

	if observed[0][0] != "" || observed[0][1] != "" {
		t.Fatalf("missing machine identity fell back to another bucket: %#v", observed[0])
	}
	if observed[1][0] != observed[2][0] ||
		observed[1][0] == observed[3][0] {
		t.Fatalf("principal buckets are not isolated: %#v", observed)
	}
	if observed[1][1] == observed[2][1] ||
		observed[1][1] != observed[3][1] {
		t.Fatalf("credential buckets are not isolated: %#v", observed)
	}
	for _, pair := range observed[1:] {
		for _, key := range pair {
			if key == "user_99|/a2a/:version" ||
				key == "192.0.2.44|/a2a/:version" {
				t.Fatalf("machine rate key degraded to human/IP bucket: %q", key)
			}
		}
	}
}
