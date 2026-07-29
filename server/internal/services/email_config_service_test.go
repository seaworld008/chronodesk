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

func TestGetEmailConfigReturnsDefaultWithoutWriting(t *testing.T) {
	db := openTestDB(t)
	if err := db.AutoMigrate(&models.EmailConfig{}); err != nil {
		t.Fatal(err)
	}
	service := NewEmailConfigServiceWithProtector(db, nil)

	config, err := service.GetEmailConfig(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if config.ID != 0 ||
		config.SMTPPort != 587 ||
		!config.SMTPUseTLS ||
		config.FromName != "ChronoDesk" ||
		config.WelcomeEmailSubject != "欢迎使用 ChronoDesk" ||
		config.OTPEmailSubject != "ChronoDesk 邮箱验证码" {
		t.Fatalf("默认投影不正确: %+v", config)
	}
	var count int64
	if err := db.Model(&models.EmailConfig{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("读取默认配置写入了 %d 条记录", count)
	}
}

func TestFirstEmailConfigUpdateUsesPersistedIDAAD(t *testing.T) {
	db := openTestDB(t)
	if err := db.AutoMigrate(&models.EmailConfig{}); err != nil {
		t.Fatal(err)
	}
	protector, err := security.NewKeyring(
		"test-first-email-config",
		map[string][]byte{
			"test-first-email-config": bytes.Repeat([]byte{0x47}, 32),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	service := NewEmailConfigServiceWithProtector(db, protector)
	host := "127.0.0.1"
	port := 2525
	username := "chronodesk"
	password := "first-save-password"
	from := "chronodesk@example.test"
	skip := true

	saved, err := service.UpdateEmailConfig(
		context.Background(),
		&models.EmailConfigUpdateRequest{
			SMTPHost:     &host,
			SMTPPort:     &port,
			SMTPUsername: &username,
			SMTPPassword: &password,
			SMTPUseTLS:   boolPtr(false),
			SMTPUseSSL:   boolPtr(false),
			FromEmail:    &from,
			SkipSMTPTest: &skip,
		},
		7,
	)
	if err != nil {
		t.Fatal(err)
	}
	if saved.ID == 0 {
		t.Fatal("首次显式保存没有取得数据库主键")
	}
	restarted := NewEmailConfigServiceWithProtector(db, protector)
	reloaded, err := restarted.GetEmailConfig(context.Background())
	if err != nil {
		t.Fatalf("按持久化ID解密SMTP密码失败: %v", err)
	}
	if reloaded.SMTPPassword != password {
		t.Fatal("首次保存的SMTP密码无法在重启后解密")
	}
}

func TestEmailCanSendIsIndependentFromRegistrationVerification(t *testing.T) {
	config := models.DefaultEmailConfig()
	config.SMTPHost = "127.0.0.1"
	config.SMTPUsername = "chronodesk"
	config.SMTPPassword = "secret"
	config.FromEmail = "chronodesk@example.test"
	config.EmailVerificationEnabled = false
	if !config.CanSendEmail() {
		t.Fatal("关闭注册邮箱验证后，完整的活动SMTP配置仍应允许发送密码重置和通知")
	}
}

func TestUpdateEmailConfigRejectsMutuallyExclusiveTLSModes(t *testing.T) {
	db := openTestDB(t)
	if err := db.AutoMigrate(&models.EmailConfig{}); err != nil {
		t.Fatal(err)
	}
	service := NewEmailConfigServiceWithProtector(db, nil)
	enabled := true
	if _, err := service.UpdateEmailConfig(
		context.Background(),
		&models.EmailConfigUpdateRequest{
			SMTPUseTLS: &enabled,
			SMTPUseSSL: &enabled,
		},
		1,
	); err == nil {
		t.Fatal("同时启用STARTTLS和隐式TLS的配置未被拒绝")
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
