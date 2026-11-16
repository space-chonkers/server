package match

import (
	"encoding/json"
	"math"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/space-chonkers/server/internal/game"
	"github.com/space-chonkers/server/internal/protocol"
)

// phenomenonStartMs is when phenomenon test matches start, on both the
// manager clock and the action timestamps.
const phenomenonStartMs = 1000

// rollInterval is PHENOMENON_ROLL_SECONDS expressed in 30 Hz ticks.
const rollInterval = 900 * time.Second / 30

func TestSecondsDurationAvoidsOverflowAndZeroForPositiveInputs(t *testing.T) {
	const maxDuration = time.Duration(1<<63 - 1)
	tests := []struct {
		name    string
		seconds float64
		want    time.Duration
	}{
		{name: "zero", seconds: 0, want: 0},
		{name: "sub-nanosecond", seconds: 1e-12, want: time.Nanosecond},
		{name: "overflow", seconds: math.MaxFloat64, want: maxDuration},
		{name: "not a number", seconds: math.NaN(), want: maxDuration},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := secondsDuration(test.seconds); got != test.want {
				t.Fatalf("secondsDuration(%v) = %v, want %v", test.seconds, got, test.want)
			}
		})
	}
}

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) set(now time.Time) {
	c.mu.Lock()
	c.now = now
	c.mu.Unlock()
}

// at returns the clock time offset from the match start.
func at(offset time.Duration) time.Time {
	return time.UnixMilli(phenomenonStartMs).Add(offset)
}

// onlyPhenomenon makes every roll produce kind (solar rolls use chance 1).
func onlyPhenomenon(kind game.PhenomenonType) func(*game.PhenomenonConfig) {
	return func(config *game.PhenomenonConfig) {
		config.SolarStormChancePerDay = 0
		config.Weights = game.PhenomenonWeights{}
		switch kind {
		case game.PhenomenonSolarStorm:
			config.SolarStormChancePerDay = 1
			config.Weights.None = 1
		case game.PhenomenonNone:
			config.Weights.None = 1
		case game.PhenomenonWaterLeak:
			config.Weights.WaterLeak = 1
		case game.PhenomenonOxygenLoss:
			config.Weights.OxygenLoss = 1
		case game.PhenomenonRottenFood:
			config.Weights.RottenFood = 1
		}
	}
}

// phenomenonTestManager starts a two-player match on a generated world with
// seed and returns the clock armed by the first Step at the start time. The
// first roll comes after one roll interval; change can override it.
func phenomenonTestManager(
	t *testing.T,
	seed int64,
	change func(*game.PhenomenonConfig),
	first, second *fakeSink,
) (*Manager, *Match, *fakeClock) {
	t.Helper()
	config := game.DefaultConfig()
	config.Phenomena.FirstRollDelaySeconds = config.Phenomena.RollSeconds
	if change != nil {
		change(&config.Phenomena)
	}
	clock := &fakeClock{now: at(0)}
	manager := NewManager(Options{
		MaxMatches:         2,
		MaxPlayersPerMatch: 2,
		Grid:               protocol.GridSize{Width: 50, Height: 50},
		GameConfig:         config,
		NewSeed:            func() (int64, error) { return seed, nil },
		Now:                clock.Now,
	})
	match, err := manager.Create("create", "p1", "one", first, phenomenonStartMs)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Join("join", match.ID(), "p2", "two", second, phenomenonStartMs); err != nil {
		t.Fatal(err)
	}
	if err := manager.Start("p1", phenomenonStartMs); err != nil {
		t.Fatal(err)
	}
	mustStep(t, manager, clock, at(0))
	return manager, match, clock
}

func mustStep(t *testing.T, manager *Manager, clock *fakeClock, now time.Time) {
	t.Helper()
	clock.set(now)
	if err := manager.Step(); err != nil {
		t.Fatal(err)
	}
}

func phenomenonEvents(t *testing.T, sink *fakeSink) []protocol.MatchPhenomenon {
	t.Helper()
	events := []protocol.MatchPhenomenon{}
	for _, envelope := range sinkMessages(sink) {
		if envelope.Type != protocol.TypeMatchPhenomenon {
			continue
		}
		var event protocol.MatchPhenomenon
		if err := json.Unmarshal(envelope.Payload, &event); err != nil {
			t.Fatal(err)
		}
		events = append(events, event)
	}
	return events
}

