package config

import (
	"reflect"
	"testing"
	"time"
)

func TestLoadParsesAdminEmails(t *testing.T) {
	t.Setenv("HOPSHARE_ADDR", ":9090")
	t.Setenv("HOPSHARE_DB_URL", "postgres://example")
	t.Setenv("HOPSHARE_ENV", "test")
	t.Setenv("HOPSHARE_ADMIN_EMAILS", " Alice@example.com ,bob@example.com,ALICE@example.com,, carol@example.com ")
	t.Setenv("HOPSHARE_TIMEZONE", "America/New_York")
	t.Setenv("FEATURE_EMAIL", "false")
	t.Setenv("FEATURE_HOP_PICTURES", "true")
	t.Setenv("HOPSHARE_AVATAR_IMAGE_SIZE", "3145728")
	t.Setenv("HOPSHARE_PUBLIC_BASE_URL", "https://hopshare.example.com")
	t.Setenv("HOPSHARE_MAILGUN_API_BASE_URL", "https://api.mailgun.net")
	t.Setenv("HOPSHARE_MAILGUN_DOMAIN", "mg.example.com")
	t.Setenv("HOPSHARE_MAILGUN_API_KEY", "key-123")
	t.Setenv("HOPSHARE_MAILGUN_FROM_ADDRESS", "HopShare <no-reply@example.com>")
	t.Setenv("HOPSHARE_COOKIE_SECURE", "false")
	t.Setenv("HOPSHARE_SESSION_ABSOLUTE_TTL", "48h")
	t.Setenv("HOPSHARE_SESSION_IDLE_TIMEOUT", "90m")
	t.Setenv("HOPSHARE_WORKERS_ENABLED", "false")
	t.Setenv("HOPSHARE_WORKER_POLL_INTERVAL", "2m")
	t.Setenv("HOPSHARE_WORKER_EXPIRE_HOPS_INTERVAL", "3h")
	t.Setenv("HOPSHARE_WORKER_SESSION_GC_INTERVAL", "12h")

	cfg := Load()
	if cfg.Addr != ":9090" {
		t.Fatalf("addr: got %q want %q", cfg.Addr, ":9090")
	}
	if cfg.DatabaseURL != "postgres://example" {
		t.Fatalf("db: got %q want %q", cfg.DatabaseURL, "postgres://example")
	}
	if cfg.Env != "test" {
		t.Fatalf("env: got %q want %q", cfg.Env, "test")
	}
	if cfg.Timezone != "America/New_York" {
		t.Fatalf("timezone: got %q want %q", cfg.Timezone, "America/New_York")
	}
	if cfg.FeatureEmail {
		t.Fatalf("feature email: got %v want false", cfg.FeatureEmail)
	}
	if !cfg.FeatureHopPictures {
		t.Fatalf("feature hop pictures: got %v want true", cfg.FeatureHopPictures)
	}
	if cfg.AvatarImageSize != 3145728 {
		t.Fatalf("avatar image size: got %d want %d", cfg.AvatarImageSize, int64(3145728))
	}
	if cfg.PublicBaseURL != "https://hopshare.example.com" {
		t.Fatalf("public base url: got %q want %q", cfg.PublicBaseURL, "https://hopshare.example.com")
	}
	if cfg.MailgunAPIBaseURL != "https://api.mailgun.net" {
		t.Fatalf("mailgun api base url: got %q want %q", cfg.MailgunAPIBaseURL, "https://api.mailgun.net")
	}
	if cfg.MailgunDomain != "mg.example.com" {
		t.Fatalf("mailgun domain: got %q want %q", cfg.MailgunDomain, "mg.example.com")
	}
	if cfg.MailgunAPIKey != "key-123" {
		t.Fatalf("mailgun api key: got %q want %q", cfg.MailgunAPIKey, "key-123")
	}
	if cfg.MailgunFromAddress != "HopShare <no-reply@example.com>" {
		t.Fatalf("mailgun from address: got %q want %q", cfg.MailgunFromAddress, "HopShare <no-reply@example.com>")
	}
	if cfg.CookieSecure {
		t.Fatalf("cookie secure: got %v want false", cfg.CookieSecure)
	}
	if cfg.SessionAbsoluteTTL != 48*time.Hour {
		t.Fatalf("session absolute ttl: got %s want %s", cfg.SessionAbsoluteTTL, 48*time.Hour)
	}
	if cfg.SessionIdleTimeout != 90*time.Minute {
		t.Fatalf("session idle timeout: got %s want %s", cfg.SessionIdleTimeout, 90*time.Minute)
	}
	if cfg.WorkersEnabled {
		t.Fatalf("workers enabled: got %v want false", cfg.WorkersEnabled)
	}
	if cfg.WorkerPollInterval != 2*time.Minute {
		t.Fatalf("worker poll interval: got %s want %s", cfg.WorkerPollInterval, 2*time.Minute)
	}
	if cfg.ExpireHopsInterval != 3*time.Hour {
		t.Fatalf("expire hops interval: got %s want %s", cfg.ExpireHopsInterval, 3*time.Hour)
	}
	if cfg.SessionGCInterval != 12*time.Hour {
		t.Fatalf("session gc interval: got %s want %s", cfg.SessionGCInterval, 12*time.Hour)
	}

	wantAdmins := []string{"alice@example.com", "bob@example.com", "carol@example.com"}
	if !reflect.DeepEqual(cfg.AdminEmails, wantAdmins) {
		t.Fatalf("admin emails: got %v want %v", cfg.AdminEmails, wantAdmins)
	}
}

