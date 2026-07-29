package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/seaworld008/chronodesk/server/internal/models"

	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// ReadPolicyBatch is a request-scoped, immutable policy snapshot for bounded
// list operations. The principal, credential and active policies are loaded
// once. Object checks are then evaluated in memory and never create one
// PolicyDecision per candidate.
type ReadPolicyBatch struct {
	service       *AgentNativeService
	principal     *models.ServicePrincipal
	credentialID  string
	policies      []models.AgentPolicy
	summary       PolicyCheckInput
	summaryReason string
	summaryPolicy string

	recordMu sync.Mutex
	recorded bool
}

// EvaluateReadAction performs a side-effect-free authorization check for
// runtime delivery paths such as MCP resource invalidations. It never creates
// PolicyDecision rows and therefore must not be used as the auditable
// authorization for a user-initiated operation.
func (s *AgentNativeService) EvaluateReadAction(
	ctx context.Context,
	input PolicyCheckInput,
) (bool, error) {
	if input.IsWrite {
		return false, errors.New("side-effect-free policy evaluation cannot authorize writes")
	}
	if s.globalEmergencyStop.Load() {
		return false, nil
	}

	principal, err := s.getUsablePrincipal(ctx, input.ServicePrincipalID)
	if err != nil {
		switch {
		case errors.Is(err, ErrPrincipalNotFound),
			errors.Is(err, ErrPrincipalDisabled),
			errors.Is(err, ErrPrincipalExpired):
			return false, nil
		default:
			return false, err
		}
	}
	if input.CredentialID != "" {
		if err := s.ValidateCredentialReference(
			ctx,
			input.ServicePrincipalID,
			input.CredentialID,
		); err != nil {
			if errors.Is(err, ErrInvalidCredential) {
				return false, nil
			}
			return false, err
		}
	}
	if !principal.HasScope(input.Scope) {
		return false, nil
	}

	var policies []models.AgentPolicy
	if err := s.db.WithContext(ctx).
		Where("service_principal_id = ? AND is_active = ?", input.ServicePrincipalID, true).
		Where("expires_at IS NULL OR expires_at > ?", s.now()).
		Order("priority DESC, created_at ASC").
		Find(&policies).Error; err != nil {
		return false, fmt.Errorf("load agent policies: %w", err)
	}
	allowed, _, _ := evaluateLoadedPolicies(policies, input)
	return allowed, nil
}

// PrepareReadPolicyBatch authorizes a list operation against one consistent
// principal/policy snapshot. A denied list request is recorded immediately.
// An allowed request must call RecordSummary once after its bounded scan.
func (s *AgentNativeService) PrepareReadPolicyBatch(
	ctx context.Context,
	summary PolicyCheckInput,
) (*ReadPolicyBatch, error) {
	if summary.IsWrite {
		return nil, errors.New("read policy batch cannot authorize writes")
	}

	principal, principalErr := s.getUsablePrincipal(ctx, summary.ServicePrincipalID)
	if principalErr != nil &&
		!errors.Is(principalErr, ErrPrincipalNotFound) &&
		!errors.Is(principalErr, ErrPrincipalDisabled) &&
		!errors.Is(principalErr, ErrPrincipalExpired) {
		return nil, principalErr
	}
	var credentialErr error
	if principalErr == nil && summary.CredentialID != "" {
		var credential models.AgentCredential
		if err := s.db.WithContext(ctx).
			Where(
				"id = ? AND service_principal_id = ?",
				summary.CredentialID,
				summary.ServicePrincipalID,
			).
			First(&credential).Error; errors.Is(err, gorm.ErrRecordNotFound) {
			credentialErr = ErrInvalidCredential
		} else if err != nil {
			return nil, fmt.Errorf("validate agent credential reference: %w", err)
		} else if credential.Status != models.AgentCredentialStatusActive ||
			credential.RevokedAt != nil ||
			!credential.ExpiresAt.After(s.now()) {
			credentialErr = ErrInvalidCredential
		}
	}

	var policies []models.AgentPolicy
	if principalErr == nil {
		if err := s.db.WithContext(ctx).
			Where("service_principal_id = ? AND is_active = ?", summary.ServicePrincipalID, true).
			Where("expires_at IS NULL OR expires_at > ?", s.now()).
			Order("priority DESC, created_at ASC").
			Find(&policies).Error; err != nil {
			return nil, fmt.Errorf("load agent policies: %w", err)
		}
	}

	batch := &ReadPolicyBatch{
		service:      s,
		principal:    principal,
		credentialID: summary.CredentialID,
		policies:     policies,
		summary:      summary,
	}

	allowed := true
	reason := "scope_allowed"
	switch {
	case s.globalEmergencyStop.Load():
		allowed, reason = false, "global_emergency_stop"
	case principalErr != nil:
		allowed, reason = false, AgentNativeErrorCode(principalErr)
	case credentialErr != nil:
		allowed, reason = false, "invalid_credential"
	case !principal.HasScope(summary.Scope):
		allowed, reason = false, "scope_not_granted"
	}
	if allowed {
		allowed, reason, batch.summaryPolicy = evaluateLoadedPolicies(policies, summary)
	}
	batch.summaryReason = reason

	if !allowed {
		if _, err := batch.recordDecision(ctx, nil, false); err != nil {
			return nil, err
		}
		return nil, readPolicyDenial(reason)
	}
	return batch, nil
}

