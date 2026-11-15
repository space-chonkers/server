package protocol

import (
	"encoding/json"
	"reflect"
	"testing"
)

func marshalJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	return data
}

func unmarshalJSON[T any](t *testing.T, data []byte) T {
	t.Helper()
	var value T
	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	return value
}

func assertJSON(t *testing.T, value any, want string) {
	t.Helper()
	if got := string(marshalJSON(t, value)); got != want {
		t.Fatalf("json = %s, want %s", got, want)
	}
}

func assertMarshalFails(t *testing.T, value any) {
	t.Helper()
	if _, err := json.Marshal(value); err == nil {
		t.Fatal("json.Marshal() succeeded, want error")
	}
}

func assertUnmarshalFails(t *testing.T, data string, target any) {
	t.Helper()
	if err := json.Unmarshal([]byte(data), target); err == nil {
		t.Fatalf("json.Unmarshal(%s) succeeded, want error", data)
	}
}

func mustResponse(t *testing.T, id string, typ MessageType, payload any) Envelope {
	t.Helper()
	envelope, err := NewResponse(id, typ, payload)
	if err != nil {
		t.Fatalf("NewResponse() error = %v", err)
	}
	return envelope
}

func mustEvent(t *testing.T, typ MessageType, payload any) Envelope {
	t.Helper()
	envelope, err := NewEvent(typ, payload)
	if err != nil {
		t.Fatalf("NewEvent() error = %v", err)
	}
	return envelope
}

func mustError(t *testing.T, id string, code ErrorCode, payload *ErrorPayload) Envelope {
	t.Helper()
	envelope, err := NewError(id, code, payload)
	if err != nil {
		t.Fatalf("NewError() error = %v", err)
	}
	return envelope
}

func testResourceState(gps, cps float64) ResourceState {
	return ResourceState{
		Pool:  ResourcePool{Current: 40, Max: 100},
		Stash: ResourceStash{Current: 12, Max: 29, GPS: gps},
		CPS:   cps,
	}
}

func testResources() Resources {
	return Resources{
		Water:  testResourceState(0.36, 1.32),
		Oxygen: testResourceState(0.36, 1.32),
		Energy: testResourceState(0.27, 1.07),
		Food:   testResourceState(0.30, 0.60),
	}
}

func testState() MatchState {
	return MatchState{
		MatchID: "AB12", Schema: StateSchema, Tick: 137, ServerTimeMs: 1763222404500,
		Phase: PhasePlaying, HostID: "p_1", Grid: GridSize{Width: 50, Height: 50},
		BaseCellCount: 9, TeamSize: 2,
		Players: []PlayerState{{
			ID: "p_1", Slot: 0, Name: "ana", Facing: FacingSouth,
			X: 25.5, Y: 24.9, VY: 4, LastInputSeq: 137,
		}},
		Constructions: []Construction{
			{ID: "c_1", Kind: ConstructionBaseTile, CellX: 24, CellY: 24},
			{ID: "c_5", Kind: ConstructionStash, CellX: 25, CellY: 25},
		},
		Investigation: []InvestigationCell{{CellX: 12, CellY: 8}},
		Resources:     testResources(),
		Phenomenon:    ActivePhenomenon{Value: PhenomenonNone{}},
	}
}

