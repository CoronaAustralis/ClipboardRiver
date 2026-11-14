package store

import (
	"fmt"
	"strings"
	"time"

	"github.com/clipboardriver/cb_river_server/internal/config"
	"github.com/clipboardriver/cb_river_server/internal/model"
	"github.com/glebarez/sqlite"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func Open(cfg config.Config) (*gorm.DB, error) {
	var dialector gorm.Dialector

	switch strings.ToLower(cfg.Storage.Driver) {
	case "sqlite":
		dialector = sqlite.Open(cfg.Storage.DSN)
	case "postgres":
		dialector = postgres.Open(cfg.Storage.DSN)
	case "mysql":
		dialector = mysql.Open(cfg.Storage.DSN)
	default:
		return nil, fmt.Errorf("unsupported storage driver %q", cfg.Storage.Driver)
	}

	db, err := gorm.Open(dialector, &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	if err := removeObsoleteSchema(db); err != nil {
		return nil, fmt.Errorf("remove obsolete schema: %w", err)
	}
	if err := db.AutoMigrate(
		&model.Account{},
		&model.AdminUser{},
		&model.Device{},
		&model.ClipboardItem{},
	); err != nil {
		return nil, fmt.Errorf("auto migrate: %w", err)
	}
	if err := db.Model(&model.ClipboardItem{}).
		Where("client_created_at IS NULL OR client_created_at <= ?", time.Time{}).
		Update("client_created_at", gorm.Expr("received_at")).Error; err != nil {
		return nil, fmt.Errorf("fill missing creation times: %w", err)
	}
	return db, nil
}

// This is a destructive protocol migration: retain history IDs, discard obsolete metadata.
func removeObsoleteSchema(db *gorm.DB) error {
	m := db.Migrator()
	if m.HasTable(&model.Device{}) && m.HasColumn(&model.Device{}, "disabled") {
		if !m.HasColumn(&model.Device{}, "deleted") {
			if err := m.RenameColumn(&model.Device{}, "disabled", "deleted"); err != nil {
				return err
			}
		} else if err := db.Table("devices").Where("disabled = ?", true).Update("deleted", true).Error; err != nil {
			return err
		}
		if err := db.Table("devices").Where("deleted IS NULL").Update("deleted", false).Error; err != nil {
			return err
		}
	}
	if m.HasTable("enrollment_tokens") {
		if err := m.DropTable("enrollment_tokens"); err != nil {
			return err
		}
	}
	if m.HasTable("clipboard_items") && m.HasIndex("clipboard_items", "idx_source_client_item") {
		if err := m.DropIndex("clipboard_items", "idx_source_client_item"); err != nil {
			return err
		}
	}
	for _, change := range []struct {
		table   string
		value   any
		columns []string
	}{
		{"accounts", &model.Account{}, []string{"realtime_fanout_enabled", "image_max_bytes"}},
		{"devices", &model.Device{}, []string{"os_name", "os_version", "platform", "capabilities_json", "last_acked_cursor", "disabled"}},
		{"clipboard_items", &model.ClipboardItem{}, []string{"client_item_id"}},
	} {
		if !m.HasTable(change.value) {
			continue
		}
		columns, err := m.ColumnTypes(change.value)
		if err != nil {
			return err
		}
		for _, column := range columns {
			for _, name := range change.columns {
				if strings.EqualFold(column.Name(), name) {
					var err error
					if db.Dialector.Name() == "sqlite" {
						// SQLite supports native column removal; avoid the driver's table-rebuild path.
						err = db.Exec(fmt.Sprintf("ALTER TABLE `%s` DROP COLUMN `%s`", change.table, name)).Error
					} else {
						err = m.DropColumn(change.value, name)
					}
					if err != nil {
						return err
					}
				}
			}
		}
	}

	return nil
}
