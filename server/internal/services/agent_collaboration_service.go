package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/scopeddb"
	"gorm.io/datatypes"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrProposalApprovalRequired = errors.New("action proposal requires approval")
	ErrApprovalExpired          = errors.New("approval has expired")
	ErrApprovalInvalidated      = errors.New("approval is no longer valid")
	ErrProposalNotExecutable    = errors.New("action proposal is not executable")
)

type AgentCollaborationService struct {
	db      *gorm.DB
	native  *AgentNativeService
	actions *ActionExecutorRegistry
	now     func() time.Time
}

func NewAgentCollaborationService(
	db *gorm.DB,
	native *AgentNativeService,
) (*AgentCollaborationService, error) {
	if db == nil {
		return nil, errors.New("collaboration database is required")
	}
	if native == nil {
		return nil, errors.New("agent-native event service is required")
	}
	actions, err := NewActionExecutorRegistry(native)
	if err != nil {
		return nil, err
	}
	return &AgentCollaborationService{
		db:      db,
		native:  native,
		actions: actions,
		now:     time.Now,
	}, nil
}

type StartAgentRunInput struct {
	TicketID       uint
	PrincipalID    string
	AgentTaskID    string
	ModelProvider  string
	ModelName      string
	PromptVersion  string
	ToolsetVersion string
	PolicyVersion  string
	PolicySnapshot map[string]any
	InputSummary   string
}

func (service *AgentCollaborationService) StartAgentRun(
	ctx context.Context,
	input StartAgentRunInput,
) (*models.AgentRun, error) {
	operation, err := OperationContextFromContext(ctx)
	if err != nil {
		return nil, err
	}
	if operation.Actor.Type != models.ActorTypeServicePrincipal ||
		operation.Actor.ID != strings.TrimSpace(input.PrincipalID) {
		return nil, errors.New("agent run actor must match the service principal")
	}
	if input.TicketID == 0 ||
		strings.TrimSpace(input.ModelProvider) == "" ||
		strings.TrimSpace(input.ModelName) == "" ||
		strings.TrimSpace(input.PromptVersion) == "" ||
		strings.TrimSpace(input.ToolsetVersion) == "" ||
		strings.TrimSpace(input.PolicyVersion) == "" {
		return nil, errors.New("complete run version metadata is required")
	}
	policySnapshot, err := json.Marshal(input.PolicySnapshot)
	if err != nil {
		return nil, fmt.Errorf("encode policy snapshot: %w", err)
	}

	run := &models.AgentRun{
		OrganizationID: operation.Scope.OrganizationID,
		ProjectID:      operation.Scope.ProjectID,
		TicketID:       input.TicketID,
		PrincipalID:    operation.Actor.ID,
		AgentTaskID:    strings.TrimSpace(input.AgentTaskID),
		Status:         models.AgentRunStatusRunning,
		ModelProvider:  strings.TrimSpace(input.ModelProvider),
		ModelName:      strings.TrimSpace(input.ModelName),
		PromptVersion:  strings.TrimSpace(input.PromptVersion),
		ToolsetVersion: strings.TrimSpace(input.ToolsetVersion),
		PolicyVersion:  strings.TrimSpace(input.PolicyVersion),
		PolicySnapshot: datatypes.JSON(policySnapshot),
		InputSummary:   strings.TrimSpace(input.InputSummary),
	}
	startedAt := service.now().UTC()
	run.StartedAt = &startedAt

	err = transactionForContext(ctx, service.db, func(tx *gorm.DB) error {
		if _, ticketErr := scopedTicketForUpdate(
			ctx,
			tx,
			operation.Scope,
			input.TicketID,
		); ticketErr != nil {
			return ticketErr
		}
		if createErr := tx.WithContext(ctx).Create(run).Error; createErr != nil {
			return fmt.Errorf("create agent run: %w", createErr)
		}
		_, eventErr := service.native.AppendDomainEventTx(
			ctx,
			tx,
			DomainEventInput{
				Type:    "io.chronodesk.agent.run.started.v1",
				Subject: "agent-run/" + run.ID,
				Actor:   operation.Actor,
				Data: map[string]any{
					"organization_id": operation.Scope.OrganizationID,
					"project_id":      operation.Scope.ProjectID,
					"ticket_id":       run.TicketID,
					"agent_run_id":    run.ID,
					"principal_id":    run.PrincipalID,
					"policy_version":  run.PolicyVersion,
				},
				TraceID:       operation.TraceID,
				CorrelationID: operation.CorrelationID,
			},
			nil,
		)
		return eventErr
	})
	if err != nil {
		return nil, err
	}
	return run, nil
}

type CreateActionProposalInput struct {
	AgentRunID     string
	TicketID       uint
	ActionType     string
	ActionPayload  map[string]any
	ChangePreview  map[string]any
	EvidenceDigest string
	RiskLevel      models.ActionRiskLevel
	TargetVersion  uint64
	PolicyVersion  string
	ExpiresIn      time.Duration
	DoubleApproval bool
}

type CreateActionProposalResult struct {
	Proposal *models.ActionProposal `json:"proposal"`
	Approval *models.ApprovalTask   `json:"approval,omitempty"`
}

