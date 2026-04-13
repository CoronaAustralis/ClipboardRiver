package app

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/clipboardriver/cb_river_server/internal/auth"
	"github.com/clipboardriver/cb_river_server/internal/model"
	"golang.org/x/crypto/bcrypt"
)

type basePageData struct {
	Title         string
	ActiveNav     string
	AdminUsername string
	Message       string
	Locale        string
	LangZHURL     string
	LangENURL     string
	T             func(string) string
}

const minAdminPasswordLength = 8

func (a *App) handleAdminLoginPage(w http.ResponseWriter, r *http.Request) {
	if a.isAdminAuthenticated(r) {
		http.Redirect(w, r, "/admin/history", http.StatusFound)
		return
	}
	a.render(w, "login", map[string]any{
		"Base": a.pageBase(w, r, "page.login", ""),
	})
}

func (a *App) handleAdminLogin(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		redirectWithMessage(w, r, "/admin/login", "invalid_form", nil)
		return
	}
	username := strings.TrimSpace(r.FormValue("username"))
	password := r.FormValue("password")

	var admin model.AdminUser
	if err := a.db.Where("username = ?", username).First(&admin).Error; err != nil {
		redirectWithMessage(w, r, "/admin/login", "login_failed", nil)
		return
	}
	if err := bcrypt.CompareHashAndPassword([]byte(admin.PasswordHash), []byte(password)); err != nil {
		redirectWithMessage(w, r, "/admin/login", "login_failed", nil)
		return
	}
	a.createAdminSession(w, admin.Username)
	http.Redirect(w, r, "/admin/history", http.StatusFound)
}

func (a *App) handleAdminLogout(w http.ResponseWriter, r *http.Request) {
	a.clearAdminSession(w)
	redirectWithMessage(w, r, "/admin/login", "logged_out", nil)
}

func (a *App) pageBase(w http.ResponseWriter, r *http.Request, titleKey, nav string) basePageData {
	lang := a.resolveLang(w, r)
	t := a.translator(lang)
	username, _ := a.adminUsername(r)
	return basePageData{
		Title:         t(titleKey),
		ActiveNav:     nav,
		AdminUsername: username,
		Message:       translateMessage(t, r.URL.Query().Get("msg")),
		Locale:        lang,
		LangZHURL:     languageURL(r, langZH),
		LangENURL:     languageURL(r, langEN),
		T:             t,
	}
}

