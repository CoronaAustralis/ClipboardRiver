package app

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/clipboardriver/cb_river_server/internal/auth"
	"github.com/clipboardriver/cb_river_server/internal/config"
	"github.com/clipboardriver/cb_river_server/internal/model"
	"github.com/gorilla/websocket"
	"golang.org/x/crypto/bcrypt"
)

func TestRegisterSameDeviceUpdatesMetadataAndRevokesOldSessions(t *testing.T) {
	app := newTestApp(t)
	defer app.Close()
	handler := app.Router()
	server := httptest.NewServer(handler)
	defer server.Close()
	code := openEnrollment(t, app)
	oldToken := registerDevice(t, handler, app, code, "device-1", "Desk")
	conn := connectClientWS(t, server.URL, oldToken, "device-1")
	defer conn.Close()
	response := performJSON(t, handler, "POST", "/api/v1/client/register", map[string]any{
		"device_uuid": "device-1", "enrollment_code": code, "app_name": "Custom Client", "app_version": "2.0.0",
	}, "")
	if response.Code != http.StatusOK {
		t.Fatalf("register: %d %s", response.Code, response.Body)
	}
	var devices []model.Device
	if err := app.db.Find(&devices).Error; err != nil {
		t.Fatal(err)
	}
	if len(devices) != 1 || devices[0].Nickname != "Desk" || devices[0].AppName != "Custom Client" || devices[0].AppVersion != "2.0.0" {
		t.Fatalf("unexpected metadata: %+v", devices)
	}
	if response := performJSON(t, handler, "POST", "/api/v1/client/device/profile", nil, oldToken); response.Code != http.StatusUnauthorized {
		t.Fatalf("old token still works: %d", response.Code)
	}
	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	if _, _, err := conn.ReadMessage(); err == nil {
		t.Fatal("old authenticated WebSocket must close")
	}
}

func TestAdminLoginPageSupportsEnglishAndPersistsCookie(t *testing.T) {
	app := newTestApp(t)
	defer func() { _ = app.Close() }()

	req := httptest.NewRequest(http.MethodGet, "/admin/login?lang=en", nil)
	recorder := httptest.NewRecorder()
	app.Router().ServeHTTP(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("login page status = %d, want %d", recorder.Code, http.StatusOK)
	}
	body := recorder.Body.String()
	if !strings.Contains(body, "Server admin console login") || !strings.Contains(body, "Login") {
		t.Fatalf("expected english login page, body=%s", body)
	}

	foundLangCookie := false
	for _, cookie := range recorder.Result().Cookies() {
		if cookie.Name == langCookieName && cookie.Value == langEN {
			foundLangCookie = true
			break
		}
	}
	if !foundLangCookie {
		t.Fatalf("expected %s cookie to be set to %s", langCookieName, langEN)
	}
}

func TestAdminLoginPageDefaultsToChinese(t *testing.T) {
	app := newTestApp(t)
	defer func() { _ = app.Close() }()

	req := httptest.NewRequest(http.MethodGet, "/admin/login", nil)
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8")
	recorder := httptest.NewRecorder()
	app.Router().ServeHTTP(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("login page status = %d, want %d", recorder.Code, http.StatusOK)
	}
	body := recorder.Body.String()
	if !strings.Contains(body, "服务端管理后台登录") || !strings.Contains(body, "登录") {
		t.Fatalf("expected chinese login page, body=%s", body)
	}
}

