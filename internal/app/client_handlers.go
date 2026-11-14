package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/clipboardriver/cb_river_server/internal/auth"
	"github.com/clipboardriver/cb_river_server/internal/model"
	"gorm.io/gorm"
)

type registerRequest struct {
	DeviceUUID     string  `json:"device_uuid"`
	EnrollmentCode string  `json:"enrollment_code"`
	Nickname       *string `json:"nickname"`
	AppName        *string `json:"app_name"`
	AppVersion     *string `json:"app_version"`
}

type profileRequest struct {
	AppName    *string `json:"app_name"`
	AppVersion *string `json:"app_version"`
}

type textItemRequest struct {
	UploadKind      string  `json:"upload_kind"`
	ClientCreatedAt string  `json:"client_created_at"`
	TextContent     *string `json:"text_content"`
}

type textBatchRequest struct {
	Items []json.RawMessage `json:"items"`
}

func (a *App) handleClientRegister(w http.ResponseWriter, r *http.Request) {
	var req registerRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json payload")
		return
	}
	if strings.TrimSpace(req.DeviceUUID) == "" || strings.TrimSpace(req.EnrollmentCode) == "" {
		writeError(w, http.StatusBadRequest, "device_uuid and enrollment_code are required")
		return
	}
	if !a.enrollment.authorize(req.EnrollmentCode) {
		writeError(w, http.StatusBadRequest, "enrollment is closed or code is invalid")
		return
	}
	rawToken, err := auth.RandomToken(24)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not issue device token")
		return
	}
	a.stateMu.Lock()
	defer a.stateMu.Unlock()
	var device model.Device
	wasDeleted := false
	now := time.Now().UTC()
	err = a.db.Transaction(func(tx *gorm.DB) error {
		err := tx.Where("account_id = ? AND device_uuid = ?", a.account.ID, req.DeviceUUID).First(&device).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if errors.Is(err, gorm.ErrRecordNotFound) {
			device = model.Device{AccountID: a.account.ID, DeviceUUID: req.DeviceUUID, SendRealtimeEnabled: true, ReceiveRealtimeEnabled: true}
		}
		if req.Nickname != nil {
			device.Nickname = *req.Nickname
		}
		if req.AppName != nil {
			device.AppName = *req.AppName
		}
		if req.AppVersion != nil {
			device.AppVersion = *req.AppVersion
		}
		wasDeleted = device.Deleted
		device.DeviceTokenHash = auth.HashToken(rawToken)
		device.Deleted = false
		device.LastSeenAt = &now
		return tx.Save(&device).Error
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "register device failed")
		return
	}
	a.hub.CloseDevice(device.ID)
	if wasDeleted && device.SendRealtimeEnabled {
		for _, item := range a.textWindow {
			if item.SourceDeviceID == device.ID {
				a.notifySyncChangedLocked()
				break
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"device_id": device.ID, "device_token": rawToken, "settings": a.settingsSnapshot()})
}

func (a *App) handleClientProfile(w http.ResponseWriter, r *http.Request) {
	var req profileRequest
	if err := decodeJSON(r, &req); err != nil && err != io.EOF {
		writeError(w, http.StatusBadRequest, "invalid json payload")
		return
	}
	updates := map[string]any{"last_seen_at": time.Now().UTC()}
	if req.AppName != nil {
		updates["app_name"] = *req.AppName
	}
	if req.AppVersion != nil {
		updates["app_version"] = *req.AppVersion
	}
	device := currentDevice(r)
	if err := a.db.Model(&model.Device{}).Where("id = ?", device.ID).Updates(updates).Error; err != nil {
		writeError(w, http.StatusInternalServerError, "update profile failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "device_id": device.ID, "server_at": time.Now().UTC(), "settings": a.settingsSnapshot()})
}

func (a *App) handleClientTextItem(w http.ResponseWriter, r *http.Request) {
	var req textItemRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json payload")
		return
	}
	item, err := buildTextItem(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	device := currentDevice(r)
	item, err = a.createClipboardItem(device, item)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "store text failed")
		return
	}
	changed, err := a.refreshTextWindowLocked()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "text window unavailable")
		return
	}
	a.fanoutClipboardItem(device, item)
	if item.UploadKind == model.UploadKindHistory && changed {
		a.notifySyncChangedLocked()
	}
	writeJSON(w, http.StatusOK, map[string]any{"created": true, "item": formatUploadReceipt(item)})
}