func (a *App) handleAdminHistory(w http.ResponseWriter, r *http.Request) {
	page := parsePositiveInt(r.URL.Query().Get("page"), 1)
	deviceID := strings.TrimSpace(r.URL.Query().Get("device_id"))
	kind := strings.TrimSpace(r.URL.Query().Get("kind"))
	query := strings.TrimSpace(r.URL.Query().Get("q"))

	tx := a.db.Model(&model.ClipboardItem{}).Where("account_id = ?", a.account.ID)
	if deviceID != "" {
		tx = tx.Where("source_device_id = ?", deviceID)
	}
	if kind != "" {
		tx = tx.Where("content_kind = ?", kind)
	}
	if query != "" {
		tx = tx.Where("text_content LIKE ?", "%"+query+"%")
	}

	var items []model.ClipboardItem
	const pageSize = 50
	if err := tx.Order("id DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&items).Error; err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	var devices []model.Device
	_ = a.db.Where("account_id = ?", a.account.ID).Order("nickname ASC, id ASC").Find(&devices).Error
	deviceNames := make(map[uint]string, len(devices))
	for _, device := range devices {
		name := device.Nickname
		if strings.TrimSpace(name) == "" {
			name = device.DeviceUUID
		}
		deviceNames[device.ID] = name
	}

	type historyRow struct {
		Item       model.ClipboardItem
		DeviceName string
	}
	rows := make([]historyRow, 0, len(items))
	for _, item := range items {
		rows = append(rows, historyRow{
			Item:       item,
			DeviceName: deviceNames[item.SourceDeviceID],
		})
	}

	a.render(w, "history", map[string]any{
		"Base":    a.pageBase(w, r, "page.history", "history"),
		"Items":   rows,
		"Devices": devices,
		"Filters": map[string]string{
			"device_id": deviceID,
			"kind":      kind,
			"q":         query,
		},
		"Page": page,
	})
}

func (a *App) handleAdminDevices(w http.ResponseWriter, r *http.Request) {
	var devices []model.Device
	if err := a.db.Where("account_id = ?", a.account.ID).Order("updated_at DESC").Find(&devices).Error; err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	type deviceRow struct {
		Device model.Device
		Online bool
	}
	rows := make([]deviceRow, 0, len(devices))
	for _, device := range devices {
		rows = append(rows, deviceRow{
			Device: device,
			Online: a.hub.IsOnline(device.ID),
		})
	}
	a.render(w, "devices", map[string]any{
		"Base":    a.pageBase(w, r, "page.devices", "devices"),
		"Devices": rows,
	})
}

func (a *App) handleAdminToggleDeviceSend(w http.ResponseWriter, r *http.Request) {
	a.handleToggleDeviceField(w, r, "send_realtime_enabled")
}

func (a *App) handleAdminToggleDeviceReceive(w http.ResponseWriter, r *http.Request) {
	a.handleToggleDeviceField(w, r, "receive_realtime_enabled")
}

func (a *App) handleAdminToggleDeviceDisabled(w http.ResponseWriter, r *http.Request) {
	a.handleToggleDeviceField(w, r, "disabled")
}

func (a *App) handleToggleDeviceField(w http.ResponseWriter, r *http.Request, field string) {
	id, err := strconv.ParseUint(r.PathValue("id"), 10, 64)
	if err == nil {
		err = a.toggleDeviceField(uint(id), field)
	}
	message := "Device updated"
	if err != nil {
		message = "update_failed"
	} else {
		message = "device_updated"
	}
	redirectWithMessage(w, r, "/admin/devices", message, nil)
}

func (a *App) handleAdminRevokeDeviceToken(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseUint(r.PathValue("id"), 10, 64)
	if err == nil {
		err = a.db.Model(&model.Device{}).Where("id = ?", id).Update("device_token_hash", "").Error
	}
	message := "device_token_revoked"
	if err != nil {
		message = "revoke_failed"
	}
	redirectWithMessage(w, r, "/admin/devices", message, nil)
}

func (a *App) handleAdminTokens(w http.ResponseWriter, r *http.Request) {
	var tokens []model.EnrollmentToken
	if err := a.db.Where("account_id = ?", a.account.ID).Order("created_at DESC").Find(&tokens).Error; err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	now := time.Now().UTC()
	type tokenRow struct {
		Token       model.EnrollmentToken
		StatusKey   string
		StatusClass string
		CanRevoke   bool
	}
	rows := make([]tokenRow, 0, len(tokens))
	for _, token := range tokens {
		status := resolveEnrollmentTokenStatus(token, now)
		row := tokenRow{
			Token:       token,
			StatusKey:   "tokens.active",
			StatusClass: "ok",
			CanRevoke:   status == enrollmentTokenStatusActive,
		}
		switch status {
		case enrollmentTokenStatusRevoked:
			row.StatusKey = "tokens.revoked"
			row.StatusClass = "warn"
		case enrollmentTokenStatusExpired:
			row.StatusKey = "tokens.expired"
			row.StatusClass = "warn"
		case enrollmentTokenStatusExhausted:
			row.StatusKey = "tokens.exhausted"
			row.StatusClass = "warn"
		}
		rows = append(rows, row)
	}

	a.render(w, "tokens", map[string]any{
		"Base":        a.pageBase(w, r, "page.tokens", "tokens"),
		"Tokens":      rows,
		"CreatedCode": r.URL.Query().Get("created"),
	})
}

func (a *App) handleAdminCreateToken(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		redirectWithMessage(w, r, "/admin/tokens", "invalid_form", nil)
		return
	}
	code, err := auth.RandomToken(12)
	if err != nil {
		redirectWithMessage(w, r, "/admin/tokens", "update_failed", nil)
		return
	}
	maxUses := parsePositiveInt(r.FormValue("max_uses"), 1)
	expiresHours := parsePositiveInt(r.FormValue("expires_hours"), 24*7)
	var expiresAt *time.Time
	if r.FormValue("no_expiry") == "" {
		value := time.Now().UTC().Add(time.Duration(expiresHours) * time.Hour)
		expiresAt = &value
	}

	token := model.EnrollmentToken{
		AccountID:  a.account.ID,
		CodeHash:   auth.HashToken(code),
		CodePrefix: code[:12],
		MaxUses:    maxUses,
		ExpiresAt:  expiresAt,
	}
	if err := a.db.Create(&token).Error; err != nil {
		redirectWithMessage(w, r, "/admin/tokens", "update_failed", nil)
		return
	}
	redirectWithMessage(w, r, "/admin/tokens", "token_created", map[string]string{"created": code})
}