func TestAdminLoginCreatesThirtyDaySession(t *testing.T) {
	app := newTestApp(t)
	defer func() { _ = app.Close() }()

	form := url.Values{
		"username": {"admin"},
		"password": {"password"},
	}
	req := httptest.NewRequest(http.MethodPost, "/admin/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	recorder := httptest.NewRecorder()
	before := time.Now()
	app.Router().ServeHTTP(recorder, req)

	if recorder.Code != http.StatusFound {
		t.Fatalf("login status = %d, want %d", recorder.Code, http.StatusFound)
	}

	var sessionCookie *http.Cookie
	for _, cookie := range recorder.Result().Cookies() {
		if cookie.Name == adminSessionCookie {
			sessionCookie = cookie
			break
		}
	}
	if sessionCookie == nil {
		t.Fatalf("expected %s cookie to be set", adminSessionCookie)
	}

	remaining := sessionCookie.Expires.Sub(before)
	if remaining < 29*24*time.Hour || remaining > 31*24*time.Hour {
		t.Fatalf("session cookie lifetime = %s, want about 30 days", remaining)
	}
}

func TestNewAppGeneratesInitialAdminPassword(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Config{
		Server: config.ServerConfig{
			ListenAddr: ":0",
		},
		Storage: config.StorageConfig{
			Driver:  "sqlite",
			DSN:     filepath.Join(dir, "bootstrap.db"),
			DataDir: dir,
			BlobDir: filepath.Join(dir, "blobs"),
		},
		Auth: config.AuthConfig{
			SessionSecret: "test-session-secret",
		},
		Sync: config.SyncConfig{
			DefaultRetentionDays: 30,
			FileMaxBytes:         2 * 1024 * 1024,
			TextBatchLimit:       20,
		},
		Admin: config.AdminBootstrap{
			Username: "admin",
		},
	}

	app, err := New(cfg)
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	defer func() { _ = app.Close() }()

	username, password, ok := app.InitialAdminCredentials()
	if !ok {
		t.Fatalf("expected initial admin credentials to be generated")
	}
	if username != "admin" {
		t.Fatalf("username = %q, want %q", username, "admin")
	}
	if got := len(password); got != 10 {
		t.Fatalf("generated password length = %d, want 10", got)
	}

	var admin model.AdminUser
	if err := app.db.Where("username = ?", username).First(&admin).Error; err != nil {
		t.Fatalf("find admin user: %v", err)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(admin.PasswordHash), []byte(password)); err != nil {
		t.Fatalf("generated password does not match stored hash: %v", err)
	}
}

func TestProfileAndNonIdempotentTextUpload(t *testing.T) {
	app := newTestApp(t)
	defer app.Close()
	handler := app.Router()
	token := registerDevice(t, handler, app, openEnrollment(t, app), "source-1", "Phone")
	response := performJSON(t, handler, "POST", "/api/v1/client/device/profile", map[string]any{"app_name": "Updated App", "app_version": "2.0.0"}, token)
	if response.Code != http.StatusOK {
		t.Fatalf("profile: %d %s", response.Code, response.Body)
	}
	for _, body := range []any{nil, map[string]any{}} {
		response = performJSON(t, handler, "POST", "/api/v1/client/device/profile", body, token)
		var profile struct {
			DeviceID uint           `json:"device_id"`
			Settings map[string]any `json:"settings"`
		}
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &profile) != nil || profile.DeviceID == 0 || len(profile.Settings) != 2 {
			t.Fatalf("profile lookup: %d %s", response.Code, response.Body)
		}
	}
	response = performJSON(t, handler, "POST", "/api/v1/client/device/profile", map[string]any{"nickname": "cannot change"}, token)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("profile accepted nickname: %d", response.Code)
	}
	var device model.Device
	if err := app.db.Where("device_uuid = ?", "source-1").First(&device).Error; err != nil {
		t.Fatal(err)
	}
	if device.Nickname != "Phone" || device.AppName != "Updated App" || device.AppVersion != "2.0.0" {
		t.Fatalf("profile metadata: %+v", device)
	}
	body := map[string]any{"text_content": "hello world", "client_created_at": time.Now().UTC().Format(time.RFC3339)}
	for i := 0; i < 2; i++ {
		response = performJSON(t, handler, "POST", "/api/v1/client/items/text", body, token)
		var receipt struct {
			Created bool           `json:"created"`
			Item    map[string]any `json:"item"`
		}
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &receipt) != nil || !receipt.Created || len(receipt.Item) != 5 || receipt.Item["item_id"] == nil {
			t.Fatalf("receipt: %d %s", response.Code, response.Body)
		}
	}
	var count int64
	if err := app.db.Model(&model.ClipboardItem{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("expected a new row for each upload, got %d", count)
	}
	body["client_created_at"] = "invalid"
	response = performJSON(t, handler, "POST", "/api/v1/client/items/text", body, token)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("invalid timestamp accepted: %d", response.Code)
	}
}

func TestRealtimeWebSocketDeliveryDoesNotReplayMissedItems(t *testing.T) {
	app := newTestApp(t)
	defer func() { _ = app.Close() }()
	handler := app.Router()

	server := httptest.NewServer(handler)
	defer server.Close()

	code := openEnrollment(t, app)
	sourceToken := registerDevice(t, handler, app, code, "ws-source-1", "Source")
	targetToken := registerDevice(t, handler, app, code, "ws-target-1", "Target")

	missedUpload := performJSON(t, handler, "POST", "/api/v1/client/items/text", map[string]any{

		"upload_kind":       "realtime",
		"client_created_at": time.Now().UTC().Format(time.RFC3339),
		"text_content":      "missed while offline",
	}, sourceToken)
	if missedUpload.Code != http.StatusOK {
		t.Fatalf("missed upload status = %d, body=%s", missedUpload.Code, missedUpload.Body.String())
	}

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/v1/client/ws"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}

	if err := conn.WriteJSON(map[string]any{
		"type": "hello",
		"payload": map[string]any{
			"device_token": targetToken,
			"device_uuid":  "ws-target-1",
		},
	}); err != nil {
		t.Fatalf("write hello: %v", err)
	}

	var hello map[string]any
	if err := conn.ReadJSON(&hello); err != nil {
		t.Fatalf("read hello_ack: %v", err)
	}
	if hello["type"] != "hello_ack" {
		t.Fatalf("expected hello_ack, got %#v", hello)
	}

	conn.SetReadDeadline(time.Now().Add(250 * time.Millisecond))
	if _, _, err := conn.ReadMessage(); err == nil {
		t.Fatalf("expected no replay message for missed item")
	} else if netErr, ok := err.(net.Error); !ok || !netErr.Timeout() {
		t.Fatalf("expected timeout waiting for replay, got %v", err)
	}
	_ = conn.Close()

	conn, _, err = websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("redial websocket: %v", err)
	}
	defer conn.Close()
	if err := conn.WriteJSON(map[string]any{
		"type": "hello",
		"payload": map[string]any{
			"device_token": targetToken,
			"device_uuid":  "ws-target-1",
		},
	}); err != nil {
		t.Fatalf("write second hello: %v", err)
	}
	hello = map[string]any{}
	if err := conn.ReadJSON(&hello); err != nil {
		t.Fatalf("read second hello_ack: %v", err)
	}
	if hello["type"] != "hello_ack" {
		t.Fatalf("expected second hello_ack, got %#v", hello)
	}

	liveUpload := performJSON(t, handler, "POST", "/api/v1/client/items/text", map[string]any{

		"upload_kind":       "realtime",
		"client_created_at": time.Now().UTC().Format(time.RFC3339),
		"text_content":      "delivered live",
	}, sourceToken)
	if liveUpload.Code != http.StatusOK {
		t.Fatalf("live upload status = %d, body=%s", liveUpload.Code, liveUpload.Body.String())
	}

	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	var push map[string]any
	if err := conn.ReadJSON(&push); err != nil {
		t.Fatalf("read live push: %v", err)
	}
	if push["type"] != "clipboard.created" {
		t.Fatalf("expected clipboard.created, got %#v", push)
	}
	item, ok := push["item"].(map[string]any)
	if !ok {
		t.Fatalf("expected item payload map, got %#v", push["item"])
	}
	if item["text_content"] != "delivered live" || item["item_id"] == nil {
		t.Fatalf("expected live-1 push, got %#v", item)
	}
}

