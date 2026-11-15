package server

import (
	"fmt"
	"time"

	"github.com/gorilla/websocket"
	"github.com/space-chonkers/server/internal/match"
	"github.com/space-chonkers/server/internal/protocol"
)

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
	now := time.Now().UnixMilli()
	c.sendResponse(id, protocol.TypeMatchJoin, protocol.MatchJoinResult{
		MatchID: matchID,
		State:   joined.State(now),
	})
	if err := joined.PublishState(c.id, now); err != nil {
		c.log.Error("publish join state", "client", c.id, "match", matchID, "error", err)
	}
}

func (c *Client) handleMatchLeave(id string) {
	surviving, err := c.matches.Leave(c.id)
	if err != nil {
		c.sendProtocolError(id, err)
		return
	}
	c.sendResponse(id, protocol.TypeMatchLeave, protocol.OKPayload{})
	c.publishState(surviving)
}

func (c *Client) handleMatchStart(id string) {
	started, err := c.matches.Start(c.id)
	if err != nil {
		c.sendProtocolError(id, err)
		return
	}
	c.sendResponse(id, protocol.TypeMatchStart, protocol.OKPayload{})
	if err := started.PublishStart(time.Now().UnixMilli()); err != nil {
		c.log.Error("publish start state", "client", c.id, "match", started.ID(), "error", err)
	}
}

// leaveMatchOnDisconnect removes a disconnected player and lets the remaining
// members see the new host, if any.
func (c *Client) leaveMatchOnDisconnect() {
	surviving, err := c.matches.Leave(c.id)
	if err != nil {
		return
	}
	c.publishState(surviving)
}

func (c *Client) publishState(live *match.Match) {
	if live == nil {
		return
	}
	if err := live.PublishState("", time.Now().UnixMilli()); err != nil {
		c.log.Error("publish match state", "client", c.id, "match", live.ID(), "error", err)
	}
}

func allowedBeforeHello(typ protocol.MessageType) bool {
	return typ == protocol.TypePing || typ == protocol.TypePlayerHello
}
