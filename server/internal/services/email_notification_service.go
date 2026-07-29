package services

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	htmltemplate "html/template"
	texttemplate "text/template"

	"github.com/seaworld008/chronodesk/server/internal/mailer"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/gorm"
)

// EmailNotificationServiceInterface 邮件通知服务接口
type EmailNotificationServiceInterface interface {
	SendEmailNotificationOutboxAttempt(ctx context.Context, notification *models.Notification) error
	GetEmailTemplate(notificationType models.NotificationType) (*EmailTemplate, error)
}

// EmailTemplate 邮件模板结构
type EmailTemplate struct {
	Subject  string
	HTMLBody string
	TextBody string
}

// EmailNotificationService 邮件通知服务实现
type EmailNotificationService struct {
	db                  *gorm.DB
	emailConfigService  EmailConfigServiceInterface
	notificationService NotificationServiceInterface
}

// NewEmailNotificationService 创建邮件通知服务
func NewEmailNotificationService(
	db *gorm.DB,
	emailConfigService EmailConfigServiceInterface,
	notificationService NotificationServiceInterface,
) EmailNotificationServiceInterface {
	return &EmailNotificationService{
		db:                  db,
		emailConfigService:  emailConfigService,
		notificationService: notificationService,
	}
}

// SendEmailNotificationOutboxAttempt performs one bounded SMTP attempt for a
// durable Outbox delivery. Retry scheduling belongs exclusively to the shared
// Outbox worker; SMTP itself does not offer exactly-once delivery.
func (s *EmailNotificationService) SendEmailNotificationOutboxAttempt(
	ctx context.Context,
	notification *models.Notification,
) error {
	if notification == nil || notification.ID == 0 {
		return fmt.Errorf("邮件通知记录无效")
	}
	var persisted models.Notification
	if err := s.db.WithContext(ctx).
		Preload("Recipient").
		Preload("Sender").
		Preload("RelatedTicket").
		First(&persisted, notification.ID).Error; err != nil {
		return fmt.Errorf("获取邮件通知失败: %w", err)
	}
	*notification = persisted
	if notification.IsSent {
		return nil
	}
	if notification.Recipient == nil {
		notification.DeliveryStatus = "skipped_recipient_unavailable"
		return s.db.WithContext(ctx).Save(notification).Error
	}

	// 检查系统是否可以发送邮件
	canSend, err := s.emailConfigService.CanSendEmail(ctx)
	if err != nil {
		return fmt.Errorf("检查邮件发送状态失败: %w", err)
	}
	if !canSend {
		return fmt.Errorf("系统邮件功能未启用")
	}

	// 获取SMTP配置
	smtpConfig, err := s.emailConfigService.GetSMTPConfig(ctx)
	if err != nil {
		return fmt.Errorf("获取SMTP配置失败: %w", err)
	}

	// 检查用户邮件偏好
	emailEnabled, err := s.isEmailEnabledForUser(ctx, notification.RecipientID, notification.Type)
	if err != nil {
		return fmt.Errorf("检查用户邮件偏好失败: %w", err)
	}
	if !emailEnabled {
		notification.DeliveryStatus = "skipped_user_preference"
		return s.db.WithContext(ctx).Save(notification).Error
	}

	// 检查用户邮箱是否有效
	if notification.Recipient.Email == "" {
		notification.DeliveryStatus = "failed_no_email"
		notification.ErrorMessage = "用户未设置邮箱地址"
		if err := s.db.WithContext(ctx).Save(notification).Error; err != nil {
			return fmt.Errorf("保存邮件投递失败状态: %w", err)
		}
		return fmt.Errorf("用户未设置邮箱地址")
	}

	// 获取邮件模板
	template, err := s.GetEmailTemplate(notification.Type)
	if err != nil {
		return fmt.Errorf("获取邮件模板失败: %w", err)
	}

	// 渲染邮件内容
	subject, htmlBody, err := s.renderEmailContent(template, notification)
	if err != nil {
		return fmt.Errorf("渲染邮件内容失败: %w", err)
	}

	// 发送邮件
	err = s.sendEmail(
		ctx,
		smtpConfig,
		notification.Recipient.Email,
		subject,
		htmlBody,
	)
	if err != nil {
		// 更新失败状态
		notification.ErrorMessage = err.Error()
		notification.DeliveryStatus = "failed"
		if saveErr := s.db.WithContext(ctx).Save(notification).Error; saveErr != nil {
			return fmt.Errorf("发送邮件失败且无法保存投递状态: %v: %w", err, saveErr)
		}
		return fmt.Errorf("发送邮件失败: %w", err)
	}

	// 更新成功状态
	notification.MarkAsSent()
	notification.MarkAsDelivered()
	notification.DeliveryStatus = "delivered"
	notification.ErrorMessage = ""
	notification.NextRetryAt = nil
	if err := s.db.WithContext(ctx).Save(notification).Error; err != nil {
		return fmt.Errorf("更新通知状态失败: %w", err)
	}

	return nil
}