func TestToggleSendStopsRealtimeFanout(t *testing.T) {
	app := newTestApp(t)
	defer func() { _ = app.Close() }()
	handler := app.Router()

	server := httptest.NewServer(handler)
	defer server.Close()

	code := openEnrollment(t, app)
	sourceToken := registerDevice(t, handler, app, code, "toggle-send-source", "Source")
	targetToken := registerDevice(t, handler, app, code, "toggle-send-target", "Target")

	var source model.Device
	if err := app.db.Where("device_uuid = ?", "toggle-send-source").First(&source).Error; err != nil {
		t.Fatalf("find source device: %v", err)
	}

	conn := connectClientWS(t, server.URL, targetToken, "toggle-send-target")
	defer conn.Close()

	resp := performAdminPost(t, handler, app, "/admin/devices/"+strconv.Itoa(int(source.ID))+"/toggle-send")
	if resp.Code != http.StatusFound {
		t.Fatalf("toggle send status = %d, want %d", resp.Code, http.StatusFound)
	}

	var updated model.Device
	if err := app.db.First(&updated, source.ID).Error; err != nil {
		t.Fatalf("reload source device: %v", err)
	}
	if updated.SendRealtimeEnabled {
		t.Fatalf("send toggle did not persist")
	}

	uploadResp := performJSON(t, handler, "POST", "/api/v1/client/items/text", map[string]any{

		"upload_kind":       "realtime",
		"client_created_at": time.Now().UTC().Format(time.RFC3339),
		"text_content":      "should not fan out",
	}, sourceToken)
	if uploadResp.Code != http.StatusOK {
		t.Fatalf("upload status = %d, body=%s", uploadResp.Code, uploadResp.Body.String())
	}

	conn.SetReadDeadline(time.Now().Add(250 * time.Millisecond))
	if _, _, err := conn.ReadMessage(); err == nil {
		t.Fatalf("expected no realtime push when source send is disabled")
	} else if netErr, ok := err.(net.Error); !ok || !netErr.Timeout() {
		t.Fatalf("expected timeout waiting for suppressed push, got %v", err)
	}
}

func TestToggleReceiveStopsRealtimeFanout(t *testing.T) {
	app := newTestApp(t)
	defer func() { _ = app.Close() }()
	handler := app.Router()

	server := httptest.NewServer(handler)
	defer server.Close()

	code := openEnrollment(t, app)
	sourceToken := registerDevice(t, handler, app, code, "toggle-recv-source", "Source")
	targetToken := registerDevice(t, handler, app, code, "toggle-recv-target", "Target")

	var target model.Device
	if err := app.db.Where("device_uuid = ?", "toggle-recv-target").First(&target).Error; err != nil {
		t.Fatalf("find target device: %v", err)
	}

	conn := connectClientWS(t, server.URL, targetToken, "toggle-recv-target")
	defer conn.Close()

	resp := performAdminPost(t, handler, app, "/admin/devices/"+strconv.Itoa(int(target.ID))+"/toggle-receive")
	if resp.Code != http.StatusFound {
		t.Fatalf("toggle receive status = %d, want %d", resp.Code, http.StatusFound)
	}

	var updated model.Device
	if err := app.db.First(&updated, target.ID).Error; err != nil {
		t.Fatalf("reload target device: %v", err)
	}
	if updated.ReceiveRealtimeEnabled {
		t.Fatalf("receive toggle did not persist")
	}

	uploadResp := performJSON(t, handler, "POST", "/api/v1/client/items/text", map[string]any{

		"upload_kind":       "realtime",
		"client_created_at": time.Now().UTC().Format(time.RFC3339),
		"text_content":      "should not be delivered",
	}, sourceToken)
	if uploadResp.Code != http.StatusOK {
		t.Fatalf("upload status = %d, body=%s", uploadResp.Code, uploadResp.Body.String())
	}

	conn.SetReadDeadline(time.Now().Add(250 * time.Millisecond))
	if _, _, err := conn.ReadMessage(); err == nil {
		t.Fatalf("expected no realtime push when target receive is disabled")
	} else if netErr, ok := err.(net.Error); !ok || !netErr.Timeout() {
		t.Fatalf("expected timeout waiting for suppressed push, got %v", err)
	}
}

