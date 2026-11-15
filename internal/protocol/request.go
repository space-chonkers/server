package protocol

import (
	"encoding/json"
	"unicode/utf8"
)

// Request is a validated client request payload. It is implemented by pointer
// payloads, as returned by Envelope.DecodeRequest.
type Request interface {
	RequestType() MessageType
}

// PingRequest is an optional liveness payload.
type PingRequest struct {
	ClientTimeMs *float64 `json:"clientTimeMs,omitempty"`
}

// RequestType implements Request.
func (*PingRequest) RequestType() MessageType { return TypePing }

// PlayerHelloRequest identifies a player after the welcome.
type PlayerHelloRequest struct {
	ProtocolVersion int    `json:"protocolVersion"`
	Nickname        string `json:"nickname"`
}

// RequestType implements Request.
func (*PlayerHelloRequest) RequestType() MessageType { return TypePlayerHello }

// MatchJoinRequest joins, creates, or quick-joins a lobby.
type MatchJoinRequest struct {
	MatchID *string `json:"matchId,omitempty"`
	Create  *bool   `json:"create,omitempty"`
}

// RequestType implements Request.
func (*MatchJoinRequest) RequestType() MessageType { return TypeMatchJoin }

// MatchLeaveRequest leaves the current match.
type MatchLeaveRequest struct{}

// RequestType implements Request.
func (*MatchLeaveRequest) RequestType() MessageType { return TypeMatchLeave }

// MatchStartRequest starts a lobby as host.
type MatchStartRequest struct{}

// RequestType implements Request.
func (*MatchStartRequest) RequestType() MessageType { return TypeMatchStart }

// InputMove is a movement vector. Both components are required when the move
// object is present.
type InputMove struct {
	X *float64 `json:"x"`
	Y *float64 `json:"y"`
}

// MatchInputRequest is a full movement intent for one tick.
type MatchInputRequest struct {
	Seq  *int64     `json:"seq"`
	Move *InputMove `json:"move,omitempty"`
}

// RequestType implements Request.
func (*MatchInputRequest) RequestType() MessageType { return TypeMatchInput }

// StashCollectRequest collects all four resources at the stash.
type StashCollectRequest struct{}

// RequestType implements Request.
func (*StashCollectRequest) RequestType() MessageType { return TypeStashCollect }

// BaseBuildRequest targets the single cell in front of the player.
type BaseBuildRequest struct {
	CellX *int `json:"cellX"`
	CellY *int `json:"cellY"`
}

// RequestType implements Request.
func (*BaseBuildRequest) RequestType() MessageType { return TypeBaseBuild }

// PhenomenonFixRequest pays energy to clear the active phenomenon.
type PhenomenonFixRequest struct{}

// RequestType implements Request.
func (*PhenomenonFixRequest) RequestType() MessageType { return TypePhenomenonFix }

// DecodeEnvelope parses a request envelope. The returned envelope may be
// partially populated when err is non-nil.
func DecodeEnvelope(data []byte) (Envelope, error) {
	if !json.Valid(data) {
		return Envelope{}, Errorf(CodeInvalidJSON, "invalid JSON")
	}

	var envelope Envelope
	if err := json.Unmarshal(data, &envelope); err != nil {
		return envelope, Errorf(CodeInvalidPayload, "invalid envelope")
	}
	if envelope.Type == "" {
		return envelope, Errorf(CodeInvalidPayload, "type is required")
	}
	return envelope, nil
}

