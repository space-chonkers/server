# Mars terrain

`GET /v1/mars-terrain/{seed}` returns a 500 m × 500 m map of real Martian terrain as a
`MarsTerrainDataV1` payload (client contract: `src/terrain-kit/marsTerrainContract.ts`).
The payload has 50 × 50 cells of 10 m, 51 × 51 corner elevations in metres above the
Mars 2000 areoid, north-west first.

| Status | When |
|---|---|
| 200 | `seed` is a decimal integer in [0, 4294967295] with no sign or leading zeros |
| 400 | anything else |
| 503 | the catalog failed to load at startup (the server logs why) |

## How it works

`catalog.json` lists **regions**. Each region is a file of little-endian float32
heights, row-major, row 0 north and column 0 west, with samples 10 m apart. The
server embeds the catalog and checks every region's size, SHA-256 and values at
startup. For each request it:

1. hashes the seed (SplitMix64) and picks a region **uniformly**, so a large region
   doesn't crowd out small ones;
2. hashes again and picks a **window**, meaning any 51 × 51 block of samples in that
   region;
3. copies that block's 2,601 heights;
4. takes the centre sample (window row 25, column 25) as the map centre:
   `latitude = northWestLatitude − row × latitudeStepDegrees` and
   `longitude = northWestLongitudeEast + column × longitudeStepDegrees`.

The same seed and catalog `version` always give the same map. Adding, removing,
reordering or changing a region changes which map a seed selects, so bump
`version` when you do.

## Current data (catalog 2026-11-15.2)

| Region | Product | Centre | Size | Windows | File |
|---|---|---|---|---|---|
| Gigas Sulci channels | `DTEED_029716_1980_029426_1980_A02` | 17.83° N, 232.49° E | 3.3 × 15.9 km | 433,998 | 2,110,192 B |
| Russell Crater dunes | `DTEEC_007018_1255_007229_1255_A01` | 54.29° S, 12.94° E | 1.5 × 3.9 km | 35,490 | 240,560 B |
| Jezero Crater fan | `DTEEC_002387_1985_003798_1985_A01` | 18.49° N, 77.41° E | 4.3 × 10.6 km | 382,536 | 1,818,144 B |
| SW Candor Chasma layers | `DTEEC_001918_1735_001984_1735_U01` | 6.47° S, 283.07° E | 4.0 × 9.9 km | 327,816 | 1,579,264 B |

Each product's SHA-256 and the source pixels used are in its region's `provenance`
in `catalog.json`. All are `MRO-M-HIRISE-5-DTM-V1.0` products, downloaded from
`https://hirise-pds.lpl.arizona.edu/PDS/DTM/` +

- `ESP/ORB_029700_029799/ESP_029716_1980_ESP_029426_1980/DTEED_029716_1980_029426_1980_A02.IMG`
- `PSP/ORB_007000_007099/PSP_007018_1255_PSP_007229_1255/DTEEC_007018_1255_007229_1255_A01.IMG`
- `PSP/ORB_002300_002399/PSP_002387_1985_PSP_003798_1985/DTEEC_002387_1985_003798_1985_A01.IMG`
- `PSP/ORB_001900_001999/PSP_001918_1735_PSP_001984_1735/DTEEC_001918_1735_001984_1735_U01.IMG`

Credit: NASA/JPL/University of Arizona ([usage policy](https://uahirise.org/media/usage.php)).

A region is the largest axis-aligned rectangle in which every source pixel is
valid, shrunk by a 1-pixel margin. For Russell the search is limited to source
window `1100,2500,3948,6400`. Outside that window the DTM contains planar
triangulated infill and square edit patches: they are valid numbers, but they are
not measured terrain. Each sample is the GDAL `average` of the source pixels in its
own 10 m × 10 m square, rounded to the millimetre. No height is filled or invented.
Recomputing served samples directly from each source with GDAL agrees to within
1 cm RMS (9 cm at most).

## Geographic approximation

A region is one equirectangular grid on the client's sphere (R = 3,389,500 m),
centred at its own reference latitude (`provenance.grid`). Rows are exactly 10 m
apart in latitude everywhere. Columns are exactly 10 m apart at the reference
latitude. The client lays a map out with its own centre latitude instead, so
east–west spacing in a map is off by `cos(lat)/cos(ref) − 1`. That is at most 0.073%
(0.18 m at a map's east or west edge) in the current regions. Keep regions short
from north to south at high latitudes to keep it small.

## Adding or rebuilding a region

Use a HiRISE DTM in an equirectangular projection, within ±85° latitude. The script
refuses a product unless its label says: float32 `PC_REAL`, one band, scale 1,
offset 0, ISIS NULL as the missing constant, an equirectangular, planetocentric,
east-positive projection, no rotation, and elevations above the Mars 2000 areoid.
Look at a hillshade of the product (`gdaldem hillshade`) before choosing a window:
the rectangle search sees only missing data, not interpolated infill.

```sh
mkdir -p "$DATA" && curl -fo "$DATA/PRODUCT.IMG" "$PRODUCT_URL" && sha256sum "$DATA/PRODUCT.IMG"
docker run --rm --user "$(id -u):$(id -g)" -v "$DATA":/data:ro -v "$PWD/internal/terrain":/out \
  ghcr.io/osgeo/gdal:ubuntu-small-3.9.3 \
  python3 -I /out/prepare_region.py /data/PRODUCT.IMG "$SHA256" REGION_ID NEW_CATALOG_VERSION /out [X0,Y0,X1,Y1]
go test ./internal/terrain
```

The script refuses a file whose SHA-256 differs from the one you pass. It writes
`REGION_ID.f32` and adds or replaces that region at the end of `catalog.json`. To
rebuild the current catalog, run it for the products in the table order above with
the `sourceSha256` and `sourceWindow` recorded in each region's `provenance`. The
committed files are reproduced byte for byte.
