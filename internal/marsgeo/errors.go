package marsgeo

import "errors"

// Sentinel errors. Callers match them with errors.Is; the wrapped message
// carries the detail. The 4A.5 HTTP layer maps them one to one onto problem
// codes (docs/MARSGEO.md).
var (
	// ErrNoCoverage: the requested footprint touches a pixel that is outside
	// the raster or invalid. Deterministic; never servable. (HTTP 422.)
	ErrNoCoverage = errors.New("marsgeo: no valid elevation coverage")
	// ErrDataUnavailable: a data file is missing, unreadable, or fails its
	// checksum or label check. Retryable once the data is fixed. (HTTP 503.)
	ErrDataUnavailable = errors.New("marsgeo: elevation data unavailable")
	// ErrInvalidProduct: a PDS label fails verification or describes an
	// unsupported format. Raised by the reader, prep and registry load.
	ErrInvalidProduct = errors.New("marsgeo: invalid or unsupported PDS product")
)