func (service *AgentCollaborationService) CreateActionProposal(
	ctx context.Context,
	input CreateActionProposalInput,
) (*CreateActionProposalResult, error) {
	operation, err := OperationContextFromContext(ctx)
	if err != nil {
		return nil, err
	}
	if operation.Actor.Type != models.ActorTypeServicePrincipal {
		return nil, errors.New("only a service principal may create an Agent action proposal")
	}
	if input.TicketID == 0 ||
		strings.TrimSpace(input.AgentRunID) == "" ||
		strings.TrimSpace(input.ActionType) == "" ||
		strings.TrimSpace(input.EvidenceDigest) == "" ||
		input.TargetVersion == 0 ||
		strings.TrimSpace(input.PolicyVersion) == "" ||
		!input.RiskLevel.IsValid() {
		return nil, errors.New("complete proposal control data is required")
	}
	if input.ExpiresIn <= 0 || input.ExpiresIn > 24*time.Hour {
		input.ExpiresIn = 30 * time.Minute
	}
	actionPayload, err := service.actions.CanonicalizePayload(
		input.ActionType,
		input.ActionPayload,
	)
	if err != nil {
		return nil, err
	}
	changePreview, err := json.Marshal(input.ChangePreview)
	if err != nil {
		return nil, fmt.Errorf("encode proposal preview: %w", err)
	}
	now := service.now().UTC()
	proposal := &models.ActionProposal{
		OrganizationID: operation.Scope.OrganizationID,
		ProjectID:      operation.Scope.ProjectID,
		TicketID:       input.TicketID,
		AgentRunID:     strings.TrimSpace(input.AgentRunID),
		ProposedByType: operation.Actor.Type,
		ProposedByID:   operation.Actor.ID,
		ActionType:     strings.TrimSpace(input.ActionType),
		ActionPayload:  datatypes.JSON(actionPayload),
		ChangePreview:  datatypes.JSON(changePreview),
		EvidenceDigest: strings.TrimSpace(input.EvidenceDigest),
		RiskLevel:      input.RiskLevel,
		TargetVersion:  input.TargetVersion,
		PolicyVersion:  strings.TrimSpace(input.PolicyVersion),
		Status:         models.ActionProposalPending,
		ExpiresAt:      now.Add(input.ExpiresIn),
	}
	requiresApproval := actionRequiresApproval(proposal.ActionType, proposal.RiskLevel)
	var approval *models.ApprovalTask

	err = transactionForContext(ctx, service.db, func(tx *gorm.DB) error {
		ticket, ticketErr := scopedTicketForUpdate(
			ctx,
			tx,
			operation.Scope,
			input.TicketID,
		)
		if ticketErr != nil {
			return ticketErr
		}
		if ticket.Version != input.TargetVersion {
			return ErrApprovalInvalidated
		}
		var run models.AgentRun
		if runErr := tx.WithContext(ctx).
			Where(
				"id = ? AND organization_id = ? AND project_id = ? AND ticket_id = ? AND principal_id = ?",
				proposal.AgentRunID,
				operation.Scope.OrganizationID,
				operation.Scope.ProjectID,
				input.TicketID,
				operation.Actor.ID,
			).
			First(&run).Error; runErr != nil {
			return fmt.Errorf("load scoped agent run: %w", runErr)
		}
		if run.Status.IsTerminal() {
			return errors.New("terminal agent run cannot create a proposal")
		}
		if run.PolicyVersion != proposal.PolicyVersion {
			return errors.New(
				"proposal policy version must match the Agent run",
			)
		}
		if createErr := tx.WithContext(ctx).Create(proposal).Error; createErr != nil {
			return fmt.Errorf("create action proposal: %w", createErr)
		}
		if requiresApproval {
			required := 1
			if input.DoubleApproval || input.RiskLevel == models.ActionRiskCritical {
				required = 2
			}
			approval = &models.ApprovalTask{
				OrganizationID:    operation.Scope.OrganizationID,
				ProjectID:         operation.Scope.ProjectID,
				TicketID:          input.TicketID,
				ProposalID:        proposal.ID,
				ProposalDigest:    proposal.ProposalDigest,
				TargetVersion:     input.TargetVersion,
				PolicyVersion:     proposal.PolicyVersion,
				RequiredApprovals: required,
				Status:            models.ApprovalTaskPending,
				ExpiresAt:         proposal.ExpiresAt,
			}
			if createErr := tx.WithContext(ctx).Create(approval).Error; createErr != nil {
				return fmt.Errorf("create approval task: %w", createErr)
			}
			if updateErr := tx.WithContext(ctx).Model(&models.AgentRun{}).
				Where(
					"id = ? AND organization_id = ? AND project_id = ?",
					run.ID,
					operation.Scope.OrganizationID,
					operation.Scope.ProjectID,
				).
				Update("status", models.AgentRunStatusWaitingApproval).Error; updateErr != nil {
				return fmt.Errorf("mark agent run waiting for approval: %w", updateErr)
			}
		}
		_, eventErr := service.native.AppendDomainEventTx(
			ctx,
			tx,
			DomainEventInput{
				Type:    "io.chronodesk.agent.action.proposed.v1",
				Subject: "ticket/" + fmt.Sprint(input.TicketID),
				Actor:   operation.Actor,
				Data: map[string]any{
					"organization_id":   operation.Scope.OrganizationID,
					"project_id":        operation.Scope.ProjectID,
					"ticket_id":         input.TicketID,
					"agent_run_id":      proposal.AgentRunID,
					"proposal_id":       proposal.ID,
					"proposal_digest":   proposal.ProposalDigest,
					"risk_level":        proposal.RiskLevel,
					"approval_required": requiresApproval,
				},
				ResourceVersion: ticket.Version,
				TraceID:         operation.TraceID,
				CorrelationID:   operation.CorrelationID,
			},
			nil,
		)
		return eventErr
	})
	if err != nil {
		return nil, err
	}
	return &CreateActionProposalResult{
		Proposal: proposal,
		Approval: approval,
	}, nil
}

