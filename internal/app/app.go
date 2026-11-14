package app

import (
	"context"
	cryptorand "crypto/rand"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log"
	"math/big"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/clipboardriver/cb_river_server/internal/auth"
	"github.com/clipboardriver/cb_river_server/internal/blob"
	"github.com/clipboardriver/cb_river_server/internal/config"
	"github.com/clipboardriver/cb_river_server/internal/model"
	"github.com/clipboardriver/cb_river_server/internal/store"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

const (
	adminSessionCookie   = "cbr_admin_session"
	adminSessionDuration = 30 * 24 * time.Hour
)

type App struct {
	cfg                  config.Config
	db                   *gorm.DB
	blobStore            *blob.Store
	hub                  *Hub
	templates            *template.Template
	account              model.Account
	initialAdminUsername string
	initialAdminPassword string
	stateMu              sync.Mutex
	textWindow           []model.ClipboardItem
	windowErr            error
	enrollment           enrollmentWindow
	cleanupDone          chan struct{}
	cleanupCtx           context.Context
	cancel               context.CancelFunc
}

func New(cfg config.Config) (*App, error) {
	if cfg.Sync.FileMaxBytes < -1 || cfg.Sync.TextBatchLimit <= 0 {
		return nil, errors.New("invalid file_max_bytes or text_batch_limit")
	}
	db, err := store.Open(cfg)
	if err != nil {
		return nil, err
	}

	account, bootstrap, err := seedDefaults(db, cfg)
	if err != nil {
		return nil, err
	}

	tpl, err := parseTemplates()
	if err != nil {
		return nil, err
	}

	cleanupCtx, cancel := context.WithCancel(context.Background())
	app := &App{
		cfg:         cfg,
		db:          db,
		blobStore:   blob.New(cfg.Storage.BlobDir),
		hub:         NewHub(),
		templates:   tpl,
		account:     account,
		cleanupCtx:  cleanupCtx,
		cancel:      cancel,
		cleanupDone: make(chan struct{}),
	}
	if bootstrap != nil {
		app.initialAdminUsername = bootstrap.Username
		app.initialAdminPassword = bootstrap.Password
	}

	if _, err := app.refreshTextWindowLocked(); err != nil {
		cancel()
		sqlDB, _ := db.DB()
		_ = sqlDB.Close()
		return nil, fmt.Errorf("initialize text window: %w", err)
	}
	go func() { defer close(app.cleanupDone); app.runCleanup(cleanupCtx) }()
	return app, nil
}

type adminBootstrapResult struct {
	Username string
	Password string
}

func (a *App) Close() error {
	a.cancel()
	<-a.cleanupDone
	a.enrollment.close()
	a.hub.Close()
	sqlDB, err := a.db.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}

func (a *App) Router() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", a.handleHealth)
	mux.HandleFunc("GET /{$}", a.handleRoot)

	mux.HandleFunc("POST /api/v1/client/register", a.handleClientRegister)
	mux.HandleFunc("POST /api/v1/client/device/profile", a.withDeviceAuth(a.handleClientProfile))
	mux.HandleFunc("POST /api/v1/client/items/text", a.withDeviceAuth(a.handleClientTextItem))
	mux.HandleFunc("POST /api/v1/client/items/text/batch", a.withDeviceAuth(a.handleClientTextBatch))
	mux.HandleFunc("POST /api/v1/client/items/file", a.handleClientFileItem)
	mux.HandleFunc("GET /api/v1/client/items/{item_id}/blob", a.handleClientBlob)
	mux.HandleFunc("POST /api/v1/client/sync/text", a.withDeviceAuth(a.handleClientTextSync))
	mux.HandleFunc("GET /api/v1/client/ws", a.handleClientWS)

	mux.HandleFunc("GET /admin/login", a.handleAdminLoginPage)
	mux.HandleFunc("POST /admin/login", a.handleAdminLogin)
	mux.HandleFunc("POST /admin/logout", a.handleAdminLogout)
	mux.HandleFunc("GET /admin/history", a.withAdminAuth(a.handleAdminHistory))
	mux.HandleFunc("GET /admin/devices", a.withAdminAuth(a.handleAdminDevices))
	mux.HandleFunc("POST /admin/devices/{id}/toggle-send", a.withAdminAuth(a.handleAdminToggleDeviceSend))
	mux.HandleFunc("POST /admin/devices/{id}/toggle-receive", a.withAdminAuth(a.handleAdminToggleDeviceReceive))
	mux.HandleFunc("POST /admin/devices/{id}/delete", a.withAdminAuth(a.handleAdminDeleteDevice))
	mux.HandleFunc("GET /admin/tokens", a.withAdminAuth(a.handleAdminTokens))
	mux.HandleFunc("POST /admin/tokens/open", a.withAdminAuth(a.handleAdminOpenEnrollment))
	mux.HandleFunc("POST /admin/tokens/close", a.withAdminAuth(a.handleAdminCloseEnrollment))
	mux.HandleFunc("GET /admin/settings", a.withAdminAuth(a.handleAdminSettings))
	mux.HandleFunc("POST /admin/settings", a.withAdminAuth(a.handleAdminUpdateSettings))
	mux.HandleFunc("POST /admin/settings/password", a.withAdminAuth(a.handleAdminChangePassword))

	return mux
}

