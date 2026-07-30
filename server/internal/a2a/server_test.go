package a2a

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func registerTestServerRoutes(routes gin.IRoutes, server *Server) {
	routes.GET(AgentCardPath, server.CardHandler())
	routes.POST(RPCPath, server.RPCHandler())
}

func TestAgentCardSupportsDiscoveryAndConditionalGET(t *testing.T) {
	server := newTestServer(t, BackendFuncs{})
	router := gin.New()
	registerTestServerRoutes(router, server)

	first := httptest.NewRecorder()
	router.ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/.well-known/agent-card.json", nil))
	if first.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", first.Code, first.Body.String())
	}
	etag := first.Header().Get("ETag")
	if etag == "" {
		t.Fatal("expected ETag")
	}
	lastModified := first.Header().Get("Last-Modified")
	if lastModified == "" {
		t.Fatal("expected Last-Modified")
	}
	var card AgentCard
	if err := json.Unmarshal(first.Body.Bytes(), &card); err != nil {
		t.Fatalf("decode agent card: %v", err)
	}
	if len(card.SupportedInterfaces) != 1 || card.SupportedInterfaces[0].ProtocolVersion != "1.0" {
		t.Fatalf("unexpected supported interfaces: %#v", card.SupportedInterfaces)
	}
	a2aResource := "https://chronodesk.example.com/a2a/v1"
	if card.SupportedInterfaces[0].URL != a2aResource {
		t.Fatalf("A2A resource URL = %q, want %q", card.SupportedInterfaces[0].URL, a2aResource)
	}
	if !card.Capabilities.Streaming || !card.Capabilities.PushNotifications {
		t.Fatalf("expected streaming and push capabilities: %#v", card.Capabilities)
	}
	wantSkills := map[string]bool{
		"ticket-intake":     false,
		"ticket-query":      false,
		"ticket-work":       false,
		"ticket-comment":    false,
		"ticket-escalation": false,
	}
	for _, skill := range card.Skills {
		if _, exists := wantSkills[skill.ID]; exists {
			wantSkills[skill.ID] = true
		}
		if len(skill.InputModes) != 1 || skill.InputModes[0] != "application/json" {
			t.Errorf("skill %q advertises non-structured input modes: %#v", skill.ID, skill.InputModes)
		}
		for _, example := range skill.Examples {
			var payload map[string]any
			if err := json.Unmarshal([]byte(example), &payload); err != nil {
				t.Errorf("skill %q example is not structured JSON: %v", skill.ID, err)
			}
			if payload["skill"] != skill.ID {
				t.Errorf("skill %q example has mismatched skill: %#v", skill.ID, payload["skill"])
			}
			input, ok := payload["input"].(map[string]any)
			if !ok {
				t.Errorf("skill %q example is missing an input object", skill.ID)
				continue
			}
			if skill.ID == "ticket-intake" {
				for _, field := range []string{
					"request_type_version_id",
					"workflow_version_id",
				} {
					value, isString := input[field].(string)
					if !isString || strings.TrimSpace(value) == "" {
						t.Errorf(
							"ticket-intake example is missing %s",
							field,
						)
					}
				}
			}
		}
	}
	for skill, found := range wantSkills {
		if !found {
			t.Errorf("missing skill %q", skill)
		}
	}
	if _, exists := card.SecuritySchemes["oauth2"]; !exists {
		t.Error("missing OAuth2 security scheme")
	}
	if _, exists := card.SecuritySchemes["bearer"]; !exists {
		t.Error("missing bearer security scheme")
	}
	if oauth := card.SecuritySchemes["oauth2"].OAuth2; oauth == nil ||
		!strings.Contains(oauth.Description, a2aResource) {
		t.Fatalf("OAuth2 scheme does not declare exact A2A resource: %#v", oauth)
	}
	if bearer := card.SecuritySchemes["bearer"].HTTPAuth; bearer == nil ||
		!strings.Contains(bearer.Description, a2aResource) {
		t.Fatalf("Bearer scheme does not declare exact A2A audience: %#v", bearer)
	}

	secondRequest := httptest.NewRequest(http.MethodGet, "/.well-known/agent-card.json", nil)
	secondRequest.Header.Set("If-None-Match", etag)
	second := httptest.NewRecorder()
	router.ServeHTTP(second, secondRequest)
	if second.Code != http.StatusNotModified {
		t.Fatalf("expected 304, got %d", second.Code)
	}
	if second.Body.Len() != 0 {
		t.Fatalf("304 response must not have a body: %q", second.Body.String())
	}

	modifiedSinceRequest := httptest.NewRequest(
		http.MethodGet,
		"/.well-known/agent-card.json",
		nil,
	)
	modifiedSinceRequest.Header.Set("If-Modified-Since", lastModified)
	modifiedSince := httptest.NewRecorder()
	router.ServeHTTP(modifiedSince, modifiedSinceRequest)
	if modifiedSince.Code != http.StatusNotModified {
		t.Fatalf(
			"expected If-Modified-Since 304, got %d",
			modifiedSince.Code,
		)
	}
}

func TestJSONRPCTaskLifecycleUsesA2A10Methods(t *testing.T) {
	backend := BackendFuncs{
		ProcessFunc: func(ctx context.Context, _ Task, _ Message, reporter Reporter) error {
			value := `{"summary":"investigation complete"}`
			return reporter.AddArtifact(ctx, Artifact{
				ArtifactID: "ticket-result",
				Name:       "Ticket result",
				Parts:      []Part{{Data: json.RawMessage(value), MediaType: "application/json"}},
			}, false, true, nil)
		},
	}
	server := newTestServer(t, backend)
	router := gin.New()
	registerTestServerRoutes(router, server)

	ticketID := uint(42)
	send := rpcCall(t, router, "SendMessage", map[string]any{
		"message": map[string]any{
			"messageId": "message-1",
			"role":      "ROLE_USER",
			"parts":     []any{map[string]any{"text": "Investigate ticket 42"}},
		},
		"metadata": map[string]any{
			MetadataLinkedTicketID: ticketID,
		},
	})
	if send.Error != nil {
		t.Fatalf("send failed: %#v", send.Error)
	}
	var sendResult SendMessageResult
	decodeResult(t, send.Result, &sendResult)
	if sendResult.Task == nil {
		t.Fatal("expected task response")
	}
	task := *sendResult.Task
	if task.Status.State != TaskStateCompleted {
		t.Fatalf("expected completed, got %s", task.Status.State)
	}
	if len(task.Artifacts) != 1 || task.Artifacts[0].ArtifactID != "ticket-result" {
		t.Fatalf("artifact not retained: %#v", task.Artifacts)
	}
	internalTask, err := server.service.GetTask(context.Background(), GetTaskParams{ID: task.ID})
	if err != nil {
		t.Fatal(err)
	}
	if internalTask.LinkedTicketID == nil || *internalTask.LinkedTicketID != ticketID {
		t.Fatalf("linked ticket was not retained internally: %#v", internalTask.LinkedTicketID)
	}
	assertStates(t, internalTask.StatusHistory,
		TaskStateSubmitted,
		TaskStateWorking,
		TaskStateCompleted,
	)

	get := rpcCall(t, router, "GetTask", map[string]any{
		"id":            task.ID,
		"historyLength": 1,
	})
	if get.Error != nil {
		t.Fatalf("get failed: %#v", get.Error)
	}
	var fetched Task
	decodeResult(t, get.Result, &fetched)
	if len(fetched.History) != 1 {
		t.Fatalf("historyLength not enforced: %d", len(fetched.History))
	}

	list := rpcCall(t, router, "ListTasks", map[string]any{
		"status":           "TASK_STATE_COMPLETED",
		"includeArtifacts": true,
		"pageSize":         10,
	})
	if list.Error != nil {
		t.Fatalf("list failed: %#v", list.Error)
	}
	var listed ListTasksResult
	decodeResult(t, list.Result, &listed)
	if listed.TotalSize != 1 || len(listed.Tasks) != 1 {
		t.Fatalf("unexpected task list: %#v", listed)
	}
	if listed.NextPageToken != "" {
		t.Fatalf("final page must return empty nextPageToken: %q", listed.NextPageToken)
	}

	cancel := rpcCall(t, router, "CancelTask", map[string]any{"id": task.ID})
	if cancel.Error == nil || cancel.Error.Code != -32002 {
		t.Fatalf("expected TaskNotCancelableError, got %#v", cancel.Error)
	}
}

