package websocket

import "testing"

func TestOriginAllowed(t *testing.T) {
    allowed := []string{"https://admin.example.com", "https://*.example.org"}

    if !originAllowed("https://admin.example.com", allowed, false) {
        t.Fatalf("expected exact origin to be allowed")
    }
    if !originAllowed("https://foo.example.org", allowed, false) {
        t.Fatalf("expected wildcard origin to be allowed")
    }
    if originAllowed("https://evil.com", allowed, false) {
        t.Fatalf("expected origin to be rejected")
    }

    if !originAllowed("https://evil.com", allowed, true) {
        t.Fatalf("expected allowAll to permit origin")
    }
}