func TestDeleteDevicePreservesRecordAndHistory(t *testing.T) {
	app := newTestApp(t)
	defer app.Close()
	handler := app.Router()
	code := openEnrollment(t, app)
	token := registerDevice(t, handler, app, code, "delete-device-1", "Delete Me")
	observerToken := registerDevice(t, handler, app, code, "delete-observer", "Observer")
	upload := performJSON(t, handler, "POST", "/api/v1/client/items/text", map[string]any{"upload_kind": "history", "text_content": "retained source"}, token)
	if upload.Code != http.StatusOK {
		t.Fatalf("upload: %d %s", upload.Code, upload.Body)
	}
	var device model.Device
	var item model.ClipboardItem
	if err := app.db.Where("device_uuid = ?", "delete-device-1").First(&device).Error; err != nil {
		t.Fatal(err)
	}
	if err := app.db.Where("source_device_id = ?", device.ID).First(&item).Error; err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	conn := connectClientWS(t, server.URL, token, device.DeviceUUID)
	defer conn.Close()
	observer := connectClientWS(t, server.URL, observerToken, "delete-observer")
	defer observer.Close()
	response := performAdminPost(t, handler, app, "/admin/devices/"+strconv.Itoa(int(device.ID))+"/delete")
	if response.Code != http.StatusFound {
		t.Fatalf("delete: %d %s", response.Code, response.Body)
	}
	var deleted model.Device
	if err := app.db.First(&deleted, device.ID).Error; err != nil {
		t.Fatalf("soft-deleted row missing: %v", err)
	}
	if !deleted.Deleted || deleted.DeviceTokenHash != "" {
		t.Fatalf("device not revoked: %+v", deleted)
	}
	if err := app.db.First(&item, item.ID).Error; err != nil || item.SourceDeviceID != device.ID {
		t.Fatalf("history source was changed: %+v, %v", item, err)
	}
	if got := formatItemResponse(item)["source_device_id"]; got != device.ID {
		t.Fatalf("source ID should remain numeric: %#v", got)
	}
	for _, path := range []string{"/api/v1/client/device/profile", "/api/v1/client/items/text", "/api/v1/client/sync/text"} {
		if response := performJSON(t, handler, "POST", path, map[string]any{}, token); response.Code != http.StatusUnauthorized {
			t.Fatalf("deleted token accepted on %s: %d", path, response.Code)
		}
	}
	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	if _, _, err := conn.ReadMessage(); err == nil {
		t.Fatal("deleted device WebSocket remained open")
	} else if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
		t.Fatal("deleted device WebSocket was not closed")
	}
	var event struct {
		Type    string `json:"type"`
		Payload struct {
			ItemIDs []uint `json:"item_ids"`
		} `json:"payload"`
	}
	_ = observer.SetReadDeadline(time.Now().Add(time.Second))
	if err := observer.ReadJSON(&event); err != nil {
		t.Fatal(err)
	}
	if event.Type != "clipboard.sync_changed" || len(event.Payload.ItemIDs) != 0 {
		t.Fatalf("deleted source remains visible: %+v", event)
	}
	page := performAdminGet(t, handler, app, "/admin/devices", "en")
	if page.Code != http.StatusOK || strings.Contains(page.Body.String(), device.DeviceUUID) || strings.Contains(page.Body.String(), "toggle-disabled") {
		t.Fatal("deleted device or disable control remains in management")
	}
	response = performAdminPost(t, handler, app, "/admin/devices/"+strconv.Itoa(int(device.ID))+"/toggle-send")
	if !strings.Contains(response.Header().Get("Location"), "msg=update_failed") {
		t.Fatal("deleted device permissions remain editable")
	}
	restoredToken := registerDevice(t, handler, app, code, device.DeviceUUID, "Restored")
	var restored model.Device
	if err := app.db.Where("device_uuid = ?", device.DeviceUUID).First(&restored).Error; err != nil {
		t.Fatal(err)
	}
	if restored.ID != device.ID || restored.Deleted || !restored.SendRealtimeEnabled || restoredToken == token {
		t.Fatalf("identity not restored: %+v", restored)
	}
	var count int64
	if err := app.db.Model(&model.Device{}).Count(&count).Error; err != nil || count != 2 {
		t.Fatalf("registration created another device: %d, %v", count, err)
	}
	if response := performJSON(t, handler, "POST", "/api/v1/client/device/profile", nil, token); response.Code != http.StatusUnauthorized {
		t.Fatal("restoring device resurrected old token")
	}
	if response := performJSON(t, handler, "POST", "/api/v1/client/device/profile", nil, restoredToken); response.Code != http.StatusOK {
		t.Fatal("restored token rejected")
	}
	if err := observer.ReadJSON(&event); err != nil {
		t.Fatal(err)
	}
	if len(event.Payload.ItemIDs) != 1 || event.Payload.ItemIDs[0] != item.ID {
		t.Fatalf("restored source not synchronized: %+v", event)
	}
}

