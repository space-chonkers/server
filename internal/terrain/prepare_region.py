"""Offline: turn one HiRISE DTM into a north-up 10 m heightmap region.

Run inside ghcr.io/osgeo/gdal:ubuntu-small-3.9.3 (see README.md):

    python3 prepare_region.py SOURCE.IMG SOURCE_SHA256 REGION_ID CATALOG_VERSION OUT_DIR [X0,Y0,X1,Y1]

It writes OUT_DIR/REGION_ID.f32 (little-endian float32, row 0 = north, column 0 = west)
and adds or replaces the region in OUT_DIR/catalog.json. The region is the largest fully
valid rectangle of the product, or of the optional source pixel window. Only fully valid
source pixels are used: no sample is filled or invented.
"""
import hashlib
import json
import math
import os
import re
import sys

import numpy as np
from osgeo import gdal, osr

gdal.UseExceptions()

MARS_RADIUS = 3389500.0  # the client's MARS_MEAN_RADIUS_METERS
SPACING = 10.0  # metres between samples
BLOCK = 5  # the valid-rectangle search works on 5 x 5 source-pixel blocks
# PDS3 label values this script relies on (first occurrence of each keyword).
LABEL = {"BANDS": "1", "SAMPLE_TYPE": "PC_REAL", "SAMPLE_BITS": "32", "SCALING_FACTOR": "1.0", "OFFSET": "0.0",
         "MISSING_CONSTANT": "16#FF7FFFFB#", "MAP_PROJECTION_TYPE": "EQUIRECTANGULAR", "MAP_PROJECTION_ROTATION": "0.0",
         "PROJECTION_LATITUDE_TYPE": "PLANETOCENTRIC", "POSITIVE_LONGITUDE_DIRECTION": "EAST"}


def require(ok, message):
    """Stop without writing anything unless ok. Unlike assert, this also runs under python3 -O."""
    if not ok:
        sys.exit(f"refusing to prepare region: {message}")


def check_label(path):
    """Refuse a product whose label is not a float32 equirectangular DTM above the Mars 2000 areoid."""
    with open(path, "rb") as f:
        text = re.split(r"^END\s*$", f.read(1 << 16).decode("latin-1"), maxsplit=1, flags=re.M)[0]
    keys = {}
    for k, v in re.findall(r"^\s*([A-Z_]+)\s*=\s*(\S+)", text, re.M):
        keys.setdefault(k, v.strip('"'))
    bad = {k: keys.get(k) for k, v in LABEL.items() if keys.get(k) != v}
    areoid = "meters above the martian equipotential surface (Mars 2000 Datum)" in " ".join(text.split())
    require(not bad and areoid, f"unsupported product label: {bad or 'elevations are not above the Mars 2000 areoid'}")


def sha256(path):
    with open(path, "rb") as f:
        return hashlib.file_digest(f, "sha256").hexdigest()


def largest_rectangle(ok):
    """Largest all-True axis-aligned rectangle: (top, left, bottom, right), end-exclusive."""
    heights = np.zeros(ok.shape[1], dtype=np.int64)
    best = (0, 0, 0, 0, 0)
    for r, row in enumerate(ok):
        heights = np.where(row, heights + 1, 0)
        stack = []  # (start column, height), heights increasing
        for c, h in enumerate(list(heights) + [0]):
            start = c
            while stack and stack[-1][1] >= h:
                s, sh = stack.pop()
                if sh * (c - s) > best[0]:
                    best = (sh * (c - s), r + 1 - sh, s, r + 1, c)
                start = s
            stack.append((start, h))
    return best[1:]


