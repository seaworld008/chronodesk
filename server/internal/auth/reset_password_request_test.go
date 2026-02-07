package auth

import "testing"

func TestResetPasswordRequest_EffectivePasswordPrefersNewPassword(t *testing.T) {
	req := ResetPasswordRequest{
		NewPassword: "new-pass-123",
		Password:    "legacy-pass-123",
	}

	if got := req.EffectivePassword(); got != "new-pass-123" {
		t.Fatalf("expected EffectivePassword to prefer new_password, got: %q", got)
	}
}

func TestResetPasswordRequest_EffectivePasswordFallsBackToLegacyPassword(t *testing.T) {
	req := ResetPasswordRequest{
		Password: "legacy-pass-123",
	}

	if got := req.EffectivePassword(); got != "legacy-pass-123" {
		t.Fatalf("expected EffectivePassword to fallback to password, got: %q", got)
	}
}
