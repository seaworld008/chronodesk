package services

import (
	"errors"
	"testing"

	"github.com/seaworld008/chronodesk/server/internal/models"
)

func TestNativeCommandTokenScopeValidationUsesCanonicalPolicyMapping(
	t *testing.T,
) {
	tests := []struct {
		kind  NativeCommandAuthorizationKind
		scope string
	}{
		{NativeCommandTicketCreate, models.ScopeTicketsCreate},
		{NativeCommandTicketQuery, models.ScopeTicketsRead},
		{NativeCommandTicketClaim, models.ScopeTasksManage},
		{NativeCommandLeaseRelease, models.ScopeTasksManage},
		{NativeCommandTicketUpdate, models.ScopeTicketsUpdate},
		{NativeCommandTicketTransit, models.ScopeTicketsTransition},
		{NativeCommandTicketAssign, models.ScopeTicketsAssign},
		{NativeCommandCommentCreate, models.ScopeCommentsWrite},
		{NativeCommandTicketEscalate, models.ScopeTicketsTransition},
	}
	for _, test := range tests {
		t.Run(string(test.kind), func(t *testing.T) {
			required, err := NativeCommandRequiredScope(test.kind)
			if err != nil || required != test.scope {
				t.Fatalf(
					"required scope = %q, %v; want %q",
					required,
					err,
					test.scope,
				)
			}
			if err := ValidateNativeCommandTokenScopes(
				test.kind,
				[]string{models.ScopeTasksManage, test.scope},
			); err != nil {
				t.Fatalf("validate required scope: %v", err)
			}
			if test.scope != models.ScopeTasksManage {
				if err := ValidateNativeCommandTokenScopes(
					test.kind,
					[]string{models.ScopeTasksManage},
				); !errors.Is(err, ErrPolicyDenied) {
					t.Fatalf(
						"narrow token error = %v, want policy denied",
						err,
					)
				}
			}
		})
	}
}

func TestNativeCommandTokenScopeValidationFailsClosed(t *testing.T) {
	tests := []struct {
		name   string
		kind   NativeCommandAuthorizationKind
		scopes []string
	}{
		{
			name: "missing",
			kind: NativeCommandTicketQuery,
		},
		{
			name:   "empty",
			kind:   NativeCommandTicketQuery,
			scopes: []string{""},
		},
		{
			name:   "unsupported",
			kind:   NativeCommandTicketQuery,
			scopes: []string{"tickets:superuser"},
		},
		{
			name:   "unsupported command",
			kind:   NativeCommandAuthorizationKind("ticket.unknown"),
			scopes: []string{models.ScopeTicketsRead},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := ValidateNativeCommandTokenScopes(
				test.kind,
				test.scopes,
			); err == nil {
				t.Fatal("invalid token scope snapshot was accepted")
			}
		})
	}
}
