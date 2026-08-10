package auth

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"html"
	"io"
	"mime"
	"net"
	"net/mail"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
)

type fixedSMTPConfigProvider struct {
	config *models.EmailConfig
}

func (p fixedSMTPConfigProvider) GetSMTPConfig(
	context.Context,
) (*models.EmailConfig, error) {
	copy := *p.config
	return &copy, nil
}

func TestAuthenticationEmailUsesConfiguredWebURLAndChineseCopy(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	message := make(chan []byte, 1)
	serverDone := make(chan error, 1)
	go serveOneAuthEmail(t, listener, message, serverDone)

	config := models.DefaultEmailConfig()
	config.SMTPHost = "127.0.0.1"
	config.SMTPPort = listener.Addr().(*net.TCPAddr).Port
	config.SMTPUsername = "chronodesk"
	config.SMTPPassword = "test-password"
	config.SMTPUseTLS = false
	config.FromEmail = "noreply@example.test"
	service, err := NewConfiguredSMTPEmailService(
		fixedSMTPConfigProvider{config: config},
		"https://support.example.test/chronodesk",
	)
	if err != nil {
		t.Fatal(err)
	}
	token := "token&scope=verify 用户"
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := service.SendVerificationEmail(
		ctx,
		"recipient@example.test",
		token,
	); err != nil {
		t.Fatal(err)
	}
	if err := <-serverDone; err != nil {
		t.Fatal(err)
	}

	raw := <-message
	parsed, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	subject, err := new(mime.WordDecoder).DecodeHeader(parsed.Header.Get("Subject"))
	if err != nil {
		t.Fatal(err)
	}
	if subject != "验证您的 ChronoDesk 邮箱" {
		t.Fatalf("邮件主题 = %q", subject)
	}
	encodedBody, err := io.ReadAll(parsed.Body)
	if err != nil {
		t.Fatal(err)
	}
	compactBody := strings.NewReplacer("\r", "", "\n", "").Replace(
		string(encodedBody),
	)
	decodedBody, err := base64.StdEncoding.DecodeString(compactBody)
	if err != nil {
		t.Fatal(err)
	}
	body := string(decodedBody)
	if strings.Contains(body, "localhost:3000") ||
		!strings.Contains(body, "验证您的邮箱") ||
		!strings.Contains(body, "support.example.test/chronodesk/#/verify-email") {
		t.Fatalf("验证邮件内容或链接不正确: %s", body)
	}
	start := strings.Index(body, "https://support.example.test")
	if start < 0 {
		t.Fatalf("未找到验证链接: %s", body)
	}
	end := strings.Index(body[start:], `"`)
	if end < 0 {
		t.Fatalf("未找到验证链接: %s", body)
	}
	link := html.UnescapeString(body[start : start+end])
	parsedLink, err := url.Parse(link)
	if err != nil {
		t.Fatal(err)
	}
	if parsedLink.RawQuery != "" {
		t.Fatalf("验证令牌不得进入服务端可见的 HTTP query: %q", parsedLink.RawQuery)
	}
	if parsedLink.Path != "/chronodesk/" {
		t.Fatalf("HashRouter 链接的服务端路径 = %q", parsedLink.Path)
	}
	_, rawFragment, found := strings.Cut(link, "#")
	if !found {
		t.Fatalf("未找到 HashRouter 片段: %q", link)
	}
	fragment, err := url.Parse(rawFragment)
	if err != nil {
		t.Fatal(err)
	}
	if fragment.Path != "/verify-email" {
		t.Fatalf("验证页面片段路径 = %q", fragment.Path)
	}
	if fragment.Query().Get("token") != token {
		t.Fatalf("验证令牌没有安全保真: %q", fragment.Query().Get("token"))
	}
}