func (a *App) handleClientTextBatch(w http.ResponseWriter, r *http.Request) {
	var req textBatchRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json payload")
		return
	}
	if len(req.Items) == 0 || len(req.Items) > a.cfg.Sync.TextBatchLimit {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("items must contain 1 to %d entries", a.cfg.Sync.TextBatchLimit))
		return
	}
	device := currentDevice(r)
	results := make([]map[string]any, 0, len(req.Items))
	hasHistory := false
	for _, raw := range req.Items {
		var req textItemRequest
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		err := decoder.Decode(&req)
		var item model.ClipboardItem
		if err == nil {
			item, err = buildTextItem(req)
		} else {
			err = errors.New("invalid text item")
		}
		if err == nil {
			item, err = a.createClipboardItem(device, item)
			if err != nil {
				err = errors.New("store text failed")
			}
		}
		result := map[string]any{"created": err == nil}
		if err != nil {
			result["error"] = err.Error()
		} else {
			result["item"] = formatUploadReceipt(item)
			hasHistory = hasHistory || item.UploadKind == model.UploadKindHistory
			a.fanoutClipboardItem(device, item)
		}
		results = append(results, result)
	}
	changed, err := a.refreshTextWindowLocked()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "text window unavailable")
		return
	}
	if hasHistory && changed {
		a.notifySyncChangedLocked()
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": results})
}

func buildTextItem(req textItemRequest) (model.ClipboardItem, error) {
	if req.TextContent == nil || *req.TextContent == "" {
		return model.ClipboardItem{}, errors.New("text_content must be a nonempty string")
	}
	kind, err := normalizeUploadKind(req.UploadKind)
	if err != nil {
		return model.ClipboardItem{}, err
	}
	now := time.Now().UTC()
	createdAt, err := clientTimeOrNow(req.ClientCreatedAt, now)
	if err != nil {
		return model.ClipboardItem{}, err
	}
	return model.ClipboardItem{
		ContentKind: model.ContentKindText, UploadKind: kind, TextContent: *req.TextContent,
		ByteSize: int64(len(*req.TextContent)), CharCount: utf8.RuneCountInString(*req.TextContent),
		ClientCreatedAt: createdAt, ReceivedAt: now,
	}, nil
}

func (a *App) handleClientFileItem(w http.ResponseWriter, r *http.Request) {
	// Do not hold the state lock while receiving or streaming a potentially unlimited file.
	a.stateMu.Lock()
	_, authErr := a.authenticateDevice(r)
	limit := a.account.FileMaxBytes
	a.stateMu.Unlock()
	if authErr != nil {
		writeError(w, http.StatusUnauthorized, "invalid device token")
		return
	}
	if limit == -1 {
		writeError(w, http.StatusBadRequest, "file uploads are disabled")
		return
	}
	if limit > 0 && limit <= (1<<63-1)-(1<<20) {
		r.Body = http.MaxBytesReader(w, r.Body, limit+(1<<20))
	}
	defer func() {
		if r.MultipartForm != nil {
			_ = r.MultipartForm.RemoveAll()
		}
	}()
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		writeError(w, http.StatusBadRequest, "invalid or oversized multipart form")
		return
	}
	req, file, header, err := parseFileMultipart(r.MultipartForm)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	defer file.Close()
	if limit > 0 && header.Size > limit {
		writeError(w, http.StatusBadRequest, "file exceeds max size")
		return
	}
	now := time.Now().UTC()
	createdAt, err := clientTimeOrNow(req.ClientCreatedAt, now)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	prefix := make([]byte, 512)
	n, err := io.ReadFull(file, prefix)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		writeError(w, http.StatusInternalServerError, "read file failed")
		return
	}
	detectedMime := http.DetectContentType(prefix[:n])
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		writeError(w, http.StatusInternalServerError, "read file failed")
		return
	}
	blobPath, size, err := a.blobStore.Save(file, header.Filename, chooseBlobExtension(req.MimeType, detectedMime, header))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "store file failed")
		return
	}
	a.stateMu.Lock()
	defer a.stateMu.Unlock()
	device, err := a.authenticateDevice(r)
	if err != nil {
		_ = a.blobStore.Delete(blobPath)
		writeError(w, http.StatusUnauthorized, "invalid device token")
		return
	}
	// Settings may have changed while the body was being read.
	limit = a.account.FileMaxBytes
	if limit == -1 || (limit > 0 && size > limit) {
		_ = a.blobStore.Delete(blobPath)
		writeError(w, http.StatusBadRequest, "file uploads disabled or file exceeds max size")
		return
	}
	item, err := a.createClipboardItem(device, model.ClipboardItem{
		ContentKind: model.ContentKindFile, UploadKind: req.UploadKind, MimeType: detectedMime,
		BlobPath: blobPath, ByteSize: size, ClientCreatedAt: createdAt, ReceivedAt: now,
	})
	if err != nil {
		_ = a.blobStore.Delete(blobPath)
		writeError(w, http.StatusInternalServerError, "store file failed")
		return
	}
	a.fanoutClipboardItem(device, item)
	writeJSON(w, http.StatusOK, map[string]any{"created": true, "item": formatUploadReceipt(item)})
}