func (a *App) handleRoot(w http.ResponseWriter, r *http.Request) {
	if a.isAdminAuthenticated(r) {
		http.Redirect(w, r, "/admin/history", http.StatusFound)
		return
	}
	http.Redirect(w, r, "/admin/login", http.StatusFound)
}

func (a *App) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "ok",
		"time":   time.Now().UTC(),
	})
}

func seedDefaults(db *gorm.DB, cfg config.Config) (model.Account, *adminBootstrapResult, error) {
	var account model.Account
	var bootstrap *adminBootstrapResult
	err := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.First(&account).Error; err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			account = model.Account{
				Name:          "Default",
				RetentionDays: cfg.Sync.DefaultRetentionDays,
				FileMaxBytes:  cfg.Sync.FileMaxBytes,
			}
			if err := tx.Create(&account).Error; err != nil {
				return err
			}
		} else if account.FileMaxBytes < -1 {
			return errors.New("stored file_max_bytes must be -1, 0, or positive")
		}

		var admin model.AdminUser
		err := tx.Where("username = ?", cfg.Admin.Username).First(&admin).Error
		if err == nil {
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}

		password, err := randomAdminPassword(10)
		if err != nil {
			return fmt.Errorf("generate admin password: %w", err)
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
		if err != nil {
			return fmt.Errorf("hash admin password: %w", err)
		}
		admin = model.AdminUser{
			AccountID:    account.ID,
			Username:     cfg.Admin.Username,
			PasswordHash: string(hash),
		}
		if err := tx.Create(&admin).Error; err != nil {
			return err
		}
		bootstrap = &adminBootstrapResult{
			Username: admin.Username,
			Password: password,
		}
		return nil
	})
	if err != nil {
		return model.Account{}, nil, fmt.Errorf("seed defaults: %w", err)
	}
	return account, bootstrap, nil
}

func (a *App) currentAccount() model.Account {
	return a.account
}

func (a *App) InitialAdminCredentials() (string, string, bool) {
	if a.initialAdminUsername == "" || a.initialAdminPassword == "" {
		return "", "", false
	}
	return a.initialAdminUsername, a.initialAdminPassword, true
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]any{"error": message})
}

func decodeJSON(r *http.Request, target any) error {
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return errors.New("expected one JSON object")
	}
	return nil
}

func clientTimeOrNow(raw string, now time.Time) (time.Time, error) {
	if raw == "" {
		return now, nil
	}
	parsed, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, errors.New("invalid client_created_at")
	}
	return parsed.UTC(), nil
}

func parsePositiveInt(raw string, fallback int) int {
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}

