package game

import (
	"fmt"
	"math"
)

// PlayerMovementState is the server-owned motion state used by B3 movement.
type PlayerMovementState struct {
	X      float64
	Y      float64
	VX     float64
	VY     float64
	Facing Facing
}

// MovementInput is a player's movement intent for one simulation step.
type MovementInput struct {
	X float64
	Y float64
}

// PlayerSpeed is the default movement speed in grid units per second.
const PlayerSpeed = 4.0

// WallCollisionOffset matches the walkable clearance around corridor walls
// and corner posts in the client scene.
const WallCollisionOffset = 0.12

const wallCollisionTolerance = 1e-9

// AdvancePlayerMovement applies one movement intent over elapsedSeconds.
// Positions are points in grid units and only occupied base cells are
// traversable.
func AdvancePlayerMovement(
	player PlayerMovementState,
	input MovementInput,
	base []Construction,
	elapsedSeconds float64,
) (PlayerMovementState, error) {
	return AdvancePlayerMovementAtSpeed(player, input, base, PlayerSpeed, elapsedSeconds)
}

// AdvancePlayerMovementAtSpeed applies one movement intent at a configured
// speed in grid units per second.
func AdvancePlayerMovementAtSpeed(
	player PlayerMovementState,
	input MovementInput,
	base []Construction,
	speedPerSecond float64,
	elapsedSeconds float64,
) (PlayerMovementState, error) {
	if !finite(elapsedSeconds) || elapsedSeconds < 0 {
		return PlayerMovementState{}, fmt.Errorf("elapsed seconds must be finite and non-negative: %v", elapsedSeconds)
	}
	if !finite(speedPerSecond) || speedPerSecond < 0 {
		return PlayerMovementState{}, fmt.Errorf("movement speed must be finite and non-negative: %v", speedPerSecond)
	}
	if !finite(input.X) || !finite(input.Y) {
		return PlayerMovementState{}, fmt.Errorf("movement input must be finite")
	}
	if !finite(player.X) || !finite(player.Y) {
		return PlayerMovementState{}, fmt.Errorf("player position must be finite")
	}
	walkable, err := baseCells(base)
	if err != nil {
		return PlayerMovementState{}, err
	}
	if !walkablePosition(player.X, player.Y, walkable) {
		return PlayerMovementState{}, fmt.Errorf("player position is not inside the base")
	}

	inputX, inputY := normalizedInput(input.X, input.Y)
	speed := speedPerSecond * elapsedSeconds
	if !finite(speed) {
		return PlayerMovementState{}, fmt.Errorf("movement distance is not finite")
	}
	deltaX, deltaY := inputX*speed, inputY*speed
	if !finite(deltaX) || !finite(deltaY) ||
		!finite(player.X+deltaX) || !finite(player.Y+deltaY) {
		return PlayerMovementState{}, fmt.Errorf("resulting movement position is not finite")
	}

	result := player
	if input.X != 0 || input.Y != 0 {
		result.Facing = facingFromInput(player.Facing, input.X, input.Y)
	}
	result.VX = inputX * speedPerSecond
	result.VY = inputY * speedPerSecond

	var blockedX bool
	result.X, blockedX = moveAxis(result.X, result.Y, deltaX, true, walkable)
	if blockedX {
		result.VX = 0
	}
	var blockedY bool
	result.Y, blockedY = moveAxis(result.X, result.Y, deltaY, false, walkable)
	if blockedY {
		result.VY = 0
	}
	return result, nil
}

// PlayerFrontCell returns the grid cell directly in front of the player.
func PlayerFrontCell(player PlayerMovementState) (Cell, error) {
	if !inBounds(player.X, player.Y) {
		return Cell{}, fmt.Errorf("player position is outside the grid")
	}
	cell := Cell{CellX: int(math.Floor(player.X)), CellY: int(math.Floor(player.Y))}
	switch player.Facing {
	case FacingNorth:
		cell.CellY--
	case FacingEast:
		cell.CellX++
	case FacingSouth:
		cell.CellY++
	case FacingWest:
		cell.CellX--
	}
	return cell, nil
}

