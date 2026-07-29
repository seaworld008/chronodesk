package models

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"gorm.io/datatypes"
)

func assertEmbeddedIdentityIsMinimal(t *testing.T, payload any, forbidden ...string) string {
	t.Helper()
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	for _, value := range forbidden {
		if strings.Contains(text, value) {
			t.Fatalf("embedded identity leaked %q: %s", value, text)
		}
	}
	return text
}

func TestTicketEmbeddedHumanIdentityOmitsAccountAndContactData(t *testing.T) {
	lastLogin := time.Now()
	user := &User{
		ID: 7, Username: "support", DisplayName: "支持工程师", Avatar: "/avatar.png",
		Email: "private@example.com", Phone: "18800001111", Role: RoleAdmin,
		TwoFactorEnabled: true, LastLoginAt: &lastLogin,
	}
	ticket := (&Ticket{
		ID: 1, CreatedByID: &user.ID, CreatedBy: user, AssignedToID: &user.ID, AssignedTo: user,
	}).ToResponse()

	text := assertEmbeddedIdentityIsMinimal(
		t,
		ticket,
		"private@example.com",
		"18800001111",
		`"role"`,
		"two_factor_enabled",
		"last_login_at",
	)
	for _, expected := range []string{`"username":"support"`, `"display_name":"支持工程师"`, `"avatar":"/avatar.png"`} {
		if !strings.Contains(text, expected) {
			t.Fatalf("safe identity field %q missing: %s", expected, text)
		}
	}
}

func TestBusinessRecordsEmbedOnlyServicePrincipalSummary(t *testing.T) {
	principal := &ServicePrincipal{
		ID: "principal-1", Name: "triage-agent", Status: ServicePrincipalStatusActive,
		Scopes:             datatypes.JSON([]byte(`["tickets:read","comments:write"]`)),
		RateLimitPerMinute: 999, ConcurrentLimit: 888, EmergencyDisabled: true,
	}
	comment := (&TicketComment{
		ActorType: ActorTypeServicePrincipal, ActorID: principal.ID,
		ServicePrincipal: principal, Content: "ok", Type: CommentTypeInternal,
	}).ToResponse()

	text := assertEmbeddedIdentityIsMinimal(
		t,
		comment,
		`"scopes"`,
		"rate_limit_per_minute",
		"concurrent_limit",
		"emergency_disabled",
	)
	if !strings.Contains(text, `"service_principal":{"id":"principal-1","name":"triage-agent"}`) {
		t.Fatalf("safe principal summary missing: %s", text)
	}
}
