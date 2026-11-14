package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/clipboardriver/cb_river_server/internal/model"
	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}

type Hub struct {
	mu      sync.RWMutex
	clients map[uint]map[*WSClient]struct{}
	closed  bool
}

type WSClient struct {
	app       *App
	conn      *websocket.Conn
	device    *model.Device
	send      chan wsMessage
	done      chan struct{}
	closeOnce sync.Once
}

type wsMessage struct {
	Type    string
	Payload any
	Item    *model.ClipboardItem
}

type wsEnvelope struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

func NewHub() *Hub { return &Hub{clients: make(map[uint]map[*WSClient]struct{})} }

func (h *Hub) Register(client *WSClient) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return false
	}
	if h.clients[client.device.ID] == nil {
		h.clients[client.device.ID] = make(map[*WSClient]struct{})
	}
	h.clients[client.device.ID][client] = struct{}{}
	return true
}

func (h *Hub) Unregister(client *WSClient) {
	h.mu.Lock()
	defer h.mu.Unlock()
	clients := h.clients[client.device.ID]
	delete(clients, client)
	if len(clients) == 0 {
		delete(h.clients, client.device.ID)
	}
}

func (h *Hub) Push(deviceID uint, payload wsMessage) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for client := range h.clients[deviceID] {
		client.queue(payload)
	}
}

func (h *Hub) Close() {
	h.mu.Lock()
	h.closed = true
	var clients []*WSClient
	for _, set := range h.clients {
		for client := range set {
			clients = append(clients, client)
		}
	}
	h.mu.Unlock()
	for _, client := range clients {
		_ = client.Close()
	}
}

func (h *Hub) CloseDevice(deviceID uint) {
	h.mu.RLock()
	clients := make([]*WSClient, 0, len(h.clients[deviceID]))
	for client := range h.clients[deviceID] {
		clients = append(clients, client)
	}
	h.mu.RUnlock()
	for _, client := range clients {
		_ = client.Close()
	}
}

func (h *Hub) IsOnline(deviceID uint) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients[deviceID]) > 0
}

func serveWebSocket(app *App, w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	conn.SetReadLimit(1 << 20)
	_ = conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	conn.SetPongHandler(func(string) error { return conn.SetReadDeadline(time.Now().Add(60 * time.Second)) })
	var hello wsEnvelope
	if err := conn.ReadJSON(&hello); err != nil || hello.Type != "hello" {
		_ = conn.Close()
		return
	}
	var payload struct {
		DeviceToken string `json:"device_token"`
		DeviceUUID  string `json:"device_uuid"`
	}
	decoder := json.NewDecoder(bytes.NewReader(hello.Payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil || payload.DeviceToken == "" || payload.DeviceUUID == "" {
		_ = conn.Close()
		return
	}
	// Authentication and registration share the lock with token rotation and device revocation.
	app.stateMu.Lock()
	req := &http.Request{Header: http.Header{"Authorization": []string{"Bearer " + payload.DeviceToken}}}
	device, err := app.authenticateDevice(req)
	if err != nil || device.DeviceUUID != payload.DeviceUUID {
		app.stateMu.Unlock()
		_ = conn.Close()
		return
	}
	client := &WSClient{app: app, conn: conn, device: device, send: make(chan wsMessage, 32), done: make(chan struct{})}
	client.queue(wsMessage{Type: "hello_ack", Payload: map[string]any{"device_id": device.ID, "settings": app.settingsSnapshot(), "time": time.Now().UTC()}})
	registered := app.hub.Register(client)
	app.stateMu.Unlock()
	if !registered {
		_ = client.Close()
		return
	}
	go client.writeLoop()
	client.readLoop()
}

func (c *WSClient) queue(payload wsMessage) {
	select {
	case <-c.done:
	case c.send <- payload:
	default:
		go c.Close()
	}
}

func (c *WSClient) readLoop() {
	defer c.Close()
	// hello is the only application message. NextReader continues to process standard
	// WebSocket control frames until the connection closes or unsupported data arrives.
	_, _, _ = c.conn.NextReader()
}

func (c *WSClient) writeMessage(message wsMessage) error {
	c.app.stateMu.Lock()
	defer c.app.stateMu.Unlock()
	var device model.Device
	if err := c.app.db.Where("id = ? AND device_token_hash = ? AND deleted = ?", c.device.ID, c.device.DeviceTokenHash, false).First(&device).Error; err != nil {
		return err
	}
	payload := map[string]any{"type": message.Type}
	switch message.Type {
	case "hello_ack":
		payload["payload"] = message.Payload
	case "clipboard.created":
		if !device.ReceiveRealtimeEnabled || message.Item == nil {
			return nil
		}
		item := *message.Item
		if item.AccountID != device.AccountID || item.SourceDeviceID == device.ID || item.UploadKind != model.UploadKindRealtime {
			return nil
		}
		if item.ExpiresAt != nil && !time.Now().Before(*item.ExpiresAt) {
			return nil
		}
		var source model.Device
		if err := c.app.db.Where("account_id = ? AND deleted = ? AND send_realtime_enabled = ?", device.AccountID, false, true).First(&source, item.SourceDeviceID).Error; err != nil {
			return nil
		}
		payload["item"] = formatItemResponse(item)
	case "clipboard.sync_changed":
		if !device.ReceiveRealtimeEnabled {
			return nil
		}
		if err := c.app.ensureTextWindowLocked(); err != nil {
			return err
		}
		items, err := c.app.visibleTextItemsLocked(&device)
		if err != nil {
			return err
		}
		ids := make([]uint, 0, len(items))
		for _, item := range items {
			ids = append(ids, item.ID)
		}
		payload["payload"] = map[string]any{"item_ids": ids}
	default:
		return nil
	}
	// Keep permission changes and the actual send ordered; stale queued events cannot bypass revocation.
	_ = c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	return c.conn.WriteJSON(payload)
}

func (c *WSClient) writeLoop() {
	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()
	defer c.Close()
	for {
		select {
		case <-c.done:
			return
		case payload := <-c.send:
			if err := c.writeMessage(payload); err != nil {
				return
			}
		case <-ticker.C:
			_ = c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

func (c *WSClient) Close() error {
	var err error
	c.closeOnce.Do(func() {
		c.app.hub.Unregister(c)
		close(c.done)
		err = c.conn.Close()
	})
	if err == nil || errors.Is(err, websocket.ErrCloseSent) {
		return nil
	}
	return err
}
