package mailer

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSMTPTransportRejectsUnsafeModes(t *testing.T) {
	tests := []struct {
		name   string
		config SMTPTransportConfig
	}{
		{
			name: "STARTTLS and implicit TLS are mutually exclusive",
			config: SMTPTransportConfig{
				Host: "smtp.example.com", Port: 465,
				UseSTARTTLS: true, UseImplicitTLS: true,
			},
		},
		{
			name: "remote plaintext is rejected",
			config: SMTPTransportConfig{
				Host: "smtp.example.com", Port: 25,
			},
		},
		{
			name: "partial credentials are rejected",
			config: SMTPTransportConfig{
				Host: "localhost", Port: 2525, Username: "user",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewSMTPTransport(test.config); err == nil {
				t.Fatal("不安全的SMTP配置未被拒绝")
			}
		})
	}
}

func TestSMTPTransportHonorsContextWhileWaitingForGreeting(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		connection, acceptErr := listener.Accept()
		if acceptErr == nil {
			accepted <- connection
		}
	}()

	transport := newTestSMTPTransport(t, listener, false, false, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 75*time.Millisecond)
	defer cancel()
	started := time.Now()
	err = transport.TestConnection(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("连接错误 = %v，期望 context deadline exceeded", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("取消SMTP连接耗时过长: %s", elapsed)
	}
	select {
	case connection := <-accepted:
		_ = connection.Close()
	case <-time.After(time.Second):
		t.Fatal("伪SMTP服务器没有接收到连接")
	}
}

func TestSMTPTransportSendsOverSTARTTLSAndImplicitTLS(t *testing.T) {
	certificate, roots := newTestCertificate(t)
	for _, test := range []struct {
		name        string
		startTLS    bool
		implicitTLS bool
	}{
		{name: "STARTTLS", startTLS: true},
		{name: "implicit TLS", implicitTLS: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			server := &smtpTestServer{
				listener:    listener,
				certificate: certificate,
				startTLS:    test.startTLS,
				implicitTLS: test.implicitTLS,
				done:        make(chan error, 1),
			}
			go server.serve()

			transport := newTestSMTPTransport(
				t,
				listener,
				test.startTLS,
				test.implicitTLS,
				roots,
			)
			message := []byte("Subject: test\r\n\r\nChronoDesk transport test\r\n")
			if err := transport.Send(
				context.Background(),
				"sender@example.test",
				[]string{"recipient@example.test"},
				message,
			); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-server.done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("伪SMTP服务器未结束")
			}
			if !strings.Contains(server.message(), "ChronoDesk transport test") {
				t.Fatalf("服务器未收到邮件正文: %q", server.message())
			}
		})
	}
}

func TestSMTPTransportDoesNotDowngradeMissingSTARTTLS(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		connection, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		defer connection.Close()
		reader := bufio.NewReader(connection)
		_, _ = fmt.Fprint(connection, "220 localhost test\r\n")
		_, _ = reader.ReadString('\n')
		_, _ = fmt.Fprint(connection, "250-localhost\r\n250 AUTH PLAIN\r\n")
	}()

	transport := newTestSMTPTransport(t, listener, true, false, nil)
	err = transport.TestConnection(context.Background())
	if err == nil || !strings.Contains(err.Error(), "不支持已配置的STARTTLS") {
		t.Fatalf("STARTTLS降级未被拒绝: %v", err)
	}
	<-done
}

