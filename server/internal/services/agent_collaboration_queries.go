package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

const (
	defaultCollaborationPageSize = 25
	maxCollaborationPageSize     = 100
)

var (
	ErrCollaborationAccessDenied = errors.New("collaboration access denied")
	ErrCollaborationNotFound     = errors.New("collaboration record not found")
	ErrCollaborationPagination   = errors.New("collaboration pagination is invalid")
)

// CollaborationPagination is normalized again inside the query service so a
// non-HTTP caller cannot accidentally issue an unbounded project query.
type CollaborationPagination struct {
	Page     int
	PageSize int
}

type CollaborationPage[T any] struct {
	Items    []T
	Total    int64
	Page     int
	PageSize int
}

// AgentRunSummary contains only observable execution state. In particular it
// deliberately omits PolicySnapshot and all hidden model reasoning.
type AgentRunSummary struct {
	ID           string                `json:"id"`
	CreatedAt    time.Time             `json:"created_at"`
	UpdatedAt    time.Time             `json:"updated_at"`
	TicketID     uint                  `json:"ticket_id"`
	TicketNumber string                `json:"ticket_number"`
	TicketTitle  string                `json:"ticket_title"`
	Status       models.AgentRunStatus `json:"status"`
}

type AgentRunDetail struct {
	AgentRunSummary
	ModelProvider     string     `json:"model_provider"`
	ModelName         string     `json:"model_name"`
	PromptVersion     string     `json:"prompt_version"`
	ToolsetVersion    string     `json:"toolset_version"`
	PolicyVersion     string     `json:"policy_version"`
	InputSummary      string     `json:"input_summary,omitempty"`
	OutputSummary     string     `json:"output_summary,omitempty"`
	PromptTokens      int64      `json:"prompt_tokens"`
	CompletionTokens  int64      `json:"completion_tokens"`
	CostMicros        int64      `json:"cost_micros"`
	StartedAt         *time.Time `json:"started_at,omitempty"`
	FinishedAt        *time.Time `json:"finished_at,omitempty"`
	TerminationReason string     `json:"termination_reason,omitempty"`
}

type ActionProposalSummary struct {
	ID            string                      `json:"id"`
	CreatedAt     time.Time                   `json:"created_at"`
	UpdatedAt     time.Time                   `json:"updated_at"`
	TicketID      uint                        `json:"ticket_id"`
	TicketNumber  string                      `json:"ticket_number"`
	TicketTitle   string                      `json:"ticket_title"`
	AgentRunID    string                      `json:"agent_run_id"`
	ActionType    string                      `json:"action_type"`
	RiskLevel     models.ActionRiskLevel      `json:"risk_level"`
	TargetVersion uint64                      `json:"target_ticket_version"`
	Status        models.ActionProposalStatus `json:"status"`
	ExpiresAt     time.Time                   `json:"expires_at"`
	ExecutedAt    *time.Time                  `json:"executed_at,omitempty"`
}

// ActionProposalDetail exposes the structured change preview, never the raw
// action payload used by the trusted action executor.
type ActionProposalDetail struct {
	ActionProposalSummary
	Preview map[string]any `json:"preview"`
}

type ApprovalTaskSummary struct {
	ID                string                    `json:"id"`
	CreatedAt         time.Time                 `json:"created_at"`
	UpdatedAt         time.Time                 `json:"updated_at"`
	TicketID          uint                      `json:"ticket_id"`
	TicketNumber      string                    `json:"ticket_number"`
	TicketTitle       string                    `json:"ticket_title"`
	ProposalID        string                    `json:"proposal_id"`
	TargetVersion     uint64                    `json:"target_ticket_version"`
	RequiredApprovals int                       `json:"required_approvals"`
	Status            models.ApprovalTaskStatus `json:"status"`
	ExpiresAt         time.Time                 `json:"expires_at"`
	CompletedAt       *time.Time                `json:"completed_at,omitempty"`
}

type ApprovalTaskDetail struct {
	ApprovalTaskSummary
	ApprovalsRecorded  int64 `json:"approvals_recorded"`
	RejectionsRecorded int64 `json:"rejections_recorded"`
}

