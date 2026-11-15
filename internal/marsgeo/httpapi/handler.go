// Package httpapi serves MarsTerrainDataV1 payloads over HTTP:
//
//	GET /v1/mars-terrain/{seed}
//
// It is transport only: it parses the seed, asks a Builder for the payload,
// and handles encoding, caching headers and RFC 9457 problem responses.
// Terrain generation lives in internal/marsgeo, which does not import this
// package or net/http.
package httpapi

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/space-chonkers/server/internal/marsgeo"
)

// Route is the mux pattern this handler expects; {seed} is read with
// Request.PathValue.
const Route = "GET /v1/mars-terrain/{seed}"

// Builder produces the payload for a seed. *marsgeo.LazyTerrain and
// *marsgeo.Terrain implement it.
type Builder interface {
	Build(seed uint32) (marsgeo.MarsTerrainDataV1, error)
}

// Options tunes the handler. Zero values use the defaults.
type Options struct {
	// RetryAfter is sent with 503 responses. Default: marsgeo.DefaultOpenRetryInterval.
	RetryAfter time.Duration
}

const (
	cacheControlOK    = "public, max-age=86400"
	cacheControlError = "no-store"
	problemTypePrefix = "urn:space-chonkers:problem:"
)

// Problem codes: stable, machine-readable identifiers (docs/MARSGEO.md §9).
const (
	CodeInvalidRequest  = "invalid-request"
	CodeNoCoverage      = "no-coverage"
	CodeDataUnavailable = "data-unavailable"
	CodeInternal        = "internal"
)

type handler struct {
	b               Builder
	registryVersion string
	log             *slog.Logger
	retryAfter      time.Duration
}

// NewHandler returns the handler for Route. registryVersion is part of every
// ETag, so a registry change invalidates cached responses.
func NewHandler(b Builder, registryVersion string, log *slog.Logger, opts Options) http.Handler {
	if opts.RetryAfter <= 0 {
		opts.RetryAfter = marsgeo.DefaultOpenRetryInterval
	}
	if log == nil {
		log = slog.Default()
	}
	return &handler{b: b, registryVersion: registryVersion, log: log, retryAfter: opts.RetryAfter}
}

// seedPattern: decimal, no sign, no leading zeros except "0", at most 10 digits.
var seedPattern = regexp.MustCompile(`^(0|[1-9][0-9]{0,9})$`)

