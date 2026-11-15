package game

import (
	"fmt"
	"math"
)

// GenerateWorld creates the starting world. The seed is an internal input and
// is not part of the serialized world snapshot.
func GenerateWorld(seed int64, teamSize int, config Config) (World, error) {
	if teamSize < 1 || teamSize > 4 {
		return World{}, fmt.Errorf("team size must be between 1 and 4: %d", teamSize)
	}
	if err := validMapConfig(config.InvestigationCount, config.InvestigationMinDistance); err != nil {
		return World{}, err
	}
	if config.StartingPoolFraction < 0 || config.StartingPoolFraction > 1 ||
		math.IsNaN(config.StartingPoolFraction) || math.IsInf(config.StartingPoolFraction, 0) {
		return World{}, fmt.Errorf("starting pool fraction must be between 0 and 1: %v", config.StartingPoolFraction)
	}
	for _, entry := range []struct {
		name   string
		config ResourceConfig
	}{
		{"water", config.Water},
		{"oxygen", config.Oxygen},
		{"energy", config.Energy},
		{"food", config.Food},
	} {
		if err := validateResourceConfig(entry.name, entry.config); err != nil {
			return World{}, err
		}
	}

	rng := newRandomSource(uint64(seed))
	constructions, investigation, spawns, err := generateMap(
		rng, config.InvestigationCount, config.InvestigationMinDistance,
	)
	if err != nil {
		return World{}, err
	}

	resources := Resources{
		Water:  initialResource(config.Water, teamSize, baseSize*baseSize, config.StartingPoolFraction, false),
		Oxygen: initialResource(config.Oxygen, teamSize, baseSize*baseSize, config.StartingPoolFraction, false),
		Energy: initialResource(config.Energy, teamSize, baseSize*baseSize, config.StartingPoolFraction, false),
		Food:   initialResource(config.Food, teamSize, baseSize*baseSize, config.StartingPoolFraction, true),
	}

	return World{
		Grid:          GridSize{Width: worldWidth, Height: worldHeight},
		BaseCellCount: baseSize * baseSize,
		TeamSize:      teamSize,
		Constructions: constructions,
		Investigation: investigation,
		Spawns:        spawns,
		Resources:     resources,
	}, nil
}

func initialResource(config ResourceConfig, teamSize, baseCellCount int, poolFraction float64, food bool) ResourceState {
	poolMax := config.PoolPerPlayer * float64(teamSize)
	gps := config.GPSPerTile * float64(baseCellCount)
	cps := config.CPSPerPlayer*float64(teamSize) + config.CPSPerTile*float64(baseCellCount)
	if food {
		gps = config.GPSPerPlayer * float64(teamSize)
		cps = config.CPSPerPlayer * float64(teamSize)
	}
	return ResourceState{
		Pool: ResourcePool{
			Current: poolMax * poolFraction,
			Max:     poolMax,
		},
		Stash: ResourceStash{
			Current: 0,
			Max: config.StashBase +
				config.StashPerTile*float64(baseCellCount) +
				config.StashPerPlayer*float64(teamSize),
			GPS: gps,
		},
		CPS: cps,
	}
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
		if entry.value < 0 || math.IsNaN(entry.value) || math.IsInf(entry.value, 0) {
			return fmt.Errorf("%s %s must be finite and non-negative: %v", name, entry.name, entry.value)
		}
	}
	return nil
}