type fileMultipartRequest struct {
	UploadKind      string
	ClientCreatedAt string
	MimeType        string
}

func parseFileMultipart(form *multipart.Form) (fileMultipartRequest, multipart.File, *multipart.FileHeader, error) {
	req := fileMultipartRequest{}
	for key, values := range form.Value {
		if len(values) != 1 {
			return req, nil, nil, errors.New("duplicate form field")
		}
		switch key {
		case "upload_kind":
			req.UploadKind = values[0]
		case "client_created_at":
			req.ClientCreatedAt = values[0]
		case "mime_type":
			req.MimeType = values[0]
		default:
			return req, nil, nil, errors.New("unknown form field")
		}
	}
	kind, err := normalizeUploadKind(req.UploadKind)
	if err != nil {
		return req, nil, nil, err
	}
	req.UploadKind = kind
	files := form.File["file"]
	if len(form.File) != 1 || len(files) != 1 {
		return req, nil, nil, errors.New("exactly one file is required")
	}
	file, err := files[0].Open()
	if err != nil {
		return req, nil, nil, fmt.Errorf("open file: %w", err)
	}
	return req, file, files[0], nil
}

func chooseBlobExtension(requested, detected string, header *multipart.FileHeader) string {
	if ext := filepath.Ext(header.Filename); ext != "" {
		return ext
	}
	if ext := extByMime(firstNonEmpty(detected, requested)); ext != "" {
		return ext
	}
	return ".bin"
}

func extByMime(mimeType string) string {
	mimeType = strings.TrimSpace(strings.Split(mimeType, ";")[0])
	if mimeType == "" {
		return ""
	}
	extensions, err := mime.ExtensionsByType(mimeType)
	if err != nil || len(extensions) == 0 {
		return ""
	}
	return extensions[0]
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func (a *App) handleClientBlob(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseUint(r.PathValue("item_id"), 10, 64)
	if err != nil || id == 0 {
		http.NotFound(w, r)
		return
	}

	a.stateMu.Lock()
	var accountID uint
	if a.isAdminAuthenticated(r) {
		accountID = a.account.ID
	} else {
		device, err := a.authenticateDevice(r)
		if err != nil {
			a.stateMu.Unlock()
			writeError(w, http.StatusUnauthorized, "invalid device token")
			return
		}
		accountID = device.AccountID
	}
	var item model.ClipboardItem
	err = a.db.Where("account_id = ? AND content_kind = ? AND (expires_at IS NULL OR expires_at > ?)", accountID, model.ContentKindFile, time.Now().UTC()).First(&item, id).Error
	a.stateMu.Unlock()
	if err != nil || item.BlobPath == "" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", item.MimeType)
	setInlineContentDisposition(w, fileNameFromPath(item.BlobPath))
	http.ServeFile(w, r, item.BlobPath)
}

func setInlineContentDisposition(w http.ResponseWriter, fileName string) {
	fileName = strings.TrimSpace(fileName)
	if fileName == "" {
		w.Header().Set("Content-Disposition", "inline")
		return
	}
	asciiName := strings.Map(func(r rune) rune {
		switch {
		case r < 32 || r > 126:
			return '_'
		case r == '"' || r == '\\':
			return '_'
		default:
			return r
		}
	}, fileName)
	asciiName = strings.TrimSpace(asciiName)
	if asciiName == "" {
		asciiName = "blob"
	}
	w.Header().Set("Content-Disposition", fmt.Sprintf(`inline; filename="%s"; filename*=UTF-8''%s`, asciiName, url.PathEscape(fileName)))
}

func (a *App) handleClientWS(w http.ResponseWriter, r *http.Request) {
	serveWebSocket(a, w, r)
}