func (a *App) retentionExpiry(now time.Time) *time.Time {
	account := a.currentAccount()
	if account.RetentionDays <= 0 {
		return nil
	}
	expiresAt := now.Add(time.Duration(account.RetentionDays) * 24 * time.Hour)
	return &expiresAt
}

func (a *App) createAdminSession(w http.ResponseWriter, username string) {
	expiresAt := time.Now().Add(adminSessionDuration)
	value := auth.SignSession(a.cfg.Auth.SessionSecret, username, expiresAt)
	http.SetCookie(w, &http.Cookie{
		Name:     adminSessionCookie,
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Expires:  expiresAt,
	})
}

func (a *App) clearAdminSession(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     adminSessionCookie,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

func (a *App) adminUsername(r *http.Request) (string, bool) {
	cookie, err := r.Cookie(adminSessionCookie)
	if err != nil {
		return "", false
	}
	username, _, err := auth.ParseSession(a.cfg.Auth.SessionSecret, cookie.Value)
	if err != nil {
		return "", false
	}
	return username, true
}

func (a *App) isAdminAuthenticated(r *http.Request) bool {
	_, ok := a.adminUsername(r)
	return ok
}

func (a *App) withAdminAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !a.isAdminAuthenticated(r) {
			http.Redirect(w, r, "/admin/login", http.StatusFound)
			return
		}
		next(w, r)
	}
}

type requestContextKey string

const deviceContextKey requestContextKey = "device"

func (a *App) withDeviceAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		a.stateMu.Lock()
		defer a.stateMu.Unlock()
		device, err := a.authenticateDevice(r)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "invalid device token")
			return
		}
		ctx := context.WithValue(r.Context(), deviceContextKey, device)
		next(w, r.WithContext(ctx))
	}
}

func (a *App) authenticateDevice(r *http.Request) (*model.Device, error) {
	token := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
	if token == "" {
		token = strings.TrimSpace(r.URL.Query().Get("device_token"))
	}
	if token == "" {
		return nil, errors.New("missing token")
	}

	var device model.Device
	if err := a.db.Where("device_token_hash = ? AND deleted = ?", auth.HashToken(token), false).First(&device).Error; err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	_ = a.db.Model(&device).Updates(map[string]any{"last_seen_at": now}).Error
	device.LastSeenAt = &now
	return &device, nil
}

func currentDevice(r *http.Request) *model.Device {
	device, _ := r.Context().Value(deviceContextKey).(*model.Device)
	return device
}

