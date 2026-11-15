package protocol

import (
	"encoding/json"
	"fmt"
)

// PhenomenonType is the phenomenon wire discriminant.
type PhenomenonType int

const (
	PhenomenonNoneType PhenomenonType = iota
	PhenomenonSolarType
	PhenomenonWaterLeakType
	PhenomenonOxygenLossType
	PhenomenonRottenFoodType
)

// Phenomenon is implemented by every phenomenon payload. Implementations carry
// only variable data; type, fixed costs, and resource identity are emitted by
// MarshalJSON. Decode through ActivePhenomenon or MatchPhenomenon.
type Phenomenon interface {
	PhenomenonType() PhenomenonType
}

// PhenomenonNone is the inactive phenomenon.
type PhenomenonNone struct{}

// PhenomenonType implements Phenomenon.
func (PhenomenonNone) PhenomenonType() PhenomenonType { return PhenomenonNoneType }

// PhenomenonSolar is the solar storm phenomenon.
type PhenomenonSolar struct {
	StartedAtMs           int64
	FixCostEnergy         float64
	GpsReductionPerSecond float64
}

// PhenomenonType implements Phenomenon.
func (PhenomenonSolar) PhenomenonType() PhenomenonType { return PhenomenonSolarType }

// PhenomenonWaterLeak is the water leak phenomenon.
type PhenomenonWaterLeak struct {
	StartedAtMs   int64
	FixCostEnergy float64
	CpsMultiplier float64
}

// PhenomenonType implements Phenomenon.
func (PhenomenonWaterLeak) PhenomenonType() PhenomenonType { return PhenomenonWaterLeakType }

// PhenomenonOxygenLoss is the oxygen loss phenomenon.
type PhenomenonOxygenLoss struct {
	StartedAtMs   int64
	FixCostEnergy float64
	CpsMultiplier float64
}

// PhenomenonType implements Phenomenon.
func (PhenomenonOxygenLoss) PhenomenonType() PhenomenonType { return PhenomenonOxygenLossType }

// PhenomenonRottenFood is the instant rotten food event.
type PhenomenonRottenFood struct {
	StartedAtMs int64
	FoodLoss    float64
}

// PhenomenonType implements Phenomenon.
func (PhenomenonRottenFood) PhenomenonType() PhenomenonType { return PhenomenonRottenFoodType }

type phenomenonWire struct {
	Type          PhenomenonType `json:"type"`
	StartedAtMs   int64          `json:"startedAtMs"`
	FixCostEnergy float64        `json:"fixCostEnergy"`
	Effects       any            `json:"effects"`
}

type solarEffectsWire struct {
	GpsReductionPerSecond float64         `json:"gpsReductionPerSecond"`
	Resources             []SolarResource `json:"resources"`
}

type cpsEffectsWire struct {
	CpsMultiplier float64     `json:"cpsMultiplier"`
	Resource      ResourceKey `json:"resource"`
}

type foodEffectsWire struct {
	FoodLoss float64 `json:"foodLoss"`
}

// MarshalJSON implements json.Marshaler.
func (PhenomenonNone) MarshalJSON() ([]byte, error) {
	return json.Marshal(phenomenonWire{Type: PhenomenonNoneType, Effects: struct{}{}})
}

// MarshalJSON implements json.Marshaler.
func (p PhenomenonSolar) MarshalJSON() ([]byte, error) {
	if p.FixCostEnergy < 0 {
		return nil, fmt.Errorf("protocol: phenomenon fix cost must not be negative")
	}
	if p.GpsReductionPerSecond < 0 || p.GpsReductionPerSecond > 1 {
		return nil, fmt.Errorf("protocol: solar reduction must be between 0 and 1")
	}
	return json.Marshal(phenomenonWire{
		Type:          PhenomenonSolarType,
		StartedAtMs:   p.StartedAtMs,
		FixCostEnergy: p.FixCostEnergy,
		Effects: solarEffectsWire{
			GpsReductionPerSecond: p.GpsReductionPerSecond,
			Resources:             []SolarResource{SolarWater, SolarOxygen, SolarEnergy},
		},
	})
}

