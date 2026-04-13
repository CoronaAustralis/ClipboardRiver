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
	return db, nil
}
