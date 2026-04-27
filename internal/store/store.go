package store

import (
	"fmt"
	"strings"

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

	if err := db.AutoMigrate(
		&model.Account{},
		&model.AdminUser{},
		&model.EnrollmentToken{},
		&model.Device{},
		&model.ClipboardItem{},
	); err != nil {
		return nil, fmt.Errorf("auto migrate: %w", err)
	}
	if err := migrateLegacyClipboardSchema(db); err != nil {
		return nil, fmt.Errorf("migrate legacy schema: %w", err)
	}
	return db, nil
}

func migrateLegacyClipboardSchema(db *gorm.DB) error {
	accountColumns, err := db.Migrator().ColumnTypes(&model.Account{})
	if err != nil {
		return err
	}
	hasLegacyImageMax := false
	hasFileMax := false
	for _, column := range accountColumns {
		name := strings.ToLower(column.Name())
		if name == "image_max_bytes" {
			hasLegacyImageMax = true
		}
		if name == "file_max_bytes" {
			hasFileMax = true
		}
	}
	if hasLegacyImageMax && hasFileMax {
		if err := db.Exec("UPDATE accounts SET file_max_bytes = image_max_bytes WHERE file_max_bytes = 0 AND image_max_bytes > 0").Error; err != nil {
			return err
		}
	}
	if err := db.Model(&model.ClipboardItem{}).
		Where("content_kind = ?", "image").
		Update("content_kind", model.ContentKindFile).Error; err != nil {
		return err
	}
	return nil
}