func endedEvent(t *testing.T, sink *fakeSink) *protocol.MatchEnded {
	t.Helper()
	for _, envelope := range sinkMessages(sink) {
		if envelope.Type == protocol.TypeMatchEnded {
			var ended protocol.MatchEnded
			if err := json.Unmarshal(envelope.Payload, &ended); err != nil {
				t.Fatal(err)
			}
			return &ended
		}
	}
	return nil
}

func stashPosition(t *testing.T, match *Match) (float64, float64) {
	t.Helper()
	match.mu.RLock()
	defer match.mu.RUnlock()
	for _, construction := range match.world.Constructions {
		if construction.Kind == game.ConstructionStash {
			return float64(construction.CellX) + 0.5, float64(construction.CellY) + 0.5
		}
	}
	t.Fatal("world has no stash")
	return 0, 0
}

// refillPools keeps long tests away from depletion; it leaves rates alone.
func refillPools(match *Match) {
	match.mu.Lock()
	defer match.mu.Unlock()
	for _, resource := range []*game.ResourceState{
		&match.world.Resources.Water, &match.world.Resources.Oxygen,
		&match.world.Resources.Energy, &match.world.Resources.Food,
	} {
		resource.Pool.Current = resource.Pool.Max
	}
}

func activeType(match *Match) game.PhenomenonType {
	match.mu.RLock()
	defer match.mu.RUnlock()
	return match.phenomenon.Type
}

func TestStepRollsAtIntervalAndResetsTimerOnEveryRoll(t *testing.T) {
	sink := &fakeSink{}
	manager, match, clock := phenomenonTestManager(t, 1, onlyPhenomenon(game.PhenomenonNone), sink, &fakeSink{})

	match.mu.RLock()
	armed := match.nextRollAt
	match.mu.RUnlock()
	if !armed.Equal(at(rollInterval)) {
		t.Fatalf("first roll at %v, want start + 30 s (900 ticks)", armed.Sub(at(0)))
	}

	mustStep(t, manager, clock, at(rollInterval-time.Millisecond))
	match.mu.RLock()
	beforeRoll := match.nextRollAt
	match.mu.RUnlock()
	if !beforeRoll.Equal(armed) {
		t.Fatal("rolled before 30 s")
	}

	// A none roll still resets the timer: the next roll is 30 s after this one.
	rolledAt := at(rollInterval + 40*time.Millisecond)
	mustStep(t, manager, clock, rolledAt)
	match.mu.RLock()
	afterRoll := match.nextRollAt
	match.mu.RUnlock()
	if !afterRoll.Equal(rolledAt.Add(rollInterval)) {
		t.Fatalf("next roll at %v, want %v", afterRoll, rolledAt.Add(rollInterval))
	}
	if events := phenomenonEvents(t, sink); len(events) != 0 {
		t.Fatalf("none roll published events: %+v", events)
	}
}

func TestFirstRollWaitsTwoMinutesByDefault(t *testing.T) {
	sink := &fakeSink{}
	firstRoll := 2 * time.Minute
	manager, match, clock := phenomenonTestManager(t, 1, func(config *game.PhenomenonConfig) {
		*config = game.DefaultPhenomenonConfig()
		onlyPhenomenon(game.PhenomenonRottenFood)(config)
	}, sink, &fakeSink{})

	for offset := 10 * time.Second; offset < firstRoll; offset += 10 * time.Second {
		refillPools(match)
		mustStep(t, manager, clock, at(offset))
	}
	refillPools(match)
	mustStep(t, manager, clock, at(firstRoll-time.Millisecond))
	if events := phenomenonEvents(t, sink); len(events) != 0 {
		t.Fatalf("events before two minutes: %+v", events)
	}

	mustStep(t, manager, clock, at(firstRoll))
	if events := phenomenonEvents(t, sink); len(events) != 1 {
		t.Fatalf("events at two minutes = %+v, want one roll", events)
	}
	refillPools(match)
	mustStep(t, manager, clock, at(firstRoll+rollInterval-time.Millisecond))
	if events := phenomenonEvents(t, sink); len(events) != 1 {
		t.Fatalf("rolled again before the 30 s interval: %+v", events)
	}
	mustStep(t, manager, clock, at(firstRoll+rollInterval))
	if events := phenomenonEvents(t, sink); len(events) != 2 {
		t.Fatalf("events after the next interval = %+v, want two rolls", events)
	}
}

