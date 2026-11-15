package protocol

import (
	"errors"
	"reflect"
	"testing"
)

func FuzzDecodeRequest(f *testing.F) {
	seeds := []string{
		`{"id":"1","type":"ping","payload":{"clientTimeMs":123}}`,
		`{"id":"2","type":"player.hello","payload":{"protocolVersion":1,"nickname":"ana"}}`,
		`{"id":"3","type":"match.join","payload":{"matchId":"AB12"}}`,
		`{"id":"4","type":"match.leave"}`,
		`{"id":"5","type":"match.start"}`,
		`{"type":"match.input","payload":{"seq":42,"move":{"x":0,"y":-1}}}`,
		`{"id":"6","type":"stash.collect"}`,
		`{"id":"7","type":"base.build","payload":{"cellX":25,"cellY":23}}`,
		`{"id":"8","type":"phenomenon.fix"}`,
		`{`,
		`[]`,
		`{"id":"9","type":"nope"}`,
	}
	for _, seed := range seeds {
		f.Add([]byte(seed))
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 4096 {
			return
		}

		envelope, err := DecodeEnvelope(data)
		if err != nil {
			assertProtocolError(t, "DecodeEnvelope", err)
			return
		}

		request, err := envelope.DecodeRequest()
		if err != nil {
			assertProtocolError(t, "DecodeRequest", err)
			if request != nil {
				t.Fatalf("DecodeRequest() = %#v with error %v, want nil", request, err)
			}
			return
		}

		encoded, err := EncodeRequest(envelope.ID, request)
		if err != nil {
			t.Fatalf("EncodeRequest() error = %v", err)
		}
		againEnvelope, err := DecodeEnvelope(encoded)
		if err != nil {
			t.Fatalf("DecodeEnvelope(encoded) error = %v for %s", err, encoded)
		}
		again, err := againEnvelope.DecodeRequest()
		if err != nil {
			t.Fatalf("DecodeRequest(encoded) error = %v for %s", err, encoded)
		}
		if !reflect.DeepEqual(again, request) {
			t.Fatalf("round trip = %#v, want %#v", again, request)
		}

		wantID := envelope.ID
		if request.RequestType() == TypeMatchInput {
			wantID = ""
		}
		if againEnvelope.ID != wantID {
			t.Fatalf("id = %q, want %q", againEnvelope.ID, wantID)
		}
	})
}

func assertProtocolError(t *testing.T, operation string, err error) {
	t.Helper()
	var protocolErr *Error
	if !errors.As(err, &protocolErr) {
		t.Fatalf("%s() error = %T %v, want *Error", operation, err, err)
	}
	if protocolErr.Code == "" {
		t.Fatalf("%s() error has an empty code", operation)
	}
}
