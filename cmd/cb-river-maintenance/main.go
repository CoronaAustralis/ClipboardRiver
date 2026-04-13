package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/clipboardriver/cb_river_server/internal/config"
	"github.com/clipboardriver/cb_river_server/internal/model"
	"github.com/clipboardriver/cb_river_server/internal/store"
	"gorm.io/gorm"
)

func main() {
	log.SetFlags(0)

	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}

	switch os.Args[1] {
	case "clear-history":
		if err := runClearHistory(); err != nil {
			log.Fatalf("clear history: %v", err)
		}
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "Usage:")
	fmt.Fprintln(os.Stderr, "  cb-river-maintenance clear-history")
}

func runClearHistory() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	db, err := store.Open(cfg)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		return fmt.Errorf("open sql db: %w", err)
	}
	defer func() {
		_ = sqlDB.Close()
	}()

	var beforeCount int64
	if err := db.Model(&model.ClipboardItem{}).Count(&beforeCount).Error; err != nil {
		return fmt.Errorf("count clipboard items: %w", err)
	}

	result := db.Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(&model.ClipboardItem{})
	if result.Error != nil {
		return fmt.Errorf("delete clipboard items: %w", result.Error)
	}

	removedEntries, err := purgeDirContents(cfg.Storage.BlobDir)
	if err != nil {
		return err
	}

	fmt.Printf("clipboard_items_before=%d\n", beforeCount)
	fmt.Printf("clipboard_items_deleted=%d\n", result.RowsAffected)
	fmt.Printf("blob_dir=%s\n", cfg.Storage.BlobDir)
	fmt.Printf("blob_entries_removed=%d\n", removedEntries)
	fmt.Println("done=true")
	return nil
}

func purgeDirContents(root string) (int, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return 0, fmt.Errorf("resolve blob dir: %w", err)
	}
	absRoot = filepath.Clean(absRoot)
	if !safePurgeRoot(absRoot) {
		return 0, fmt.Errorf("refusing to purge unsafe blob dir %q", absRoot)
	}

	entries, err := os.ReadDir(absRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("read blob dir %q: %w", absRoot, err)
	}

	removed := 0
	for _, entry := range entries {
		target := filepath.Join(absRoot, entry.Name())
		if err := os.RemoveAll(target); err != nil {
			return removed, fmt.Errorf("remove blob entry %q: %w", target, err)
		}
		removed++
	}
	return removed, nil
}

func safePurgeRoot(path string) bool {
	if path == "" {
		return false
	}

	volume := filepath.VolumeName(path)
	root := volume + string(os.PathSeparator)
	if path == root {
		return false
	}

	base := filepath.Base(path)
	if base == "" || base == "." || base == string(os.PathSeparator) {
		return false
	}
	return true
}
