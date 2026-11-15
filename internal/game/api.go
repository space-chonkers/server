package game

// GridSize describes the fixed world dimensions in cells.
type GridSize struct {
	Width  int `json:"width"`
	Height int `json:"height"`
}

// Cell identifies one grid cell.
type Cell struct {
	CellX int `json:"cellX"`
	CellY int `json:"cellY"`
}

type ConstructionKind string

const (
	ConstructionBaseTile ConstructionKind = "base_tile"
	ConstructionStash    ConstructionKind = "stash"
)

// Construction is a one-cell structure in the starting base.
type Construction struct {
	ID    string           `json:"id"`
	Kind  ConstructionKind `json:"kind"`
	CellX int              `json:"cellX"`
	CellY int              `json:"cellY"`
}

type Facing string

const (
	FacingNorth Facing = "north"
	FacingEast  Facing = "east"
	FacingSouth Facing = "south"
	FacingWest  Facing = "west"
)

// PlayerSpawn contains the initial position and facing for one player slot.
type PlayerSpawn struct {
	Slot   int     `json:"slot"`
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Facing Facing  `json:"facing"`
}

type ResourcePool struct {
	Current float64 `json:"current"`
	Max     float64 `json:"max"`
}

type ResourceStash struct {
	Current float64 `json:"current"`
	Max     float64 `json:"max"`
	GPS     float64 `json:"gps"`
}

type ResourceState struct {
	Pool  ResourcePool  `json:"pool"`
	Stash ResourceStash `json:"stash"`
	CPS   float64       `json:"cps"`
}

type Resources struct {
	Water  ResourceState `json:"water"`
	Oxygen ResourceState `json:"oxygen"`
	Energy ResourceState `json:"energy"`
	Food   ResourceState `json:"food"`
}

// ResourceConfig holds the per-player and per-base-cell tuning values for one
// resource. Food uses GPSPerPlayer and CPSPerPlayer; its tile values are zero.
type ResourceConfig struct {
	PoolPerPlayer  float64
	StashBase      float64
	StashPerTile   float64
	StashPerPlayer float64
	GPSPerTile     float64
	GPSPerPlayer   float64
	CPSPerPlayer   float64
	CPSPerTile     float64
}

// Config contains the inputs needed to create an initial world.
type Config struct {
	InvestigationCount       int
	InvestigationMinDistance float64
	StartingPoolFraction     float64
	Water                    ResourceConfig
	Oxygen                   ResourceConfig
	Energy                   ResourceConfig
	Food                     ResourceConfig
}

// World is the immutable starting snapshot produced for a match.
type World struct {
	Grid          GridSize       `json:"grid"`
	BaseCellCount int            `json:"baseCellCount"`
	TeamSize      int            `json:"teamSize"`
	Constructions []Construction `json:"constructions"`
	Investigation []Cell         `json:"investigation"`
	Spawns        []PlayerSpawn  `json:"spawns"`
	Resources     Resources      `json:"resources"`
}

// DefaultConfig returns the M1 placeholder tuning values from docs/SCOPE.md.
func DefaultConfig() Config {
	return Config{
		InvestigationCount:       10,
		InvestigationMinDistance: 5,
		StartingPoolFraction:     0.5,
		Water: ResourceConfig{
			PoolPerPlayer: 50, StashBase: 10, StashPerTile: 1, StashPerPlayer: 5,
			GPSPerTile: 0.04, CPSPerPlayer: 0.30, CPSPerTile: 0.08,
		},
		Oxygen: ResourceConfig{
			PoolPerPlayer: 50, StashBase: 10, StashPerTile: 1, StashPerPlayer: 5,
			GPSPerTile: 0.04, CPSPerPlayer: 0.30, CPSPerTile: 0.08,
		},
		Energy: ResourceConfig{
			PoolPerPlayer: 50, StashBase: 10, StashPerTile: 1, StashPerPlayer: 5,
			GPSPerTile: 0.03, CPSPerPlayer: 0.25, CPSPerTile: 0.06,
		},
		Food: ResourceConfig{
			PoolPerPlayer: 40, StashBase: 10, StashPerTile: 1, StashPerPlayer: 5,
			GPSPerPlayer: 0.15, CPSPerPlayer: 0.30,
		},
	}
}
