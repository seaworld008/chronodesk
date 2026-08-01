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
	"time"

	"github.com/gin-gonic/gin"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/services"
	"gorm.io/datatypes"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type collaborationHandlerFixture struct {
	db              *gorm.DB
	handler         *AgentCollaborationHandler
	commands        *services.AgentCollaborationService
	human           models.User
	humanContext    context.Context
	agentContext    context.Context
	access          services.ProjectAccess
	ticket          models.Ticket
	run             *models.AgentRun
	proposal        *models.ActionProposal
	approval        *models.ApprovalTask
	foreignRun      *models.AgentRun
	foreignProposal *models.ActionProposal
	foreignApproval *models.ApprovalTask
	handoff         models.Handoff
	foreignHandoff  models.Handoff
}

func newCollaborationHandlerFixture(
	t *testing.T,
) collaborationHandlerFixture {
	t.Helper()
	name := strings.ReplaceAll(t.Name(), "/", "_")
	db, err := gorm.Open(
		sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", name)),
		&gorm.Config{},
	)
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	if err := db.AutoMigrate(
		&models.User{},
		&models.ServicePrincipal{},
		&models.Ticket{},
		&models.TicketLease{},
		&models.DomainEvent{},
		&models.OutboxDelivery{},
		&models.AgentTask{},
		&models.AgentRun{},
		&models.ActionProposal{},
		&models.ApprovalTask{},
		&models.ApprovalDecision{},
		&models.Handoff{},
	); err != nil {
		t.Fatal(err)
	}

	human := models.User{
		Username:     "collaboration-manager",
		Email:        "collaboration-manager@example.test",
		PasswordHash: "hash",
		PlatformRole: models.PlatformRoleMember,
		Status:       models.UserStatusActive,
	}
	if err := db.Create(&human).Error; err != nil {
		t.Fatal(err)
	}
	const principalID = "00000000-0000-7000-8000-000000009101"
	scope := models.ProjectScope{OrganizationID: 31, ProjectID: 41}
	foreignScope := models.ProjectScope{OrganizationID: 31, ProjectID: 42}
	humanContext := handlerOperationContext(
		t,
		scope,
		models.HumanActor(human.ID),
		services.SourceProtocolHumanREST,
		"",
	)
	agentContext := handlerOperationContext(
		t,
		scope,
		models.ServicePrincipalActor(principalID),
		services.SourceProtocolAgentREST,
		"credential-test",
	)
	foreignAgentContext := handlerOperationContext(
		t,
		foreignScope,
		models.ServicePrincipalActor(principalID),
		services.SourceProtocolAgentREST,
		"credential-foreign",
	)

	ticket := collaborationHandlerTicket(
		scope,
		"TEST-1",
		models.HumanActor(human.ID),
	)
	foreignTicket := collaborationHandlerTicket(
		foreignScope,
		"OTHER-1",
		models.HumanActor(human.ID),
	)
	if err := db.Create(&ticket).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&foreignTicket).Error; err != nil {
		t.Fatal(err)
	}

	native := services.NewAgentNativeService(db)
	commands, err := services.NewAgentCollaborationService(db, native)
	if err != nil {
		t.Fatal(err)
	}
	run := startCollaborationHandlerRun(
		t,
		commands,
		agentContext,
		ticket.ID,
		principalID,
	)
	foreignRun := startCollaborationHandlerRun(
		t,
		commands,
		foreignAgentContext,
		foreignTicket.ID,
		principalID,
	)
	proposal, approval := createCollaborationHandlerApproval(
		t,
		commands,
		agentContext,
		run.ID,
		ticket,
	)
	foreignProposal, foreignApproval := createCollaborationHandlerApproval(
		t,
		commands,
		foreignAgentContext,
		foreignRun.ID,
		foreignTicket,
	)

	handoff := collaborationHandlerHandoff(
		scope,
		ticket.ID,
		run.ID,
		human.ID,
	)
	foreignHandoff := collaborationHandlerHandoff(
		foreignScope,
		foreignTicket.ID,
		foreignRun.ID,
		human.ID,
	)
	if err := db.Create(&handoff).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&foreignHandoff).Error; err != nil {
		t.Fatal(err)
	}

	queries, err := services.NewAgentCollaborationQueryService(db)
	if err != nil {
		t.Fatal(err)
	}
	access := services.ProjectAccess{
		Project: models.Project{
			ID:             scope.ProjectID,
			OrganizationID: scope.OrganizationID,
			Key:            models.ProjectKey("TEST"),
		},
		Role:  models.ProjectRoleManager,
		Scope: scope,
	}
	return collaborationHandlerFixture{
		db:              db,
		handler:         NewAgentCollaborationHandler(queries, commands),
		commands:        commands,
		human:           human,
		humanContext:    humanContext,
		agentContext:    agentContext,
		access:          access,
		ticket:          ticket,
		run:             run,
		proposal:        proposal,
		approval:        approval,
		foreignRun:      foreignRun,
		foreignProposal: foreignProposal,
		foreignApproval: foreignApproval,
		handoff:         handoff,
		foreignHandoff:  foreignHandoff,
	}
}

