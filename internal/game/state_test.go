package game

import (
	"encoding/json"
	"math"
	"reflect"
	"testing"
)

func TestExportGeneratedWorld(t *testing.T) {
	const seed = 20261115
	world, err := GenerateWorld(seed, 4, DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.MarshalIndent(world, "", "  ")
	if err != nil {
		t.Fatalf("marshal generated world: %v", err)
	}
	t.Logf("Generated world payload (seed=%d):\n%s", seed, payload)
}

func TestGenerateWorldStructureAndBounds(t *testing.T) {
	config := DefaultConfig()
	for seed := int64(-100); seed <= 100; seed++ {
		world, err := GenerateWorld(seed, 4, config)
		if err != nil {
			t.Fatalf("GenerateWorld(%d): %v", seed, err)
		}
		if world.Grid != (GridSize{Width: 50, Height: 50}) {
			t.Fatalf("seed %d: unexpected grid: %+v", seed, world.Grid)
		}
		if world.BaseCellCount != 9 || len(world.Constructions) != 9 {
			t.Fatalf("seed %d: expected 9 base cells/constructions; got %d/%d",
				seed, world.BaseCellCount, len(world.Constructions))
		}
		if len(world.Investigation) != config.InvestigationCount {
			t.Fatalf("seed %d: expected %d goals, got %d",
				seed, config.InvestigationCount, len(world.Investigation))
		}

		constructionCells := make(map[Cell]ConstructionKind)
		stashCount, tileCount := 0, 0
		var stash Cell
		for _, construction := range world.Constructions {
			cell := Cell{CellX: construction.CellX, CellY: construction.CellY}
			if cell.CellX < 0 || cell.CellX >= worldWidth ||
				cell.CellY < 0 || cell.CellY >= worldHeight {
				t.Fatalf("seed %d: construction out of bounds: %+v", seed, construction)
			}
			if _, duplicate := constructionCells[cell]; duplicate {
				t.Fatalf("seed %d: duplicate construction at %+v", seed, cell)
			}
			constructionCells[cell] = construction.Kind
			switch construction.Kind {
			case ConstructionStash:
				stashCount++
				stash = cell
				if construction.ID != "stash" {
					t.Fatalf("seed %d: unexpected stash id %q", seed, construction.ID)
				}
			case ConstructionBaseTile:
				tileCount++
			default:
				t.Fatalf("seed %d: unexpected construction kind %q", seed, construction.Kind)
			}
		}
		if stashCount != 1 || tileCount != 8 {
			t.Fatalf("seed %d: expected 1 stash and 8 base tiles; got %d and %d",
				seed, stashCount, tileCount)
		}
		minX, maxX := worldWidth, -1
		minY, maxY := worldHeight, -1
		for cell := range constructionCells {
			if cell.CellX < minX {
				minX = cell.CellX
			}
			if cell.CellX > maxX {
				maxX = cell.CellX
			}
			if cell.CellY < minY {
				minY = cell.CellY
			}
			if cell.CellY > maxY {
				maxY = cell.CellY
			}
		}
		if maxX-minX != 2 || maxY-minY != 2 || stash.CellX != minX+1 || stash.CellY != minY+1 {
			t.Fatalf("seed %d: base is not a centered 3x3 with a centered stash", seed)
		}

		expectedSpawns := []struct {
			slot   int
			cell   Cell
			facing Facing
		}{
			{0, Cell{CellX: minX + 1, CellY: minY}, FacingNorth},
			{1, Cell{CellX: maxX, CellY: minY + 1}, FacingEast},
			{2, Cell{CellX: minX + 1, CellY: maxY}, FacingSouth},
			{3, Cell{CellX: minX, CellY: minY + 1}, FacingWest},
		}
		if len(world.Spawns) != len(expectedSpawns) {
			t.Fatalf("seed %d: expected four spawns, got %d", seed, len(world.Spawns))
		}
		for i, expected := range expectedSpawns {
			spawn := world.Spawns[i]
			actualCell := Cell{CellX: int(math.Floor(spawn.X)), CellY: int(math.Floor(spawn.Y))}
			if spawn.Slot != expected.slot || actualCell != expected.cell || spawn.Facing != expected.facing {
				t.Fatalf("seed %d: spawn %d = %+v at %+v, want slot %d at %+v facing %s",
					seed, i, spawn, actualCell, expected.slot, expected.cell, expected.facing)
			}
			if spawn.X < 0 || spawn.X >= float64(worldWidth) ||
				spawn.Y < 0 || spawn.Y >= float64(worldHeight) {
				t.Fatalf("seed %d: spawn out of bounds: %+v", seed, spawn)
			}
		}

		goals := make(map[Cell]struct{}, len(world.Investigation))
		for _, goal := range world.Investigation {
			if goal.CellX < 0 || goal.CellX >= worldWidth ||
				goal.CellY < 0 || goal.CellY >= worldHeight {
				t.Fatalf("seed %d: goal out of bounds: %+v", seed, goal)
			}
			if _, inBase := constructionCells[goal]; inBase {
				t.Fatalf("seed %d: goal overlaps base: %+v", seed, goal)
			}
			if _, duplicate := goals[goal]; duplicate {
				t.Fatalf("seed %d: duplicate goal: %+v", seed, goal)
			}
			goals[goal] = struct{}{}
			for other := range goals {
				if other == goal {
					continue
				}
				dx := float64(goal.CellX - other.CellX)
				dy := float64(goal.CellY - other.CellY)
				if dx*dx+dy*dy < config.InvestigationMinDistance*config.InvestigationMinDistance {
					t.Fatalf("seed %d: goals are too close: %+v and %+v", seed, goal, other)
				}
			}
		}
	}
}

func TestGenerateWorldDeterministic(t *testing.T) {
	config := DefaultConfig()
	first, err := GenerateWorld(123456, 3, config)
	if err != nil {
		t.Fatal(err)
	}
	second, err := GenerateWorld(123456, 3, config)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatal("same seed and inputs produced different worlds")
	}
}

