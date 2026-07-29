package mailer

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/smtp"
	"strconv"
	"strings"
	"time"
)

const (
	defaultSMTPConnectTimeout = 5 * time.Second
	defaultSMTPCommandTimeout = 20 * time.Second
)

// SMTPTransportConfig describes one SMTP endpoint. STARTTLS and implicit TLS
// are intentionally separate and mutually exclusive: ChronoDesk never guesses
// a transport mode or downgrades a failed encrypted connection to plaintext.
type SMTPTransportConfig struct {
	Host           string
	Port           int
	Username       string
	Password       string
	UseSTARTTLS    bool
	UseImplicitTLS bool
	ConnectTimeout time.Duration
	CommandTimeout time.Duration
}

// SMTPTransport performs one context-aware SMTP session. It is safe for
// concurrent use because all mutable protocol state is scoped to a call.
type SMTPTransport struct {
	config  SMTPTransportConfig
	rootCAs *x509.CertPool
}

func NewSMTPTransport(config SMTPTransportConfig) (*SMTPTransport, error) {
	config.Host = strings.TrimSpace(config.Host)
	config.Username = strings.TrimSpace(config.Username)
	if err := validateSMTPTransportConfig(config); err != nil {
		return nil, err
	}
	if config.ConnectTimeout <= 0 {
		config.ConnectTimeout = defaultSMTPConnectTimeout
	}
	if config.CommandTimeout <= 0 {
		config.CommandTimeout = defaultSMTPCommandTimeout
	}
	return &SMTPTransport{config: config}, nil
}

// TestConnection verifies greeting, the configured encryption mode and
// authentication without submitting a message.
func (t *SMTPTransport) TestConnection(ctx context.Context) error {
	client, cleanup, err := t.open(ctx)
	if err != nil {
		return err
	}
	defer cleanup()

	if err := client.Noop(); err != nil {
		return t.contextualError(ctx, "SMTP连接检查失败", err)
	}
	// A successful authenticated NOOP is authoritative. cleanup closes the
	// socket directly so a server that ignores QUIT cannot extend this call.
	return nil
}

// Send submits one already-built RFC 5322 message. Success means the SMTP
// server accepted the terminating DATA response. SMTP cannot provide
// exactly-once delivery; callers must treat a timeout or connection loss near
// DATA acknowledgement as an unknown outcome.
func (t *SMTPTransport) Send(
	ctx context.Context,
	from string,
	recipients []string,
	message []byte,
) error {
	if len(recipients) == 0 {
		return errors.New("SMTP收件人不能为空")
	}
	if len(message) == 0 {
		return errors.New("SMTP邮件内容不能为空")
	}
	canonicalFrom, err := CanonicalMailbox(from)
	if err != nil {
		return fmt.Errorf("SMTP发件邮箱无效: %w", err)
	}
	canonicalRecipients := make([]string, 0, len(recipients))
	for _, recipient := range recipients {
		canonicalRecipient, canonicalErr := CanonicalMailbox(recipient)
		if canonicalErr != nil {
			return fmt.Errorf("SMTP收件邮箱无效: %w", canonicalErr)
		}
		canonicalRecipients = append(canonicalRecipients, canonicalRecipient)
	}

	client, cleanup, err := t.open(ctx)
	if err != nil {
		return err
	}
	defer cleanup()

	if err := client.Mail(canonicalFrom); err != nil {
		return t.contextualError(ctx, "SMTP发件人被拒绝", err)
	}
	for _, recipient := range canonicalRecipients {
		if err := client.Rcpt(recipient); err != nil {
			return t.contextualError(ctx, "SMTP收件人被拒绝", err)
		}
	}
	writer, err := client.Data()
	if err != nil {
		return t.contextualError(ctx, "SMTP服务器拒绝邮件正文", err)
	}
	if _, err := writer.Write(message); err != nil {
		_ = writer.Close()
		return t.contextualError(ctx, "写入SMTP邮件正文失败", err)
	}
	// Close reads the server's final DATA acknowledgement. Once it succeeds the
	// message has been accepted, so a later QUIT failure must not trigger a
	// duplicate retry.
	if err := writer.Close(); err != nil {
		return t.contextualError(ctx, "SMTP服务器未确认邮件接收", err)
	}
	// Do not wait for QUIT after the authoritative DATA acknowledgement. Direct
	// cleanup prevents a non-compliant server from delaying a successful send.
	return nil
}