type DecideApprovalInput struct {
	ApprovalTaskID string
	Decision       models.ApprovalDecisionValue
	Comment        string
}

func (service *AgentCollaborationService) DecideApproval(
	ctx context.Context,
	input DecideApprovalInput,
) (*models.ApprovalTask, error) {
	operation, err := OperationContextFromContext(ctx)
	if err != nil {
		return nil, err
	}
	if operation.Actor.Type != models.ActorTypeHuman {
		return nil, errors.New("approval decision requires a human actor")
	}
	if strings.TrimSpace(input.ApprovalTaskID) == "" {
		return nil, errors.New("approval task id is required")
	}
	now := service.now().UTC()
	var result models.ApprovalTask
	var outcomeErr error

	err = transactionForContext(ctx, service.db, func(tx *gorm.DB) error {
		if loadErr := tx.WithContext(ctx).
			Clauses(clause.Locking{Strength: "UPDATE"}).
			Where(
				"id = ? AND organization_id = ? AND project_id = ?",
				input.ApprovalTaskID,
				operation.Scope.OrganizationID,
				operation.Scope.ProjectID,
			).
			First(&result).Error; loadErr != nil {
			return loadErr
		}
		if result.Status != models.ApprovalTaskPending {
			return ErrApprovalInvalidated
		}
		if !result.ExpiresAt.After(now) {
			if expireErr := service.expireApprovalTx(
				ctx,
				tx,
				&result,
				now,
			); expireErr != nil {
				return expireErr
			}
			outcomeErr = ErrApprovalExpired
			return nil
		}
		ticket, ticketErr := scopedTicketForUpdate(
			ctx,
			tx,
			operation.Scope,
			result.TicketID,
		)
		if ticketErr != nil {
			return ticketErr
		}
		var proposal models.ActionProposal
		if proposalErr := tx.WithContext(ctx).
			Clauses(clause.Locking{Strength: "UPDATE"}).
			Where(
				"id = ? AND organization_id = ? AND project_id = ?",
				result.ProposalID,
				operation.Scope.OrganizationID,
				operation.Scope.ProjectID,
			).
			First(&proposal).Error; proposalErr != nil {
			return proposalErr
		}
		if invalidProposalApproval(&proposal, &result, ticket.Version, now) {
			if invalidateErr := service.invalidateApprovalTx(
				ctx,
				tx,
				&proposal,
				&result,
				now,
			); invalidateErr != nil {
				return invalidateErr
			}
			outcomeErr = ErrApprovalInvalidated
			return nil
		}
		decision := &models.ApprovalDecision{
			OrganizationID: operation.Scope.OrganizationID,
			ProjectID:      operation.Scope.ProjectID,
			ApprovalTaskID: result.ID,
			ActorType:      operation.Actor.Type,
			ActorID:        operation.Actor.ID,
			Decision:       input.Decision,
			Comment:        strings.TrimSpace(input.Comment),
			ProposalDigest: result.ProposalDigest,
		}
		if createErr := tx.WithContext(ctx).Create(decision).Error; createErr != nil {
			return fmt.Errorf("record approval decision: %w", createErr)
		}
		eventType := "io.chronodesk.agent.approval.recorded.v1"
		if input.Decision == models.ApprovalDecisionReject {
			result.Status = models.ApprovalTaskRejected
			proposal.Status = models.ActionProposalRejected
			result.CompletedAt = &now
			eventType = "io.chronodesk.agent.approval.rejected.v1"
		} else {
			var approvals int64
			if countErr := tx.WithContext(ctx).Model(&models.ApprovalDecision{}).
				Where(
					"approval_task_id = ? AND organization_id = ? AND project_id = ? AND decision = ?",
					result.ID,
					operation.Scope.OrganizationID,
					operation.Scope.ProjectID,
					models.ApprovalDecisionApprove,
				).
				Count(&approvals).Error; countErr != nil {
				return countErr
			}
			if approvals >= int64(result.RequiredApprovals) {
				result.Status = models.ApprovalTaskApproved
				proposal.Status = models.ActionProposalApproved
				result.CompletedAt = &now
				eventType = "io.chronodesk.agent.approval.approved.v1"
			}
		}
		updateApproval := tx.WithContext(ctx).Model(&models.ApprovalTask{}).
			Where(
				"id = ? AND organization_id = ? AND project_id = ?",
				result.ID,
				operation.Scope.OrganizationID,
				operation.Scope.ProjectID,
			).
			Updates(map[string]any{
				"status":       result.Status,
				"completed_at": result.CompletedAt,
			})
		if updateApproval.Error != nil {
			return updateApproval.Error
		}
		if updateApproval.RowsAffected != 1 {
			return ErrApprovalInvalidated
		}
		if updateErr := tx.WithContext(ctx).Model(&models.ActionProposal{}).
			Where(
				"id = ? AND organization_id = ? AND project_id = ?",
				proposal.ID,
				operation.Scope.OrganizationID,
				operation.Scope.ProjectID,
			).
			Update("status", proposal.Status).Error; updateErr != nil {
			return updateErr
		}
		_, eventErr := service.native.AppendDomainEventTx(
			ctx,
			tx,
			DomainEventInput{
				Type:    eventType,
				Subject: "approval-task/" + result.ID,
				Actor:   operation.Actor,
				Data: map[string]any{
					"organization_id": operation.Scope.OrganizationID,
					"project_id":      operation.Scope.ProjectID,
					"ticket_id":       result.TicketID,
					"approval_id":     result.ID,
					"proposal_id":     result.ProposalID,
					"proposal_digest": result.ProposalDigest,
					"decision":        input.Decision,
					"status":          result.Status,
				},
				ResourceVersion: ticket.Version,
				TraceID:         operation.TraceID,
				CorrelationID:   operation.CorrelationID,
			},
			nil,
		)
		return eventErr
	})
	if err != nil {
		return nil, err
	}
	if outcomeErr != nil {
		return nil, outcomeErr
	}
	return &result, nil
}

