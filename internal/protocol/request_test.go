package protocol

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

func ptr[T any](value T) *T { return &value }

func decodeRequest(t *testing.T, raw string) (Envelope, Request) {
	t.Helper()
	envelope, err := DecodeEnvelope([]byte(raw))
	if err != nil {
		t.Fatalf("DecodeEnvelope() error = %v", err)
	}
	request, err := envelope.DecodeRequest()
	if err != nil {
		t.Fatalf("DecodeRequest() error = %v", err)
	}
	return envelope, request
}

func decodeError(t *testing.T, raw string) (Envelope, *Error) {
	t.Helper()
	envelope, err := DecodeEnvelope([]byte(raw))
	if err == nil {
		_, err = envelope.DecodeRequest()
	}
	if err == nil {
		t.Fatalf("Decode(%s) succeeded, want error", raw)
	}
	var protocolErr *Error
	if !errors.As(err, &protocolErr) {
		t.Fatalf("Decode(%s) error = %T %v, want *Error", raw, err, err)
	}
	return envelope, protocolErr
}

func TestDecodeRequests(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want Request
	}{
		{
			name: "ping",
			raw:  `{"id":"1","type":"ping","payload":{"clientTimeMs":123}}`,
			want: &PingRequest{ClientTimeMs: ptr(123.0)},
		},
		{
			name: "ping without payload",
			raw:  `{"id":"2","type":"ping"}`,
			want: &PingRequest{},
		},
		{
			name: "hello",
			raw:  `{"id":"3","type":"player.hello","payload":{"protocolVersion":1,"nickname":"ana"}}`,
			want: &PlayerHelloRequest{ProtocolVersion: 1, Nickname: "ana"},
		},
		{
			name: "hello with max multibyte nickname",
			raw:  `{"id":"4","type":"player.hello","payload":{"protocolVersion":1,"nickname":"éééééééééééééééé"}}`,
			want: &PlayerHelloRequest{ProtocolVersion: 1, Nickname: "éééééééééééééééé"},
		},
		{
			name: "join quick",
			raw:  `{"id":"5","type":"match.join"}`,
			want: &MatchJoinRequest{},
		},
		{
			name: "join empty payload",
			raw:  `{"id":"6","type":"match.join","payload":{}}`,
			want: &MatchJoinRequest{},
		},
		{
			name: "join match id",
			raw:  `{"id":"7","type":"match.join","payload":{"matchId":"AB12"}}`,
			want: &MatchJoinRequest{MatchID: ptr("AB12")},
		},
		{
			name: "join create",
			raw:  `{"id":"8","type":"match.join","payload":{"create":true}}`,
			want: &MatchJoinRequest{Create: ptr(true)},
		},
		{
			name: "leave",
			raw:  `{"id":"9","type":"match.leave"}`,
			want: &MatchLeaveRequest{},
		},
		{
			name: "start",
			raw:  `{"id":"10","type":"match.start"}`,
			want: &MatchStartRequest{},
		},
		{
			name: "input",
			raw:  `{"type":"match.input","payload":{"seq":42,"move":{"x":0,"y":-1}}}`,
			want: &MatchInputRequest{
				Seq:  ptr(int64(42)),
				Move: &InputMove{X: ptr(0.0), Y: ptr(-1.0)},
			},
		},
		{
			name: "input without move",
			raw:  `{"type":"match.input","payload":{"seq":0}}`,
			want: &MatchInputRequest{Seq: ptr(int64(0))},
		},
		{
			name: "collect",
			raw:  `{"id":"11","type":"stash.collect"}`,
			want: &StashCollectRequest{},
		},
		{
			name: "build",
			raw:  `{"id":"12","type":"base.build","payload":{"cellX":25,"cellY":23}}`,
			want: &BaseBuildRequest{CellX: ptr(25), CellY: ptr(23)},
		},
		{
			name: "fix",
			raw:  `{"id":"13","type":"phenomenon.fix"}`,
			want: &PhenomenonFixRequest{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			envelope, got := decodeRequest(t, tt.raw)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("DecodeRequest() = %#v, want %#v", got, tt.want)
			}
			if envelope.Type != got.RequestType() {
				t.Fatalf("envelope type = %q, want %q", envelope.Type, got.RequestType())
			}
		})
	}
}