func TestStepDoesNotRollWhilePhenomenonIsActive(t *testing.T) {
	sink := &fakeSink{}
	manager, match, clock := phenomenonTestManager(t, 1, onlyPhenomenon(game.PhenomenonOxygenLoss), sink, &fakeSink{})

	mustStep(t, manager, clock, at(rollInterval))
	match.mu.RLock()
	started := match.phenomenon
	nextRoll := match.nextRollAt
	match.mu.RUnlock()
	if started.Type != game.PhenomenonOxygenLoss {
		t.Fatalf("active = %+v, want oxygen loss", started)
	}

	for i := 2; i <= 5; i++ {
		refillPools(match)
		mustStep(t, manager, clock, at(time.Duration(i)*rollInterval))
	}
	match.mu.RLock()
	defer match.mu.RUnlock()
	if match.phenomenon.Type != game.PhenomenonOxygenLoss ||
		match.phenomenon.StartedAtMs != started.StartedAtMs || !match.nextRollAt.Equal(nextRoll) {
		t.Fatalf("rolled while active: %+v next %v", match.phenomenon, match.nextRollAt)
	}
	if events := phenomenonEvents(t, sink); len(events) != 1 ||
		events[0].Action != protocol.PhenomenonActionStarted {
		t.Fatalf("events = %+v, want one started event", events)
	}
}

func TestStepDoesNotRollOutsidePlaying(t *testing.T) {
	config := game.DefaultConfig()
	onlyPhenomenon(game.PhenomenonWaterLeak)(&config.Phenomena)
	clock := &fakeClock{now: at(0)}
	opts := testOptions()
	opts.GameConfig = config
	opts.Now = clock.Now
	manager := NewManager(opts)
	sink := &fakeSink{}
	lobby := mustCreate(t, manager, "p1", sink)

	for i := 0; i <= 4; i++ {
		mustStep(t, manager, clock, at(time.Duration(i)*rollInterval))
	}
	lobby.mu.RLock()
	defer lobby.mu.RUnlock()
	if lobby.phenomenon.Type != game.PhenomenonNone || !lobby.lastStepAt.IsZero() {
		t.Fatalf("lobby was stepped: %+v", lobby.phenomenon)
	}
	if events := phenomenonEvents(t, sink); len(events) != 0 {
		t.Fatalf("lobby published phenomenon events: %+v", events)
	}
}

