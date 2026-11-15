package game

import (
	"errors"
	"reflect"
	"testing"
)

func actionTestWorld(t *testing.T, investigation []Cell) (World, Config) {
	t.Helper()
	if investigation == nil {
		investigation = []Cell{{CellX: 30, CellY: 30}}
	}
	config := DefaultConfig()
	rates, err := CalculateEconomyRates(config, 1, 9)
	if err != nil {
		t.Fatal(err)
	}
	resource := func(rates ResourceRates) ResourceState {
		return ResourceState{
			Pool:  ResourcePool{Current: 20, Max: 50},
			Stash: ResourceStash{Max: rates.StashMax, GPS: rates.GPS},
			CPS:   rates.CPS,
		}
	}
	constructions := make([]Construction, 0, 9)
	for y := 10; y < 13; y++ {
		for x := 10; x < 13; x++ {
			kind := ConstructionBaseTile
			id := "base"
			if x == 11 && y == 11 {
				kind = ConstructionStash
				id = "stash"
			}
			constructions = append(constructions, Construction{
				ID: id, Kind: kind, CellX: x, CellY: y,
			})
		}
	}
	return World{
		Grid:          GridSize{Width: worldWidth, Height: worldHeight},
		BaseCellCount: len(constructions),
		TeamSize:      1,
		Constructions: constructions,
		Investigation: investigation,
		Resources: Resources{
			Water:  resource(rates.Water),
			Oxygen: resource(rates.Oxygen),
			Energy: resource(rates.Energy),
			Food:   resource(rates.Food),
		},
	}, config
}

func TestCollectAtStashMovesResourcesAndReportsOverflow(t *testing.T) {
	world, _ := actionTestWorld(t, nil)
	world.Resources.Water.Pool = ResourcePool{Current: 8, Max: 10}
	world.Resources.Water.Stash.Current = 5
	world.Resources.Oxygen.Pool = ResourcePool{Current: 1, Max: 10}
	world.Resources.Oxygen.Stash.Current = 2
	world.Resources.Energy.Pool = ResourcePool{Current: 10, Max: 10}
	world.Resources.Energy.Stash.Current = 3
	world.Resources.Food.Pool = ResourcePool{Current: 2, Max: 10}
	world.Resources.Food.Stash.Current = 4
	before := world
	player := PlayerMovementState{X: 11.5, Y: 11.5, Facing: FacingNorth}

	gotWorld, result, err := CollectAtStash(world, player)
	if err != nil {
		t.Fatal(err)
	}
	if result.Collected != (ResourceAmounts{Water: 2, Oxygen: 2, Food: 4}) ||
		result.Lost != (ResourceAmounts{Water: 3, Energy: 3}) {
		t.Fatalf("collect result = %+v, want expected moved/lost values", result)
	}
	if gotWorld.Resources.Water.Pool.Current != 10 ||
		gotWorld.Resources.Oxygen.Pool.Current != 3 ||
		gotWorld.Resources.Energy.Pool.Current != 10 ||
		gotWorld.Resources.Food.Pool.Current != 6 {
		t.Fatalf("pool values after collect = %+v", gotWorld.Resources)
	}
	if gotWorld.Resources.Water.Stash.Current != 0 ||
		gotWorld.Resources.Oxygen.Stash.Current != 0 ||
		gotWorld.Resources.Energy.Stash.Current != 0 ||
		gotWorld.Resources.Food.Stash.Current != 0 {
		t.Fatalf("stash was not emptied: %+v", gotWorld.Resources)
	}
	if before.Resources.Water.Pool.Current != 8 || before.Resources.Water.Stash.Current != 5 {
		t.Fatalf("input world was mutated: %+v", before.Resources.Water)
	}
}

func TestCollectAtStashRequiresStashCellAndIsAtomic(t *testing.T) {
	world, _ := actionTestWorld(t, nil)
	world.Resources.Water.Stash.Current = 4

	got, _, err := CollectAtStash(world, PlayerMovementState{X: 10.5, Y: 10.5})
	if !errors.Is(err, ErrNotAtStash) {
		t.Fatalf("CollectAtStash error = %v, want ErrNotAtStash", err)
	}
	if !reflect.DeepEqual(got, world) {
		t.Fatal("failed collection changed the world")
	}
}

