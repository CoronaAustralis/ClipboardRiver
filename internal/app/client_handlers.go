package app

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
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
	DeviceUUID     string             `json:"device_uuid"`
	EnrollmentCode string             `json:"enrollment_code"`
	Nickname       string             `json:"nickname"`
	OSName         string             `json:"os_name"`
	OSVersion      string             `json:"os_version"`
	Platform       string             `json:"platform"`
	AppVersion     string             `json:"app_version"`
	Capabilities   clientCapabilities `json:"capabilities"`
}

type profileRequest struct {
	Nickname     string             `json:"nickname"`
	OSName       string             `json:"os_name"`
	OSVersion    string             `json:"os_version"`
	Platform     string             `json:"platform"`
	AppVersion   string             `json:"app_version"`
	Capabilities clientCapabilities `json:"capabilities"`
}

type textItemRequest struct {
	ClientItemID    string `json:"client_item_id"`
	UploadKind      string `json:"upload_kind"`
	ClientCreatedAt string `json:"client_created_at"`
	TextContent     string `json:"text_content"`
}

type textBatchRequest struct {
	Items []textItemRequest `json:"items"`
}

func (a *App) handleClientHeartbeat(w http.ResponseWriter, r *http.Request) {
	device := currentDevice(r)
	if device == nil {
		writeError(w, http.StatusUnauthorized, "device not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":        true,
		"server_at": time.Now().UTC(),
		"settings":  a.settingsSnapshot(),
	})
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

	var token model.EnrollmentToken
	if err := a.db.Where("code_hash = ?", auth.HashToken(req.EnrollmentCode)).First(&token).Error; err != nil {
		writeError(w, http.StatusUnauthorized, "invalid enrollment code")
		return
	}
	now := time.Now().UTC()
	if resolveEnrollmentTokenStatus(token, now) != enrollmentTokenStatusActive {
		writeError(w, http.StatusUnauthorized, "enrollment code is not usable")
		return
	}

	rawDeviceToken, err := auth.RandomToken(24)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not issue device token")
		return
	}

	var device model.Device
	err = a.db.Transaction(func(tx *gorm.DB) error {
		err := tx.Where("account_id = ? AND device_uuid = ?", a.account.ID, req.DeviceUUID).First(&device).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if errors.Is(err, gorm.ErrRecordNotFound) {
			device = model.Device{
				AccountID:              a.account.ID,
				DeviceUUID:             req.DeviceUUID,
				SendRealtimeEnabled:    true,
				ReceiveRealtimeEnabled: true,
			}
		}

		device.Nickname = strings.TrimSpace(req.Nickname)
		device.OSName = strings.TrimSpace(req.OSName)
		device.OSVersion = strings.TrimSpace(req.OSVersion)
		device.Platform = strings.TrimSpace(req.Platform)
		device.AppVersion = strings.TrimSpace(req.AppVersion)
		device.CapabilitiesJSON = encodeCapabilities(req.Capabilities)
		device.DeviceTokenHash = auth.HashToken(rawDeviceToken)
		device.Disabled = false
		device.LastSeenAt = &now
		if err := tx.Save(&device).Error; err != nil {
			return err
		}

		token.UsedCount++
		token.LastUsedAt = &now
		return tx.Save(&token).Error
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "register device failed")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"device_id":     device.ID,
		"device_token":  rawDeviceToken,
		"settings":      a.settingsSnapshot(),
		"server_cursor": 0,
	})
}

func (a *App) handleClientProfile(w http.ResponseWriter, r *http.Request) {
	device := currentDevice(r)
	if device == nil {
		writeError(w, http.StatusUnauthorized, "device not found")
		return
	}

	var req profileRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json payload")
		return
	}

	now := time.Now().UTC()
	updates := map[string]any{
		"nickname":          strings.TrimSpace(req.Nickname),
		"os_name":           strings.TrimSpace(req.OSName),
		"os_version":        strings.TrimSpace(req.OSVersion),
		"platform":          strings.TrimSpace(req.Platform),
		"app_version":       strings.TrimSpace(req.AppVersion),
		"capabilities_json": encodeCapabilities(req.Capabilities),
		"last_seen_at":      now,
	}
	if err := a.db.Model(&model.Device{}).Where("id = ?", device.ID).Updates(updates).Error; err != nil {
		writeError(w, http.StatusInternalServerError, "update profile failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *App) handleClientTextItem(w http.ResponseWriter, r *http.Request) {
	device := currentDevice(r)
	if device == nil {
		writeError(w, http.StatusUnauthorized, "device not found")
		return
	}

	var req textItemRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json payload")
		return
	}
	item, created, err := a.createTextClipboardItem(device, req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"created": created, "item": formatItemResponse(item)})
}

func (a *App) handleClientTextBatch(w http.ResponseWriter, r *http.Request) {
	device := currentDevice(r)
	if device == nil {
		writeError(w, http.StatusUnauthorized, "device not found")
		return
	}

	var req textBatchRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json payload")
		return
	}
	if len(req.Items) > a.cfg.Sync.TextBatchLimit {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("batch limit exceeded, max=%d", a.cfg.Sync.TextBatchLimit))
		return
	}

	results := make([]map[string]any, 0, len(req.Items))
	for _, entry := range req.Items {
		item, created, err := a.createTextClipboardItem(device, entry)
		result := map[string]any{
			"client_item_id": entry.ClientItemID,
			"created":        created,
		}
		if err != nil {
			result["error"] = err.Error()
		} else {
			result["item"] = formatItemResponse(item)
		}
		results = append(results, result)
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": results})
}

