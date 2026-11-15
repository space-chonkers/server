package server

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/space-chonkers/server/internal/match"
	"github.com/space-chonkers/server/internal/protocol"
)

const (
	pongWait   = 60 * time.Second
	pingPeriod = (pongWait * 9) / 10
	writeWait  = 10 * time.Second

	// controlQueueSize bounds reliable messages. Overflow closes the socket
	// with 1011 instead of dropping a response or state transition.
	controlQueueSize = 32
	// frameQueueSize bounds lossy match frames. The oldest frame is discarded
	// so a slow reader cannot block the match tick.
	frameQueueSize = 4
)

// Client is one WebSocket connection. Reads happen in one goroutine and writes
// in another, which is the concurrency model gorilla/websocket requires.
type Client struct {
	id          string
	hub         *Hub
	matches     *match.Manager
	conn        *websocket.Conn
	cfg         Config
	log         *slog.Logger
	control     chan []byte // reliable responses and events
	frames      chan []byte // lossy match frames
	done        chan struct{}
	closeOnce   sync.Once
	closeCode   int
	closeReason string
	nickname    string // set once by the read pump; never empty after hello
}

func NewClient(hub *Hub, matches *match.Manager, conn *websocket.Conn, cfg Config, log *slog.Logger) (*Client, error) {
	id, err := newID()
	if err != nil {
		return nil, err
	}
	return &Client{
		id:      id,
		hub:     hub,
		matches: matches,
		conn:    conn,
		cfg:     cfg,
		log:     log,
		control: make(chan []byte, controlQueueSize),
		frames:  make(chan []byte, frameQueueSize),
		done:    make(chan struct{}),
	}, nil
}

func (c *Client) ID() string { return c.id }

func (c *Client) identified() bool { return c.nickname != "" }

// closing reports whether the client has started shutting down.
func (c *Client) closing() bool {
	select {
	case <-c.done:
		return true
	default:
		return false
	}
}

// identify records the player's name. It returns false when already identified.
func (c *Client) identify(nickname string) bool {
	if c.nickname != "" {
		return false
	}
	c.nickname = nickname
	return true
}

func (c *Client) Start() {
	// The welcome must be the first frame, so it is queued before the read loop
	// can answer a client that writes player.hello immediately after dialing.
	c.sendWelcome()
	go c.writePump()
	go c.readPump()
}

// Send queues one server message for the write loop. Frames are lossy and may
// be dropped; every other message is reliable and may only be lost by closing
// the connection.
func (c *Client) Send(envelope protocol.Envelope) {
	message, err := json.Marshal(envelope)
	if err != nil {
		c.log.Error("encode server message", "client", c.id, "type", envelope.Type, "error", err)
		return
	}
	if envelope.Type == protocol.TypeMatchFrame {
		c.enqueueFrame(message)
		return
	}
	c.enqueueControl(message)
}

// Close closes the connection normally.
func (c *Client) Close() {
	c.closeWith(websocket.CloseNormalClosure, "")
}

// GoAway closes the connection during server shutdown.
func (c *Client) GoAway() {
	c.closeWith(websocket.CloseGoingAway, "server shutting down")
}

// closeWith records the first close code and asks the write loop to flush
// queued messages before sending the close frame.
func (c *Client) closeWith(code int, reason string) {
	c.closeOnce.Do(func() {
		c.closeCode = code
		c.closeReason = reason
		close(c.done)
	})
}

func (c *Client) sendWelcome() {
	event, err := protocol.NewEvent(protocol.TypeWelcome, c.cfg.Welcome(c.id))
	if err != nil {
		c.log.Error("build welcome", "client", c.id, "error", err)
		return
	}
	c.Send(event)
}

func (c *Client) sendResponse(id string, typ protocol.MessageType, payload any) {
	envelope, err := protocol.NewResponse(id, typ, payload)
	if err != nil {
		c.log.Error("build response", "client", c.id, "type", typ, "error", err)
		return
	}
	c.Send(envelope)
}