func TestA2A10WireObjectsUseCanonicalClosedFieldSets(t *testing.T) {
	server := newTestServer(t, BackendFuncs{
		ProcessFunc: func(context.Context, Task, Message, Reporter) error {
			return nil
		},
	})
	router := gin.New()
	registerTestServerRoutes(router, server)

	send := rpcCall(t, router, "SendMessage", map[string]any{
		"message": validInboundMessageParams("canonical-wire-task"),
		"metadata": map[string]any{
			MetadataLinkedTicketID: 42,
		},
	})
	if send.Error != nil {
		t.Fatalf("send failed: %#v", send.Error)
	}
	sendObject := decodeJSONObject(t, send.Result)
	assertNoUnexpectedJSONKeys(t, sendObject, "task")
	taskObject := decodeJSONObject(t, sendObject["task"])
	assertNoUnexpectedJSONKeys(t, taskObject,
		"id", "contextId", "status", "artifacts", "history", "metadata",
	)
	statusObject := decodeJSONObject(t, taskObject["status"])
	assertNoUnexpectedJSONKeys(t, statusObject, "state", "message", "timestamp")
	for _, forbidden := range []string{
		"createdAt", "lastModified", "linkedTicketId", "statusHistory",
	} {
		if _, exists := taskObject[forbidden]; exists {
			t.Fatalf("non-standard Task field %q leaked: %s", forbidden, send.Result)
		}
	}

	list := rpcCall(t, router, "ListTasks", map[string]any{"pageSize": 10})
	if list.Error != nil {
		t.Fatalf("list failed: %#v", list.Error)
	}
	assertNoUnexpectedJSONKeys(t, decodeJSONObject(t, list.Result),
		"tasks", "nextPageToken", "pageSize", "totalSize",
	)

	var sent SendMessageResult
	decodeResult(t, send.Result, &sent)
	push := rpcCall(t, router, "CreateTaskPushNotificationConfig", map[string]any{
		"taskId": sent.Task.ID,
		"url":    "https://hooks.example.com/a2a",
	})
	if push.Error != nil {
		t.Fatalf("create push config failed: %#v", push.Error)
	}
	assertNoUnexpectedJSONKeys(t, decodeJSONObject(t, push.Result),
		"tenant", "id", "taskId", "url", "token", "authentication",
	)
	if _, exists := decodeJSONObject(t, push.Result)["createdAt"]; exists {
		t.Fatalf("non-standard push createdAt leaked: %s", push.Result)
	}

	legacyLink := rpcCall(t, router, "SendMessage", map[string]any{
		"message":        validInboundMessageParams("legacy-linked-ticket-field"),
		"linkedTicketId": 42,
	})
	if legacyLink.Error == nil || legacyLink.Error.Code != -32602 {
		t.Fatalf("legacy linkedTicketId was accepted: %#v", legacyLink.Error)
	}
	legacyPush := rpcCall(t, router, "CreateTaskPushNotificationConfig", map[string]any{
		"taskId": sent.Task.ID,
		"config": map[string]any{
			"url": "https://hooks.example.com/a2a",
		},
	})
	if legacyPush.Error == nil || legacyPush.Error.Code != -32602 {
		t.Fatalf("legacy nested push config was accepted: %#v", legacyPush.Error)
	}
}

func TestJSONRPCRejectsNonCanonicalParameterFieldsBeforeDispatch(t *testing.T) {
	var backendCalls int
	server := newTestServer(t, BackendFuncs{
		ProcessFunc: func(context.Context, Task, Message, Reporter) error {
			backendCalls++
			return nil
		},
	})
	router := gin.New()
	registerTestServerRoutes(router, server)

	tests := []struct {
		name   string
		method string
		params map[string]any
	}{
		{
			name:   "top-level configuration case variant",
			method: "SendMessage",
			params: map[string]any{
				"message": validInboundMessageParams("strict-config"),
				"Configuration": map[string]any{
					"taskPushNotificationConfig": map[string]any{
						"url": "https://events.example.test/a2a",
					},
				},
			},
		},
		{
			name:   "nested push case variant",
			method: "SendMessage",
			params: map[string]any{
				"message": validInboundMessageParams("strict-nested-push"),
				"configuration": map[string]any{
					"TaskPushNotificationConfig": map[string]any{
						"url": "https://events.example.test/a2a",
					},
				},
			},
		},
		{
			name:   "resource ID case variant",
			method: "GetTask",
			params: map[string]any{"ID": "task-secret"},
		},
		{
			name:   "task ID case variant",
			method: "GetTaskPushNotificationConfig",
			params: map[string]any{
				"TaskId": "task-secret",
				"id":     "push-1",
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := rpcCall(t, router, test.method, test.params)
			if response.Error == nil || response.Error.Code != -32602 {
				t.Fatalf("expected invalid params, got %#v", response.Error)
			}
		})
	}
	if backendCalls != 0 {
		t.Fatalf("strictly invalid requests reached backend %d times", backendCalls)
	}

	wrongMethodCase := rpcCall(t, router, "sendmessage", map[string]any{
		"message": validInboundMessageParams("strict-method"),
	})
	if wrongMethodCase.Error == nil || wrongMethodCase.Error.Code != -32601 {
		t.Fatalf("expected method not found, got %#v", wrongMethodCase.Error)
	}
	if backendCalls != 0 {
		t.Fatalf("case-variant method reached backend %d times", backendCalls)
	}
}

