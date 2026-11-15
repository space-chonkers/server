package game

import (
	"math"
	"testing"
)

func squareBase(x, y, size int) []Construction {
	base := make([]Construction, 0, size*size)
	for cellY := y; cellY < y+size; cellY++ {
		for cellX := x; cellX < x+size; cellX++ {
			base = append(base, Construction{
				ID: "tile", Kind: ConstructionBaseTile, CellX: cellX, CellY: cellY,
			})
		}
	}
	return base
}

func TestAdvancePlayerMovementSpeedAndInputMagnitude(t *testing.T) {
	base := squareBase(10, 10, 3)
	tests := []struct {
		name string
		move MovementInput
		dt   float64
		want float64
	}{
		{name: "full speed", move: MovementInput{X: 1}, dt: 0.25, want: 12},
		{name: "half speed", move: MovementInput{X: 0.5}, dt: 0.25, want: 11.5},
		{name: "diagonal normalized", move: MovementInput{X: 1, Y: 1}, dt: 0.25, want: 11 + 1/math.Sqrt2},
		{name: "over-unit vector normalized", move: MovementInput{X: 3, Y: 4}, dt: 0.25, want: 11.6},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := AdvancePlayerMovement(PlayerMovementState{
				X: 11, Y: 11.5, Facing: FacingNorth,
			}, tt.move, base, tt.dt)
			if err != nil {
				t.Fatal(err)
			}
			if math.Abs(got.X-tt.want) > 1e-12 {
				t.Fatalf("X = %.15f, want %.15f", got.X, tt.want)
			}
			if got.VX == 0 {
				t.Fatalf("VX = 0, want a nonzero velocity")
			}
		})
	}
}

func TestAdvancePlayerMovementUsesConfiguredSpeed(t *testing.T) {
	got, err := AdvancePlayerMovementAtSpeed(
		PlayerMovementState{X: 11, Y: 11.5, Facing: FacingEast},
		MovementInput{X: 1},
		squareBase(10, 10, 3),
		2,
		0.25,
	)
	if err != nil {
		t.Fatal(err)
	}
	if got.X != 11.5 || got.VX != 2 {
		t.Fatalf("configured speed result = position %v velocity %v, want 11.5 and 2", got.X, got.VX)
	}
}

func TestAdvancePlayerMovementFacesDominantAxisAndKeepsTies(t *testing.T) {
	base := squareBase(10, 10, 3)
	tests := []struct {
		name   string
		facing Facing
		input  MovementInput
		want   Facing
	}{
		{name: "east dominant", facing: FacingNorth, input: MovementInput{X: 2, Y: 1}, want: FacingEast},
		{name: "north dominant", facing: FacingEast, input: MovementInput{X: 1, Y: -2}, want: FacingNorth},
		{name: "tie retains facing", facing: FacingSouth, input: MovementInput{X: 1, Y: -1}, want: FacingSouth},
		{name: "zero retains facing", facing: FacingWest, input: MovementInput{}, want: FacingWest},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := AdvancePlayerMovement(PlayerMovementState{
				X: 11.5, Y: 11.5, Facing: tt.facing,
			}, tt.input, base, 0)
			if err != nil {
				t.Fatal(err)
			}
			if got.Facing != tt.want {
				t.Fatalf("facing = %q, want %q", got.Facing, tt.want)
			}
		})
	}
}

func TestAdvancePlayerMovementClampsAndSlidesAlongBase(t *testing.T) {
	base := []Construction{
		{ID: "start", Kind: ConstructionBaseTile, CellX: 1, CellY: 0},
		{ID: "south", Kind: ConstructionBaseTile, CellX: 1, CellY: 1},
	}
	got, err := AdvancePlayerMovement(PlayerMovementState{
		X: 1.5, Y: 0.5, Facing: FacingNorth,
	}, MovementInput{X: 1, Y: 1}, base, 0.25)
	if err != nil {
		t.Fatal(err)
	}
	if got.X != math.Nextafter(2, math.Inf(-1)) || math.Abs(got.Y-(0.5+1/math.Sqrt2)) > 1e-12 {
		t.Fatalf("diagonal slide = (%v,%v), want (%v,%v)",
			got.X, got.Y, math.Nextafter(2, math.Inf(-1)), 0.5+1/math.Sqrt2)
	}
	if got.VX != 0 || got.VY == 0 {
		t.Fatalf("sliding velocity = (%v,%v), want X blocked and Y moving", got.VX, got.VY)
	}
	if got.Facing != FacingNorth {
		t.Fatalf("tied diagonal facing = %q, want retained north", got.Facing)
	}
}

func TestAdvancePlayerMovementTriesXAxisBeforeYAxis(t *testing.T) {
	base := []Construction{
		{ID: "start", Kind: ConstructionBaseTile, CellX: 0, CellY: 0},
		{ID: "east", Kind: ConstructionBaseTile, CellX: 1, CellY: 0},
		{ID: "south", Kind: ConstructionBaseTile, CellX: 0, CellY: 1},
	}
	got, err := AdvancePlayerMovement(PlayerMovementState{
		X: 0.5, Y: 0.5, Facing: FacingNorth,
	}, MovementInput{X: 1, Y: 1}, base, 0.25)
	if err != nil {
		t.Fatal(err)
	}
	wantX := 0.5 + 1/math.Sqrt2
	wantY := math.Nextafter(1, math.Inf(-1))
	if math.Abs(got.X-wantX) > 1e-12 || got.Y != wantY {
		t.Fatalf("diagonal movement = (%v,%v), want (%v,%v)", got.X, got.Y, wantX, wantY)
	}
}