func TestPayloadRoundTrip(t *testing.T) {
	state := testState()
	tests := []struct {
		name  string
		value any
	}{
		{"welcome", Welcome{
			PlayerID: "p_1", ProtocolVersion: ProtocolVersion, ServerTimeMs: 1763222400000,
			TickRate: 30, FrameRate: 15,
			Limits: Limits{MaxMessageBytes: 32768, MaxPlayersPerMatch: 4, MaxNicknameLength: MaxNicknameLength, MaxMatches: 64},
			Rules:  Rules{Grid: GridSize{Width: 50, Height: 50}, InvestigationCount: 10, BuildCost: BuildCost{Water: 5, Oxygen: 5, Energy: 5}, PlayerSpeed: 4, PhenomenonRollSeconds: 30},
		}},
		{"state", state},
		{"frame", MatchFrame{MatchID: "AB12", Tick: 137, ServerTimeMs: 1763222404500, HostID: "p_1", BaseCellCount: 9, TeamSize: 2, Players: state.Players, Resources: testResources(), Phenomenon: ActivePhenomenon{Value: PhenomenonNone{}}}},
		{"build event", MatchBuild{MatchID: "AB12", Construction: Construction{ID: "c_10", Kind: ConstructionBaseTile, CellX: 25, CellY: 23}, Resources: testResources()}},
		{"phenomenon event", MatchPhenomenon{MatchID: "AB12", Action: PhenomenonActionStarted, Phenomenon: PhenomenonSolar{StartedAtMs: 1763222410000, FixCostEnergy: 20, GpsReductionPerSecond: 0.10}}},
		{"ended", MatchEnded{MatchID: "AB12", EndedAtMs: 1763222580000, Outcome: OutcomeLoss, Reason: ReasonResourceDepleted, Stats: MatchStats{ElapsedMs: 178000, BaseCells: 32, InvestigationCovered: 7, InvestigationTotal: 10, Remaining: ResourceAmounts{Water: 0, Oxygen: 12.3, Energy: 4.1, Food: 8}, DepletedResource: ResourceWater}}},
		{"pong", PongPayload{ClientTimeMs: ptr(123.0), ServerTimeMs: 1763222400000, Tick: 5}},
		{"hello result", HelloResult{PlayerID: "p_1", Nickname: "ana"}},
		{"join result", MatchJoinResult{MatchID: "AB12", State: state}},
		{"ok", OKPayload{}},
		{"collect result", StashCollectResult{Collected: ResourceAmounts{Water: 5, Oxygen: 5, Energy: 5, Food: 5}, Lost: ResourceAmounts{Water: 1}, Resources: testResources()}},
		{"build result", BaseBuildResult{Construction: Construction{ID: "c_10", Kind: ConstructionBaseTile, CellX: 25, CellY: 23}, Resources: testResources()}},
		{"fix result", PhenomenonFixResult{Phenomenon: ActivePhenomenon{Value: PhenomenonNone{}}, Resources: testResources()}},
		{"error payload", ErrorPayload{Message: "not enough", Missing: []ResourceKey{ResourceWater, ResourceEnergy}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			roundTrip(t, tt.value)
		})
	}
}

func roundTrip(t *testing.T, value any) {
	t.Helper()
	data := marshalJSON(t, value)
	got := reflect.New(reflect.TypeOf(value))
	if err := json.Unmarshal(data, got.Interface()); err != nil {
		t.Fatalf("json.Unmarshal(%s) error = %v", data, err)
	}
	if decoded := got.Elem().Interface(); !reflect.DeepEqual(decoded, value) {
		t.Fatalf("round trip = %#v, want %#v", decoded, value)
	}
}

func TestEnvelopeConstructors(t *testing.T) {
	t.Run("response", func(t *testing.T) {
		fields := envelopeFields(t, mustResponse(t, "7", TypeMatchStart, OKPayload{}))
		if fields["id"] != "7" || fields["type"] != string(TypeMatchStart) {
			t.Fatalf("envelope = %v", fields)
		}
		if _, ok := fields["payload"]; !ok {
			t.Fatal("payload is missing")
		}
	})

	t.Run("event omits id", func(t *testing.T) {
		frame := MatchFrame{Phenomenon: ActivePhenomenon{Value: PhenomenonNone{}}}
		fields := envelopeFields(t, mustEvent(t, TypeMatchFrame, frame))
		if _, ok := fields["id"]; ok {
			t.Fatal("event has an id")
		}
	})

	t.Run("error", func(t *testing.T) {
		fields := envelopeFields(t, mustError(t, "9", CodeMatchFull, nil))
		if fields["error"] != string(CodeMatchFull) {
			t.Fatalf("error = %v, want %q", fields["error"], CodeMatchFull)
		}
		if _, ok := fields["payload"]; ok {
			t.Fatal("error payload was encoded")
		}
	})

	t.Run("error payload", func(t *testing.T) {
		payload := &ErrorPayload{Message: "not enough", Missing: []ResourceKey{ResourceWater}}
		fields := envelopeFields(t, mustError(t, "10", CodeInsufficientResources, payload))
		if _, ok := fields["payload"]; !ok {
			t.Fatal("error payload is missing")
		}
	})

	t.Run("validation", func(t *testing.T) {
		tests := []struct {
			name string
			call func() (Envelope, error)
		}{
			{"empty response id", func() (Envelope, error) { return NewResponse("", TypeMatchStart, OKPayload{}) }},
			{"response error type", func() (Envelope, error) { return NewResponse("1", TypeError, OKPayload{}) }},
			{"event error type", func() (Envelope, error) { return NewEvent(TypeError, OKPayload{}) }},
			{"nil response payload", func() (Envelope, error) { return NewResponse("1", TypeMatchStart, nil) }},
			{"empty error code", func() (Envelope, error) { return NewError("1", "", nil) }},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				if _, err := tt.call(); err == nil {
					t.Fatal("constructor succeeded, want error")
				}
			})
		}
	})
}