func TestA2ASendMethodsRequireAuthenticatedProjectMetadataAndIgnoreTenant(t *testing.T) {
	var backendCalls int
	server := newTestServer(t, BackendFuncs{
		ProcessFunc: func(context.Context, Task, Message, Reporter) error {
			backendCalls++
			return nil
		},
	})
	router := gin.New()
	registerTestServerRoutes(router, server)

	tests := []struct {
		name   string
		method string
		params map[string]any
		reason string
	}{
		{
			name:   "send missing project metadata",
			method: "SendMessage",
			params: map[string]any{
				"message": validInboundMessageParams("scope-missing"),
			},
			reason: "PROJECT_SCOPE_REQUIRED",
		},
		{
			name:   "send mismatched project metadata",
			method: "SendMessage",
			params: map[string]any{
				"message": validInboundMessageParams("scope-mismatch"),
				"metadata": map[string]any{
					MetadataProjectKey: "OTHER",
				},
			},
			reason: "PROJECT_SCOPE_MISMATCH",
		},
		{
			name:   "stream mismatched project metadata",
			method: "SendStreamingMessage",
			params: map[string]any{
				"message": validInboundMessageParams("stream-scope-mismatch"),
				"metadata": map[string]any{
					MetadataProjectKey: "OTHER",
				},
			},
			reason: "PROJECT_SCOPE_MISMATCH",
		},
		{
			name:   "nested message metadata cannot override project",
			method: "SendMessage",
			params: map[string]any{
				"message": map[string]any{
					"messageId": "nested-scope-mismatch",
					"role":      "ROLE_USER",
					"parts":     []any{map[string]any{"text": "strict request"}},
					"metadata": map[string]any{
						MetadataProjectKey: "OTHER",
					},
				},
				"metadata": map[string]any{
					MetadataProjectKey: a2aTestProjectKey,
				},
			},
			reason: "PROJECT_SCOPE_MISMATCH",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body := rawRPCBody(t, test.method, test.params)
			request := httptest.NewRequest(http.MethodPost, RPCPath, bytes.NewReader(body))
			request = request.WithContext(a2aTestContext(t))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("A2A-Version", ProtocolVersion)
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, request)
			if recorder.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
			var response rpcEnvelope
			if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
				t.Fatalf("decode project-scope error: %v (%s)", err, recorder.Body.String())
			}
			if response.Error == nil || response.Error.Code != -32602 {
				t.Fatalf("project-scope error=%#v", response.Error)
			}
			if got := rpcErrorReason(t, response.Error); got != test.reason {
				t.Fatalf("project-scope reason=%q, want %q", got, test.reason)
			}
		})
	}
	if backendCalls != 0 {
		t.Fatalf("invalid project assertions reached backend %d times", backendCalls)
	}

	// tenant is an opaque A2A compatibility field. It cannot select or
	// override the project already bound by OAuth.
	accepted := rpcCall(t, router, "SendMessage", map[string]any{
		"tenant":  "OTHER",
		"message": validInboundMessageParams("tenant-is-not-project"),
		"metadata": map[string]any{
			MetadataProjectKey: a2aTestProjectKey,
		},
	})
	if accepted.Error != nil {
		t.Fatalf("opaque tenant was treated as project scope: %#v", accepted.Error)
	}
	if backendCalls != 1 {
		t.Fatalf("valid authenticated project reached backend %d times, want 1", backendCalls)
	}
}

func TestJSONRPCIgnoresForwardCompatibleUnknownFieldsWithoutReflectingThem(t *testing.T) {
	var backendCalls int
	server := newTestServer(t, BackendFuncs{
		ProcessFunc: func(context.Context, Task, Message, Reporter) error {
			backendCalls++
			return nil
		},
	})
	router := gin.New()
	registerTestServerRoutes(router, server)

	response := rpcCall(t, router, "SendMessage", map[string]any{
		"message": map[string]any{
			"messageId":       "forward-compatible-fields",
			"role":            "ROLE_USER",
			"parts":           []any{map[string]any{"text": "work", "futurePartField": true}},
			"tckUnknownField": "ignored",
		},
		"tckExtraParam": 42,
	})
	if response.Error != nil {
		t.Fatalf("unrecognized forward-compatible fields were rejected: %#v", response.Error)
	}
	if backendCalls != 1 {
		t.Fatalf("backend calls=%d, want 1", backendCalls)
	}
	for _, forbidden := range []string{
		"tckUnknownField", "tckExtraParam", "futurePartField",
	} {
		if strings.Contains(string(response.Result), forbidden) {
			t.Fatalf("unknown field %q was reflected: %s", forbidden, response.Result)
		}
	}
}

func validInboundMessageParams(messageID string) map[string]any {
	return map[string]any{
		"messageId": messageID,
		"role":      "ROLE_USER",
		"parts": []any{
			map[string]any{"text": "strict request"},
		},
	}
}

func TestInputRequiredIsAnA2AStateOnly(t *testing.T) {
	backend := BackendFuncs{
		ProcessFunc: func(ctx context.Context, _ Task, _ Message, reporter Reporter) error {
			prompt := "Please provide the affected service name."
			return reporter.SetStatus(ctx, TaskStateInputRequired, &Message{
				Role:  RoleAgent,
				Parts: []Part{{Text: &prompt}},
			}, map[string]any{"reason": "MISSING_SERVICE"})
		},
	}
	server := newTestServer(t, backend)
	router := gin.New()
	registerTestServerRoutes(router, server)

	send := rpcCall(t, router, "SendMessage", map[string]any{
		"message": map[string]any{
			"messageId": "message-input",
			"role":      "ROLE_USER",
			"parts":     []any{map[string]any{"text": "The service is down"}},
		},
		"metadata": map[string]any{
			MetadataLinkedTicketID: 77,
		},
	})
	if send.Error != nil {
		t.Fatalf("send failed: %#v", send.Error)
	}
	var result SendMessageResult
	decodeResult(t, send.Result, &result)
	if result.Task.Status.State != TaskStateInputRequired {
		t.Fatalf("expected input-required, got %s", result.Task.Status.State)
	}
	internalTask, err := server.service.GetTask(
		context.Background(),
		GetTaskParams{ID: result.Task.ID},
	)
	if err != nil {
		t.Fatal(err)
	}
	if internalTask.LinkedTicketID == nil || *internalTask.LinkedTicketID != 77 {
		t.Fatalf("linked Ticket relation changed: %#v", internalTask.LinkedTicketID)
	}
	// No Ticket service is part of the Backend/Reporter contract, so this state
	// transition cannot implicitly set the business Ticket to pending.
	assertStates(t, internalTask.StatusHistory,
		TaskStateSubmitted,
		TaskStateWorking,
		TaskStateInputRequired,
	)
}