func TestBuildBaseTileChargesAndUpdatesEconomy(t *testing.T) {
	world, config := actionTestWorld(t, nil)
	config.BuildCost = BuildCost{Water: 5, Oxygen: 4, Energy: 3}
	player := PlayerMovementState{X: 12.5, Y: 10.5, Facing: FacingEast}

	got, result, err := BuildBaseTile(world, player, Cell{CellX: 13, CellY: 10}, config)
	if err != nil {
		t.Fatal(err)
	}
	if result.Construction != (Construction{
		ID: "base-13-10", Kind: ConstructionBaseTile, CellX: 13, CellY: 10,
	}) || result.Won {
		t.Fatalf("build result = %+v", result)
	}
	if got.BaseCellCount != 10 || len(got.Constructions) != 10 {
		t.Fatalf("built base count=%d constructions=%d", got.BaseCellCount, len(got.Constructions))
	}
	if got.Resources.Water.Pool.Current != 15 ||
		got.Resources.Oxygen.Pool.Current != 16 ||
		got.Resources.Energy.Pool.Current != 17 ||
		got.Resources.Food.Pool.Current != 20 {
		t.Fatalf("pool values after build = %+v", got.Resources)
	}
	wantRates, err := CalculateEconomyRates(config, 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	for name, pair := range map[string]struct {
		got  ResourceState
		want ResourceRates
	}{
		"water":  {got.Resources.Water, wantRates.Water},
		"oxygen": {got.Resources.Oxygen, wantRates.Oxygen},
		"energy": {got.Resources.Energy, wantRates.Energy},
		"food":   {got.Resources.Food, wantRates.Food},
	} {
		if pair.got.Stash.Max != pair.want.StashMax ||
			pair.got.Stash.GPS != pair.want.GPS ||
			pair.got.CPS != pair.want.CPS {
			t.Errorf("%s rates after build = %+v, want %+v", name, pair.got, pair.want)
		}
	}
	if world.BaseCellCount != 9 || world.Resources.Water.Pool.Current != 20 {
		t.Fatal("successful build mutated its input world")
	}
}

func TestBuildBaseTileReportsWinWhenLastGoalIsCovered(t *testing.T) {
	world, config := actionTestWorld(t, []Cell{{CellX: 13, CellY: 10}})
	world.Resources.Water.Pool.Current = 5
	world.Resources.Oxygen.Pool.Current = 5
	world.Resources.Energy.Pool.Current = 5

	got, result, err := BuildBaseTile(
		world, PlayerMovementState{X: 12.5, Y: 10.5, Facing: FacingEast},
		Cell{CellX: 13, CellY: 10}, config,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Won {
		t.Fatal("covering the final investigation cell did not report a win")
	}
	if got.Resources.Water.Pool.Current != 0 ||
		got.Resources.Oxygen.Pool.Current != 0 ||
		got.Resources.Energy.Pool.Current != 0 {
		t.Fatalf("build costs were not paid: %+v", got.Resources)
	}
}

func TestBuildBaseTileRejectsInvalidActionsWithoutMutation(t *testing.T) {
	tests := []struct {
		name   string
		player PlayerMovementState
		target Cell
		want   error
	}{
		{
			name: "not on base", player: PlayerMovementState{X: 20.5, Y: 20.5, Facing: FacingEast},
			target: Cell{CellX: 21, CellY: 20}, want: ErrNotOnBase,
		},
		{
			name: "out of bounds", player: PlayerMovementState{X: 12.5, Y: 10.5, Facing: FacingEast},
			target: Cell{CellX: worldWidth, CellY: 10}, want: ErrOutOfBounds,
		},
		{
			name: "not in front", player: PlayerMovementState{X: 12.5, Y: 10.5, Facing: FacingEast},
			target: Cell{CellX: 13, CellY: 11}, want: ErrNotInFront,
		},
		{
			name: "occupied cell", player: PlayerMovementState{X: 10.5, Y: 10.5, Facing: FacingEast},
			target: Cell{CellX: 11, CellY: 10}, want: ErrCellOccupied,
		},
		{
			name: "insufficient resources", player: PlayerMovementState{X: 12.5, Y: 10.5, Facing: FacingEast},
			target: Cell{CellX: 13, CellY: 10},
			want:   ErrInsufficientResource,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			world, config := actionTestWorld(t, nil)
			if tt.want == ErrInsufficientResource {
				config.BuildCost = BuildCost{Water: 21}
			}
			before := world
			updated, _, err := BuildBaseTile(world, tt.player, tt.target, config)
			if !errors.Is(err, tt.want) {
				t.Fatalf("BuildBaseTile error = %v, want %v", err, tt.want)
			}
			if !reflect.DeepEqual(updated, before) {
				t.Fatal("failed build changed the world")
			}
			var insufficient *InsufficientResourcesError
			if errors.Is(tt.want, ErrInsufficientResource) &&
				(!errors.As(err, &insufficient) || !reflect.DeepEqual(insufficient.Missing, []ResourceKey{ResourceWater})) {
				t.Fatalf("insufficient-resource error = %#v", err)
			}
		})
	}
}
