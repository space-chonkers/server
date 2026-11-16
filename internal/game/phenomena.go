package game

import (
	"errors"
	"fmt"
	"math"
	"time"
)

var (
	ErrNoPhenomenon       = errors.New("no phenomenon is active")
	ErrPhenomenonActive   = errors.New("a phenomenon is already active")
	ErrInsufficientEnergy = errors.New("insufficient energy")
)

// phenomenonSeedSalt keeps phenomenon rolls separate from map generation.
const phenomenonSeedSalt = 0x5048454e4f4d454e // "PHENOMEN"

// PhenomenonType is the phenomenon kind; values match the wire enum.
type PhenomenonType int

const (
	PhenomenonNone PhenomenonType = iota
	PhenomenonSolarStorm
	PhenomenonWaterLeak
	PhenomenonOxygenLoss
	PhenomenonRottenFood
)

// SolarSeverity is the strength of a solar storm.
type SolarSeverity string

const (
	SolarMild   SolarSeverity = "mild"
	SolarStrong SolarSeverity = "strong"
)

// SolarLevel is the GPS reduction and fix cost of one solar severity.
type SolarLevel struct {
	GPSReductionPerSecond float64
	FixCostEnergy         float64
}

// PhenomenonWeights are the relative weights of the non-solar roll outcomes.
type PhenomenonWeights struct {
	None       float64
	WaterLeak  float64
	OxygenLoss float64
	RottenFood float64
}

// PhenomenonConfig holds the roll timing, probabilities, and effects of
// phenomena.
type PhenomenonConfig struct {
	// FirstRollDelaySeconds is the time after play starts before the first roll.
	FirstRollDelaySeconds float64
	// RollSeconds is the time between rolls.
	RollSeconds float64
	// SecondsPerDay is the game length of one Mars day.
	SecondsPerDay float64
	// SolarStormChancePerDay is the chance of a solar storm per day.
	SolarStormChancePerDay float64
	// StrongShare is the share of solar storms that are strong.
	StrongShare float64
	StrongSolar SolarLevel
	MildSolar   SolarLevel
	Weights     PhenomenonWeights
	// The CPS multipliers scale pool drain until the phenomenon is fixed.
	WaterLeakCPSMultiplier  float64
	WaterLeakFixCostEnergy  float64
	OxygenLossCPSMultiplier float64
	OxygenLossFixCostEnergy float64
	// RottenFoodPerPlayer is removed from the food pool per team member.
	RottenFoodPerPlayer float64
}

// DefaultPhenomenonConfig returns the default phenomenon tuning values.
func DefaultPhenomenonConfig() PhenomenonConfig {
	return PhenomenonConfig{
		FirstRollDelaySeconds:   120,
		RollSeconds:             30,
		SecondsPerDay:           60,
		SolarStormChancePerDay:  0.31,
		StrongShare:             0.38,
		StrongSolar:             SolarLevel{GPSReductionPerSecond: 0.15, FixCostEnergy: 30},
		MildSolar:               SolarLevel{GPSReductionPerSecond: 0.05, FixCostEnergy: 15},
		Weights:                 PhenomenonWeights{None: 1, WaterLeak: 1, OxygenLoss: 1, RottenFood: 1},
		WaterLeakCPSMultiplier:  1.5,
		WaterLeakFixCostEnergy:  10,
		OxygenLossCPSMultiplier: 1.5,
		OxygenLossFixCostEnergy: 10,
		RottenFoodPerPlayer:     5,
	}
}

// ActivePhenomenon is the phenomenon in the active slot; the zero value is
// none.
type ActivePhenomenon struct {
	Type                  PhenomenonType
	Severity              SolarSeverity
	StartedAtMs           int64
	FixCostEnergy         float64
	GPSReductionPerSecond float64
	CPSMultiplier         float64
	// ElapsedSeconds is the simulated time since the phenomenon started.
	ElapsedSeconds float64
}

// PhenomenonRoll is the outcome of one roll.
type PhenomenonRoll struct {
	Type     PhenomenonType
	Severity SolarSeverity
}

