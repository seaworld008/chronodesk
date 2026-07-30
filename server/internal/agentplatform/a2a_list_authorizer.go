package agentplatform

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/seaworld008/chronodesk/server/internal/a2a"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/safeconv"
	"github.com/seaworld008/chronodesk/server/internal/services"
)

// A2ATaskListAuthorizer applies one request-scoped policy snapshot to a
// bounded Task candidate scan. It records one aggregate decision, not one
// PolicyDecision per Task.
type A2ATaskListAuthorizer struct {
	native *services.AgentNativeService
}

func NewA2ATaskListAuthorizer(
	native *services.AgentNativeService,
) *A2ATaskListAuthorizer {
	return &A2ATaskListAuthorizer{native: native}
}

func (a *A2ATaskListAuthorizer) AuthorizeTaskSnapshot(
	ctx context.Context,
	task a2a.Task,
) (bool, error) {
	if a == nil || a.native == nil {
		return false, errors.New("A2A Task snapshot policy service is unavailable")
	}
	identity, err := trustedA2AIdentity(ctx)
	if err != nil {
		return false, err
	}
	ticketIDs := a2aTaskSnapshotTicketIDs(task)
	if len(ticketIDs) == 0 {
		return true, nil
	}
	if !a2aTokenHasScopes(
		identity,
		models.ScopeTasksManage,
		models.ScopeTicketsRead,
	) {
		return false, nil
	}
	var checkErr error
	transactionErr := a.native.RunProjectOperation(
		ctx,
		func(scopedContext context.Context) error {
			for _, ticketID := range ticketIDs {
				if _, err := a.native.CheckAction(
					scopedContext,
					services.PolicyCheckInput{
						ServicePrincipalID: identity.Actor.ID,
						CredentialID:       identity.CredentialID,
						Scope:              models.ScopeTicketsRead,
						Action:             "ticket.read",
						ResourceType:       "ticket",
						ResourceID: strconv.FormatUint(
							uint64(ticketID),
							10,
						),
						SourceProtocol: a2aSourceProtocol,
						Context: map[string]any{
							"a2a_task_id":     task.ID,
							"a2a_context_id":  task.ContextID,
							"stored_snapshot": true,
						},
					},
				); err != nil {
					checkErr = err
					break
				}
			}
			// A denied snapshot is a durable authorization outcome. Commit its
			// PolicyDecision and return the domain error after the short
			// project transaction closes.
			return nil
		},
	)
	if transactionErr != nil {
		return false, transactionErr
	}
	if checkErr != nil {
		if errors.Is(checkErr, services.ErrPolicyDenied) {
			return false, nil
		}
		return false, checkErr
	}
	return true, nil
}

func (a *A2ATaskListAuthorizer) PrepareTaskList(
	ctx context.Context,
	params a2a.ListTasksParams,
) (a2a.TaskListAuthorizationBatch, error) {
	if a == nil || a.native == nil {
		return nil, errors.New("A2A Task list policy service is unavailable")
	}
	identity, err := trustedA2AIdentity(ctx)
	if err != nil {
		return nil, err
	}
	payload, err := json.Marshal(struct {
		Method string              `json:"method"`
		Params a2a.ListTasksParams `json:"params"`
	}{
		Method: "ListTasks",
		Params: params,
	})
	if err != nil {
		return nil, err
	}
	var (
		batch      *services.ReadPolicyBatch
		prepareErr error
	)
	transactionErr := a.native.RunProjectOperation(
		ctx,
		func(scopedContext context.Context) error {
			batch, prepareErr = a.native.PrepareReadPolicyBatch(
				scopedContext,
				services.PolicyCheckInput{
					ServicePrincipalID: identity.Actor.ID,
					CredentialID:       identity.CredentialID,
					Scope:              models.ScopeTasksManage,
					Action:             "a2a.ListTasks",
					ResourceType:       "a2a_task",
					ResourceID:         "*",
					RequestDigest:      digestBytes(payload),
					SourceProtocol:     a2aSourceProtocol,
				},
			)
			// A denied list request records one PolicyDecision during prepare.
			// Keep that evidence and return the denial after COMMIT.
			return nil
		},
	)
	if transactionErr != nil {
		return nil, transactionErr
	}
	if prepareErr != nil {
		return nil, prepareErr
	}
	return &a2aTaskListPolicyBatch{
		service:      a.native,
		native:       batch,
		principalID:  identity.Actor.ID,
		credentialID: identity.CredentialID,
		tokenScopes:  append([]string(nil), identity.TokenScopes...),
	}, nil
}

