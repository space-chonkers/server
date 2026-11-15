// Package protocol defines the frozen v1 wire contract and validates inbound
// client requests. It mirrors docs/protocol/v1.schema.json.
//
// Client requests are decoded with DecodeEnvelope and Envelope.DecodeRequest.
// Server messages are encoded from the exported types; clients may decode them
// into the same types. Integer fields use Go integer decoding, so a
// fractional-form integer such as 1.0 is rejected even though JSON Schema
// accepts it.
package protocol

import (
	"errors"
	"fmt"
)

const (
	// ProtocolVersion is the wire protocol version carried by player.hello.
	ProtocolVersion = 1
	// StateSchema is the match.state schema version.
	StateSchema = 1
	// MaxNicknameLength is the nickname limit in runes.
	MaxNicknameLength = 16
)

// MessageType is a client request or server message type.
type MessageType string

// Client request message types.
const (
	TypePing          MessageType = "ping"
	TypePlayerHello   MessageType = "player.hello"
	TypeMatchJoin     MessageType = "match.join"
	TypeMatchLeave    MessageType = "match.leave"
	TypeMatchStart    MessageType = "match.start"
	TypeMatchInput    MessageType = "match.input"
	TypeStashCollect  MessageType = "stash.collect"
	TypeBaseBuild     MessageType = "base.build"
	TypePhenomenonFix MessageType = "phenomenon.fix"
)

// Server message types.
const (
	TypeWelcome         MessageType = "welcome"
	TypePong            MessageType = "pong"
	TypeMatchState      MessageType = "match.state"
	TypeMatchFrame      MessageType = "match.frame"
	TypeMatchBuild      MessageType = "match.build"
	TypeMatchPhenomenon MessageType = "match.phenomenon"
	TypeMatchEnded      MessageType = "match.ended"
	TypeError           MessageType = "error"
)

// ErrorCode is a stable protocol error code.
type ErrorCode string

// Stable error codes from the v1 contract.
const (
	CodeInvalidJSON             ErrorCode = "invalid_json"
	CodeInvalidPayload          ErrorCode = "invalid_payload"
	CodeUnsupportedType         ErrorCode = "unsupported_type"
	CodeProtocolVersionMismatch ErrorCode = "protocol_version_mismatch"
	CodeNotIdentified           ErrorCode = "not_identified"
	CodeAlreadyIdentified       ErrorCode = "already_identified"
	CodeAlreadyInMatch          ErrorCode = "already_in_match"
	CodeNotInMatch              ErrorCode = "not_in_match"
	CodeMatchNotFound           ErrorCode = "match_not_found"
	CodeMatchFull               ErrorCode = "match_full"
	CodeMatchInProgress         ErrorCode = "match_in_progress"
	CodeNotHost                 ErrorCode = "not_host"
	CodeWrongPhase              ErrorCode = "wrong_phase"
	CodeNotOnBase               ErrorCode = "not_on_base"
	CodeNotInFront              ErrorCode = "not_in_front"
	CodeCellOccupied            ErrorCode = "cell_occupied"
	CodeOutOfBounds             ErrorCode = "out_of_bounds"
	CodeInsufficientResources   ErrorCode = "insufficient_resources"
	CodeInsufficientEnergy      ErrorCode = "insufficient_energy"
	CodeNotAtStash              ErrorCode = "not_at_stash"
	CodeNoPhenomenon            ErrorCode = "no_phenomenon"
	CodeMatchOver               ErrorCode = "match_over"
	CodeInvalidCell             ErrorCode = "invalid_cell"
	CodeServerFull              ErrorCode = "server_full"
	CodeRateLimited             ErrorCode = "rate_limited"
	CodeInternal                ErrorCode = "internal"
)

// Error is a protocol failure with a stable error code.
type Error struct {
	Code    ErrorCode
	Message string
}

// Error implements error.
func (e *Error) Error() string {
	if e.Message == "" {
		return string(e.Code)
	}
	return string(e.Code) + ": " + e.Message
}

// Errorf builds a protocol error.
func Errorf(code ErrorCode, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

// CodeOf returns the protocol code carried by err, or CodeInternal.
func CodeOf(err error) ErrorCode {
	var protocolErr *Error
	if errors.As(err, &protocolErr) {
		return protocolErr.Code
	}
	return CodeInternal
}