// PhenomenonRollResult reports whether a roll started a phenomenon or applied
// an instant food loss.
type PhenomenonRollResult struct {
	Started  bool
	Instant  bool
	FoodLoss float64
}

// PhenomenonRandom is the seeded random source for phenomenon rolls.
type PhenomenonRandom struct {
	source *randomSource
}

// NewPhenomenonRandom returns a roll source seeded from the world seed.
func NewPhenomenonRandom(worldSeed int64) *PhenomenonRandom {
	return &PhenomenonRandom{source: newRandomSource(uint64(worldSeed) ^ phenomenonSeedSalt)}
}

// SolarChancePerRoll returns the chance that one roll is a solar storm.
func SolarChancePerRoll(config PhenomenonConfig) float64 {
	daysPerRoll := config.RollSeconds / config.SecondsPerDay
	return 1 - math.Pow(1-config.SolarStormChancePerDay, daysPerRoll)
}

// ValidatePhenomenonConfig verifies roll timing, probabilities, weights, and
// effect values.
func ValidatePhenomenonConfig(config Config) error {
	phenomena := config.Phenomena
	if !validDurationSeconds(phenomena.FirstRollDelaySeconds, true) {
		return fmt.Errorf("first phenomenon roll delay must be representable as a duration and non-negative: %v",
			phenomena.FirstRollDelaySeconds)
	}
	if !validDurationSeconds(phenomena.RollSeconds, false) {
		return fmt.Errorf("phenomenon roll seconds must be representable as a positive duration: %v",
			phenomena.RollSeconds)
	}
	if phenomena.SecondsPerDay <= 0 || !finite(phenomena.SecondsPerDay) {
		return fmt.Errorf("seconds per day must be finite and positive: %v", phenomena.SecondsPerDay)
	}
	for _, share := range []struct {
		name  string
		value float64
	}{
		{"solar storm chance per day", phenomena.SolarStormChancePerDay},
		{"strong solar share", phenomena.StrongShare},
	} {
		if share.value < 0 || share.value > 1 || !finite(share.value) {
			return fmt.Errorf("%s must be between 0 and 1: %v", share.name, share.value)
		}
	}
	for _, level := range []struct {
		name  string
		value SolarLevel
	}{
		{"strong", phenomena.StrongSolar},
		{"mild", phenomena.MildSolar},
	} {
		if level.value.GPSReductionPerSecond < 0 || level.value.GPSReductionPerSecond > 1 ||
			!finite(level.value.GPSReductionPerSecond) {
			return fmt.Errorf("%s solar GPS reduction per second must be between 0 and 1: %v",
				level.name, level.value.GPSReductionPerSecond)
		}
		if level.value.FixCostEnergy < 0 || !finite(level.value.FixCostEnergy) {
			return fmt.Errorf("%s solar fix cost must be finite and non-negative: %v",
				level.name, level.value.FixCostEnergy)
		}
	}
	for _, value := range []struct {
		name  string
		value float64
	}{
		{"water leak CPS multiplier", phenomena.WaterLeakCPSMultiplier},
		{"water leak fix cost", phenomena.WaterLeakFixCostEnergy},
		{"oxygen loss CPS multiplier", phenomena.OxygenLossCPSMultiplier},
		{"oxygen loss fix cost", phenomena.OxygenLossFixCostEnergy},
		{"rotten food per player", phenomena.RottenFoodPerPlayer},
	} {
		if value.value < 0 || !finite(value.value) {
			return fmt.Errorf("%s must be finite and non-negative: %v", value.name, value.value)
		}
	}

	weights := phenomena.Weights
	sum := 0.0
	for _, weight := range []struct {
		name  string
		value float64
	}{
		{"none", weights.None},
		{"water leak", weights.WaterLeak},
		{"oxygen loss", weights.OxygenLoss},
		{"rotten food", weights.RottenFood},
	} {
		if weight.value < 0 || !finite(weight.value) {
			return fmt.Errorf("phenomenon weight %s must be finite and non-negative: %v",
				weight.name, weight.value)
		}
		sum += weight.value
	}
	if sum <= 0 || !finite(sum) {
		return fmt.Errorf("phenomenon weights must have a finite positive sum: %v", sum)
	}

	chance := SolarChancePerRoll(phenomena)
	if chance < 0 || chance > 1 || !finite(chance) {
		return fmt.Errorf("solar chance per roll must be between 0 and 1: %v", chance)
	}
	return nil
}

