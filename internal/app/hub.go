package app

import (
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/clipboardriver/cb_river_server/internal/model"
	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

type Hub struct {
	mu      sync.RWMutex
	clients map[uint]map[*WSClient]struct{}
	closed  bool
}

type WSClient struct {
	app       *App
	conn      *websocket.Conn
	device    *model.Device
	send      chan []byte
	closeOnce sync.Once
}

type wsEnvelope struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

func NewHub() *Hub {
	return &Hub{clients: make(map[uint]map[*WSClient]struct{})}
}

func (h *Hub) Register(client *WSClient) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return
	}
	if h.clients[client.device.ID] == nil {
		h.clients[client.device.ID] = make(map[*WSClient]struct{})
	}
	h.clients[client.device.ID][client] = struct{}{}
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

func (h *Hub) Push(deviceID uint, payload any) {
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	for client := range h.clients[deviceID] {
		select {
		case client.send <- data:
		default:
			go client.Close()
		}
	}
}

func (h *Hub) Close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.closed = true
	for _, set := range h.clients {
		for client := range set {
			go client.Close()
		}
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
	conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})

	var hello wsEnvelope
	if err := conn.ReadJSON(&hello); err != nil || hello.Type != "hello" {
		_ = conn.WriteJSON(map[string]any{"type": "error", "error": "first websocket message must be hello"})
		_ = conn.Close()
		return
	}

	var payload struct {
		DeviceToken string `json:"device_token"`
		DeviceUUID  string `json:"device_uuid"`
		LastCursor  uint   `json:"last_cursor"`
	}
	if err := json.Unmarshal(hello.Payload, &payload); err != nil {
		_ = conn.WriteJSON(map[string]any{"type": "error", "error": "invalid hello payload"})
		_ = conn.Close()
		return
	}
	req := &http.Request{Header: http.Header{"Authorization": []string{"Bearer " + payload.DeviceToken}}}
	device, err := app.authenticateDevice(req)
	if err != nil || device.DeviceUUID != payload.DeviceUUID {
		_ = conn.WriteJSON(map[string]any{"type": "error", "error": "authentication failed"})
		_ = conn.Close()
		return
	}
	if payload.LastCursor > 0 {
		app.updateDeviceAck(device.ID, payload.LastCursor)
	}

	client := &WSClient{
		app:    app,
		conn:   conn,
		device: device,
		send:   make(chan []byte, 32),
	}
	app.hub.Register(client)
	client.queue(map[string]any{
		"type": "hello_ack",
		"payload": map[string]any{
			"device_id": device.ID,
			"settings":  app.settingsSnapshot(),
			"time":      time.Now().UTC(),
		},
	})
	go client.writeLoop()
	client.readLoop()
}

func (c *WSClient) queue(payload any) {
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	select {
	case c.send <- data:
	default:
		go c.Close()
	}
}

func (c *WSClient) readLoop() {
	defer c.Close()
	for {
		var msg wsEnvelope
		if err := c.conn.ReadJSON(&msg); err != nil {
			return
		}
		switch msg.Type {
		case "ack":
			var payload struct {
				ServerCursor uint `json:"server_cursor"`
			}
			if err := json.Unmarshal(msg.Payload, &payload); err == nil && payload.ServerCursor > 0 {
				c.app.updateDeviceAck(c.device.ID, payload.ServerCursor)
			}
		case "ping":
			c.queue(map[string]any{"type": "pong"})
		}
	}
}

func (c *WSClient) writeLoop() {
	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()
	defer c.Close()
	for {
		select {
		case payload, ok := <-c.send:
			if !ok {
				return
			}
			c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := c.conn.WriteMessage(websocket.TextMessage, payload); err != nil {
				return
			}
		case <-ticker.C:
			c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := c.conn.WriteMessage(websocket.PingMessage, []byte("ping")); err != nil {
				return
			}
		}
	}
}

func (c *WSClient) Close() error {
	var err error
	c.closeOnce.Do(func() {
		c.app.hub.Unregister(c)
		close(c.send)
		err = c.conn.Close()
	})
	if err == nil || errors.Is(err, websocket.ErrCloseSent) {
		return nil
	}
	return err
}
