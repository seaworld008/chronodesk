package services

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

type collaborationFixture struct {
	db       *gorm.DB
	service  *AgentCollaborationService
	agentCtx context.Context
	humanCtx context.Context
	ticket   models.Ticket
	run      *models.AgentRun
	human    models.User
}

func newCollaborationFixture(t *testing.T) collaborationFixture {
	t.Helper()
	db := openTestDB(t)
	if err := db.AutoMigrate(
		&models.User{},
		&models.ServicePrincipal{},
		&models.AgentCredential{},
		&models.AgentPolicy{},
		&models.PolicyDecision{},
		&models.IdempotencyRecord{},
		&models.Ticket{},
		&models.TicketComment{},
		&models.TicketHistory{},
		&models.TicketLease{},
		&models.DomainEvent{},
		&models.OutboxDelivery{},
		&models.AgentTask{},
		&models.AgentRun{},
		&models.ActionProposal{},
		&models.ApprovalTask{},
		&models.ApprovalDecision{},
		&models.Handoff{},
		&models.EvidenceReference{},
	); err != nil {
		t.Fatal(err)
	}
	human := models.User{
		Username:     "approver",
		Email:        "approver@example.test",
		PasswordHash: "hash",
		PlatformRole: models.PlatformRoleMember,
		Status:       models.UserStatusActive,
	}
	if err := db.Create(&human).Error; err != nil {
		t.Fatal(err)
	}
	principalID := "00000000-0000-7000-8000-000000009001"
	scopes, err := json.Marshal(models.SupportedAgentScopes)
	if err != nil {
		t.Fatal(err)
	}
	principal := models.ServicePrincipal{
		ID:     principalID,
		Name:   "collaboration-agent",
		Status: models.ServicePrincipalStatusActive,
		Scopes: datatypes.JSON(scopes),
	}
	if err := db.Create(&principal).Error; err != nil {
		t.Fatal(err)
	}
	credential := models.AgentCredential{
		ID:                 "test-credential",
		ServicePrincipalID: principalID,
		Name:               "collaboration-test",
		SecretHash:         "not-used-by-service-test",
		Status:             models.AgentCredentialStatusActive,
		ExpiresAt:          time.Now().UTC().Add(time.Hour),
	}
	if err := db.Create(&credential).Error; err != nil {
		t.Fatal(err)
	}
	agentCtx := testProjectOperationContext(
		t,
		db,
		models.ServicePrincipalActor(principalID),
	)
	agentOperation, err := OperationContextFromContext(agentCtx)
	if err != nil {
		t.Fatal(err)
	}
	humanCtx, err := WithOperationContext(
		context.Background(),
		OperationContext{
			Scope:  agentOperation.Scope,
			Actor:  models.HumanActor(human.ID),
			Source: SourceProtocolHumanREST,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	var queue models.Queue
	if err := db.Where(
		"project_id = ? AND is_default = ?",
		agentOperation.Scope.ProjectID,
		true,
	).First(&queue).Error; err != nil {
		t.Fatal(err)
	}
	ticket := models.Ticket{
		OrganizationID:       agentOperation.Scope.OrganizationID,
		ProjectID:            agentOperation.Scope.ProjectID,
		QueueID:              queue.ID,
		RequestTypeVersionID: defaultRequestTypeRequestVersionID,
		WorkflowVersionID:    defaultWorkflowVersionID,
		TicketNumber:         "TEST-1",
		Title:                "Agent collaboration",
		Description:          "approval and takeover",
		Type:                 models.TicketTypeRequest,
		Priority:             models.TicketPriorityHigh,
		Status:               models.TicketStatusOpen,
		Source:               models.TicketSourceAgent,
		Version:              1,
		CreatedByActorType:   models.ActorTypeServicePrincipal,
		CreatedByActorID:     principalID,
	}
	if err := db.Create(&ticket).Error; err != nil {
		t.Fatal(err)
	}
	native := NewAgentNativeService(db)
	service, err := NewAgentCollaborationService(db, native)
	if err != nil {
		t.Fatal(err)
	}
	run, err := service.StartAgentRun(agentCtx, StartAgentRunInput{
		TicketID:       ticket.ID,
		PrincipalID:    principalID,
		ModelProvider:  "test",
		ModelName:      "test-model",
		PromptVersion:  "prompt-v1",
		ToolsetVersion: "tools-v1",
		PolicyVersion:  "policy-v1",
		PolicySnapshot: map[string]any{"external_data": false},
		InputSummary:   "classify ticket",
	})
	if err != nil {
		t.Fatalf("start agent run: %v", err)
	}
	return collaborationFixture{
		db:       db,
		service:  service,
		agentCtx: agentCtx,
		humanCtx: humanCtx,
		ticket:   ticket,
		run:      run,
		human:    human,
	}
}

func TestAgentCollaborationApprovalBindsDigestPolicyAndTicketVersion(t *testing.T) {
	fixture := newCollaborationFixture(t)
	lease := fixture.createLease(t)
	created, err := fixture.service.CreateActionProposal(
		fixture.agentCtx,
		CreateActionProposalInput{
			AgentRunID: fixture.run.ID,
			TicketID:   fixture.ticket.ID,
			ActionType: ActionTypeTicketUpdate,
			ActionPayload: map[string]any{
				"lease_id": lease.ID,
				"title":    "Approved Agent update",
			},
			ChangePreview:  map[string]any{"title": "Approved Agent update"},
			EvidenceDigest: "evidence-hash",
			RiskLevel:      models.ActionRiskHigh,
			TargetVersion:  fixture.ticket.Version,
			PolicyVersion:  "policy-v1",
			ExpiresIn:      time.Hour,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if created.Approval == nil ||
		created.Approval.ProposalDigest != created.Proposal.ProposalDigest {
		t.Fatalf("approval did not bind proposal digest: %+v", created)
	}
	approval, err := fixture.service.DecideApproval(
		fixture.humanCtx,
		DecideApprovalInput{
			ApprovalTaskID: created.Approval.ID,
			Decision:       models.ApprovalDecisionApprove,
			Comment:        "approved with evidence",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if approval.Status != models.ApprovalTaskApproved {
		t.Fatalf("approval status = %s", approval.Status)
	}
	executed, err := fixture.service.ExecuteApprovedProposal(
		fixture.agentCtx,
		ExecuteApprovedProposalInput{
			ProposalID:      created.Proposal.ID,
			IdempotencyKey:  "same-request",
			AuthorizedScope: models.ScopeTicketsUpdate,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if executed.Proposal.Status != models.ActionProposalExecuted ||
		executed.Proposal.ExecutedAt == nil ||
		executed.ActionReceipt.ResourceVersion != 2 {
		t.Fatalf("proposal not consumed: %+v", executed)
	}
	replayed, err := fixture.service.ExecuteApprovedProposal(
		fixture.agentCtx,
		ExecuteApprovedProposalInput{
			ProposalID:      created.Proposal.ID,
			IdempotencyKey:  "same-request",
			AuthorizedScope: models.ScopeTicketsUpdate,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !replayed.Replayed ||
		replayed.ActionReceipt.EventID != executed.ActionReceipt.EventID ||
		replayed.ExecutionEventID != executed.ExecutionEventID {
		t.Fatalf("idempotent replay changed execution result: %+v", replayed)
	}
	var eventCount int64
	if err := fixture.db.Model(&models.DomainEvent{}).
		Where(
			"organization_id = ? AND project_id = ? AND type = ?",
			fixture.ticket.OrganizationID,
			fixture.ticket.ProjectID,
			"io.chronodesk.agent.action.executed.v1",
		).
		Count(&eventCount).Error; err != nil {
		t.Fatal(err)
	}
	if eventCount != 1 {
		t.Fatalf("executed event count = %d", eventCount)
	}
}

func TestAgentCollaborationInvalidatesApprovalAfterTicketVersionChange(t *testing.T) {
	fixture := newCollaborationFixture(t)
	created, err := fixture.service.CreateActionProposal(
		fixture.agentCtx,
		CreateActionProposalInput{
			AgentRunID: fixture.run.ID,
			TicketID:   fixture.ticket.ID,
			ActionType: ActionTypeTicketUpdate,
			ActionPayload: map[string]any{
				"lease_id": "approval-version-test-lease",
				"priority": models.TicketPriorityUrgent,
			},
			ChangePreview:  map[string]any{"priority": "urgent"},
			EvidenceDigest: "delete-evidence",
			RiskLevel:      models.ActionRiskCritical,
			TargetVersion:  fixture.ticket.Version,
			PolicyVersion:  "policy-v1",
			ExpiresIn:      time.Hour,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.Model(&models.Ticket{}).
		Where("id = ?", fixture.ticket.ID).
		Update("version", fixture.ticket.Version+1).Error; err != nil {
		t.Fatal(err)
	}
	_, err = fixture.service.DecideApproval(
		fixture.humanCtx,
		DecideApprovalInput{
			ApprovalTaskID: created.Approval.ID,
			Decision:       models.ApprovalDecisionApprove,
		},
	)
	if !errors.Is(err, ErrApprovalInvalidated) {
		t.Fatalf("decision error = %v", err)
	}
	var task models.ApprovalTask
	if err := fixture.db.First(&task, "id = ?", created.Approval.ID).Error; err != nil {
		t.Fatal(err)
	}
	if task.Status != models.ApprovalTaskInvalidated {
		t.Fatalf("persisted status = %s", task.Status)
	}
}

func TestTakeoverAtomicallyRevokesAgentClaimLeaseAndAssignment(t *testing.T) {
	fixture := newCollaborationFixture(t)
	now := time.Now().UTC()
	task := models.AgentTask{
		ID:                 "task-takeover",
		OrganizationID:     fixture.ticket.OrganizationID,
		ProjectID:          fixture.ticket.ProjectID,
		ContextID:          "context-takeover",
		LinkedTicketID:     &fixture.ticket.ID,
		OwnerActorType:     models.ActorTypeServicePrincipal,
		OwnerActorID:       fixture.run.PrincipalID,
		State:              models.A2ATaskStateWorking,
		StatusTimestamp:    now,
		Version:            1,
		ExecutionClaimID:   "claim-1",
		ExecutionMessageID: "message-1",
		ExecutionExpiresAt: pointerTime(now.Add(time.Minute)),
	}
	if err := fixture.db.Create(&task).Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.Model(&models.AgentRun{}).
		Where("id = ?", fixture.run.ID).
		Update("agent_task_id", task.ID).Error; err != nil {
		t.Fatal(err)
	}
	lease := models.TicketLease{
		ID:              uuid.NewString(),
		TicketID:        fixture.ticket.ID,
		HolderActorType: models.ActorTypeServicePrincipal,
		HolderActorID:   fixture.run.PrincipalID,
		TicketVersion:   fixture.ticket.Version,
		ExpiresAt:       now.Add(time.Minute),
		LastHeartbeatAt: now,
	}
	if err := fixture.db.Create(&lease).Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.Model(&models.Ticket{}).
		Where("id = ?", fixture.ticket.ID).
		Updates(map[string]any{
			"assigned_to_actor_type": models.ActorTypeServicePrincipal,
			"assigned_to_actor_id":   fixture.run.PrincipalID,
		}).Error; err != nil {
		t.Fatal(err)
	}

	handoff, err := fixture.service.TakeoverAgentRun(
		fixture.humanCtx,
		TakeoverAgentRunInput{
			AgentRunID:         fixture.run.ID,
			Reason:             "需要人工确认外部影响",
			CompletedSummary:   "已完成分类与证据收集",
			MissingInformation: []string{"客户确认"},
			EvidenceDigest:     "handoff-evidence",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if handoff.Direction != models.HandoffAgentToHuman {
		t.Fatalf("handoff = %+v", handoff)
	}
	var persistedLease models.TicketLease
	if err := fixture.db.First(&persistedLease, "id = ?", lease.ID).Error; err != nil {
		t.Fatal(err)
	}
	if persistedLease.ReleasedAt == nil {
		t.Fatal("ticket lease remains active")
	}
	var persistedTask models.AgentTask
	if err := fixture.db.First(&persistedTask, "id = ?", task.ID).Error; err != nil {
		t.Fatal(err)
	}
	if persistedTask.ExecutionClaimID != "" ||
		persistedTask.State != models.A2ATaskStateCanceled {
		t.Fatalf("task execution authority remains: %+v", persistedTask)
	}
	var persistedTicket models.Ticket
	if err := fixture.db.First(&persistedTicket, fixture.ticket.ID).Error; err != nil {
		t.Fatal(err)
	}
	if persistedTicket.AssignedToActorType != models.ActorTypeHuman ||
		persistedTicket.AssignedToActorID != models.HumanActor(fixture.human.ID).ID ||
		persistedTicket.Version != fixture.ticket.Version+1 {
		t.Fatalf("ticket takeover projection = %+v", persistedTicket)
	}
	var persistedRun models.AgentRun
	if err := fixture.db.First(&persistedRun, "id = ?", fixture.run.ID).Error; err != nil {
		t.Fatal(err)
	}
	if persistedRun.Status != models.AgentRunStatusTakenOver {
		t.Fatalf("run status = %s", persistedRun.Status)
	}
}

func pointerTime(value time.Time) *time.Time {
	return &value
}

func (fixture collaborationFixture) createLease(
	t *testing.T,
) models.TicketLease {
	t.Helper()
	now := time.Now().UTC()
	lease := models.TicketLease{
		ID:              uuid.NewString(),
		OrganizationID:  fixture.ticket.OrganizationID,
		ProjectID:       fixture.ticket.ProjectID,
		TicketID:        fixture.ticket.ID,
		HolderActorType: models.ActorTypeServicePrincipal,
		HolderActorID:   fixture.run.PrincipalID,
		TicketVersion:   fixture.ticket.Version,
		ExpiresAt:       now.Add(time.Minute),
		LastHeartbeatAt: now,
	}
	if err := fixture.db.Create(&lease).Error; err != nil {
		t.Fatal(err)
	}
	return lease
}

func TestActionExecutorRegistryRejectsUnknownFieldsAndActions(t *testing.T) {
	fixture := newCollaborationFixture(t)
	_, err := fixture.service.CreateActionProposal(
		fixture.agentCtx,
		CreateActionProposalInput{
			AgentRunID: fixture.run.ID,
			TicketID:   fixture.ticket.ID,
			ActionType: ActionTypeTicketUpdate,
			ActionPayload: map[string]any{
				"lease_id": "strict-schema-lease",
				"title":    "valid",
				"callback": "https://attacker.invalid",
			},
			ChangePreview:  map[string]any{"title": "valid"},
			EvidenceDigest: "strict-schema-evidence",
			RiskLevel:      models.ActionRiskLow,
			TargetVersion:  fixture.ticket.Version,
			PolicyVersion:  "policy-v1",
			ExpiresIn:      time.Hour,
		},
	)
	if !errors.Is(err, ErrInvalidProposalPayload) {
		t.Fatalf("unknown payload field error = %v", err)
	}
	_, err = fixture.service.CreateActionProposal(
		fixture.agentCtx,
		CreateActionProposalInput{
			AgentRunID:     fixture.run.ID,
			TicketID:       fixture.ticket.ID,
			ActionType:     "script.execute",
			ActionPayload:  map[string]any{"script": "rm -rf /"},
			ChangePreview:  map[string]any{},
			EvidenceDigest: "unsupported-action-evidence",
			RiskLevel:      models.ActionRiskCritical,
			TargetVersion:  fixture.ticket.Version,
			PolicyVersion:  "policy-v1",
			ExpiresIn:      time.Hour,
		},
	)
	if !errors.Is(err, ErrUnsupportedProposalAction) {
		t.Fatalf("unsupported action error = %v", err)
	}
}
