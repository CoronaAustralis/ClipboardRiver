package app

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/clipboardriver/cb_river_server/internal/model"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
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

type historyRow struct {
	Item       model.ClipboardItem
	DeviceName string
	BlobName   string
	PreviewImg bool
}

type historyPaginationLink struct {
	Label  string
	URL    string
	Active bool
	Gap    bool
}

type historyPaginationData struct {
	Page           int
	PageSize       int
	TotalItems     int64
	TotalPages     int
	HasMultiple    bool
	HasPrev        bool
	HasNext        bool
	PrevURL        string
	NextURL        string
	Links          []historyPaginationLink
	CurrentPageURL string
}

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
	base := a.pageBase(w, r, "page.history", "history")
	page := parsePositiveInt(r.URL.Query().Get("page"), 1)
	deviceID := strings.TrimSpace(r.URL.Query().Get("device_id"))
	kind := strings.TrimSpace(r.URL.Query().Get("kind"))
	query := strings.TrimSpace(r.URL.Query().Get("q"))

	baseTx := a.db.Model(&model.ClipboardItem{}).Where("account_id = ?", a.account.ID)
	if deviceID != "" {
		baseTx = baseTx.Where("source_device_id = ?", deviceID)
	}
	if kind != "" {
		baseTx = baseTx.Where("content_kind = ?", kind)
	}
	if query != "" {
		baseTx = baseTx.Where("text_content LIKE ?", "%"+query+"%")
	}

	var items []model.ClipboardItem
	const pageSize = 50
	var totalItems int64
	if err := baseTx.Session(&gorm.Session{}).Count(&totalItems).Error; err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	totalPages := int((totalItems + int64(pageSize) - 1) / int64(pageSize))
	if totalPages == 0 {
		totalPages = 1
	}
	if page > totalPages {
		page = totalPages
	}

	if err := baseTx.Session(&gorm.Session{}).
		Order("id DESC").
		Offset((page - 1) * pageSize).
		Limit(pageSize).
		Find(&items).Error; err != nil {
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
		if device.Deleted {
			name += " (" + base.T("history.deleted_device") + ")"
		}
		deviceNames[device.ID] = name
	}
	deletedDeviceName := base.T("history.deleted_device")

	rows := make([]historyRow, 0, len(items))
	for _, item := range items {
		deviceName := deviceNames[item.SourceDeviceID]
		if strings.TrimSpace(deviceName) == "" {
			deviceName = deletedDeviceName
		}
		rows = append(rows, historyRow{
			Item:       item,
			DeviceName: deviceName,
			BlobName:   fileNameFromPath(item.BlobPath),
			PreviewImg: item.ContentKind == model.ContentKindFile && strings.HasPrefix(strings.ToLower(item.MimeType), "image/"),
		})
	}

	a.render(w, "history", map[string]any{
		"Base":       base,
		"Items":      rows,
		"Devices":    devices,
		"Pagination": buildHistoryPagination(page, pageSize, totalItems, deviceID, kind, query),
		"Filters": map[string]string{
			"device_id": deviceID,
			"kind":      kind,
			"q":         query,
		},
	})
}

func buildHistoryPagination(page, pageSize int, totalItems int64, deviceID, kind, query string) historyPaginationData {
	totalPages := int((totalItems + int64(pageSize) - 1) / int64(pageSize))
	if totalPages == 0 {
		totalPages = 1
	}

	pagination := historyPaginationData{
		Page:           page,
		PageSize:       pageSize,
		TotalItems:     totalItems,
		TotalPages:     totalPages,
		HasMultiple:    totalPages > 1,
		HasPrev:        page > 1,
		HasNext:        page < totalPages,
		CurrentPageURL: buildHistoryPageURL(page, deviceID, kind, query),
	}
	if pagination.HasPrev {
		pagination.PrevURL = buildHistoryPageURL(page-1, deviceID, kind, query)
	}
	if pagination.HasNext {
		pagination.NextURL = buildHistoryPageURL(page+1, deviceID, kind, query)
	}

	addLink := func(targetPage int) {
		if targetPage < 1 || targetPage > totalPages {
			return
		}
		pagination.Links = append(pagination.Links, historyPaginationLink{
			Label:  strconv.Itoa(targetPage),
			URL:    buildHistoryPageURL(targetPage, deviceID, kind, query),
			Active: targetPage == page,
		})
	}
	addGap := func() {
		if len(pagination.Links) == 0 || pagination.Links[len(pagination.Links)-1].Gap {
			return
		}
		pagination.Links = append(pagination.Links, historyPaginationLink{
			Label: "…",
			Gap:   true,
		})
	}

	addLink(1)
	if totalPages > 1 {
		start := page - 1
		if start < 2 {
			start = 2
		}
		end := page + 1
		if end > totalPages-1 {
			end = totalPages - 1
		}
		if start > 2 {
			addGap()
		}
		for p := start; p <= end; p++ {
			addLink(p)
		}
		if end < totalPages-1 {
			addGap()
		}
		addLink(totalPages)
	}

	return pagination
}

