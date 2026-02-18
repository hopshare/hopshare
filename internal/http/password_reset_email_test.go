package http

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestNewMailgunPasswordResetEmailSenderValidatesRequiredFields(t *testing.T) {
	_, err := NewMailgunPasswordResetEmailSender(MailgunPasswordResetEmailSenderConfig{})
	if err == nil {
		t.Fatalf("expected error when required mailgun config is missing")
	}
}

func TestMailgunPasswordResetEmailSenderSendsRequest(t *testing.T) {
	type capturedRequest struct {
		method string
		path   string
		user   string
		pass   string
		form   url.Values
	}

	captured := capturedRequest{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured.method = r.Method
		captured.path = r.URL.Path
		captured.user, captured.pass, _ = r.BasicAuth()
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read request body: %v", err)
		}
		if err := r.Body.Close(); err != nil {
			t.Fatalf("close request body: %v", err)
		}
		captured.form, err = url.ParseQuery(string(body))
		if err != nil {
			t.Fatalf("parse request body: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	sender, err := NewMailgunPasswordResetEmailSender(MailgunPasswordResetEmailSenderConfig{
		APIBaseURL:  srv.URL,
		Domain:      "mg.example.com",
		APIKey:      "key-test",
		FromAddress: "HopShare <no-reply@example.com>",
		HTTPClient:  srv.Client(),
	})
	if err != nil {
		t.Fatalf("build sender: %v", err)
	}

	resetURL := "https://hopshare.test/reset-password?token=abc123"
	if err := sender.SendPasswordReset(context.Background(), "person@example.com", resetURL); err != nil {
		t.Fatalf("send password reset email: %v", err)
	}

	if captured.method != http.MethodPost {
		t.Fatalf("method: got %q want %q", captured.method, http.MethodPost)
	}
	if captured.path != "/v3/mg.example.com/messages" {
		t.Fatalf("path: got %q want %q", captured.path, "/v3/mg.example.com/messages")
	}
	if captured.user != "api" || captured.pass != "key-test" {
		t.Fatalf("basic auth: got user=%q pass=%q", captured.user, captured.pass)
	}
	if captured.form.Get("to") != "person@example.com" {
		t.Fatalf("to: got %q want %q", captured.form.Get("to"), "person@example.com")
	}
	if captured.form.Get("from") != "HopShare <no-reply@example.com>" {
		t.Fatalf("from: got %q want %q", captured.form.Get("from"), "HopShare <no-reply@example.com>")
	}
	if !strings.Contains(captured.form.Get("text"), resetURL) {
		t.Fatalf("mail body did not contain reset url")
	}
}

func TestMailgunPasswordResetEmailSenderReturnsErrorForNon2xxResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	sender, err := NewMailgunPasswordResetEmailSender(MailgunPasswordResetEmailSenderConfig{
		APIBaseURL:  srv.URL,
		Domain:      "mg.example.com",
		APIKey:      "key-test",
		FromAddress: "HopShare <no-reply@example.com>",
		HTTPClient:  srv.Client(),
	})
	if err != nil {
		t.Fatalf("build sender: %v", err)
	}

	err = sender.SendPasswordReset(context.Background(), "person@example.com", "https://hopshare.test/reset-password?token=abc123")
	if err == nil {
		t.Fatalf("expected send to fail on non-2xx status")
	}
}
