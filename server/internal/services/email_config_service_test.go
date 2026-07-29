package services

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/security"
)

func TestUpdateEmailConfigCanSkipSMTPTest(t *testing.T) {
	db := openTestDB(t)

	if err := db.AutoMigrate(&models.EmailConfig{}); err != nil {
		t.Fatalf("failed to migrate email config: %v", err)
	}

	protector, err := security.NewKeyring("test-email", map[string][]byte{
		"test-email": bytes.Repeat([]byte{0x45}, 32),
	})
	if err != nil {
		t.Fatal(err)
	}
	service := NewEmailConfigServiceWithProtector(db, protector)

	enableEmail := true
	smtpHost := "127.0.0.1"
	smtpPort := 1
	smtpUsername := "test_user"
	smtpPassword := "test_pass"
	fromEmail := "test@example.com"
	fromName := "测试系统"
	skipSMTPTest := true

	req := &models.EmailConfigUpdateRequest{
		EmailVerificationEnabled: &enableEmail,
		SMTPHost:                 &smtpHost,
		SMTPPort:                 &smtpPort,
		SMTPUsername:             &smtpUsername,
		SMTPPassword:             &smtpPassword,
		SMTPUseTLS:               boolPtr(true),
		SMTPUseSSL:               boolPtr(false),
		FromEmail:                &fromEmail,
		FromName:                 &fromName,
		SkipSMTPTest:             &skipSMTPTest,
	}

	updated, err := service.UpdateEmailConfig(context.Background(), req, 1)
	if err != nil {
		t.Fatalf("expected update to succeed with skip flag, got error: %v", err)
	}

	if !updated.EmailVerificationEnabled {
		t.Fatalf("expected email verification enabled to remain true")
	}
	if updated.SMTPHost != smtpHost {
		t.Fatalf("expected smtp host %s, got %s", smtpHost, updated.SMTPHost)
	}
	if updated.SMTPPassword != smtpPassword {
		t.Fatal("runtime SMTP password was not available to the email sender")
	}
	var stored models.EmailConfig
	if err := db.First(&stored, updated.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.SMTPPassword == smtpPassword ||
		strings.Contains(stored.SMTPPassword, smtpPassword) ||
		!security.IsEnvelope(stored.SMTPPassword) {
		t.Fatalf("SMTP password was not encrypted at rest: %q", stored.SMTPPassword)
	}

	// A separately constructed service simulates a process restart.
	restarted := NewEmailConfigServiceWithProtector(db, protector)
	reloaded, err := restarted.GetEmailConfig(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.SMTPPassword != smtpPassword {
		t.Fatal("restarted service could not decrypt SMTP password")
	}
}

func TestUpdateEmailConfigRejectsInjectedHeaderValues(t *testing.T) {
	db := openTestDB(t)
	if err := db.AutoMigrate(&models.EmailConfig{}); err != nil {
		t.Fatal(err)
	}
	protector, err := security.NewKeyring("test-email-headers", map[string][]byte{
		"test-email-headers": bytes.Repeat([]byte{0x46}, 32),
	})
	if err != nil {
		t.Fatal(err)
	}
	service := NewEmailConfigServiceWithProtector(db, protector)

	injected := "合法中文\r\nBcc: victim@example.com"
	if _, err := service.UpdateEmailConfig(
		context.Background(),
		&models.EmailConfigUpdateRequest{FromName: &injected},
		1,
	); err == nil {
		t.Fatal("expected CRLF header injection to be rejected")
	}
}
