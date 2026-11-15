package game

import (
	"fmt"
	"math"
)

const (
	minEconomyBaseCells = baseSize * baseSize
	maxEconomyBaseCells = worldWidth * worldHeight
)

// ValidateEconomyConfig verifies the configured rates for every supported
// team size and possible base size on the world grid.
func ValidateEconomyConfig(config Config) error {
	resources := []struct {
		key    ResourceKey
		config ResourceConfig
	}{
		{ResourceWater, config.Water},
		{ResourceOxygen, config.Oxygen},
		{ResourceEnergy, config.Energy},
		{ResourceFood, config.Food},
	}

	for _, resource := range resources {
		if err := validateResourceConfig(string(resource.key), resource.config); err != nil {
			return err
		}
		if resource.config.PoolPerPlayer <= 0 {
			return fmt.Errorf("%s pool capacity per player must be positive", resource.key)
		}
		if !finite(resource.config.PoolPerPlayer * 4) {
			return fmt.Errorf("%s pool capacity overflows at maximum team size", resource.key)
		}
	}

	// Each GPS/CPS difference is linear in base-cell count (or constant for
	// food), so checking both endpoints proves the inequality across the range.
	// Rate and capacity outputs are non-negative linear formulas, so their
	// finite maximum is also at an endpoint.
	for teamSize := 1; teamSize <= 4; teamSize++ {
		for _, baseCells := range []int{minEconomyBaseCells, maxEconomyBaseCells} {
			rates := deriveEconomyRates(config, teamSize, baseCells)
			for _, entry := range []struct {
				key   ResourceKey
				rates ResourceRates
			}{
				{ResourceWater, rates.Water},
				{ResourceOxygen, rates.Oxygen},
				{ResourceEnergy, rates.Energy},
				{ResourceFood, rates.Food},
			} {
				if !finite(entry.rates.StashMax) || !finite(entry.rates.GPS) || !finite(entry.rates.CPS) {
					return fmt.Errorf(
						"%s economy rates overflow for team size %d and base cells %d",
						entry.key, teamSize, baseCells,
					)
				}
				if entry.rates.CPS <= entry.rates.GPS {
					return fmt.Errorf(
						"%s CPS must exceed GPS for team size %d and base cells %d (CPS %.6g, GPS %.6g)",
						entry.key, teamSize, baseCells, entry.rates.CPS, entry.rates.GPS,
					)
				}
			}
		}
	}
	return nil
}

// CalculateEconomyRates derives base resource rates for a world snapshot.
// teamSize is the frozen starting team size; baseCellCount includes the stash.
func CalculateEconomyRates(config Config, teamSize, baseCellCount int) (EconomyRates, error) {
	if teamSize < 1 || teamSize > 4 {
		return EconomyRates{}, fmt.Errorf("team size must be between 1 and 4: %d", teamSize)
	}
	if baseCellCount < minEconomyBaseCells || baseCellCount > maxEconomyBaseCells {
		return EconomyRates{}, fmt.Errorf(
			"base cell count must be between %d and %d: %d",
			minEconomyBaseCells, maxEconomyBaseCells, baseCellCount,
		)
	}
	if err := ValidateEconomyConfig(config); err != nil {
		return EconomyRates{}, err
	}
	return deriveEconomyRates(config, teamSize, baseCellCount), nil
}

func deriveEconomyRates(config Config, teamSize, baseCellCount int) EconomyRates {
	return EconomyRates{
		Water:  deriveResourceRates(config.Water, teamSize, baseCellCount, false),
		Oxygen: deriveResourceRates(config.Oxygen, teamSize, baseCellCount, false),
		Energy: deriveResourceRates(config.Energy, teamSize, baseCellCount, false),
		Food:   deriveResourceRates(config.Food, teamSize, baseCellCount, true),
	}
}

func deriveResourceRates(config ResourceConfig, teamSize, baseCellCount int, food bool) ResourceRates {
	gps := config.GPSPerTile * float64(baseCellCount)
	cps := config.CPSPerPlayer*float64(teamSize) + config.CPSPerTile*float64(baseCellCount)
	if food {
		gps = config.GPSPerPlayer * float64(teamSize)
		cps = config.CPSPerPlayer * float64(teamSize)
	}
	return ResourceRates{
		StashMax: config.StashBase +
			config.StashPerTile*float64(baseCellCount) +
			config.StashPerPlayer*float64(teamSize),
		GPS: gps,
		CPS: cps,
	}
}

// AdvanceEconomy applies one elapsed-time step to the shared resource state.
// Stash overflow is discarded. Every pool at zero after the step is reported.
func AdvanceEconomy(resources *Resources, dtSeconds float64) (EconomyResult, error) {
	if resources == nil {
		return EconomyResult{}, fmt.Errorf("resources must not be nil")
	}
	if dtSeconds < 0 || !finite(dtSeconds) {
		return EconomyResult{}, fmt.Errorf("elapsed seconds must be finite and non-negative: %v", dtSeconds)
	}

	next := *resources
	states := []struct {
		key   ResourceKey
		state *ResourceState
	}{
		{ResourceWater, &next.Water},
		{ResourceOxygen, &next.Oxygen},
		{ResourceEnergy, &next.Energy},
		{ResourceFood, &next.Food},
	}
	for _, entry := range states {
		if err := validateResourceState(entry.key, *entry.state); err != nil {
			return EconomyResult{}, err
		}
	}

	result := EconomyResult{DepletedResources: make([]ResourceKey, 0, len(states))}
	for _, entry := range states {
		entry.state.Stash.Current = math.Min(
			entry.state.Stash.Current+entry.state.Stash.GPS*dtSeconds,
			entry.state.Stash.Max,
		)
		entry.state.Pool.Current = math.Max(
			entry.state.Pool.Current-entry.state.CPS*dtSeconds,
			0,
		)
		if entry.state.Pool.Current == 0 {
			result.DepletedResources = append(result.DepletedResources, entry.key)
		}
	}
	*resources = next
	return result, nil
}

func validateResourceState(key ResourceKey, state ResourceState) error {
	values := []struct {
		name  string
		value float64
	}{
		{"pool current", state.Pool.Current},
		{"pool max", state.Pool.Max},
		{"stash current", state.Stash.Current},
		{"stash max", state.Stash.Max},
		{"stash GPS", state.Stash.GPS},
		{"pool CPS", state.CPS},
	}
	for _, entry := range values {
		if entry.value < 0 || !finite(entry.value) {
			return fmt.Errorf("%s %s must be finite and non-negative: %v", key, entry.name, entry.value)
		}
	}
	if state.Pool.Current > state.Pool.Max {
		return fmt.Errorf("%s pool current exceeds its maximum", key)
	}
	if state.Stash.Current > state.Stash.Max {
		return fmt.Errorf("%s stash current exceeds its maximum", key)
	}
	return nil
}

func validateResourceConfig(name string, config ResourceConfig) error {
	values := []struct {
		name  string
		value float64
	}{
		{"pool per player", config.PoolPerPlayer},
		{"stash base", config.StashBase},
		{"stash per tile", config.StashPerTile},
		{"stash per player", config.StashPerPlayer},
		{"GPS per tile", config.GPSPerTile},
		{"GPS per player", config.GPSPerPlayer},
		{"CPS per player", config.CPSPerPlayer},
		{"CPS per tile", config.CPSPerTile},
	}
	for _, entry := range values {
		if entry.value < 0 || !finite(entry.value) {
			return fmt.Errorf("%s %s must be finite and non-negative: %v", name, entry.name, entry.value)
		}
	}
	return nil
}

func finite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}
