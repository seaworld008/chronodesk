package security

import (
	"context"
	"errors"
	"net"
	"net/url"
	"testing"
	"time"
)

func TestCallbackURLRejectsSSRFPrimitives(t *testing.T) {
	tests := []string{
		"http://example.com/callback",
		"https://user:password@example.com/callback",
		"https://localhost/callback",
		"https://service.localhost/callback",
		"https://127.0.0.1/callback",
		"https://10.0.0.1/callback",
		"https://169.254.169.254/latest/meta-data",
		"https://[::1]/callback",
		"https://192.0.2.10/callback",
	}
	for _, rawURL := range tests {
		t.Run(rawURL, func(t *testing.T) {
			if err := ValidateHTTPSCallbackURLString(rawURL); !errors.Is(err, ErrUnsafeCallback) {
				t.Fatalf("validation error=%v", err)
			}
		})
	}
	if err := ValidateHTTPSCallbackURLString("https://8.8.8.8/callback"); err != nil {
		t.Fatalf("public HTTPS literal rejected: %v", err)
	}
}

func TestPinnedCallbackClientRejectsPrivateDNSAnswer(t *testing.T) {
	target, err := url.Parse("https://callback.example.test/events")
	if err != nil {
		t.Fatal(err)
	}
	resolver := &net.Resolver{
		PreferGo: true,
		Dial: func(context.Context, string, string) (net.Conn, error) {
			return nil, errors.New("test DNS unavailable")
		},
	}
	if _, err := NewPinnedHTTPSClient(
		context.Background(),
		target,
		resolver,
		time.Second,
	); !errors.Is(err, ErrUnsafeCallback) {
		t.Fatalf("unresolvable callback error=%v", err)
	}
}

func TestPublicCallbackIPClassification(t *testing.T) {
	for _, raw := range []string{
		"127.0.0.1",
		"10.1.2.3",
		"100.64.0.1",
		"169.254.169.254",
		"192.0.2.1",
		"198.18.0.1",
		"203.0.113.1",
		"::1",
		"2001:db8::1",
	} {
		if IsPublicCallbackIP(net.ParseIP(raw)) {
			t.Fatalf("%s classified as public", raw)
		}
	}
	for _, raw := range []string{"8.8.8.8", "1.1.1.1", "2606:4700:4700::1111"} {
		if !IsPublicCallbackIP(net.ParseIP(raw)) {
			t.Fatalf("%s classified as private", raw)
		}
	}
}