// GetEmailTemplate 获取邮件模板
func (s *EmailNotificationService) GetEmailTemplate(notificationType models.NotificationType) (*EmailTemplate, error) {
	switch notificationType {
	case models.NotificationTypeTicketAssigned:
		return &EmailTemplate{
			Subject:  "新工单已分配 - {{.Title}}",
			HTMLBody: s.getTicketAssignedHTMLTemplate(),
			TextBody: s.getTicketAssignedTextTemplate(),
		}, nil
	case models.NotificationTypeTicketStatusChanged:
		return &EmailTemplate{
			Subject:  "工单状态更新 - {{.Title}}",
			HTMLBody: s.getTicketStatusChangedHTMLTemplate(),
			TextBody: s.getTicketStatusChangedTextTemplate(),
		}, nil
	case models.NotificationTypeTicketCommented:
		return &EmailTemplate{
			Subject:  "工单新回复 - {{.Title}}",
			HTMLBody: s.getTicketCommentedHTMLTemplate(),
			TextBody: s.getTicketCommentedTextTemplate(),
		}, nil
	case models.NotificationTypeTicketCreated:
		return &EmailTemplate{
			Subject:  "新工单创建 - {{.Title}}",
			HTMLBody: s.getTicketCreatedHTMLTemplate(),
			TextBody: s.getTicketCreatedTextTemplate(),
		}, nil
	case models.NotificationTypeTicketOverdue:
		return &EmailTemplate{
			Subject:  "工单即将逾期 - {{.Title}}",
			HTMLBody: s.getTicketOverdueHTMLTemplate(),
			TextBody: s.getTicketOverdueTextTemplate(),
		}, nil
	case models.NotificationTypeSystemMaintenance:
		return &EmailTemplate{
			Subject:  "系统维护通知 - {{.Title}}",
			HTMLBody: s.getSystemMaintenanceHTMLTemplate(),
			TextBody: s.getSystemMaintenanceTextTemplate(),
		}, nil
	case models.NotificationTypeSystemAlert:
		return &EmailTemplate{
			Subject:  "系统警报 - {{.Title}}",
			HTMLBody: s.getSystemAlertHTMLTemplate(),
			TextBody: s.getSystemAlertTextTemplate(),
		}, nil
	default:
		return &EmailTemplate{
			Subject:  "通知 - {{.Title}}",
			HTMLBody: s.getDefaultHTMLTemplate(),
			TextBody: s.getDefaultTextTemplate(),
		}, nil
	}
}

// isEmailEnabledForUser 检查用户是否启用了邮件通知
func (s *EmailNotificationService) isEmailEnabledForUser(ctx context.Context, userID uint, notificationType models.NotificationType) (bool, error) {
	var preference models.NotificationPreference
	err := s.db.WithContext(ctx).
		Where("user_id = ? AND notification_type = ?", userID, notificationType).
		First(&preference).Error

	if err != nil {
		if err == gorm.ErrRecordNotFound {
			// 没有设置偏好，默认启用邮件
			return true, nil
		}
		return false, err
	}

	return preference.EmailEnabled, nil
}