func (a *App) createTextClipboardItem(device *model.Device, req textItemRequest) (model.ClipboardItem, bool, error) {
	if strings.TrimSpace(req.ClientItemID) == "" {
		return model.ClipboardItem{}, false, errors.New("client_item_id is required")
	}
	uploadKind := strings.TrimSpace(req.UploadKind)
	if uploadKind == "" {
		uploadKind = model.UploadKindRealtime
	}
	if uploadKind != model.UploadKindRealtime && uploadKind != model.UploadKindHistory {
		return model.ClipboardItem{}, false, errors.New("invalid upload_kind")
	}

	item := model.ClipboardItem{
		ClientItemID:    strings.TrimSpace(req.ClientItemID),
		ContentKind:     model.ContentKindText,
		UploadKind:      uploadKind,
		TextContent:     req.TextContent,
		CharCount:       utf8.RuneCountInString(req.TextContent),
		ByteSize:        int64(len([]byte(req.TextContent))),
		ClientCreatedAt: clientTimeOrNow(req.ClientCreatedAt),
	}
	return a.createClipboardItem(device, item)
}

func (a *App) handleClientImageItem(w http.ResponseWriter, r *http.Request) {
	device := currentDevice(r)
	if device == nil {
		writeError(w, http.StatusUnauthorized, "device not found")
		return
	}
	if err := r.ParseMultipartForm(a.currentAccount().ImageMaxBytes + (1 << 20)); err != nil {
		writeError(w, http.StatusBadRequest, "invalid multipart form")
		return
	}

	req, file, header, err := parseImageMultipart(r.MultipartForm)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	defer file.Close()

	data, err := io.ReadAll(io.LimitReader(file, a.currentAccount().ImageMaxBytes+1))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "read image failed")
		return
	}
	if int64(len(data)) > a.currentAccount().ImageMaxBytes {
		writeError(w, http.StatusBadRequest, "image exceeds max size")
		return
	}

	detectedMime := http.DetectContentType(data)
	if !strings.HasPrefix(detectedMime, "image/") {
		writeError(w, http.StatusBadRequest, "uploaded file is not an image")
		return
	}
	if _, _, err := image.DecodeConfig(bytes.NewReader(data)); err != nil {
		writeError(w, http.StatusBadRequest, "invalid image data")
		return
	}

	blobPath, err := a.blobStore.Save(data, header.Filename, chooseBlobExtension(req.MimeType, detectedMime, header))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "store image failed")
		return
	}

	uploadKind := req.UploadKind
	if uploadKind == "" {
		uploadKind = model.UploadKindRealtime
	}
	item := model.ClipboardItem{
		ClientItemID:    req.ClientItemID,
		ContentKind:     model.ContentKindImage,
		UploadKind:      uploadKind,
		MimeType:        firstNonEmpty(req.MimeType, detectedMime),
		BlobPath:        blobPath,
		ByteSize:        int64(len(data)),
		ClientCreatedAt: clientTimeOrNow(req.ClientCreatedAt),
	}
	item, created, err := a.createClipboardItem(device, item)
	if err != nil {
		_ = a.blobStore.Delete(blobPath)
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"created": created, "item": formatItemResponse(item)})
}

type imageMultipartRequest struct {
	ClientItemID    string
	UploadKind      string
	ClientCreatedAt string
	MimeType        string
}

func parseImageMultipart(form *multipart.Form) (imageMultipartRequest, multipart.File, *multipart.FileHeader, error) {
	req := imageMultipartRequest{
		ClientItemID:    strings.TrimSpace(firstFormValue(form.Value["client_item_id"])),
		UploadKind:      strings.TrimSpace(firstFormValue(form.Value["upload_kind"])),
		ClientCreatedAt: strings.TrimSpace(firstFormValue(form.Value["client_created_at"])),
		MimeType:        strings.TrimSpace(firstFormValue(form.Value["mime_type"])),
	}
	if req.ClientItemID == "" {
		return req, nil, nil, errors.New("client_item_id is required")
	}
	if req.UploadKind != "" && req.UploadKind != model.UploadKindRealtime && req.UploadKind != model.UploadKindHistory {
		return req, nil, nil, errors.New("invalid upload_kind")
	}
	files := form.File["file"]
	if len(files) != 1 {
		return req, nil, nil, errors.New("exactly one file is required")
	}
	file, err := files[0].Open()
	if err != nil {
		return req, nil, nil, fmt.Errorf("open file: %w", err)
	}
	return req, file, files[0], nil
}

func firstFormValue(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func chooseBlobExtension(requested, detected string, header *multipart.FileHeader) string {
	if ext := filepath.Ext(header.Filename); ext != "" {
		return ext
	}
	if ext := extByMime(firstNonEmpty(requested, detected)); ext != "" {
		return ext
	}
	return ".bin"
}

func extByMime(mime string) string {
	switch mime {
	case "image/png":
		return ".png"
	case "image/jpeg":
		return ".jpg"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	default:
		return ""
	}
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
	id, err := strconv.ParseUint(r.PathValue("id"), 10, 64)
	if err != nil || id == 0 {
		http.NotFound(w, r)
		return
	}

	var item model.ClipboardItem
	if err := a.db.First(&item, id).Error; err != nil {
		http.NotFound(w, r)
		return
	}

	if !a.isAdminAuthenticated(r) {
		device, err := a.authenticateDevice(r)
		if err != nil || device.AccountID != item.AccountID {
			writeError(w, http.StatusUnauthorized, "invalid device token")
			return
		}
	}
	setInlineContentDisposition(w, fileNameFromPath(item.BlobPath))
	http.ServeFile(w, r, item.BlobPath)
}

func setInlineContentDisposition(w http.ResponseWriter, fileName string) {
	fileName = strings.TrimSpace(fileName)
	if fileName == "" {
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