func envelopeFields(t *testing.T, envelope Envelope) map[string]any {
	t.Helper()
	var fields map[string]any
	if err := json.Unmarshal(marshalJSON(t, envelope), &fields); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	return fields
}

func TestPhenomenonWireShapes(t *testing.T) {
	tests := []struct {
		name  string
		value Phenomenon
		want  string
	}{
		{"none", PhenomenonNone{}, `{"type":0,"startedAtMs":0,"fixCostEnergy":0,"effects":{}}`},
		{"solar zero", PhenomenonSolar{}, `{"type":1,"startedAtMs":0,"fixCostEnergy":0,"effects":{"gpsReductionPerSecond":0,"resources":["water","oxygen","energy"]}}`},
		{"solar", PhenomenonSolar{StartedAtMs: 1763222410000, FixCostEnergy: 20, GpsReductionPerSecond: 0.10}, `{"type":1,"startedAtMs":1763222410000,"fixCostEnergy":20,"effects":{"gpsReductionPerSecond":0.1,"resources":["water","oxygen","energy"]}}`},
		{"water leak", PhenomenonWaterLeak{StartedAtMs: 1000, FixCostEnergy: 10, CpsMultiplier: 1.5}, `{"type":2,"startedAtMs":1000,"fixCostEnergy":10,"effects":{"cpsMultiplier":1.5,"resource":"water"}}`},
		{"oxygen loss", PhenomenonOxygenLoss{StartedAtMs: 1000, FixCostEnergy: 10, CpsMultiplier: 1.5}, `{"type":3,"startedAtMs":1000,"fixCostEnergy":10,"effects":{"cpsMultiplier":1.5,"resource":"oxygen"}}`},
		{"rotten food", PhenomenonRottenFood{StartedAtMs: 2000, FoodLoss: 10}, `{"type":4,"startedAtMs":2000,"fixCostEnergy":0,"effects":{"foodLoss":10}}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := marshalJSON(t, tt.value)
			if string(data) != tt.want {
				t.Fatalf("json = %s, want %s", data, tt.want)
			}
			if tt.value.PhenomenonType() <= PhenomenonOxygenLossType {
				active := unmarshalJSON[ActivePhenomenon](t, data)
				if active.Value.PhenomenonType() != tt.value.PhenomenonType() {
					t.Fatalf("active type = %d, want %d", active.Value.PhenomenonType(), tt.value.PhenomenonType())
				}
			}
		})
	}
}

func TestMatchPhenomenonActions(t *testing.T) {
	valid := []MatchPhenomenon{
		{MatchID: "AB12", Action: PhenomenonActionStarted, Phenomenon: PhenomenonSolar{}},
		{MatchID: "AB12", Action: PhenomenonActionStarted, Phenomenon: PhenomenonWaterLeak{}},
		{MatchID: "AB12", Action: PhenomenonActionStarted, Phenomenon: PhenomenonOxygenLoss{}},
		{MatchID: "AB12", Action: PhenomenonActionResolved, Phenomenon: PhenomenonNone{}},
		{MatchID: "AB12", Action: PhenomenonActionInstant, Phenomenon: PhenomenonRottenFood{}},
	}
	for _, event := range valid {
		got := unmarshalJSON[MatchPhenomenon](t, marshalJSON(t, event))
		if got.Action != event.Action || got.Phenomenon.PhenomenonType() != event.Phenomenon.PhenomenonType() {
			t.Fatalf("round trip = %#v, want %#v", got, event)
		}
	}

	invalid := []MatchPhenomenon{
		{MatchID: "AB12", Action: PhenomenonActionStarted, Phenomenon: PhenomenonNone{}},
		{MatchID: "AB12", Action: PhenomenonActionResolved, Phenomenon: PhenomenonSolar{}},
		{MatchID: "AB12", Action: PhenomenonActionInstant, Phenomenon: PhenomenonSolar{}},
		{MatchID: "AB12", Action: "unknown", Phenomenon: PhenomenonNone{}},
	}
	for _, event := range invalid {
		assertMarshalFails(t, event)
	}

	raw := `{"matchId":"AB12","action":"resolved","phenomenon":{"type":1,"startedAtMs":1,"fixCostEnergy":20,"effects":{"gpsReductionPerSecond":0.1,"resources":["water","oxygen","energy"]}}}`
	assertUnmarshalFails(t, raw, &MatchPhenomenon{})
}

func TestActivePhenomenonRejectsRottenFood(t *testing.T) {
	assertUnmarshalFails(t, `{"type":4,"startedAtMs":2000,"fixCostEnergy":0,"effects":{"foodLoss":10}}`, &ActivePhenomenon{})
	assertMarshalFails(t, ActivePhenomenon{Value: PhenomenonRottenFood{}})
	assertMarshalFails(t, ActivePhenomenon{})
}

func TestPhenomenonRejectsInvalid(t *testing.T) {
	tests := []struct {
		name string
		raw  string
	}{
		{"unknown type", `{"matchId":"AB12","action":"started","phenomenon":{"type":5,"effects":{}}}`},
		{"missing type", `{"matchId":"AB12","action":"started","phenomenon":{"effects":{}}}`},
		{"null phenomenon", `{"matchId":"AB12","action":"started","phenomenon":null}`},
		{"non-integer type", `{"matchId":"AB12","action":"started","phenomenon":{"type":1.5,"effects":{}}}`},
		{"solar duplicate resources", `{"matchId":"AB12","action":"started","phenomenon":{"type":1,"effects":{"gpsReductionPerSecond":0.1,"resources":["water","water","oxygen"]}}}`},
		{"solar short resources", `{"matchId":"AB12","action":"started","phenomenon":{"type":1,"effects":{"gpsReductionPerSecond":0.1,"resources":["water"]}}}`},
		{"solar reduction too large", `{"matchId":"AB12","action":"started","phenomenon":{"type":1,"effects":{"gpsReductionPerSecond":1.1,"resources":["water","oxygen","energy"]}}}`},
		{"water leak wrong resource", `{"matchId":"AB12","action":"started","phenomenon":{"type":2,"effects":{"cpsMultiplier":1.5,"resource":"food"}}}`},
		{"negative fix cost", `{"matchId":"AB12","action":"started","phenomenon":{"type":2,"startedAtMs":1,"fixCostEnergy":-1,"effects":{"cpsMultiplier":1.5,"resource":"water"}}}`},
		{"rotten food fix cost", `{"matchId":"AB12","action":"instant","phenomenon":{"type":4,"startedAtMs":1,"fixCostEnergy":5,"effects":{"foodLoss":10}}}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertUnmarshalFails(t, tt.raw, &MatchPhenomenon{})
		})
	}
}

