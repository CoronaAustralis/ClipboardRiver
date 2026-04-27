package config

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type Config struct {
	Server  ServerConfig   `json:"server"`
	Storage StorageConfig  `json:"storage"`
	Auth    AuthConfig     `json:"auth"`
	Sync    SyncConfig     `json:"sync"`
	Admin   AdminBootstrap `json:"admin"`
}

type ServerConfig struct {
	ListenAddr string `json:"listen_addr"`
}

type StorageConfig struct {
	Driver  string `json:"driver"`
	DSN     string `json:"dsn"`
	DataDir string `json:"data_dir"`
	BlobDir string `json:"blob_dir"`
}

type AuthConfig struct {
	SessionSecret string `json:"session_secret"`
}

type SyncConfig struct {
	DefaultRetentionDays int   `json:"default_retention_days"`
	FileMaxBytes         int64 `json:"file_max_bytes"`
	TextBatchLimit       int   `json:"text_batch_limit"`
}

type AdminBootstrap struct {
	Username string `json:"username"`
}

func defaultConfig() Config {
	return Config{
		Server: ServerConfig{
			ListenAddr: ":8080",
		},
		Storage: StorageConfig{
			Driver:  "sqlite",
			DSN:     "./data/clipboard_river.db",
			DataDir: "./data",
			BlobDir: "./data/blobs",
		},
		Auth: AuthConfig{
			SessionSecret: "",
		},
		Sync: SyncConfig{
			DefaultRetentionDays: 30,
			FileMaxBytes:         5 * 1024 * 1024,
			TextBatchLimit:       200,
		},
		Admin: AdminBootstrap{
			Username: "admin",
		},
	}
}

func Load() (Config, error) {
	cfg := defaultConfig()
	configPath := strings.TrimSpace(os.Getenv("CBR_CONFIG"))
	if configPath == "" {
		configPath = "config.json"
	}

	if err := ensureConfigFile(configPath, cfg); err != nil {
		return Config{}, err
	}
	if err := loadJSON(configPath, &cfg); err != nil {
		return Config{}, err
	}

	overrideString(&cfg.Server.ListenAddr, "CBR_LISTEN_ADDR")
	overrideString(&cfg.Storage.Driver, "CBR_DB_DRIVER")
	overrideString(&cfg.Storage.DSN, "CBR_DB_DSN")
	overrideString(&cfg.Storage.DataDir, "CBR_DATA_DIR")
	overrideString(&cfg.Storage.BlobDir, "CBR_BLOB_DIR")
	overrideString(&cfg.Auth.SessionSecret, "CBR_SESSION_SECRET")
	overrideString(&cfg.Admin.Username, "CBR_ADMIN_USERNAME")
	overrideInt(&cfg.Sync.DefaultRetentionDays, "CBR_RETENTION_DAYS")
	overrideInt64(&cfg.Sync.FileMaxBytes, "CBR_FILE_MAX_BYTES")
	overrideInt(&cfg.Sync.TextBatchLimit, "CBR_TEXT_BATCH_LIMIT")

	if cfg.Storage.DataDir == "" {
		cfg.Storage.DataDir = "./data"
	}
	if cfg.Storage.BlobDir == "" {
		cfg.Storage.BlobDir = filepath.Join(cfg.Storage.DataDir, "blobs")
	}
	if cfg.Storage.DSN == "" {
		switch strings.ToLower(cfg.Storage.Driver) {
		case "sqlite":
			cfg.Storage.DSN = filepath.Join(cfg.Storage.DataDir, "clipboard_river.db")
		case "postgres", "mysql":
			return Config{}, fmt.Errorf("storage dsn is required for %s", cfg.Storage.Driver)
		default:
			return Config{}, fmt.Errorf("unsupported storage driver %q", cfg.Storage.Driver)
		}
	}
	if cfg.Auth.SessionSecret == "" {
		secret, err := randomString(32)
		if err != nil {
			return Config{}, err
		}
		cfg.Auth.SessionSecret = secret
	}
	if strings.TrimSpace(cfg.Admin.Username) == "" {
		cfg.Admin.Username = "admin"
	}
	if cfg.Sync.TextBatchLimit <= 0 {
		cfg.Sync.TextBatchLimit = 200
	}
	if cfg.Sync.FileMaxBytes <= 0 {
		cfg.Sync.FileMaxBytes = 5 * 1024 * 1024
	}
	if err := os.MkdirAll(cfg.Storage.DataDir, 0o755); err != nil {
		return Config{}, fmt.Errorf("create data dir: %w", err)
	}
	if err := os.MkdirAll(cfg.Storage.BlobDir, 0o755); err != nil {
		return Config{}, fmt.Errorf("create blob dir: %w", err)
	}
	return cfg, nil
}

func ensureConfigFile(path string, cfg Config) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}

	if cfg.Auth.SessionSecret == "" {
		secret, err := randomString(32)
		if err != nil {
			return err
		}
		cfg.Auth.SessionSecret = secret
	}

	dir := filepath.Dir(path)
	if dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create config dir %s: %w", dir, err)
		}
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("encode default config: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write default config %s: %w", path, err)
	}
	return nil
}

func loadJSON(path string, cfg *Config) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read config %s: %w", path, err)
	}
	if err := json.Unmarshal(data, cfg); err != nil {
		return fmt.Errorf("decode config %s: %w", path, err)
	}
	return nil
}

func overrideString(target *string, key string) {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		*target = value
	}
}

func overrideInt(target *int, key string) {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		if parsed, err := strconv.Atoi(value); err == nil {
			*target = parsed
		}
	}
}

func overrideInt64(target *int64, key string) {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		if parsed, err := strconv.ParseInt(value, 10, 64); err == nil {
			*target = parsed
		}
	}
}

func randomString(size int) (string, error) {
	raw := make([]byte, size)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate random string: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}