// DecodeRequest validates the envelope payload and returns a typed request.
// match.input is the only request that may omit the id.
func (e Envelope) DecodeRequest() (Request, error) {
	if e.Type != TypeMatchInput && e.ID == "" {
		return nil, Errorf(CodeInvalidPayload, "id is required")
	}

	var request Request
	switch e.Type {
	case TypePing:
		var payload PingRequest
		if len(e.Payload) > 0 {
			if err := json.Unmarshal(e.Payload, &payload); err != nil {
				return nil, invalidPayload(e.Type)
			}
		}
		request = &payload
	case TypePlayerHello:
		var payload PlayerHelloRequest
		if err := json.Unmarshal(e.Payload, &payload); err != nil {
			return nil, invalidPayload(e.Type)
		}
		request = &payload
	case TypeMatchJoin:
		var payload MatchJoinRequest
		if len(e.Payload) > 0 {
			if err := json.Unmarshal(e.Payload, &payload); err != nil {
				return nil, invalidPayload(e.Type)
			}
		}
		request = &payload
	case TypeMatchLeave:
		request = &MatchLeaveRequest{}
	case TypeMatchStart:
		request = &MatchStartRequest{}
	case TypeMatchInput:
		var payload MatchInputRequest
		if err := json.Unmarshal(e.Payload, &payload); err != nil {
			return nil, invalidPayload(e.Type)
		}
		request = &payload
	case TypeStashCollect:
		request = &StashCollectRequest{}
	case TypeBaseBuild:
		var payload BaseBuildRequest
		if err := json.Unmarshal(e.Payload, &payload); err != nil {
			return nil, invalidPayload(e.Type)
		}
		request = &payload
	case TypePhenomenonFix:
		request = &PhenomenonFixRequest{}
	default:
		return nil, Errorf(CodeUnsupportedType, "unsupported message type %q", e.Type)
	}

	if err := validateRequest(request); err != nil {
		return nil, err
	}
	return request, nil
}

// EncodeRequest builds a request envelope. The id is ignored for match.input.
// Pass a pointer, as returned by Envelope.DecodeRequest.
func EncodeRequest(id string, request Request) ([]byte, error) {
	if request == nil {
		return nil, Errorf(CodeInternal, "nil request")
	}

	typ := request.RequestType()
	if typ == TypeMatchInput {
		id = ""
	} else if id == "" {
		return nil, Errorf(CodeInvalidPayload, "id is required")
	}
	if err := validateRequest(request); err != nil {
		return nil, err
	}

	envelope := Envelope{ID: id, Type: typ}
	if requestHasPayload(request) {
		encoded, err := json.Marshal(request)
		if err != nil {
			return nil, Errorf(CodeInternal, "encode %s request", typ)
		}
		envelope.Payload = encoded
	}
	return json.Marshal(envelope)
}

func validateRequest(request Request) error {
	switch request := request.(type) {
	case *PingRequest:
		return nil
	case *PlayerHelloRequest:
		if request.ProtocolVersion == 0 {
			return Errorf(CodeInvalidPayload, "protocolVersion is required")
		}
		if request.ProtocolVersion != ProtocolVersion {
			return Errorf(CodeProtocolVersionMismatch, "unsupported protocol version %d", request.ProtocolVersion)
		}
		length := utf8.RuneCountInString(request.Nickname)
		if length == 0 || length > MaxNicknameLength {
			return Errorf(CodeInvalidPayload, "nickname must be 1..%d runes", MaxNicknameLength)
		}
	case *MatchJoinRequest:
		if request.MatchID != nil {
			if *request.MatchID == "" {
				return Errorf(CodeInvalidPayload, "matchId must not be empty")
			}
			if request.Create != nil && *request.Create {
				return Errorf(CodeInvalidPayload, "matchId and create are mutually exclusive")
			}
		}
	case *MatchLeaveRequest, *MatchStartRequest, *StashCollectRequest, *PhenomenonFixRequest:
		return nil
	case *MatchInputRequest:
		if request.Seq == nil {
			return Errorf(CodeInvalidPayload, "seq is required")
		}
		if *request.Seq < 0 {
			return Errorf(CodeInvalidPayload, "seq must not be negative")
		}
		if request.Move != nil && (request.Move.X == nil || request.Move.Y == nil) {
			return Errorf(CodeInvalidPayload, "move requires x and y")
		}
	case *BaseBuildRequest:
		if request.CellX == nil || request.CellY == nil {
			return Errorf(CodeInvalidPayload, "cellX and cellY are required")
		}
		if *request.CellX < 0 || *request.CellY < 0 {
			return Errorf(CodeInvalidPayload, "cellX and cellY must not be negative")
		}
	default:
		return Errorf(CodeInternal, "unhandled request type %T", request)
	}
	return nil
}

func requestHasPayload(request Request) bool {
	switch request.(type) {
	case *MatchLeaveRequest, *MatchStartRequest, *StashCollectRequest, *PhenomenonFixRequest:
		return false
	default:
		return true
	}
}

func invalidPayload(typ MessageType) *Error {
	return Errorf(CodeInvalidPayload, "invalid %s payload", typ)
}
