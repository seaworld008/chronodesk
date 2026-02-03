package services

import (
	"context"
	"testing"

	"gongdan-system/internal/models"
)

func TestUpdateEmailConfigCanSkipSMTPTest(t *testing.T) {
	db := openTestDB(t)

	if err := db.AutoMigrate(&models.EmailConfig{}); err != nil {
		t.Fatalf("failed to migrate email config: %v", err)
	}

	service := NewEmailConfigService(db)

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
}
