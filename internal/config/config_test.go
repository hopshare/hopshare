package config

import (
	"reflect"
	"testing"
)

func TestLoadParsesAdmins(t *testing.T) {
	t.Setenv("HOPSHARE_ADDR", ":9090")
	t.Setenv("HOPSHARE_DB_URL", "postgres://example")
	t.Setenv("HOPSHARE_ENV", "test")
	t.Setenv("HOPSHARE_ADMINS", " Alice ,bob,ALICE,, carol ")

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

	wantAdmins := []string{"alice", "bob", "carol"}
	if !reflect.DeepEqual(cfg.Admins, wantAdmins) {
		t.Fatalf("admins: got %v want %v", cfg.Admins, wantAdmins)
	}
}

func TestLoadWithoutAdmins(t *testing.T) {
	t.Setenv("HOPSHARE_ADMINS", "")
	cfg := Load()
	if len(cfg.Admins) != 0 {
		t.Fatalf("admins: got %v want empty", cfg.Admins)
	}
}