func buildHistoryPageURL(page int, deviceID, kind, query string) string {
	values := url.Values{}
	if page > 1 {
		values.Set("page", strconv.Itoa(page))
	}
	if deviceID != "" {
		values.Set("device_id", deviceID)
	}
	if kind != "" {
		values.Set("kind", kind)
	}
	if query != "" {
		values.Set("q", query)
	}
	encoded := values.Encode()
	if encoded == "" {
		return "/admin/history"
	}
	return fmt.Sprintf("/admin/history?%s", encoded)
}

func (a *App) handleAdminDevices(w http.ResponseWriter, r *http.Request) {
	var devices []model.Device
	if err := a.db.Where("account_id = ? AND deleted = ?", a.account.ID, false).Order("updated_at DESC").Find(&devices).Error; err != nil {
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

func (a *App) handleAdminDeleteDevice(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseUint(r.PathValue("id"), 10, 64)
	if err == nil && id > 0 {
		a.stateMu.Lock()
		result := a.db.Model(&model.Device{}).
			Where("account_id = ? AND id = ? AND deleted = ?", a.account.ID, id, false).
			Updates(map[string]any{"deleted": true, "device_token_hash": ""})
		err = result.Error
		if err == nil && result.RowsAffected > 0 {
			a.hub.CloseDevice(uint(id))
			a.notifySyncChangedLocked()
		}
		a.stateMu.Unlock()
	}
	message := "device_deleted"
	if err != nil || id == 0 {
		message = "update_failed"
	}
	redirectWithMessage(w, r, "/admin/devices", message, nil)
}

func (a *App) handleAdminTokens(w http.ResponseWriter, r *http.Request) {
	a.render(w, "tokens", map[string]any{
		"Base":       a.pageBase(w, r, "page.tokens", "tokens"),
		"Enrollment": a.enrollment.snapshot(),
	})
}

func (a *App) handleAdminOpenEnrollment(w http.ResponseWriter, r *http.Request) {
	if err := a.enrollment.open(); err != nil {
		redirectWithMessage(w, r, "/admin/tokens", "update_failed", nil)
		return
	}
	redirectWithMessage(w, r, "/admin/tokens", "enrollment_opened", nil)
}

func (a *App) handleAdminCloseEnrollment(w http.ResponseWriter, r *http.Request) {
	a.enrollment.close()
	redirectWithMessage(w, r, "/admin/tokens", "enrollment_closed", nil)
}

func (a *App) handleAdminSettings(w http.ResponseWriter, r *http.Request) {
	a.stateMu.Lock()
	account := a.currentAccount()
	a.stateMu.Unlock()
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
	a.stateMu.Lock()
	defer a.stateMu.Unlock()
	retentionDays, err := strconv.Atoi(strings.TrimSpace(r.FormValue("retention_days")))
	fileMaxBytes, fileErr := strconv.ParseInt(strings.TrimSpace(r.FormValue("file_max_bytes")), 10, 64)
	if err != nil || retentionDays < 0 || fileErr != nil || fileMaxBytes < -1 {
		redirectWithMessage(w, r, "/admin/settings", "invalid_form", nil)
		return
	}
	updates := map[string]any{"retention_days": retentionDays, "file_max_bytes": fileMaxBytes}
	if err := a.db.Model(&model.Account{}).Where("id = ?", a.account.ID).Updates(updates).Error; err != nil {
		redirectWithMessage(w, r, "/admin/settings", "save_failed", nil)
		return
	}
	a.account.RetentionDays = retentionDays
	a.account.FileMaxBytes = fileMaxBytes
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