def main(src_path, src_sha, region_id, catalog_version, out_dir, window=None):
    if sha256(src_path) != src_sha:
        sys.exit("source SHA-256 mismatch")
    check_label(src_path)
    src = gdal.Open(src_path)
    band = src.GetRasterBand(1)
    z = band.ReadAsArray()
    # ISIS special values (NoData included) are all below -3.4e38.
    ok = np.isfinite(z) & (z > -1e30)

    # Largest fully valid rectangle, in source pixel-edge coordinates, with a 1 px margin.
    wx0, wy0, wx1, wy1 = map(int, window.split(",")) if window else (0, 0, z.shape[1], z.shape[0])
    sub = ok[wy0:wy1, wx0:wx1]
    rows, cols = sub.shape[0] // BLOCK, sub.shape[1] // BLOCK
    blocks = sub[: rows * BLOCK, : cols * BLOCK].reshape(rows, BLOCK, cols, BLOCK).all(axis=(1, 3))
    top, left, bottom, right = (int(v) * BLOCK for v in largest_rectangle(blocks))
    px0, px1, py0, py1 = wx0 + left + 1, wx0 + right - 1, wy0 + top + 1, wy0 + bottom - 1
    require(right > left and bottom > top and ok[py0:py1, px0:px1].all(), "no fully valid rectangle")

    # Source projection: equirectangular on the product's local sphere.
    srs = osr.SpatialReference(wkt=src.GetProjection())
    require(srs.GetAttrValue("PROJECTION") == "Equirectangular", f"projection {srs.GetAttrValue('PROJECTION')}")
    rs, lat_ts, lon_c = srs.GetSemiMajor(), srs.GetProjParm("standard_parallel_1"), srs.GetProjParm("central_meridian")
    gt = src.GetGeoTransform()
    require(gt[2] == gt[4] == 0 and gt[5] < 0, f"source raster is rotated or not north-up: {gt}")

    def src_lon(px):
        return lon_c + math.degrees((gt[0] + px * gt[1]) / (rs * math.cos(math.radians(lat_ts))))

    def src_lat(py):
        return math.degrees((gt[3] + py * gt[5]) / rs)

    lon_w, lon_e, lat_n, lat_s = src_lon(px0), src_lon(px1), src_lat(py0), src_lat(py1)

    # Target: equirectangular on the client's sphere, true scale at the region's middle latitude.
    lat_ref = round((lat_n + lat_s) / 2, 3)
    lon_ref = round((lon_w + lon_e) / 2, 3)
    k = MARS_RADIUS * math.cos(math.radians(lat_ref))
    proj = f"+proj=eqc +lat_ts={lat_ref} +lat_0=0 +lon_0={lon_ref} +R={MARS_RADIUS} +units=m +no_defs"
    x0 = math.ceil(k * math.radians(lon_w - lon_ref) / SPACING) * SPACING
    x1 = math.floor(k * math.radians(lon_e - lon_ref) / SPACING) * SPACING
    y0 = math.ceil(MARS_RADIUS * math.radians(lat_s) / SPACING) * SPACING
    y1 = math.floor(MARS_RADIUS * math.radians(lat_n) / SPACING) * SPACING

    # Both CRSs use planetocentric lat/lon on a sphere, so PROJ must keep lat/lon unchanged.
    dst_srs = osr.SpatialReference()
    dst_srs.ImportFromProj4(proj)
    t = osr.CoordinateTransformation(srs, dst_srs)
    for px, py in ((px0, py0), (px1, py1)):
        x, y, _ = t.TransformPoint(gt[0] + px * gt[1], gt[3] + py * gt[5])
        ex, ey = k * math.radians(src_lon(px) - lon_ref), MARS_RADIUS * math.radians(src_lat(py))
        require(abs(x - ex) < 1e-6 and abs(y - ey) < 1e-6, f"PROJ moved a corner: {(x, ex, y, ey)}")

    # Each sample is the area average of the source pixels in its own 10 m x 10 m square.
    out = gdal.Warp("", src, format="MEM", dstSRS=proj, outputBounds=(x0, y0, x1, y1),
                    xRes=SPACING, yRes=SPACING, resampleAlg="average", outputType=gdal.GDT_Float32,
                    workingType=gdal.GDT_Float64, dstNodata=float("nan"))
    ogt = out.GetGeoTransform()
    require(ogt[1] == SPACING and ogt[5] == -SPACING and ogt[2] == ogt[4] == 0, f"unexpected output grid {ogt}")
    heights = out.GetRasterBand(1).ReadAsArray().astype(np.float64)
    require(np.isfinite(heights).all() and (heights > -1e30).all(), "invalid sample in region")
    heights = np.round(heights, 3).astype("<f4")  # millimetres; HiRISE vertical precision is decimetres

    file = region_id + ".f32"
    heights.tofile(os.path.join(out_dir, file))
    region = {
        "id": region_id,
        "file": file,
        "sha256": sha256(os.path.join(out_dir, file)),
        "columns": heights.shape[1],
        "rows": heights.shape[0],
        "spacingMeters": SPACING,
        "northWestLatitude": math.degrees((ogt[3] - SPACING / 2) / MARS_RADIUS),
        "northWestLongitudeEast": lon_ref + math.degrees((ogt[0] + SPACING / 2) / k),
        "latitudeStepDegrees": math.degrees(SPACING / MARS_RADIUS),
        "longitudeStepDegrees": math.degrees(SPACING / k),
        "datum": "mars-2000-areoid",
        "source": {
            "datasetId": src.GetMetadataItem("DATA_SET_ID").strip('"'),
            "provider": "NASA/JPL/University of Arizona (HiRISE)",
            "resolutionMeters": gt[1],
            "productId": src.GetMetadataItem("PRODUCT_ID").strip('"'),
        },
        "provenance": {
            "sourceFile": os.path.basename(src_path),
            "sourceSha256": src_sha,
            "sourceWindow": window or "whole product",
            "sourcePixelEdges": f"x {px0}-{px1}, y {py0}-{py1}",
            "grid": proj,
            "resampling": f"GDAL {gdal.__version__} gdalwarp -r average",
        },
    }
    path = os.path.join(out_dir, "catalog.json")
    catalog = {"version": catalog_version, "regions": []}  # version first in a new file
    if os.path.exists(path):
        with open(path) as f:
            catalog = json.load(f)
    catalog["version"] = catalog_version
    catalog["regions"] = [r for r in catalog["regions"] if r["id"] != region_id] + [region]
    with open(path, "w") as f:
        json.dump(catalog, f, indent=2)
        f.write("\n")
    print(json.dumps({k: region[k] for k in ("id", "columns", "rows", "northWestLatitude", "northWestLongitudeEast")}))


if __name__ == "__main__":
    main(*sys.argv[1:])
