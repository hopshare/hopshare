package http

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const defaultMailgunAPIBaseURL = "https://api.mailgun.net"

// MailgunPasswordResetEmailSenderConfig configures Mailgun account emails.
type MailgunPasswordResetEmailSenderConfig struct {
	APIBaseURL  string
	Domain      string
	APIKey      string
	FromAddress string
	HTTPClient  *http.Client
}

type mailgunPasswordResetEmailSender struct {
	endpoint    string
	apiKey      string
	fromAddress string
	httpClient  *http.Client
}

// NewMailgunPasswordResetEmailSender returns a Mailgun-backed account email sender.
func NewMailgunPasswordResetEmailSender(cfg MailgunPasswordResetEmailSenderConfig) (PasswordResetEmailSender, error) {
	apiBaseURL := strings.TrimSpace(cfg.APIBaseURL)
	if apiBaseURL == "" {
		apiBaseURL = defaultMailgunAPIBaseURL
	}
	domain := strings.TrimSpace(cfg.Domain)
	apiKey := strings.TrimSpace(cfg.APIKey)
	fromAddress := strings.TrimSpace(cfg.FromAddress)
	if domain == "" {
		return nil, fmt.Errorf("mailgun domain is required")
	}
	if apiKey == "" {
		return nil, fmt.Errorf("mailgun api key is required")
	}
	if fromAddress == "" {
		return nil, fmt.Errorf("mailgun from address is required")
	}

	parsedBaseURL, err := url.Parse(apiBaseURL)
	if err != nil || parsedBaseURL.Scheme == "" || parsedBaseURL.Host == "" {
		return nil, fmt.Errorf("invalid mailgun api base url")
	}

	endpoint := strings.TrimRight(parsedBaseURL.String(), "/") + "/v3/" + url.PathEscape(domain) + "/messages"
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}

	return &mailgunPasswordResetEmailSender{
		endpoint:    endpoint,
		apiKey:      apiKey,
		fromAddress: fromAddress,
		httpClient:  httpClient,
	}, nil
}

func (s *mailgunPasswordResetEmailSender) SendPasswordReset(ctx context.Context, toEmail, resetURL string) error {
	return s.sendText(ctx, toEmail, "Reset your HopShare password", "We received a request to reset your HopShare password.\n\nUse this link to set a new password:\n"+strings.TrimSpace(resetURL)+"\n\nIf you did not request this, you can ignore this email.")
}

func (s *mailgunPasswordResetEmailSender) SendEmailVerification(ctx context.Context, toEmail, verifyURL string) error {
	return s.sendText(ctx, toEmail, "Verify your HopShare email", "Welcome to HopShare.\n\nUse this link to verify your email address:\n"+strings.TrimSpace(verifyURL)+"\n\nIf you did not create this account, you can ignore this email.")
}

func (s *mailgunPasswordResetEmailSender) sendText(ctx context.Context, toEmail, subject, bodyText string) error {
	toEmail = strings.TrimSpace(toEmail)
	subject = strings.TrimSpace(subject)
	bodyText = strings.TrimSpace(bodyText)
	if toEmail == "" {
		return fmt.Errorf("email recipient is required")
	}
	if subject == "" {
		return fmt.Errorf("email subject is required")
	}
	if bodyText == "" {
		return fmt.Errorf("email body is required")
	}

	body := url.Values{}
	body.Set("from", s.fromAddress)
	body.Set("to", toEmail)
	body.Set("subject", subject)
	body.Set("text", bodyText)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.endpoint, strings.NewReader(body.Encode()))
	if err != nil {
		return fmt.Errorf("build mailgun request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth("api", s.apiKey)

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("send mailgun request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("mailgun request failed with status %d", resp.StatusCode)
	}

	return nil
}
