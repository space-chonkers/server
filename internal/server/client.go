package server

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	pongWait   = 60 * time.Second
	pingPeriod = (pongWait * 9) / 10
	writeWait  = 10 * time.Second
)

// Client is one WebSocket connection. Reads happen in one goroutine and writes
// in another, which is the concurrency model gorilla/websocket requires.
type Client struct {
	id              string
	hub             *Hub
	conn            *websocket.Conn
	log             *slog.Logger
	send            chan []byte
	done            chan struct{}
	maxMessageBytes int64
	closeOnce       sync.Once
}

func NewClient(hub *Hub, conn *websocket.Conn, maxMessageBytes int64, log *slog.Logger) *Client {
	return &Client{
		id:              newID(),
		hub:             hub,
		conn:            conn,
		log:             log,
		send:            make(chan []byte, 32),
		done:            make(chan struct{}),
		maxMessageBytes: maxMessageBytes,
	}
}

func (c *Client) ID() string { return c.id }

func (c *Client) Start() {
	go c.writePump()
	go c.readPump()
	c.sendEvent("welcome", map[string]any{
		"clientId": c.id,
		"clients":  c.hub.Count(),
	})
}

func (c *Client) Close() {
	c.closeOnce.Do(func() {
		close(c.done)
		_ = c.conn.Close()
	})
}

func (c *Client) enqueue(message []byte) {
	select {
	case c.send <- message:
	case <-c.done:
	default:
		c.log.Warn("dropping message for slow client", "client", c.id)
	}
}

func (c *Client) sendEvent(eventType string, payload any) {
	c.enqueue(mustMarshal(Envelope{Type: eventType, Payload: marshalPayload(payload)}))
}

func (c *Client) sendResponse(id, eventType string, payload any) {
	c.enqueue(mustMarshal(Envelope{ID: id, Type: eventType, Payload: marshalPayload(payload)}))
}

func (c *Client) sendError(id, message string) {
	c.enqueue(mustMarshal(Envelope{ID: id, Type: "error", Error: message}))
}

func (c *Client) readPump() {
	defer func() {
		c.hub.Remove(c)
		c.log.Info("client disconnected", "client", c.id, "clients", c.hub.Count())
	}()

	c.conn.SetReadLimit(c.maxMessageBytes)
	_ = c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(string) error {
		return c.conn.SetReadDeadline(time.Now().Add(pongWait))
	})

	for {
		_, message, err := c.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
				c.log.Warn("websocket read error", "client", c.id, "error", err)
			}
			return
		}
		c.handleMessage(message)
	}
}

func (c *Client) writePump() {
	ticker := time.NewTicker(pingPeriod)
	defer ticker.Stop()

	for {
		select {
		case message := <-c.send:
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.TextMessage, message); err != nil {
				c.Close()
				return
			}
		case <-ticker.C:
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				c.Close()
				return
			}
		case <-c.done:
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			_ = c.conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
			return
		}
	}
}

func (c *Client) handleMessage(message []byte) {
	var envelope Envelope
	if err := json.Unmarshal(message, &envelope); err != nil {
		c.sendError("", "invalid json")
		return
	}

	switch envelope.Type {
	case "ping":
		c.sendResponse(envelope.ID, "pong", map[string]any{
			"time": time.Now().UTC().Format(time.RFC3339Nano),
		})
	case "echo":
		c.sendResponse(envelope.ID, "echo", envelope.Payload)
	case "broadcast":
		c.hub.Broadcast("broadcast", envelope.Payload, c)
		c.sendResponse(envelope.ID, "ack", nil)
	default:
		c.sendError(envelope.ID, "unknown message type")
	}
}

func newID() string {
	var buffer [8]byte
	if _, err := rand.Read(buffer[:]); err != nil {
		return "unknown"
	}
	return hex.EncodeToString(buffer[:])
}