func handlerOperationContext(
	t *testing.T,
	scope models.ProjectScope,
	actor models.ActorRef,
	source services.SourceProtocol,
	credentialID string,
) context.Context {
	t.Helper()
	ctx, err := services.WithOperationContext(
		context.Background(),
		services.OperationContext{
			Scope:        scope,
			Actor:        actor,
			Source:       source,
			CredentialID: credentialID,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return ctx
}

func collaborationHandlerTicket(
	scope models.ProjectScope,
	number string,
	createdBy models.ActorRef,
) models.Ticket {
	return models.Ticket{
		OrganizationID:       scope.OrganizationID,
		ProjectID:            scope.ProjectID,
		QueueID:              scope.ProjectID,
		RequestTypeVersionID: "00000000-0000-7000-8000-000000000102",
		WorkflowVersionID:    "00000000-0000-7000-8000-000000000201",
		TicketNumber:         number,
		Title:                "AI 协作处理",
		Description:          "人工审批与接管",
		Type:                 models.TicketTypeRequest,
		Priority:             models.TicketPriorityHigh,
		Status:               models.TicketStatusOpen,
		Source:               models.TicketSourceAgent,
		Version:              1,
		CreatedByActorType:   createdBy.Type,
		CreatedByActorID:     createdBy.ID,
	}
}

func startCollaborationHandlerRun(
	t *testing.T,
	service *services.AgentCollaborationService,
	ctx context.Context,
	ticketID uint,
	principalID string,
) *models.AgentRun {
	t.Helper()
	run, err := service.StartAgentRun(
		ctx,
		services.StartAgentRunInput{
			TicketID:       ticketID,
			PrincipalID:    principalID,
			ModelProvider:  "test",
			ModelName:      "test-model",
			PromptVersion:  "prompt-v1",
			ToolsetVersion: "tools-v1",
			PolicyVersion:  "policy-v1",
			PolicySnapshot: map[string]any{
				"secret_control": "must-not-leak",
			},
			InputSummary: "工单分类",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return run
}

func createCollaborationHandlerApproval(
	t *testing.T,
	service *services.AgentCollaborationService,
	ctx context.Context,
	runID string,
	ticket models.Ticket,
) (*models.ActionProposal, *models.ApprovalTask) {
	t.Helper()
	result, err := service.CreateActionProposal(
		ctx,
		services.CreateActionProposalInput{
			AgentRunID: runID,
			TicketID:   ticket.ID,
			ActionType: services.ActionTypeTicketCommentCreate,
			ActionPayload: map[string]any{
				"lease_id":     "redacted-proposal-lease",
				"content":      "sensitive payload",
				"content_type": "text",
				"type":         models.CommentTypePublic,
			},
			ChangePreview: map[string]any{
				"summary":         "向请求人发送进度说明",
				"policy_snapshot": "must-not-leak",
			},
			EvidenceDigest: "private-evidence",
			RiskLevel:      models.ActionRiskHigh,
			TargetVersion:  ticket.Version,
			PolicyVersion:  "policy-v1",
			ExpiresIn:      time.Hour,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return result.Proposal, result.Approval
}

func collaborationHandlerHandoff(
	scope models.ProjectScope,
	ticketID uint,
	runID string,
	humanID uint,
) models.Handoff {
	return models.Handoff{
		OrganizationID:   scope.OrganizationID,
		ProjectID:        scope.ProjectID,
		TicketID:         ticketID,
		AgentRunID:       runID,
		Direction:        models.HandoffAgentToHuman,
		FromActorType:    models.ActorTypeServicePrincipal,
		FromActorID:      "principal-secret",
		ToActorType:      models.ActorTypeHuman,
		ToActorID:        fmt.Sprint(humanID),
		Reason:           "需要人工确认",
		CompletedSummary: "已完成分类",
		MissingInfo:      datatypes.JSON(`["客户确认"]`),
		EvidenceDigest:   "private-evidence",
	}
}

func (fixture collaborationHandlerFixture) router(
	role models.ProjectRole,
) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	group := router.Group("/api/projects/:projectKey")
	group.Use(func(c *gin.Context) {
		access := fixture.access
		access.Role = role
		c.Set("user_id", fixture.human.ID)
		c.Set(projectAccessContextKey, access)
		c.Set(projectRoleContextKey, string(role))
		c.Request = c.Request.WithContext(fixture.humanContext)
		c.Next()
	})
	fixture.handler.RegisterRoutes(group)
	return router
}

func TestAgentCollaborationHandlerRejectsCrossProjectResources(
	t *testing.T,
) {
	fixture := newCollaborationHandlerFixture(t)
	router := fixture.router(models.ProjectRoleManager)
	paths := []string{
		"/api/projects/TEST/agent-collaboration/runs/" +
			fixture.foreignRun.ID,
		"/api/projects/TEST/agent-collaboration/proposals/" +
			fixture.foreignProposal.ID,
		"/api/projects/TEST/agent-collaboration/approvals/" +
			fixture.foreignApproval.ID,
		"/api/projects/TEST/agent-collaboration/handoffs/" +
			fixture.foreignHandoff.ID,
	}
	for _, path := range paths {
		response := performCollaborationRequest(
			t,
			router,
			http.MethodGet,
			path,
			"",
		)
		if response.Code != http.StatusNotFound ||
			!strings.Contains(response.Body.String(), "collaboration_not_found") {
			t.Fatalf("%s response = %d %s", path, response.Code, response.Body.String())
		}
	}

	response := performCollaborationRequest(
		t,
		router,
		http.MethodPost,
		"/api/projects/TEST/agent-collaboration/approvals/"+
			fixture.foreignApproval.ID+
			"/decisions",
		`{"decision":"approve"}`,
	)
	if response.Code != http.StatusNotFound {
		t.Fatalf("cross-project decision = %d %s", response.Code, response.Body.String())
	}
	var decisions int64
	if err := fixture.db.Model(&models.ApprovalDecision{}).
		Where(
			"approval_task_id = ? AND organization_id = ? AND project_id = ?",
			fixture.foreignApproval.ID,
			fixture.foreignApproval.OrganizationID,
			fixture.foreignApproval.ProjectID,
		).
		Count(&decisions).Error; err != nil {
		t.Fatal(err)
	}
	if decisions != 0 {
		t.Fatalf("cross-project decision count = %d", decisions)
	}
}

func TestAgentCollaborationHandlerInvalidatesStaleTicketApproval(
	t *testing.T,
) {
	fixture := newCollaborationHandlerFixture(t)
	if err := fixture.db.Model(&models.Ticket{}).
		Where(
			"id = ? AND organization_id = ? AND project_id = ?",
			fixture.ticket.ID,
			fixture.ticket.OrganizationID,
			fixture.ticket.ProjectID,
		).
		Update("version", fixture.ticket.Version+1).Error; err != nil {
		t.Fatal(err)
	}
	response := performCollaborationRequest(
		t,
		fixture.router(models.ProjectRoleManager),
		http.MethodPost,
		"/api/projects/TEST/agent-collaboration/approvals/"+
			fixture.approval.ID+
			"/decisions",
		`{"decision":"approve","comment":"同意执行"}`,
	)
	if response.Code != http.StatusConflict ||
		!strings.Contains(response.Body.String(), "approval_invalidated") {
		t.Fatalf("stale approval = %d %s", response.Code, response.Body.String())
	}
	var task models.ApprovalTask
	if err := fixture.db.Where(
		"id = ? AND organization_id = ? AND project_id = ?",
		fixture.approval.ID,
		fixture.ticket.OrganizationID,
		fixture.ticket.ProjectID,
	).Take(&task).Error; err != nil {
		t.Fatal(err)
	}
	if task.Status != models.ApprovalTaskInvalidated {
		t.Fatalf("approval status = %s", task.Status)
	}
}

func TestAgentCollaborationHandlerTakeoverBlocksLaterAgentWrite(
	t *testing.T,
) {
	fixture := newCollaborationHandlerFixture(t)
	var before int64
	if err := fixture.db.Model(&models.ActionProposal{}).
		Where(
			"agent_run_id = ? AND organization_id = ? AND project_id = ?",
			fixture.run.ID,
			fixture.ticket.OrganizationID,
			fixture.ticket.ProjectID,
		).
		Count(&before).Error; err != nil {
		t.Fatal(err)
	}
	response := performCollaborationRequest(
		t,
		fixture.router(models.ProjectRoleAgent),
		http.MethodPost,
		"/api/projects/TEST/agent-collaboration/runs/"+
			fixture.run.ID+
			"/takeover",
		`{"reason":"需要人工确认外部影响","completed_summary":"已完成分类","missing_information":["客户确认"],"evidence_digest":"handoff-evidence"}`,
	)
	if response.Code != http.StatusOK {
		t.Fatalf("takeover = %d %s", response.Code, response.Body.String())
	}
	assertSafeCollaborationResponse(t, response.Body.Bytes())

	var ticket models.Ticket
	if err := fixture.db.Where(
		"id = ? AND organization_id = ? AND project_id = ?",
		fixture.ticket.ID,
		fixture.ticket.OrganizationID,
		fixture.ticket.ProjectID,
	).Take(&ticket).Error; err != nil {
		t.Fatal(err)
	}
	_, err := fixture.commands.CreateActionProposal(
		fixture.agentContext,
		services.CreateActionProposalInput{
			AgentRunID: fixture.run.ID,
			TicketID:   fixture.ticket.ID,
			ActionType: "ticket.update",
			ActionPayload: map[string]any{
				"lease_id": "released-after-takeover",
				"priority": "urgent",
			},
			ChangePreview:  map[string]any{"priority": "urgent"},
			EvidenceDigest: "post-takeover-evidence",
			RiskLevel:      models.ActionRiskLow,
			TargetVersion:  ticket.Version,
			PolicyVersion:  "policy-v1",
			ExpiresIn:      time.Hour,
		},
	)
	if err == nil || !strings.Contains(err.Error(), "terminal agent run") {
		t.Fatalf("post-takeover Agent write error = %v", err)
	}
	var after int64
	if err := fixture.db.Model(&models.ActionProposal{}).
		Where(
			"agent_run_id = ? AND organization_id = ? AND project_id = ?",
			fixture.run.ID,
			fixture.ticket.OrganizationID,
			fixture.ticket.ProjectID,
		).
		Count(&after).Error; err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatalf("proposal count after blocked write = %d, before = %d", after, before)
	}
}

func TestAgentCollaborationHandlerLeastPrivilegeAndStrictJSON(
	t *testing.T,
) {
	fixture := newCollaborationHandlerFixture(t)

	for _, role := range []models.ProjectRole{
		models.ProjectRoleObserver,
		models.ProjectRoleRequester,
	} {
		t.Run(string(role)+" reads safe proposal", func(t *testing.T) {
			response := performCollaborationRequest(
				t,
				fixture.router(role),
				http.MethodGet,
				"/api/projects/TEST/agent-collaboration/proposals/"+
					fixture.proposal.ID,
				"",
			)
			if response.Code != http.StatusOK {
				t.Fatalf("read = %d %s", response.Code, response.Body.String())
			}
			assertSafeCollaborationResponse(t, response.Body.Bytes())
		})
		t.Run(string(role)+" cannot approve", func(t *testing.T) {
			response := performCollaborationRequest(
				t,
				fixture.router(role),
				http.MethodPost,
				"/api/projects/TEST/agent-collaboration/approvals/"+
					fixture.approval.ID+
					"/decisions",
				`{"decision":"approve"}`,
			)
			if response.Code != http.StatusForbidden {
				t.Fatalf("approval write = %d %s", response.Code, response.Body.String())
			}
		})
		t.Run(string(role)+" cannot takeover", func(t *testing.T) {
			response := performCollaborationRequest(
				t,
				fixture.router(role),
				http.MethodPost,
				"/api/projects/TEST/agent-collaboration/runs/"+
					fixture.run.ID+
					"/takeover",
				`{"reason":"越权","evidence_digest":"evidence"}`,
			)
			if response.Code != http.StatusForbidden {
				t.Fatalf("takeover write = %d %s", response.Code, response.Body.String())
			}
		})
	}

	response := performCollaborationRequest(
		t,
		fixture.router(models.ProjectRoleManager),
		http.MethodPost,
		"/api/projects/TEST/agent-collaboration/approvals/"+
			fixture.approval.ID+
			"/decisions",
		`{"decision":"approve","project_id":42}`,
	)
	if response.Code != http.StatusBadRequest ||
		!strings.Contains(response.Body.String(), "invalid_collaboration_request") {
		t.Fatalf("unknown JSON field = %d %s", response.Code, response.Body.String())
	}
	var decisions int64
	if err := fixture.db.Model(&models.ApprovalDecision{}).
		Where(
			"approval_task_id = ? AND organization_id = ? AND project_id = ?",
			fixture.approval.ID,
			fixture.ticket.OrganizationID,
			fixture.ticket.ProjectID,
		).
		Count(&decisions).Error; err != nil {
		t.Fatal(err)
	}
	if decisions != 0 {
		t.Fatalf("strict JSON still wrote %d decisions", decisions)
	}
}

func TestAgentCollaborationHandlerUsesStrictBoundedPagination(t *testing.T) {
	fixture := newCollaborationHandlerFixture(t)
	router := fixture.router(models.ProjectRoleManager)
	for _, resource := range []string{
		"runs",
		"proposals",
		"approvals",
		"handoffs",
	} {
		t.Run(resource+" defaults to 25", func(t *testing.T) {
			response := performCollaborationRequest(
				t,
				router,
				http.MethodGet,
				"/api/projects/TEST/agent-collaboration/"+resource,
				"",
			)
			if response.Code != http.StatusOK ||
				!strings.Contains(response.Body.String(), `"page_size":25`) {
				t.Fatalf(
					"default pagination = %d %s",
					response.Code,
					response.Body.String(),
				)
			}
		})
		for _, query := range []string{
			"page=0",
			"page=-1",
			"page_size=0",
			"page_size=101",
			"page_size=",
			"page_size=25&page_size=50",
			"page_size=%2025",
			"unknown=value",
			"page=999999999999999999999999999999",
		} {
			t.Run(resource+" rejects "+query, func(t *testing.T) {
				response := performCollaborationRequest(
					t,
					router,
					http.MethodGet,
					"/api/projects/TEST/agent-collaboration/"+
						resource+"?"+query,
					"",
				)
				if response.Code != http.StatusBadRequest ||
					!strings.Contains(
						response.Body.String(),
						`"code":"invalid_pagination"`,
					) {
					t.Fatalf(
						"invalid pagination = %d %s",
						response.Code,
						response.Body.String(),
					)
				}
			})
		}
	}
}

func TestAgentCollaborationHandlerRejectsUntrustedOperationContext(
	t *testing.T,
) {
	fixture := newCollaborationHandlerFixture(t)
	gin.SetMode(gin.TestMode)
	router := gin.New()
	group := router.Group("/api/projects/:projectKey")
	group.Use(func(c *gin.Context) {
		c.Set("user_id", fixture.human.ID)
		c.Set(projectAccessContextKey, fixture.access)
		// Deliberately omit the trusted OperationContext.
		c.Next()
	})
	fixture.handler.RegisterRoutes(group)
	response := performCollaborationRequest(
		t,
		router,
		http.MethodGet,
		"/api/projects/TEST/agent-collaboration/runs",
		"",
	)
	if response.Code != http.StatusForbidden ||
		!strings.Contains(response.Body.String(), "collaboration_access_denied") {
		t.Fatalf("untrusted context = %d %s", response.Code, response.Body.String())
	}
}

func performCollaborationRequest(
	t *testing.T,
	router http.Handler,
	method string,
	path string,
	body string,
) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

func assertSafeCollaborationResponse(t *testing.T, body []byte) {
	t.Helper()
	var payload any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("response is not JSON: %v (%s)", err, body)
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	lower := strings.ToLower(string(encoded))
	for _, forbidden := range []string{
		"action_payload",
		"evidence_digest",
		"policy_snapshot",
		"principal_id",
		"proposal_digest",
		"private@example.test",
		"sensitive payload",
		"must-not-leak",
		"private-evidence",
		"principal-secret",
	} {
		if strings.Contains(lower, forbidden) {
			t.Fatalf("unsafe field %q leaked in %s", forbidden, lower)
		}
	}
}
