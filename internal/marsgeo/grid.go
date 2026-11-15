package marsgeo

// Grid is the logical cell grid of one map: Columns west->east, Rows
// north->south, each cell CellSizeMeters on the ground.
type Grid struct {
	Columns        int     `json:"columns"`
	Rows           int     `json:"rows"`
	CellSizeMeters float64 `json:"cellSizeMeters"`
}

// Phase 4A map size: 50 x 50 cells of 10 m, so 51 x 51 corner samples.
var DefaultGrid = Grid{Columns: 50, Rows: 50, CellSizeMeters: 10}

// CornerLocal returns the local position of corner (col, row) per contract §3:
//
//	east = (col - C/2)·s    north = (N/2 - row)·s
//
// Row 0 is the north edge, column 0 the west edge.
func (g Grid) CornerLocal(col, row int) LocalPoint {
	c, n, s := float64(g.Columns), float64(g.Rows), g.CellSizeMeters
	return LocalPoint{
		East:  (float64(col) - c/2) * s,
		North: (n/2 - float64(row)) * s,
	}
}
