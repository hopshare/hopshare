package config

import (
	"os"
	"strings"
)

// Config holds server configuration loaded from environment variables.
type Config struct {
	Addr               string
	DatabaseURL        string
	Env                string
	Admins             []string
	Timezone           string
	PublicBaseURL      string
	MailgunAPIBaseURL  string
	MailgunDomain      string
	MailgunAPIKey      string
	MailgunFromAddress string
}

// Load returns configuration populated from HOPSHARE_* environment variables.
func Load() Config {
	return Config{
		Addr:               getenv("HOPSHARE_ADDR", ":8080"),
		DatabaseURL:        getenv("HOPSHARE_DB_URL", ""),
		Env:                getenv("HOPSHARE_ENV", "development"),
		Admins:             parseAdmins(getenv("HOPSHARE_ADMINS", "")),
		Timezone:           loadTimezone(),
		PublicBaseURL:      getenv("HOPSHARE_PUBLIC_BASE_URL", "http://localhost:8080"),
		MailgunAPIBaseURL:  getenv("HOPSHARE_MAILGUN_API_BASE_URL", "https://api.mailgun.net"),
		MailgunDomain:      getenv("HOPSHARE_MAILGUN_DOMAIN", "hopshare.org"),
		MailgunAPIKey:      getenv("HOPSHARE_MAILGUN_API_KEY", ""),
		MailgunFromAddress: getenv("HOPSHARE_MAILGUN_FROM_ADDRESS", "support@hopshare.org"),
	}
}

func getenv(key, fallback string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return fallback
}

func parseAdmins(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}

	parts := strings.Split(raw, ",")
	admins := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		username := strings.ToLower(strings.TrimSpace(part))
		if username == "" {
			continue
		}
		if _, ok := seen[username]; ok {
			continue
		}
		seen[username] = struct{}{}
		admins = append(admins, username)
	}
	return admins
}

func loadTimezone() string {
	if tz := strings.TrimSpace(getenv("HOPSHARE_TIMEZONE", "")); tz != "" {
		return tz
	}
	return "UTC"
}
