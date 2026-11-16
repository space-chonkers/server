package game

import (
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"
)

func TestSolarChancePerRoll(t *testing.T) {
	tests := []struct {
		name          string
		chancePerDay  float64
		rollSeconds   float64
		secondsPerDay float64
		want          float64
		tolerance     float64
	}{
		{"default half-day roll", 0.31, 30, 60, 0.169, 0.001},
		{"roll equals one day", 0.31, 60, 60, 0.31, 1e-12},
		{"never", 0, 30, 60, 0, 0},
		{"always", 1, 30, 60, 1, 0},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := DefaultPhenomenonConfig()
			config.SolarStormChancePerDay = test.chancePerDay
			config.RollSeconds = test.rollSeconds
			config.SecondsPerDay = test.secondsPerDay
			if got := SolarChancePerRoll(config); math.Abs(got-test.want) > test.tolerance {
				t.Fatalf("SolarChancePerRoll = %.6f, want %.6f ± %g", got, test.want, test.tolerance)
			}
		})
	}

	perRoll := SolarChancePerRoll(DefaultPhenomenonConfig())
	if perDay := 1 - math.Pow(1-perRoll, 2); math.Abs(perDay-0.31) > 1e-12 {
		t.Fatalf("two rolls compound to %.12f, want 0.31", perDay)
	}
}

func TestValidatePhenomenonConfigDefaults(t *testing.T) {
	if err := ValidatePhenomenonConfig(DefaultConfig()); err != nil {
		t.Fatalf("default phenomenon config should be valid: %v", err)
	}
}

