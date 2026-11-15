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
	send        chan []byte
	done        chan struct{}
	closeOnce   sync.Once
	closeCode   int
	closeReason string
	nickname    string // set once by the read pump; never empty after hello
	matchID     string // set by the read pump while the player is in a match
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
		send:    make(chan []byte, 32),
		done:    make(chan struct{}),
	}, nil
}

func (c *Client) ID() string { return c.id }

func (c *Client) identified() bool { return c.nickname != "" }

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

// Send queues one server message for the write loop.
func (c *Client) Send(envelope protocol.Envelope) {
	message, err := json.Marshal(envelope)
	if err != nil {
		c.log.Error("encode server message", "client", c.id, "type", envelope.Type, "error", err)
		return
	}
	c.enqueue(message)
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

func (c *Client) enqueue(message []byte) {
	// A message queued while the client is closing may be dropped. The close
	// frame ordering is the only guarantee the protocol needs.
	select {
	case c.send <- message:
	case <-c.done:
	default:
		c.log.Warn("dropping message for slow client", "client", c.id)
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
		messageType, message, err := c.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
				c.log.Warn("websocket read error", "client", c.id, "error", err)
			}
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

func (c *Client) handleMessage(message []byte) (closeCode int, closeReason string) {
	envelope, err := protocol.DecodeEnvelope(message)
	if err != nil {
		c.sendProtocolError(envelope.ID, err)
		return 0, ""
	}

	request, err := envelope.DecodeRequest()
	if err != nil {
		c.sendProtocolError(envelope.ID, err)
		if protocol.CodeOf(err) == protocol.CodeProtocolVersionMismatch {
			return websocket.ClosePolicyViolation, "protocol version mismatch"
		}
		return 0, ""
	}

	c.handleRequest(envelope, request)
	return 0, ""
}

func (c *Client) handleRequest(envelope protocol.Envelope, request protocol.Request) {
	if !c.identified() && !allowedBeforeHello(request.RequestType()) {
		c.sendError(envelope.ID, protocol.CodeNotIdentified, "send player.hello first")
		return
	}

	switch request := request.(type) {
	case *protocol.PingRequest:
		c.sendResponse(envelope.ID, protocol.TypePong, protocol.PongPayload{
			ClientTimeMs: request.ClientTimeMs,
			ServerTimeMs: time.Now().UnixMilli(),
			Tick:         0,
		})
	case *protocol.PlayerHelloRequest:
		if !c.identify(request.Nickname) {
			c.sendError(envelope.ID, protocol.CodeAlreadyIdentified, "player is already identified")
			return
		}
		c.sendResponse(envelope.ID, protocol.TypePlayerHello, protocol.HelloResult{
			PlayerID: c.id,
			Nickname: request.Nickname,
		})
	case *protocol.MatchJoinRequest:
		c.handleMatchJoin(envelope.ID, request)
	case *protocol.MatchLeaveRequest:
		c.handleMatchLeave(envelope.ID)
	case *protocol.MatchStartRequest:
		c.handleMatchStart(envelope.ID)
	default:
		// A5 replaces this branch with movement and action handlers.
		c.sendError(envelope.ID, protocol.CodeInternal, fmt.Sprintf("%s is not implemented yet", request.RequestType()))
	}
}

func (c *Client) handleMatchJoin(id string, request *protocol.MatchJoinRequest) {
	var joined *match.Match
	var err error
	switch {
	case request.Create != nil && *request.Create:
		joined, err = c.matches.Create(c.id, c.nickname, c)
	case request.MatchID != nil:
		joined, err = c.matches.Join(*request.MatchID, c.id, c.nickname, c)
	default:
		joined, err = c.matches.JoinAny(c.id, c.nickname, c)
	}
	if err != nil {
		c.sendProtocolError(id, err)
		return
	}

	matchID := joined.ID()
	c.matchID = matchID
	state := joined.State(time.Now().UnixMilli())
	c.sendResponse(id, protocol.TypeMatchJoin, protocol.MatchJoinResult{
		MatchID: matchID,
		State:   state,
	})
	if err := joined.BroadcastExcept(c.id, protocol.TypeMatchState, state); err != nil {
		c.log.Error("broadcast join state", "client", c.id, "match", matchID, "error", err)
	}
}

func (c *Client) handleMatchLeave(id string) {
	if c.matchID == "" {
		c.sendError(id, protocol.CodeNotInMatch, "player is not in a match")
		return
	}

	matchID := c.matchID
	if err := c.matches.Leave(matchID, c.id); err != nil {
		c.matchID = ""
		c.sendProtocolError(id, err)
		return
	}
	c.matchID = ""
	c.sendResponse(id, protocol.TypeMatchLeave, protocol.OKPayload{})
	c.broadcastMatchState(matchID)
}

func (c *Client) handleMatchStart(id string) {
	if c.matchID == "" {
		c.sendError(id, protocol.CodeNotInMatch, "player is not in a match")
		return
	}

	started, err := c.matches.Start(c.matchID, c.id)
	if err != nil {
		c.sendProtocolError(id, err)
		return
	}
	c.sendResponse(id, protocol.TypeMatchStart, protocol.OKPayload{})
	if err := started.Match.Broadcast(protocol.TypeMatchState, started.Countdown); err != nil {
		c.log.Error("broadcast countdown state", "client", c.id, "match", c.matchID, "error", err)
	}
	if err := started.Match.Broadcast(protocol.TypeMatchState, started.Playing); err != nil {
		c.log.Error("broadcast playing state", "client", c.id, "match", c.matchID, "error", err)
	}
}

// leaveMatchOnDisconnect removes a disconnected player and lets the remaining
// members see the new host, if any.
func (c *Client) leaveMatchOnDisconnect() {
	if c.matchID == "" {
		return
	}
	matchID := c.matchID
	c.matchID = ""
	if err := c.matches.Leave(matchID, c.id); err != nil {
		return
	}
	c.broadcastMatchState(matchID)
}

func (c *Client) broadcastMatchState(matchID string) {
	live, ok := c.matches.Get(matchID)
	if !ok {
		return
	}
	state := live.State(time.Now().UnixMilli())
	if err := live.Broadcast(protocol.TypeMatchState, state); err != nil {
		c.log.Error("broadcast match state", "client", c.id, "match", matchID, "error", err)
	}
}

func allowedBeforeHello(typ protocol.MessageType) bool {
	return typ == protocol.TypePing || typ == protocol.TypePlayerHello
}

func (c *Client) writePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		_ = c.conn.Close()
	}()

	for {
		select {
		case message := <-c.send:
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

// flushAndClose drains anything already queued so an error is delivered before
// the close frame that follows it.
func (c *Client) flushAndClose() {
	for {
		select {
		case message := <-c.send:
			if err := c.write(websocket.TextMessage, message); err != nil {
				return
			}
		default:
			_ = c.write(websocket.CloseMessage, websocket.FormatCloseMessage(c.closeCode, c.closeReason))
			return
		}
	}
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