func TestApplicationURLKeepsSensitiveTokensInsideHashRouterFragment(t *testing.T) {
	service := &SMTPEmailService{
		webURL: mustParseApplicationWebURLForTest(
			t,
			"https://support.example.test/chronodesk",
		),
	}
	token := "one-time&token=含 空格"

	link := service.applicationURL(
		"/reset-password",
		map[string]string{"token": token},
	)
	parsed, err := url.Parse(link)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Path != "/chronodesk/" || parsed.RawQuery != "" {
		t.Fatalf(
			"服务端可见地址不安全: path=%q query=%q",
			parsed.Path,
			parsed.RawQuery,
		)
	}
	_, rawFragment, found := strings.Cut(link, "#")
	if !found {
		t.Fatalf("未找到 HashRouter 片段: %q", link)
	}
	fragment, err := url.Parse(rawFragment)
	if err != nil {
		t.Fatal(err)
	}
	if fragment.Path != "/reset-password" ||
		fragment.Query().Get("token") != token {
		t.Fatalf("HashRouter 片段没有保真: %q", rawFragment)
	}
}

func mustParseApplicationWebURLForTest(t *testing.T, raw string) *url.URL {
	t.Helper()
	parsed, err := parseApplicationWebURL(raw)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

func TestConfiguredSMTPEmailServiceRejectsInvalidWebURL(t *testing.T) {
	config := models.DefaultEmailConfig()
	provider := fixedSMTPConfigProvider{config: config}
	for _, raw := range []string{
		"",
		"/relative",
		"ftp://example.test",
		"http://example.test",
		"https://user:password@example.test",
		"https://example.test?redirect=unsafe",
	} {
		if _, err := NewConfiguredSMTPEmailService(provider, raw); err == nil {
			t.Fatalf("无效WEB_URL未被拒绝: %q", raw)
		}
	}
	if _, err := NewConfiguredSMTPEmailService(
		provider,
		"http://127.0.0.1:3000",
		"development",
	); err != nil {
		t.Fatalf("本机开发WEB_URL被拒绝: %v", err)
	}
	if _, err := NewConfiguredSMTPEmailService(
		provider,
		"http://localhost:3000",
		"production",
	); err == nil {
		t.Fatal("生产环境本机HTTP WEB_URL未被拒绝")
	}
}

func serveOneAuthEmail(
	t *testing.T,
	listener net.Listener,
	message chan<- []byte,
	done chan<- error,
) {
	t.Helper()
	connection, err := listener.Accept()
	if err != nil {
		done <- err
		return
	}
	defer connection.Close()
	reader := bufio.NewReader(connection)
	if _, err := fmt.Fprint(connection, "220 localhost ChronoDesk test\r\n"); err != nil {
		done <- err
		return
	}
	acceptedMessage := false
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if acceptedMessage && errors.Is(err, io.EOF) {
				done <- nil
				return
			}
			done <- err
			return
		}
		command := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(command, "EHLO "):
			_, err = fmt.Fprint(connection, "250-localhost\r\n250 AUTH PLAIN\r\n")
		case strings.HasPrefix(command, "AUTH PLAIN "):
			_, err = fmt.Fprint(connection, "235 authenticated\r\n")
		case strings.HasPrefix(command, "MAIL FROM:"):
			_, err = fmt.Fprint(connection, "250 sender accepted\r\n")
		case strings.HasPrefix(command, "RCPT TO:"):
			_, err = fmt.Fprint(connection, "250 recipient accepted\r\n")
		case command == "DATA":
			if _, err = fmt.Fprint(connection, "354 end with dot\r\n"); err != nil {
				done <- err
				return
			}
			var raw bytes.Buffer
			for {
				dataLine, readErr := reader.ReadBytes('\n')
				if readErr != nil {
					done <- readErr
					return
				}
				if string(dataLine) == ".\r\n" {
					break
				}
				raw.Write(dataLine)
			}
			message <- raw.Bytes()
			acceptedMessage = true
			_, err = fmt.Fprint(connection, "250 queued\r\n")
		case command == "QUIT":
			_, err = fmt.Fprint(connection, "221 bye\r\n")
			done <- err
			return
		default:
			done <- fmt.Errorf("unexpected SMTP command %q", command)
			return
		}
		if err != nil {
			done <- err
			return
		}
	}
}
