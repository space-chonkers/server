package server

import (
	"fmt"
	"math"
	"time"

	"github.com/gorilla/websocket"
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
	case *protocol.MatchInputRequest:
		c.handleMatchInput(envelope.ID, request)
	case *protocol.PhenomenonFixRequest:
		c.handlePhenomenonFix(envelope.ID)
	default:
		// Later match actions are not implemented yet.
		c.sendError(envelope.ID, protocol.CodeInternal, fmt.Sprintf("%s is not implemented yet", request.RequestType()))
	}
}

func (c *Client) handleMatchJoin(id string, request *protocol.MatchJoinRequest) {
	now := time.Now().UnixMilli()
	var err error
	switch {
	case request.Create != nil && *request.Create:
		_, err = c.matches.Create(id, c.id, c.nickname, c, now)
	case request.MatchID != nil:
		_, err = c.matches.Join(id, *request.MatchID, c.id, c.nickname, c, now)
	default:
		_, err = c.matches.JoinAny(id, c.id, c.nickname, c, now)
	}
	if err != nil {
		c.sendProtocolError(id, err)
	}
}

func (c *Client) handleMatchLeave(id string) {
	if err := c.matches.Leave(c.id, time.Now().UnixMilli()); err != nil {
		c.sendProtocolError(id, err)
		return
	}
	c.sendResponse(id, protocol.TypeMatchLeave, protocol.OKPayload{})
}

func (c *Client) handleMatchStart(id string) {
	if err := c.matches.Start(c.id, time.Now().UnixMilli()); err != nil {
		c.sendProtocolError(id, err)
		return
	}
	c.sendResponse(id, protocol.TypeMatchStart, protocol.OKPayload{})
}

func (c *Client) handleMatchInput(id string, request *protocol.MatchInputRequest) {
	if c.cfg.TickRate <= 0 || math.IsNaN(c.cfg.TickRate) || math.IsInf(c.cfg.TickRate, 0) {
		c.sendError(id, protocol.CodeInternal, "server tick rate must be finite and positive")
		return
	}
	if _, _, err := c.matches.ApplyPlayerInput(
		c.id, request, c.cfg.Rules.PlayerSpeed, 1/c.cfg.TickRate,
	); err != nil {
		c.sendProtocolError(id, err)
	}
}

// handlePhenomenonFix pays energy to clear the active phenomenon. The fixer
// gets the response; other members get the reliable resolved event.
func (c *Client) handlePhenomenonFix(id string) {
	result, _, err := c.matches.Fix(c.id, time.Now().UnixMilli())
	if err != nil {
		c.sendProtocolError(id, err)
		return
	}
	c.sendResponse(id, protocol.TypePhenomenonFix, result)
}

// leaveMatchOnDisconnect removes a disconnected player and publishes the
// surviving state.
func (c *Client) leaveMatchOnDisconnect() {
	_ = c.matches.Leave(c.id, time.Now().UnixMilli())
}

func allowedBeforeHello(typ protocol.MessageType) bool {
	return typ == protocol.TypePing || typ == protocol.TypePlayerHello
}