// sendEmail 发送邮件
func (s *EmailNotificationService) sendEmail(
	ctx context.Context,
	config *models.EmailConfig,
	to string,
	subject string,
	body string,
) error {
	recipient, err := mailer.CanonicalMailbox(to)
	if err != nil {
		return fmt.Errorf("收件邮箱无效: %w", err)
	}
	sender, err := mailer.CanonicalMailbox(config.FromEmail)
	if err != nil {
		return fmt.Errorf("发件邮箱无效: %w", err)
	}

	// 构建邮件消息
	msg, err := s.buildEmailMessage(sender, config.FromName, subject, body)
	if err != nil {
		return fmt.Errorf("构建邮件失败: %w", err)
	}

	transport, err := smtpTransportForEmailConfig(config)
	if err != nil {
		return err
	}
	return transport.Send(ctx, sender, []string{recipient}, msg)
}

// buildEmailMessage 构建邮件消息
func (s *EmailNotificationService) buildEmailMessage(fromEmail, fromName, subject, htmlBody string) ([]byte, error) {
	return mailer.BuildHTMLMessage(fromEmail, fromName, subject, htmlBody)
}

// renderEmailContent 渲染邮件内容
func (s *EmailNotificationService) renderEmailContent(template *EmailTemplate, notification *models.Notification) (string, string, error) {
	// 创建模板数据
	data := s.buildTemplateData(notification)

	subjectTemplate, err := texttemplate.New("subject").Parse(template.Subject)
	if err != nil {
		return "", "", fmt.Errorf("解析邮件主题模板失败: %w", err)
	}
	var subject bytes.Buffer
	if err := subjectTemplate.Execute(&subject, data); err != nil {
		return "", "", fmt.Errorf("渲染邮件主题失败: %w", err)
	}

	// html/template 按文本、属性、CSS 和 URL 上下文分别转义数据。
	bodyTemplate, err := htmltemplate.New("body").Parse(template.HTMLBody)
	if err != nil {
		return "", "", fmt.Errorf("解析HTML邮件模板失败: %w", err)
	}
	var htmlBody bytes.Buffer
	if err := bodyTemplate.Execute(&htmlBody, data); err != nil {
		return "", "", fmt.Errorf("渲染HTML邮件失败: %w", err)
	}

	return subject.String(), htmlBody.String(), nil
}

// buildTemplateData 构建模板数据
func (s *EmailNotificationService) buildTemplateData(notification *models.Notification) map[string]interface{} {
	data := map[string]interface{}{
		"Title":     notification.Title,
		"Content":   notification.Content,
		"Type":      string(notification.Type),
		"Priority":  string(notification.Priority),
		"CreatedAt": notification.CreatedAt.Format("2006-01-02 15:04:05"),
		"ActionURL": notification.ActionURL,
	}

	// 添加接收者信息
	if notification.Recipient != nil {
		data["RecipientName"] = notification.Recipient.Username
		data["RecipientEmail"] = notification.Recipient.Email
	}

	// 添加发送者信息
	if notification.Sender != nil {
		data["SenderName"] = notification.Sender.Username
	}

	// 添加相关工单信息
	if notification.RelatedTicket != nil {
		data["TicketNumber"] = notification.RelatedTicket.TicketNumber
		data["TicketTitle"] = notification.RelatedTicket.Title
		data["TicketStatus"] = string(notification.RelatedTicket.Status)
		data["TicketPriority"] = string(notification.RelatedTicket.Priority)
	}

	// 解析metadata
	if notification.Metadata != "" {
		var metadata map[string]interface{}
		if err := json.Unmarshal([]byte(notification.Metadata), &metadata); err == nil {
			for k, v := range metadata {
				if _, reserved := data[k]; !reserved {
					data[k] = v
				}
			}
		}
	}

	return data
}