func TestStreamingContinuationResubmitsAndIgnoresHistoricalInterruptedEvent(t *testing.T) {
	var executions int
	backend := BackendFuncs{
		ProcessFunc: func(ctx context.Context, _ Task, _ Message, reporter Reporter) error {
			executions++
			if executions == 1 {
				return reporter.SetStatus(
					ctx,
					TaskStateInputRequired,
					textMessage("provide follow-up"),
					nil,
				)
			}
			return nil
		},
	}
	server := newTestServer(t, backend)
	router := gin.New()
	registerTestServerRoutes(router, server)

	first := rpcCall(t, router, "SendMessage", map[string]any{
		"message": map[string]any{
			"messageId": "continuation-initial",
			"role":      "ROLE_USER",
			"parts":     []any{map[string]any{"text": "initial request"}},
		},
	})
	if first.Error != nil {
		t.Fatalf("initial send failed: %#v", first.Error)
	}
	var initial SendMessageResult
	decodeResult(t, first.Result, &initial)
	if initial.Task.Status.State != TaskStateInputRequired {
		t.Fatalf("initial state=%s", initial.Task.Status.State)
	}

	body := rpcBody(t, "SendStreamingMessage", map[string]any{
		"message": map[string]any{
			"messageId": "continuation-follow-up",
			"taskId":    initial.Task.ID,
			"contextId": initial.Task.ContextID,
			"role":      "ROLE_USER",
			"parts":     []any{map[string]any{"text": "follow-up details"}},
		},
	})
	request := httptest.NewRequest(http.MethodPost, "/a2a/v1", bytes.NewReader(body))
	request = request.WithContext(a2aTestContext(t))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("A2A-Version", ProtocolVersion)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("continuation stream failed: %d %s", recorder.Code, recorder.Body.String())
	}
	events := parseSSE(t, recorder.Body.String())
	if !containsTerminalEvent(t, events) {
		t.Fatalf(
			"historical input-required event terminated continuation stream: %s",
			recorder.Body.String(),
		)
	}

	current, err := server.service.GetTask(
		context.Background(),
		GetTaskParams{ID: initial.Task.ID},
	)
	if err != nil {
		t.Fatal(err)
	}
	assertStates(t, current.StatusHistory,
		TaskStateSubmitted,
		TaskStateWorking,
		TaskStateInputRequired,
		TaskStateSubmitted,
		TaskStateWorking,
		TaskStateCompleted,
	)
}

func TestStreamingReplayUsesLastEventID(t *testing.T) {
	backend := BackendFuncs{
		ProcessFunc: func(ctx context.Context, _ Task, _ Message, reporter Reporter) error {
			text := "partial result"
			if err := reporter.AddArtifact(ctx, Artifact{
				ArtifactID: "stream-result",
				Parts:      []Part{{Text: &text}},
			}, false, true, nil); err != nil {
				return err
			}
			return reporter.SetStatus(
				ctx,
				TaskStateInputRequired,
				textMessage("provide follow-up"),
				nil,
			)
		},
	}
	server := newTestServer(t, backend)
	router := gin.New()
	registerTestServerRoutes(router, server)

	streamBody := rpcBody(t, "SendStreamingMessage", map[string]any{
		"message": map[string]any{
			"messageId": "stream-message",
			"role":      "ROLE_USER",
			"parts":     []any{map[string]any{"text": "stream this task"}},
		},
	})
	streamRequest := httptest.NewRequest(http.MethodPost, "/a2a/v1", bytes.NewReader(streamBody))
	streamRequest = streamRequest.WithContext(a2aTestContext(t))
	streamRequest.Header.Set("Content-Type", "application/json")
	streamRequest.Header.Set("A2A-Version", ProtocolVersion)
	streamRecorder := httptest.NewRecorder()
	router.ServeHTTP(streamRecorder, streamRequest)
	if streamRecorder.Code != http.StatusOK {
		t.Fatalf("stream failed: %d %s", streamRecorder.Code, streamRecorder.Body.String())
	}
	if !strings.HasPrefix(streamRecorder.Header().Get("Content-Type"), "text/event-stream") {
		t.Fatalf("unexpected content type: %s", streamRecorder.Header().Get("Content-Type"))
	}
	events := parseSSE(t, streamRecorder.Body.String())
	if len(events) < 4 {
		t.Fatalf("expected task, working, artifact, input-required events, got %d: %s", len(events), streamRecorder.Body.String())
	}
	firstCursor := events[0].ID
	if firstCursor == "" {
		t.Fatal("stream event is missing cursor")
	}
	taskID := taskIDFromEvents(t, events)

	resubscribeBody := rpcBody(t, "SubscribeToTask", map[string]any{"id": taskID})
	resubscribeRequest := httptest.NewRequest(http.MethodPost, "/a2a/v1", bytes.NewReader(resubscribeBody))
	resubscribeRequest.Header.Set("Content-Type", "application/json")
	resubscribeRequest.Header.Set("A2A-Version", ProtocolVersion)
	resubscribeRequest.Header.Set("Last-Event-ID", firstCursor)
	resubscribeRecorder := httptest.NewRecorder()
	router.ServeHTTP(resubscribeRecorder, resubscribeRequest)
	replayed := parseSSE(t, resubscribeRecorder.Body.String())
	if len(replayed) < 2 {
		t.Fatalf("expected snapshot plus replayed events: %s", resubscribeRecorder.Body.String())
	}
	if replayed[0].ID != "" {
		t.Fatalf("current task snapshot must not advance replay cursor: %q", replayed[0].ID)
	}
	for _, event := range replayed[1:] {
		if event.ID == firstCursor {
			t.Fatalf("Last-Event-ID event was replayed again: %q", firstCursor)
		}
	}
	if !containsInterruptedEvent(t, replayed) {
		t.Fatal("replay did not recover interrupted event")
	}
}

