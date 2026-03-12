package config

import (
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds server configuration loaded from environment variables.
type Config struct {
	Addr               string
	DatabaseURL        string
	Env                string
	AdminEmails        []string
	Timezone           string
	FeatureEmail       bool
	FeatureHopPictures bool
	AvatarImageSize    int64
	PublicBaseURL      string
	MailgunAPIBaseURL  string
	MailgunDomain      string
	MailgunAPIKey      string
	MailgunFromAddress string
	CookieSecure       bool
	SessionAbsoluteTTL time.Duration
	SessionIdleTimeout time.Duration
	WorkersEnabled     bool
	WorkerPollInterval time.Duration
	ExpireHopsInterval time.Duration
	SessionGCInterval  time.Duration
}

// Load returns configuration populated from HOPSHARE_* environment variables.
func Load() Config {
	return Config{
		Addr:               getenv("HOPSHARE_ADDR", ":8080"),
		DatabaseURL:        getenv("HOPSHARE_DB_URL", ""),
		Env:                getenv("HOPSHARE_ENV", "development"),
		AdminEmails:        parseAdminEmails(getenv("HOPSHARE_ADMIN_EMAILS", "")),
		Timezone:           loadTimezone(),
		FeatureEmail:       getenvBool("FEATURE_EMAIL", true),
		FeatureHopPictures: getenvBool("FEATURE_HOP_PICTURES", false),
		AvatarImageSize:    getenvInt64("HOPSHARE_AVATAR_IMAGE_SIZE", 2<<20),
		PublicBaseURL:      getenv("HOPSHARE_PUBLIC_BASE_URL", "http://localhost:8080"),
		MailgunAPIBaseURL:  getenv("HOPSHARE_MAILGUN_API_BASE_URL", "https://api.mailgun.net"),
		MailgunDomain:      getenv("HOPSHARE_MAILGUN_DOMAIN", "hopshare.org"),
		MailgunAPIKey:      getenv("HOPSHARE_MAILGUN_API_KEY", ""),
		MailgunFromAddress: getenv("HOPSHARE_MAILGUN_FROM_ADDRESS", "support@hopshare.org"),
		CookieSecure:       getenvBool("HOPSHARE_COOKIE_SECURE", true),
		SessionAbsoluteTTL: getenvDuration("HOPSHARE_SESSION_ABSOLUTE_TTL", 168*time.Hour),
		SessionIdleTimeout: getenvDuration("HOPSHARE_SESSION_IDLE_TIMEOUT", 24*time.Hour),
		WorkersEnabled:     getenvBool("HOPSHARE_WORKERS_ENABLED", true),
		WorkerPollInterval: getenvDuration("HOPSHARE_WORKER_POLL_INTERVAL", time.Minute),
		ExpireHopsInterval: getenvDuration("HOPSHARE_WORKER_EXPIRE_HOPS_INTERVAL", time.Hour),
		SessionGCInterval:  getenvDuration("HOPSHARE_WORKER_SESSION_GC_INTERVAL", 6*time.Hour),
	}
}

func getenv(key, fallback string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return fallback
}

func getenvBool(key string, fallback bool) bool {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(raw)
	if err != nil {
		return fallback
	}
	return parsed
}

func getenvDuration(key string, fallback time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(raw)
	if err != nil {
		return fallback
	}
	if parsed < 0 {
		return 0
	}
	return parsed
}

func getenvInt64(key string, fallback int64) int64 {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	parsed, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}

func parseAdminEmails(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}

	parts := strings.Split(raw, ",")
	admins := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		email := strings.ToLower(strings.TrimSpace(part))
		if email == "" {
			continue
		}
		if _, ok := seen[email]; ok {
			continue
		}
		seen[email] = struct{}{}
		admins = append(admins, email)
	}
	return admins
}

func loadTimezone() string {
	if tz := strings.TrimSpace(getenv("HOPSHARE_TIMEZONE", "")); tz != "" {
		return tz
	}
	return "UTC"
}
