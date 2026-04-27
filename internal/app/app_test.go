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

func TestRegisterSameDeviceUpdatesMetadata(t *testing.T) {
	app := newTestApp(t)
	defer func() { _ = app.Close() }()

	code := createEnrollmentToken(t, app, 10)

	first := performJSON(t, app.Router(), "POST", "/api/v1/client/register", map[string]any{
		"device_uuid":     "device-1",
		"enrollment_code": code,
		"nickname":        "Desk",
		"os_name":         "Windows",
		"os_version":      "11",
		"platform":        "desktop",
		"app_version":     "1.0.0",
	}, "")
	if first.Code != http.StatusOK {
		t.Fatalf("register status = %d, want %d, body=%s", first.Code, http.StatusOK, first.Body.String())
	}

	second := performJSON(t, app.Router(), "POST", "/api/v1/client/register", map[string]any{
		"device_uuid":     "device-1",
		"enrollment_code": code,
		"nickname":        "Desk Updated",
		"os_name":         "Windows",
		"os_version":      "11 24H2",
		"platform":        "desktop",
		"app_version":     "1.1.0",
	}, "")
	if second.Code != http.StatusOK {
		t.Fatalf("second register status = %d, want %d, body=%s", second.Code, http.StatusOK, second.Body.String())
	}

	var devices []model.Device
	if err := app.db.Find(&devices).Error; err != nil {
		t.Fatalf("list devices: %v", err)
	}
	if got := len(devices); got != 1 {
		t.Fatalf("device count = %d, want 1", got)
	}
	if devices[0].Nickname != "Desk Updated" || devices[0].OSVersion != "11 24H2" || devices[0].AppVersion != "1.1.0" {
		t.Fatalf("device metadata not updated: %+v", devices[0])
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

func TestProfileUpdateAndIdempotentTextUpload(t *testing.T) {
	app := newTestApp(t)
	defer func() { _ = app.Close() }()
	handler := app.Router()

	code := createEnrollmentToken(t, app, 10)
	deviceToken := registerDevice(t, handler, app, code, "source-1", "Phone")
	secondToken := registerDevice(t, handler, app, code, "target-1", "Laptop")

	profileResp := performJSON(t, handler, "POST", "/api/v1/client/device/profile", map[string]any{
		"nickname":     "Phone Updated",
		"os_name":      "Android",
		"os_version":   "15",
		"platform":     "mobile",
		"app_version":  "2.0.0",
		"capabilities": map[string]any{"supports_text": true, "supports_file": true, "supports_ws": true},
	}, deviceToken)
	if profileResp.Code != http.StatusOK {
		t.Fatalf("profile update status = %d, body=%s", profileResp.Code, profileResp.Body.String())
	}

	var source model.Device
	if err := app.db.Where("device_uuid = ?", "source-1").First(&source).Error; err != nil {
		t.Fatalf("find source device: %v", err)
	}
	if source.Nickname != "Phone Updated" || source.OSVersion != "15" {
		t.Fatalf("profile not updated: %+v", source)
	}
	if !strings.Contains(source.CapabilitiesJSON, `"supports_file":true`) {
		t.Fatalf("capabilities missing supports_file: %s", source.CapabilitiesJSON)
	}

	heartbeatResp := performJSON(t, handler, "POST", "/api/v1/client/heartbeat", map[string]any{}, deviceToken)
	if heartbeatResp.Code != http.StatusOK {
		t.Fatalf("heartbeat status = %d, body=%s", heartbeatResp.Code, heartbeatResp.Body.String())
	}
	var heartbeatPayload struct {
		Settings map[string]any `json:"settings"`
	}
	if err := json.Unmarshal(heartbeatResp.Body.Bytes(), &heartbeatPayload); err != nil {
		t.Fatalf("decode heartbeat response: %v", err)
	}
	if _, ok := heartbeatPayload.Settings["file_max_bytes"]; !ok {
		t.Fatalf("expected file_max_bytes in heartbeat settings: %#v", heartbeatPayload.Settings)
	}
	if got := len(heartbeatPayload.Settings); got != 4 {
		t.Fatalf("unexpected heartbeat settings field count = %d, settings=%#v", got, heartbeatPayload.Settings)
	}

	body := map[string]any{
		"client_item_id":    "item-1",
		"upload_kind":       "realtime",
		"client_created_at": time.Now().UTC().Format(time.RFC3339),
		"text_content":      "hello world",
	}
	firstUpload := performJSON(t, handler, "POST", "/api/v1/client/items/text", body, deviceToken)
	if firstUpload.Code != http.StatusOK {
		t.Fatalf("first upload status = %d, body=%s", firstUpload.Code, firstUpload.Body.String())
	}
	secondUpload := performJSON(t, handler, "POST", "/api/v1/client/items/text", body, deviceToken)
	if secondUpload.Code != http.StatusOK {
		t.Fatalf("second upload status = %d, body=%s", secondUpload.Code, secondUpload.Body.String())
	}

	var items []model.ClipboardItem
	if err := app.db.Find(&items).Error; err != nil {
		t.Fatalf("list items: %v", err)
	}
	if got := len(items); got != 1 {
		t.Fatalf("clipboard item count = %d, want 1", got)
	}

	pullResp := performJSON(t, handler, "GET", "/api/v1/client/sync/pull?after_cursor=0&limit=10", nil, secondToken)
	if pullResp.Code != http.StatusNotFound {
		t.Fatalf("sync pull should be unavailable, status = %d, body=%s", pullResp.Code, pullResp.Body.String())
	}
}

func TestRealtimeWebSocketDeliveryDoesNotReplayMissedItems(t *testing.T) {
	app := newTestApp(t)
	defer func() { _ = app.Close() }()
	handler := app.Router()

	server := httptest.NewServer(handler)
	defer server.Close()

	code := createEnrollmentToken(t, app, 10)
	sourceToken := registerDevice(t, handler, app, code, "ws-source-1", "Source")
	targetToken := registerDevice(t, handler, app, code, "ws-target-1", "Target")

	missedUpload := performJSON(t, handler, "POST", "/api/v1/client/items/text", map[string]any{
		"client_item_id":    "missed-1",
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
			"last_cursor":  0,
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
			"last_cursor":  0,
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
		"client_item_id":    "live-1",
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
	if item["client_item_id"] != "live-1" {
		t.Fatalf("expected live-1 push, got %#v", item)
	}
}

func TestToggleSendStopsRealtimeFanout(t *testing.T) {
	app := newTestApp(t)
	defer func() { _ = app.Close() }()
	handler := app.Router()

	server := httptest.NewServer(handler)
	defer server.Close()

	code := createEnrollmentToken(t, app, 10)
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
		"client_item_id":    "send-off-1",
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

	code := createEnrollmentToken(t, app, 10)
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
		"client_item_id":    "recv-off-1",
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

func TestHubOnlineStatus(t *testing.T) {
	hub := NewHub()
	device := &model.Device{ID: 42}
	client := &WSClient{
		device: device,
		send:   make(chan []byte, 1),
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

	code := createEnrollmentToken(t, app, 10)
	handler := app.Router()
	deviceToken := registerDevice(t, handler, app, code, "batch-1", "Batcher")

	resp := performJSON(t, handler, "POST", "/api/v1/client/items/text/batch", map[string]any{
		"items": []map[string]any{
			{
				"client_item_id":    "ok-1",
				"upload_kind":       "history",
				"client_created_at": time.Now().UTC().Format(time.RFC3339),
				"text_content":      "saved",
			},
			{
				"client_item_id": "",
				"text_content":   "bad",
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

	code := createEnrollmentToken(t, app, 10)
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
		"client_item_id":    "file-item-1",
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
	if got := payload.Item["blob_name"]; got != "clip-123-origin name.png" {
		t.Fatalf("blob_name = %#v, want %q", got, "clip-123-origin name.png")
	}

	blobID, ok := payload.Item["id"].(float64)
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

	code := createEnrollmentToken(t, app, 10)
	handler := app.Router()
	deviceToken := registerDevice(t, handler, app, code, "file-1", "File User")

	fileData := []byte("hello from clipboard river")
	resp := performMultipartFileUpload(t, handler, "/api/v1/client/items/file", deviceToken, map[string]string{
		"client_item_id":    "file-item-1",
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
	if got := payload.Item["blob_name"]; got != "notes.txt" {
		t.Fatalf("blob_name = %#v, want %q", got, "notes.txt")
	}
	mimeType, _ := payload.Item["mime_type"].(string)
	if !strings.HasPrefix(mimeType, "text/plain") {
		t.Fatalf("mime_type = %q, want text/plain*", mimeType)
	}
}

func TestExhaustedEnrollmentTokenCannotBeReused(t *testing.T) {
	app := newTestApp(t)
	defer func() { _ = app.Close() }()

	code := createEnrollmentToken(t, app, 1)
	handler := app.Router()

	registerDevice(t, handler, app, code, "limited-1", "First")

	resp := performJSON(t, handler, "POST", "/api/v1/client/register", map[string]any{
		"device_uuid":     "limited-2",
		"enrollment_code": code,
		"nickname":        "Second",
		"os_name":         "TestOS",
		"os_version":      "1.0",
		"platform":        "test",
		"app_version":     "1.0.0",
	}, "")
	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("second register status = %d, want %d, body=%s", resp.Code, http.StatusUnauthorized, resp.Body.String())
	}

	var token model.EnrollmentToken
	if err := app.db.Where("code_hash = ?", auth.HashToken(code)).First(&token).Error; err != nil {
		t.Fatalf("find enrollment token: %v", err)
	}
	if token.UsedCount != 1 {
		t.Fatalf("used_count = %d, want 1", token.UsedCount)
	}
	if token.RevokedAt != nil {
		t.Fatalf("exhausted token should not be revoked")
	}
	if status := resolveEnrollmentTokenStatus(token, time.Now().UTC()); status != enrollmentTokenStatusExhausted {
		t.Fatalf("token status = %s, want %s", status, enrollmentTokenStatusExhausted)
	}
}

func TestRevokeInactiveEnrollmentTokenDoesNotChangeState(t *testing.T) {
	app := newTestApp(t)
	defer func() { _ = app.Close() }()

	code := createEnrollmentToken(t, app, 1)
	handler := app.Router()
	registerDevice(t, handler, app, code, "limited-3", "First")

	var token model.EnrollmentToken
	if err := app.db.Where("code_hash = ?", auth.HashToken(code)).First(&token).Error; err != nil {
		t.Fatalf("find enrollment token: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/admin/tokens/1/revoke", nil)
	req.SetPathValue("id", strconv.FormatUint(uint64(token.ID), 10))
	recorder := httptest.NewRecorder()
	app.handleAdminRevokeToken(recorder, req)

	if recorder.Code != http.StatusFound {
		t.Fatalf("revoke status = %d, want %d", recorder.Code, http.StatusFound)
	}
	location := recorder.Result().Header.Get("Location")
	if !strings.Contains(location, "msg=token_not_active") {
		t.Fatalf("unexpected redirect location: %s", location)
	}

	var after model.EnrollmentToken
	if err := app.db.First(&after, token.ID).Error; err != nil {
		t.Fatalf("reload token: %v", err)
	}
	if after.RevokedAt != nil {
		t.Fatalf("inactive token should not be revoked")
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
		"realtime_fanout_enabled": {"on"},
		"retention_days":          {strconv.Itoa(app.account.RetentionDays)},
		"file_max_bytes":          {"111111"},
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

	code := createEnrollmentToken(t, app, 10)
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
			AccountID:       app.account.ID,
			SourceDeviceID:  device.ID,
			ClientItemID:    "hist-item-" + strconv.Itoa(i),
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

func createEnrollmentToken(t *testing.T, app *App, maxUses int) string {
	t.Helper()
	code := "code-" + time.Now().UTC().Format("150405.000000000")
	token := model.EnrollmentToken{
		AccountID:  app.account.ID,
		CodeHash:   auth.HashToken(code),
		CodePrefix: code,
		MaxUses:    maxUses,
	}
	if err := app.db.Create(&token).Error; err != nil {
		t.Fatalf("create enrollment token: %v", err)
	}
	return code
}

func registerDevice(t *testing.T, handler http.Handler, app *App, code, uuid, nickname string) string {
	t.Helper()
	resp := performJSON(t, handler, "POST", "/api/v1/client/register", map[string]any{
		"device_uuid":     uuid,
		"enrollment_code": code,
		"nickname":        nickname,
		"os_name":         "TestOS",
		"os_version":      "1.0",
		"platform":        "test",
		"app_version":     "1.0.0",
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
			"last_cursor":  0,
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
