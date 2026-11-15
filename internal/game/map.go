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

var errNoInvestigationLayout = errors.New("unable to place investigation cells at the configured minimum distance")

func generateMap(rng *randomSource, count int, minDistance float64) ([]Construction, []Cell, []PlayerSpawn, error) {
	maxX := worldWidth - baseSize
	maxY := worldHeight - baseSize
	startX := rng.intn(maxX + 1)
	startY := rng.intn(maxY + 1)

	constructions := make([]Construction, 0, baseSize*baseSize)
	for y := startY; y < startY+baseSize; y++ {
		for x := startX; x < startX+baseSize; x++ {
			kind := ConstructionBaseTile
			id := fmt.Sprintf("base-%d-%d", x, y)
			if x == startX+1 && y == startY+1 {
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
		{Slot: 0, X: float64(startX+1) + 0.5, Y: float64(startY) + 0.5, Facing: FacingNorth},
		{Slot: 1, X: float64(startX+2) + 0.5, Y: float64(startY+1) + 0.5, Facing: FacingEast},
		{Slot: 2, X: float64(startX+1) + 0.5, Y: float64(startY+2) + 0.5, Facing: FacingSouth},
		{Slot: 3, X: float64(startX) + 0.5, Y: float64(startY+1) + 0.5, Facing: FacingWest},
	}

	goals, err := generateInvestigation(rng, Cell{CellX: startX, CellY: startY}, count, minDistance)
	if err != nil {
		return nil, nil, nil, err
	}
	return constructions, goals, spawns, nil
}

func generateInvestigation(rng *randomSource, baseTopLeft Cell, count int, minDistance float64) ([]Cell, error) {
	if count == 0 {
		return []Cell{}, nil
	}

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

	// Randomized dart throwing yields a Poisson-disk distribution over valid
	// cells. Retry with independent deterministic permutations if a greedy
	// order strands the sampler before it reaches the requested count.
	const maxAttempts = 128
	for attempt := 0; attempt < maxAttempts; attempt++ {
		rng.shuffleCells(candidates)
		selected := make([]Cell, 0, count)
		for _, candidate := range candidates {
			if separatedFromAll(candidate, selected, minDistance) {
				selected = append(selected, candidate)
				if len(selected) == count {
					return selected, nil
				}
			}
		}
	}
	return nil, errNoInvestigationLayout
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
