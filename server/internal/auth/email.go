package auth

import (
	"context"
	"errors"
	"fmt"
	"html"
	"net/url"
	"path"
	"strings"

	"github.com/seaworld008/chronodesk/server/internal/mailer"
	"github.com/seaworld008/chronodesk/server/internal/models"
)

// SMTPConfigProvider returns the active, decrypted SMTP configuration for one
// Outbox attempt. Looking it up per attempt makes credential and transport-mode
// rotation effective without restarting ChronoDesk.
type SMTPConfigProvider interface {
	GetSMTPConfig(context.Context) (*models.EmailConfig, error)
}

// SMTPEmailService is the dynamic authentication-email SMTP adapter. Business
// flows queue durable email intents; only the Outbox consumer calls this type.
type SMTPEmailService struct {
	provider SMTPConfigProvider
	webURL   *url.URL
}

func NewConfiguredSMTPEmailService(
	provider SMTPConfigProvider,
	webURL string,
) (*SMTPEmailService, error) {
	if provider == nil {
		return nil, errors.New("SMTP配置提供器不能为空")
	}
	baseURL, err := parseApplicationWebURL(webURL)
	if err != nil {
		return nil, err
	}
	return &SMTPEmailService{
		provider: provider,
		webURL:   baseURL,
	}, nil
}

func (s *SMTPEmailService) SendVerificationEmail(
	ctx context.Context,
	email string,
	token string,
) error {
	verificationURL := html.EscapeString(s.applicationURL(
		"/verify-email",
		map[string]string{"token": token},
	))
	body := fmt.Sprintf(`<!DOCTYPE html>
<html lang="zh-CN">
<head><meta charset="UTF-8"><title>验证您的 ChronoDesk 邮箱</title></head>
<body style="font-family:Arial,sans-serif;line-height:1.7;color:#1f2937">
  <div style="max-width:600px;margin:0 auto;padding:24px">
    <h1 style="color:#2563eb">验证您的邮箱</h1>
    <p>感谢您注册 ChronoDesk。请点击下面的按钮完成邮箱验证：</p>
    <p><a href="%s" style="display:inline-block;padding:12px 24px;background:#2563eb;color:#fff;text-decoration:none;border-radius:6px">验证邮箱</a></p>
    <p>如果按钮无法打开，请复制以下链接到浏览器：</p>
    <p style="word-break:break-all">%s</p>
    <p>此链接将在 24 小时后失效。如果您没有注册 ChronoDesk，请忽略本邮件。</p>
    <p style="color:#6b7280">ChronoDesk 团队</p>
  </div>
</body>
</html>`, verificationURL, verificationURL)
	return s.sendEmail(ctx, email, "验证您的 ChronoDesk 邮箱", body)
}

func (s *SMTPEmailService) SendPasswordResetEmail(
	ctx context.Context,
	email string,
	token string,
) error {
	resetURL := html.EscapeString(s.applicationURL(
		"/reset-password",
		map[string]string{"token": token},
	))
	body := fmt.Sprintf(`<!DOCTYPE html>
<html lang="zh-CN">
<head><meta charset="UTF-8"><title>重置您的 ChronoDesk 密码</title></head>
<body style="font-family:Arial,sans-serif;line-height:1.7;color:#1f2937">
  <div style="max-width:600px;margin:0 auto;padding:24px">
    <h1 style="color:#dc2626">重置密码</h1>
    <p>我们收到了您的 ChronoDesk 密码重置请求。请点击下面的按钮继续：</p>
    <p><a href="%s" style="display:inline-block;padding:12px 24px;background:#dc2626;color:#fff;text-decoration:none;border-radius:6px">重置密码</a></p>
    <p>如果按钮无法打开，请复制以下链接到浏览器：</p>
    <p style="word-break:break-all">%s</p>
    <p>此链接将在 1 小时后失效。如果不是您本人发起的请求，请忽略本邮件，现有密码不会改变。</p>
    <p style="color:#6b7280">ChronoDesk 团队</p>
  </div>
</body>
</html>`, resetURL, resetURL)
	return s.sendEmail(ctx, email, "重置您的 ChronoDesk 密码", body)
}