func TestHubOnlineStatus(t *testing.T) {
	hub := NewHub()
	device := &model.Device{ID: 42}
	client := &WSClient{
		device: device,
		send:   make(chan wsMessage, 1),
		done:   make(chan struct{}),
	}

	if hub.IsOnline(device.ID) {
		t.Fatalf("device should start offline")
	}
	hub.Register(client)
	if !hub.IsOnline(device.ID) {
		t.Fatalf("device should be online after register")
	}
	hub.Unregister(client)
	if hub.IsOnline(device.ID) {
		t.Fatalf("device should be offline after unregister")
	}
}

func TestTextBatchReturnsPartialResults(t *testing.T) {
	app := newTestApp(t)
	defer func() { _ = app.Close() }()

	code := openEnrollment(t, app)
	handler := app.Router()
	deviceToken := registerDevice(t, handler, app, code, "batch-1", "Batcher")

	resp := performJSON(t, handler, "POST", "/api/v1/client/items/text/batch", map[string]any{
		"items": []map[string]any{
			{

				"upload_kind":       "history",
				"client_created_at": time.Now().UTC().Format(time.RFC3339),
				"text_content":      "saved",
			},
			{

				"text_content": "",
			},
		},
	}, deviceToken)
	if resp.Code != http.StatusOK {
		t.Fatalf("batch status = %d, body=%s", resp.Code, resp.Body.String())
	}

	var payload struct {
		Results []map[string]any `json:"results"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode batch response: %v", err)
	}
	if got := len(payload.Results); got != 2 {
		t.Fatalf("batch results = %d, want 2", got)
	}
	if _, ok := payload.Results[0]["item"]; !ok {
		t.Fatalf("first batch result should succeed: %#v", payload.Results[0])
	}
	if _, ok := payload.Results[1]["error"]; !ok {
		t.Fatalf("second batch result should fail: %#v", payload.Results[1])
	}
}

func TestFileUploadPreservesOriginalFilenameAndBlobDownloadHeaders(t *testing.T) {
	app := newTestApp(t)
	defer func() { _ = app.Close() }()

	code := openEnrollment(t, app)
	handler := app.Router()
	deviceToken := registerDevice(t, handler, app, code, "preview-file-1", "Previewer")

	previewableFileData := []byte{
		0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a,
		0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52,
		0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
		0x08, 0x06, 0x00, 0x00, 0x00, 0x1f, 0x15, 0xc4,
		0x89, 0x00, 0x00, 0x00, 0x0d, 0x49, 0x44, 0x41,
		0x54, 0x78, 0x9c, 0x63, 0xf8, 0xcf, 0xc0, 0x00,
		0x00, 0x03, 0x01, 0x01, 0x00, 0xc9, 0xfe, 0x92,
		0xef, 0x00, 0x00, 0x00, 0x00, 0x49, 0x45, 0x4e,
		0x44, 0xae, 0x42, 0x60, 0x82,
	}

	resp := performMultipartFileUpload(t, handler, "/api/v1/client/items/file", deviceToken, map[string]string{

		"upload_kind":       "realtime",
		"client_created_at": time.Now().UTC().Format(time.RFC3339),
	}, "clip-123-origin name.png", previewableFileData)
	if resp.Code != http.StatusOK {
		t.Fatalf("file upload status = %d, body=%s", resp.Code, resp.Body.String())
	}

	var payload struct {
		Item map[string]any `json:"item"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode file upload response: %v", err)
	}
	if got := payload.Item["content_kind"]; got != model.ContentKindFile {
		t.Fatalf("content_kind = %#v, want %q", got, model.ContentKindFile)
	}
	if _, ok := payload.Item["blob_url"]; ok {
		t.Fatalf("blob_url should not be returned: %#v", payload.Item)
	}
	if _, ok := payload.Item["blob_name"]; ok {
		t.Fatalf("upload receipt must omit blob_name: %#v", payload.Item)
	}

	blobID, ok := payload.Item["item_id"].(float64)
	if !ok {
		t.Fatalf("blob item id missing: %#v", payload.Item)
	}
	blobResp := performJSON(t, handler, "GET", "/api/v1/client/items/"+strconv.Itoa(int(blobID))+"/blob", nil, deviceToken)
	if blobResp.Code != http.StatusOK {
		t.Fatalf("blob download status = %d, body=%s", blobResp.Code, blobResp.Body.String())
	}
	if got := blobResp.Header().Get("Content-Disposition"); !strings.Contains(got, `inline; filename="clip-123-origin name.png"`) {
		t.Fatalf("content-disposition = %q", got)
	}
	if !bytes.Equal(blobResp.Body.Bytes(), previewableFileData) {
		t.Fatalf("blob body mismatch")
	}
}