// HTML模板定义
func (s *EmailNotificationService) getTicketAssignedHTMLTemplate() string {
	return `<!DOCTYPE html>
<html>
<head>
    <meta charset="UTF-8">
    <title>新工单分配</title>
    <style>
        body { font-family: Arial, sans-serif; line-height: 1.6; color: #333; }
        .container { max-width: 600px; margin: 0 auto; padding: 20px; }
        .header { background-color: #007bff; color: white; padding: 20px; text-align: center; }
        .content { padding: 20px; background-color: #f9f9f9; }
        .ticket-info { background-color: white; padding: 15px; margin: 15px 0; border-radius: 4px; }
        .button { display: inline-block; padding: 12px 24px; background-color: #007bff; color: white; text-decoration: none; border-radius: 4px; margin: 20px 0; }
        .footer { padding: 20px; text-align: center; color: #666; font-size: 12px; }
        .priority-high { border-left: 4px solid #dc3545; }
        .priority-normal { border-left: 4px solid #28a745; }
        .priority-urgent { border-left: 4px solid #ffc107; }
    </style>
</head>
<body>
    <div class="container">
        <div class="header">
            <h1>🎫 新工单已分配</h1>
        </div>
        <div class="content">
            <h2>您好，{{.RecipientName}}！</h2>
            <p>有一个新的工单已分配给您，请及时处理：</p>
            
            <div class="ticket-info priority-{{.TicketPriority}}">
                <h3>{{.TicketTitle}}</h3>
                <p><strong>工单编号：</strong>{{.TicketNumber}}</p>
                <p><strong>优先级：</strong>{{.TicketPriority}}</p>
                <p><strong>创建时间：</strong>{{.CreatedAt}}</p>
                <p><strong>描述：</strong></p>
                <p>{{.Content}}</p>
            </div>
            
            <a href="{{.ActionURL}}" class="button">查看工单详情</a>
            
            <p>请尽快登录系统查看和处理此工单。</p>
        </div>
        <div class="footer">
            <p>© ChronoDesk. 保留所有权利.</p>
            <p>如果您不想接收此类邮件，请在系统中修改通知设置。</p>
        </div>
    </div>
</body>
</html>`
}

func (s *EmailNotificationService) getTicketStatusChangedHTMLTemplate() string {
	return `<!DOCTYPE html>
<html>
<head>
    <meta charset="UTF-8">
    <title>工单状态更新</title>
    <style>
        body { font-family: Arial, sans-serif; line-height: 1.6; color: #333; }
        .container { max-width: 600px; margin: 0 auto; padding: 20px; }
        .header { background-color: #28a745; color: white; padding: 20px; text-align: center; }
        .content { padding: 20px; background-color: #f9f9f9; }
        .status-change { background-color: white; padding: 15px; margin: 15px 0; border-radius: 4px; border-left: 4px solid #28a745; }
        .button { display: inline-block; padding: 12px 24px; background-color: #28a745; color: white; text-decoration: none; border-radius: 4px; margin: 20px 0; }
        .footer { padding: 20px; text-align: center; color: #666; font-size: 12px; }
    </style>
</head>
<body>
    <div class="container">
        <div class="header">
            <h1>📊 工单状态更新</h1>
        </div>
        <div class="content">
            <h2>您好，{{.RecipientName}}！</h2>
            <p>您关注的工单状态已更新：</p>
            
            <div class="status-change">
                <h3>{{.TicketTitle}}</h3>
                <p><strong>工单编号：</strong>{{.TicketNumber}}</p>
                <p><strong>状态变更：</strong>{{.old_status}} → {{.new_status}}</p>
                <p><strong>更新时间：</strong>{{.CreatedAt}}</p>
                <p><strong>更新说明：</strong></p>
                <p>{{.Content}}</p>
            </div>
            
            <a href="{{.ActionURL}}" class="button">查看工单详情</a>
        </div>
        <div class="footer">
            <p>© ChronoDesk. 保留所有权利.</p>
        </div>
    </div>
</body>
</html>`
}

func (s *EmailNotificationService) getTicketCommentedHTMLTemplate() string {
	return `<!DOCTYPE html>
<html>
<head>
    <meta charset="UTF-8">
    <title>工单新回复</title>
    <style>
        body { font-family: Arial, sans-serif; line-height: 1.6; color: #333; }
        .container { max-width: 600px; margin: 0 auto; padding: 20px; }
        .header { background-color: #6f42c1; color: white; padding: 20px; text-align: center; }
        .content { padding: 20px; background-color: #f9f9f9; }
        .comment { background-color: white; padding: 15px; margin: 15px 0; border-radius: 4px; border-left: 4px solid #6f42c1; }
        .button { display: inline-block; padding: 12px 24px; background-color: #6f42c1; color: white; text-decoration: none; border-radius: 4px; margin: 20px 0; }
        .footer { padding: 20px; text-align: center; color: #666; font-size: 12px; }
    </style>
</head>
<body>
    <div class="container">
        <div class="header">
            <h1>💬 工单新回复</h1>
        </div>
        <div class="content">
            <h2>您好，{{.RecipientName}}！</h2>
            <p>您的工单收到了新的回复：</p>
            
            <div class="comment">
                <h3>{{.TicketTitle}}</h3>
                <p><strong>工单编号：</strong>{{.TicketNumber}}</p>
                <p><strong>回复者：</strong>{{.SenderName}}</p>
                <p><strong>回复时间：</strong>{{.CreatedAt}}</p>
                <p><strong>回复内容：</strong></p>
                <p>{{.Content}}</p>
            </div>
            
            <a href="{{.ActionURL}}" class="button">查看完整对话</a>
        </div>
        <div class="footer">
            <p>© ChronoDesk. 保留所有权利.</p>
        </div>
    </div>
</body>
</html>`
}

