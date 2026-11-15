// Command marsgeo-prep prepares HiRISE DTM data for internal/marsgeo. It is an
// offline tool: it is never imported by the server and is the only part of
// this repository that touches the network for terrain data.
//
// Full product (production data directory):
//
//	marsgeo-prep -url <DTEE*.IMG URL> -out $MARSGEO_DATA_DIR -registry registry.json
//
// Downloads the whole file, streaming its SHA-256, verifies the label, scans
// the valid-centre lattice and writes a one-entry registry.
//
// Crop (redistributable test data):
//
//	marsgeo-prep -url <URL> -crop x0,y0,w,h -source-sha256 <hex> -out <dir> -registry registry.json
//
// Fetches only the label and the needed lines with HTTP Range requests,
// writes a re-labelled <PRODUCT_ID>_crop.IMG, a provenance JSON and a
// registry for the crop.
//
// It exits non-zero on any failure. Label, format, coverage and checksum
// verification all happen before the first output file is written, so a
// verification failure writes nothing. Writes are not atomic as a set: an I/O
// error while writing crop-mode outputs (crop, provenance, registry) can
// leave the earlier files behind; delete the output and re-run.
package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

func main() {
	var cfg config
	flag.StringVar(&cfg.url, "url", "", "HiRISE DTEE* .IMG product URL (required)")
	flag.StringVar(&cfg.out, "out", "", "output directory for the product or crop (required)")
	flag.StringVar(&cfg.crop, "crop", "", "crop window x0,y0,w,h in source pixels (0-based); omit for the full product")
	flag.StringVar(&cfg.sourceSHA256, "source-sha256", "", "crop mode: SHA-256 of the full source file, recorded in provenance")
	flag.StringVar(&cfg.registry, "registry", "", "write a one-entry registry JSON to this path (required)")
	flag.StringVar(&cfg.registryVersion, "registry-version", "", "registryVersion to record (required)")
	flag.Float64Var(&cfg.latticeStepMeters, "lattice-step-m", 50, "spacing of candidate map centres, metres")
	flag.Parse()
	if cfg.url == "" || cfg.out == "" || cfg.registry == "" || cfg.registryVersion == "" {
		flag.Usage()
		os.Exit(2)
	}
	cfg.client = &http.Client{Timeout: 30 * time.Minute}
	cfg.commandLine = append([]string{filepath.Base(os.Args[0])}, os.Args[1:]...)
	if err := run(cfg); err != nil {
		fmt.Fprintln(os.Stderr, "marsgeo-prep:", err)
		os.Exit(1)
	}
}