func (c *Client) sendError(id string, code protocol.ErrorCode, message string) {
	envelope, err := protocol.NewError(id, code, &protocol.ErrorPayload{Message: message})
	if err != nil {
		c.log.Error("build error message", "client", c.id, "error", err)
		return
	}
	c.Send(envelope)
}

func (c *Client) sendProtocolError(id string, err error) {
	code := protocol.CodeOf(err)
	message := "internal error"
	var protocolErr *protocol.Error
	if errors.As(err, &protocolErr) {
		message = protocolErr.Message
	}
	c.sendError(id, code, message)
}

func (c *Client) enqueueControl(message []byte) {
	// A message queued while the client is closing may be dropped. The close
	// frame ordering is the only guarantee the protocol needs.
	select {
	case c.control <- message:
	case <-c.done:
	default:
		c.log.Warn("closing client for control backpressure", "client", c.id)
		c.closeWith(websocket.CloseInternalServerErr, "control queue full")
	}
}

// enqueueFrame keeps only the newest frames so a slow reader cannot apply
// backpressure to the match tick.
func (c *Client) enqueueFrame(message []byte) {
	select {
	case c.frames <- message:
		return
	default:
	}
	// Discard the oldest frame to make room. Both sends are non-blocking; a
	// concurrent writer may free the slot first.
	select {
	case <-c.frames:
	default:
	}
	select {
	case c.frames <- message:
	default:
	}
}

func (c *Client) readPump() {
	defer func() {
		c.leaveMatchOnDisconnect()
		c.hub.Remove(c)
		c.log.Info("client disconnected", "client", c.id, "clients", c.hub.Count())
	}()

	c.conn.SetReadLimit(c.cfg.MaxMessageBytes)
	_ = c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(string) error {
		return c.conn.SetReadDeadline(time.Now().Add(pongWait))
	})

	for {
		if c.closing() {
			return
		}
		messageType, message, err := c.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
				c.log.Warn("websocket read error", "client", c.id, "error", err)
			}
			return
		}
		if c.closing() {
			return
		}
		if messageType != websocket.TextMessage {
			c.closeWith(websocket.CloseUnsupportedData, "text frames only")
			return
		}
		if code, reason := c.handleMessage(message); code != 0 {
			c.closeWith(code, reason)
			return
		}
	}
}

func (c *Client) writePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		_ = c.conn.Close()
	}()

	for {
		select {
		case message := <-c.control:
			if err := c.write(websocket.TextMessage, message); err != nil {
				c.Close()
				return
			}
		case message := <-c.frames:
			// Deliver anything reliable that was queued before this frame.
			if !c.flushControl() {
				return
			}
			if err := c.write(websocket.TextMessage, message); err != nil {
				c.Close()
				return
			}
		case <-ticker.C:
			if err := c.write(websocket.PingMessage, nil); err != nil {
				c.Close()
				return
			}
		case <-c.done:
			c.flushAndClose()
			return
		}
	}
}

// flushControl writes queued reliable messages. It reports false when the
// connection failed and the writer must stop.
func (c *Client) flushControl() bool {
	for {
		select {
		case message := <-c.control:
			if err := c.write(websocket.TextMessage, message); err != nil {
				c.Close()
				return false
			}
		default:
			return true
		}
	}
}

// flushAndClose drains reliable messages so an error is delivered before the
// close frame that follows it. Lossy frames are discarded.
func (c *Client) flushAndClose() {
	if !c.flushControl() {
		return
	}
	_ = c.write(websocket.CloseMessage, websocket.FormatCloseMessage(c.closeCode, c.closeReason))
}

func (c *Client) write(messageType int, data []byte) error {
	_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
	return c.conn.WriteMessage(messageType, data)
}

func newID() (string, error) {
	var buffer [4]byte
	if _, err := rand.Read(buffer[:]); err != nil {
		return "", fmt.Errorf("generate player id: %w", err)
	}
	return "p_" + hex.EncodeToString(buffer[:]), nil
}