func TestNonImageFileUploadAcceptedOnFileEndpoint(t *testing.T) {
	app := newTestApp(t)
	defer func() { _ = app.Close() }()

	code := openEnrollment(t, app)
	handler := app.Router()
	deviceToken := registerDevice(t, handler, app, code, "file-1", "File User")

	fileData := []byte("hello from clipboard river")
	resp := performMultipartFileUpload(t, handler, "/api/v1/client/items/file", deviceToken, map[string]string{

		"upload_kind":       "history",
		"client_created_at": time.Now().UTC().Format(time.RFC3339),
	}, "notes.txt", fileData)
	if resp.Code != http.StatusOK {
		t.Fatalf("file upload status = %d, body=%s", resp.Code, resp.Body.String())
	}

	var payload struct {
		Item map[string]any `json:"item"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode file upload response: %v", err)
	}
	if got := payload.Item["content_kind"]; got != model.ContentKindFile {
		t.Fatalf("content_kind = %#v, want %q", got, model.ContentKindFile)
	}
	if got := payload.Item["upload_kind"]; got != model.UploadKindHistory {
		t.Fatalf("upload_kind = %#v, want %q", got, model.UploadKindHistory)
	}
	if _, ok := payload.Item["blob_url"]; ok {
		t.Fatalf("blob_url should not be returned: %#v", payload.Item)
	}
	if _, ok := payload.Item["blob_name"]; ok {
		t.Fatalf("upload receipt must omit blob_name: %#v", payload.Item)
	}
	mimeType, _ := payload.Item["mime_type"].(string)
	if !strings.HasPrefix(mimeType, "text/plain") {
		t.Fatalf("mime_type = %q, want text/plain*", mimeType)
	}
}

func TestEnrollmentWindowLifecycle(t *testing.T) {
	app := newTestApp(t)
	defer app.Close()
	handler := app.Router()
	if app.enrollment.snapshot().Open || app.db.Migrator().HasTable("enrollment_tokens") {
		t.Fatal("enrollment must start closed without a table")
	}
	response := performAdminPost(t, handler, app, "/admin/tokens/open")
	if response.Code != http.StatusFound {
		t.Fatalf("open: %d", response.Code)
	}
	snapshot := app.enrollment.snapshot()
	if !snapshot.Open || snapshot.RemainingSeconds < 299 || snapshot.RemainingSeconds > 300 {
		t.Fatalf("unexpected window: %+v", snapshot)
	}
	token := registerDevice(t, handler, app, snapshot.Code, "window-1", "Window")
	registerDevice(t, handler, app, snapshot.Code, "window-2", "Window 2")
	for _, page := range []string{"/admin/tokens", "/admin/devices", "/admin/settings"} {
		response := performAdminGet(t, handler, app, page, "en")
		if response.Code != http.StatusOK || strings.Contains(response.Body.String(), "can't evaluate field") {
			t.Fatalf("admin page %s: %d %s", page, response.Code, response.Body)
		}
	}
	performAdminPost(t, handler, app, "/admin/tokens/close")
	request := map[string]any{"device_uuid": "window-1", "enrollment_code": snapshot.Code}
	if response := performJSON(t, handler, "POST", "/api/v1/client/register", request, ""); response.Code != http.StatusBadRequest {
		t.Fatalf("closed window accepted code: %d", response.Code)
	}
	if response := performJSON(t, handler, "POST", "/api/v1/client/device/profile", nil, token); response.Code != http.StatusOK {
		t.Fatalf("closing enrollment revoked device: %d", response.Code)
	}
	freshCode := openEnrollment(t, app)
	if freshCode == snapshot.Code || app.enrollment.authorize(snapshot.Code) {
		t.Fatal("reopening must replace the code")
	}
	app.enrollment.mu.Lock()
	app.enrollment.deadline = time.Now().Add(-time.Second)
	app.enrollment.mu.Unlock()
	if app.enrollment.authorize(freshCode) || app.enrollment.snapshot().Open {
		t.Fatal("expired window must close even before timer fires")
	}
}

func TestAdminCanChangePasswordFromSettings(t *testing.T) {
	app := newTestApp(t)
	defer func() { _ = app.Close() }()

	handler := app.Router()
	resp := performAdminFormPost(t, handler, app, "/admin/settings/password", url.Values{
		"current_password": {"password"},
		"new_password":     {"better-pass-123"},
		"confirm_password": {"better-pass-123"},
	})
	if resp.Code != http.StatusFound {
		t.Fatalf("change password status = %d, want %d", resp.Code, http.StatusFound)
	}
	if location := resp.Result().Header.Get("Location"); !strings.Contains(location, "msg=password_changed") {
		t.Fatalf("unexpected redirect location: %s", location)
	}

	var admin model.AdminUser
	if err := app.db.Where("username = ?", app.cfg.Admin.Username).First(&admin).Error; err != nil {
		t.Fatalf("find admin user: %v", err)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(admin.PasswordHash), []byte("password")); err == nil {
		t.Fatalf("old password should no longer match")
	}
	if err := bcrypt.CompareHashAndPassword([]byte(admin.PasswordHash), []byte("better-pass-123")); err != nil {
		t.Fatalf("new password should match stored hash: %v", err)
	}
}

func TestAdminSettingsAcceptFileMaxBytes(t *testing.T) {
	app := newTestApp(t)
	defer func() { _ = app.Close() }()

	handler := app.Router()
	resp := performAdminFormPost(t, handler, app, "/admin/settings", url.Values{

		"retention_days": {strconv.Itoa(app.account.RetentionDays)},
		"file_max_bytes": {"111111"},
	})
	if resp.Code != http.StatusFound {
		t.Fatalf("settings update status = %d, body=%s", resp.Code, resp.Body.String())
	}

	var account model.Account
	if err := app.db.First(&account, app.account.ID).Error; err != nil {
		t.Fatalf("load account after file_max_bytes update: %v", err)
	}
	if account.FileMaxBytes != 111111 {
		t.Fatalf("file_max_bytes after update = %d, want %d", account.FileMaxBytes, 111111)
	}
}

func TestAdminHistoryShowsPaginationWhenMultiplePagesExist(t *testing.T) {
	app := newTestApp(t)
	defer func() { _ = app.Close() }()

	code := openEnrollment(t, app)
	handler := app.Router()
	registerDevice(t, handler, app, code, "history-device-1", "History Device")

	var device model.Device
	if err := app.db.Where("device_uuid = ?", "history-device-1").First(&device).Error; err != nil {
		t.Fatalf("find history device: %v", err)
	}

	now := time.Now().UTC()
	items := make([]model.ClipboardItem, 0, 51)
	for i := 0; i < 51; i++ {
		items = append(items, model.ClipboardItem{
			AccountID:      app.account.ID,
			SourceDeviceID: device.ID,

			ContentKind:     model.ContentKindText,
			UploadKind:      model.UploadKindHistory,
			TextContent:     "history item " + strconv.Itoa(i),
			CharCount:       len("history item ") + len(strconv.Itoa(i)),
			ByteSize:        int64(len("history item ") + len(strconv.Itoa(i))),
			ClientCreatedAt: now.Add(time.Duration(i) * time.Second),
			ReceivedAt:      now.Add(time.Duration(i) * time.Second),
		})
	}
	if err := app.db.Create(&items).Error; err != nil {
		t.Fatalf("create history items: %v", err)
	}

	firstPage := performAdminGet(t, handler, app, "/admin/history", "en")
	if firstPage.Code != http.StatusOK {
		t.Fatalf("history first page status = %d, body=%s", firstPage.Code, firstPage.Body.String())
	}
	firstBody := firstPage.Body.String()
	if !strings.Contains(firstBody, "Page 1 / 2") {
		t.Fatalf("expected first page pagination summary, body=%s", firstBody)
	}
	if !strings.Contains(firstBody, "/admin/history?page=2") {
		t.Fatalf("expected link to page 2, body=%s", firstBody)
	}

	secondPage := performAdminGet(t, handler, app, "/admin/history?page=2", "en")
	if secondPage.Code != http.StatusOK {
		t.Fatalf("history second page status = %d, body=%s", secondPage.Code, secondPage.Body.String())
	}
	secondBody := secondPage.Body.String()
	if !strings.Contains(secondBody, "Page 2 / 2") {
		t.Fatalf("expected second page pagination summary, body=%s", secondBody)
	}
	if !strings.Contains(secondBody, "history item 0") {
		t.Fatalf("expected oldest item on second page, body=%s", secondBody)
	}
}

func TestAdminHistoryShowsDeletedDeviceLabel(t *testing.T) {
	app := newTestApp(t)
	defer func() { _ = app.Close() }()
	handler := app.Router()

	code := openEnrollment(t, app)
	deviceToken := registerDevice(t, handler, app, code, "deleted-history-device-1", "History Owner")

	uploadResp := performJSON(t, handler, "POST", "/api/v1/client/items/text", map[string]any{

		"upload_kind":       "history",
		"client_created_at": time.Now().UTC().Format(time.RFC3339),
		"text_content":      "saved before delete",
	}, deviceToken)
	if uploadResp.Code != http.StatusOK {
		t.Fatalf("upload status = %d, body=%s", uploadResp.Code, uploadResp.Body.String())
	}

	var device model.Device
	if err := app.db.Where("device_uuid = ?", "deleted-history-device-1").First(&device).Error; err != nil {
		t.Fatalf("find device: %v", err)
	}
	if response := performAdminPost(t, handler, app, "/admin/devices/"+strconv.Itoa(int(device.ID))+"/delete"); response.Code != http.StatusFound {
		t.Fatalf("delete device: %d", response.Code)
	}

	page := performAdminGet(t, handler, app, "/admin/history", "en")
	if page.Code != http.StatusOK {
		t.Fatalf("history page status = %d, body=%s", page.Code, page.Body.String())
	}
	if !strings.Contains(page.Body.String(), "History Owner (Deleted device)") {
		t.Fatalf("expected deleted device label, body=%s", page.Body.String())
	}
}

func newTestApp(t *testing.T) *App {
	t.Helper()
	dir := t.TempDir()
	cfg := config.Config{
		Server: config.ServerConfig{
			ListenAddr: ":0",
		},
		Storage: config.StorageConfig{
			Driver:  "sqlite",
			DSN:     filepath.Join(dir, "test.db"),
			DataDir: dir,
			BlobDir: filepath.Join(dir, "blobs"),
		},
		Auth: config.AuthConfig{
			SessionSecret: "test-session-secret",
		},
		Sync: config.SyncConfig{
			DefaultRetentionDays: 30,
			FileMaxBytes:         2 * 1024 * 1024,
			TextBatchLimit:       20,
		},
		Admin: config.AdminBootstrap{
			Username: "admin",
		},
	}
	app, err := New(cfg)
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	setAdminPassword(t, app, cfg.Admin.Username, "password")
	return app
}

func setAdminPassword(t *testing.T, app *App, username, password string) {
	t.Helper()

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	if err := app.db.Model(&model.AdminUser{}).Where("username = ?", username).Update("password_hash", string(hash)).Error; err != nil {
		t.Fatalf("update admin password: %v", err)
	}
}

func openEnrollment(t *testing.T, app *App) string {
	t.Helper()
	if err := app.enrollment.open(); err != nil {
		t.Fatalf("open enrollment: %v", err)
	}
	return app.enrollment.snapshot().Code
}

func registerDevice(t *testing.T, handler http.Handler, app *App, code, uuid, nickname string) string {
	t.Helper()
	resp := performJSON(t, handler, "POST", "/api/v1/client/register", map[string]any{
		"device_uuid":     uuid,
		"enrollment_code": code,
		"nickname":        nickname,

		"app_name":    "Test Client",
		"app_version": "1.0.0",
	}, "")
	if resp.Code != http.StatusOK {
		t.Fatalf("register device %s failed: %d %s", uuid, resp.Code, resp.Body.String())
	}
	var payload struct {
		DeviceToken string `json:"device_token"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode register response: %v", err)
	}
	return payload.DeviceToken
}

