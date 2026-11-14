package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadCreatesMissingConfigAtConfiguredPath(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)

	configPath := filepath.Join(root, "data", "config.json")

	cfg, err := LoadFile(configPath)
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
	if cfg.Sync.FileMaxBytes != 5*1024*1024 {
		t.Fatalf("file_max_bytes = %d, want %d", cfg.Sync.FileMaxBytes, 5*1024*1024)
	}
}

func TestLoadAcceptsFileMaxBytes(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)

	configPath := filepath.Join(root, "config.json")
	if err := os.WriteFile(configPath, []byte("{\n  \"sync\": {\n    \"file_max_bytes\": 654321\n  }\n}\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.Sync.FileMaxBytes != 654321 {
		t.Fatalf("file_max_bytes = %d, want %d", cfg.Sync.FileMaxBytes, 654321)
	}
}

func TestLoadUsesJSONAndIgnoresEnvironment(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	expected := Config{
		Server:  ServerConfig{ListenAddr: "127.0.0.1:9090"},
		Storage: StorageConfig{Driver: "sqlite", DSN: "./json-data/history.db", DataDir: "./json-data", BlobDir: "./json-blobs"},
		Auth:    AuthConfig{SessionSecret: "json-secret"},
		Sync:    SyncConfig{DefaultRetentionDays: 7, FileMaxBytes: -1, TextBatchLimit: 12},
		Admin:   AdminBootstrap{Username: "json-admin"},
	}
	data, err := json.Marshal(expected)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("config.json", data, 0o644); err != nil {
		t.Fatal(err)
	}
	ignoredConfig := filepath.Join(root, "ignored.json")
	for key, value := range map[string]string{
		"CBR_CONFIG": ignoredConfig, "CBR_LISTEN_ADDR": ":9999",
		"CBR_DB_DRIVER": "unsupported", "CBR_DB_DSN": "other-database",
		"CBR_DATA_DIR": "./env-data", "CBR_BLOB_DIR": "./env-blobs",
		"CBR_SESSION_SECRET": "env-secret", "CBR_ADMIN_USERNAME": "env-admin",
		"CBR_RETENTION_DAYS": "100", "CBR_FILE_MAX_BYTES": "123", "CBR_TEXT_BATCH_LIMIT": "999",
	} {
		t.Setenv(key, value)
	}
	actual, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if actual != expected {
		t.Fatalf("expected JSON configuration, got %+v", actual)
	}
	if _, err := os.Stat(ignoredConfig); !os.IsNotExist(err) {
		t.Fatalf("environment config was used: %v", err)
	}
}