type HandoffSummary struct {
	ID           string                  `json:"id"`
	CreatedAt    time.Time               `json:"created_at"`
	TicketID     uint                    `json:"ticket_id"`
	TicketNumber string                  `json:"ticket_number"`
	TicketTitle  string                  `json:"ticket_title"`
	AgentRunID   string                  `json:"agent_run_id,omitempty"`
	Direction    models.HandoffDirection `json:"direction"`
}

type HandoffDetail struct {
	HandoffSummary
	Reason             string   `json:"reason"`
	CompletedSummary   string   `json:"completed_summary,omitempty"`
	MissingInformation []string `json:"missing_information"`
}

type AgentCollaborationQueryService struct {
	db *gorm.DB
}

func NewAgentCollaborationQueryService(
	db *gorm.DB,
) (*AgentCollaborationQueryService, error) {
	if db == nil {
		return nil, errors.New("collaboration query database is required")
	}
	return &AgentCollaborationQueryService{db: db}, nil
}

func (service *AgentCollaborationQueryService) ListAgentRuns(
	ctx context.Context,
	access ProjectAccess,
	pagination CollaborationPagination,
) (*CollaborationPage[AgentRunSummary], error) {
	query, err := service.scopedQuery(ctx, access, &models.AgentRun{})
	if err != nil {
		return nil, err
	}
	page, pageSize, err := validateCollaborationPagination(pagination)
	if err != nil {
		return nil, err
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, fmt.Errorf("count scoped Agent runs: %w", err)
	}
	items := make([]AgentRunSummary, 0)
	if err := query.
		Select(
			"id, created_at, updated_at, ticket_id, status",
		).
		Order("created_at DESC, id DESC").
		Offset((page - 1) * pageSize).
		Limit(pageSize).
		Scan(&items).Error; err != nil {
		return nil, fmt.Errorf("list scoped Agent runs: %w", err)
	}
	if err := service.populateAgentRunTickets(ctx, access.Scope, items); err != nil {
		return nil, err
	}
	return &CollaborationPage[AgentRunSummary]{
		Items: items, Total: total, Page: page, PageSize: pageSize,
	}, nil
}

func (service *AgentCollaborationQueryService) GetAgentRun(
	ctx context.Context,
	access ProjectAccess,
	runID string,
) (*AgentRunDetail, error) {
	query, err := service.scopedQuery(ctx, access, &models.AgentRun{})
	if err != nil {
		return nil, err
	}
	var result AgentRunDetail
	err = query.
		Select(strings.Join([]string{
			"id", "created_at", "updated_at", "ticket_id", "status",
			"model_provider", "model_name", "prompt_version", "toolset_version",
			"policy_version", "input_summary", "output_summary",
			"prompt_tokens", "completion_tokens", "cost_micros", "started_at",
			"finished_at", "termination_reason",
		}, ", ")).
		Where("id = ?", strings.TrimSpace(runID)).
		Take(&result).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrCollaborationNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get scoped Agent run: %w", err)
	}
	projection, err := service.collaborationTicketProjections(
		ctx,
		access.Scope,
		[]uint{result.TicketID},
	)
	if err != nil {
		return nil, err
	}
	if ticket, ok := projection[result.TicketID]; ok {
		result.TicketNumber = ticket.TicketNumber
		result.TicketTitle = ticket.Title
	}
	return &result, nil
}

func (service *AgentCollaborationQueryService) ListActionProposals(
	ctx context.Context,
	access ProjectAccess,
	pagination CollaborationPagination,
) (*CollaborationPage[ActionProposalSummary], error) {
	query, err := service.scopedQuery(ctx, access, &models.ActionProposal{})
	if err != nil {
		return nil, err
	}
	page, pageSize, err := validateCollaborationPagination(pagination)
	if err != nil {
		return nil, err
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, fmt.Errorf("count scoped action proposals: %w", err)
	}
	items := make([]ActionProposalSummary, 0)
	if err := query.
		Select(strings.Join([]string{
			"id", "created_at", "updated_at", "ticket_id", "agent_run_id",
			"action_type", "risk_level", "target_version", "status",
			"expires_at", "executed_at",
		}, ", ")).
		Order("created_at DESC, id DESC").
		Offset((page - 1) * pageSize).
		Limit(pageSize).
		Scan(&items).Error; err != nil {
		return nil, fmt.Errorf("list scoped action proposals: %w", err)
	}
	if err := service.populateActionProposalTickets(
		ctx,
		access.Scope,
		items,
	); err != nil {
		return nil, err
	}
	return &CollaborationPage[ActionProposalSummary]{
		Items: items, Total: total, Page: page, PageSize: pageSize,
	}, nil
}

