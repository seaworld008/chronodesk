package agentcontract

import "testing"

func TestSupportedScopesAreUniqueAndDescribed(t *testing.T) {
	scopes := SupportedScopes()
	descriptions := ScopeDescriptions()
	if len(scopes) != 12 {
		t.Fatalf("supported scopes = %d, want 12", len(scopes))
	}
	seen := make(map[string]struct{}, len(scopes))
	for _, scope := range scopes {
		if _, exists := seen[scope]; exists {
			t.Errorf("duplicate supported scope %q", scope)
		}
		seen[scope] = struct{}{}
		if descriptions[scope] == "" {
			t.Errorf("supported scope %q has no description", scope)
		}
	}
	if len(descriptions) != len(scopes) {
		t.Errorf("scope descriptions = %d, want %d", len(descriptions), len(scopes))
	}
	for _, scope := range []string{ScopeKnowledgeRead, ScopeKnowledgeWrite} {
		if _, ok := seen[scope]; !ok {
			t.Errorf("knowledge scope %q is not discoverable", scope)
		}
	}
}

func TestScopeContractsReturnDefensiveCopies(t *testing.T) {
	scopes := SupportedScopes()
	scopes[0] = "mutated"
	if SupportedScopes()[0] == "mutated" {
		t.Fatal("SupportedScopes returned mutable shared state")
	}

	descriptions := ScopeDescriptions()
	descriptions[ScopeTicketsRead] = "mutated"
	if ScopeDescriptions()[ScopeTicketsRead] == "mutated" {
		t.Fatal("ScopeDescriptions returned mutable shared state")
	}
}
