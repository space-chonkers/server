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
	rates, err := CalculateEconomyRates(config, teamSize, baseSize*baseSize)
	if err != nil {
		return World{}, err
	}

	rng := newRandomSource(uint64(seed))
	constructions, investigation, spawns, err := generateMap(
		rng, config.InvestigationCount, config.InvestigationMinDistance,
	)
	if err != nil {
		return World{}, err
	}

	resources := Resources{
		Water:  initialResource(config.Water, teamSize, rates.Water, config.StartingPoolFraction),
		Oxygen: initialResource(config.Oxygen, teamSize, rates.Oxygen, config.StartingPoolFraction),
		Energy: initialResource(config.Energy, teamSize, rates.Energy, config.StartingPoolFraction),
		Food:   initialResource(config.Food, teamSize, rates.Food, config.StartingPoolFraction),
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

func initialResource(config ResourceConfig, teamSize int, rates ResourceRates, poolFraction float64) ResourceState {
	poolMax := config.PoolPerPlayer * float64(teamSize)
	return ResourceState{
		Pool: ResourcePool{
			Current: poolMax * poolFraction,
			Max:     poolMax,
		},
		Stash: ResourceStash{
			Current: 0,
			Max:     rates.StashMax,
			GPS:     rates.GPS,
		},
		CPS: rates.CPS,
	}
}
