package store

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/clipboardriver/cb_river_server/internal/config"
	"github.com/clipboardriver/cb_river_server/internal/model"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestProtocolMigrationRemovesObsoleteSchemaAndPreservesHistory(t *testing.T) {
	cfg := config.Config{Storage: config.StorageConfig{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "migration.db")}}
	old, err := gorm.Open(sqlite.Open(cfg.Storage.DSN), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		"CREATE TABLE accounts (id integer PRIMARY KEY, name text, retention_days integer, file_max_bytes integer, realtime_fanout_enabled numeric)",
		"CREATE TABLE devices (id integer PRIMARY KEY, account_id integer, device_uuid text, nickname text, os_name text, os_version text, platform text, capabilities_json text, last_acked_cursor integer, disabled numeric)",
		"CREATE TABLE clipboard_items (id integer PRIMARY KEY, account_id integer, source_device_id integer, client_item_id text, content_kind text, text_content text, client_created_at datetime, received_at datetime)",
		"CREATE UNIQUE INDEX idx_source_client_item ON clipboard_items(source_device_id, client_item_id)",
		"CREATE TABLE enrollment_tokens (id integer PRIMARY KEY, code_hash text)",
		"INSERT INTO accounts (id, name, retention_days, file_max_bytes) VALUES (1, 'Default', 30, 0)",
		"INSERT INTO devices (id, account_id, device_uuid, nickname, disabled) VALUES (7, 1, 'kept-device', 'Desk', 0), (8, 1, 'deleted-device', 'Deleted Desk', 1)",
	} {
		if err := old.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
	received := time.Now().UTC().Truncate(time.Second)
	if err := old.Exec("INSERT INTO clipboard_items (id, account_id, source_device_id, client_item_id, content_kind, text_content, received_at) VALUES (31, 1, 7, 'old-local-id', 'text', 'retained', ?)", received).Error; err != nil {
		t.Fatal(err)
	}
	sqlDB, err := old.DB()
	if err != nil {
		t.Fatal(err)
	}
	_ = sqlDB.Close()
	db, err := Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err = db.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	if db.Migrator().HasTable("enrollment_tokens") || db.Migrator().HasIndex("clipboard_items", "idx_source_client_item") {
		t.Fatal("obsolete enrollment table or deduplication index remains")
	}
	for table, columns := range map[string][]string{
		"accounts":        {"realtime_fanout_enabled"},
		"devices":         {"os_name", "os_version", "platform", "capabilities_json", "last_acked_cursor", "disabled"},
		"clipboard_items": {"client_item_id"},
	} {
		for _, column := range columns {
			if db.Migrator().HasColumn(table, column) {
				t.Fatalf("obsolete column %s.%s remains", table, column)
			}
		}
	}
	var item model.ClipboardItem
	if err := db.First(&item, 31).Error; err != nil {
		t.Fatal(err)
	}
	if item.TextContent != "retained" || item.SourceDeviceID != 7 || !item.ClientCreatedAt.Equal(received) {
		t.Fatalf("history changed: %+v", item)
	}
	var device model.Device
	if err := db.First(&device, 7).Error; err != nil || device.Nickname != "Desk" || device.Deleted {
		t.Fatalf("device changed: %+v, %v", device, err)
	}
	var deleted model.Device
	if err := db.First(&deleted, 8).Error; err != nil || !deleted.Deleted || deleted.DeviceUUID != "deleted-device" {
		t.Fatalf("deleted flag was not migrated: %+v, %v", deleted, err)
	}
}
