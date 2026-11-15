package marsgeo

// ElevationSource returns the elevation, in metres above Datum, for the
// corner at local point p of a map centred on center. cellSize is the grid's
// cell size in metres (it sets the averaging footprint). An implementation
// must fail rather than invent a value.
type ElevationSource interface {
	SampleCorner(center Coordinate, p LocalPoint, cellSize float64) (float64, error)
}

// Generate returns the (Columns+1) x (Rows+1) corner samples of grid g,
// row-major from the north-west corner (index = row*(Columns+1) + col). It
// aborts on the first error and never returns partial output.
func Generate(center Coordinate, g Grid, src ElevationSource) ([]float64, error) {
	if err := checkMapCenter(center); err != nil {
		return nil, err
	}
	cols, rows := g.Columns+1, g.Rows+1
	samples := make([]float64, 0, cols*rows)
	for row := 0; row < rows; row++ {
		for col := 0; col < cols; col++ {
			v, err := src.SampleCorner(center, g.CornerLocal(col, row), g.CellSizeMeters)
			if err != nil {
				return nil, err
			}
			samples = append(samples, v)
		}
	}
	return samples, nil
}
