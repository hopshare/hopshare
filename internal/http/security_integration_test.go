package http_test

import (
	"net/http"
	"strings"
	"testing"
)

func TestSecurityHeadersOnHTMLResponse(t *testing.T) {
	server := newHTTPServer(t, nil)
	anon := newTestActor(t, "anon", server.URL, "", "")

	resp := anon.Get("/login")
	body := requireStatus(t, resp, http.StatusOK)
	requireBodyContains(t, body, "Log in")

	headers := resp.Header
	if got := headers.Get("X-Frame-Options"); got != "DENY" {
		t.Fatalf("expected X-Frame-Options=DENY, got %q", got)
	}
	if got := headers.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("expected X-Content-Type-Options=nosniff, got %q", got)
	}
	if got := headers.Get("Referrer-Policy"); got != "strict-origin-when-cross-origin" {
		t.Fatalf("expected Referrer-Policy strict-origin-when-cross-origin, got %q", got)
	}
	if got := headers.Get("Permissions-Policy"); got == "" {
		t.Fatalf("expected Permissions-Policy header")
	}
	csp := headers.Get("Content-Security-Policy")
	if csp == "" {
		t.Fatalf("expected Content-Security-Policy header")
	}
	if !strings.Contains(csp, "default-src 'self'") {
		t.Fatalf("expected baseline CSP directive in %q", csp)
	}
}