func (service *AgentCollaborationQueryService) GetActionProposal(
	ctx context.Context,
	access ProjectAccess,
	proposalID string,
) (*ActionProposalDetail, error) {
	query, err := service.scopedQuery(ctx, access, &models.ActionProposal{})
	if err != nil {
		return nil, err
	}
	var row actionProposalDetailRow
	err = query.
		Select(strings.Join([]string{
			"id", "created_at", "updated_at", "ticket_id", "agent_run_id",
			"action_type", "change_preview", "risk_level", "target_version",
			"status", "expires_at", "executed_at",
		}, ", ")).
		Where("id = ?", strings.TrimSpace(proposalID)).
		Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrCollaborationNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get scoped action proposal: %w", err)
	}
	result := &ActionProposalDetail{
		ActionProposalSummary: row.ActionProposalSummary,
		Preview:               safeProposalPreview(row.ChangePreview),
	}
	projection, err := service.collaborationTicketProjections(
		ctx,
		access.Scope,
		[]uint{result.TicketID},
	)
	if err != nil {
		return nil, err
	}
	if ticket, ok := projection[result.TicketID]; ok {
		result.TicketNumber = ticket.TicketNumber
		result.TicketTitle = ticket.Title
	}
	return result, nil
}

func (service *AgentCollaborationQueryService) ListApprovalTasks(
	ctx context.Context,
	access ProjectAccess,
	pagination CollaborationPagination,
) (*CollaborationPage[ApprovalTaskSummary], error) {
	query, err := service.scopedQuery(ctx, access, &models.ApprovalTask{})
	if err != nil {
		return nil, err
	}
	page, pageSize, err := validateCollaborationPagination(pagination)
	if err != nil {
		return nil, err
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, fmt.Errorf("count scoped approval tasks: %w", err)
	}
	items := make([]ApprovalTaskSummary, 0)
	if err := query.
		Select(strings.Join([]string{
			"id", "created_at", "updated_at", "ticket_id", "proposal_id",
			"target_version", "required_approvals", "status", "expires_at",
			"completed_at",
		}, ", ")).
		Order("created_at DESC, id DESC").
		Offset((page - 1) * pageSize).
		Limit(pageSize).
		Scan(&items).Error; err != nil {
		return nil, fmt.Errorf("list scoped approval tasks: %w", err)
	}
	if err := service.populateApprovalTaskTickets(
		ctx,
		access.Scope,
		items,
	); err != nil {
		return nil, err
	}
	return &CollaborationPage[ApprovalTaskSummary]{
		Items: items, Total: total, Page: page, PageSize: pageSize,
	}, nil
}

func (service *AgentCollaborationQueryService) GetApprovalTask(
	ctx context.Context,
	access ProjectAccess,
	approvalID string,
) (*ApprovalTaskDetail, error) {
	query, err := service.scopedQuery(ctx, access, &models.ApprovalTask{})
	if err != nil {
		return nil, err
	}
	var result ApprovalTaskDetail
	err = query.
		Select(strings.Join([]string{
			"id", "created_at", "updated_at", "ticket_id", "proposal_id",
			"target_version", "required_approvals", "status", "expires_at",
			"completed_at",
		}, ", ")).
		Where("id = ?", strings.TrimSpace(approvalID)).
		Take(&result).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrCollaborationNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get scoped approval task: %w", err)
	}
	projection, err := service.collaborationTicketProjections(
		ctx,
		access.Scope,
		[]uint{result.TicketID},
	)
	if err != nil {
		return nil, err
	}
	if ticket, ok := projection[result.TicketID]; ok {
		result.TicketNumber = ticket.TicketNumber
		result.TicketTitle = ticket.Title
	}
	scope := access.Scope
	var counts []struct {
		Decision models.ApprovalDecisionValue
		Count    int64
	}
	if err := service.db.WithContext(ctx).
		Model(&models.ApprovalDecision{}).
		Select("decision, COUNT(*) AS count").
		Where(
			"approval_task_id = ? AND organization_id = ? AND project_id = ?",
			result.ID,
			scope.OrganizationID,
			scope.ProjectID,
		).
		Group("decision").
		Scan(&counts).Error; err != nil {
		return nil, fmt.Errorf("count scoped approval decisions: %w", err)
	}
	for _, count := range counts {
		switch count.Decision {
		case models.ApprovalDecisionApprove:
			result.ApprovalsRecorded = count.Count
		case models.ApprovalDecisionReject:
			result.RejectionsRecorded = count.Count
		}
	}
	return &result, nil
}

