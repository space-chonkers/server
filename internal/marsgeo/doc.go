// Package marsgeo builds MarsTerrainDataV1 payloads (client contract, commit
// 147863f) from real HiRISE elevation data. It is a library with no HTTP,
// WebSocket or game-state dependencies; cmd/marsgeo-prep prepares its data.
//
// A backend-owned uint32 seed selects a product and a map centre from the
// verified registry. The payload is 51 x 51 corner samples of a 50 x 50 grid
// of 10 m cells, row-major from the north-west corner, in metres above the
// Mars 2000 areoid ("mars-2000-areoid"). Each sample is a 10 m area average
// (k x k strict bilinear sub-samples), not a point measurement.
//
// Corner placement uses the client's sphere (MarsMeanRadiusMeters); pixel
// lookup uses each product's own projection radius. Pixels matching the
// label MISSING_CONSTANT (by raw bits), any ISIS special value, NaN, Inf or
// a value outside the label VALID range are invalid, and any invalid pixel in
// a footprint fails the whole map with ErrNoCoverage. There is no fallback:
// no procedural, synthetic or gap-filled elevations exist in this package.
//
// Conventions, evidence and data provenance: docs/MARSGEO.md.
package marsgeo
