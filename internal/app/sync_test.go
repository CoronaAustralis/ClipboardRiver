package app

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/clipboardriver/cb_river_server/internal/model"
)

func TestTextWindowHistoryNotificationAndPermissions(t *testing.T) {
	app := newTestApp(t)
	defer app.Close()
	app.cfg.Sync.TextBatchLimit = 100
	handler := app.Router()
	code := openEnrollment(t, app)
	sourceToken := registerDevice(t, handler, app, code, "sync-source", "Source")
	targetToken := registerDevice(t, handler, app, code, "sync-target", "Target")
	server := httptest.NewServer(handler)
	defer server.Close()
	conn := connectClientWS(t, server.URL, targetToken, "sync-target")
	defer conn.Close()
	entries := make([]map[string]any, 0, 56)
	base := time.Now().UTC().Add(-time.Hour)
	for i := 0; i < 55; i++ {
		entries = append(entries, map[string]any{"text_content": fmt.Sprint(i), "upload_kind": "history", "client_created_at": base.Add(time.Duration(i) * time.Second).Format(time.RFC3339)})
	}
	entries = append(entries, map[string]any{"text_content": ""})
	response := performJSON(t, handler, "POST", "/api/v1/client/items/text/batch", map[string]any{"items": entries}, sourceToken)
	if response.Code != http.StatusOK {
		t.Fatalf("batch: %d %s", response.Code, response.Body)
	}
	var event struct {
		Type    string `json:"type"`
		Payload struct {
			ItemIDs []uint `json:"item_ids"`
		} `json:"payload"`
	}
	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	if err := conn.ReadJSON(&event); err != nil {
		t.Fatal(err)
	}
	if event.Type != "clipboard.sync_changed" || len(event.Payload.ItemIDs) != 50 {
		t.Fatalf("notification: %+v", event)
	}
	type syncResponse struct {
		ItemIDs []uint           `json:"item_ids"`
		Items   []map[string]any `json:"items"`
	}
	sync := func(token string, ids []uint, content bool) syncResponse {
		t.Helper()
		response := performJSON(t, handler, "POST", "/api/v1/client/sync/text", map[string]any{"item_ids": ids, "include_content": content}, token)
		var result syncResponse
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &result) != nil || result.ItemIDs == nil || result.Items == nil {
			t.Fatalf("sync: %d %s", response.Code, response.Body)
		}
		return result
	}
	listing := sync(targetToken, []uint{}, false)
	if len(listing.ItemIDs) != 50 || len(listing.Items) != 0 || listing.ItemIDs[0] != event.Payload.ItemIDs[0] {
		t.Fatalf("listing: %+v", listing)
	}
	all := sync(targetToken, []uint{}, true)
	if len(all.Items) != 50 || all.Items[0]["text_content"] != "54" || all.Items[49]["text_content"] != "5" {
		t.Fatalf("window sorting: %+v", all)
	}
	selected := []uint{listing.ItemIDs[1], listing.ItemIDs[0], listing.ItemIDs[1], 999999}
	subset := sync(targetToken, selected, true)
	if len(subset.ItemIDs) != 2 || len(subset.Items) != 2 || subset.ItemIDs[0] != selected[0] || subset.Items[0]["item_id"] != float64(selected[0]) {
		t.Fatalf("subset: %+v", subset)
	}
	if result := sync(targetToken, selected, false); len(result.ItemIDs) != 2 || len(result.Items) != 0 {
		t.Fatalf("subset without contents: %+v", result)
	}
	response = performJSON(t, handler, "POST", "/api/v1/client/items/text", map[string]any{"text_content": "ancient high ID", "upload_kind": "history", "client_created_at": "2000-01-01T00:00:00Z"}, sourceToken)
	if response.Code != http.StatusOK {
		t.Fatal(response.Body)
	}
	if result := sync(targetToken, []uint{}, true); result.Items[0]["text_content"] != "54" || result.Items[49]["text_content"] != "5" {
		t.Fatal("higher database ID displaced newer text")
	}
	var source, target model.Device
	if err := app.db.Where("device_uuid = ?", "sync-source").First(&source).Error; err != nil {
		t.Fatal(err)
	}
	if err := app.db.Where("device_uuid = ?", "sync-target").First(&target).Error; err != nil {
		t.Fatal(err)
	}
	performAdminPost(t, handler, app, "/admin/devices/"+strconv.Itoa(int(source.ID))+"/toggle-send")
	if err := conn.ReadJSON(&event); err != nil {
		t.Fatal(err)
	}
	if event.Type != "clipboard.sync_changed" || len(event.Payload.ItemIDs) != 0 {
		t.Fatalf("source permission notification: %+v", event)
	}
	if result := sync(targetToken, selected, true); len(result.ItemIDs) != 0 || len(result.Items) != 0 {
		t.Fatal("source permission bypassed")
	}
	if result := sync(sourceToken, []uint{}, false); len(result.ItemIDs) != 50 {
		t.Fatal("sending disabled should not hide own items")
	}
	performAdminPost(t, handler, app, "/admin/devices/"+strconv.Itoa(int(target.ID))+"/toggle-receive")
	for _, ids := range [][]uint{{}, selected} {
		for _, content := range []bool{false, true} {
			if result := sync(targetToken, ids, content); len(result.ItemIDs) != 0 || len(result.Items) != 0 {
				t.Fatal("receive permission bypassed")
			}
		}
	}
	response = performJSON(t, handler, "POST", "/api/v1/client/items/text", map[string]any{"text_content": "hidden history", "upload_kind": "history"}, sourceToken)
	if response.Code != http.StatusOK {
		t.Fatal(response.Body)
	}
	_ = conn.SetReadDeadline(time.Now().Add(250 * time.Millisecond))
	if _, _, err := conn.ReadMessage(); err == nil {
		t.Fatal("unexpected extra notification or notification without receive permission")
	} else if netErr, ok := err.(net.Error); !ok || !netErr.Timeout() {
		t.Fatal(err)
	}
}

