package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadCreatesMissingConfigAtConfiguredPath(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)

	configPath := filepath.Join(root, "data", "config.json")
	t.Setenv("CBR_CONFIG", configPath)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	if _, err := os.Stat(configPath); err != nil {
		t.Fatalf("expected config file to be created: %v", err)
	}
	if cfg.Auth.SessionSecret == "" {
		t.Fatalf("expected generated session secret")
	}
	if cfg.Server.ListenAddr != ":8080" {
		t.Fatalf("listen_addr = %q, want %q", cfg.Server.ListenAddr, ":8080")
	}
	if cfg.Storage.DSN != "./data/clipboard_river.db" {
		t.Fatalf("dsn = %q, want %q", cfg.Storage.DSN, "./data/clipboard_river.db")
	}
	if cfg.Admin.Username != "admin" {
		t.Fatalf("admin.username = %q, want %q", cfg.Admin.Username, "admin")
	}
}