type a2aTaskListPolicyBatch struct {
	service      *services.AgentNativeService
	native       *services.ReadPolicyBatch
	principalID  string
	credentialID string
	tokenScopes  []string
}

func (b *a2aTaskListPolicyBatch) Allows(task a2a.Task) (bool, error) {
	for _, action := range []string{"a2a.ListTasks", "a2a.GetTask"} {
		allowed, err := b.native.Allows(services.PolicyCheckInput{
			ServicePrincipalID: b.principalID,
			CredentialID:       b.credentialID,
			Scope:              models.ScopeTasksManage,
			Action:             action,
			ResourceType:       "a2a_task",
			ResourceID:         task.ID,
			SourceProtocol:     a2aSourceProtocol,
		})
		if err != nil || !allowed {
			return allowed, err
		}
	}
	ticketIDs := a2aTaskSnapshotTicketIDs(task)
	if len(ticketIDs) > 0 && !a2aTokenScopeSnapshotHasScopes(
		b.tokenScopes,
		models.ScopeTasksManage,
		models.ScopeTicketsRead,
	) {
		return false, nil
	}
	for _, ticketID := range ticketIDs {
		allowed, err := b.native.Allows(services.PolicyCheckInput{
			ServicePrincipalID: b.principalID,
			CredentialID:       b.credentialID,
			Scope:              models.ScopeTicketsRead,
			Action:             "ticket.read",
			ResourceType:       "ticket",
			ResourceID:         strconv.FormatUint(uint64(ticketID), 10),
			SourceProtocol:     a2aSourceProtocol,
		})
		if err != nil || !allowed {
			return allowed, err
		}
	}
	return true, nil
}

func (b *a2aTaskListPolicyBatch) RecordSummary(
	ctx context.Context,
	summary a2a.TaskListAuthorizationSummary,
) error {
	if b == nil || b.service == nil || b.native == nil {
		return errors.New("A2A Task list policy batch is unavailable")
	}
	summaryContext := map[string]any{
		"candidate_budget":   summary.CandidateBudget,
		"candidates_scanned": summary.CandidatesScanned,
		"items_returned":     summary.ItemsReturned,
		"items_filtered":     summary.ItemsFiltered,
		"has_more":           summary.HasMore,
		"cursor_semantics":   summary.CursorSemantics,
	}
	return b.service.RunProjectOperation(
		ctx,
		func(scopedContext context.Context) error {
			_, err := b.native.RecordSummary(scopedContext, summaryContext)
			return err
		},
	)
}

func trustedA2AIdentity(ctx context.Context) (A2AExecutionIdentity, error) {
	identity, ok := A2AExecutionIdentityFromContext(ctx)
	if !ok || identity.Actor.Type != models.ActorTypeServicePrincipal ||
		strings.TrimSpace(identity.Actor.ID) == "" {
		return A2AExecutionIdentity{}, errors.New("trusted A2A identity is unavailable")
	}
	if err := validateA2AExecutionIdentity(ctx, identity); err != nil {
		return A2AExecutionIdentity{}, fmt.Errorf(
			"trusted A2A identity is invalid: %w",
			err,
		)
	}
	return identity, nil
}

