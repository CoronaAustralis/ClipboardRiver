package config

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
	return LoadFile("")
}

// LoadFile reads the selected JSON file, defaulting to ./config.json.
func LoadFile(configPath string) (Config, error) {
	cfg := defaultConfig()
	if configPath == "" {
		configPath = "config.json"
	}

	if err := ensureConfigFile(configPath, cfg); err != nil {
		return Config{}, err
	}
	if err := loadJSON(configPath, &cfg); err != nil {
		return Config{}, err
	}

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
		return Config{}, errors.New("text_batch_limit must be positive")
	}
	if cfg.Sync.FileMaxBytes < -1 {
		return Config{}, errors.New("file_max_bytes must be -1, 0, or positive")
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

func randomString(size int) (string, error) {
	raw := make([]byte, size)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate random string: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}
