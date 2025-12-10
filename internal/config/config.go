package config

import (
	"os"
)

// Config holds server configuration loaded from environment variables.
type Config struct {
	Addr        string
	DatabaseURL string
	Env         string
}

// Load returns configuration populated from HOPSHARE_* environment variables.
func Load() Config {
	return Config{
		Addr:        getenv("HOPSHARE_ADDR", ":8080"),
		DatabaseURL: getenv("HOPSHARE_DB_URL", ""),
		Env:         getenv("HOPSHARE_ENV", "development"),
	}
}

func getenv(key, fallback string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return fallback
}
