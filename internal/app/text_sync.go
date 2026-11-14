package app

import (
	"errors"
	"net/http"
	"time"

	"github.com/clipboardriver/cb_river_server/internal/model"
)

const textWindowLimit = 50

// All window reads and publications share stateMu with writes and permission changes.
func (a *App) refreshTextWindowLocked() (bool, error) {
	var items []model.ClipboardItem
	err := a.db.Where("account_id = ? AND content_kind = ? AND (expires_at IS NULL OR expires_at > ?)", a.account.ID, model.ContentKindText, time.Now().UTC()).
		Order("client_created_at DESC, id DESC").Limit(textWindowLimit).Find(&items).Error
	a.windowErr = err
	if err != nil {
		return false, err
	}
	changed := len(items) != len(a.textWindow)
	if !changed {
		for i := range items {
			if items[i].ID != a.textWindow[i].ID {
				changed = true
				break
			}
		}
	}
	a.textWindow = items
	return changed, nil
}

func (a *App) ensureTextWindowLocked() error {
	refresh := a.windowErr != nil
	now := time.Now()
	for _, item := range a.textWindow {
		if item.ExpiresAt != nil && !now.Before(*item.ExpiresAt) {
			refresh = true
			break
		}
	}
	if !refresh {
		return nil
	}
	changed, err := a.refreshTextWindowLocked()
	if err == nil && changed {
		a.notifySyncChangedLocked()
	}
	return err
}

func (a *App) visibleTextItemsLocked(device *model.Device) ([]model.ClipboardItem, error) {
	result := make([]model.ClipboardItem, 0, len(a.textWindow))
	if device.Deleted || !device.ReceiveRealtimeEnabled {
		return result, nil
	}
	var sources []model.Device
	if err := a.db.Where("account_id = ? AND deleted = ?", device.AccountID, false).Find(&sources).Error; err != nil {
		return nil, err
	}
	allowed := make(map[uint]bool, len(sources))
	for _, source := range sources {
		allowed[source.ID] = source.SendRealtimeEnabled
	}
	for _, item := range a.textWindow {
		if item.AccountID == device.AccountID && (item.SourceDeviceID == device.ID || allowed[item.SourceDeviceID]) {
			result = append(result, item)
		}
	}
	return result, nil
}

func (a *App) notifySyncChangedLocked() {
	var targets []model.Device
	if err := a.db.Where("account_id = ? AND deleted = ? AND receive_realtime_enabled = ?", a.account.ID, false, true).Find(&targets).Error; err != nil {
		return
	}
	for _, target := range targets {
		a.hub.Push(target.ID, wsMessage{Type: "clipboard.sync_changed"})
	}
}

func (a *App) handleClientTextSync(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ItemIDs        *[]uint `json:"item_ids"`
		IncludeContent *bool   `json:"include_content"`
	}
	if err := decodeJSON(r, &req); err != nil || req.ItemIDs == nil || req.IncludeContent == nil {
		writeError(w, http.StatusBadRequest, "item_ids array and include_content boolean are required")
		return
	}
	for _, id := range *req.ItemIDs {
		if id == 0 {
			writeError(w, http.StatusBadRequest, "item_ids must contain positive integers")
			return
		}
	}
	ids := make([]uint, 0)
	contents := make([]map[string]any, 0)
	device := currentDevice(r)
	if !device.ReceiveRealtimeEnabled {
		writeJSON(w, http.StatusOK, map[string]any{"item_ids": ids, "items": contents})
		return
	}
	if err := a.ensureTextWindowLocked(); err != nil {
		writeError(w, http.StatusInternalServerError, "text window unavailable")
		return
	}
	items, err := a.visibleTextItemsLocked(device)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "text window unavailable")
		return
	}
	if len(*req.ItemIDs) > 0 {
		byID := make(map[uint]model.ClipboardItem, len(items))
		for _, item := range items {
			byID[item.ID] = item
		}
		items = make([]model.ClipboardItem, 0)
		for _, id := range *req.ItemIDs {
			if item, ok := byID[id]; ok {
				items = append(items, item)
				delete(byID, id)
			}
		}
	}
	for _, item := range items {
		ids = append(ids, item.ID)
		if *req.IncludeContent {
			contents = append(contents, formatItemResponse(item))
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"item_ids": ids, "items": contents})
}

func normalizeUploadKind(value string) (string, error) {
	if value == "" {
		return model.UploadKindRealtime, nil
	}
	if value != model.UploadKindRealtime && value != model.UploadKindHistory {
		return "", errors.New("invalid upload_kind")
	}
	return value, nil
}