func TestLoadWithoutAdmins(t *testing.T) {
	t.Setenv("HOPSHARE_ADMIN_EMAILS", "")
	cfg := Load()
	if len(cfg.AdminEmails) != 0 {
		t.Fatalf("admin emails: got %v want empty", cfg.AdminEmails)
	}
}

func TestLoadTimezoneDefaultUTC(t *testing.T) {
	t.Setenv("HOPSHARE_TIMEZONE", "")
	cfg := Load()
	if cfg.Timezone != "UTC" {
		t.Fatalf("timezone default: got %q want %q", cfg.Timezone, "UTC")
	}
}

func TestLoadDefaultsForPasswordResetEmailConfig(t *testing.T) {
	t.Setenv("FEATURE_EMAIL", "")
	t.Setenv("FEATURE_HOP_PICTURES", "")
	t.Setenv("HOPSHARE_AVATAR_IMAGE_SIZE", "")
	t.Setenv("HOPSHARE_PUBLIC_BASE_URL", "")
	t.Setenv("HOPSHARE_MAILGUN_API_BASE_URL", "")
	t.Setenv("HOPSHARE_MAILGUN_DOMAIN", "")
	t.Setenv("HOPSHARE_MAILGUN_API_KEY", "")
	t.Setenv("HOPSHARE_MAILGUN_FROM_ADDRESS", "")
	t.Setenv("HOPSHARE_COOKIE_SECURE", "")
	t.Setenv("HOPSHARE_SESSION_ABSOLUTE_TTL", "")
	t.Setenv("HOPSHARE_SESSION_IDLE_TIMEOUT", "")
	t.Setenv("HOPSHARE_WORKERS_ENABLED", "")
	t.Setenv("HOPSHARE_WORKER_POLL_INTERVAL", "")
	t.Setenv("HOPSHARE_WORKER_EXPIRE_HOPS_INTERVAL", "")
	t.Setenv("HOPSHARE_WORKER_SESSION_GC_INTERVAL", "")

	cfg := Load()
	if !cfg.FeatureEmail {
		t.Fatalf("feature email default: got %v want true", cfg.FeatureEmail)
	}
	if cfg.FeatureHopPictures {
		t.Fatalf("feature hop pictures default: got %v want false", cfg.FeatureHopPictures)
	}
	if cfg.AvatarImageSize != 2<<20 {
		t.Fatalf("avatar image size default: got %d want %d", cfg.AvatarImageSize, int64(2<<20))
	}
	if cfg.PublicBaseURL != "http://localhost:8080" {
		t.Fatalf("public base url default: got %q want %q", cfg.PublicBaseURL, "http://localhost:8080")
	}
	if cfg.MailgunAPIBaseURL != "https://api.mailgun.net" {
		t.Fatalf("mailgun api base url default: got %q want %q", cfg.MailgunAPIBaseURL, "https://api.mailgun.net")
	}
	if cfg.MailgunDomain != "hopshare.org" {
		t.Fatalf("mailgun domain default: got %q want %q", cfg.MailgunDomain, "hopshare.org")
	}
	if cfg.MailgunAPIKey != "" {
		t.Fatalf("mailgun api key default: got %q want empty", cfg.MailgunAPIKey)
	}
	if cfg.MailgunFromAddress != "support@hopshare.org" {
		t.Fatalf("mailgun from address default: got %q want %q", cfg.MailgunFromAddress, "support@hopshare.org")
	}
	if !cfg.CookieSecure {
		t.Fatalf("cookie secure default: got %v want true", cfg.CookieSecure)
	}
	if cfg.SessionAbsoluteTTL != 168*time.Hour {
		t.Fatalf("session absolute ttl default: got %s want %s", cfg.SessionAbsoluteTTL, 168*time.Hour)
	}
	if cfg.SessionIdleTimeout != 24*time.Hour {
		t.Fatalf("session idle timeout default: got %s want %s", cfg.SessionIdleTimeout, 24*time.Hour)
	}
	if !cfg.WorkersEnabled {
		t.Fatalf("workers enabled default: got %v want true", cfg.WorkersEnabled)
	}
	if cfg.WorkerPollInterval != time.Minute {
		t.Fatalf("worker poll interval default: got %s want %s", cfg.WorkerPollInterval, time.Minute)
	}
	if cfg.ExpireHopsInterval != time.Hour {
		t.Fatalf("expire hops interval default: got %s want %s", cfg.ExpireHopsInterval, time.Hour)
	}
	if cfg.SessionGCInterval != 6*time.Hour {
		t.Fatalf("session gc interval default: got %s want %s", cfg.SessionGCInterval, 6*time.Hour)
	}
}
