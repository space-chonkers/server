# Mars topography (`internal/marsgeo`)

`internal/marsgeo` builds `MarsTerrainDataV1` payloads (client contract, commit `147863f`) from real HiRISE elevation
data. It is a library: no HTTP, no WebSocket, no game state. `cmd/marsgeo-prep` is the offline tool that prepares the
data it reads.

Status: Phase 4A (engine). The HTTP endpoint (section 9) is Phase 4A.5.

## 1. What a payload contains

- 50 × 50 cells of 10 m, so 51 × 51 = 2,601 corner samples.
- Samples are row-major from the north-west corner: `index = row·51 + column`, row 0 = north edge, column 0 = west
  edge.
- Elevations are metres above the Mars 2000 areoid. `elevation.datum = "mars-2000-areoid"`. Every HiRISE `DTEE*` label
  names this surface ("meters above the martian equipotential surface (Mars 2000 Datum) defined by Smith, et al.
  (2001)"). No datum conversion is done. The per-product MOLA tie offset is a measure of accuracy, not a datum shift.
- `source.datasetId = "MRO-M-HIRISE-5-DTM-V1.0"`, `source.provider = "NASA/JPL/University of Arizona (HiRISE)"`,
  `source.resolutionMeters` = the label's `MAP_SCALE`, and `source.productId` = the label's `PRODUCT_ID`.
- `seed` is the backend-owned `uint32` that chose the location (section 6).

**Each sample is a 10 m area average of the DTM, not a point measurement** (section 4).

## 2. Two radii, never mixed

| Constant | Value | Used for |
|---|---|---|
| `MarsMeanRadiusMeters` | 3,389,500 m | Placing corners and kernel sub-samples (local ↔ geographic). Contract §3; identical to the client. |
| `Label.RadiusMeters` | the label's `A_AXIS_RADIUS` (3,394,839.8133163 m for A02) | Geographic → pixel inside that product only. |

HiRISE labels use a local radius of the Mars ellipsoid at `CENTER_LATITUDE`, which differs per product. Contract §3
says HiRISE "often use 3,396,190 m", which is not what the labels contain (follow-up for the client team, section 10).

Corner placement ports `marsCoordinates.ts` step by step. The values `π/180` and R are held in runtime `float64`
variables because Go folds constant expressions exactly while JavaScript rounds after each operation.

## 3. Supported products and missing data

- Only HiRISE `DTEE*` products: equirectangular, planetocentric, east-positive longitudes, no rotation, elevations
  above the areoid. `DTER*` (radii) and `DTEP*` (polar stereographic) are rejected.
- The binary layout is read from the product's own label, never assumed: `RECORD_TYPE = FIXED_LENGTH`,
  `RECORD_BYTES = LINE_SAMPLES × 4`, `^IMAGE` as a record pointer or `n <BYTES>`, single band, no line
  prefix or suffix, `SCALING_FACTOR = 1`, `OFFSET = 0`. A file shorter than the declared image is rejected, never
  zero-filled.
- The only sample format accepted is `SAMPLE_TYPE = PC_REAL` with `SAMPLE_BITS = 32` (little-endian IEEE 754
  binary32). Every other type, size, band count or detached pointer is rejected rather than guessed.
- `MISSING_CONSTANT` must be `16#FF7FFFFB#` (ISIS NULL4). That bit pattern decodes to a finite float32 (about
  −3.4028e38), so NaN/Inf checks alone would accept it. A pixel is therefore invalid if any of these holds, checked in
  this order:
  1. its raw bits equal `MISSING_CONSTANT`, or lie anywhere in the ISIS special range `0xFF7FFFFA`–`0xFF7FFFFF`;
  2. it is NaN or ±Inf;
  3. it is outside `[VALID_MINIMUM, VALID_MAXIMUM]`.
- One predicate (`pixelValidity.valid`) applies this rule everywhere a pixel is used. `CheckShape` repeats the
  special-pattern and finite checks on every outgoing sample.
- There is no gap filling and no fallback (section 7).

## 4. Resampling: k × k area-averaged strict bilinear

For each corner at local position (E, N), in metres from the map centre:

1. `k = ceil(cellSize / MAP_SCALE)`. For A02, 10 / 2.0195488956581 = 4.95, so **k = 5**. The prep tool stores k in
   the registry, and it is re-checked on load.
2. Sub-sample offsets are `o_i = ((i + 0.5)/k − 0.5)·cellSize` for `i = 0…k−1`, which gives **−4, −2, 0, +2, +4 m**
   for k = 5. Sub-sample (i, j) sits at local `(E + o_i, N − o_j)`; j = 0 is the north row. The 25 sub-samples cover
   the corner's own 10 m × 10 m square, so neighbouring footprints tile the map with no gaps or overlap.
3. Each sub-sample goes local → geographic with R = 3,389,500 m, then geographic → fractional pixel with the
   product's radius (section 5).
4. **Strict bilinear.** `x0 = floor(x)`, `y0 = floor(y)`. All four stencil pixels must be inside the raster and valid,
   even those with weight 0. A sample exactly on the last column or line is therefore rejected.
5. The corner value is `Σ_j Σ_i v_ij / k²`, summed north → south, then west → east, in float64. Values are not
   rounded.
6. If any stencil pixel of any sub-sample is invalid or outside the raster, the corner fails with `ErrNoCoverage`.
   The whole tile then fails and no partial payload is returned.

**Determinism.** The same seed, registry and raster bytes give bit-identical payload bytes on the same GOARCH,
`GOAMD64`/`GOARM64` level and Go toolchain. Explicit `float64(...)` conversions stop this package's own multiply-adds
from being fused. `math.Cos` itself may be fused on arm64 or on amd64 v3, so bit identity across architectures is not
claimed. Values still agree to well within 1e-6 m.

## 5. Pixel convention (verified; Phase 4A P5 gate)

0-based pixels; integer `(i, j)` is the **centre** of pixel (i, j); x grows east, y grows south; pixel (0, 0) is the
north-west pixel and covers `[−0.5, 0.5)` on both axes.

```
x = SAMPLE_PROJECTION_OFFSET + rad(Δλ)·R_p·cos(rad(CENTER_LATITUDE)) / MAP_SCALE − pixelIndexShift
y = LINE_PROJECTION_OFFSET   − rad(φ)·R_p / MAP_SCALE                            − pixelIndexShift
```

Here Δλ is the signed shortest difference from `CENTER_LONGITUDE`. **`pixelIndexShift = 0`.**

Evidence, gathered 2026-11-15:

1. **ISIS writer** (`ProcessExportPds.cpp`, ISIS 8.0.0, lines 760–773). The labels say the DTMs were "produced using
   ISIS and SOCET Set":
   ```
   // These keywords are the distance from the origin of the image to the
   // origin of the projection. The units are line or samples. The image origin
   // is the middle of pixel (1,1)
   lineOffset = UpperLeftCornerY / PixelResolution;  lineOffset -= 0.5;
   sampleOffset = -UpperLeftCornerX / PixelResolution; sampleOffset -= 0.5;
   ```
   So the outer corner is at `X = −(S0 + 0.5)·scale` and `Y = (L0 + 0.5)·scale`, and the centre of 0-based pixel i is at
   `X = (i − S0)·scale`. That is the formula above with a shift of 0.
2. **GDAL reader** (`frmts/pds/pdsdataset.cpp`, v3.9.3, lines 358–411). The defaults are
   `PDS_SampleProjOffset_Shift = 0.5`, `PDS_LineProjOffset_Shift = 0.5`, `PDS_SampleProjOffset_Mult = −1.0` and
   `PDS_LineProjOffset_Mult = 1.0`, giving `ULX = −(S0 + 0.5)·Xdim` and `ULY = (L0 + 0.5)·|Ydim|`. The source comment
   reads "defaults should be correct for what is documented in the PDS3 standard" (history in GDAL ticket #5941).
   This is the same convention as ISIS.
3. **HiRISE `DSMAP.CAT`** (hirise-pds.lpl.arizona.edu/PDS/CATALOG/DSMAP.CAT, sha256 `cc3d3b20…b3201d479`)
   contradicts itself. It gives `x = (Sample − S0)·Scale`, but also `Sample = x/Scale + S0 + 1` and
   `x = (Sample − S0 − 1)·Scale`, and its Line equation has the sign of L0 reversed. It cannot settle a ±1 and is not
   used.
4. **Independent GDAL run**: `internal/marsgeo/testdata/hirise/gdal_reference.json`, produced by
   `gdal_reference.sh` with `ghcr.io/osgeo/gdal:ubuntu-small-3.9.3`
   (`sha256:7125d6f5d6af5331354328fc6a64c9e420b29b31460d1a6aaf9526e559255e07`). GDAL reads the real A02 product over
   `/vsicurl/` with default config options. It contains:
   - `gdalinfo -json`: geotransform `[−3766.4586904023568, 2.0195488956581, 0, 1064633.4740306863, 0,
     −2.0195488956581]`, an SRS with radius 3,394,839.8133163 m, standard parallel 15° and central meridian 232.49°,
     and NoData = MISSING_CONSTANT;
   - `gdaltransform`: lon/lat at the corner and centre of 9 pixels (the four raster corners and 5 interior pixels);
   - `gdallocationinfo`: the pixel and value at those centres and at offsets of ±0.25, ±0.49 and ±1 px on each axis
     around two interior pixels.

   Result: our georef matches every GDAL pair to within 1e-9° and every reported pixel exactly. Shifts of ±0.5 and ±1
   are all detected (`TestGDALReferenceDetectsWrongConvention`).

**Known label quirk.** The label's `MAXIMUM_LATITUDE`, `MINIMUM_LATITUDE`, `WESTERNMOST_LONGITUDE` and
`EASTERNMOST_LONGITUDE` map to y = 0.85, 8252.51 and x = 0.95, 3504.82. Under the verified convention those are not
the raster edges (−0.5 and W − 0.5); they lie about one pixel off, inside on the north and west and outside on the south
and east. The bounds are informational, and this package never uses them. `TestA02LabelBoundsRegression` pins the
observed values.

## 6. Coverage and seed selection

*(Completed in P6–P7.)*

## 7. No fallback

There is no procedural, synthetic or gap-filled code path. Every failure is a typed error:

| Error | Meaning |
|---|---|
| `ErrNoCoverage` | The footprint touches an invalid pixel or leaves the raster. Deterministic, never servable. |
| `ErrDataUnavailable` | A data file is missing or unreadable, or fails its size, SHA-256 or label check. Retryable once fixed. |
| `ErrInvalidProduct` | A label fails verification or describes an unsupported format (prep and registry load). |
| `ErrInvalidCoordinate` | A centre the local mapping cannot represent. |

## 8. Data and preparation

*(Completed in P8–P9.)*

## 9. HTTP design (approved; implemented in Phase 4A.5)

*(See plan section 5.7 and 11.)*

## 10. Client-team follow-ups (not actioned; the client repo is read-only here)

- Contract §3 says HiRISE DTMs "often use 3,396,190 m". The labels actually use a per-product local radius
  (3,394,839.8 m for A02).
- The schema's `elevation.datum` description still reads "Value still to be agreed". Proposal: pin it with
  `const: "mars-2000-areoid"`.

## Attribution

HiRISE data: NASA/JPL/University of Arizona. Usage policy: https://uahirise.org/media/usage.php (public domain;
credit requested).