func (s *EmailNotificationService) getTicketCreatedHTMLTemplate() string {
	return `<!DOCTYPE html>
<html>
<head>
    <meta charset="UTF-8">
    <title>新工单创建</title>
    <style>
        body { font-family: Arial, sans-serif; line-height: 1.6; color: #333; }
        .container { max-width: 600px; margin: 0 auto; padding: 20px; }
        .header { background-color: #17a2b8; color: white; padding: 20px; text-align: center; }
        .content { padding: 20px; background-color: #f9f9f9; }
        .ticket-info { background-color: white; padding: 15px; margin: 15px 0; border-radius: 4px; border-left: 4px solid #17a2b8; }
        .button { display: inline-block; padding: 12px 24px; background-color: #17a2b8; color: white; text-decoration: none; border-radius: 4px; margin: 20px 0; }
        .footer { padding: 20px; text-align: center; color: #666; font-size: 12px; }
    </style>
</head>
<body>
    <div class="container">
        <div class="header">
            <h1>🆕 新工单创建</h1>
        </div>
        <div class="content">
            <h2>您好，{{.RecipientName}}！</h2>
            <p>有一个新的工单已创建：</p>
            
            <div class="ticket-info">
                <h3>{{.TicketTitle}}</h3>
                <p><strong>工单编号：</strong>{{.TicketNumber}}</p>
                <p><strong>创建者：</strong>{{.SenderName}}</p>
                <p><strong>优先级：</strong>{{.TicketPriority}}</p>
                <p><strong>创建时间：</strong>{{.CreatedAt}}</p>
                <p><strong>描述：</strong></p>
                <p>{{.Content}}</p>
            </div>
            
            <a href="{{.ActionURL}}" class="button">查看工单详情</a>
        </div>
        <div class="footer">
            <p>© ChronoDesk. 保留所有权利.</p>
        </div>
    </div>
</body>
</html>`
}

func (s *EmailNotificationService) getTicketOverdueHTMLTemplate() string {
	return `<!DOCTYPE html>
<html>
<head>
    <meta charset="UTF-8">
    <title>工单即将逾期</title>
    <style>
        body { font-family: Arial, sans-serif; line-height: 1.6; color: #333; }
        .container { max-width: 600px; margin: 0 auto; padding: 20px; }
        .header { background-color: #ffc107; color: #212529; padding: 20px; text-align: center; }
        .content { padding: 20px; background-color: #f9f9f9; }
        .warning { background-color: #fff3cd; border: 1px solid #ffeaa7; padding: 15px; margin: 15px 0; border-radius: 4px; border-left: 4px solid #ffc107; }
        .button { display: inline-block; padding: 12px 24px; background-color: #ffc107; color: #212529; text-decoration: none; border-radius: 4px; margin: 20px 0; }
        .footer { padding: 20px; text-align: center; color: #666; font-size: 12px; }
    </style>
</head>
<body>
    <div class="container">
        <div class="header">
            <h1>⚠️ 工单即将逾期</h1>
        </div>
        <div class="content">
            <h2>您好，{{.RecipientName}}！</h2>
            <p>以下工单即将逾期，请及时处理：</p>
            
            <div class="warning">
                <h3>{{.TicketTitle}}</h3>
                <p><strong>工单编号：</strong>{{.TicketNumber}}</p>
                <p><strong>当前状态：</strong>{{.TicketStatus}}</p>
                <p><strong>优先级：</strong>{{.TicketPriority}}</p>
                <p><strong>逾期提醒：</strong>{{.Content}}</p>
            </div>
            
            <a href="{{.ActionURL}}" class="button">立即处理</a>
        </div>
        <div class="footer">
            <p>© ChronoDesk. 保留所有权利.</p>
        </div>
    </div>
</body>
</html>`
}

