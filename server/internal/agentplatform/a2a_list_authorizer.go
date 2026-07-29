package agentplatform

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"

	"github.com/seaworld008/chronodesk/server/internal/a2a"
	"github.com/seaworld008/chronodesk/server/internal/models"
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
	for _, ticketID := range a2aTaskSnapshotTicketIDs(task) {
		if _, checkErr := a.native.CheckAction(ctx, services.PolicyCheckInput{
			ServicePrincipalID: identity.Actor.ID,
			CredentialID:       identity.CredentialID,
			Scope:              models.ScopeTicketsRead,
			Action:             "ticket.read",
			ResourceType:       "ticket",
			ResourceID:         strconv.FormatUint(uint64(ticketID), 10),
			SourceProtocol:     a2aSourceProtocol,
			Context: map[string]any{
				"a2a_task_id":     task.ID,
				"a2a_context_id":  task.ContextID,
				"stored_snapshot": true,
			},
		}); checkErr != nil {
			if errors.Is(checkErr, services.ErrPolicyDenied) {
				return false, nil
			}
			return false, checkErr
		}
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
	batch, err := a.native.PrepareReadPolicyBatch(
		ctx,
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
	if err != nil {
		return nil, err
	}
	return &a2aTaskListPolicyBatch{
		native:       batch,
		principalID:  identity.Actor.ID,
		credentialID: identity.CredentialID,
	}, nil
}

type a2aTaskListPolicyBatch struct {
	native       *services.ReadPolicyBatch
	principalID  string
	credentialID string
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
	for _, ticketID := range a2aTaskSnapshotTicketIDs(task) {
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
	_, err := b.native.RecordSummary(ctx, map[string]any{
		"candidate_budget":   summary.CandidateBudget,
		"candidates_scanned": summary.CandidatesScanned,
		"items_returned":     summary.ItemsReturned,
		"items_filtered":     summary.ItemsFiltered,
		"has_more":           summary.HasMore,
		"cursor_semantics":   summary.CursorSemantics,
	})
	return err
}

func trustedA2AIdentity(ctx context.Context) (A2AExecutionIdentity, error) {
	identity, ok := A2AExecutionIdentityFromContext(ctx)
	if !ok || identity.Actor.Type != models.ActorTypeServicePrincipal ||
		strings.TrimSpace(identity.Actor.ID) == "" {
		return A2AExecutionIdentity{}, errors.New("trusted A2A identity is unavailable")
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
		if typed == 0 || uint64(uint(typed)) != typed {
			return 0, false
		}
		return uint(typed), true
	case int:
		if typed <= 0 {
			return 0, false
		}
		return uint(typed), true
	default:
		return 0, false
	}
	parsed, err := strconv.ParseUint(encoded, 10, 64)
	if err != nil || parsed == 0 || uint64(uint(parsed)) != parsed {
		return 0, false
	}
	return uint(parsed), true
}