func baseCells(constructions []Construction) (map[Cell]struct{}, error) {
	if len(constructions) == 0 {
		return nil, fmt.Errorf("base must contain at least one construction")
	}
	walkable := make(map[Cell]struct{}, len(constructions))
	for _, construction := range constructions {
		if construction.Kind != ConstructionBaseTile && construction.Kind != ConstructionStash {
			return nil, fmt.Errorf("construction %q is not a walkable base cell", construction.ID)
		}
		if !inBounds(float64(construction.CellX), float64(construction.CellY)) {
			return nil, fmt.Errorf("base construction %q is outside the grid", construction.ID)
		}
		cell := Cell{CellX: construction.CellX, CellY: construction.CellY}
		if _, duplicate := walkable[cell]; duplicate {
			return nil, fmt.Errorf("multiple base constructions occupy cell (%d,%d)", cell.CellX, cell.CellY)
		}
		walkable[cell] = struct{}{}
	}
	return walkable, nil
}

func walkablePosition(x, y float64, walkable map[Cell]struct{}) bool {
	if !inBounds(x, y) {
		return false
	}
	cell := Cell{CellX: int(math.Floor(x)), CellY: int(math.Floor(y))}
	_, ok := walkable[cell]
	return ok
}

func inBounds(x, y float64) bool {
	return x >= 0 && x < worldWidth && y >= 0 && y < worldHeight
}

func moveAxis(x, y, distance float64, horizontal bool, walkable map[Cell]struct{}) (float64, bool) {
	if distance == 0 {
		if horizontal {
			return x, false
		}
		return y, false
	}

	position := x
	if !horizontal {
		position = y
	}
	target := position + distance
	for position != target {
		cellX, cellY := int(math.Floor(x)), int(math.Floor(y))
		if horizontal {
			cellX = int(math.Floor(position))
		} else {
			cellY = int(math.Floor(position))
		}
		center := float64(cellX) + 0.5
		orthogonal := y
		orthogonalCenter := float64(cellY) + 0.5
		if !horizontal {
			center = float64(cellY) + 0.5
			orthogonal = x
			orthogonalCenter = float64(cellX) + 0.5
		}
		cornerBlocked := math.Abs(orthogonal-orthogonalCenter) > WallCollisionOffset+wallCollisionTolerance

		if distance > 0 {
			wallLimit := center + WallCollisionOffset
			nextX, nextY := cellX, cellY
			if horizontal {
				nextX++
			} else {
				nextY++
			}
			if cornerBlocked || !hasCell(walkable, nextX, nextY) {
				if position >= wallLimit {
					return position, true
				}
				if target > wallLimit {
					return wallLimit, true
				}
				return target, false
			}
			boundary := math.Floor(position) + 1
			if target < boundary {
				return target, false
			}
			position = boundary
		} else {
			wallLimit := center - WallCollisionOffset
			nextX, nextY := cellX, cellY
			if horizontal {
				nextX--
			} else {
				nextY--
			}
			if cornerBlocked || !hasCell(walkable, nextX, nextY) {
				if position <= wallLimit {
					return position, true
				}
				if target < wallLimit {
					return wallLimit, true
				}
				return target, false
			}
			boundary := math.Floor(position)
			if target >= boundary {
				return target, false
			}
			position = math.Nextafter(boundary, math.Inf(-1))
		}
	}
	return position, false
}

func hasCell(walkable map[Cell]struct{}, x, y int) bool {
	_, ok := walkable[Cell{CellX: x, CellY: y}]
	return ok
}

func normalizedInput(x, y float64) (float64, float64) {
	if math.Abs(x) <= 1 && math.Abs(y) <= 1 && math.Hypot(x, y) <= 1 {
		return x, y
	}
	scale := math.Max(math.Abs(x), math.Abs(y))
	x, y = x/scale, y/scale
	length := math.Hypot(x, y)
	return x / length, y / length
}

func facingFromInput(current Facing, x, y float64) Facing {
	switch {
	case math.Abs(x) > math.Abs(y):
		if x > 0 {
			return FacingEast
		}
		return FacingWest
	case math.Abs(y) > math.Abs(x):
		if y > 0 {
			return FacingSouth
		}
		return FacingNorth
	default:
		return current
	}
}