// Allows evaluates one object against the already loaded policy snapshot.
// Policy denial is returned as allowed=false; infrastructure or invalid batch
// use is returned as an error.
func (b *ReadPolicyBatch) Allows(input PolicyCheckInput) (bool, error) {
	if input.IsWrite {
		return false, errors.New("read policy batch cannot authorize writes")
	}
	if input.ServicePrincipalID != b.summary.ServicePrincipalID ||
		input.CredentialID != b.credentialID {
		return false, errors.New("read policy batch principal does not match")
	}
	if b.service.globalEmergencyStop.Load() {
		return false, ErrGlobalEmergencyStop
	}
	if b.principal == nil || !b.principal.HasScope(input.Scope) {
		return false, nil
	}
	allowed, _, _ := evaluateLoadedPolicies(b.policies, input)
	return allowed, nil
}

// RecordSummary persists the sole allowed PolicyDecision for a list request.
// Context should contain aggregate scan counts, never per-object sensitive
// payloads.
func (b *ReadPolicyBatch) RecordSummary(
	ctx context.Context,
	summaryContext map[string]any,
) (*models.PolicyDecision, error) {
	b.recordMu.Lock()
	defer b.recordMu.Unlock()
	if b.recorded {
		return nil, errors.New("read policy batch summary already recorded")
	}
	b.recorded = true
	return b.recordDecision(ctx, summaryContext, true)
}

func (b *ReadPolicyBatch) recordDecision(
	ctx context.Context,
	summaryContext map[string]any,
	allowed bool,
) (*models.PolicyDecision, error) {
	decisionContext := make(map[string]any)
	for key, value := range b.summary.Context {
		decisionContext[key] = value
	}
	for key, value := range summaryContext {
		decisionContext[key] = value
	}
	contextJSON, err := json.Marshal(decisionContext)
	if err != nil {
		return nil, fmt.Errorf("encode policy context: %w", err)
	}
	decision := &models.PolicyDecision{
		ID:                 newNativeID(),
		CreatedAt:          b.service.now(),
		ServicePrincipalID: b.summary.ServicePrincipalID,
		CredentialID:       b.summary.CredentialID,
		ActorType:          models.ActorTypeServicePrincipal,
		ActorID:            b.summary.ServicePrincipalID,
		Scope:              b.summary.Scope,
		Action:             b.summary.Action,
		ResourceType:       b.summary.ResourceType,
		ResourceID:         b.summary.ResourceID,
		IsWrite:            false,
		IsRisky:            b.summary.IsRisky,
		Allowed:            allowed,
		ReasonCode:         b.summaryReason,
		MatchedPolicyID:    b.summaryPolicy,
		RequestDigest:      b.summary.RequestDigest,
		SourceProtocol:     b.summary.SourceProtocol,
		Context:            datatypes.JSON(contextJSON),
	}
	if err := b.service.db.WithContext(ctx).Create(decision).Error; err != nil {
		return nil, fmt.Errorf("persist policy decision: %w", err)
	}
	return decision, nil
}

func evaluateLoadedPolicies(
	policies []models.AgentPolicy,
	input PolicyCheckInput,
) (bool, string, string) {
	explicitAllow := false
	matchedPolicyID := ""
	for i := range policies {
		policy := &policies[i]
		if !policyMatches(policy, input) {
			continue
		}
		if policy.Effect == models.AgentPolicyEffectDeny {
			return false, "explicit_deny", policy.ID
		}
		if policy.Effect == models.AgentPolicyEffectAllow && !explicitAllow {
			explicitAllow = true
			matchedPolicyID = policy.ID
		}
	}
	if input.IsRisky && !explicitAllow {
		return false, "explicit_allow_required", ""
	}
	if explicitAllow {
		return true, "explicit_allow", matchedPolicyID
	}
	return true, "scope_allowed", ""
}

func readPolicyDenial(reason string) error {
	switch strings.TrimSpace(reason) {
	case "global_emergency_stop":
		return ErrGlobalEmergencyStop
	case "principal_disabled":
		return ErrPrincipalDisabled
	case "principal_expired":
		return ErrPrincipalExpired
	case "principal_not_found":
		return ErrPrincipalNotFound
	case "invalid_credential":
		return ErrInvalidCredential
	default:
		return fmt.Errorf("%w: %s", ErrPolicyDenied, reason)
	}
}