func (a *App) render(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := a.templates.ExecuteTemplate(w, name, data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func compareStrings(aValue, bValue string) bool {
	return subtle.ConstantTimeCompare([]byte(aValue), []byte(bValue)) == 1
}

func randomAdminPassword(length int) (string, error) {
	if length <= 0 {
		return "", fmt.Errorf("invalid password length %d", length)
	}
	const charset = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789"
	max := big.NewInt(int64(len(charset)))
	password := make([]byte, length)
	for i := range password {
		index, err := cryptorand.Int(cryptorand.Reader, max)
		if err != nil {
			return "", fmt.Errorf("generate password char: %w", err)
		}
		password[i] = charset[index.Int64()]
	}
	return string(password), nil
}

func fileNameFromPath(path string) string {
	return filepath.Base(path)
}

func (a *App) toggleDeviceField(deviceID uint, field string) error {
	a.stateMu.Lock()
	defer a.stateMu.Unlock()
	var device model.Device
	if err := a.db.Where("account_id = ? AND deleted = ?", a.account.ID, false).First(&device, deviceID).Error; err != nil {
		return err
	}
	var value bool
	switch field {
	case "send_realtime_enabled":
		value = !device.SendRealtimeEnabled
	case "receive_realtime_enabled":
		value = !device.ReceiveRealtimeEnabled
	default:
		return fmt.Errorf("unknown field %s", field)
	}
	if err := a.db.Model(&device).Update(field, value).Error; err != nil {
		return err
	}
	// Queued messages are checked again under stateMu immediately before delivery.
	if field == "receive_realtime_enabled" && value {
		a.hub.Push(deviceID, wsMessage{Type: "clipboard.sync_changed"})
	}
	if field == "send_realtime_enabled" {
		for _, item := range a.textWindow {
			if item.SourceDeviceID == deviceID {
				a.notifySyncChangedLocked()
				break
			}
		}
	}
	return nil
}

func (a *App) settingsSnapshot() map[string]any {
	return map[string]any{
		"file_max_bytes":   a.account.FileMaxBytes,
		"text_batch_limit": a.cfg.Sync.TextBatchLimit,
	}
}

func (a *App) fanoutClipboardItem(source *model.Device, item model.ClipboardItem) {
	if source.Deleted || !source.SendRealtimeEnabled || item.UploadKind != model.UploadKindRealtime {
		return
	}
	var targets []model.Device
	if err := a.db.Where("account_id = ? AND id <> ? AND deleted = ? AND receive_realtime_enabled = ?", source.AccountID, source.ID, false, true).Find(&targets).Error; err != nil {
		return
	}
	for _, target := range targets {
		a.hub.Push(target.ID, wsMessage{Type: "clipboard.created", Item: &item})
	}
}

func formatUploadReceipt(item model.ClipboardItem) map[string]any {
	return map[string]any{
		"item_id":      item.ID,
		"content_kind": item.ContentKind,
		"upload_kind":  item.UploadKind,
		"mime_type":    item.MimeType,
		"received_at":  item.ReceivedAt,
	}
}

func formatItemResponse(item model.ClipboardItem) map[string]any {
	response := map[string]any{
		"item_id":           item.ID,
		"source_device_id":  item.SourceDeviceID,
		"content_kind":      item.ContentKind,
		"upload_kind":       item.UploadKind,
		"mime_type":         item.MimeType,
		"byte_size":         item.ByteSize,
		"char_count":        item.CharCount,
		"client_created_at": item.ClientCreatedAt,
		"received_at":       item.ReceivedAt,
	}
	if item.ContentKind == model.ContentKindText {
		response["text_content"] = item.TextContent
	}
	if item.BlobPath != "" {
		response["blob_name"] = fileNameFromPath(item.BlobPath)
	}
	return response
}

// Caller holds stateMu; every accepted upload creates a new row.
func (a *App) createClipboardItem(source *model.Device, item model.ClipboardItem) (model.ClipboardItem, error) {
	item.AccountID = source.AccountID
	item.SourceDeviceID = source.ID
	if item.ReceivedAt.IsZero() {
		item.ReceivedAt = time.Now().UTC()
	}
	item.ExpiresAt = a.retentionExpiry(item.ReceivedAt)
	if err := a.db.Create(&item).Error; err != nil {
		return model.ClipboardItem{}, err
	}
	return item, nil
}

func (a *App) runCleanup(ctx context.Context) {
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()
	a.cleanupExpired(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.cleanupExpired(ctx)
		}
	}
}

func (a *App) cleanupExpired(ctx context.Context) {
	a.stateMu.Lock()
	defer a.stateMu.Unlock()
	defer func() {
		changed, err := a.refreshTextWindowLocked()
		if err != nil {
			log.Printf("refresh text window after cleanup: %v", err)
			return
		}
		if changed {
			a.notifySyncChangedLocked()
		}
	}()

	for {
		var items []model.ClipboardItem
		if err := a.db.WithContext(ctx).
			Where("expires_at IS NOT NULL AND expires_at <= ?", time.Now().UTC()).
			Order("id ASC").
			Limit(100).
			Find(&items).Error; err != nil {
			return
		}
		if len(items) == 0 {
			return
		}

		for _, item := range items {
			if err := a.db.WithContext(ctx).Delete(&model.ClipboardItem{}, item.ID).Error; err != nil {
				return
			}
			_ = a.blobStore.Delete(item.BlobPath)
		}
	}
}
