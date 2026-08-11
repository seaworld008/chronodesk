package services

import (
	"errors"
	"slices"
	"testing"

	"gorm.io/gorm"
)

func TestAgentNativeErrorCatalogMatchesStableRuntimeMappings(t *testing.T) {
	runtimeErrors := []error{
		ErrInvalidTicketTags,
		ErrInvalidTicketTransition,
		ErrInvalidAssignee,
		ErrAssigneeNotFound,
		ErrAssigneePolicyDenied,
		ErrInvalidActor,
		ErrInvalidScope,
		ErrPrincipalNotFound,
		ErrPrincipalDisabled,
		ErrPrincipalExpired,
		ErrInvalidCredential,
		ErrCredentialExpired,
		ErrPolicyDenied,
		ErrGlobalEmergencyStop,
		ErrReadOnlyMode,
		ErrRateLimited,
		ErrConcurrencyLimit,
		ErrExecutionGuardUnavailable,
		ErrAutomationLoop,
		ErrIdempotencyConflict,
		ErrIdempotencyInProgress,
		ErrCommandScopeMismatch,
		ErrVersionConflict,
		ErrLeaseConflict,
		ErrLeaseExpired,
		ErrLeaseNotOwned,
		ErrAttachmentTooLarge,
		ErrAttachmentNotClean,
		ErrInvalidAttachment,
		ErrInvalidAttachmentName,
		ErrNestedCommentReply,
		ErrInvalidComment,
		ErrOutboxReplayConflict,
		ErrOutboxReplayExpired,
		ErrTicketConfigurationUnavailable,
		ErrTicketRequestTypeAmbiguous,
		ErrTicketFormValidation,
		gorm.ErrRecordNotFound,
		errors.New("unexpected runtime failure"),
	}
	catalog := AgentNativeErrorCodes()
	mapped := make(map[string]struct{}, len(runtimeErrors))
	for _, runtimeErr := range runtimeErrors {
		code := AgentNativeErrorCode(runtimeErr)
		mapped[code] = struct{}{}
		if !slices.Contains(catalog, code) {
			t.Errorf("runtime error %v maps to uncatalogued code %q", runtimeErr, code)
		}
	}
	for _, code := range catalog {
		if _, ok := mapped[code]; !ok {
			t.Errorf("runtime error catalog contains unmapped code %q", code)
		}
	}

	catalog[0] = "mutated"
	if fresh := AgentNativeErrorCodes(); fresh[0] == "mutated" {
		t.Fatal("AgentNativeErrorCodes returned mutable shared storage")
	}
}