const proposalExecutionIdempotencyTTL = 24 * time.Hour

// ExecuteApprovedProposalInput contains only trusted Adapter control data.
// AuthorizedScope must be resolved from the authenticated OAuth token; it is
// never decoded from a request body or Proposal payload.
type ExecuteApprovedProposalInput struct {
	ProposalID      string
	IdempotencyKey  string
	AuthorizedScope string
}

type ExecuteApprovedProposalResult struct {
	Proposal         *models.ActionProposal `json:"proposal"`
	ActionReceipt    OperationReceipt       `json:"action_receipt"`
	ExecutionEventID string                 `json:"execution_event_id"`
	Replayed         bool                   `json:"replayed"`
}

// RequiredProposalExecutionScope resolves the OAuth scope for a persisted
// Proposal. The Adapter compares this result with the signed token scope, and
// ExecuteApprovedProposal compares it again with the locked Proposal.
func (service *AgentCollaborationService) RequiredProposalExecutionScope(
	ctx context.Context,
	proposalID string,
) (string, error) {
	operation, err := OperationContextFromContext(ctx)
	if err != nil {
		return "", err
	}
	if operation.Actor.Type != models.ActorTypeServicePrincipal {
		return "", errors.New(
			"proposal execution requires a service principal actor",
		)
	}
	proposalID = strings.TrimSpace(proposalID)
	if proposalID == "" {
		return "", errors.New("proposal id is required")
	}
	var proposal models.ActionProposal
	if err := service.db.WithContext(ctx).
		Where(
			"id = ? AND organization_id = ? AND project_id = ? AND proposed_by_type = ? AND proposed_by_id = ?",
			proposalID,
			operation.Scope.OrganizationID,
			operation.Scope.ProjectID,
			models.ActorTypeServicePrincipal,
			operation.Actor.ID,
		).
		First(&proposal).Error; err != nil {
		return "", err
	}
	return service.actions.RequiredScope(proposal.ActionType)
}

// ExecuteApprovedProposal executes exactly one immutable, closed-schema
// Proposal through the trusted ActionExecutorRegistry. Approval, Proposal
// digest, Agent run authority, Ticket version, Lease, policy, event, audit and
// idempotency checks all complete before the Proposal is consumed.
func (service *AgentCollaborationService) ExecuteApprovedProposal(
	ctx context.Context,
	input ExecuteApprovedProposalInput,
) (*ExecuteApprovedProposalResult, error) {
	operation, err := OperationContextFromContext(ctx)
	if err != nil {
		return nil, err
	}
	if operation.Actor.Type != models.ActorTypeServicePrincipal {
		return nil, errors.New(
			"proposal execution requires a service principal actor",
		)
	}
	input.ProposalID = strings.TrimSpace(input.ProposalID)
	input.IdempotencyKey = strings.TrimSpace(input.IdempotencyKey)
	input.AuthorizedScope = strings.TrimSpace(input.AuthorizedScope)
	if input.ProposalID == "" ||
		input.IdempotencyKey == "" ||
		input.AuthorizedScope == "" {
		return nil, errors.New(
			"proposal id, idempotency key and authorized OAuth scope are required",
		)
	}

	fingerprint, err := json.Marshal(struct {
		ProposalID string `json:"proposal_id"`
	}{
		ProposalID: input.ProposalID,
	})
	if err != nil {
		return nil, fmt.Errorf("encode proposal execution fingerprint: %w", err)
	}
	reservation, err := service.native.ReserveIdempotency(
		ctx,
		operation.Actor,
		"agent.action-proposal.execute",
		input.IdempotencyKey,
		fingerprint,
		proposalExecutionIdempotencyTTL,
	)
	if err != nil {
		return nil, err
	}
	if reservation.Replayed {
		return service.replayApprovedProposal(
			ctx,
			input,
			operation,
			reservation.Record,
		)
	}

	now := service.now().UTC()
	result := &ExecuteApprovedProposalResult{}
	err = service.native.InTransaction(
		ctx,
		func(txContext context.Context, tx *gorm.DB) error {
			execute := func(executionContext context.Context) error {
				return service.executeApprovedProposalTx(
					executionContext,
					tx.WithContext(executionContext),
					input,
					operation,
					now,
					reservation.Record.ID,
					result,
				)
			}
			if scopeddb.HasTransaction(txContext) {
				return execute(txContext)
			}
			return scopeddb.WithTransactionBinding(
				txContext,
				service.db,
				tx,
				operation.Scope,
				execute,
			)
		},
	)
	if err != nil {
		_ = service.native.FailIdempotency(
			ctx,
			reservation.Record.ID,
			proposalExecutionErrorCode(err),
		)
		return nil, err
	}
	return result, nil
}