func TestFixValidatesAndResolves(t *testing.T) {
	fixerSink, otherSink := &fakeSink{}, &fakeSink{}
	manager, match, clock := phenomenonTestManager(t, 1, onlyPhenomenon(game.PhenomenonWaterLeak), fixerSink, otherSink)

	if _, _, err := manager.Fix("p1", phenomenonStartMs); protocol.CodeOf(err) != protocol.CodeNoPhenomenon {
		t.Fatalf("fix without phenomenon error = %v, want no_phenomenon", err)
	}
	mustStep(t, manager, clock, at(rollInterval))
	if activeType(match) != game.PhenomenonWaterLeak {
		t.Fatal("water leak did not start")
	}

	stashX, stashY := stashPosition(t, match)
	setActionPlayerPosition(t, match, "p1", stashX+1, stashY, game.FacingNorth)
	if _, _, err := manager.Fix("p1", 32000); protocol.CodeOf(err) != protocol.CodeNotAtStash {
		t.Fatalf("fix off the stash error = %v, want not_at_stash", err)
	}

	setActionPlayerPosition(t, match, "p1", stashX, stashY, game.FacingNorth)
	match.mu.Lock()
	match.world.Resources.Energy.Pool.Current = 9
	before := match.world
	match.mu.Unlock()
	if _, _, err := manager.Fix("p1", 32000); protocol.CodeOf(err) != protocol.CodeInsufficientEnergy {
		t.Fatalf("fix without energy error = %v, want insufficient_energy", err)
	}
	match.mu.RLock()
	unchanged := reflect.DeepEqual(match.world, before) && match.phenomenon.Type == game.PhenomenonWaterLeak
	match.mu.RUnlock()
	if !unchanged {
		t.Fatal("rejected fix changed match state")
	}

	match.mu.Lock()
	match.world.Resources.Energy.Pool.Current = 25
	match.mu.Unlock()
	// The timer restarts from the manager clock, not the action timestamp.
	fixedAt := at(rollInterval + 9*time.Second)
	clock.set(fixedAt)
	result, ended, err := manager.Fix("p1", 0)
	if err != nil || ended != nil {
		t.Fatalf("fix = %v, %+v", err, ended)
	}
	if result.Phenomenon.Value.PhenomenonType() != protocol.PhenomenonNoneType {
		t.Fatalf("fix result phenomenon = %+v, want none", result.Phenomenon)
	}
	if result.Resources.Energy.Pool.Current != 15 {
		t.Fatalf("energy after fix = %v, want 25 - 10", result.Resources.Energy.Pool.Current)
	}
	match.mu.RLock()
	nextRoll := match.nextRollAt
	match.mu.RUnlock()
	if !nextRoll.Equal(fixedAt.Add(rollInterval)) {
		t.Fatalf("next roll at %v, want manager clock at fix + 30 s", nextRoll)
	}

	otherEvents := phenomenonEvents(t, otherSink)
	if len(otherEvents) != 2 || otherEvents[1].Action != protocol.PhenomenonActionResolved ||
		otherEvents[1].Phenomenon.PhenomenonType() != protocol.PhenomenonNoneType {
		t.Fatalf("other member events = %+v, want started then resolved", otherEvents)
	}
	if fixerEvents := phenomenonEvents(t, fixerSink); len(fixerEvents) != 1 {
		t.Fatalf("fixer events = %+v, want only started (fixer gets the response)", fixerEvents)
	}
	if frame := match.Frame(); frame.Phenomenon.Value.PhenomenonType() != protocol.PhenomenonNoneType {
		t.Fatalf("frame after fix = %+v, want none", frame.Phenomenon)
	}
}

func TestFixSpendingEnergyToZeroLoses(t *testing.T) {
	sink := &fakeSink{}
	manager, match, clock := phenomenonTestManager(t, 1, onlyPhenomenon(game.PhenomenonOxygenLoss), sink, &fakeSink{})
	mustStep(t, manager, clock, at(rollInterval))
	stashX, stashY := stashPosition(t, match)
	setActionPlayerPosition(t, match, "p1", stashX, stashY, game.FacingNorth)
	match.mu.Lock()
	match.world.Resources.Energy.Pool.Current = 10
	match.mu.Unlock()

	_, ended, err := manager.Fix("p1", 32000)
	if err != nil {
		t.Fatal(err)
	}
	if ended == nil || ended.Outcome != protocol.OutcomeLoss || ended.Stats.DepletedResource != protocol.ResourceEnergy {
		t.Fatalf("ended = %+v, want energy loss", ended)
	}
}