func TestValidatePhenomenonConfigRejectsInvalidValues(t *testing.T) {
	tests := []struct {
		name   string
		change func(*PhenomenonConfig)
		want   string
	}{
		{"negative chance", func(c *PhenomenonConfig) { c.SolarStormChancePerDay = -0.01 },
			"solar storm chance per day must be between 0 and 1"},
		{"chance above one", func(c *PhenomenonConfig) { c.SolarStormChancePerDay = 1.01 },
			"solar storm chance per day must be between 0 and 1"},
		{"zero seconds per day", func(c *PhenomenonConfig) { c.SecondsPerDay = 0 },
			"seconds per day must be finite and positive"},
		{"negative seconds per day", func(c *PhenomenonConfig) { c.SecondsPerDay = -60 },
			"seconds per day must be finite and positive"},
		{"negative first roll delay", func(c *PhenomenonConfig) { c.FirstRollDelaySeconds = -1 },
			"first phenomenon roll delay must be representable as a duration and non-negative"},
		{"sub-nanosecond first roll delay", func(c *PhenomenonConfig) { c.FirstRollDelaySeconds = 1e-12 },
			"first phenomenon roll delay must be representable as a duration"},
		{"overflowing first roll delay", func(c *PhenomenonConfig) { c.FirstRollDelaySeconds = math.MaxFloat64 },
			"first phenomenon roll delay must be representable as a duration"},
		{"zero roll seconds", func(c *PhenomenonConfig) { c.RollSeconds = 0 },
			"phenomenon roll seconds must be representable as a positive duration"},
		{"sub-nanosecond roll seconds", func(c *PhenomenonConfig) { c.RollSeconds = 1e-12 },
			"phenomenon roll seconds must be representable as a positive duration"},
		{"overflowing roll seconds", func(c *PhenomenonConfig) { c.RollSeconds = math.MaxFloat64 },
			"phenomenon roll seconds must be representable as a positive duration"},
		{"negative weight", func(c *PhenomenonConfig) { c.Weights.WaterLeak = -1 },
			"phenomenon weight water leak must be finite and non-negative"},
		{"all-zero weights", func(c *PhenomenonConfig) { c.Weights = PhenomenonWeights{} },
			"phenomenon weights must have a finite positive sum"},
		{"strong share above one", func(c *PhenomenonConfig) { c.StrongShare = 2 },
			"strong solar share must be between 0 and 1"},
		{"solar reduction above one", func(c *PhenomenonConfig) { c.StrongSolar.GPSReductionPerSecond = 1.5 },
			"strong solar GPS reduction per second must be between 0 and 1"},
		{"negative fix cost", func(c *PhenomenonConfig) { c.WaterLeakFixCostEnergy = -1 },
			"water leak fix cost must be finite and non-negative"},
		{"NaN multiplier", func(c *PhenomenonConfig) { c.OxygenLossCPSMultiplier = math.NaN() },
			"oxygen loss CPS multiplier must be finite and non-negative"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := DefaultConfig()
			test.change(&config.Phenomena)
			err := ValidatePhenomenonConfig(config)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ValidatePhenomenonConfig error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestRollPhenomenonDistribution(t *testing.T) {
	const rolls = 120_000
	config := DefaultConfig()
	rng := NewPhenomenonRandom(1)
	counts := map[PhenomenonType]int{}
	strong := 0
	for i := 0; i < rolls; i++ {
		roll, err := RollPhenomenon(rng, config)
		if err != nil {
			t.Fatal(err)
		}
		counts[roll.Type]++
		if roll.Type == PhenomenonSolarStorm && roll.Severity == SolarStrong {
			strong++
		}
	}

	solar := SolarChancePerRoll(config.Phenomena)
	other := (1 - solar) / 4
	tests := []struct {
		name string
		got  float64
		want float64
	}{
		{"solar", float64(counts[PhenomenonSolarStorm]) / rolls, solar},
		{"none", float64(counts[PhenomenonNone]) / rolls, other},
		{"water leak", float64(counts[PhenomenonWaterLeak]) / rolls, other},
		{"oxygen loss", float64(counts[PhenomenonOxygenLoss]) / rolls, other},
		{"rotten food", float64(counts[PhenomenonRottenFood]) / rolls, other},
		{"strong share of solar", float64(strong) / float64(counts[PhenomenonSolarStorm]), 0.38},
	}
	for _, test := range tests {
		t.Logf("%s share = %.4f (want %.4f)", test.name, test.got, test.want)
		if math.Abs(test.got-test.want) > 0.01 {
			t.Fatalf("%s share = %.4f, want %.4f ± 0.01", test.name, test.got, test.want)
		}
	}
}

func TestRollPhenomenonIsDeterministicPerSeed(t *testing.T) {
	sequence := func(seed int64) []PhenomenonRoll {
		rng := NewPhenomenonRandom(seed)
		rolls := make([]PhenomenonRoll, 50)
		for i := range rolls {
			roll, err := RollPhenomenon(rng, DefaultConfig())
			if err != nil {
				t.Fatal(err)
			}
			rolls[i] = roll
		}
		return rolls
	}
	if first, second := sequence(7), sequence(7); !reflect.DeepEqual(first, second) {
		t.Fatalf("same seed produced different sequences:\n%v\n%v", first, second)
	}
	if reflect.DeepEqual(sequence(7), sequence(8)) {
		t.Fatal("different seeds produced the same 50-roll sequence")
	}
}

func TestNewPhenomenonRandomLeavesWorldGenerationUnchanged(t *testing.T) {
	before, err := GenerateWorld(3, 2, DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	rng := NewPhenomenonRandom(3)
	for i := 0; i < 10; i++ {
		if _, err := RollPhenomenon(rng, DefaultConfig()); err != nil {
			t.Fatal(err)
		}
	}
	after, err := GenerateWorld(3, 2, DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatal("phenomenon rolls changed the generated world")
	}
}

func TestApplyPhenomenonRollStartsPersistentTypes(t *testing.T) {
	world, config := actionTestWorld(t, nil)
	phenomena := config.Phenomena
	tests := []struct {
		name string
		roll PhenomenonRoll
		want ActivePhenomenon
	}{
		{"strong solar", PhenomenonRoll{Type: PhenomenonSolarStorm, Severity: SolarStrong}, ActivePhenomenon{
			Type: PhenomenonSolarStorm, Severity: SolarStrong, StartedAtMs: 5000,
			FixCostEnergy: 30, GPSReductionPerSecond: 0.15,
		}},
		{"mild solar", PhenomenonRoll{Type: PhenomenonSolarStorm, Severity: SolarMild}, ActivePhenomenon{
			Type: PhenomenonSolarStorm, Severity: SolarMild, StartedAtMs: 5000,
			FixCostEnergy: 15, GPSReductionPerSecond: 0.05,
		}},
		{"water leak", PhenomenonRoll{Type: PhenomenonWaterLeak}, ActivePhenomenon{
			Type: PhenomenonWaterLeak, StartedAtMs: 5000,
			FixCostEnergy: phenomena.WaterLeakFixCostEnergy, CPSMultiplier: 1.5,
		}},
		{"oxygen loss", PhenomenonRoll{Type: PhenomenonOxygenLoss}, ActivePhenomenon{
			Type: PhenomenonOxygenLoss, StartedAtMs: 5000,
			FixCostEnergy: phenomena.OxygenLossFixCostEnergy, CPSMultiplier: 1.5,
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			next, active, result, err := ApplyPhenomenonRoll(world, ActivePhenomenon{}, test.roll, config, 5000)
			if err != nil {
				t.Fatal(err)
			}
			if !result.Started || result.Instant {
				t.Fatalf("result = %+v, want started", result)
			}
			if active != test.want {
				t.Fatalf("active = %+v, want %+v", active, test.want)
			}
			if !reflect.DeepEqual(next, world) {
				t.Fatal("persistent phenomenon changed the world at start")
			}
		})
	}
}

func TestApplyPhenomenonRollRottenFoodIsInstant(t *testing.T) {
	world, config := actionTestWorld(t, nil)
	world.TeamSize = 3
	world.Resources.Food.Pool.Current = 20

	next, active, result, err := ApplyPhenomenonRoll(
		world, ActivePhenomenon{}, PhenomenonRoll{Type: PhenomenonRottenFood}, config, 5000,
	)
	if err != nil {
		t.Fatal(err)
	}
	if active.Type != PhenomenonNone {
		t.Fatalf("rotten food occupied the active slot: %+v", active)
	}
	if !result.Instant || result.Started || result.FoodLoss != 15 {
		t.Fatalf("result = %+v, want instant loss 15", result)
	}
	if next.Resources.Food.Pool.Current != 5 {
		t.Fatalf("food pool = %v, want 5", next.Resources.Food.Pool.Current)
	}
	if world.Resources.Food.Pool.Current != 20 {
		t.Fatal("rotten food mutated the input world")
	}

	next, _, _, err = ApplyPhenomenonRoll(
		next, ActivePhenomenon{}, PhenomenonRoll{Type: PhenomenonRottenFood}, config, 6000,
	)
	if err != nil {
		t.Fatal(err)
	}
	if next.Resources.Food.Pool.Current != 0 {
		t.Fatalf("food pool = %v, want floor at 0", next.Resources.Food.Pool.Current)
	}
}

func TestApplyPhenomenonRollRejectsSecondActivePhenomenon(t *testing.T) {
	world, config := actionTestWorld(t, nil)
	active := ActivePhenomenon{Type: PhenomenonWaterLeak, CPSMultiplier: 1.5, FixCostEnergy: 10}
	next, gotActive, _, err := ApplyPhenomenonRoll(
		world, active, PhenomenonRoll{Type: PhenomenonRottenFood}, config, 5000,
	)
	if !errors.Is(err, ErrPhenomenonActive) {
		t.Fatalf("error = %v, want %v", err, ErrPhenomenonActive)
	}
	if gotActive != active || !reflect.DeepEqual(next, world) {
		t.Fatal("rejected roll changed state")
	}
}

func TestEffectiveResourcesSolarReachesZeroAndStays(t *testing.T) {
	world, _ := actionTestWorld(t, nil)
	base := world.Resources
	for _, test := range []struct {
		name string
		rate float64
	}{
		{"strong", 0.15},
		{"mild", 0.05},
	} {
		t.Run(test.name, func(t *testing.T) {
			active := ActivePhenomenon{Type: PhenomenonSolarStorm, GPSReductionPerSecond: test.rate}
			zeroAt := 1 / test.rate
			checks := []struct {
				elapsed float64
				factor  float64
			}{
				{0, 1},
				{zeroAt / 2, 0.5},
				{zeroAt, 0},
				{zeroAt * 3, 0},
			}
			for _, check := range checks {
				active.ElapsedSeconds = check.elapsed
				got := EffectiveResources(base, active)
				for _, resource := range []struct {
					key       ResourceKey
					got, base ResourceState
				}{
					{ResourceWater, got.Water, base.Water},
					{ResourceOxygen, got.Oxygen, base.Oxygen},
					{ResourceEnergy, got.Energy, base.Energy},
				} {
					want := resource.base.Stash.GPS * check.factor
					if math.Abs(resource.got.Stash.GPS-want) > 1e-12 {
						t.Fatalf("%s GPS at %.2fs = %v, want %v", resource.key, check.elapsed, resource.got.Stash.GPS, want)
					}
					if resource.got.CPS != resource.base.CPS {
						t.Fatalf("solar changed %s CPS", resource.key)
					}
				}
				if got.Food != base.Food {
					t.Fatal("solar changed food")
				}
			}
		})
	}
}

func TestEffectiveResourcesLeakAndOxygenMultiplyCPS(t *testing.T) {
	world, _ := actionTestWorld(t, nil)
	base := world.Resources

	leak := EffectiveResources(base, ActivePhenomenon{Type: PhenomenonWaterLeak, CPSMultiplier: 1.5})
	if leak.Water.CPS != base.Water.CPS*1.5 || leak.Oxygen != base.Oxygen ||
		leak.Water.Stash.GPS != base.Water.Stash.GPS {
		t.Fatalf("water leak resources = %+v, want only water CPS × 1.5", leak)
	}
	oxygen := EffectiveResources(base, ActivePhenomenon{Type: PhenomenonOxygenLoss, CPSMultiplier: 1.5})
	if oxygen.Oxygen.CPS != base.Oxygen.CPS*1.5 || oxygen.Water != base.Water {
		t.Fatalf("oxygen loss resources = %+v, want only oxygen CPS × 1.5", oxygen)
	}
	if none := EffectiveResources(base, ActivePhenomenon{}); none != base {
		t.Fatal("no phenomenon changed resources")
	}
}

func TestAdvanceEconomyWithPhenomenonUsesEffectiveRatesAndKeepsBase(t *testing.T) {
	world, _ := actionTestWorld(t, nil)
	resources := world.Resources
	active := ActivePhenomenon{Type: PhenomenonWaterLeak, CPSMultiplier: 1.5}

	if _, err := AdvanceEconomyWithPhenomenon(&resources, active, 2); err != nil {
		t.Fatal(err)
	}
	wantWater := world.Resources.Water.Pool.Current - world.Resources.Water.CPS*1.5*2
	if math.Abs(resources.Water.Pool.Current-wantWater) > 1e-9 {
		t.Fatalf("water pool = %v, want %v", resources.Water.Pool.Current, wantWater)
	}
	wantOxygen := world.Resources.Oxygen.Pool.Current - world.Resources.Oxygen.CPS*2
	if math.Abs(resources.Oxygen.Pool.Current-wantOxygen) > 1e-9 {
		t.Fatalf("oxygen pool = %v, want %v", resources.Oxygen.Pool.Current, wantOxygen)
	}
	if resources.Water.CPS != world.Resources.Water.CPS {
		t.Fatal("economy step stored the effective CPS instead of the base CPS")
	}
}

func TestFixPhenomenon(t *testing.T) {
	world, _ := actionTestWorld(t, nil)
	atStash := PlayerMovementState{X: 11.5, Y: 11.5, Facing: FacingNorth}
	offStash := PlayerMovementState{X: 10.5, Y: 11.5, Facing: FacingNorth}
	leak := ActivePhenomenon{Type: PhenomenonWaterLeak, CPSMultiplier: 1.5, FixCostEnergy: 10}

	tests := []struct {
		name   string
		energy float64
		player PlayerMovementState
		active ActivePhenomenon
		want   error
	}{
		{"no phenomenon", 20, atStash, ActivePhenomenon{}, ErrNoPhenomenon},
		{"wrong cell", 20, offStash, leak, ErrNotAtStash},
		{"not enough energy", 9.99, atStash, leak, ErrInsufficientEnergy},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := world
			input.Resources.Energy.Pool.Current = test.energy
			next, err := FixPhenomenon(input, test.player, test.active)
			if !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
			if !reflect.DeepEqual(next, input) {
				t.Fatal("rejected fix changed the world")
			}
		})
	}

	input := world
	input.Resources.Energy.Pool.Current = 10
	next, err := FixPhenomenon(input, atStash, leak)
	if err != nil {
		t.Fatal(err)
	}
	if next.Resources.Energy.Pool.Current != 0 {
		t.Fatalf("energy pool = %v, want 0 after paying exactly the cost", next.Resources.Energy.Pool.Current)
	}
	if input.Resources.Energy.Pool.Current != 10 {
		t.Fatal("fix mutated the input world")
	}
}

// TestSolarStormGapWithFixDelay estimates the wall-clock gap between solar
// storms. Rolls pause while a storm, leak, or oxygen loss is active, so each
// such event adds its time-to-fix to the cycle. Analytically the mean gap is
// (RollSeconds + pPersistent × fixDelay) / pSolar.
func TestSolarStormGapWithFixDelay(t *testing.T) {
	config := DefaultConfig()
	pSolar := SolarChancePerRoll(config.Phenomena)
	pPersistent := pSolar + 2*(1-pSolar)/4
	for _, fixDelay := range []float64{0, 15, 30, 60} {
		rng := NewPhenomenonRandom(11)
		clock, lastStorm, storms, gapSum := 0.0, 0.0, 0, 0.0
		for storms < 20_000 {
			clock += config.Phenomena.RollSeconds
			roll, err := RollPhenomenon(rng, config)
			if err != nil {
				t.Fatal(err)
			}
			switch roll.Type {
			case PhenomenonSolarStorm:
				gapSum += clock - lastStorm
				lastStorm = clock
				storms++
				clock += fixDelay
			case PhenomenonWaterLeak, PhenomenonOxygenLoss:
				clock += fixDelay
			}
		}
		simulated := gapSum / float64(storms)
		analytic := (config.Phenomena.RollSeconds + pPersistent*fixDelay) / pSolar
		t.Logf("fix delay %2.0fs: mean gap between solar storms %.0fs simulated, %.0fs analytic",
			fixDelay, simulated, analytic)
		if math.Abs(simulated-analytic)/analytic > 0.03 {
			t.Fatalf("fix delay %.0fs: simulated gap %.1fs, analytic %.1fs", fixDelay, simulated, analytic)
		}
	}
}
