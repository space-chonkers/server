package game

import (
	"math"
	"reflect"
	"strings"
	"testing"
)

func TestValidateEconomyConfigDefaults(t *testing.T) {
	if err := ValidateEconomyConfig(DefaultConfig()); err != nil {
		t.Fatalf("default economy config should be valid: %v", err)
	}
}

func TestValidateEconomyConfigRejectsInvalidRatesAndPoolCapacity(t *testing.T) {
	tests := []struct {
		name   string
		change func(*Config)
		want   string
	}{
		{
			name: "water cps falls below gps at large base",
			change: func(config *Config) {
				config.Water.GPSPerTile = 1
				config.Water.CPSPerPlayer = 1000
				config.Water.CPSPerTile = 0.5
			},
			want: "water CPS must exceed GPS for team size 1 and base cells 2500",
		},
		{
			name: "food cps equals gps",
			change: func(config *Config) {
				config.Food.GPSPerPlayer = config.Food.CPSPerPlayer
			},
			want: "food CPS must exceed GPS",
		},
		{
			name: "zero capacity",
			change: func(config *Config) {
				config.Energy.PoolPerPlayer = 0
			},
			want: "energy pool capacity per player must be positive",
		},
		{
			name: "negative config",
			change: func(config *Config) {
				config.Oxygen.CPSPerTile = -1
			},
			want: "oxygen CPS per tile must be finite and non-negative",
		},
		{
			name: "non-finite derived rate",
			change: func(config *Config) {
				config.Water.GPSPerTile = math.MaxFloat64
			},
			want: "water economy rates overflow",
		},
		{
			name: "pool max overflows",
			change: func(config *Config) {
				config.Food.PoolPerPlayer = math.MaxFloat64
			},
			want: "food pool capacity overflows",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := DefaultConfig()
			test.change(&config)
			err := ValidateEconomyConfig(config)
			if err == nil {
				t.Fatal("expected invalid economy config to fail")
			}
			if !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error %q does not contain %q", err, test.want)
			}
		})
	}
}

func TestCalculateEconomyRates(t *testing.T) {
	config := DefaultConfig()
	for teamSize := 1; teamSize <= 4; teamSize++ {
		for _, baseCells := range []int{9, 10, 2500} {
			rates, err := CalculateEconomyRates(config, teamSize, baseCells)
			if err != nil {
				t.Fatalf("team size %d, base %d: %v", teamSize, baseCells, err)
			}
			assertRates(t, "water", rates.Water, config.Water, teamSize, baseCells, false)
			assertRates(t, "oxygen", rates.Oxygen, config.Oxygen, teamSize, baseCells, false)
			assertRates(t, "energy", rates.Energy, config.Energy, teamSize, baseCells, false)
			assertRates(t, "food", rates.Food, config.Food, teamSize, baseCells, true)
		}
	}
}

func TestCalculateEconomyRatesRejectsUnsupportedWorldDimensions(t *testing.T) {
	config := DefaultConfig()
	for _, test := range []struct {
		teamSize int
		base     int
	}{
		{teamSize: 0, base: 9},
		{teamSize: 5, base: 9},
		{teamSize: 1, base: 8},
		{teamSize: 1, base: 2501},
	} {
		if _, err := CalculateEconomyRates(config, test.teamSize, test.base); err == nil {
			t.Errorf("expected unsupported team/base combination (%d,%d) to fail", test.teamSize, test.base)
		}
	}
}

func TestAdvanceEconomyCapsStashAndReportsDepletion(t *testing.T) {
	resources := Resources{
		Water:  ResourceState{Pool: ResourcePool{Current: 2, Max: 10}, Stash: ResourceStash{Current: 9, Max: 10, GPS: 4}, CPS: 1},
		Oxygen: ResourceState{Pool: ResourcePool{Current: 1, Max: 10}, Stash: ResourceStash{Max: 5, GPS: 0.5}, CPS: 2},
		Energy: ResourceState{Pool: ResourcePool{Current: 0.25, Max: 10}, Stash: ResourceStash{Max: 10, GPS: 1}, CPS: 0.5},
		Food:   ResourceState{Pool: ResourcePool{Current: 0, Max: 10}, Stash: ResourceStash{Max: 10, GPS: 0.25}, CPS: 0.1},
	}
	result, err := AdvanceEconomy(&resources, 1)
	if err != nil {
		t.Fatal(err)
	}
	if resources.Water.Stash.Current != 10 || resources.Water.Pool.Current != 1 {
		t.Errorf("water after tick = %+v; want stash 10 and pool 1", resources.Water)
	}
	if resources.Oxygen.Stash.Current != 0.5 || resources.Oxygen.Pool.Current != 0 {
		t.Errorf("oxygen after tick = %+v; want stash 0.5 and pool 0", resources.Oxygen)
	}
	if resources.Energy.Stash.Current != 1 || resources.Energy.Pool.Current != 0 {
		t.Errorf("energy after tick = %+v; want stash 1 and pool 0", resources.Energy)
	}
	if resources.Food.Stash.Current != 0.25 || resources.Food.Pool.Current != 0 {
		t.Errorf("food after tick = %+v; want stash 0.25 and pool 0", resources.Food)
	}
	wantDepleted := []ResourceKey{ResourceOxygen, ResourceEnergy, ResourceFood}
	if !reflect.DeepEqual(result.DepletedResources, wantDepleted) {
		t.Errorf("depleted resources = %v; want %v", result.DepletedResources, wantDepleted)
	}
}