func (service *AgentCollaborationService) executeApprovedProposalTx(
	ctx context.Context,
	tx *gorm.DB,
	input ExecuteApprovedProposalInput,
	operation OperationContext,
	now time.Time,
	idempotencyRecordID string,
	result *ExecuteApprovedProposalResult,
) error {
	var initial models.ActionProposal
	if loadErr := tx.WithContext(ctx).
		Where(
			"id = ? AND organization_id = ? AND project_id = ? AND proposed_by_type = ? AND proposed_by_id = ?",
			input.ProposalID,
			operation.Scope.OrganizationID,
			operation.Scope.ProjectID,
			models.ActorTypeServicePrincipal,
			operation.Actor.ID,
		).
		First(&initial).Error; loadErr != nil {
		return loadErr
	}
	requiredScope, err := service.actions.RequiredScope(initial.ActionType)
	if err != nil {
		return err
	}
	if requiredScope != input.AuthorizedScope {
		return fmt.Errorf(
			"%w: OAuth token does not authorize %s",
			ErrInvalidScope,
			requiredScope,
		)
	}

	// Takeover locks AgentRun before Ticket. Keeping the same order makes the
	// authority revocation and approved execution mutually exclusive without a
	// lock inversion.
	var run models.AgentRun
	if loadErr := tx.WithContext(ctx).
		Clauses(clause.Locking{Strength: "UPDATE"}).
		Where(
			"id = ? AND organization_id = ? AND project_id = ? AND ticket_id = ? AND principal_id = ?",
			initial.AgentRunID,
			operation.Scope.OrganizationID,
			operation.Scope.ProjectID,
			initial.TicketID,
			operation.Actor.ID,
		).
		First(&run).Error; loadErr != nil {
		return loadErr
	}
	if run.Status.IsTerminal() {
		return ErrProposalNotExecutable
	}

	requiresApproval := actionRequiresApproval(
		initial.ActionType,
		initial.RiskLevel,
	)
	var approval models.ApprovalTask
	if requiresApproval {
		approvalErr := tx.WithContext(ctx).
			Clauses(clause.Locking{Strength: "UPDATE"}).
			Where(
				"proposal_id = ? AND organization_id = ? AND project_id = ?",
				initial.ID,
				operation.Scope.OrganizationID,
				operation.Scope.ProjectID,
			).
			First(&approval).Error
		if approvalErr != nil {
			return ErrProposalApprovalRequired
		}
	}

	ticket, ticketErr := scopedTicketForUpdate(
		ctx,
		tx,
		operation.Scope,
		initial.TicketID,
	)
	if ticketErr != nil {
		return ticketErr
	}
	var proposal models.ActionProposal
	if loadErr := tx.WithContext(ctx).
		Clauses(clause.Locking{Strength: "UPDATE"}).
		Where(
			"id = ? AND organization_id = ? AND project_id = ? AND proposed_by_type = ? AND proposed_by_id = ?",
			initial.ID,
			operation.Scope.OrganizationID,
			operation.Scope.ProjectID,
			models.ActorTypeServicePrincipal,
			operation.Actor.ID,
		).
		First(&proposal).Error; loadErr != nil {
		return loadErr
	}
	if proposal.AgentRunID != initial.AgentRunID ||
		proposal.TicketID != initial.TicketID ||
		proposal.ActionType != initial.ActionType ||
		proposal.ProposalDigest != initial.ProposalDigest ||
		proposal.RiskLevel != initial.RiskLevel {
		return ErrApprovalInvalidated
	}
	if run.PolicyVersion != proposal.PolicyVersion ||
		!proposal.ExpiresAt.After(now) ||
		proposal.TargetVersion != ticket.Version {
		return ErrApprovalInvalidated
	}
	digest, digestErr := proposal.CalculateDigest()
	if digestErr != nil || digest != proposal.ProposalDigest {
		return ErrApprovalInvalidated
	}
	if requiresApproval {
		if approval.ProposalDigest != proposal.ProposalDigest ||
			approval.TargetVersion != proposal.TargetVersion ||
			approval.PolicyVersion != proposal.PolicyVersion ||
			approval.Status != models.ApprovalTaskApproved ||
			!approval.ExpiresAt.After(now) {
			return ErrProposalApprovalRequired
		}
		if proposal.Status != models.ActionProposalApproved {
			return ErrProposalNotExecutable
		}
	} else if proposal.Status != models.ActionProposalPending {
		return ErrProposalNotExecutable
	}

	action, err := service.actions.execute(ctx, &proposal, operation)
	if err != nil {
		return err
	}
	if action == nil || action.Event == nil ||
		action.Event.ID == "" ||
		action.Receipt.EventID != action.Event.ID ||
		action.Receipt.ResourceVersion == 0 {
		return errors.New(
			"trusted proposal executor returned an incomplete domain result",
		)
	}

	proposal.Status = models.ActionProposalExecuted
	proposal.ExecutedAt = &now
	updateProposal := tx.WithContext(ctx).Model(&models.ActionProposal{}).
		Where(
			"id = ? AND organization_id = ? AND project_id = ? AND status IN ?",
			proposal.ID,
			operation.Scope.OrganizationID,
			operation.Scope.ProjectID,
			[]models.ActionProposalStatus{
				models.ActionProposalPending,
				models.ActionProposalApproved,
			},
		).
		Updates(map[string]any{
			"status":      proposal.Status,
			"executed_at": proposal.ExecutedAt,
		})
	if updateProposal.Error != nil {
		return updateProposal.Error
	}
	if updateProposal.RowsAffected != 1 {
		return ErrProposalNotExecutable
	}
	if run.Status == models.AgentRunStatusWaitingApproval {
		updateRun := tx.WithContext(ctx).Model(&models.AgentRun{}).
			Where(
				"id = ? AND organization_id = ? AND project_id = ? AND status = ?",
				run.ID,
				operation.Scope.OrganizationID,
				operation.Scope.ProjectID,
				models.AgentRunStatusWaitingApproval,
			).
			Update("status", models.AgentRunStatusRunning)
		if updateRun.Error != nil {
			return updateRun.Error
		}
		if updateRun.RowsAffected != 1 {
			return ErrProposalNotExecutable
		}
	}

	executionEvent, err := service.native.AppendDomainEventTx(
		ctx,
		tx,
		DomainEventInput{
			Type:    "io.chronodesk.agent.action.executed.v1",
			Subject: "ticket/" + fmt.Sprint(proposal.TicketID),
			Actor:   operation.Actor,
			Data: map[string]any{
				"organization_id":    operation.Scope.OrganizationID,
				"project_id":         operation.Scope.ProjectID,
				"ticket_id":          proposal.TicketID,
				"agent_run_id":       proposal.AgentRunID,
				"proposal_id":        proposal.ID,
				"proposal_digest":    proposal.ProposalDigest,
				"action_type":        proposal.ActionType,
				"action_event_id":    action.Event.ID,
				"action_resource_id": action.Receipt.ResourceID,
				"policy_decision_id": action.Receipt.PolicyDecisionID,
			},
			ResourceVersion:  action.Receipt.ResourceVersion,
			PolicyDecisionID: action.Receipt.PolicyDecisionID,
			TraceID:          operation.TraceID,
			CorrelationID:    operation.CorrelationID,
			CausationID:      action.Event.ID,
		},
		nil,
	)
	if err != nil {
		return err
	}
	*result = ExecuteApprovedProposalResult{
		Proposal:         &proposal,
		ActionReceipt:    action.Receipt,
		ExecutionEventID: executionEvent.ID,
		Replayed:         false,
	}
	return service.native.CompleteIdempotencyTxWithTTL(
		ctx,
		tx,
		idempotencyRecordID,
		200,
		result,
		proposal.ID,
		executionEvent.ID,
		proposalExecutionIdempotencyTTL,
	)
}