func TestGenerateWorldResourceFormulas(t *testing.T) {
	config := DefaultConfig()
	for teamSize := 1; teamSize <= 4; teamSize++ {
		world, err := GenerateWorld(42, teamSize, config)
		if err != nil {
			t.Fatalf("team size %d: %v", teamSize, err)
		}
		if world.TeamSize != teamSize {
			t.Fatalf("team size %d: world reports %d", teamSize, world.TeamSize)
		}
		assertInitialResource(t, "water", world.Resources.Water, config.Water, teamSize, config.StartingPoolFraction, false)
		assertInitialResource(t, "oxygen", world.Resources.Oxygen, config.Oxygen, teamSize, config.StartingPoolFraction, false)
		assertInitialResource(t, "energy", world.Resources.Energy, config.Energy, teamSize, config.StartingPoolFraction, false)
		assertInitialResource(t, "food", world.Resources.Food, config.Food, teamSize, config.StartingPoolFraction, true)
	}
}

func TestGenerateWorldUsesSuppliedResourceConfig(t *testing.T) {
	config := DefaultConfig()
	config.Water = ResourceConfig{
		PoolPerPlayer: 12, StashBase: 7, StashPerTile: 2, StashPerPlayer: 3,
		GPSPerTile: 0.5, CPSPerPlayer: 1.25, CPSPerTile: 0.75,
	}
	world, err := GenerateWorld(8, 2, config)
	if err != nil {
		t.Fatal(err)
	}
	assertInitialResource(t, "water", world.Resources.Water, config.Water, 2, config.StartingPoolFraction, false)
}

func TestGenerateWorldRejectsInvalidInputs(t *testing.T) {
	tests := []struct {
		name   string
		team   int
		change func(*Config)
	}{
		{name: "team below range", team: 0},
		{name: "team above range", team: 5},
		{name: "zero investigation count", team: 1, change: func(c *Config) { c.InvestigationCount = 0 }},
		{name: "negative investigation count", team: 1, change: func(c *Config) { c.InvestigationCount = -1 }},
		{name: "invalid spacing", team: 1, change: func(c *Config) { c.InvestigationMinDistance = 0 }},
		{name: "invalid pool fraction", team: 1, change: func(c *Config) { c.StartingPoolFraction = 1.1 }},
		{name: "negative resource value", team: 1, change: func(c *Config) { c.Water.GPSPerTile = -1 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := DefaultConfig()
			if test.change != nil {
				test.change(&config)
			}
			if _, err := GenerateWorld(1, test.team, config); err == nil {
				t.Fatal("expected invalid input to fail")
			}
		})
	}
}

func assertInitialResource(t *testing.T, name string, actual ResourceState, config ResourceConfig, teamSize int, poolFraction float64, food bool) {
	t.Helper()
	baseCells := float64(baseSize * baseSize)
	poolMax := config.PoolPerPlayer * float64(teamSize)
	stashMax := config.StashBase + config.StashPerTile*baseCells + config.StashPerPlayer*float64(teamSize)
	gps := config.GPSPerTile * baseCells
	cps := config.CPSPerPlayer*float64(teamSize) + config.CPSPerTile*baseCells
	if food {
		gps = config.GPSPerPlayer * float64(teamSize)
		cps = config.CPSPerPlayer * float64(teamSize)
	}
	if actual.Pool.Max != poolMax || actual.Pool.Current != poolMax*poolFraction {
		t.Errorf("%s pool = %+v; want current %v max %v", name, actual.Pool, poolMax*poolFraction, poolMax)
	}
	if actual.Stash.Current != 0 || actual.Stash.Max != stashMax || actual.Stash.GPS != gps {
		t.Errorf("%s stash = %+v; want current 0 max %v GPS %v", name, actual.Stash, stashMax, gps)
	}
	if actual.CPS != cps {
		t.Errorf("%s CPS = %v; want %v", name, actual.CPS, cps)
	}
}