func TestPhenomenonMarshalValidation(t *testing.T) {
	tests := []struct {
		name  string
		value Phenomenon
	}{
		{"solar negative fix cost", PhenomenonSolar{FixCostEnergy: -1}},
		{"solar reduction too large", PhenomenonSolar{GpsReductionPerSecond: 2}},
		{"water leak negative cps", PhenomenonWaterLeak{CpsMultiplier: -1}},
		{"oxygen loss negative fix cost", PhenomenonOxygenLoss{FixCostEnergy: -1}},
		{"rotten food negative loss", PhenomenonRottenFood{FoodLoss: -1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertMarshalFails(t, tt.value)
		})
	}
}

func TestMatchStateEmptyArrays(t *testing.T) {
	state := MatchState{Phenomenon: ActivePhenomenon{Value: PhenomenonNone{}}}
	assertArrays(t, marshalJSON(t, state), "players", "constructions", "investigation")

	frame := MatchFrame{Phenomenon: ActivePhenomenon{Value: PhenomenonNone{}}}
	assertArrays(t, marshalJSON(t, frame), "players")
}

func assertArrays(t *testing.T, data []byte, keys ...string) {
	t.Helper()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	for _, key := range keys {
		if string(fields[key]) != "[]" {
			t.Fatalf("%s = %s, want []", key, fields[key])
		}
	}
}

func TestOKPayload(t *testing.T) {
	assertJSON(t, OKPayload{}, `{"ok":true}`)
	_ = unmarshalJSON[OKPayload](t, []byte(`{"ok":true}`))
	assertUnmarshalFails(t, `{"ok":false}`, &OKPayload{})
}