func (s *EmailNotificationService) getSystemMaintenanceHTMLTemplate() string {
	return `<!DOCTYPE html>
<html>
<head>
    <meta charset="UTF-8">
    <title>系统维护通知</title>
    <style>
        body { font-family: Arial, sans-serif; line-height: 1.6; color: #333; }
        .container { max-width: 600px; margin: 0 auto; padding: 20px; }
        .header { background-color: #6c757d; color: white; padding: 20px; text-align: center; }
        .content { padding: 20px; background-color: #f9f9f9; }
        .maintenance-info { background-color: white; padding: 15px; margin: 15px 0; border-radius: 4px; border-left: 4px solid #6c757d; }
        .footer { padding: 20px; text-align: center; color: #666; font-size: 12px; }
    </style>
</head>
<body>
    <div class="container">
        <div class="header">
            <h1>🔧 系统维护通知</h1>
        </div>
        <div class="content">
            <h2>尊敬的用户，{{.RecipientName}}！</h2>
            <p>系统将进行例行维护：</p>
            
            <div class="maintenance-info">
                <h3>{{.Title}}</h3>
                <p><strong>维护时间：</strong>{{.CreatedAt}}</p>
                <p><strong>维护内容：</strong></p>
                <p>{{.Content}}</p>
            </div>
            
            <p>维护期间可能会影响系统正常使用，请您提前做好准备。感谢您的理解与配合！</p>
        </div>
        <div class="footer">
            <p>© ChronoDesk. 保留所有权利.</p>
        </div>
    </div>
</body>
</html>`
}

func (s *EmailNotificationService) getSystemAlertHTMLTemplate() string {
	return `<!DOCTYPE html>
<html>
<head>
    <meta charset="UTF-8">
    <title>系统警报</title>
    <style>
        body { font-family: Arial, sans-serif; line-height: 1.6; color: #333; }
        .container { max-width: 600px; margin: 0 auto; padding: 20px; }
        .header { background-color: #dc3545; color: white; padding: 20px; text-align: center; }
        .content { padding: 20px; background-color: #f9f9f9; }
        .alert { background-color: #f8d7da; border: 1px solid #f5c6cb; color: #721c24; padding: 15px; margin: 15px 0; border-radius: 4px; border-left: 4px solid #dc3545; }
        .footer { padding: 20px; text-align: center; color: #666; font-size: 12px; }
    </style>
</head>
<body>
    <div class="container">
        <div class="header">
            <h1>🚨 系统警报</h1>
        </div>
        <div class="content">
            <h2>您好，{{.RecipientName}}！</h2>
            <p>系统发生了重要警报：</p>
            
            <div class="alert">
                <h3>{{.Title}}</h3>
                <p><strong>警报时间：</strong>{{.CreatedAt}}</p>
                <p><strong>警报级别：</strong>{{.Priority}}</p>
                <p><strong>详细信息：</strong></p>
                <p>{{.Content}}</p>
            </div>
            
            <p>请管理员及时查看和处理此警报。</p>
        </div>
        <div class="footer">
            <p>© ChronoDesk. 保留所有权利.</p>
        </div>
    </div>
</body>
</html>`
}

func (s *EmailNotificationService) getDefaultHTMLTemplate() string {
	return `<!DOCTYPE html>
<html>
<head>
    <meta charset="UTF-8">
    <title>系统通知</title>
    <style>
        body { font-family: Arial, sans-serif; line-height: 1.6; color: #333; }
        .container { max-width: 600px; margin: 0 auto; padding: 20px; }
        .header { background-color: #007bff; color: white; padding: 20px; text-align: center; }
        .content { padding: 20px; background-color: #f9f9f9; }
        .notification { background-color: white; padding: 15px; margin: 15px 0; border-radius: 4px; border-left: 4px solid #007bff; }
        .footer { padding: 20px; text-align: center; color: #666; font-size: 12px; }
    </style>
</head>
<body>
    <div class="container">
        <div class="header">
            <h1>📧 系统通知</h1>
        </div>
        <div class="content">
            <h2>您好，{{.RecipientName}}！</h2>
            <p>您收到了一个新的系统通知：</p>
            
            <div class="notification">
                <h3>{{.Title}}</h3>
                <p><strong>通知时间：</strong>{{.CreatedAt}}</p>
                <p><strong>通知内容：</strong></p>
                <p>{{.Content}}</p>
            </div>
        </div>
        <div class="footer">
            <p>© ChronoDesk. 保留所有权利.</p>
        </div>
    </div>
</body>
</html>`
}