func TestTextWindowRebuildAndExpiryRefill(t *testing.T) {
	app := newTestApp(t)
	handler := app.Router()
	token := registerDevice(t, handler, app, openEnrollment(t, app), "expiry-source", "Source")
	var source model.Device
	if err := app.db.Where("device_uuid = ?", "expiry-source").First(&source).Error; err != nil {
		t.Fatal(err)
	}
	app.stateMu.Lock()
	base := time.Now().UTC().Add(-time.Hour)
	entries := make([]model.ClipboardItem, 52)
	for i := range entries {
		entries[i] = model.ClipboardItem{AccountID: app.account.ID, SourceDeviceID: source.ID, ContentKind: model.ContentKindText, UploadKind: model.UploadKindHistory, TextContent: fmt.Sprint(i), ClientCreatedAt: base.Add(time.Duration(i) * time.Second), ReceivedAt: base}
	}
	expiry := time.Now().UTC().Add(-time.Second)
	entries[51].ExpiresAt = &expiry
	if err := app.db.Create(&entries).Error; err != nil {
		app.stateMu.Unlock()
		t.Fatal(err)
	}
	if _, err := app.refreshTextWindowLocked(); err != nil {
		app.stateMu.Unlock()
		t.Fatal(err)
	}
	// Simulate a cached item reaching its expiry between hourly cleanup runs.
	app.textWindow[0].ExpiresAt = &expiry
	if err := app.db.Model(&model.ClipboardItem{}).Where("id = ?", app.textWindow[0].ID).Update("expires_at", expiry).Error; err != nil {
		app.stateMu.Unlock()
		t.Fatal(err)
	}
	app.stateMu.Unlock()
	response := performJSON(t, handler, "POST", "/api/v1/client/sync/text", map[string]any{"item_ids": []uint{}, "include_content": true}, token)
	var result struct {
		Items []map[string]any `json:"items"`
	}
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &result) != nil || len(result.Items) != 50 || result.Items[0]["text_content"] != "49" || result.Items[49]["text_content"] != "0" {
		t.Fatalf("expiry: %d %s", response.Code, response.Body)
	}
	cfg := app.cfg
	if err := app.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	if restarted.enrollment.snapshot().Open {
		t.Fatal("restart restored enrollment")
	}
	response = performJSON(t, restarted.Router(), "POST", "/api/v1/client/sync/text", map[string]any{"item_ids": []uint{}, "include_content": true}, token)
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &result) != nil || len(result.Items) != 50 {
		t.Fatalf("restart: %d %s", response.Code, response.Body)
	}
}

func TestFileUploadLimitModes(t *testing.T) {
	app := newTestApp(t)
	defer app.Close()
	handler := app.Router()
	token := registerDevice(t, handler, app, openEnrollment(t, app), "file-limits", "Files")
	for _, mode := range []struct {
		limit  int64
		size   int
		status int
	}{{3, 4, 400}, {3, 3, 200}, {0, 4096, 200}, {-1, 1, 400}} {
		response := performAdminFormPost(t, handler, app, "/admin/settings", map[string][]string{"retention_days": {"30"}, "file_max_bytes": {fmt.Sprint(mode.limit)}})
		if response.Code != http.StatusFound {
			t.Fatal(response.Body)
		}
		upload := performMultipartFileUpload(t, handler, "/api/v1/client/items/file", token, nil, "data.bin", make([]byte, mode.size))
		if upload.Code != mode.status {
			t.Fatalf("limit %d size %d: %d %s", mode.limit, mode.size, upload.Code, upload.Body)
		}
	}
	var count int64
	if err := app.db.Model(&model.ClipboardItem{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("rejected files created records: %d", count)
	}
}