func (service *AgentCollaborationQueryService) ListHandoffs(
	ctx context.Context,
	access ProjectAccess,
	pagination CollaborationPagination,
) (*CollaborationPage[HandoffSummary], error) {
	query, err := service.scopedQuery(ctx, access, &models.Handoff{})
	if err != nil {
		return nil, err
	}
	page, pageSize, err := validateCollaborationPagination(pagination)
	if err != nil {
		return nil, err
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, fmt.Errorf("count scoped handoffs: %w", err)
	}
	items := make([]HandoffSummary, 0)
	if err := query.
		Select("id, created_at, ticket_id, agent_run_id, direction").
		Order("created_at DESC, id DESC").
		Offset((page - 1) * pageSize).
		Limit(pageSize).
		Scan(&items).Error; err != nil {
		return nil, fmt.Errorf("list scoped handoffs: %w", err)
	}
	if err := service.populateHandoffTickets(
		ctx,
		access.Scope,
		items,
	); err != nil {
		return nil, err
	}
	return &CollaborationPage[HandoffSummary]{
		Items: items, Total: total, Page: page, PageSize: pageSize,
	}, nil
}

func (service *AgentCollaborationQueryService) GetHandoff(
	ctx context.Context,
	access ProjectAccess,
	handoffID string,
) (*HandoffDetail, error) {
	query, err := service.scopedQuery(ctx, access, &models.Handoff{})
	if err != nil {
		return nil, err
	}
	var row handoffDetailRow
	err = query.
		Select(strings.Join([]string{
			"id", "created_at", "ticket_id", "agent_run_id", "direction",
			"reason", "completed_summary", "missing_info",
		}, ", ")).
		Where("id = ?", strings.TrimSpace(handoffID)).
		Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrCollaborationNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get scoped handoff: %w", err)
	}
	result := &HandoffDetail{
		HandoffSummary:     row.HandoffSummary,
		Reason:             row.Reason,
		CompletedSummary:   row.CompletedSummary,
		MissingInformation: safeMissingInformation(row.MissingInfo),
	}
	projection, err := service.collaborationTicketProjections(
		ctx,
		access.Scope,
		[]uint{result.TicketID},
	)
	if err != nil {
		return nil, err
	}
	if ticket, ok := projection[result.TicketID]; ok {
		result.TicketNumber = ticket.TicketNumber
		result.TicketTitle = ticket.Title
	}
	return result, nil
}

type actionProposalDetailRow struct {
	ActionProposalSummary
	ChangePreview datatypes.JSON `gorm:"column:change_preview"`
}

type handoffDetailRow struct {
	HandoffSummary
	Reason           string
	CompletedSummary string
	MissingInfo      datatypes.JSON `gorm:"column:missing_info"`
}

func (service *AgentCollaborationQueryService) scopedQuery(
	ctx context.Context,
	access ProjectAccess,
	model any,
) (*gorm.DB, error) {
	if service == nil || service.db == nil {
		return nil, errors.New("collaboration query service is unavailable")
	}
	operation, err := OperationContextFromContext(ctx)
	if err != nil {
		return nil, ErrCollaborationAccessDenied
	}
	if operation.Source != SourceProtocolHumanREST ||
		operation.Actor.Type != models.ActorTypeHuman ||
		operation.Scope != access.Scope ||
		!access.Role.IsValid() {
		return nil, ErrCollaborationAccessDenied
	}
	if access.Project.ID != access.Scope.ProjectID ||
		access.Project.OrganizationID != access.Scope.OrganizationID {
		return nil, ErrCollaborationAccessDenied
	}
	if _, err := humanActorID(operation.Actor); err != nil {
		return nil, ErrCollaborationAccessDenied
	}

	scope := access.Scope
	query := service.db.WithContext(ctx).
		Model(model).
		Where(
			"organization_id = ? AND project_id = ?",
			scope.OrganizationID,
			scope.ProjectID,
		)
	if access.Role == models.ProjectRoleRequester {
		requesterTickets := service.db.WithContext(ctx).
			Model(&models.Ticket{}).
			Select("id").
			Where(
				"organization_id = ? AND project_id = ? AND created_by_actor_type = ? AND created_by_actor_id = ?",
				scope.OrganizationID,
				scope.ProjectID,
				models.ActorTypeHuman,
				operation.Actor.ID,
			)
		query = query.Where("ticket_id IN (?)", requesterTickets)
	}
	return query, nil
}