func (a *App) handleAdminRevokeToken(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseUint(r.PathValue("id"), 10, 64)
	if err != nil {
		redirectWithMessage(w, r, "/admin/tokens", "revoke_failed", nil)
		return
	}

	var token model.EnrollmentToken
	if err := a.db.Where("account_id = ? AND id = ?", a.account.ID, id).First(&token).Error; err != nil {
		redirectWithMessage(w, r, "/admin/tokens", "revoke_failed", nil)
		return
	}

	now := time.Now().UTC()
	if resolveEnrollmentTokenStatus(token, now) != enrollmentTokenStatusActive {
		redirectWithMessage(w, r, "/admin/tokens", "token_not_active", nil)
		return
	}
	if err := a.db.Model(&model.EnrollmentToken{}).Where("id = ?", id).Update("revoked_at", &now).Error; err != nil {
		redirectWithMessage(w, r, "/admin/tokens", "revoke_failed", nil)
		return
	}
	redirectWithMessage(w, r, "/admin/tokens", "token_revoked", nil)
}

func (a *App) handleAdminSettings(w http.ResponseWriter, r *http.Request) {
	var account model.Account
	if err := a.db.First(&account, a.account.ID).Error; err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	a.account = account
	a.render(w, "settings", map[string]any{
		"Base":     a.pageBase(w, r, "page.settings", "settings"),
		"Account":  account,
		"BlobDir":  a.cfg.Storage.BlobDir,
		"DBDriver": a.cfg.Storage.Driver,
	})
}

func (a *App) handleAdminUpdateSettings(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		redirectWithMessage(w, r, "/admin/settings", "invalid_form", nil)
		return
	}
	retentionDays := 0
	if value := strings.TrimSpace(r.FormValue("retention_days")); value != "" {
		if parsed, err := strconv.Atoi(value); err == nil && parsed >= 0 {
			retentionDays = parsed
		}
	}
	imageMaxBytes := a.account.ImageMaxBytes
	if value := strings.TrimSpace(r.FormValue("image_max_bytes")); value != "" {
		if parsed, err := strconv.ParseInt(value, 10, 64); err == nil && parsed > 0 {
			imageMaxBytes = parsed
		}
	}
	updates := map[string]any{
		"realtime_fanout_enabled": r.FormValue("realtime_fanout_enabled") == "on",
		"retention_days":          retentionDays,
		"image_max_bytes":         imageMaxBytes,
	}
	if err := a.db.Model(&model.Account{}).Where("id = ?", a.account.ID).Updates(updates).Error; err != nil {
		redirectWithMessage(w, r, "/admin/settings", "save_failed", nil)
		return
	}
	_ = a.db.First(&a.account, a.account.ID).Error
	redirectWithMessage(w, r, "/admin/settings", "settings_saved", nil)
}

func (a *App) handleAdminChangePassword(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		redirectWithMessage(w, r, "/admin/settings", "invalid_form", nil)
		return
	}

	username, ok := a.adminUsername(r)
	if !ok {
		http.Redirect(w, r, "/admin/login", http.StatusFound)
		return
	}

	currentPassword := r.FormValue("current_password")
	newPassword := r.FormValue("new_password")
	confirmPassword := r.FormValue("confirm_password")

	if newPassword != confirmPassword {
		redirectWithMessage(w, r, "/admin/settings", "password_confirm_mismatch", nil)
		return
	}
	if len(newPassword) < minAdminPasswordLength {
		redirectWithMessage(w, r, "/admin/settings", "password_too_short", nil)
		return
	}

	var admin model.AdminUser
	if err := a.db.Where("username = ?", username).First(&admin).Error; err != nil {
		redirectWithMessage(w, r, "/admin/settings", "save_failed", nil)
		return
	}
	if err := bcrypt.CompareHashAndPassword([]byte(admin.PasswordHash), []byte(currentPassword)); err != nil {
		redirectWithMessage(w, r, "/admin/settings", "password_current_invalid", nil)
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		redirectWithMessage(w, r, "/admin/settings", "save_failed", nil)
		return
	}
	if err := a.db.Model(&model.AdminUser{}).Where("id = ?", admin.ID).Update("password_hash", string(hash)).Error; err != nil {
		redirectWithMessage(w, r, "/admin/settings", "save_failed", nil)
		return
	}

	redirectWithMessage(w, r, "/admin/settings", "password_changed", nil)
}