func performJSON(t *testing.T, handler http.Handler, method, path string, payload any, token string) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	if payload != nil {
		if err := json.NewEncoder(&body).Encode(payload); err != nil {
			t.Fatalf("encode payload: %v", err)
		}
	}
	req := httptest.NewRequest(method, path, &body)
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	return recorder
}

func performAdminPost(t *testing.T, handler http.Handler, app *App, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, nil)
	session := auth.SignSession(app.cfg.Auth.SessionSecret, app.cfg.Admin.Username, time.Now().Add(1*time.Hour))
	req.AddCookie(&http.Cookie{
		Name:  adminSessionCookie,
		Value: session,
		Path:  "/",
	})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	return recorder
}

func performAdminFormPost(t *testing.T, handler http.Handler, app *App, path string, values url.Values) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(values.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	session := auth.SignSession(app.cfg.Auth.SessionSecret, app.cfg.Admin.Username, time.Now().Add(1*time.Hour))
	req.AddCookie(&http.Cookie{
		Name:  adminSessionCookie,
		Value: session,
		Path:  "/",
	})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	return recorder
}

func performAdminGet(t *testing.T, handler http.Handler, app *App, path, acceptLanguage string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(http.MethodGet, path, nil)
	if acceptLanguage != "" {
		req.Header.Set("Accept-Language", acceptLanguage)
	}
	session := auth.SignSession(app.cfg.Auth.SessionSecret, app.cfg.Admin.Username, time.Now().Add(1*time.Hour))
	req.AddCookie(&http.Cookie{
		Name:  adminSessionCookie,
		Value: session,
		Path:  "/",
	})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	return recorder
}

