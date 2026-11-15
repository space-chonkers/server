package protocol

import "encoding/json"

// Envelope is the JSON frame used for every request, response, and event.
// Requests carry an ID; responses echo it and events omit it.
type Envelope struct {
	ID      string          `json:"id,omitempty"`
	Type    MessageType     `json:"type"`
	Payload json.RawMessage `json:"payload,omitempty"`
	Error   ErrorCode       `json:"error,omitempty"`
}

// NewResponse builds a response envelope. The id is required and the payload
// must be present.
func NewResponse(id string, typ MessageType, payload any) (Envelope, error) {
	if id == "" {
		return Envelope{}, Errorf(CodeInternal, "response %q requires an id", typ)
	}
	return newEnvelope(id, typ, payload)
}

// NewEvent builds an event envelope without an id.
func NewEvent(typ MessageType, payload any) (Envelope, error) {
	return newEnvelope("", typ, payload)
}

// NewError builds an error message. An empty id is allowed for errors that do
// not answer a request.
func NewError(id string, code ErrorCode, payload *ErrorPayload) (Envelope, error) {
	if code == "" {
		return Envelope{}, Errorf(CodeInternal, "error code is required")
	}
	envelope := Envelope{ID: id, Type: TypeError, Error: code}
	if payload == nil {
		return envelope, nil
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return Envelope{}, err
	}
	envelope.Payload = encoded
	return envelope, nil
}

func newEnvelope(id string, typ MessageType, payload any) (Envelope, error) {
	if typ == "" {
		return Envelope{}, Errorf(CodeInternal, "message type is required")
	}
	if typ == TypeError {
		return Envelope{}, Errorf(CodeInternal, "use NewError for error messages")
	}
	if payload == nil {
		return Envelope{}, Errorf(CodeInternal, "message %q requires a payload", typ)
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return Envelope{}, err
	}
	return Envelope{ID: id, Type: typ, Payload: encoded}, nil
}