func TestDecodeErrors(t *testing.T) {
	tests := []struct {
		name   string
		raw    string
		want   ErrorCode
		wantID string
	}{
		{name: "empty", raw: ``, want: CodeInvalidJSON},
		{name: "malformed", raw: `{`, want: CodeInvalidJSON},
		{name: "top level array", raw: `[]`, want: CodeInvalidPayload},
		{name: "missing type", raw: `{"id":"42"}`, want: CodeInvalidPayload, wantID: "42"},
		{name: "unknown type", raw: `{"id":"42","type":"nope"}`, want: CodeUnsupportedType, wantID: "42"},
		{name: "non-string type", raw: `{"id":"42","type":1}`, want: CodeInvalidPayload},
		{name: "ping missing id", raw: `{"type":"ping"}`, want: CodeInvalidPayload},
		{name: "ping bad payload", raw: `{"id":"1","type":"ping","payload":[]}`, want: CodeInvalidPayload},
		{name: "hello missing payload", raw: `{"id":"1","type":"player.hello"}`, want: CodeInvalidPayload},
		{name: "hello missing version", raw: `{"id":"1","type":"player.hello","payload":{"nickname":"ana"}}`, want: CodeInvalidPayload},
		{name: "hello zero version", raw: `{"id":"1","type":"player.hello","payload":{"protocolVersion":0,"nickname":"ana"}}`, want: CodeInvalidPayload},
		{name: "hello version mismatch", raw: `{"id":"42","type":"player.hello","payload":{"protocolVersion":2,"nickname":"ana"}}`, want: CodeProtocolVersionMismatch, wantID: "42"},
		{name: "hello empty nickname", raw: `{"id":"1","type":"player.hello","payload":{"protocolVersion":1,"nickname":""}}`, want: CodeInvalidPayload},
		{name: "hello long nickname", raw: `{"id":"1","type":"player.hello","payload":{"protocolVersion":1,"nickname":"xxxxxxxxxxxxxxxxx"}}`, want: CodeInvalidPayload},
		{name: "hello non-string nickname", raw: `{"id":"1","type":"player.hello","payload":{"protocolVersion":1,"nickname":1}}`, want: CodeInvalidPayload},
		{name: "join empty match id", raw: `{"id":"1","type":"match.join","payload":{"matchId":""}}`, want: CodeInvalidPayload},
		{name: "join match id and create", raw: `{"id":"1","type":"match.join","payload":{"matchId":"AB12","create":true}}`, want: CodeInvalidPayload},
		{name: "leave missing id", raw: `{"type":"match.leave"}`, want: CodeInvalidPayload},
		{name: "input missing payload", raw: `{"type":"match.input"}`, want: CodeInvalidPayload},
		{name: "input missing seq", raw: `{"type":"match.input","payload":{}}`, want: CodeInvalidPayload},
		{name: "input negative seq", raw: `{"type":"match.input","payload":{"seq":-1}}`, want: CodeInvalidPayload},
		{name: "input move missing x", raw: `{"type":"match.input","payload":{"seq":1,"move":{"y":1}}}`, want: CodeInvalidPayload},
		{name: "input move bad x", raw: `{"type":"match.input","payload":{"seq":1,"move":{"x":"a","y":1}}}`, want: CodeInvalidPayload},
		{name: "build missing payload", raw: `{"id":"1","type":"base.build"}`, want: CodeInvalidPayload},
		{name: "build missing cell x", raw: `{"id":"1","type":"base.build","payload":{"cellY":1}}`, want: CodeInvalidPayload},
		{name: "build negative cell y", raw: `{"id":"1","type":"base.build","payload":{"cellX":1,"cellY":-1}}`, want: CodeInvalidPayload},
		{name: "build bad payload keeps id", raw: `{"id":"42","type":"base.build","payload":{}}`, want: CodeInvalidPayload, wantID: "42"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			envelope, protocolErr := decodeError(t, tt.raw)
			if protocolErr.Code != tt.want {
				t.Fatalf("error code = %q, want %q", protocolErr.Code, tt.want)
			}
			if tt.wantID != "" && envelope.ID != tt.wantID {
				t.Fatalf("envelope id = %q, want %q", envelope.ID, tt.wantID)
			}
		})
	}
}