func connectClientWS(t *testing.T, serverURL, deviceToken, deviceUUID string) *websocket.Conn {
	t.Helper()
	wsURL := "ws" + strings.TrimPrefix(serverURL, "http") + "/api/v1/client/ws"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	if err := conn.WriteJSON(map[string]any{
		"type": "hello",
		"payload": map[string]any{
			"device_token": deviceToken,
			"device_uuid":  deviceUUID,
		},
	}); err != nil {
		_ = conn.Close()
		t.Fatalf("write websocket hello: %v", err)
	}

	var hello map[string]any
	if err := conn.ReadJSON(&hello); err != nil {
		_ = conn.Close()
		t.Fatalf("read hello_ack: %v", err)
	}
	if hello["type"] != "hello_ack" {
		_ = conn.Close()
		t.Fatalf("expected hello_ack, got %#v", hello)
	}
	return conn
}

func performMultipartFileUpload(t *testing.T, handler http.Handler, path, token string, fields map[string]string, fileName string, fileData []byte) *httptest.ResponseRecorder {
	t.Helper()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for key, value := range fields {
		if err := writer.WriteField(key, value); err != nil {
			t.Fatalf("write field %s: %v", key, err)
		}
	}
	part, err := writer.CreateFormFile("file", fileName)
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := io.Copy(part, bytes.NewReader(fileData)); err != nil {
		t.Fatalf("write form file: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, path, &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	return recorder
}
