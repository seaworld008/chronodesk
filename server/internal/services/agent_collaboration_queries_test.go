package services

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

type collaborationQueryFixture struct {
	db              *gorm.DB
	service         *AgentCollaborationQueryService
	context         context.Context
	access          ProjectAccess
	requester       ProjectAccess
	runOwn          models.AgentRun
	runOther        models.AgentRun
	runForeign      models.AgentRun
	proposalOwn     models.ActionProposal
	proposalForeign models.ActionProposal
	approvalOwn     models.ApprovalTask
	approvalForeign models.ApprovalTask
	handoffOwn      models.Handoff
	handoffForeign  models.Handoff
}

func newCollaborationQueryFixture(t *testing.T) collaborationQueryFixture {
	t.Helper()
	db := openTestDB(t)
	if err := db.AutoMigrate(
		&models.User{},
		&models.ServicePrincipal{},
		&models.Ticket{},
		&models.AgentRun{},
		&models.ActionProposal{},
		&models.ApprovalTask{},
		&models.ApprovalDecision{},
		&models.Handoff{},
	); err != nil {
		t.Fatal(err)
	}

	const (
		organizationID = uint(11)
		projectID      = uint(21)
		foreignProject = uint(22)
		requesterID    = uint(7)
	)
	scope := models.ProjectScope{
		OrganizationID: organizationID,
		ProjectID:      projectID,
	}
	ctx, err := WithOperationContext(
		context.Background(),
		OperationContext{
			Scope:  scope,
			Actor:  models.HumanActor(requesterID),
			Source: SourceProtocolHumanREST,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	access := ProjectAccess{
		Project: models.Project{
			ID:             projectID,
			OrganizationID: organizationID,
			Key:            models.ProjectKey("TEST"),
		},
		Role:  models.ProjectRoleManager,
		Scope: scope,
	}

	tickets := []models.Ticket{
		queryTestTicket(
			organizationID,
			projectID,
			"TEST-1",
			models.HumanActor(requesterID),
		),
		queryTestTicket(
			organizationID,
			projectID,
			"TEST-2",
			models.HumanActor(8),
		),
		queryTestTicket(
			organizationID,
			foreignProject,
			"OTHER-1",
			models.HumanActor(requesterID),
		),
	}
	if err := db.Create(&tickets).Error; err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	runs := []models.AgentRun{
		queryTestRun(organizationID, projectID, tickets[0].ID, now),
		queryTestRun(organizationID, projectID, tickets[1].ID, now.Add(time.Second)),
		queryTestRun(
			organizationID,
			foreignProject,
			tickets[2].ID,
			now.Add(2*time.Second),
		),
	}
	if err := db.Create(&runs).Error; err != nil {
		t.Fatal(err)
	}

	proposals := []models.ActionProposal{
		queryTestProposal(
			organizationID,
			projectID,
			tickets[0].ID,
			runs[0].ID,
			now,
		),
		queryTestProposal(
			organizationID,
			projectID,
			tickets[1].ID,
			runs[1].ID,
			now.Add(time.Second),
		),
		queryTestProposal(
			organizationID,
			foreignProject,
			tickets[2].ID,
			runs[2].ID,
			now.Add(2*time.Second),
		),
	}
	if err := db.Create(&proposals).Error; err != nil {
		t.Fatal(err)
	}

	approvals := make([]models.ApprovalTask, 0, len(proposals))
	handoffs := make([]models.Handoff, 0, len(proposals))
	for index := range proposals {
		proposal := proposals[index]
		approvals = append(approvals, models.ApprovalTask{
			OrganizationID:    proposal.OrganizationID,
			ProjectID:         proposal.ProjectID,
			TicketID:          proposal.TicketID,
			ProposalID:        proposal.ID,
			ProposalDigest:    proposal.ProposalDigest,
			TargetVersion:     proposal.TargetVersion,
			PolicyVersion:     proposal.PolicyVersion,
			RequiredApprovals: 1,
			Status:            models.ApprovalTaskPending,
			ExpiresAt:         proposal.ExpiresAt,
		})
		handoffs = append(handoffs, models.Handoff{
			OrganizationID:   proposal.OrganizationID,
			ProjectID:        proposal.ProjectID,
			TicketID:         proposal.TicketID,
			AgentRunID:       proposal.AgentRunID,
			Direction:        models.HandoffAgentToHuman,
			FromActorType:    models.ActorTypeServicePrincipal,
			FromActorID:      "principal-secret",
			ToActorType:      models.ActorTypeHuman,
			ToActorID:        "7",
			Reason:           "需要人工确认",
			CompletedSummary: "已完成分类",
			MissingInfo:      datatypes.JSON(`["客户确认"]`),
			EvidenceDigest:   "private-evidence-digest",
		})
	}
	if err := db.Create(&approvals).Error; err != nil {
		t.Fatal(err)
	}
	decisions := []models.ApprovalDecision{
		{
			OrganizationID: organizationID,
			ProjectID:      projectID,
			ApprovalTaskID: approvals[0].ID,
			ActorType:      models.ActorTypeHuman,
			ActorID:        "7",
			Decision:       models.ApprovalDecisionApprove,
			ProposalDigest: approvals[0].ProposalDigest,
		},
		{
			OrganizationID: organizationID,
			ProjectID:      foreignProject,
			ApprovalTaskID: approvals[0].ID,
			ActorType:      models.ActorTypeHuman,
			ActorID:        "9",
			Decision:       models.ApprovalDecisionApprove,
			ProposalDigest: approvals[0].ProposalDigest,
		},
	}
	if err := db.Create(&decisions).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&handoffs).Error; err != nil {
		t.Fatal(err)
	}

	service, err := NewAgentCollaborationQueryService(db)
	if err != nil {
		t.Fatal(err)
	}
	requester := access
	requester.Role = models.ProjectRoleRequester
	return collaborationQueryFixture{
		db:              db,
		service:         service,
		context:         ctx,
		access:          access,
		requester:       requester,
		runOwn:          runs[0],
		runOther:        runs[1],
		runForeign:      runs[2],
		proposalOwn:     proposals[0],
		proposalForeign: proposals[2],
		approvalOwn:     approvals[0],
		approvalForeign: approvals[2],
		handoffOwn:      handoffs[0],
		handoffForeign:  handoffs[2],
	}
}

func queryTestTicket(
	organizationID uint,
	projectID uint,
	number string,
	actor models.ActorRef,
) models.Ticket {
	return models.Ticket{
		OrganizationID:       organizationID,
		ProjectID:            projectID,
		QueueID:              projectID,
		RequestTypeVersionID: defaultRequestTypeRequestVersionID,
		WorkflowVersionID:    defaultWorkflowVersionID,
		TicketNumber:         number,
		Title:                number,
		Description:          "query boundary",
		Type:                 models.TicketTypeRequest,
		Priority:             models.TicketPriorityNormal,
		Status:               models.TicketStatusOpen,
		Source:               models.TicketSourceWeb,
		Version:              1,
		CreatedByActorType:   actor.Type,
		CreatedByActorID:     actor.ID,
	}
}

func queryTestRun(
	organizationID uint,
	projectID uint,
	ticketID uint,
	createdAt time.Time,
) models.AgentRun {
	return models.AgentRun{
		CreatedAt:        createdAt,
		UpdatedAt:        createdAt,
		OrganizationID:   organizationID,
		ProjectID:        projectID,
		TicketID:         ticketID,
		PrincipalID:      "principal-secret",
		Status:           models.AgentRunStatusRunning,
		ModelProvider:    "provider",
		ModelName:        "model",
		PromptVersion:    "prompt-v1",
		ToolsetVersion:   "tools-v1",
		PolicyVersion:    "policy-v1",
		PolicySnapshot:   datatypes.JSON(`{"secret":"never-return"}`),
		InputSummary:     "输入摘要",
		OutputSummary:    "输出摘要",
		PromptTokens:     10,
		CompletionTokens: 20,
		CostMicros:       30,
	}
}

func queryTestProposal(
	organizationID uint,
	projectID uint,
	ticketID uint,
	runID string,
	createdAt time.Time,
) models.ActionProposal {
	return models.ActionProposal{
		CreatedAt:      createdAt,
		UpdatedAt:      createdAt,
		OrganizationID: organizationID,
		ProjectID:      projectID,
		TicketID:       ticketID,
		AgentRunID:     runID,
		ProposedByType: models.ActorTypeServicePrincipal,
		ProposedByID:   "principal-secret",
		ActionType:     "external.communication.reply",
		ActionPayload: datatypes.JSON(
			`{"recipient":"private@example.test","token":"never-return"}`,
		),
		ChangePreview: datatypes.JSON(
			`{"summary":"回复请求人","policy_snapshot":"never-return","nested":{"safe":"保留","secret":"never-return"}}`,
		),
		EvidenceDigest: "private-evidence-digest",
		RiskLevel:      models.ActionRiskHigh,
		TargetVersion:  1,
		PolicyVersion:  "policy-v1",
		Status:         models.ActionProposalPending,
		ExpiresAt:      createdAt.Add(time.Hour),
	}
}

func TestAgentCollaborationQueriesEnforceProjectScopeAndSafeDTOs(
	t *testing.T,
) {
	fixture := newCollaborationQueryFixture(t)

	runs, err := fixture.service.ListAgentRuns(
		fixture.context,
		fixture.access,
		CollaborationPagination{Page: 1, PageSize: 100},
	)
	if err != nil {
		t.Fatal(err)
	}
	if runs.Total != 2 || len(runs.Items) != 2 || runs.PageSize != 100 {
		t.Fatalf("scoped/bounded runs = %+v", runs)
	}
	for _, invalid := range []CollaborationPagination{
		{Page: -1, PageSize: 25},
		{Page: 1, PageSize: -1},
		{Page: 1, PageSize: 101},
		{Page: math.MaxInt, PageSize: 100},
	} {
		if _, listErr := fixture.service.ListAgentRuns(
			fixture.context,
			fixture.access,
			invalid,
		); !errors.Is(listErr, ErrCollaborationPagination) {
			t.Fatalf("invalid pagination %+v error = %v", invalid, listErr)
		}
	}
	proposals, err := fixture.service.ListActionProposals(
		fixture.context,
		fixture.access,
		CollaborationPagination{},
	)
	if err != nil {
		t.Fatal(err)
	}
	approvals, err := fixture.service.ListApprovalTasks(
		fixture.context,
		fixture.access,
		CollaborationPagination{},
	)
	if err != nil {
		t.Fatal(err)
	}
	handoffs, err := fixture.service.ListHandoffs(
		fixture.context,
		fixture.access,
		CollaborationPagination{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if proposals.Total != 2 || approvals.Total != 2 || handoffs.Total != 2 {
		t.Fatalf(
			"scoped totals: proposals=%d approvals=%d handoffs=%d",
			proposals.Total,
			approvals.Total,
			handoffs.Total,
		)
	}
	for _, item := range runs.Items {
		if item.TicketNumber == "" || item.TicketTitle == "" {
			t.Fatalf("run omitted ticket projection: %+v", item)
		}
	}
	for _, item := range proposals.Items {
		if item.TicketNumber == "" || item.TicketTitle == "" {
			t.Fatalf("proposal omitted ticket projection: %+v", item)
		}
	}
	for _, item := range approvals.Items {
		if item.TicketNumber == "" || item.TicketTitle == "" {
			t.Fatalf("approval omitted ticket projection: %+v", item)
		}
	}
	for _, item := range handoffs.Items {
		if item.TicketNumber == "" || item.TicketTitle == "" {
			t.Fatalf("handoff omitted ticket projection: %+v", item)
		}
	}

	for name, check := range map[string]func() error{
		"run": func() error {
			_, err := fixture.service.GetAgentRun(
				fixture.context,
				fixture.access,
				fixture.runForeign.ID,
			)
			return err
		},
		"proposal": func() error {
			_, err := fixture.service.GetActionProposal(
				fixture.context,
				fixture.access,
				fixture.proposalForeign.ID,
			)
			return err
		},
		"approval": func() error {
			_, err := fixture.service.GetApprovalTask(
				fixture.context,
				fixture.access,
				fixture.approvalForeign.ID,
			)
			return err
		},
		"handoff": func() error {
			_, err := fixture.service.GetHandoff(
				fixture.context,
				fixture.access,
				fixture.handoffForeign.ID,
			)
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			if err := check(); !errors.Is(err, ErrCollaborationNotFound) {
				t.Fatalf("cross-project detail error = %v", err)
			}
		})
	}

	run, err := fixture.service.GetAgentRun(
		fixture.context,
		fixture.access,
		fixture.runOwn.ID,
	)
	if err != nil {
		t.Fatal(err)
	}
	assertSafeCollaborationJSON(t, run)

	proposal, err := fixture.service.GetActionProposal(
		fixture.context,
		fixture.access,
		fixture.proposalOwn.ID,
	)
	if err != nil {
		t.Fatal(err)
	}
	assertSafeCollaborationJSON(t, proposal)
	if proposal.Preview["summary"] != "回复请求人" {
		t.Fatalf("safe preview = %#v", proposal.Preview)
	}
	if _, exists := proposal.Preview["policy_snapshot"]; exists {
		t.Fatalf("policy snapshot leaked in preview: %#v", proposal.Preview)
	}
	nested, _ := proposal.Preview["nested"].(map[string]any)
	if nested["safe"] != "保留" {
		t.Fatalf("nested safe preview = %#v", nested)
	}
	if _, exists := nested["secret"]; exists {
		t.Fatalf("nested secret leaked: %#v", nested)
	}

	handoff, err := fixture.service.GetHandoff(
		fixture.context,
		fixture.access,
		fixture.handoffOwn.ID,
	)
	if err != nil {
		t.Fatal(err)
	}
	assertSafeCollaborationJSON(t, handoff)

	approval, err := fixture.service.GetApprovalTask(
		fixture.context,
		fixture.access,
		fixture.approvalOwn.ID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if approval.ApprovalsRecorded != 1 || approval.RejectionsRecorded != 0 {
		t.Fatalf("scoped decision counts = %+v", approval)
	}
}

func TestAgentCollaborationQueriesRequesterOnlySeesOwnTickets(t *testing.T) {
	fixture := newCollaborationQueryFixture(t)
	runs, err := fixture.service.ListAgentRuns(
		fixture.context,
		fixture.requester,
		CollaborationPagination{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if runs.Total != 1 ||
		len(runs.Items) != 1 ||
		runs.Items[0].ID != fixture.runOwn.ID {
		t.Fatalf("requester runs = %+v", runs)
	}
	if _, err := fixture.service.GetAgentRun(
		fixture.context,
		fixture.requester,
		fixture.runOther.ID,
	); !errors.Is(err, ErrCollaborationNotFound) {
		t.Fatalf("other requester ticket error = %v", err)
	}
}

func TestAgentCollaborationQueriesRejectMismatchedTrustedAccess(t *testing.T) {
	fixture := newCollaborationQueryFixture(t)
	mismatched := fixture.access
	mismatched.Scope.ProjectID++
	if _, err := fixture.service.ListAgentRuns(
		fixture.context,
		mismatched,
		CollaborationPagination{},
	); !errors.Is(err, ErrCollaborationAccessDenied) {
		t.Fatalf("mismatched access error = %v", err)
	}
	if _, err := fixture.service.ListAgentRuns(
		context.Background(),
		fixture.access,
		CollaborationPagination{},
	); !errors.Is(err, ErrCollaborationAccessDenied) {
		t.Fatalf("missing operation context error = %v", err)
	}
}

func assertSafeCollaborationJSON(t *testing.T, value any) {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	payload := strings.ToLower(string(encoded))
	for _, forbidden := range []string{
		"action_payload",
		"evidence_digest",
		"policy_snapshot",
		"principal_id",
		"proposal_digest",
		"private-evidence-digest",
		"principal-secret",
		"never-return",
	} {
		if strings.Contains(payload, forbidden) {
			t.Fatalf("unsafe field %q leaked in %s", forbidden, payload)
		}
	}
}