func (service *AgentCollaborationService) replayApprovedProposal(
	ctx context.Context,
	input ExecuteApprovedProposalInput,
	operation OperationContext,
	record *models.IdempotencyRecord,
) (*ExecuteApprovedProposalResult, error) {
	if record == nil ||
		record.OrganizationID != operation.Scope.OrganizationID ||
		record.ProjectID != operation.Scope.ProjectID ||
		record.ActorType != operation.Actor.Type ||
		record.ActorID != operation.Actor.ID {
		return nil, ErrIdempotencyConflict
	}
	var proposal models.ActionProposal
	if err := service.db.WithContext(ctx).
		Where(
			"id = ? AND organization_id = ? AND project_id = ? AND proposed_by_type = ? AND proposed_by_id = ?",
			input.ProposalID,
			operation.Scope.OrganizationID,
			operation.Scope.ProjectID,
			models.ActorTypeServicePrincipal,
			operation.Actor.ID,
		).
		First(&proposal).Error; err != nil {
		return nil, err
	}
	requiredScope, err := service.actions.RequiredScope(proposal.ActionType)
	if err != nil {
		return nil, err
	}
	if requiredScope != input.AuthorizedScope {
		return nil, fmt.Errorf(
			"%w: OAuth token does not authorize %s",
			ErrInvalidScope,
			requiredScope,
		)
	}
	if proposal.Status != models.ActionProposalExecuted ||
		proposal.ExecutedAt == nil {
		return nil, ErrIdempotencyConflict
	}
	if err := service.actions.authorizeReplay(
		ctx,
		&proposal,
		operation,
	); err != nil {
		return nil, err
	}
	var result ExecuteApprovedProposalResult
	if len(record.ResponseBody) == 0 ||
		json.Unmarshal(record.ResponseBody, &result) != nil ||
		result.Proposal == nil ||
		result.Proposal.ID != proposal.ID ||
		result.ExecutionEventID == "" ||
		result.ActionReceipt.EventID == "" {
		return nil, ErrIdempotencyConflict
	}
	result.Proposal = &proposal
	result.Replayed = true
	return &result, nil
}

func proposalExecutionErrorCode(err error) string {
	switch {
	case errors.Is(err, ErrUnsupportedProposalAction):
		return "unsupported_proposal_action"
	case errors.Is(err, ErrInvalidProposalPayload):
		return "invalid_proposal_payload"
	case errors.Is(err, ErrProposalApprovalRequired):
		return "proposal_approval_required"
	case errors.Is(err, ErrApprovalExpired):
		return "approval_expired"
	case errors.Is(err, ErrApprovalInvalidated):
		return "approval_invalidated"
	case errors.Is(err, ErrProposalNotExecutable):
		return "proposal_not_executable"
	default:
		return AgentNativeErrorCode(err)
	}
}

type TakeoverAgentRunInput struct {
	AgentRunID         string
	Reason             string
	CompletedSummary   string
	MissingInformation []string
	EvidenceDigest     string
}

