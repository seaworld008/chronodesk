package agentplatform

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/seaworld008/chronodesk/server/internal/a2a"
	"github.com/seaworld008/chronodesk/server/internal/mcp"
	"github.com/seaworld008/chronodesk/server/internal/models"
)

func TestMCPRealAdapterPublishClosesRevokedSubscriptionAndReleasesSlot(
	t *testing.T,
) {
	fixture := newMCPAdapterFixture(t)
	ticket := fixture.seedTicket(
		t,
		"MCP-LIVE-REVOCATION",
		"triage",
	)
	server, err := mcp.NewServer(
		fixture.adapter,
		fixture.adapter,
		mcp.WithAuthorizer(fixture.adapter),
		mcp.WithCredentialRecheckInterval(time.Hour),
		mcp.WithSubscriptionStreamLimits(1, 1, 1),
	)
	if err != nil {
		t.Fatalf("create MCP server: %v", err)
	}
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Any("/mcp", server.Handler())
	httpServer := httptest.NewServer(router)
	t.Cleanup(func() {
		_ = server.Close()
		httpServer.Close()
	})

	uri := "ticket://projects/TEST/tickets/" +
		strconvFormatUint(ticket.ID)
	firstContext, cancelFirst := context.WithCancel(
		context.Background(),
	)
	defer cancelFirst()
	firstResponse, err := openMCPAdapterSubscription(
		firstContext,
		httpServer.Client(),
		httpServer.URL+"/mcp",
		fixture.token,
		"mcp-live-revoked",
		uri,
	)
	if err != nil {
		t.Fatalf("open first MCP subscription: %v", err)
	}
	defer firstResponse.Body.Close()
	firstMessages := scanAdapterSSE(firstResponse)
	firstMessage := waitAdapterSSEMessage(t, firstMessages)
	if firstMessage["method"] !=
		"notifications/subscriptions/acknowledged" {
		t.Fatalf("first MCP subscription acknowledgement = %#v", firstMessage)
	}

	if err := fixture.db.Model(
		&models.ProjectPrincipalGrant{},
	).Where(
		"project_id = ? AND service_principal_id = ?",
		fixture.project.ID,
		fixture.principal.ID,
	).Update("is_active", false).Error; err != nil {
		t.Fatalf("revoke MCP project grant: %v", err)
	}
	server.Publish(mcp.ResourceEvent{URI: uri})
	assertAdapterSSEClosesWithoutResourceUpdate(
		t,
		firstMessages,
		"MCP",
	)

	if err := fixture.db.Model(
		&models.ProjectPrincipalGrant{},
	).Where(
		"project_id = ? AND service_principal_id = ?",
		fixture.project.ID,
		fixture.principal.ID,
	).Update("is_active", true).Error; err != nil {
		t.Fatalf("restore MCP project grant: %v", err)
	}
	secondContext, cancelSecond := context.WithCancel(
		context.Background(),
	)
	defer cancelSecond()
	secondResponse, err := openMCPAdapterSubscription(
		secondContext,
		httpServer.Client(),
		httpServer.URL+"/mcp",
		fixture.token,
		"mcp-live-reacquired",
		uri,
	)
	if err != nil {
		t.Fatalf("open second MCP subscription after revocation: %v", err)
	}
	defer secondResponse.Body.Close()
	secondMessage := waitAdapterSSEMessage(
		t,
		scanAdapterSSE(secondResponse),
	)
	if secondMessage["method"] !=
		"notifications/subscriptions/acknowledged" {
		t.Fatalf(
			"MCP stream slot was not reusable after revocation: %#v",
			secondMessage,
		)
	}
}