// 文本模板（用于不支持HTML的邮件客户端）
func (s *EmailNotificationService) getTicketAssignedTextTemplate() string {
	return `新工单已分配

您好，{{.RecipientName}}！

有一个新的工单已分配给您：

工单标题：{{.TicketTitle}}
工单编号：{{.TicketNumber}}
优先级：{{.TicketPriority}}
创建时间：{{.CreatedAt}}

描述：
{{.Content}}

请访问以下链接查看详情：{{.ActionURL}}

---
ChronoDesk`
}

func (s *EmailNotificationService) getTicketStatusChangedTextTemplate() string {
	return `工单状态更新

您好，{{.RecipientName}}！

您关注的工单状态已更新：

工单标题：{{.TicketTitle}}
工单编号：{{.TicketNumber}}
状态变更：{{.old_status}} → {{.new_status}}
更新时间：{{.CreatedAt}}

更新说明：
{{.Content}}

请访问以下链接查看详情：{{.ActionURL}}

---
ChronoDesk`
}

func (s *EmailNotificationService) getTicketCommentedTextTemplate() string {
	return `工单新回复

您好，{{.RecipientName}}！

您的工单收到了新的回复：

工单标题：{{.TicketTitle}}
工单编号：{{.TicketNumber}}
回复者：{{.SenderName}}
回复时间：{{.CreatedAt}}

回复内容：
{{.Content}}

请访问以下链接查看完整对话：{{.ActionURL}}

---
ChronoDesk`
}

func (s *EmailNotificationService) getTicketCreatedTextTemplate() string {
	return `新工单创建

您好，{{.RecipientName}}！

有一个新的工单已创建：

工单标题：{{.TicketTitle}}
工单编号：{{.TicketNumber}}
创建者：{{.SenderName}}
优先级：{{.TicketPriority}}
创建时间：{{.CreatedAt}}

描述：
{{.Content}}

请访问以下链接查看详情：{{.ActionURL}}

---
ChronoDesk`
}

func (s *EmailNotificationService) getTicketOverdueTextTemplate() string {
	return `工单即将逾期

您好，{{.RecipientName}}！

以下工单即将逾期，请及时处理：

工单标题：{{.TicketTitle}}
工单编号：{{.TicketNumber}}
当前状态：{{.TicketStatus}}
优先级：{{.TicketPriority}}

逾期提醒：
{{.Content}}

请立即访问以下链接处理：{{.ActionURL}}

---
ChronoDesk`
}

func (s *EmailNotificationService) getSystemMaintenanceTextTemplate() string {
	return `系统维护通知

尊敬的用户，{{.RecipientName}}！

系统将进行例行维护：

维护标题：{{.Title}}
维护时间：{{.CreatedAt}}

维护内容：
{{.Content}}

维护期间可能会影响系统正常使用，请您提前做好准备。
感谢您的理解与配合！

---
ChronoDesk`
}

func (s *EmailNotificationService) getSystemAlertTextTemplate() string {
	return `系统警报

您好，{{.RecipientName}}！

系统发生了重要警报：

警报标题：{{.Title}}
警报时间：{{.CreatedAt}}
警报级别：{{.Priority}}

详细信息：
{{.Content}}

请管理员及时查看和处理此警报。

---
ChronoDesk`
}

func (s *EmailNotificationService) getDefaultTextTemplate() string {
	return `系统通知

您好，{{.RecipientName}}！

您收到了一个新的系统通知：

通知标题：{{.Title}}
通知时间：{{.CreatedAt}}

通知内容：
{{.Content}}

---
ChronoDesk`
}