// TakeoverAgentRun atomically removes both execution authorities (A2A claim
// and Ticket lease), terminates the Agent run, assigns the Ticket to the human
// and records the handoff/event. A stale Agent therefore cannot write after
// takeover succeeds.
func (service *AgentCollaborationService) TakeoverAgentRun(
	ctx context.Context,
	input TakeoverAgentRunInput,
) (*models.Handoff, error) {
	operation, err := OperationContextFromContext(ctx)
	if err != nil {
		return nil, err
	}
	if operation.Actor.Type != models.ActorTypeHuman {
		return nil, errors.New("takeover requires a human actor")
	}
	if strings.TrimSpace(input.AgentRunID) == "" ||
		strings.TrimSpace(input.Reason) == "" ||
		strings.TrimSpace(input.EvidenceDigest) == "" {
		return nil, errors.New("run id, reason and evidence digest are required")
	}
	humanID, err := humanActorID(operation.Actor)
	if err != nil {
		return nil, err
	}
	missingInfo, err := json.Marshal(input.MissingInformation)
	if err != nil {
		return nil, fmt.Errorf("encode missing information: %w", err)
	}
	now := service.now().UTC()
	var handoff models.Handoff

	err = transactionForContext(ctx, service.db, func(tx *gorm.DB) error {
		var run models.AgentRun
		if loadErr := tx.WithContext(ctx).
			Clauses(clause.Locking{Strength: "UPDATE"}).
			Where(
				"id = ? AND organization_id = ? AND project_id = ?",
				input.AgentRunID,
				operation.Scope.OrganizationID,
				operation.Scope.ProjectID,
			).
			First(&run).Error; loadErr != nil {
			return loadErr
		}
		if run.Status.IsTerminal() {
			return errors.New("terminal agent run cannot be taken over")
		}
		ticket, ticketErr := scopedTicketForUpdate(
			ctx,
			tx,
			operation.Scope,
			run.TicketID,
		)
		if ticketErr != nil {
			return ticketErr
		}
		updateRun := tx.WithContext(ctx).Model(&models.AgentRun{}).
			Where(
				"id = ? AND organization_id = ? AND project_id = ? AND status NOT IN ?",
				run.ID,
				operation.Scope.OrganizationID,
				operation.Scope.ProjectID,
				[]models.AgentRunStatus{
					models.AgentRunStatusSucceeded,
					models.AgentRunStatusFailed,
					models.AgentRunStatusCancelled,
					models.AgentRunStatusTakenOver,
				},
			).
			Updates(map[string]any{
				"status":             models.AgentRunStatusTakenOver,
				"finished_at":        now,
				"termination_reason": strings.TrimSpace(input.Reason),
			})
		if updateRun.Error != nil {
			return updateRun.Error
		}
		if updateRun.RowsAffected != 1 {
			return errors.New("agent run changed concurrently")
		}

		if leaseErr := tx.WithContext(ctx).Model(&models.TicketLease{}).
			Where(
				"ticket_id = ? AND organization_id = ? AND project_id = ? AND holder_actor_type = ? AND holder_actor_id = ? AND released_at IS NULL",
				ticket.ID,
				operation.Scope.OrganizationID,
				operation.Scope.ProjectID,
				models.ActorTypeServicePrincipal,
				run.PrincipalID,
			).
			Updates(map[string]any{
				"released_at":    now,
				"release_reason": "human_takeover",
			}).Error; leaseErr != nil {
			return leaseErr
		}
		if run.AgentTaskID != "" {
			if taskErr := tx.WithContext(ctx).Model(&models.AgentTask{}).
				Where(
					"id = ? AND organization_id = ? AND project_id = ? AND execution_claim_id <> ''",
					run.AgentTaskID,
					operation.Scope.OrganizationID,
					operation.Scope.ProjectID,
				).
				Updates(map[string]any{
					"execution_claim_id":   "",
					"execution_message_id": "",
					"execution_expires_at": nil,
					"state":                models.A2ATaskStateCanceled,
					"status_timestamp":     now,
					"version":              gorm.Expr("version + 1"),
				}).Error; taskErr != nil {
				return taskErr
			}
		}
		nextVersion := ticket.Version + 1
		if ticketErr := tx.WithContext(ctx).Model(&models.Ticket{}).
			Where(
				"id = ? AND organization_id = ? AND project_id = ? AND version = ?",
				ticket.ID,
				operation.Scope.OrganizationID,
				operation.Scope.ProjectID,
				ticket.Version,
			).
			Updates(map[string]any{
				"assigned_to_id":                   humanID,
				"assigned_to_actor_type":           models.ActorTypeHuman,
				"assigned_to_actor_id":             operation.Actor.ID,
				"assigned_to_service_principal_id": nil,
				"version":                          nextVersion,
			}).Error; ticketErr != nil {
			return ticketErr
		}
		handoff = models.Handoff{
			OrganizationID:   operation.Scope.OrganizationID,
			ProjectID:        operation.Scope.ProjectID,
			TicketID:         ticket.ID,
			AgentRunID:       run.ID,
			Direction:        models.HandoffAgentToHuman,
			FromActorType:    models.ActorTypeServicePrincipal,
			FromActorID:      run.PrincipalID,
			ToActorType:      operation.Actor.Type,
			ToActorID:        operation.Actor.ID,
			Reason:           strings.TrimSpace(input.Reason),
			CompletedSummary: strings.TrimSpace(input.CompletedSummary),
			MissingInfo:      datatypes.JSON(missingInfo),
			EvidenceDigest:   strings.TrimSpace(input.EvidenceDigest),
		}
		if createErr := tx.WithContext(ctx).Create(&handoff).Error; createErr != nil {
			return createErr
		}
		_, eventErr := service.native.AppendDomainEventTx(
			ctx,
			tx,
			DomainEventInput{
				Type:    "io.chronodesk.agent.run.taken_over.v1",
				Subject: "ticket/" + fmt.Sprint(ticket.ID),
				Actor:   operation.Actor,
				Data: map[string]any{
					"organization_id": operation.Scope.OrganizationID,
					"project_id":      operation.Scope.ProjectID,
					"ticket_id":       ticket.ID,
					"agent_run_id":    run.ID,
					"handoff_id":      handoff.ID,
					"previous_actor": map[string]any{
						"type": models.ActorTypeServicePrincipal,
						"id":   run.PrincipalID,
					},
				},
				ResourceVersion: nextVersion,
				TraceID:         operation.TraceID,
				CorrelationID:   operation.CorrelationID,
			},
			nil,
		)
		return eventErr
	})
	if err != nil {
		return nil, err
	}
	return &handoff, nil
}