// MarshalJSON implements json.Marshaler.
func (p PhenomenonWaterLeak) MarshalJSON() ([]byte, error) {
	if p.FixCostEnergy < 0 {
		return nil, fmt.Errorf("protocol: phenomenon fix cost must not be negative")
	}
	if p.CpsMultiplier < 0 {
		return nil, fmt.Errorf("protocol: cps multiplier must not be negative")
	}
	return json.Marshal(phenomenonWire{
		Type:          PhenomenonWaterLeakType,
		StartedAtMs:   p.StartedAtMs,
		FixCostEnergy: p.FixCostEnergy,
		Effects:       cpsEffectsWire{CpsMultiplier: p.CpsMultiplier, Resource: ResourceWater},
	})
}

// MarshalJSON implements json.Marshaler.
func (p PhenomenonOxygenLoss) MarshalJSON() ([]byte, error) {
	if p.FixCostEnergy < 0 {
		return nil, fmt.Errorf("protocol: phenomenon fix cost must not be negative")
	}
	if p.CpsMultiplier < 0 {
		return nil, fmt.Errorf("protocol: cps multiplier must not be negative")
	}
	return json.Marshal(phenomenonWire{
		Type:          PhenomenonOxygenLossType,
		StartedAtMs:   p.StartedAtMs,
		FixCostEnergy: p.FixCostEnergy,
		Effects:       cpsEffectsWire{CpsMultiplier: p.CpsMultiplier, Resource: ResourceOxygen},
	})
}

// MarshalJSON implements json.Marshaler.
func (p PhenomenonRottenFood) MarshalJSON() ([]byte, error) {
	if p.FoodLoss < 0 {
		return nil, fmt.Errorf("protocol: food loss must not be negative")
	}
	return json.Marshal(phenomenonWire{
		Type:        PhenomenonRottenFoodType,
		StartedAtMs: p.StartedAtMs,
		Effects:     foodEffectsWire{FoodLoss: p.FoodLoss},
	})
}

// ActivePhenomenon is the phenomenon union in match.state, match.frame, and
// phenomenon.fix. It excludes the instant rotten food event (type 4).
type ActivePhenomenon struct {
	Value Phenomenon
}

// MarshalJSON implements json.Marshaler.
func (p ActivePhenomenon) MarshalJSON() ([]byte, error) {
	if err := validateActivePhenomenon(p.Value); err != nil {
		return nil, err
	}
	return json.Marshal(p.Value)
}

// UnmarshalJSON implements json.Unmarshaler.
func (p *ActivePhenomenon) UnmarshalJSON(data []byte) error {
	value, err := decodePhenomenon(data, false)
	if err != nil {
		return err
	}
	p.Value = value
	return nil
}

// MarshalJSON implements json.Marshaler.
func (m MatchPhenomenon) MarshalJSON() ([]byte, error) {
	if err := validatePhenomenonEvent(m.Action, m.Phenomenon); err != nil {
		return nil, err
	}
	type wire MatchPhenomenon
	return json.Marshal(wire(m))
}