func a2aTaskSnapshotTicketIDs(task a2a.Task) []uint {
	seen := make(map[uint]struct{})
	if task.LinkedTicketID != nil && *task.LinkedTicketID != 0 {
		seen[*task.LinkedTicketID] = struct{}{}
	}
	for _, artifact := range task.Artifacts {
		collectA2ATicketSnapshotIDs(artifact.Metadata, seen)
		for _, part := range artifact.Parts {
			collectA2ATicketSnapshotIDs(part.Metadata, seen)
			payloads := []json.RawMessage{part.Data}
			if len(part.Raw) > 0 && strings.EqualFold(part.MediaType, "application/json") {
				payloads = append(payloads, json.RawMessage(part.Raw))
			}
			for _, raw := range payloads {
				if len(raw) == 0 {
					continue
				}
				var payload any
				decoder := json.NewDecoder(strings.NewReader(string(raw)))
				decoder.UseNumber()
				if err := decoder.Decode(&payload); err != nil {
					continue
				}
				collectA2ATicketSnapshotIDs(payload, seen)
			}
		}
	}
	result := make([]uint, 0, len(seen))
	for ticketID := range seen {
		result = append(result, ticketID)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i] < result[j]
	})
	return result
}

func collectA2ATicketSnapshotIDs(value any, seen map[uint]struct{}) {
	switch typed := value.(type) {
	case map[string]any:
		if ticketID, ok := typedA2ATicketResourceID(typed); ok {
			seen[ticketID] = struct{}{}
		}
		for key, nested := range typed {
			normalized := strings.ToLower(strings.ReplaceAll(key, "_", ""))
			if normalized == "ticketid" {
				if ticketID, ok := a2aTicketIDValue(nested); ok {
					seen[ticketID] = struct{}{}
				}
			}
			if strings.EqualFold(key, "ticket") {
				if ticket, ok := nested.(map[string]any); ok {
					if ticketID, ok := a2aTicketIDValue(ticket["id"]); ok {
						seen[ticketID] = struct{}{}
					}
				}
			}
			collectA2ATicketSnapshotIDs(nested, seen)
		}
	case []any:
		for _, nested := range typed {
			collectA2ATicketSnapshotIDs(nested, seen)
		}
	}
}

func typedA2ATicketResourceID(value map[string]any) (uint, bool) {
	resourceType, exists := normalizedA2AMapValue(value, "resourcetype")
	if !exists {
		resourceType, _ = normalizedA2AMapValue(value, "type")
	}
	resourceTypeText, ok := resourceType.(string)
	if !ok || !strings.EqualFold(strings.TrimSpace(resourceTypeText), "ticket") {
		return 0, false
	}
	for _, key := range []string{"id", "resourceid"} {
		if candidate, exists := normalizedA2AMapValue(value, key); exists {
			if ticketID, valid := a2aTicketIDValue(candidate); valid {
				return ticketID, true
			}
		}
	}
	resource, ok := normalizedA2AMapValue(value, "resource")
	if !ok {
		return 0, false
	}
	resourceMap, ok := resource.(map[string]any)
	if !ok {
		return 0, false
	}
	candidate, ok := normalizedA2AMapValue(resourceMap, "id")
	if !ok {
		return 0, false
	}
	return a2aTicketIDValue(candidate)
}

func normalizedA2AMapValue(value map[string]any, normalizedKey string) (any, bool) {
	for key, candidate := range value {
		normalized := strings.ToLower(strings.ReplaceAll(key, "_", ""))
		if normalized == normalizedKey {
			return candidate, true
		}
	}
	return nil, false
}

func a2aTicketIDValue(value any) (uint, bool) {
	var encoded string
	switch typed := value.(type) {
	case json.Number:
		encoded = typed.String()
	case string:
		encoded = strings.TrimSpace(typed)
	case float64:
		encoded = strconv.FormatFloat(typed, 'f', -1, 64)
	case uint:
		if typed == 0 {
			return 0, false
		}
		return typed, true
	case uint64:
		parsed, err := safeconv.PositiveUint(typed)
		if err != nil {
			return 0, false
		}
		return parsed, true
	case int:
		if typed <= 0 {
			return 0, false
		}
		return uint(typed), true
	default:
		return 0, false
	}
	parsed, err := safeconv.ParsePositiveUint(encoded)
	if err != nil {
		return 0, false
	}
	return parsed, true
}
