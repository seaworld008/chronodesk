package auth

import (
	"context"
	"fmt"
	"net/smtp"
	"strings"
	"time"
)

// SMTPEmailService SMTP邮件服务实现
type SMTPEmailService struct {
	host     string
	port     string
	username string
	password string
	from     string
	auth     smtp.Auth
}

// EmailConfig 邮件配置
type EmailConfig struct {
	Host     string
	Port     string
	Username string
	Password string
	From     string
}

// NewSMTPEmailService 创建SMTP邮件服务
func NewSMTPEmailService(config *EmailConfig) *SMTPEmailService {
	auth := smtp.PlainAuth("", config.Username, config.Password, config.Host)
	return &SMTPEmailService{
		host:     config.Host,
		port:     config.Port,
		username: config.Username,
		password: config.Password,
		from:     config.From,
		auth:     auth,
	}
}

// SendVerificationEmail 发送邮箱验证邮件
func (s *SMTPEmailService) SendVerificationEmail(ctx context.Context, email, token string) error {
	subject := "Verify Your Email Address"
	body := fmt.Sprintf(`
<!DOCTYPE html>
<html>
<head>
    <meta charset="UTF-8">
    <title>Email Verification</title>
    <style>
        body { font-family: Arial, sans-serif; line-height: 1.6; color: #333; }
        .container { max-width: 600px; margin: 0 auto; padding: 20px; }
        .header { background-color: #007bff; color: white; padding: 20px; text-align: center; }
        .content { padding: 20px; background-color: #f9f9f9; }
        .button { display: inline-block; padding: 12px 24px; background-color: #007bff; color: white; text-decoration: none; border-radius: 4px; margin: 20px 0; }
        .footer { padding: 20px; text-align: center; color: #666; font-size: 12px; }
    </style>
</head>
<body>
    <div class="container">
        <div class="header">
            <h1>Email Verification</h1>
        </div>
        <div class="content">
            <h2>Welcome to our Ticketing System!</h2>
            <p>Thank you for registering with us. To complete your registration, please verify your email address by clicking the button below:</p>
            <a href="http://localhost:3000/verify-email?token=%s" class="button">Verify Email Address</a>
            <p>If the button doesn't work, you can copy and paste this link into your browser:</p>
            <p>http://localhost:3000/verify-email?token=%s</p>
            <p>This verification link will expire in 24 hours.</p>
            <p>If you didn't create an account with us, please ignore this email.</p>
        </div>
        <div class="footer">
            <p>© 2024 Ticketing System. All rights reserved.</p>
        </div>
    </div>
</body>
</html>
	`, token, token)

	return s.sendEmail(email, subject, body)
}

// SendPasswordResetEmail 发送密码重置邮件
func (s *SMTPEmailService) SendPasswordResetEmail(ctx context.Context, email, token string) error {
	subject := "Reset Your Password"
	body := fmt.Sprintf(`
<!DOCTYPE html>
<html>
<head>
    <meta charset="UTF-8">
    <title>Password Reset</title>
    <style>
        body { font-family: Arial, sans-serif; line-height: 1.6; color: #333; }
        .container { max-width: 600px; margin: 0 auto; padding: 20px; }
        .header { background-color: #dc3545; color: white; padding: 20px; text-align: center; }
        .content { padding: 20px; background-color: #f9f9f9; }
        .button { display: inline-block; padding: 12px 24px; background-color: #dc3545; color: white; text-decoration: none; border-radius: 4px; margin: 20px 0; }
        .footer { padding: 20px; text-align: center; color: #666; font-size: 12px; }
        .warning { background-color: #fff3cd; border: 1px solid #ffeaa7; padding: 10px; border-radius: 4px; margin: 10px 0; }
    </style>
</head>
<body>
    <div class="container">
        <div class="header">
            <h1>Password Reset Request</h1>
        </div>
        <div class="content">
            <h2>Reset Your Password</h2>
            <p>We received a request to reset your password. If you made this request, click the button below to reset your password:</p>
            <a href="http://localhost:3000/reset-password?token=%s" class="button">Reset Password</a>
            <p>If the button doesn't work, you can copy and paste this link into your browser:</p>
            <p>http://localhost:3000/reset-password?token=%s</p>
            <div class="warning">
                <strong>Security Notice:</strong>
                <ul>
                    <li>This link will expire in 1 hour for security reasons</li>
                    <li>If you didn't request this password reset, please ignore this email</li>
                    <li>Your password will remain unchanged until you create a new one</li>
                </ul>
            </div>
        </div>
        <div class="footer">
            <p>© 2024 Ticketing System. All rights reserved.</p>
        </div>
    </div>
</body>
</html>
	`, token, token)

	return s.sendEmail(email, subject, body)
}