func validateCollaborationPagination(
	pagination CollaborationPagination,
) (int, int, error) {
	page := pagination.Page
	if page == 0 {
		page = 1
	}
	pageSize := pagination.PageSize
	if pageSize == 0 {
		pageSize = defaultCollaborationPageSize
	}
	if page < 1 ||
		pageSize < 1 ||
		pageSize > maxCollaborationPageSize ||
		page > math.MaxInt/pageSize {
		return 0, 0, ErrCollaborationPagination
	}
	return page, pageSize, nil
}

type collaborationTicketProjection struct {
	ID           uint
	TicketNumber string
	Title        string
}

func (service *AgentCollaborationQueryService) collaborationTicketProjections(
	ctx context.Context,
	scope models.ProjectScope,
	ticketIDs []uint,
) (map[uint]collaborationTicketProjection, error) {
	uniqueIDs := make([]uint, 0, len(ticketIDs))
	seen := make(map[uint]struct{}, len(ticketIDs))
	for _, ticketID := range ticketIDs {
		if ticketID == 0 {
			continue
		}
		if _, exists := seen[ticketID]; exists {
			continue
		}
		seen[ticketID] = struct{}{}
		uniqueIDs = append(uniqueIDs, ticketID)
	}
	if len(uniqueIDs) == 0 {
		return map[uint]collaborationTicketProjection{}, nil
	}
	var items []collaborationTicketProjection
	if err := service.db.WithContext(ctx).
		Model(&models.Ticket{}).
		Select("id", "ticket_number", "title").
		Where(
			"organization_id = ? AND project_id = ? AND id IN ?",
			scope.OrganizationID,
			scope.ProjectID,
			uniqueIDs,
		).
		Find(&items).Error; err != nil {
		return nil, fmt.Errorf("load collaboration ticket projections: %w", err)
	}
	result := make(
		map[uint]collaborationTicketProjection,
		len(items),
	)
	for index := range items {
		result[items[index].ID] = items[index]
	}
	return result, nil
}

func (service *AgentCollaborationQueryService) populateAgentRunTickets(
	ctx context.Context,
	scope models.ProjectScope,
	items []AgentRunSummary,
) error {
	ids := make([]uint, 0, len(items))
	for index := range items {
		ids = append(ids, items[index].TicketID)
	}
	projections, err := service.collaborationTicketProjections(ctx, scope, ids)
	if err != nil {
		return err
	}
	for index := range items {
		ticket, exists := projections[items[index].TicketID]
		if !exists {
			return fmt.Errorf(
				"collaboration ticket %d is unavailable",
				items[index].TicketID,
			)
		}
		items[index].TicketNumber = ticket.TicketNumber
		items[index].TicketTitle = ticket.Title
	}
	return nil
}

func (service *AgentCollaborationQueryService) populateActionProposalTickets(
	ctx context.Context,
	scope models.ProjectScope,
	items []ActionProposalSummary,
) error {
	ids := make([]uint, 0, len(items))
	for index := range items {
		ids = append(ids, items[index].TicketID)
	}
	projections, err := service.collaborationTicketProjections(ctx, scope, ids)
	if err != nil {
		return err
	}
	for index := range items {
		ticket, exists := projections[items[index].TicketID]
		if !exists {
			return fmt.Errorf(
				"collaboration ticket %d is unavailable",
				items[index].TicketID,
			)
		}
		items[index].TicketNumber = ticket.TicketNumber
		items[index].TicketTitle = ticket.Title
	}
	return nil
}

