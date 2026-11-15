package game

import (
	"errors"
	"fmt"
	"math"
)

const (
	worldWidth  = 50
	worldHeight = 50
	baseSize    = 3
)

var (
	errNoInvestigationLayout = errors.New("unable to create a valid investigation layout")
	fallbackBase             = Cell{CellX: 24, CellY: 24}
	fallbackObjectives       = [...]Cell{
		{CellX: 0, CellY: 0},
		{CellX: 45, CellY: 45},
		{CellX: 45, CellY: 0},
		{CellX: 0, CellY: 45},
		{CellX: 20, CellY: 20},
		{CellX: 40, CellY: 25},
		{CellX: 25, CellY: 40},
		{CellX: 20, CellY: 0},
		{CellX: 0, CellY: 20},
		{CellX: 10, CellY: 10},
	}
)

func generateMap(rng *randomSource, count int, minDistance float64) ([]Construction, []Cell, []PlayerSpawn, error) {
	return generateMapWithAttempts(rng, count, minDistance, 128)
}

func generateMapWithAttempts(rng *randomSource, count int, minDistance float64, maxAttempts int) ([]Construction, []Cell, []PlayerSpawn, error) {
	maxX := worldWidth - baseSize
	maxY := worldHeight - baseSize
	base := Cell{CellX: rng.intn(maxX + 1), CellY: rng.intn(maxY + 1)}
	candidates := investigationCandidates(base)

	for attempt := 0; attempt < maxAttempts; attempt++ {
		rng.shuffleCells(candidates)
		if goals, ok := sampleInvestigation(candidates, count, minDistance); ok {
			constructions, goals, spawns := startingMap(base, goals)
			return constructions, goals, spawns, nil
		}
	}

	return hardcodedFallbackMap(count, minDistance)
}

func investigationCandidates(baseTopLeft Cell) []Cell {
	candidates := make([]Cell, 0, worldWidth*worldHeight-baseSize*baseSize)
	for y := 0; y < worldHeight; y++ {
		for x := 0; x < worldWidth; x++ {
			if x >= baseTopLeft.CellX && x < baseTopLeft.CellX+baseSize &&
				y >= baseTopLeft.CellY && y < baseTopLeft.CellY+baseSize {
				continue
			}
			candidates = append(candidates, Cell{CellX: x, CellY: y})
		}
	}
	return candidates
}

func sampleInvestigation(candidates []Cell, count int, minDistance float64) ([]Cell, bool) {
	selected := make([]Cell, 0, count)
	for _, candidate := range candidates {
		if separatedFromAll(candidate, selected, minDistance) {
			selected = append(selected, candidate)
			if len(selected) == count {
				return selected, true
			}
		}
	}
	return nil, false
}

func hardcodedFallbackMap(count int, minDistance float64) ([]Construction, []Cell, []PlayerSpawn, error) {
	if count < 1 || count > len(fallbackObjectives) {
		return nil, nil, nil, fmt.Errorf(
			"%w: hardcoded map supports 1..%d objectives, got %d",
			errNoInvestigationLayout, len(fallbackObjectives), count,
		)
	}
	goals := append([]Cell(nil), fallbackObjectives[:count]...)
	for i, goal := range goals {
		if goal.CellX >= fallbackBase.CellX && goal.CellX < fallbackBase.CellX+baseSize &&
			goal.CellY >= fallbackBase.CellY && goal.CellY < fallbackBase.CellY+baseSize {
			return nil, nil, nil, fmt.Errorf(
				"%w: hardcoded objective (%d,%d) overlaps its base",
				errNoInvestigationLayout, goal.CellX, goal.CellY,
			)
		}
		if !separatedFromAll(goal, goals[:i], minDistance) {
			return nil, nil, nil, fmt.Errorf(
				"%w: hardcoded objectives do not satisfy minimum distance %.2f",
				errNoInvestigationLayout, minDistance,
			)
		}
	}
	constructions, goals, spawns := startingMap(fallbackBase, goals)
	return constructions, goals, spawns, nil
}

func startingMap(base Cell, goals []Cell) ([]Construction, []Cell, []PlayerSpawn) {
	constructions := make([]Construction, 0, baseSize*baseSize)
	for y := base.CellY; y < base.CellY+baseSize; y++ {
		for x := base.CellX; x < base.CellX+baseSize; x++ {
			kind := ConstructionBaseTile
			id := fmt.Sprintf("base-%d-%d", x, y)
			if x == base.CellX+1 && y == base.CellY+1 {
				kind = ConstructionStash
				id = "stash"
			}
			constructions = append(constructions, Construction{
				ID: id, Kind: kind, CellX: x, CellY: y,
			})
		}
	}

	// Spawn positions use the cardinal edge cells in protocol slot order.
	spawns := []PlayerSpawn{
		{Slot: 0, X: float64(base.CellX+1) + 0.5, Y: float64(base.CellY) + 0.5, Facing: FacingNorth},
		{Slot: 1, X: float64(base.CellX+2) + 0.5, Y: float64(base.CellY+1) + 0.5, Facing: FacingEast},
		{Slot: 2, X: float64(base.CellX+1) + 0.5, Y: float64(base.CellY+2) + 0.5, Facing: FacingSouth},
		{Slot: 3, X: float64(base.CellX) + 0.5, Y: float64(base.CellY+1) + 0.5, Facing: FacingWest},
	}
	return constructions, goals, spawns
}

func separatedFromAll(candidate Cell, selected []Cell, minDistance float64) bool {
	minDistanceSquared := minDistance * minDistance
	for _, current := range selected {
		dx := float64(candidate.CellX - current.CellX)
		dy := float64(candidate.CellY - current.CellY)
		if dx*dx+dy*dy < minDistanceSquared {
			return false
		}
	}
	return true
}

func validMapConfig(count int, minDistance float64) error {
	if count < 1 {
		return fmt.Errorf("investigation count must be positive: %d", count)
	}
	if minDistance < 1 || math.IsNaN(minDistance) || math.IsInf(minDistance, 0) {
		return fmt.Errorf("investigation minimum distance must be finite and at least 1: %v", minDistance)
	}
	if count > worldWidth*worldHeight-baseSize*baseSize {
		return fmt.Errorf("investigation count %d exceeds available cells", count)
	}
	return nil
}
