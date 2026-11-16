package game

import (
	"errors"
	"fmt"
	"math"
)

var (
	ErrNotOnBase            = errors.New("player is not on the base")
	ErrNotAtStash           = errors.New("player is not at the stash")
	ErrOutOfBounds          = errors.New("target is outside the grid")
	ErrNotInFront           = errors.New("target is not in front of the player")
	ErrCellOccupied         = errors.New("target cell is occupied")
	ErrInsufficientResource = errors.New("insufficient resources")
)

// BuildCost is paid from the resource pools to add one base tile.
type BuildCost struct {
	Water  float64
	Oxygen float64
	Energy float64
}

// ResourceAmounts reports one action's amount for each shared resource.
type ResourceAmounts struct {
	Water  float64
	Oxygen float64
	Energy float64
	Food   float64
}

// CollectResult reports stash resources moved to pools and overflow discarded.
type CollectResult struct {
	Collected ResourceAmounts
	Lost      ResourceAmounts
}

// BuildResult reports the added tile and whether it covers every goal.
type BuildResult struct {
	Construction Construction
	Won          bool
}

// InsufficientResourcesError lists the build costs that the pools cannot pay.
type InsufficientResourcesError struct {
	Missing []ResourceKey
}

func (e *InsufficientResourcesError) Error() string {
	return fmt.Sprintf("%v: %v", ErrInsufficientResource, e.Missing)
}

func (e *InsufficientResourcesError) Unwrap() error { return ErrInsufficientResource }

const stashCollectMaxDistance = 0.70

// CollectAtStash transfers each stashed resource into its pool. The input
// world is unchanged when collection fails.
func CollectAtStash(
	world World,
	player PlayerMovementState,
) (World, CollectResult, error) {
	playerCell := Cell{CellX: int(math.Floor(player.X)), CellY: int(math.Floor(player.Y))}
	atStash := false
	for _, construction := range world.Constructions {
		if construction.Kind != ConstructionStash {
			continue
		}
		if construction.CellX == playerCell.CellX && construction.CellY == playerCell.CellY {
			atStash = true
			break
		}
		dx := player.X - (float64(construction.CellX) + 0.5)
		dy := player.Y - (float64(construction.CellY) + 0.5)
		if dx*dx+dy*dy <= stashCollectMaxDistance*stashCollectMaxDistance {
			atStash = true
			break
		}
	}
	if !atStash {
		return world, CollectResult{}, ErrNotAtStash
	}

	result := CollectResult{}
	world.Resources.Water, result.Collected.Water, result.Lost.Water =
		collectResource(world.Resources.Water)
	world.Resources.Oxygen, result.Collected.Oxygen, result.Lost.Oxygen =
		collectResource(world.Resources.Oxygen)
	world.Resources.Energy, result.Collected.Energy, result.Lost.Energy =
		collectResource(world.Resources.Energy)
	world.Resources.Food, result.Collected.Food, result.Lost.Food =
		collectResource(world.Resources.Food)
	return world, result, nil
}

// BuildBaseTile adds one paid tile at the requested cell. The input world is
// unchanged if validation or rate calculation fails.
func BuildBaseTile(
	world World,
	player PlayerMovementState,
	target Cell,
	config Config,
) (World, BuildResult, error) {
	playerCell := Cell{CellX: int(math.Floor(player.X)), CellY: int(math.Floor(player.Y))}
	if !containsCell(world.Constructions, playerCell) {
		return world, BuildResult{}, ErrNotOnBase
	}
	if !inBounds(float64(target.CellX), float64(target.CellY)) {
		return world, BuildResult{}, ErrOutOfBounds
	}

	front, err := PlayerFrontCell(player)
	if err != nil {
		return world, BuildResult{}, err
	}
	if target != front {
		return world, BuildResult{}, ErrNotInFront
	}
	if containsCell(world.Constructions, target) {
		return world, BuildResult{}, ErrCellOccupied
	}

	cost := config.BuildCost
	missing := missingBuildCosts(world.Resources, cost)
	if len(missing) > 0 {
		return world, BuildResult{}, &InsufficientResourcesError{Missing: missing}
	}

	next := world
	next.Constructions = append(
		append([]Construction(nil), world.Constructions...),
		Construction{
			ID:    fmt.Sprintf("base-%d-%d", target.CellX, target.CellY),
			Kind:  ConstructionBaseTile,
			CellX: target.CellX,
			CellY: target.CellY,
		},
	)
	next.BaseCellCount++
	next.Resources.Water.Pool.Current -= cost.Water
	next.Resources.Oxygen.Pool.Current -= cost.Oxygen
	next.Resources.Energy.Pool.Current -= cost.Energy

	rates, err := CalculateEconomyRates(config, next.TeamSize, next.BaseCellCount)
	if err != nil {
		return world, BuildResult{}, err
	}
	next.Resources.Water = updateTileRates(next.Resources.Water, rates.Water)
	next.Resources.Oxygen = updateTileRates(next.Resources.Oxygen, rates.Oxygen)
	next.Resources.Energy = updateTileRates(next.Resources.Energy, rates.Energy)
	next.Resources.Food = updateTileRates(next.Resources.Food, rates.Food)

	construction := next.Constructions[len(next.Constructions)-1]
	return next, BuildResult{
		Construction: construction,
		Won:          AllInvestigationCovered(next),
	}, nil
}

// AllInvestigationCovered reports whether each investigation cell is now part
// of the base.
func AllInvestigationCovered(world World) bool {
	for _, goal := range world.Investigation {
		if !containsCell(world.Constructions, goal) {
			return false
		}
	}
	return true
}

func collectResource(resource ResourceState) (ResourceState, float64, float64) {
	moved := math.Min(resource.Stash.Current, resource.Pool.Max-resource.Pool.Current)
	lost := resource.Stash.Current - moved
	resource.Pool.Current += moved
	resource.Stash.Current = 0
	return resource, moved, lost
}

func missingBuildCosts(resources Resources, cost BuildCost) []ResourceKey {
	missing := make([]ResourceKey, 0, 3)
	if resources.Water.Pool.Current < cost.Water {
		missing = append(missing, ResourceWater)
	}
	if resources.Oxygen.Pool.Current < cost.Oxygen {
		missing = append(missing, ResourceOxygen)
	}
	if resources.Energy.Pool.Current < cost.Energy {
		missing = append(missing, ResourceEnergy)
	}
	return missing
}

func containsCell(constructions []Construction, cell Cell) bool {
	for _, construction := range constructions {
		if construction.CellX == cell.CellX && construction.CellY == cell.CellY {
			return true
		}
	}
	return false
}

func updateTileRates(resource ResourceState, rates ResourceRates) ResourceState {
	resource.Stash.Max = rates.StashMax
	resource.Stash.GPS = rates.GPS
	resource.CPS = rates.CPS
	return resource
}