func validDurationSeconds(seconds float64, allowZero bool) bool {
	if !finite(seconds) || seconds < 0 {
		return false
	}
	if seconds == 0 {
		return allowZero
	}

	nanoseconds := seconds * float64(time.Second)
	maxDuration := float64(time.Duration(1<<63 - 1))
	return nanoseconds >= 1 && nanoseconds < maxDuration
}

// RollPhenomenon draws the outcome of one roll.
func RollPhenomenon(rng *PhenomenonRandom, config Config) (PhenomenonRoll, error) {
	if rng == nil {
		return PhenomenonRoll{}, fmt.Errorf("phenomenon random source must not be nil")
	}
	if err := ValidatePhenomenonConfig(config); err != nil {
		return PhenomenonRoll{}, err
	}
	phenomena := config.Phenomena
	if rng.source.float64() < SolarChancePerRoll(phenomena) {
		severity := SolarMild
		if rng.source.float64() < phenomena.StrongShare {
			severity = SolarStrong
		}
		return PhenomenonRoll{Type: PhenomenonSolarStorm, Severity: severity}, nil
	}

	weights := phenomena.Weights
	outcomes := []struct {
		kind   PhenomenonType
		weight float64
	}{
		{PhenomenonNone, weights.None},
		{PhenomenonWaterLeak, weights.WaterLeak},
		{PhenomenonOxygenLoss, weights.OxygenLoss},
		{PhenomenonRottenFood, weights.RottenFood},
	}
	sum := weights.None + weights.WaterLeak + weights.OxygenLoss + weights.RottenFood
	draw := rng.source.float64() * sum
	for _, outcome := range outcomes {
		if outcome.weight > 0 && draw < outcome.weight {
			return PhenomenonRoll{Type: outcome.kind}, nil
		}
		draw -= outcome.weight
	}
	for i := len(outcomes) - 1; i >= 0; i-- {
		if outcomes[i].weight > 0 {
			return PhenomenonRoll{Type: outcomes[i].kind}, nil
		}
	}
	return PhenomenonRoll{}, fmt.Errorf("phenomenon weights must have a positive sum")
}

// ApplyPhenomenonRoll starts the rolled phenomenon, or applies rotten food to
// the food pool. The input world is unchanged on error.
func ApplyPhenomenonRoll(
	world World,
	active ActivePhenomenon,
	roll PhenomenonRoll,
	config Config,
	nowMs int64,
) (World, ActivePhenomenon, PhenomenonRollResult, error) {
	if active.Type != PhenomenonNone {
		return world, active, PhenomenonRollResult{}, ErrPhenomenonActive
	}
	if err := ValidatePhenomenonConfig(config); err != nil {
		return world, active, PhenomenonRollResult{}, err
	}
	phenomena := config.Phenomena

	switch roll.Type {
	case PhenomenonNone:
		return world, active, PhenomenonRollResult{}, nil
	case PhenomenonSolarStorm:
		level := phenomena.MildSolar
		switch roll.Severity {
		case SolarMild:
		case SolarStrong:
			level = phenomena.StrongSolar
		default:
			return world, active, PhenomenonRollResult{},
				fmt.Errorf("unknown solar severity %q", roll.Severity)
		}
		return world, ActivePhenomenon{
			Type: PhenomenonSolarStorm, Severity: roll.Severity, StartedAtMs: nowMs,
			FixCostEnergy: level.FixCostEnergy, GPSReductionPerSecond: level.GPSReductionPerSecond,
		}, PhenomenonRollResult{Started: true}, nil
	case PhenomenonWaterLeak:
		return world, ActivePhenomenon{
			Type: PhenomenonWaterLeak, StartedAtMs: nowMs,
			FixCostEnergy: phenomena.WaterLeakFixCostEnergy, CPSMultiplier: phenomena.WaterLeakCPSMultiplier,
		}, PhenomenonRollResult{Started: true}, nil
	case PhenomenonOxygenLoss:
		return world, ActivePhenomenon{
			Type: PhenomenonOxygenLoss, StartedAtMs: nowMs,
			FixCostEnergy: phenomena.OxygenLossFixCostEnergy, CPSMultiplier: phenomena.OxygenLossCPSMultiplier,
		}, PhenomenonRollResult{Started: true}, nil
	case PhenomenonRottenFood:
		if world.TeamSize < 1 || world.TeamSize > 4 {
			return world, active, PhenomenonRollResult{},
				fmt.Errorf("team size must be between 1 and 4: %d", world.TeamSize)
		}
		loss := phenomena.RottenFoodPerPlayer * float64(world.TeamSize)
		next := world
		next.Resources.Food.Pool.Current = math.Max(next.Resources.Food.Pool.Current-loss, 0)
		return next, active, PhenomenonRollResult{Instant: true, FoodLoss: loss}, nil
	default:
		return world, active, PhenomenonRollResult{}, fmt.Errorf("unknown phenomenon type %d", roll.Type)
	}
}

