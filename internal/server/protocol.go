package server

import "encoding/json"

// Envelope is the JSON frame used for every client request and server event.
// Requests carry an ID; responses echo it so clients can correlate them.
type Envelope struct {
	ID      string          `json:"id,omitempty"`
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload,omitempty"`
	Error   string          `json:"error,omitempty"`
}

func marshalPayload(value any) json.RawMessage {
	if value == nil {
		return nil
	}
	if raw, ok := value.(json.RawMessage); ok {
		return raw
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	return encoded
}

func mustMarshal(envelope Envelope) []byte {
	encoded, err := json.Marshal(envelope)
	if err != nil {
		return []byte(`{"type":"error","error":"failed to encode message"}`)
	}
	return encoded
}