func actionRequiresApproval(
	actionType string,
	risk models.ActionRiskLevel,
) bool {
	if risk == models.ActionRiskHigh || risk == models.ActionRiskCritical {
		return true
	}
	actionType = strings.ToLower(strings.TrimSpace(actionType))
	for _, prefix := range []string{
		"ticket.delete",
		"ticket.bulk_",
		"external.communication",
		"permission.",
		"credential.",
		"funds.",
		"payment.",
	} {
		if strings.HasPrefix(actionType, prefix) {
			return true
		}
	}
	return false
}

func scopedTicketForUpdate(
	ctx context.Context,
	tx *gorm.DB,
	scope models.ProjectScope,
	ticketID uint,
) (*models.Ticket, error) {
	var ticket models.Ticket
	if err := tx.WithContext(ctx).
		Clauses(clause.Locking{Strength: "UPDATE"}).
		Where(
			"id = ? AND organization_id = ? AND project_id = ?",
			ticketID,
			scope.OrganizationID,
			scope.ProjectID,
		).
		First(&ticket).Error; err != nil {
		return nil, err
	}
	return &ticket, nil
}

func invalidProposalApproval(
	proposal *models.ActionProposal,
	task *models.ApprovalTask,
	ticketVersion uint64,
	now time.Time,
) bool {
	if proposal == nil || task == nil {
		return true
	}
	digest, err := proposal.CalculateDigest()
	return err != nil ||
		digest != proposal.ProposalDigest ||
		proposal.ProposalDigest != task.ProposalDigest ||
		proposal.TargetVersion != task.TargetVersion ||
		proposal.PolicyVersion != task.PolicyVersion ||
		proposal.TargetVersion != ticketVersion ||
		proposal.Status != models.ActionProposalPending ||
		!proposal.ExpiresAt.After(now)
}

func (service *AgentCollaborationService) expireApprovalTx(
	ctx context.Context,
	tx *gorm.DB,
	task *models.ApprovalTask,
	now time.Time,
) error {
	if task == nil {
		return errors.New("approval task is required")
	}
	if err := tx.WithContext(ctx).Model(&models.ApprovalTask{}).
		Where(
			"id = ? AND organization_id = ? AND project_id = ? AND status = ?",
			task.ID,
			task.OrganizationID,
			task.ProjectID,
			models.ApprovalTaskPending,
		).
		Updates(map[string]any{
			"status":       models.ApprovalTaskExpired,
			"completed_at": now,
		}).Error; err != nil {
		return err
	}
	if err := tx.WithContext(ctx).Model(&models.ActionProposal{}).
		Where(
			"id = ? AND organization_id = ? AND project_id = ? AND status = ?",
			task.ProposalID,
			task.OrganizationID,
			task.ProjectID,
			models.ActionProposalPending,
		).
		Update("status", models.ActionProposalExpired).Error; err != nil {
		return err
	}
	return nil
}

func (service *AgentCollaborationService) invalidateApprovalTx(
	ctx context.Context,
	tx *gorm.DB,
	proposal *models.ActionProposal,
	task *models.ApprovalTask,
	now time.Time,
) error {
	if err := tx.WithContext(ctx).Model(&models.ApprovalTask{}).
		Where(
			"id = ? AND organization_id = ? AND project_id = ? AND status = ?",
			task.ID,
			task.OrganizationID,
			task.ProjectID,
			models.ApprovalTaskPending,
		).
		Updates(map[string]any{
			"status":       models.ApprovalTaskInvalidated,
			"completed_at": now,
		}).Error; err != nil {
		return err
	}
	if proposal != nil {
		if err := tx.WithContext(ctx).Model(&models.ActionProposal{}).
			Where(
				"id = ? AND organization_id = ? AND project_id = ? AND status = ?",
				proposal.ID,
				proposal.OrganizationID,
				proposal.ProjectID,
				models.ActionProposalPending,
			).
			Update("status", models.ActionProposalInvalidated).Error; err != nil {
			return err
		}
	}
	return nil
}

func humanActorID(actor models.ActorRef) (uint, error) {
	if actor.Type != models.ActorTypeHuman {
		return 0, errors.New("human actor is required")
	}
	var userID uint64
	if _, err := fmt.Sscan(actor.ID, &userID); err != nil || userID == 0 {
		return 0, errors.New("human actor id must be a positive integer")
	}
	return uint(userID), nil
}