// SendWelcomeEmail 发送欢迎邮件
func (s *SMTPEmailService) SendWelcomeEmail(ctx context.Context, email, username string) error {
	subject := "Welcome to Ticketing System!"
	body := fmt.Sprintf(`
<!DOCTYPE html>
<html>
<head>
    <meta charset="UTF-8">
    <title>Welcome</title>
    <style>
        body { font-family: Arial, sans-serif; line-height: 1.6; color: #333; }
        .container { max-width: 600px; margin: 0 auto; padding: 20px; }
        .header { background-color: #28a745; color: white; padding: 20px; text-align: center; }
        .content { padding: 20px; background-color: #f9f9f9; }
        .feature { background-color: white; padding: 15px; margin: 10px 0; border-radius: 4px; border-left: 4px solid #28a745; }
        .button { display: inline-block; padding: 12px 24px; background-color: #28a745; color: white; text-decoration: none; border-radius: 4px; margin: 20px 0; }
        .footer { padding: 20px; text-align: center; color: #666; font-size: 12px; }
    </style>
</head>
<body>
    <div class="container">
        <div class="header">
            <h1>Welcome to Ticketing System!</h1>
        </div>
        <div class="content">
            <h2>Hello %s!</h2>
            <p>Congratulations! Your account has been successfully created and verified. You can now start using our ticketing system.</p>
            
            <h3>What you can do:</h3>
            <div class="feature">
                <strong>Create Tickets:</strong> Submit support requests and track their progress
            </div>
            <div class="feature">
                <strong>Manage Profile:</strong> Update your personal information and preferences
            </div>
            <div class="feature">
                <strong>Track History:</strong> View all your past tickets and interactions
            </div>
            <div class="feature">
                <strong>Secure Access:</strong> Enable two-factor authentication for enhanced security
            </div>
            
            <a href="http://localhost:3000/dashboard" class="button">Go to Dashboard</a>
            
            <p>If you have any questions or need assistance, feel free to contact our support team.</p>
        </div>
        <div class="footer">
            <p>© 2024 Ticketing System. All rights reserved.</p>
        </div>
    </div>
</body>
</html>
	`, username)

	return s.sendEmail(email, subject, body)
}

// SendOTPEmail 发送OTP验证码邮件
func (s *SMTPEmailService) SendOTPEmail(ctx context.Context, email, code string) error {
	subject := "Your Verification Code"
	body := fmt.Sprintf(`
<!DOCTYPE html>
<html>
<head>
    <meta charset="UTF-8">
    <title>Verification Code</title>
    <style>
        body { font-family: Arial, sans-serif; line-height: 1.6; color: #333; }
        .container { max-width: 600px; margin: 0 auto; padding: 20px; }
        .header { background-color: #6f42c1; color: white; padding: 20px; text-align: center; }
        .content { padding: 20px; background-color: #f9f9f9; }
        .code { font-size: 32px; font-weight: bold; text-align: center; background-color: #6f42c1; color: white; padding: 20px; border-radius: 8px; margin: 20px 0; letter-spacing: 8px; }
        .footer { padding: 20px; text-align: center; color: #666; font-size: 12px; }
        .warning { background-color: #fff3cd; border: 1px solid #ffeaa7; padding: 10px; border-radius: 4px; margin: 10px 0; }
    </style>
</head>
<body>
    <div class="container">
        <div class="header">
            <h1>Verification Code</h1>
        </div>
        <div class="content">
            <h2>Your One-Time Password</h2>
            <p>Use the following verification code to complete your login:</p>
            
            <div class="code">%s</div>
            
            <div class="warning">
                <strong>Important:</strong>
                <ul>
                    <li>This code will expire in 5 minutes</li>
                    <li>Do not share this code with anyone</li>
                    <li>If you didn't request this code, please ignore this email</li>
                </ul>
            </div>
            
            <p>If you're having trouble, please contact our support team.</p>
        </div>
        <div class="footer">
            <p>© 2024 Ticketing System. All rights reserved.</p>
        </div>
    </div>
</body>
</html>
	`, code)

	return s.sendEmail(email, subject, body)
}