func (service *AgentCollaborationQueryService) populateApprovalTaskTickets(
	ctx context.Context,
	scope models.ProjectScope,
	items []ApprovalTaskSummary,
) error {
	ids := make([]uint, 0, len(items))
	for index := range items {
		ids = append(ids, items[index].TicketID)
	}
	projections, err := service.collaborationTicketProjections(ctx, scope, ids)
	if err != nil {
		return err
	}
	for index := range items {
		ticket, exists := projections[items[index].TicketID]
		if !exists {
			return fmt.Errorf(
				"collaboration ticket %d is unavailable",
				items[index].TicketID,
			)
		}
		items[index].TicketNumber = ticket.TicketNumber
		items[index].TicketTitle = ticket.Title
	}
	return nil
}

func (service *AgentCollaborationQueryService) populateHandoffTickets(
	ctx context.Context,
	scope models.ProjectScope,
	items []HandoffSummary,
) error {
	ids := make([]uint, 0, len(items))
	for index := range items {
		ids = append(ids, items[index].TicketID)
	}
	projections, err := service.collaborationTicketProjections(ctx, scope, ids)
	if err != nil {
		return err
	}
	for index := range items {
		ticket, exists := projections[items[index].TicketID]
		if !exists {
			return fmt.Errorf(
				"collaboration ticket %d is unavailable",
				items[index].TicketID,
			)
		}
		items[index].TicketNumber = ticket.TicketNumber
		items[index].TicketTitle = ticket.Title
	}
	return nil
}

func safeProposalPreview(raw datatypes.JSON) map[string]any {
	if len(raw) == 0 || len(raw) > agentCollaborationSafeJSONLimit {
		return map[string]any{}
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	var preview map[string]any
	if err := decoder.Decode(&preview); err != nil {
		return map[string]any{}
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return map[string]any{}
	}
	sanitized, ok := sanitizePreviewValue(preview, 0).(map[string]any)
	if !ok {
		return map[string]any{}
	}
	return sanitized
}

func sanitizePreviewValue(value any, depth int) any {
	if depth >= 4 {
		return nil
	}
	switch typed := value.(type) {
	case map[string]any:
		result := make(map[string]any)
		count := 0
		for key, item := range typed {
			if count >= 32 || sensitivePreviewKey(key) {
				continue
			}
			sanitized := sanitizePreviewValue(item, depth+1)
			if sanitized == nil {
				continue
			}
			result[key] = sanitized
			count++
		}
		return result
	case []any:
		size := len(typed)
		if size > 50 {
			size = 50
		}
		result := make([]any, 0, size)
		for _, item := range typed[:size] {
			if sanitized := sanitizePreviewValue(item, depth+1); sanitized != nil {
				result = append(result, sanitized)
			}
		}
		return result
	case string:
		return truncateCollaborationText(typed, 2000)
	case json.Number, bool:
		return typed
	case nil:
		return nil
	default:
		return nil
	}
}

func sensitivePreviewKey(key string) bool {
	normalized := strings.ToLower(strings.TrimSpace(key))
	normalized = strings.ReplaceAll(normalized, "-", "_")
	for _, fragment := range []string{
		"api_key",
		"action_payload",
		"access_key",
		"authorization",
		"chain_of_thought",
		"cookie",
		"credential",
		"internal_reasoning",
		"password",
		"policy_snapshot",
		"private_key",
		"prompt",
		"raw_payload",
		"reasoning",
		"secret",
		"token",
	} {
		if strings.Contains(normalized, fragment) {
			return true
		}
	}
	return false
}

func safeMissingInformation(raw datatypes.JSON) []string {
	if len(raw) == 0 || len(raw) > agentCollaborationSafeJSONLimit {
		return []string{}
	}
	var values []string
	if err := json.Unmarshal(raw, &values); err != nil {
		return []string{}
	}
	if len(values) > 50 {
		values = values[:50]
	}
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		value = truncateCollaborationText(value, 2000)
		result = append(result, value)
	}
	return result
}

const agentCollaborationSafeJSONLimit = 64 << 10

func truncateCollaborationText(value string, maxRunes int) string {
	if maxRunes < 1 {
		return ""
	}
	runes := []rune(value)
	if len(runes) > maxRunes {
		runes = runes[:maxRunes]
	}
	return string(runes)
}
