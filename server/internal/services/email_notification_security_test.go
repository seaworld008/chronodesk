package services

import (
	"strings"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
)

func TestEmailNotificationRenderingEscapesUntrustedHTMLByContext(t *testing.T) {
	service := &EmailNotificationService{}
	emailTemplate, err := service.GetEmailTemplate(models.NotificationTypeTicketAssigned)
	if err != nil {
		t.Fatal(err)
	}

	notification := &models.Notification{
		Type:      models.NotificationTypeTicketAssigned,
		Title:     `合法中文 <img src=x onerror=alert(1)>`,
		Content:   `<script>alert("邮件")</script>合法正文`,
		Priority:  models.NotificationPriorityHigh,
		CreatedAt: time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC),
		ActionURL: `javascript:alert("跳转")`,
		Metadata:  `{"Title":"不得覆盖标题"}`,
		Recipient: &models.User{Username: `<b>收件人</b>`},
		RelatedTicket: &models.Ticket{
			TicketNumber: "CD-2026-1",
			Title:        `<svg onload=alert(1)>工单`,
			Priority:     models.TicketPriorityHigh,
		},
	}

	subject, htmlBody, err := service.renderEmailContent(emailTemplate, notification)
	if err != nil {
		t.Fatal(err)
	}
	if subject != "新工单已分配 - "+notification.Title {
		t.Fatalf("subject = %q", subject)
	}
	for _, unsafe := range []string{
		`<script>alert("邮件")</script>`,
		`<svg onload=alert(1)>`,
		`<b>收件人</b>`,
		`href="javascript:`,
		"不得覆盖标题",
	} {
		if strings.Contains(htmlBody, unsafe) {
			t.Fatalf("HTML邮件包含未转义数据 %q", unsafe)
		}
	}
	for _, expected := range []string{
		"工单",
		"合法正文",
		"&lt;script&gt;",
		"&lt;svg",
		"#ZgotmplZ",
	} {
		if !strings.Contains(htmlBody, expected) {
			t.Fatalf("HTML邮件缺少 %q", expected)
		}
	}
}

func TestEmailNotificationMessageRejectsHeaderInjection(t *testing.T) {
	service := &EmailNotificationService{}
	if _, err := service.buildEmailMessage(
		"sender@example.com",
		"ChronoDesk",
		"工单更新\r\nBcc: victim@example.com",
		"<p>正文</p>",
	); err == nil {
		t.Fatal("expected injected subject to be rejected")
	}
}