// EffectiveResources returns resources with the active phenomenon's effect
// applied to GPS and CPS.
func EffectiveResources(resources Resources, active ActivePhenomenon) Resources {
	switch active.Type {
	case PhenomenonSolarStorm:
		factor := math.Max(1-active.GPSReductionPerSecond*active.ElapsedSeconds, 0)
		resources.Water.Stash.GPS *= factor
		resources.Oxygen.Stash.GPS *= factor
		resources.Energy.Stash.GPS *= factor
	case PhenomenonWaterLeak:
		resources.Water.CPS *= active.CPSMultiplier
	case PhenomenonOxygenLoss:
		resources.Oxygen.CPS *= active.CPSMultiplier
	}
	return resources
}

// AdvanceEconomyWithPhenomenon advances the economy at the effective rates and
// keeps the base rates stored.
func AdvanceEconomyWithPhenomenon(
	resources *Resources,
	active ActivePhenomenon,
	dtSeconds float64,
) (EconomyResult, error) {
	if resources == nil {
		return EconomyResult{}, fmt.Errorf("resources must not be nil")
	}
	effective := EffectiveResources(*resources, active)
	result, err := AdvanceEconomy(&effective, dtSeconds)
	if err != nil {
		return EconomyResult{}, err
	}
	next := *resources
	next.Water = withAmounts(next.Water, effective.Water)
	next.Oxygen = withAmounts(next.Oxygen, effective.Oxygen)
	next.Energy = withAmounts(next.Energy, effective.Energy)
	next.Food = withAmounts(next.Food, effective.Food)
	*resources = next
	return result, nil
}

// FixPhenomenon pays the fix cost from the energy pool for a player on the
// stash. The input world is unchanged on error.
func FixPhenomenon(
	world World,
	player PlayerMovementState,
	active ActivePhenomenon,
) (World, error) {
	if active.Type == PhenomenonNone {
		return world, ErrNoPhenomenon
	}
	if !playerAtStash(world, player) {
		return world, ErrNotAtStash
	}
	if world.Resources.Energy.Pool.Current < active.FixCostEnergy {
		return world, ErrInsufficientEnergy
	}
	next := world
	next.Resources.Energy.Pool.Current -= active.FixCostEnergy
	return next, nil
}

func playerAtStash(world World, player PlayerMovementState) bool {
	playerCell := Cell{CellX: int(math.Floor(player.X)), CellY: int(math.Floor(player.Y))}
	for _, construction := range world.Constructions {
		if construction.Kind == ConstructionStash &&
			construction.CellX == playerCell.CellX && construction.CellY == playerCell.CellY {
			return true
		}
	}
	return false
}

func withAmounts(base, advanced ResourceState) ResourceState {
	base.Pool.Current = advanced.Pool.Current
	base.Stash.Current = advanced.Stash.Current
	return base
}