func (t *SMTPTransport) open(
	ctx context.Context,
) (*smtp.Client, func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, func() {}, err
	}
	address := net.JoinHostPort(t.config.Host, strconv.Itoa(t.config.Port))
	dialer := &net.Dialer{Timeout: t.config.ConnectTimeout}

	var (
		connection net.Conn
		err        error
	)
	if t.config.UseImplicitTLS {
		tlsDialer := &tls.Dialer{
			NetDialer: dialer,
			Config:    t.tlsConfig(),
		}
		connection, err = tlsDialer.DialContext(ctx, "tcp", address)
	} else {
		connection, err = dialer.DialContext(ctx, "tcp", address)
	}
	if err != nil {
		return nil, func() {}, t.contextualError(ctx, "无法连接SMTP服务器", err)
	}

	stopContextWatch := watchSMTPContext(ctx, connection)
	cleanupConnection := func() {
		stopContextWatch()
		_ = connection.Close()
	}
	if err := setSMTPDeadline(ctx, connection, t.config.CommandTimeout); err != nil {
		cleanupConnection()
		return nil, func() {}, err
	}

	client, err := smtp.NewClient(connection, t.config.Host)
	if err != nil {
		cleanupConnection()
		return nil, func() {}, t.contextualError(ctx, "读取SMTP服务问候失败", err)
	}
	cleanup := func() {
		_ = client.Close()
		cleanupConnection()
	}

	if t.config.UseSTARTTLS {
		if supported, _ := client.Extension("STARTTLS"); !supported {
			cleanup()
			return nil, func() {}, errors.New("SMTP服务器不支持已配置的STARTTLS")
		}
		if err := client.StartTLS(t.tlsConfig()); err != nil {
			cleanup()
			return nil, func() {}, t.contextualError(ctx, "SMTP STARTTLS握手失败", err)
		}
	}

	if t.config.Username != "" {
		auth := smtp.PlainAuth(
			"",
			t.config.Username,
			t.config.Password,
			t.config.Host,
		)
		if err := client.Auth(auth); err != nil {
			cleanup()
			return nil, func() {}, t.contextualError(ctx, "SMTP认证失败", err)
		}
	}
	return client, cleanup, nil
}

func (t *SMTPTransport) tlsConfig() *tls.Config {
	return &tls.Config{
		MinVersion: tls.VersionTLS12,
		ServerName: t.config.Host,
		RootCAs:    t.rootCAs,
	}
}

func (t *SMTPTransport) contextualError(
	ctx context.Context,
	action string,
	err error,
) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return fmt.Errorf("%s: %w", action, ctxErr)
	}
	if errors.Is(err, io.EOF) {
		return fmt.Errorf("%s: SMTP服务器提前关闭连接", action)
	}
	return fmt.Errorf("%s: %w", action, err)
}

func validateSMTPTransportConfig(config SMTPTransportConfig) error {
	if config.Host == "" {
		return errors.New("SMTP主机不能为空")
	}
	if strings.ContainsAny(config.Host, "\x00\r\n\t /") {
		return errors.New("SMTP主机格式无效")
	}
	if config.Port < 1 || config.Port > 65535 {
		return errors.New("SMTP端口必须在1到65535之间")
	}
	if config.UseSTARTTLS && config.UseImplicitTLS {
		return errors.New("SMTP STARTTLS与隐式TLS不能同时启用")
	}
	if !config.UseSTARTTLS &&
		!config.UseImplicitTLS &&
		!isExplicitLoopbackHost(config.Host) {
		return errors.New("远程SMTP服务器必须启用STARTTLS或隐式TLS")
	}
	if (config.Username == "") != (config.Password == "") {
		return errors.New("SMTP用户名和密码必须同时配置")
	}
	return nil
}

func isExplicitLoopbackHost(host string) bool {
	host = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func setSMTPDeadline(
	ctx context.Context,
	connection net.Conn,
	timeout time.Duration,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	// The dedicated context watcher sets an immediate socket deadline only
	// after ctx.Done() is closed. Using the context deadline here as well
	// creates a race where the socket timeout can surface a few microseconds
	// before context.Err(), incorrectly turning cancellation into an ordinary
	// I/O timeout.
	deadline := time.Now().Add(timeout)
	if err := connection.SetDeadline(deadline); err != nil {
		return fmt.Errorf("设置SMTP连接读写期限失败: %w", err)
	}
	return nil
}

func watchSMTPContext(ctx context.Context, connection net.Conn) func() {
	stopped := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		select {
		case <-ctx.Done():
			// SetDeadline unblocks reads and writes without relying on a server
			// to close the socket. The regular cleanup path owns Close.
			_ = connection.SetDeadline(time.Now())
		case <-stopped:
		}
	}()
	return func() {
		close(stopped)
		<-done
	}
}