func TestSMTPTransportTLSMinimumVersion(t *testing.T) {
	transport, err := NewSMTPTransport(SMTPTransportConfig{
		Host: "smtp.example.com", Port: 465, UseImplicitTLS: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if transport.tlsConfig().MinVersion != tls.VersionTLS12 {
		t.Fatalf("TLS最低版本 = %d，期望 TLS 1.2", transport.tlsConfig().MinVersion)
	}
}

func newTestSMTPTransport(
	t *testing.T,
	listener net.Listener,
	startTLS bool,
	implicitTLS bool,
	roots *x509.CertPool,
) *SMTPTransport {
	t.Helper()
	port := listener.Addr().(*net.TCPAddr).Port
	transport, err := NewSMTPTransport(SMTPTransportConfig{
		Host:           "127.0.0.1",
		Port:           port,
		Username:       "chronodesk",
		Password:       "test-password",
		UseSTARTTLS:    startTLS,
		UseImplicitTLS: implicitTLS,
		ConnectTimeout: time.Second,
		CommandTimeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	transport.rootCAs = roots
	return transport
}

type smtpTestServer struct {
	listener    net.Listener
	certificate tls.Certificate
	startTLS    bool
	implicitTLS bool
	done        chan error

	mu   sync.Mutex
	body string
}

func (s *smtpTestServer) message() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.body
}

func (s *smtpTestServer) serve() {
	connection, err := s.listener.Accept()
	if err != nil {
		s.done <- err
		return
	}
	defer connection.Close()
	if s.implicitTLS {
		connection = tls.Server(connection, &tls.Config{
			Certificates: []tls.Certificate{s.certificate},
			MinVersion:   tls.VersionTLS12,
		})
		if err := connection.(*tls.Conn).Handshake(); err != nil {
			s.done <- err
			return
		}
	}
	s.done <- s.serveSession(connection)
}

func (s *smtpTestServer) serveSession(connection net.Conn) error {
	reader := bufio.NewReader(connection)
	if _, err := fmt.Fprint(connection, "220 localhost ChronoDesk test\r\n"); err != nil {
		return err
	}
	encrypted := s.implicitTLS
	acceptedMessage := false
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if acceptedMessage && errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		command := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(command, "EHLO "):
			if s.startTLS && !encrypted {
				_, err = fmt.Fprint(
					connection,
					"250-localhost\r\n250-STARTTLS\r\n250 AUTH PLAIN\r\n",
				)
			} else {
				_, err = fmt.Fprint(
					connection,
					"250-localhost\r\n250 AUTH PLAIN\r\n",
				)
			}
		case command == "STARTTLS" && s.startTLS && !encrypted:
			if _, err = fmt.Fprint(connection, "220 ready for TLS\r\n"); err != nil {
				return err
			}
			tlsConnection := tls.Server(connection, &tls.Config{
				Certificates: []tls.Certificate{s.certificate},
				MinVersion:   tls.VersionTLS12,
			})
			if err = tlsConnection.Handshake(); err != nil {
				return err
			}
			connection = tlsConnection
			reader = bufio.NewReader(connection)
			encrypted = true
			continue
		case strings.HasPrefix(command, "AUTH PLAIN "):
			_, err = fmt.Fprint(connection, "235 authenticated\r\n")
		case strings.HasPrefix(command, "MAIL FROM:"):
			_, err = fmt.Fprint(connection, "250 sender accepted\r\n")
		case strings.HasPrefix(command, "RCPT TO:"):
			_, err = fmt.Fprint(connection, "250 recipient accepted\r\n")
		case command == "DATA":
			if _, err = fmt.Fprint(connection, "354 end with dot\r\n"); err != nil {
				return err
			}
			var body strings.Builder
			for {
				dataLine, readErr := reader.ReadString('\n')
				if readErr != nil {
					return readErr
				}
				if dataLine == ".\r\n" {
					break
				}
				body.WriteString(dataLine)
			}
			s.mu.Lock()
			s.body = body.String()
			s.mu.Unlock()
			acceptedMessage = true
			_, err = fmt.Fprint(connection, "250 queued\r\n")
		case command == "NOOP":
			_, err = fmt.Fprint(connection, "250 ok\r\n")
		case command == "QUIT":
			_, err = fmt.Fprint(connection, "221 bye\r\n")
			return err
		default:
			return fmt.Errorf("unexpected SMTP command %q", command)
		}
		if err != nil {
			return err
		}
	}
}

func newTestCertificate(t *testing.T) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "127.0.0.1"},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(time.Hour),
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		KeyUsage:     x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(
		rand.Reader,
		template,
		template,
		&privateKey.PublicKey,
		privateKey,
	)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := tls.X509KeyPair(
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}),
	)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	certificateRecord, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	roots.AddCert(certificateRecord)
	return certificate, roots
}