// parseSeed accepts exactly the decimal uint32 range.
func parseSeed(s string) (uint32, bool) {
	if !seedPattern.MatchString(s) {
		return 0, false
	}
	v, err := strconv.ParseUint(s, 10, 32)
	return uint32(v), err == nil
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	raw := r.PathValue("seed")
	seed, ok := parseSeed(raw)
	if !ok {
		h.problem(w, http.StatusBadRequest, CodeInvalidRequest, "Invalid seed",
			"seed must be a decimal integer in [0, 4294967295] with no sign or leading zeros", false)
		return
	}

	d, err := h.b.Build(seed)
	if err != nil {
		h.buildError(w, seed, err)
		return
	}
	body, err := json.Marshal(&d)
	if err != nil {
		h.log.Error("mars terrain: encode payload", "seed", seed, "error", err)
		h.problem(w, http.StatusInternalServerError, CodeInternal, "Internal error", "the terrain payload could not be produced", true)
		return
	}

	gz := acceptsGzip(r.Header.Values("Accept-Encoding"))
	sum := sha256.Sum256(append([]byte(h.registryVersion+"\x00"), body...))
	etag := `"` + hex.EncodeToString(sum[:])
	if gz {
		etag += "-gzip"
	}
	etag += `"`

	hdr := w.Header()
	hdr.Set("Vary", "Accept-Encoding")
	hdr.Set("ETag", etag)
	hdr.Set("Cache-Control", cacheControlOK)
	if ifNoneMatch(r.Header.Values("If-None-Match"), etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	hdr.Set("Content-Type", "application/json")
	if gz {
		var buf bytes.Buffer
		zw := gzip.NewWriter(&buf) // no name or mtime: identical bytes for identical bodies
		_, _ = zw.Write(body)
		if err := zw.Close(); err != nil {
			h.log.Error("mars terrain: gzip", "seed", seed, "error", err)
			h.problem(w, http.StatusInternalServerError, CodeInternal, "Internal error", "the terrain payload could not be produced", true)
			return
		}
		body = buf.Bytes()
		hdr.Set("Content-Encoding", "gzip")
	}
	hdr.Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

// buildError maps engine sentinels one to one onto problem responses.
// Internal error text is logged, never echoed.
func (h *handler) buildError(w http.ResponseWriter, seed uint32, err error) {
	switch {
	case errors.Is(err, marsgeo.ErrNoCoverage):
		h.log.Warn("mars terrain: no coverage", "seed", seed, "error", err)
		h.problem(w, http.StatusUnprocessableEntity, CodeNoCoverage, "No elevation coverage",
			"real elevation data does not cover the location this seed selects", false)
	case errors.Is(err, marsgeo.ErrDataUnavailable):
		h.log.Warn("mars terrain: data unavailable", "seed", seed, "error", err)
		w.Header().Set("Retry-After", strconv.Itoa(int((h.retryAfter+time.Second-1)/time.Second)))
		h.problem(w, http.StatusServiceUnavailable, CodeDataUnavailable, "Elevation data unavailable",
			"elevation data is temporarily unavailable; retry later", true)
	default:
		h.log.Error("mars terrain: build failed", "seed", seed, "error", err)
		h.problem(w, http.StatusInternalServerError, CodeInternal, "Internal error", "the terrain payload could not be produced", true)
	}
}

// problemDetails is an RFC 9457 problem object with two extension members.
type problemDetails struct {
	Type      string `json:"type"`
	Title     string `json:"title"`
	Status    int    `json:"status"`
	Detail    string `json:"detail"`
	Code      string `json:"code"`
	Retryable bool   `json:"retryable"`
}

func (h *handler) problem(w http.ResponseWriter, status int, code, title, detail string, retryable bool) {
	body, _ := json.Marshal(problemDetails{Type: problemTypePrefix + code, Title: title, Status: status, Detail: detail, Code: code, Retryable: retryable})
	hdr := w.Header()
	hdr.Set("Content-Type", "application/problem+json")
	hdr.Set("Cache-Control", cacheControlError)
	hdr.Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// acceptsGzip applies RFC 9110 §12.5.3: gzip (or x-gzip) is acceptable if
// listed with q > 0, or if "*" is listed with q > 0 and gzip is not listed.
// A malformed qvalue makes that entry unacceptable.
func acceptsGzip(values []string) bool {
	gzipQ, starQ := -1.0, -1.0
	for _, v := range values {
		for _, item := range strings.Split(v, ",") {
			parts := strings.Split(item, ";")
			coding := strings.ToLower(strings.TrimSpace(parts[0]))
			if coding == "" {
				continue
			}
			q := 1.0
			for _, p := range parts[1:] {
				k, val, _ := strings.Cut(strings.TrimSpace(p), "=")
				if strings.EqualFold(strings.TrimSpace(k), "q") {
					f, err := strconv.ParseFloat(strings.TrimSpace(val), 64)
					if err != nil || f < 0 || f > 1 {
						f = 0
					}
					q = f
				}
			}
			switch coding {
			case "gzip", "x-gzip":
				gzipQ = max(gzipQ, q)
			case "*":
				starQ = max(starQ, q)
			}
		}
	}
	if gzipQ >= 0 {
		return gzipQ > 0
	}
	return starQ > 0
}

// ifNoneMatch reports whether an If-None-Match header matches etag, using
// the weak comparison RFC 9110 §13.1.2 requires for this header ("*" matches
// any current representation).
func ifNoneMatch(values []string, etag string) bool {
	for _, v := range values {
		for _, tag := range strings.Split(v, ",") {
			tag = strings.TrimSpace(tag)
			if tag == "*" || strings.TrimPrefix(tag, "W/") == etag {
				return true
			}
		}
	}
	return false
}