func TestAdvanceEconomyRepeatedStepsUseElapsedSeconds(t *testing.T) {
	resources := Resources{
		Water:  ResourceState{Pool: ResourcePool{Current: 5, Max: 10}, Stash: ResourceStash{Max: 10, GPS: 2}, CPS: 1},
		Oxygen: ResourceState{Pool: ResourcePool{Current: 5, Max: 10}, Stash: ResourceStash{Max: 10, GPS: 2}, CPS: 1},
		Energy: ResourceState{Pool: ResourcePool{Current: 5, Max: 10}, Stash: ResourceStash{Max: 10, GPS: 2}, CPS: 1},
		Food:   ResourceState{Pool: ResourcePool{Current: 5, Max: 10}, Stash: ResourceStash{Max: 10, GPS: 2}, CPS: 1},
	}
	for range 4 {
		result, err := AdvanceEconomy(&resources, 0.5)
		if err != nil {
			t.Fatal(err)
		}
		if len(result.DepletedResources) != 0 {
			t.Fatalf("unexpected depletion: %v", result.DepletedResources)
		}
	}
	for _, state := range []ResourceState{resources.Water, resources.Oxygen, resources.Energy, resources.Food} {
		if state.Pool.Current != 3 || state.Stash.Current != 4 {
			t.Fatalf("resource after four half-second steps = %+v; want pool 3 and stash 4", state)
		}
	}
}

func TestAdvanceEconomyRejectsInvalidInputWithoutPartialMutation(t *testing.T) {
	valid := Resources{
		Water:  ResourceState{Pool: ResourcePool{Current: 5, Max: 10}, Stash: ResourceStash{Max: 10, GPS: 1}, CPS: 1},
		Oxygen: ResourceState{Pool: ResourcePool{Current: 5, Max: 10}, Stash: ResourceStash{Max: 10, GPS: 1}, CPS: 1},
		Energy: ResourceState{Pool: ResourcePool{Current: 5, Max: 10}, Stash: ResourceStash{Max: 10, GPS: 1}, CPS: 1},
		Food:   ResourceState{Pool: ResourcePool{Current: 5, Max: 10}, Stash: ResourceStash{Max: 10, GPS: 1}, CPS: 1},
	}
	tests := []struct {
		name string
		dt   float64
		edit func(*Resources)
	}{
		{name: "negative elapsed time", dt: -1},
		{name: "nan elapsed time", dt: math.NaN()},
		{name: "infinite elapsed time", dt: math.Inf(1)},
		{name: "invalid final resource state", dt: 1, edit: func(r *Resources) { r.Food.Pool.Current = 11 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			resources := valid
			if test.edit != nil {
				test.edit(&resources)
			}
			before := resources
			if _, err := AdvanceEconomy(&resources, test.dt); err == nil {
				t.Fatal("expected invalid input to fail")
			}
			if !reflect.DeepEqual(resources, before) {
				t.Fatalf("resources changed despite error: got %+v, want %+v", resources, before)
			}
		})
	}
	if _, err := AdvanceEconomy(nil, 1); err == nil {
		t.Fatal("expected nil resources to fail")
	}
}

func TestAdvanceEconomyReportsPoolAlreadyAtZero(t *testing.T) {
	resources := Resources{}
	result, err := AdvanceEconomy(&resources, 0)
	if err != nil {
		t.Fatal(err)
	}
	want := []ResourceKey{ResourceWater, ResourceOxygen, ResourceEnergy, ResourceFood}
	if !reflect.DeepEqual(result.DepletedResources, want) {
		t.Fatalf("depleted resources = %v; want %v", result.DepletedResources, want)
	}
}

func TestAdvanceEconomyDoesNotChangeFrozenTeamSize(t *testing.T) {
	world, err := GenerateWorld(7, 3, DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	teamSize := world.TeamSize
	if _, err := AdvanceEconomy(&world.Resources, 1.0/30.0); err != nil {
		t.Fatal(err)
	}
	if world.TeamSize != teamSize {
		t.Fatalf("economy step changed frozen team size from %d to %d", teamSize, world.TeamSize)
	}
}

func assertRates(t *testing.T, name string, actual ResourceRates, config ResourceConfig, teamSize, baseCells int, food bool) {
	t.Helper()
	wantGPS := config.GPSPerTile * float64(baseCells)
	wantCPS := config.CPSPerPlayer*float64(teamSize) + config.CPSPerTile*float64(baseCells)
	if food {
		wantGPS = config.GPSPerPlayer * float64(teamSize)
		wantCPS = config.CPSPerPlayer * float64(teamSize)
	}
	wantStashMax := config.StashBase +
		config.StashPerTile*float64(baseCells) +
		config.StashPerPlayer*float64(teamSize)
	if actual.GPS != wantGPS || actual.CPS != wantCPS || actual.StashMax != wantStashMax {
		t.Errorf("%s rates = %+v; want GPS %v CPS %v stash max %v",
			name, actual, wantGPS, wantCPS, wantStashMax)
	}
}