func TestUnknownFieldsIgnored(t *testing.T) {
	raw := `{"id":"1","type":"player.hello","extra":true,"payload":{"protocolVersion":1,"nickname":"ana","extra":1}}`
	_, request := decodeRequest(t, raw)
	hello, ok := request.(*PlayerHelloRequest)
	if !ok {
		t.Fatalf("request = %T, want *PlayerHelloRequest", request)
	}
	if hello.Nickname != "ana" {
		t.Fatalf("nickname = %q, want ana", hello.Nickname)
	}
}

func TestMatchInputDropsID(t *testing.T) {
	envelope, request := decodeRequest(t, `{"id":"ignored","type":"match.input","payload":{"seq":1}}`)
	if envelope.ID != "ignored" {
		t.Fatalf("envelope id = %q, want ignored", envelope.ID)
	}

	encoded, err := EncodeRequest(envelope.ID, request)
	if err != nil {
		t.Fatalf("EncodeRequest() error = %v", err)
	}
	var got Envelope
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if got.ID != "" {
		t.Fatalf("encoded id = %q, want empty", got.ID)
	}
}

func TestEncodeRequest(t *testing.T) {
	t.Run("round trip", func(t *testing.T) {
		raws := []string{
			`{"id":"1","type":"player.hello","payload":{"protocolVersion":1,"nickname":"ana"}}`,
			`{"id":"2","type":"match.leave"}`,
			`{"type":"match.input","payload":{"seq":7,"move":{"x":0.5,"y":-1}}}`,
		}
		for _, raw := range raws {
			envelope, request := decodeRequest(t, raw)
			encoded, err := EncodeRequest(envelope.ID, request)
			if err != nil {
				t.Fatalf("EncodeRequest() error = %v", err)
			}
			againEnvelope, again := decodeRequest(t, string(encoded))
			if !reflect.DeepEqual(again, request) {
				t.Fatalf("round trip = %#v, want %#v", again, request)
			}
			if againEnvelope.ID != envelope.ID {
				t.Fatalf("round trip id = %q, want %q", againEnvelope.ID, envelope.ID)
			}
		}
	})

	t.Run("empty payload requests omit payload", func(t *testing.T) {
		encoded, err := EncodeRequest("1", &MatchLeaveRequest{})
		if err != nil {
			t.Fatalf("EncodeRequest() error = %v", err)
		}
		var envelope Envelope
		if err := json.Unmarshal(encoded, &envelope); err != nil {
			t.Fatalf("json.Unmarshal() error = %v", err)
		}
		if len(envelope.Payload) != 0 {
			t.Fatalf("payload = %s, want omitted", envelope.Payload)
		}
	})

	t.Run("rejects invalid requests", func(t *testing.T) {
		tests := []struct {
			name    string
			id      string
			request Request
		}{
			{name: "missing id", request: &MatchLeaveRequest{}},
			{name: "build missing cells", id: "1", request: &BaseBuildRequest{}},
			{name: "input missing seq", id: "1", request: &MatchInputRequest{}},
			{name: "join conflict", id: "1", request: &MatchJoinRequest{MatchID: ptr("AB12"), Create: ptr(true)}},
			{name: "hello bad version", id: "1", request: &PlayerHelloRequest{ProtocolVersion: 2, Nickname: "ana"}},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				if _, err := EncodeRequest(tt.id, tt.request); err == nil {
					t.Fatal("EncodeRequest() succeeded, want error")
				}
			})
		}
	})
}

func TestCodeOf(t *testing.T) {
	if got := CodeOf(Errorf(CodeMatchFull, "")); got != CodeMatchFull {
		t.Fatalf("CodeOf(protocol error) = %q, want %q", got, CodeMatchFull)
	}
	if got := CodeOf(errors.New("boom")); got != CodeInternal {
		t.Fatalf("CodeOf(other error) = %q, want %q", got, CodeInternal)
	}
}