func TestFixBeforeStepPreventsThatStepsEffect(t *testing.T) {
	manager, match, clock := phenomenonTestManager(t, 1, onlyPhenomenon(game.PhenomenonWaterLeak), &fakeSink{}, &fakeSink{})
	mustStep(t, manager, clock, at(rollInterval))
	refillPools(match)
	stashX, stashY := stashPosition(t, match)
	setActionPlayerPosition(t, match, "p1", stashX, stashY, game.FacingNorth)

	match.mu.RLock()
	water := match.world.Resources.Water
	match.mu.RUnlock()
	if _, _, err := manager.Fix("p1", at(rollInterval).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	mustStep(t, manager, clock, at(rollInterval+time.Second))

	match.mu.RLock()
	got := match.world.Resources.Water.Pool.Current
	match.mu.RUnlock()
	if want := water.Pool.Current - water.CPS; math.Abs(got-want) > 1e-9 {
		t.Fatalf("water pool = %v, want base drain to %v", got, want)
	}
}

func TestStepAppliesLeakAndOxygenMultipliers(t *testing.T) {
	for _, kind := range []game.PhenomenonType{game.PhenomenonWaterLeak, game.PhenomenonOxygenLoss} {
		manager, match, clock := phenomenonTestManager(t, 1, onlyPhenomenon(kind), &fakeSink{}, &fakeSink{})
		match.mu.RLock()
		beforeRoll := match.world.Resources
		match.mu.RUnlock()
		mustStep(t, manager, clock, at(rollInterval))
		// The interval that ends with the roll drains at base rates.
		match.mu.RLock()
		atRoll := match.world.Resources
		match.mu.RUnlock()
		for _, resource := range []struct {
			key           game.ResourceKey
			before, after game.ResourceState
		}{
			{game.ResourceWater, beforeRoll.Water, atRoll.Water},
			{game.ResourceOxygen, beforeRoll.Oxygen, atRoll.Oxygen},
		} {
			want := resource.before.Pool.Current - resource.before.CPS*rollInterval.Seconds()
			if math.Abs(resource.after.Pool.Current-want) > 1e-9 {
				t.Fatalf("type %d: %s pool at roll = %v, want base drain to %v",
					kind, resource.key, resource.after.Pool.Current, want)
			}
		}
		refillPools(match)
		match.mu.RLock()
		before := match.world.Resources
		match.mu.RUnlock()

		mustStep(t, manager, clock, at(rollInterval+2*time.Second))
		match.mu.RLock()
		after := match.world.Resources
		match.mu.RUnlock()

		waterMultiplier, oxygenMultiplier := 1.5, 1.0
		if kind == game.PhenomenonOxygenLoss {
			waterMultiplier, oxygenMultiplier = 1.0, 1.5
		}
		for _, resource := range []struct {
			key           game.ResourceKey
			before, after game.ResourceState
			multiplier    float64
		}{
			{game.ResourceWater, before.Water, after.Water, waterMultiplier},
			{game.ResourceOxygen, before.Oxygen, after.Oxygen, oxygenMultiplier},
		} {
			want := resource.before.Pool.Current - resource.before.CPS*resource.multiplier*2
			if math.Abs(resource.after.Pool.Current-want) > 1e-9 {
				t.Fatalf("type %d: %s pool = %v, want %v", kind, resource.key, resource.after.Pool.Current, want)
			}
		}
		frame := match.Frame()
		if frame.Resources.Water.CPS != before.Water.CPS*waterMultiplier ||
			frame.Resources.Oxygen.CPS != before.Oxygen.CPS*oxygenMultiplier {
			t.Fatalf("type %d: frame CPS = %v/%v, want effective rates", kind,
				frame.Resources.Water.CPS, frame.Resources.Oxygen.CPS)
		}
	}
}

func TestSolarStormInFrameAndStateUntilFixed(t *testing.T) {
	sink := &fakeSink{}
	manager, match, clock := phenomenonTestManager(t, 1, onlyPhenomenon(game.PhenomenonSolarStorm), sink, &fakeSink{})
	mustStep(t, manager, clock, at(rollInterval))

	match.mu.RLock()
	active := match.phenomenon
	baseGPS := match.world.Resources.Water.Stash.GPS
	match.mu.RUnlock()
	if active.Type != game.PhenomenonSolarStorm {
		t.Fatalf("active = %+v, want solar storm", active)
	}
	events := phenomenonEvents(t, sink)
	if len(events) != 1 || events[0].Action != protocol.PhenomenonActionStarted {
		t.Fatalf("events = %+v, want started", events)
	}
	solar, ok := events[0].Phenomenon.(protocol.PhenomenonSolar)
	if !ok || solar.FixCostEnergy != active.FixCostEnergy ||
		solar.GpsReductionPerSecond != active.GPSReductionPerSecond ||
		solar.StartedAtMs != at(rollInterval).UnixMilli() {
		t.Fatalf("started payload = %+v, want %+v", events[0].Phenomenon, active)
	}

	zeroAfter := time.Duration(float64(time.Second) / active.GPSReductionPerSecond)
	for _, offset := range []time.Duration{zeroAfter / 2, zeroAfter, 3 * zeroAfter} {
		refillPools(match)
		mustStep(t, manager, clock, at(rollInterval+offset))
		frame := match.Frame()
		state := match.State(at(rollInterval + offset).UnixMilli())
		for name, value := range map[string]protocol.ActivePhenomenon{"frame": frame.Phenomenon, "state": state.Phenomenon} {
			if value.Value != events[0].Phenomenon {
				t.Fatalf("%s phenomenon = %+v, want %+v", name, value.Value, events[0].Phenomenon)
			}
		}
		want := baseGPS * math.Max(1-active.GPSReductionPerSecond*offset.Seconds(), 0)
		if math.Abs(frame.Resources.Water.Stash.GPS-want) > 1e-9 ||
			math.Abs(state.Resources.Water.Stash.GPS-want) > 1e-9 {
			t.Fatalf("water GPS after %v = %v/%v, want %v", offset,
				frame.Resources.Water.Stash.GPS, state.Resources.Water.Stash.GPS, want)
		}
	}
	if gps := match.Frame().Resources.Energy.Stash.GPS; gps != 0 {
		t.Fatalf("energy GPS = %v, want 0 until fixed", gps)
	}
}

func TestRottenFoodIsInstantAndCanLose(t *testing.T) {
	sink := &fakeSink{}
	manager, match, clock := phenomenonTestManager(t, 1, onlyPhenomenon(game.PhenomenonRottenFood), sink, &fakeSink{})
	match.mu.Lock()
	match.world.Resources.Food.Pool.Current = 9
	match.mu.Unlock()

	mustStep(t, manager, clock, at(rollInterval))
	if activeType(match) != game.PhenomenonNone {
		t.Fatal("rotten food occupied the active slot")
	}
	events := phenomenonEvents(t, sink)
	if len(events) != 1 || events[0].Action != protocol.PhenomenonActionInstant {
		t.Fatalf("events = %+v, want one instant event", events)
	}
	if food, ok := events[0].Phenomenon.(protocol.PhenomenonRottenFood); !ok || food.FoodLoss != 10 {
		t.Fatalf("instant payload = %+v, want food loss 5 × 2 players", events[0].Phenomenon)
	}
	ended := endedEvent(t, sink)
	if ended == nil || ended.Outcome != protocol.OutcomeLoss || ended.Stats.DepletedResource != protocol.ResourceFood {
		t.Fatalf("ended = %+v, want food loss", ended)
	}

	// The terminal match is not stepped again.
	mustStep(t, manager, clock, at(3*rollInterval))
	if got := len(phenomenonEvents(t, sink)); got != 1 {
		t.Fatalf("ended match published %d phenomenon events, want 1", got)
	}
}

func TestSameSeedProducesSamePhenomenonSequence(t *testing.T) {
	run := func(seed int64) []protocol.MatchPhenomenon {
		sink := &fakeSink{}
		manager, match, clock := phenomenonTestManager(t, seed, nil, sink, &fakeSink{})
		stashX, stashY := stashPosition(t, match)
		setActionPlayerPosition(t, match, "p1", stashX, stashY, game.FacingNorth)
		for i := 1; i <= 40; i++ {
			refillPools(match)
			now := at(time.Duration(i) * rollInterval)
			mustStep(t, manager, clock, now)
			if activeType(match) != game.PhenomenonNone {
				if _, _, err := manager.Fix("p2", now.UnixMilli()); protocol.CodeOf(err) != protocol.CodeNotAtStash {
					t.Fatalf("p2 off the stash fixed: %v", err)
				}
				if _, _, err := manager.Fix("p1", now.UnixMilli()); err != nil {
					t.Fatal(err)
				}
			}
		}
		return phenomenonEvents(t, sink)
	}
	// Match codes are random; compare everything else.
	withoutIDs := func(events []protocol.MatchPhenomenon) []protocol.MatchPhenomenon {
		for i := range events {
			events[i].MatchID = ""
		}
		return events
	}
	first, second := withoutIDs(run(5)), withoutIDs(run(5))
	if len(first) == 0 || !reflect.DeepEqual(first, second) {
		t.Fatalf("same seed produced different event sequences:\n%+v\n%+v", first, second)
	}
	if reflect.DeepEqual(first, withoutIDs(run(6))) {
		t.Fatal("different seeds produced the same event sequence")
	}
}
