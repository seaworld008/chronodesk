package auth

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/seaworld008/chronodesk/server/internal/models"
)

func TestUserProfileJSONNeverEmbedsPersistentUserAuthorizationState(
	t *testing.T,
) {
	payload, err := json.Marshal(UserProfile{
		ID:          7,
		UserID:      11,
		DisplayName: "Contract User",
		User: models.User{
			ID:           11,
			PlatformRole: models.PlatformRolePlatformAdmin,
			Permissions:  `["legacy-secret-permission"]`,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{
		`"user"`,
		`"permissions"`,
		`"platform_role"`,
		"legacy-secret-permission",
	} {
		if strings.Contains(string(payload), forbidden) {
			t.Fatalf(
				"profile JSON exposes persistent authorization state %q: %s",
				forbidden,
				payload,
			)
		}
	}
}