// UnmarshalJSON implements json.Unmarshaler.
func (m *MatchPhenomenon) UnmarshalJSON(data []byte) error {
	var wire struct {
		MatchID    string                `json:"matchId"`
		Action     MatchPhenomenonAction `json:"action"`
		Phenomenon json.RawMessage       `json:"phenomenon"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	phenomenon, err := decodePhenomenon(wire.Phenomenon, true)
	if err != nil {
		return err
	}
	if err := validatePhenomenonEvent(wire.Action, phenomenon); err != nil {
		return err
	}
	m.MatchID = wire.MatchID
	m.Action = wire.Action
	m.Phenomenon = phenomenon
	return nil
}

func validateActivePhenomenon(value Phenomenon) error {
	if value == nil {
		return fmt.Errorf("protocol: missing phenomenon")
	}
	kind := value.PhenomenonType()
	if kind < PhenomenonNoneType || kind > PhenomenonOxygenLossType {
		return fmt.Errorf("protocol: phenomenon type %d is not active", kind)
	}
	return nil
}

func validatePhenomenonEvent(action MatchPhenomenonAction, value Phenomenon) error {
	if value == nil {
		return fmt.Errorf("protocol: missing phenomenon")
	}
	kind := value.PhenomenonType()
	switch action {
	case PhenomenonActionStarted:
		if kind == PhenomenonSolarType || kind == PhenomenonWaterLeakType || kind == PhenomenonOxygenLossType {
			return nil
		}
	case PhenomenonActionResolved:
		if kind == PhenomenonNoneType {
			return nil
		}
	case PhenomenonActionInstant:
		if kind == PhenomenonRottenFoodType {
			return nil
		}
	default:
		return fmt.Errorf("protocol: unknown phenomenon action %q", action)
	}
	return fmt.Errorf("protocol: phenomenon type %d cannot be %q", kind, action)
}

func decodePhenomenon(data []byte, allowRottenFood bool) (Phenomenon, error) {
	var wire struct {
		Type          *PhenomenonType `json:"type"`
		StartedAtMs   int64           `json:"startedAtMs"`
		FixCostEnergy float64         `json:"fixCostEnergy"`
		Effects       json.RawMessage `json:"effects"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return nil, err
	}
	if wire.Type == nil {
		return nil, fmt.Errorf("protocol: phenomenon type is required")
	}
	if wire.FixCostEnergy < 0 {
		return nil, fmt.Errorf("protocol: phenomenon fix cost must not be negative")
	}

	switch *wire.Type {
	case PhenomenonNoneType:
		if wire.StartedAtMs != 0 || wire.FixCostEnergy != 0 {
			return nil, fmt.Errorf("protocol: inactive phenomenon must be zero")
		}
		return PhenomenonNone{}, nil
	case PhenomenonSolarType:
		var effects solarEffectsWire
		if err := json.Unmarshal(wire.Effects, &effects); err != nil {
			return nil, err
		}
		if effects.GpsReductionPerSecond < 0 || effects.GpsReductionPerSecond > 1 {
			return nil, fmt.Errorf("protocol: solar reduction must be between 0 and 1")
		}
		if err := validateSolarResources(effects.Resources); err != nil {
			return nil, err
		}
		return PhenomenonSolar{
			StartedAtMs:           wire.StartedAtMs,
			FixCostEnergy:         wire.FixCostEnergy,
			GpsReductionPerSecond: effects.GpsReductionPerSecond,
		}, nil
	case PhenomenonWaterLeakType, PhenomenonOxygenLossType:
		var effects cpsEffectsWire
		if err := json.Unmarshal(wire.Effects, &effects); err != nil {
			return nil, err
		}
		resource := ResourceWater
		if *wire.Type == PhenomenonOxygenLossType {
			resource = ResourceOxygen
		}
		if effects.Resource != resource {
			return nil, fmt.Errorf("protocol: phenomenon must target %s", resource)
		}
		if effects.CpsMultiplier < 0 {
			return nil, fmt.Errorf("protocol: cps multiplier must not be negative")
		}
		if *wire.Type == PhenomenonWaterLeakType {
			return PhenomenonWaterLeak{
				StartedAtMs:   wire.StartedAtMs,
				FixCostEnergy: wire.FixCostEnergy,
				CpsMultiplier: effects.CpsMultiplier,
			}, nil
		}
		return PhenomenonOxygenLoss{
			StartedAtMs:   wire.StartedAtMs,
			FixCostEnergy: wire.FixCostEnergy,
			CpsMultiplier: effects.CpsMultiplier,
		}, nil
	case PhenomenonRottenFoodType:
		if !allowRottenFood {
			return nil, fmt.Errorf("protocol: rotten food is not an active phenomenon")
		}
		if wire.FixCostEnergy != 0 {
			return nil, fmt.Errorf("protocol: rotten food must have zero fix cost")
		}
		var effects foodEffectsWire
		if err := json.Unmarshal(wire.Effects, &effects); err != nil {
			return nil, err
		}
		if effects.FoodLoss < 0 {
			return nil, fmt.Errorf("protocol: food loss must not be negative")
		}
		return PhenomenonRottenFood{
			StartedAtMs: wire.StartedAtMs,
			FoodLoss:    effects.FoodLoss,
		}, nil
	default:
		return nil, fmt.Errorf("protocol: unknown phenomenon type %d", *wire.Type)
	}
}

func validateSolarResources(resources []SolarResource) error {
	if len(resources) != 3 {
		return fmt.Errorf("protocol: solar resources must contain three entries")
	}
	seen := make(map[SolarResource]bool, len(resources))
	for _, resource := range resources {
		switch resource {
		case SolarWater, SolarOxygen, SolarEnergy:
		default:
			return fmt.Errorf("protocol: invalid solar resource %q", resource)
		}
		if seen[resource] {
			return fmt.Errorf("protocol: duplicate solar resource %q", resource)
		}
		seen[resource] = true
	}
	return nil
}