func TestA2ARealAuthorizerClosesRevokedLiveStreamAndReleasesPermit(
	t *testing.T,
) {
	fixture := newA2AAdapterFixture(t)
	store := a2a.NewMemoryStore()
	broker := a2a.NewBroker()
	now := time.Now().UTC()
	task := a2a.Task{
		ID:        "task-live-revocation",
		ContextID: "context-live-revocation",
		Status: a2a.TaskStatus{
			State:     a2a.TaskStateWorking,
			Timestamp: now,
		},
		CreatedAt:    now,
		LastModified: now,
		Version:      1,
	}
	if err := store.CreateTask(
		a2aFixtureContext(t, fixture),
		task,
	); err != nil {
		t.Fatalf("create A2A stream task: %v", err)
	}

	var releases atomic.Int64
	server, err := a2a.NewServer(
		store,
		a2a.BackendFuncs{},
		a2a.ServerOptions{
			ServiceOptions: a2a.ServiceOptions{
				Broker: broker,
				TaskListAuthorizer: NewA2ATaskListAuthorizer(
					fixture.native,
				),
			},
			StreamLimiter: a2a.StreamLimiterFunc(
				func(context.Context) (func(), error) {
					var released atomic.Bool
					return func() {
						if released.CompareAndSwap(false, true) {
							releases.Add(1)
						}
					}, nil
				},
			),
			Heartbeat: time.Hour,
		},
	)
	if err != nil {
		t.Fatalf("create A2A server: %v", err)
	}
	identity := A2AExecutionIdentity{
		Actor:        models.ServicePrincipalActor(fixture.principal.ID),
		CredentialID: fixture.credential.ID,
		ProjectKey:   string(fixture.project.Key),
		Scope:        fixture.project.Scope(),
		TokenScopes:  fixture.principal.ScopeList(),
	}
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		requestContext, bindErr := bindA2AOperationIdentity(
			c.Request.Context(),
			identity,
		)
		if bindErr != nil {
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}
		c.Request = c.Request.WithContext(requestContext)
		c.Next()
	})
	router.POST("/a2a/v1", server.RPCHandler())
	httpServer := httptest.NewServer(router)
	t.Cleanup(httpServer.Close)

	streamContext, cancelStream := context.WithCancel(
		context.Background(),
	)
	defer cancelStream()
	response, err := openA2AAdapterSubscription(
		streamContext,
		httpServer.Client(),
		httpServer.URL+"/a2a/v1",
		task.ID,
	)
	if err != nil {
		t.Fatalf("open A2A subscription: %v", err)
	}
	defer response.Body.Close()
	messages := scanAdapterSSE(response)
	initial := waitAdapterSSEMessage(t, messages)
	if initial["result"] == nil || initial["error"] != nil {
		t.Fatalf("A2A initial snapshot = %#v", initial)
	}

	if err := fixture.db.Model(
		&models.ProjectPrincipalGrant{},
	).Where(
		"project_id = ? AND service_principal_id = ?",
		fixture.project.ID,
		fixture.principal.ID,
	).Update("is_active", false).Error; err != nil {
		t.Fatalf("revoke A2A project grant: %v", err)
	}
	broker.Publish(a2a.StoredEvent{
		TaskID:    task.ID,
		ContextID: task.ContextID,
		Cursor:    "live-revocation-event",
		Payload: a2a.StreamResponse{
			StatusUpdate: &a2a.TaskStatusUpdateEvent{
				TaskID:    task.ID,
				ContextID: task.ContextID,
				Status: a2a.TaskStatus{
					State:     a2a.TaskStateWorking,
					Timestamp: time.Now().UTC(),
				},
			},
		},
	})
	assertAdapterSSEClosesWithoutResourceUpdate(t, messages, "A2A")
	deadline := time.Now().Add(time.Second)
	for releases.Load() != 1 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if releases.Load() != 1 {
		t.Fatalf(
			"A2A revoked stream permit releases = %d, want 1",
			releases.Load(),
		)
	}
}

func openMCPAdapterSubscription(
	ctx context.Context,
	client *http.Client,
	endpoint string,
	token string,
	requestID string,
	uri string,
) (*http.Response, error) {
	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      requestID,
		"method":  "subscriptions/listen",
		"params": map[string]any{
			"_meta": map[string]any{
				"io.modelcontextprotocol/protocolVersion": mcp.ProtocolVersion,
				"io.modelcontextprotocol/clientCapabilities": map[string]any{
					"extensions": map[string]any{
						mcp.OAuthClientCredentialsExtension: map[string]any{},
					},
				},
				"io.modelcontextprotocol/clientInfo": map[string]any{
					"name":    "chronodesk-live-revocation-test",
					"version": "1.0.0",
				},
			},
			"notifications": map[string]any{
				"resourceSubscriptions": []string{uri},
			},
		},
	})
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		endpoint,
		bytes.NewReader(body),
	)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Accept", "application/json, text/event-stream")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(mcp.HeaderProtocolVersion, mcp.ProtocolVersion)
	request.Header.Set(mcp.HeaderMethod, "subscriptions/listen")
	return client.Do(request)
}

func openA2AAdapterSubscription(
	ctx context.Context,
	client *http.Client,
	endpoint string,
	taskID string,
) (*http.Response, error) {
	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      "a2a-live-revocation",
		"method":  "SubscribeToTask",
		"params":  map[string]any{"id": taskID},
	})
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		endpoint,
		bytes.NewReader(body),
	)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "text/event-stream")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("A2A-Version", a2a.ProtocolVersion)
	return client.Do(request)
}

func scanAdapterSSE(response *http.Response) <-chan map[string]any {
	messages := make(chan map[string]any, 8)
	go func() {
		defer close(messages)
		scanner := bufio.NewScanner(response.Body)
		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			var message map[string]any
			if json.Unmarshal(
				[]byte(strings.TrimPrefix(line, "data: ")),
				&message,
			) == nil {
				messages <- message
			}
		}
	}()
	return messages
}

func waitAdapterSSEMessage(
	t *testing.T,
	messages <-chan map[string]any,
) map[string]any {
	t.Helper()
	select {
	case message, ok := <-messages:
		if !ok {
			t.Fatal("SSE stream closed before expected message")
		}
		return message
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for SSE message")
		return nil
	}
}

func assertAdapterSSEClosesWithoutResourceUpdate(
	t *testing.T,
	messages <-chan map[string]any,
	protocol string,
) {
	t.Helper()
	timeout := time.NewTimer(3 * time.Second)
	defer timeout.Stop()
	for {
		select {
		case message, ok := <-messages:
			if !ok {
				return
			}
			if protocol != "MCP" ||
				message["method"] ==
					"notifications/resources/updated" {
				t.Fatalf(
					"%s revoked stream leaked frame: %#v",
					protocol,
					message,
				)
			}
		case <-timeout.C:
			t.Fatalf("%s revoked stream did not close", protocol)
		}
	}
}

func strconvFormatUint(value uint) string {
	return strconv.FormatUint(uint64(value), 10)
}
