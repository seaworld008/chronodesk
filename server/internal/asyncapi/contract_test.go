package asyncapi

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"go.yaml.in/yaml/v3"
)

func TestSpecificationDefinesProjectScopedCloudEvents(t *testing.T) {
	var document map[string]any
	if err := yaml.Unmarshal(Specification(), &document); err != nil {
		t.Fatalf("parse AsyncAPI contract: %v", err)
	}
	if got := document["asyncapi"]; got != "3.1.0" {
		t.Fatalf("asyncapi = %v, want 3.1.0", got)
	}
	channels := asyncAPIMap(t, document["channels"], "channels")
	if _, ok := channels["projectEvents"]; !ok {
		t.Fatal("projectEvents channel is missing")
	}
	operations := asyncAPIMap(t, document["operations"], "operations")
	for _, operation := range []string{"publishProjectEvent", "deliverSignedWebhook"} {
		if _, ok := operations[operation]; !ok {
			t.Errorf("operation %s is missing", operation)
		}
	}

	components := asyncAPIMap(t, document["components"], "components")
	schemas := asyncAPIMap(t, components["schemas"], "components.schemas")
	event := asyncAPIMap(t, schemas["projectCloudEvent"], "projectCloudEvent")
	required := asyncAPISlice(t, event["required"], "projectCloudEvent.required")
	for _, field := range []string{
		"organizationid",
		"projectid",
		"actortype",
		"actorid",
		"resourceversion",
	} {
		if !asyncAPIContains(required, field) {
			t.Errorf("project CloudEvent does not require %s", field)
		}
	}
	properties := asyncAPIMap(t, event["properties"], "projectCloudEvent.properties")
	for _, field := range []string{"configurationversion", "policydecisionid"} {
		if _, ok := properties[field]; !ok {
			t.Errorf("project CloudEvent does not expose %s", field)
		}
	}

	headers := asyncAPIMap(t, schemas["webhookHeaders"], "webhookHeaders")
	headerProperties := asyncAPIMap(t, headers["properties"], "webhookHeaders.properties")
	signature := asyncAPIMap(
		t,
		headerProperties["X-ChronoDesk-Signature"],
		"webhook signature",
	)
	if signature["pattern"] != "^v1=[a-f0-9]{64}$" {
		t.Fatalf("webhook signature pattern = %v", signature["pattern"])
	}
}

func TestSpecificationIsSingleYAMLDocument(t *testing.T) {
	decoder := yaml.NewDecoder(bytes.NewReader(Specification()))
	var document yaml.Node
	if err := decoder.Decode(&document); err != nil {
		t.Fatalf("decode AsyncAPI document: %v", err)
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); err != io.EOF {
		t.Fatalf("AsyncAPI must contain one YAML document, got %v", err)
	}
}

func TestRegisterRoutesServesAsyncAPI31(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	RegisterRoutes(router)
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/asyncapi.yaml", nil)
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	if contentType := response.Header().Get("Content-Type"); contentType !=
		"application/vnd.aai.asyncapi+yaml;version=3.1.0" {
		t.Fatalf("Content-Type = %q", contentType)
	}
}

func asyncAPIMap(t *testing.T, value any, location string) map[string]any {
	t.Helper()
	result, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("%s is %T, want object", location, value)
	}
	return result
}

func asyncAPISlice(t *testing.T, value any, location string) []any {
	t.Helper()
	result, ok := value.([]any)
	if !ok {
		t.Fatalf("%s is %T, want array", location, value)
	}
	return result
}

func asyncAPIContains(values []any, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}