func TestStreamingIdempotentReplayOfTerminalTaskReturnsSnapshot(t *testing.T) {
	var executions int
	server := newTestServer(t, BackendFuncs{
		ProcessFunc: func(context.Context, Task, Message, Reporter) error {
			executions++
			return nil
		},
	})
	router := gin.New()
	registerTestServerRoutes(router, server)

	params := map[string]any{
		"message": validInboundMessageParams("terminal-stream-replay"),
	}
	first := rpcCall(t, router, "SendMessage", params)
	if first.Error != nil {
		t.Fatalf("initial send failed: %#v", first.Error)
	}
	var sent SendMessageResult
	decodeResult(t, first.Result, &sent)
	if sent.Task == nil || !sent.Task.Status.State.IsTerminal() {
		t.Fatalf("expected terminal Task, got %#v", sent.Task)
	}

	body := rpcBody(t, "SendStreamingMessage", params)
	request := httptest.NewRequest(
		http.MethodPost,
		RPCPath,
		bytes.NewReader(body),
	)
	request = request.WithContext(a2aTestContext(t))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("A2A-Version", ProtocolVersion)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK ||
		!strings.HasPrefix(
			recorder.Header().Get("Content-Type"),
			"text/event-stream",
		) {
		t.Fatalf(
			"terminal replay did not return an SSE stream: %d %s",
			recorder.Code,
			recorder.Body.String(),
		)
	}
	events := parseSSE(t, recorder.Body.String())
	if len(events) != 1 {
		t.Fatalf(
			"terminal replay should return one current Task snapshot: %s",
			recorder.Body.String(),
		)
	}
	payload, err := json.Marshal(events[0].Response.Result)
	if err != nil {
		t.Fatal(err)
	}
	var stream StreamResponse
	if err := json.Unmarshal(payload, &stream); err != nil {
		t.Fatal(err)
	}
	if stream.Task == nil ||
		stream.Task.ID != sent.Task.ID ||
		!stream.Task.Status.State.IsTerminal() {
		t.Fatalf("terminal replay snapshot = %#v", stream)
	}
	if executions != 1 {
		t.Fatalf("idempotent stream replay re-executed backend %d times", executions)
	}
}

func TestSubscribeToTerminalTaskReturnsUnsupportedOperation(t *testing.T) {
	server := newTestServer(t, BackendFuncs{
		ProcessFunc: func(context.Context, Task, Message, Reporter) error {
			return nil
		},
	})
	router := gin.New()
	registerTestServerRoutes(router, server)

	send := rpcCall(t, router, "SendMessage", map[string]any{
		"message": validInboundMessageParams("terminal-subscribe"),
	})
	if send.Error != nil {
		t.Fatalf("send failed: %#v", send.Error)
	}
	var sent SendMessageResult
	decodeResult(t, send.Result, &sent)
	if sent.Task == nil || !sent.Task.Status.State.IsTerminal() {
		t.Fatalf("expected terminal task, got %#v", sent.Task)
	}

	subscribe := rpcCall(t, router, "SubscribeToTask", map[string]any{
		"id": sent.Task.ID,
	})
	if subscribe.Error == nil || subscribe.Error.Code != -32004 {
		t.Fatalf("expected UnsupportedOperationError, got %#v", subscribe.Error)
	}
}

func TestPushNotificationConfigurationIsValidatedAndRedacted(t *testing.T) {
	server := newTestServer(t, BackendFuncs{})
	router := gin.New()
	registerTestServerRoutes(router, server)

	send := rpcCall(t, router, "SendMessage", map[string]any{
		"message": map[string]any{
			"messageId": "push-task",
			"role":      "ROLE_USER",
			"parts":     []any{map[string]any{"text": "wait for input"}},
		},
	})
	var sent SendMessageResult
	decodeResult(t, send.Result, &sent)

	create := rpcCall(t, router, "CreateTaskPushNotificationConfig", map[string]any{
		"taskId": sent.Task.ID,
		"url":    "https://hooks.example.com/a2a",
		"token":  "opaque-correlation-token",
		"authentication": map[string]any{
			"scheme":      "Bearer",
			"credentials": "secret",
		},
	})
	if create.Error != nil {
		t.Fatalf("create push config failed: %#v", create.Error)
	}
	var config PushNotificationConfig
	decodeResult(t, create.Result, &config)
	if config.ID == "" {
		t.Fatal("expected generated push configuration ID")
	}
	if config.Token != "" || (config.Authentication != nil && config.Authentication.Credentials != "") {
		t.Fatalf("push secrets were returned: %#v", config)
	}

	get := rpcCall(t, router, "GetTaskPushNotificationConfig", map[string]any{
		"taskId": sent.Task.ID,
		"id":     config.ID,
	})
	if get.Error != nil {
		t.Fatalf("get push config failed: %#v", get.Error)
	}
	var fetched PushNotificationConfig
	decodeResult(t, get.Result, &fetched)
	if fetched.Token != "" || (fetched.Authentication != nil && fetched.Authentication.Credentials != "") {
		t.Fatalf("stored push secrets were returned: %#v", fetched)
	}

	invalid := rpcCall(t, router, "CreateTaskPushNotificationConfig", map[string]any{
		"taskId": sent.Task.ID,
		"url":    "https://127.0.0.1/callback",
	})
	if invalid.Error == nil || invalid.Error.Code != -32602 {
		t.Fatalf("expected SSRF-safe validation error, got %#v", invalid.Error)
	}
}

func TestTaskTransitionMatrix(t *testing.T) {
	if !canTransition(TaskStateSubmitted, TaskStateWorking) {
		t.Error("submitted -> working should be allowed")
	}
	if !canTransition(TaskStateWorking, TaskStateInputRequired) {
		t.Error("working -> input-required should be allowed")
	}
	if !canTransition(TaskStateInputRequired, TaskStateWorking) {
		t.Error("input-required -> working should be allowed")
	}
	if canTransition(TaskStateCompleted, TaskStateWorking) {
		t.Error("terminal tasks must not restart")
	}
	if canTransition(TaskStateFailed, TaskStateCanceled) {
		t.Error("terminal task state must be immutable")
	}
}

