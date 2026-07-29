package mailer

import (
	"encoding/base64"
	"io"
	"mime"
	"net/mail"
	"strings"
	"testing"
)

func TestBuildHTMLMessageRejectsHeaderInjection(t *testing.T) {
	tests := []struct {
		name     string
		from     string
		fromName string
		subject  string
	}{
		{name: "from address", from: "sender@example.com\r\nBcc: victim@example.com", fromName: "ChronoDesk", subject: "测试"},
		{name: "from name", from: "sender@example.com", fromName: "ChronoDesk\r\nBcc: victim@example.com", subject: "测试"},
		{name: "subject", from: "sender@example.com", fromName: "ChronoDesk", subject: "测试\r\nBcc: victim@example.com"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := BuildHTMLMessage(test.from, test.fromName, test.subject, "<p>安全</p>"); err == nil {
				t.Fatal("expected header injection to be rejected")
			}
		})
	}
}

func TestBuildHTMLMessageEncodesChineseAndBody(t *testing.T) {
	message, err := BuildHTMLMessage(
		"sender@example.com",
		"ChronoDesk 工单系统",
		"工单更新：测试",
		"<p>合法中文正文</p>",
	)
	if err != nil {
		t.Fatal(err)
	}

	parsed, err := mail.ReadMessage(strings.NewReader(string(message)))
	if err != nil {
		t.Fatalf("parse message: %v", err)
	}
	if parsed.Header.Get("To") != "undisclosed-recipients:;" {
		t.Fatalf("To header = %q", parsed.Header.Get("To"))
	}
	if decoded, err := (&mail.AddressParser{}).Parse(parsed.Header.Get("From")); err != nil ||
		decoded.Name != "ChronoDesk 工单系统" {
		t.Fatalf("From header did not preserve Chinese: address=%+v error=%v", decoded, err)
	}
	subject, err := new(mime.WordDecoder).DecodeHeader(parsed.Header.Get("Subject"))
	if err != nil || subject != "工单更新：测试" {
		t.Fatalf("Subject = %q, error=%v", subject, err)
	}

	encodedBody := strings.ReplaceAll(readAll(t, parsed), "\r\n", "")
	body, err := base64.StdEncoding.DecodeString(encodedBody)
	if err != nil {
		t.Fatalf("decode MIME body: %v", err)
	}
	if string(body) != "<p>合法中文正文</p>" {
		t.Fatalf("body = %q", body)
	}
}

func TestCanonicalMailboxRejectsDisplayNameAndSMTPUTF8(t *testing.T) {
	for _, value := range []string{
		"Attacker <sender@example.com>",
		"用户@example.com",
		"one@example.com, two@example.com",
	} {
		if _, err := CanonicalMailbox(value); err == nil {
			t.Fatalf("CanonicalMailbox(%q) unexpectedly succeeded", value)
		}
	}
}

func readAll(t *testing.T, message *mail.Message) string {
	t.Helper()
	body, err := io.ReadAll(message.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}
