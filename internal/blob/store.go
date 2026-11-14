package blob

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Store struct {
	root string
}

func New(root string) *Store {
	return &Store{root: root}
}

func (s *Store) Save(data io.Reader, originalName, fallbackExt string) (string, int64, error) {
	now := time.Now()
	dir := filepath.Join(s.root, now.Format("2006"), now.Format("01"), now.Format("02"))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", 0, fmt.Errorf("create blob dir: %w", err)
	}

	fileName, err := buildBlobFileName(originalName, fallbackExt)
	if err != nil {
		return "", 0, err
	}

	path, size, err := writeUniqueFile(dir, fileName, data)
	if err != nil {
		return "", 0, err
	}
	return path, size, nil
}

func (s *Store) Delete(path string) error {
	if path == "" {
		return nil
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove blob: %w", err)
	}
	return nil
}

func randomHex(size int) (string, error) {
	buf := make([]byte, size)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("random name: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

func buildBlobFileName(originalName, fallbackExt string) (string, error) {
	name := sanitizeFileName(originalName)
	if name == "" {
		randomName, err := randomHex(12)
		if err != nil {
			return "", err
		}
		return randomName + normalizeExt(fallbackExt), nil
	}

	if filepath.Ext(name) == "" {
		name += normalizeExt(fallbackExt)
	}
	return name, nil
}

func sanitizeFileName(name string) string {
	name = strings.TrimSpace(name)
	name = strings.ReplaceAll(name, "\\", "/")
	name = filepath.Base(name)
	name = strings.TrimSpace(name)
	name = strings.Map(func(r rune) rune {
		switch {
		case r == 0:
			return -1
		case r < 32:
			return -1
		case strings.ContainsRune(`<>:"/\|?*`, r):
			return -1
		default:
			return r
		}
	}, name)
	name = strings.TrimSpace(name)
	name = strings.Trim(name, ". ")
	if name == "" || name == "." || name == ".." {
		return ""
	}
	return name
}

func normalizeExt(ext string) string {
	ext = strings.TrimSpace(ext)
	if ext == "" {
		return ""
	}
	if !strings.HasPrefix(ext, ".") {
		return "." + ext
	}
	return ext
}

func writeUniqueFile(dir, fileName string, data io.Reader) (string, int64, error) {
	ext := filepath.Ext(fileName)
	base := strings.TrimSuffix(fileName, ext)
	candidates := []string{fileName}

	for i := 0; i < 8; i++ {
		suffix, err := randomHex(4)
		if err != nil {
			return "", 0, err
		}
		candidates = append(candidates, base+"-"+suffix+ext)
	}

	for _, candidate := range candidates {
		path := filepath.Join(dir, candidate)
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err != nil {
			if os.IsExist(err) {
				continue
			}
			return "", 0, fmt.Errorf("write blob: %w", err)
		}
		size, err := io.Copy(file, data)
		if err != nil {
			_ = file.Close()
			_ = os.Remove(path)
			return "", 0, fmt.Errorf("write blob: %w", err)
		}
		if err := file.Close(); err != nil {
			_ = os.Remove(path)
			return "", 0, fmt.Errorf("close blob: %w", err)
		}
		return path, size, nil
	}

	return "", 0, fmt.Errorf("write blob: could not allocate unique filename")
}