func TestJSONRPCValidationAndVersionErrors(t *testing.T) {
	server := newTestServer(t, BackendFuncs{})
	router := gin.New()
	registerTestServerRoutes(router, server)

	malformedRequest := httptest.NewRequest(http.MethodPost, RPCPath, strings.NewReader(`{"jsonrpc":`))
	malformedRequest.Header.Set("Content-Type", "application/json")
	malformedRequest.Header.Set("A2A-Version", ProtocolVersion)
	malformedRecorder := httptest.NewRecorder()
	router.ServeHTTP(malformedRecorder, malformedRequest)
	if malformedRecorder.Code != http.StatusBadRequest {
		t.Fatalf("expected malformed request to return 400, got %d", malformedRecorder.Code)
	}
	var malformed rpcEnvelope
	if err := json.Unmarshal(malformedRecorder.Body.Bytes(), &malformed); err != nil {
		t.Fatalf("decode malformed response: %v", err)
	}
	if malformed.Error == nil || malformed.Error.Code != -32700 {
		t.Fatalf("expected JSON parse error, got %#v", malformed.Error)
	}

	unknown := rpcCall(t, router, "UnknownOperation", map[string]any{})
	if unknown.Error == nil || unknown.Error.Code != -32601 {
		t.Fatalf("expected method-not-found, got %#v", unknown.Error)
	}

	for _, legacyMethod := range []string{
		"message/send",
		"message/stream",
		"tasks/get",
		"tasks/list",
		"tasks/cancel",
		"tasks/resubscribe",
		"tasks/pushNotificationConfig/set",
		"tasks/pushNotificationConfig/get",
		"tasks/pushNotificationConfig/list",
		"tasks/pushNotificationConfig/delete",
	} {
		t.Run("reject legacy method "+legacyMethod, func(t *testing.T) {
			response := rpcCall(t, router, legacyMethod, map[string]any{})
			if response.Error == nil || response.Error.Code != -32601 {
				t.Fatalf("expected method-not-found, got %#v", response.Error)
			}
		})
	}

	for _, versionHeader := range []string{"", "0.3", "1.0.1", "2.0"} {
		t.Run("reject version "+versionHeader, func(t *testing.T) {
			versionRequest := httptest.NewRequest(http.MethodPost, RPCPath, bytes.NewReader(rpcBody(t, "ListTasks", map[string]any{})))
			versionRequest.Header.Set("Content-Type", "application/json")
			if versionHeader != "" {
				versionRequest.Header.Set("A2A-Version", versionHeader)
			}
			versionRecorder := httptest.NewRecorder()
			router.ServeHTTP(versionRecorder, versionRequest)
			var version rpcEnvelope
			if err := json.Unmarshal(versionRecorder.Body.Bytes(), &version); err != nil {
				t.Fatalf("decode version response: %v", err)
			}
			if version.Error == nil || version.Error.Code != -32009 {
				t.Fatalf("expected version-not-supported, got %#v", version.Error)
			}
		})
	}

	legacyRole := rpcCall(t, router, "SendMessage", map[string]any{
		"message": map[string]any{
			"messageId": "legacy-role",
			"role":      "user",
			"parts":     []any{map[string]any{"text": "legacy role"}},
		},
	})
	if legacyRole.Error == nil || legacyRole.Error.Code != -32602 {
		t.Fatalf("expected invalid-params for legacy role, got %#v", legacyRole.Error)
	}

	legacyState := rpcCall(t, router, "ListTasks", map[string]any{"status": "completed"})
	if legacyState.Error == nil || legacyState.Error.Code != -32602 {
		t.Fatalf("expected invalid-params for legacy task state, got %#v", legacyState.Error)
	}

	legacyMediaTypeRequest := httptest.NewRequest(
		http.MethodPost,
		RPCPath,
		bytes.NewReader(rpcBody(t, "ListTasks", map[string]any{})),
	)
	legacyMediaTypeRequest.Header.Set("Content-Type", "application/a2a+json")
	legacyMediaTypeRequest.Header.Set("A2A-Version", ProtocolVersion)
	legacyMediaTypeRecorder := httptest.NewRecorder()
	router.ServeHTTP(legacyMediaTypeRecorder, legacyMediaTypeRequest)
	if legacyMediaTypeRecorder.Code != http.StatusUnsupportedMediaType {
		t.Fatalf(
			"expected legacy media type to return 415, got %d: %s",
			legacyMediaTypeRecorder.Code,
			legacyMediaTypeRecorder.Body.String(),
		)
	}
	var unsupportedMediaType rpcEnvelope
	if err := json.Unmarshal(
		legacyMediaTypeRecorder.Body.Bytes(),
		&unsupportedMediaType,
	); err != nil {
		t.Fatalf("decode unsupported media type response: %v", err)
	}
	if unsupportedMediaType.Error == nil ||
		unsupportedMediaType.Error.Code != -32005 {
		t.Fatalf(
			"expected ContentTypeNotSupportedError, got %#v",
			unsupportedMediaType.Error,
		)
	}
	if len(unsupportedMediaType.Error.Data) != 1 {
		t.Fatalf(
			"expected ContentTypeNotSupportedError ErrorInfo, got %#v",
			unsupportedMediaType.Error.Data,
		)
	}
	errorInfo, ok := unsupportedMediaType.Error.Data[0].(map[string]any)
	if !ok ||
		errorInfo["@type"] != "type.googleapis.com/google.rpc.ErrorInfo" ||
		errorInfo["reason"] != "CONTENT_TYPE_NOT_SUPPORTED" ||
		errorInfo["domain"] != "a2a-protocol.org" {
		t.Fatalf(
			"invalid ContentTypeNotSupportedError ErrorInfo: %#v",
			unsupportedMediaType.Error.Data,
		)
	}

	invalidPart := rpcCall(t, router, "SendMessage", map[string]any{
		"message": map[string]any{
			"messageId": "invalid-part",
			"role":      "ROLE_USER",
			"parts": []any{map[string]any{
				"text": "one",
				"data": map[string]any{"two": true},
			}},
		},
	})
	if invalidPart.Error == nil || invalidPart.Error.Code != -32602 {
		t.Fatalf("expected invalid-params for non-discriminated Part, got %#v", invalidPart.Error)
	}
}

func TestA2AVersionQueryParameterAndConflict(t *testing.T) {
	server := newTestServer(t, BackendFuncs{})
	router := gin.New()
	registerTestServerRoutes(router, server)

	queryRequest := httptest.NewRequest(
		http.MethodPost,
		RPCPath+"?A2A-Version="+ProtocolVersion,
		bytes.NewReader(rpcBody(t, "ListTasks", map[string]any{})),
	)
	queryRequest.Header.Set("Content-Type", "application/json")
	queryRecorder := httptest.NewRecorder()
	router.ServeHTTP(queryRecorder, queryRequest)
	var queryResponse rpcEnvelope
	if err := json.Unmarshal(queryRecorder.Body.Bytes(), &queryResponse); err != nil {
		t.Fatalf("decode query-version response: %v", err)
	}
	if queryResponse.Error != nil {
		t.Fatalf("query A2A-Version was rejected: %#v", queryResponse.Error)
	}

	conflictRequest := httptest.NewRequest(
		http.MethodPost,
		RPCPath+"?A2A-Version=2.0",
		bytes.NewReader(rpcBody(t, "ListTasks", map[string]any{})),
	)
	conflictRequest.Header.Set("Content-Type", "application/json")
	conflictRequest.Header.Set("A2A-Version", ProtocolVersion)
	conflictRecorder := httptest.NewRecorder()
	router.ServeHTTP(conflictRecorder, conflictRequest)
	var conflictResponse rpcEnvelope
	if err := json.Unmarshal(conflictRecorder.Body.Bytes(), &conflictResponse); err != nil {
		t.Fatalf("decode conflicting-version response: %v", err)
	}
	if conflictResponse.Error == nil || conflictResponse.Error.Code != -32009 {
		t.Fatalf("expected VersionNotSupportedError, got %#v", conflictResponse.Error)
	}
}