// sendEmail 发送邮件的通用方法
func (s *SMTPEmailService) sendEmail(to, subject, body string) error {
	// 构建邮件头
	headers := make(map[string]string)
	headers["From"] = s.from
	headers["To"] = to
	headers["Subject"] = subject
	headers["MIME-Version"] = "1.0"
	headers["Content-Type"] = "text/html; charset=UTF-8"
	headers["Date"] = time.Now().Format(time.RFC1123Z)

	// 构建邮件消息
	message := ""
	for k, v := range headers {
		message += fmt.Sprintf("%s: %s\r\n", k, v)
	}
	message += "\r\n" + body

	// 发送邮件
	addr := fmt.Sprintf("%s:%s", s.host, s.port)
	err := smtp.SendMail(addr, s.auth, s.from, []string{to}, []byte(message))
	if err != nil {
		return fmt.Errorf("failed to send email: %w", err)
	}

	return nil
}

// MockEmailService 模拟邮件服务（用于测试）
type MockEmailService struct {
	sentEmails []SentEmail
}

// SentEmail 已发送邮件记录
type SentEmail struct {
	To      string
	Subject string
	Body    string
	SentAt  time.Time
}

// NewMockEmailService 创建模拟邮件服务
func NewMockEmailService() *MockEmailService {
	return &MockEmailService{
		sentEmails: make([]SentEmail, 0),
	}
}

// SendVerificationEmail 模拟发送邮箱验证邮件
func (m *MockEmailService) SendVerificationEmail(ctx context.Context, email, token string) error {
	m.sentEmails = append(m.sentEmails, SentEmail{
		To:      email,
		Subject: "Verify Your Email Address",
		Body:    fmt.Sprintf("Verification token: %s", token),
		SentAt:  time.Now(),
	})
	return nil
}

// SendPasswordResetEmail 模拟发送密码重置邮件
func (m *MockEmailService) SendPasswordResetEmail(ctx context.Context, email, token string) error {
	m.sentEmails = append(m.sentEmails, SentEmail{
		To:      email,
		Subject: "Reset Your Password",
		Body:    fmt.Sprintf("Reset token: %s", token),
		SentAt:  time.Now(),
	})
	return nil
}

// SendWelcomeEmail 模拟发送欢迎邮件
func (m *MockEmailService) SendWelcomeEmail(ctx context.Context, email, username string) error {
	m.sentEmails = append(m.sentEmails, SentEmail{
		To:      email,
		Subject: "Welcome to Ticketing System!",
		Body:    fmt.Sprintf("Welcome %s!", username),
		SentAt:  time.Now(),
	})
	return nil
}

// SendOTPEmail 模拟发送OTP验证码邮件
func (m *MockEmailService) SendOTPEmail(ctx context.Context, email, code string) error {
	m.sentEmails = append(m.sentEmails, SentEmail{
		To:      email,
		Subject: "Your Verification Code",
		Body:    fmt.Sprintf("OTP Code: %s", code),
		SentAt:  time.Now(),
	})
	return nil
}

// GetSentEmails 获取已发送邮件列表
func (m *MockEmailService) GetSentEmails() []SentEmail {
	return m.sentEmails
}

// GetLastSentEmail 获取最后发送的邮件
func (m *MockEmailService) GetLastSentEmail() *SentEmail {
	if len(m.sentEmails) == 0 {
		return nil
	}
	return &m.sentEmails[len(m.sentEmails)-1]
}

// Clear 清空已发送邮件记录
func (m *MockEmailService) Clear() {
	m.sentEmails = make([]SentEmail, 0)
}

// 邮件模板辅助函数

// ValidateEmailTemplate 验证邮件模板
func ValidateEmailTemplate(template string) error {
	if template == "" {
		return fmt.Errorf("email template cannot be empty")
	}
	if !strings.Contains(template, "%s") {
		return fmt.Errorf("email template must contain placeholder")
	}
	return nil
}

// SanitizeEmailContent 清理邮件内容
func SanitizeEmailContent(content string) string {
	// 移除潜在的恶意脚本
	content = strings.ReplaceAll(content, "<script", "&lt;script")
	content = strings.ReplaceAll(content, "</script>", "&lt;/script&gt;")
	content = strings.ReplaceAll(content, "javascript:", "")
	return content
}

// FormatEmailAddress 格式化邮件地址
func FormatEmailAddress(name, email string) string {
	if name == "" {
		return email
	}
	return fmt.Sprintf("%s <%s>", name, email)
}

// IsValidEmailFormat 验证邮件格式
func IsValidEmailFormat(email string) bool {
	return IsValidEmail(email)
}