func (s *SMTPEmailService) SendWelcomeEmail(
	ctx context.Context,
	email string,
	username string,
) error {
	dashboardURL := html.EscapeString(s.applicationURL("/dashboard", nil))
	safeUsername := html.EscapeString(username)
	body := fmt.Sprintf(`<!DOCTYPE html>
<html lang="zh-CN">
<head><meta charset="UTF-8"><title>欢迎使用 ChronoDesk</title></head>
<body style="font-family:Arial,sans-serif;line-height:1.7;color:#1f2937">
  <div style="max-width:600px;margin:0 auto;padding:24px">
    <h1 style="color:#16a34a">欢迎使用 ChronoDesk</h1>
    <p>%s，您好！您的账户已经可以使用。</p>
    <p>您可以在 ChronoDesk 中提交服务请求、跟踪处理进度并管理个人资料。</p>
    <p><a href="%s" style="display:inline-block;padding:12px 24px;background:#16a34a;color:#fff;text-decoration:none;border-radius:6px">进入工作台</a></p>
    <p>如果您需要帮助，请联系支持团队。</p>
    <p style="color:#6b7280">ChronoDesk 团队</p>
  </div>
</body>
</html>`, safeUsername, dashboardURL)
	return s.sendEmail(ctx, email, "欢迎使用 ChronoDesk", body)
}

func (s *SMTPEmailService) sendEmail(
	ctx context.Context,
	to string,
	subject string,
	body string,
) error {
	config, err := s.provider.GetSMTPConfig(ctx)
	if err != nil {
		return fmt.Errorf("读取SMTP配置失败: %w", err)
	}
	recipient, err := mailer.CanonicalMailbox(to)
	if err != nil {
		return fmt.Errorf("收件邮箱无效: %w", err)
	}
	sender, err := mailer.CanonicalMailbox(config.FromEmail)
	if err != nil {
		return fmt.Errorf("发件邮箱无效: %w", err)
	}
	fromName := strings.TrimSpace(config.FromName)
	if fromName == "" {
		fromName = "ChronoDesk"
	}
	message, err := mailer.BuildHTMLMessage(sender, fromName, subject, body)
	if err != nil {
		return fmt.Errorf("构建邮件失败: %w", err)
	}
	transport, err := mailer.NewSMTPTransport(mailer.SMTPTransportConfig{
		Host:           config.SMTPHost,
		Port:           config.SMTPPort,
		Username:       config.SMTPUsername,
		Password:       config.SMTPPassword,
		UseSTARTTLS:    config.SMTPUseTLS,
		UseImplicitTLS: config.SMTPUseSSL,
	})
	if err != nil {
		return fmt.Errorf("SMTP配置无效: %w", err)
	}
	if err := transport.Send(ctx, sender, []string{recipient}, message); err != nil {
		return fmt.Errorf("发送邮件失败: %w", err)
	}
	return nil
}

func (s *SMTPEmailService) applicationURL(
	route string,
	parameters map[string]string,
) string {
	target := *s.webURL
	target.Path = path.Join(strings.TrimSuffix(target.Path, "/"), route)
	query := target.Query()
	for key, value := range parameters {
		query.Set(key, value)
	}
	target.RawQuery = query.Encode()
	return target.String()
}

func parseApplicationWebURL(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	parsed, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("WEB_URL格式无效: %w", err)
	}
	if (parsed.Scheme != "http" && parsed.Scheme != "https") ||
		parsed.Host == "" ||
		parsed.User != nil ||
		parsed.RawQuery != "" ||
		parsed.Fragment != "" {
		return nil, errors.New("WEB_URL必须是无凭据、查询参数和片段的绝对HTTP(S)地址")
	}
	return parsed, nil
}