func TestGetExtendedAgentCardUsesCanonicalErrors(t *testing.T) {
	t.Run("capability not declared", func(t *testing.T) {
		server := newTestServer(t, BackendFuncs{})
		router := gin.New()
		registerTestServerRoutes(router, server)
		response := rpcCall(t, router, "GetExtendedAgentCard", map[string]any{})
		if response.Error == nil || response.Error.Code != -32004 {
			t.Fatalf("expected UnsupportedOperationError, got %#v", response.Error)
		}
	})

	t.Run("declared but not configured", func(t *testing.T) {
		card := DefaultAgentCard(CardOptions{
			BaseURL: "https://chronodesk.example.com",
		})
		card.Capabilities.ExtendedAgentCard = true
		server, err := NewServer(NewMemoryStore(), BackendFuncs{}, ServerOptions{
			Card: &card,
		})
		if err != nil {
			t.Fatal(err)
		}
		router := gin.New()
		registerTestServerRoutes(router, server)
		response := rpcCall(t, router, "GetExtendedAgentCard", map[string]any{})
		if response.Error == nil || response.Error.Code != -32007 {
			t.Fatalf(
				"expected ExtendedAgentCardNotConfiguredError, got %#v",
				response.Error,
			)
		}
	})
}

func TestUnavailablePushCapabilityUsesCanonicalError(t *testing.T) {
	server, err := NewServer(
		NewMemoryStore(),
		BackendFuncs{},
		ServerOptions{
			CardOptions: CardOptions{
				BaseURL: "https://chronodesk.example.com",
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	registerTestServerRoutes(router, server)

	response := rpcCall(
		t,
		router,
		"CreateTaskPushNotificationConfig",
		map[string]any{
			"taskId": "task-without-push",
			"url":    "https://events.example.test/a2a",
		},
	)
	if response.Error == nil || response.Error.Code != -32003 {
		t.Fatalf(
			"expected PushNotificationNotSupportedError, got %#v",
			response.Error,
		)
	}
	if len(response.Error.Data) != 1 {
		t.Fatalf(
			"expected canonical ErrorInfo, got %#v",
			response.Error.Data,
		)
	}
}

func TestUnsupportedMessageAndOutputMediaTypesUseCanonicalError(t *testing.T) {
	server := newTestServer(t, BackendFuncs{})
	router := gin.New()
	registerTestServerRoutes(router, server)

	tests := []struct {
		name   string
		params map[string]any
	}{
		{
			name: "message part",
			params: map[string]any{
				"message": map[string]any{
					"messageId": "unsupported-input-media",
					"role":      "ROLE_USER",
					"parts": []any{map[string]any{
						"text":      "binary input is not supported",
						"mediaType": "image/png",
					}},
				},
			},
		},
		{
			name: "accepted output modes",
			params: map[string]any{
				"message": map[string]any{
					"messageId": "unsupported-output-media",
					"role":      "ROLE_USER",
					"parts": []any{map[string]any{
						"text":      "request unsupported output",
						"mediaType": "text/plain; charset=utf-8",
					}},
				},
				"configuration": map[string]any{
					"acceptedOutputModes": []string{"image/png"},
				},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := rpcCall(t, router, "SendMessage", test.params)
			if response.Error == nil || response.Error.Code != -32005 {
				t.Fatalf(
					"expected ContentTypeNotSupportedError, got %#v",
					response.Error,
				)
			}
			if len(response.Error.Data) != 1 {
				t.Fatalf(
					"expected canonical ErrorInfo, got %#v",
					response.Error.Data,
				)
			}
		})
	}

	list := rpcCall(t, router, "ListTasks", map[string]any{})
	if list.Error != nil {
		t.Fatalf("list tasks: %#v", list.Error)
	}
	var listed ListTasksResult
	decodeResult(t, list.Result, &listed)
	if listed.TotalSize != 0 || len(listed.Tasks) != 0 {
		t.Fatalf("unsupported media type created a Task: %#v", listed)
	}
}

func TestCancelStopsRunningBackendWithoutOverwritingCanceledState(t *testing.T) {
	started := make(chan struct{})
	finished := make(chan struct{})
	var once sync.Once
	server := newTestServer(t, BackendFuncs{
		ProcessFunc: func(ctx context.Context, _ Task, _ Message, _ Reporter) error {
			once.Do(func() { close(started) })
			<-ctx.Done()
			close(finished)
			return ctx.Err()
		},
	})
	text := "long-running work"
	task, err := server.Service().SendMessage(context.Background(), SendMessageParams{
		Message: Message{
			MessageID: "cancel-message",
			Role:      RoleUser,
			Parts:     []Part{{Text: &text}},
		},
		Configuration: SendMessageConfiguration{ReturnImmediately: true},
	})
	if err != nil {
		t.Fatalf("start async task: %v", err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("backend did not start")
	}
	canceled, err := server.Service().CancelTask(context.Background(), task.ID)
	if err != nil {
		t.Fatalf("cancel task: %v", err)
	}
	if canceled.Status.State != TaskStateCanceled {
		t.Fatalf("expected canceled state, got %s", canceled.Status.State)
	}
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("backend did not stop after cancellation")
	}
	current, err := server.Service().GetTask(context.Background(), GetTaskParams{ID: task.ID})
	if err != nil {
		t.Fatalf("get canceled task: %v", err)
	}
	if current.Status.State != TaskStateCanceled {
		t.Fatalf("backend completion overwrote cancellation: %s", current.Status.State)
	}
}

func TestAsyncExecutionKeepsTrustedContextValuesAfterRequestCancellation(t *testing.T) {
	type identityKey struct{}
	observed := make(chan string, 1)
	server := newTestServer(t, BackendFuncs{
		ProcessFunc: func(ctx context.Context, _ Task, _ Message, _ Reporter) error {
			value, _ := ctx.Value(identityKey{}).(string)
			observed <- value
			return nil
		},
	})
	requestContext, cancel := context.WithCancel(context.WithValue(
		context.Background(),
		identityKey{},
		"verified-service-principal",
	))
	text := "run after response"
	if _, err := server.Service().SendMessage(requestContext, SendMessageParams{
		Message: Message{
			MessageID: "context-snapshot-message",
			Role:      RoleUser,
			Parts:     []Part{{Text: &text}},
		},
		Configuration: SendMessageConfiguration{ReturnImmediately: true},
	}); err != nil {
		t.Fatalf("start async execution: %v", err)
	}
	cancel()
	select {
	case value := <-observed:
		if value != "verified-service-principal" {
			t.Fatalf("trusted identity snapshot was lost: %q", value)
		}
	case <-time.After(time.Second):
		t.Fatal("async backend did not run after request cancellation")
	}
}

func newTestServer(t *testing.T, backend Backend) *Server {
	t.Helper()
	gin.SetMode(gin.TestMode)
	var mu sync.Mutex
	nextID := 0
	id := func() string {
		mu.Lock()
		defer mu.Unlock()
		nextID++
		return "00000000-0000-4000-8000-" + leftPad(nextID, 12)
	}
	baseTime := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	var timeMu sync.Mutex
	nextTime := 0
	now := func() time.Time {
		timeMu.Lock()
		defer timeMu.Unlock()
		nextTime++
		return baseTime.Add(time.Duration(nextTime) * time.Millisecond)
	}
	server, err := NewServer(NewMemoryStore(), backend, ServerOptions{
		CardOptions: CardOptions{
			BaseURL:          "https://chronodesk.example.com",
			OAuthMetadataURL: "https://auth.example.com/.well-known/oauth-authorization-server",
			OAuthTokenURL:    "https://auth.example.com/oauth/token",
		},
		ServiceOptions: ServiceOptions{
			NewID:          id,
			Now:            now,
			PushDispatcher: noopTestPushDispatcher{},
		},
		StreamLimiter: StreamLimiterFunc(func(context.Context) (func(), error) {
			return func() {}, nil
		}),
		Heartbeat: time.Hour,
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	return server
}

func leftPad(value, width int) string {
	text := strconv.Itoa(value)
	return strings.Repeat("0", width-len(text)) + text
}

type rpcEnvelope struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *JSONRPCError   `json:"error"`
}

func rpcCall(t *testing.T, router http.Handler, method string, params any) rpcEnvelope {
	t.Helper()
	body := rpcBody(t, method, params)
	request := httptest.NewRequest(http.MethodPost, "/a2a/v1", bytes.NewReader(body))
	request = request.WithContext(a2aTestContext(t))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("A2A-Version", ProtocolVersion)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("%s returned %d: %s", method, recorder.Code, recorder.Body.String())
	}
	var response rpcEnvelope
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode %s response: %v (%s)", method, err, recorder.Body.String())
	}
	return response
}

func rpcBody(t *testing.T, method string, params any) []byte {
	t.Helper()
	params = withA2ATestProjectMetadata(method, params)
	return rawRPCBody(t, method, params)
}

func rawRPCBody(t *testing.T, method string, params any) []byte {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      "request-1",
		"method":  method,
		"params":  params,
	})
	if err != nil {
		t.Fatalf("marshal RPC body: %v", err)
	}
	return body
}