func TestBlockedMovementStillUpdatesFacing(t *testing.T) {
	base := []Construction{{ID: "only", Kind: ConstructionBaseTile, CellX: 0, CellY: 0}}
	got, err := AdvancePlayerMovement(PlayerMovementState{
		X: 0.5, Y: 0.5, Facing: FacingNorth,
	}, MovementInput{X: 1}, base, 1)
	if err != nil {
		t.Fatal(err)
	}
	if got.Facing != FacingEast {
		t.Fatalf("facing after blocked push = %q, want east", got.Facing)
	}
	if got.X != math.Nextafter(1, math.Inf(-1)) || got.VX != 0 {
		t.Fatalf("blocked position/velocity = (%v,%v), want clamped and stopped", got.X, got.VX)
	}
}

func TestAdvancePlayerMovementDoesNotCrossDisconnectedCells(t *testing.T) {
	base := []Construction{
		{ID: "left", Kind: ConstructionBaseTile, CellX: 0, CellY: 0},
		{ID: "right", Kind: ConstructionBaseTile, CellX: 2, CellY: 0},
	}
	got, err := AdvancePlayerMovement(PlayerMovementState{
		X: 0.5, Y: 0.5, Facing: FacingEast,
	}, MovementInput{X: 1}, base, 1)
	if err != nil {
		t.Fatal(err)
	}
	if got.X != math.Nextafter(1, math.Inf(-1)) {
		t.Fatalf("X = %v, want clamp at the first void boundary", got.X)
	}
}

func TestAdvancePlayerMovementAllowsOverlapAndNewBaseCells(t *testing.T) {
	base := squareBase(10, 10, 2)
	first, err := AdvancePlayerMovement(PlayerMovementState{
		X: 10.5, Y: 10.5, Facing: FacingEast,
	}, MovementInput{X: 1}, base, 0.25)
	if err != nil {
		t.Fatal(err)
	}
	second, err := AdvancePlayerMovement(PlayerMovementState{
		X: 10.5, Y: 10.5, Facing: FacingWest,
	}, MovementInput{X: 1}, base, 0.25)
	if err != nil {
		t.Fatal(err)
	}
	if first.X != second.X || first.Y != second.Y {
		t.Fatalf("overlapping players moved differently: %+v and %+v", first, second)
	}

	expandedBase := append(base, Construction{
		ID: "new", Kind: ConstructionBaseTile, CellX: 12, CellY: 10,
	})
	got, err := AdvancePlayerMovement(PlayerMovementState{
		X: 11.5, Y: 10.5, Facing: FacingEast,
	}, MovementInput{X: 1}, expandedBase, 0.25)
	if err != nil {
		t.Fatal(err)
	}
	if got.X != 12.5 {
		t.Fatalf("movement onto added base cell X = %v, want 12.5", got.X)
	}
}

func TestPlayerFrontCellUsesPositionAndFacing(t *testing.T) {
	tests := []struct {
		facing Facing
		want   Cell
	}{
		{facing: FacingNorth, want: Cell{CellX: 4, CellY: 6}},
		{facing: FacingEast, want: Cell{CellX: 5, CellY: 7}},
		{facing: FacingSouth, want: Cell{CellX: 4, CellY: 8}},
		{facing: FacingWest, want: Cell{CellX: 3, CellY: 7}},
	}
	for _, tt := range tests {
		got, err := PlayerFrontCell(PlayerMovementState{X: 4.8, Y: 7.1, Facing: tt.facing})
		if err != nil {
			t.Fatal(err)
		}
		if got != tt.want {
			t.Errorf("front cell for %s = %+v, want %+v", tt.facing, got, tt.want)
		}
	}
}

func TestAdvancePlayerMovementRejectsInvalidState(t *testing.T) {
	base := squareBase(0, 0, 1)
	tests := []struct {
		name   string
		player PlayerMovementState
		input  MovementInput
		base   []Construction
		dt     float64
	}{
		{name: "negative elapsed", player: PlayerMovementState{X: 0.5, Y: 0.5, Facing: FacingNorth}, base: base, dt: -1},
		{name: "non-finite input", player: PlayerMovementState{X: 0.5, Y: 0.5, Facing: FacingNorth}, input: MovementInput{X: math.NaN()}, base: base},
		{name: "outside base", player: PlayerMovementState{X: 1.5, Y: 0.5, Facing: FacingNorth}, base: base},
		{name: "empty base", player: PlayerMovementState{X: 0.5, Y: 0.5, Facing: FacingNorth}},
		{name: "invalid facing", player: PlayerMovementState{X: 0.5, Y: 0.5, Facing: "up"}, base: base},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := AdvancePlayerMovement(tt.player, tt.input, tt.base, tt.dt); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}