func rpcErrorReason(t *testing.T, rpcErr *JSONRPCError) string {
	t.Helper()
	if rpcErr == nil || len(rpcErr.Data) != 1 {
		t.Fatalf("expected one canonical ErrorInfo, got %#v", rpcErr)
	}
	detail, ok := rpcErr.Data[0].(map[string]any)
	if !ok {
		t.Fatalf("canonical ErrorInfo has type %T", rpcErr.Data[0])
	}
	reason, ok := detail["reason"].(string)
	if !ok {
		t.Fatalf("canonical ErrorInfo has no reason: %#v", detail)
	}
	return reason
}

func withA2ATestProjectMetadata(method string, params any) any {
	if method != "SendMessage" && method != "SendStreamingMessage" {
		return params
	}
	switch value := params.(type) {
	case map[string]any:
		cloned := make(map[string]any, len(value)+1)
		for key, item := range value {
			cloned[key] = item
		}
		metadata := make(map[string]any)
		if existing, ok := value["metadata"].(map[string]any); ok {
			for key, item := range existing {
				metadata[key] = item
			}
		}
		if _, exists := metadata[MetadataProjectKey]; !exists {
			metadata[MetadataProjectKey] = a2aTestProjectKey
		}
		cloned["metadata"] = metadata
		return cloned
	case SendMessageParams:
		cloned := value
		cloned.Metadata = cloneMap(value.Metadata)
		if cloned.Metadata == nil {
			cloned.Metadata = make(map[string]any)
		}
		if _, exists := cloned.Metadata[MetadataProjectKey]; !exists {
			cloned.Metadata[MetadataProjectKey] = a2aTestProjectKey
		}
		return cloned
	default:
		return params
	}
}

func decodeResult(t *testing.T, raw json.RawMessage, target any) {
	t.Helper()
	if err := json.Unmarshal(raw, target); err != nil {
		t.Fatalf("decode RPC result: %v (%s)", err, string(raw))
	}
}

func decodeJSONObject(t *testing.T, raw json.RawMessage) map[string]json.RawMessage {
	t.Helper()
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		t.Fatalf("decode JSON object: %v (%s)", err, string(raw))
	}
	return object
}

func assertNoUnexpectedJSONKeys(
	t *testing.T,
	object map[string]json.RawMessage,
	allowed ...string,
) {
	t.Helper()
	allowedSet := make(map[string]struct{}, len(allowed))
	for _, field := range allowed {
		allowedSet[field] = struct{}{}
	}
	for field := range object {
		if _, ok := allowedSet[field]; !ok {
			t.Fatalf("unexpected JSON field %q in %#v", field, object)
		}
	}
}

func assertStates(t *testing.T, statuses []TaskStatus, expected ...TaskState) {
	t.Helper()
	if len(statuses) != len(expected) {
		t.Fatalf("expected %d statuses, got %d: %#v", len(expected), len(statuses), statuses)
	}
	for i := range expected {
		if statuses[i].State != expected[i] {
			t.Fatalf("status %d: expected %s, got %s", i, expected[i], statuses[i].State)
		}
	}
}

type sseEvent struct {
	ID       string
	Response JSONRPCResponse
}

func parseSSE(t *testing.T, body string) []sseEvent {
	t.Helper()
	var events []sseEvent
	current := sseEvent{}
	scanner := bufio.NewScanner(strings.NewReader(body))
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "id: "):
			current.ID = strings.TrimPrefix(line, "id: ")
		case strings.HasPrefix(line, "data: "):
			if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &current.Response); err != nil {
				t.Fatalf("decode SSE data: %v (%s)", err, line)
			}
		case line == "" && current.Response.JSONRPC != "":
			events = append(events, current)
			current = sseEvent{}
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan SSE: %v", err)
	}
	return events
}

func taskIDFromEvents(t *testing.T, events []sseEvent) string {
	t.Helper()
	for _, event := range events {
		payload, err := json.Marshal(event.Response.Result)
		if err != nil {
			continue
		}
		var stream StreamResponse
		if err := json.Unmarshal(payload, &stream); err == nil {
			if stream.Task != nil {
				return stream.Task.ID
			}
			if stream.StatusUpdate != nil {
				return stream.StatusUpdate.TaskID
			}
		}
	}
	t.Fatal("no task ID found in stream")
	return ""
}

func containsTerminalEvent(t *testing.T, events []sseEvent) bool {
	t.Helper()
	for _, event := range events {
		payload, err := json.Marshal(event.Response.Result)
		if err != nil {
			continue
		}
		var stream StreamResponse
		if err := json.Unmarshal(payload, &stream); err == nil && stream.Terminal() {
			return true
		}
	}
	return false
}

func containsInterruptedEvent(t *testing.T, events []sseEvent) bool {
	t.Helper()
	for _, event := range events {
		payload, err := json.Marshal(event.Response.Result)
		if err != nil {
			continue
		}
		var stream StreamResponse
		if err := json.Unmarshal(payload, &stream); err != nil {
			continue
		}
		if (stream.Task != nil && stream.Task.Status.State.IsInterrupted()) ||
			(stream.StatusUpdate != nil && stream.StatusUpdate.Status.State.IsInterrupted()) {
			return true
		}
	}
	return false
}
