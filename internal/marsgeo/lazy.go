package marsgeo

import (
	"fmt"
	"sync"
	"time"
)

// DefaultOpenRetryInterval is how long LazyTerrain reuses a failed open
// before trying again. Opening hashes every product file (about 0.3 s for
// A02), so retrying on every request would turn a broken data directory into
// a CPU and I/O amplifier.
const DefaultOpenRetryInterval = 30 * time.Second

// LazyTerrain owns a Terrain's lifecycle for long-running callers such as
// the HTTP server. It opens the registry on first use (never at
// construction, so startup never blocks or fails on missing data), caches a
// failed open for the retry interval, and makes Close safe against
// concurrent Build calls: Build runs under a read lock and Close takes the
// write lock, so Close waits for in-flight builds and later calls fail with
// ErrDataUnavailable. It is safe for concurrent use.
type LazyTerrain struct {
	reg     *Registry
	dataDir string
	retry   time.Duration
	now     func() time.Time

	mu          sync.RWMutex
	terr        *Terrain
	closed      bool
	lastErr     error
	lastAttempt time.Time
}

// NewLazyTerrain returns a LazyTerrain for reg and dataDir. retry <= 0 uses
// DefaultOpenRetryInterval. reg may be nil (every Build then reports
// ErrDataUnavailable).
func NewLazyTerrain(reg *Registry, dataDir string, retry time.Duration) *LazyTerrain {
	if retry <= 0 {
		retry = DefaultOpenRetryInterval
	}
	return &LazyTerrain{reg: reg, dataDir: dataDir, retry: retry, now: time.Now}
}

// RegistryVersion is the registry's version, available without opening.
func (l *LazyTerrain) RegistryVersion() string {
	if l.reg == nil {
		return ""
	}
	return l.reg.RegistryVersion
}

// Build opens the terrain if needed and builds the payload for seed.
func (l *LazyTerrain) Build(seed uint32) (MarsTerrainDataV1, error) {
	for attempt := 0; attempt < 2; attempt++ {
		l.mu.RLock()
		if l.terr != nil {
			d, err := l.terr.Build(seed)
			l.mu.RUnlock()
			return d, err
		}
		l.mu.RUnlock()
		if err := l.open(); err != nil {
			return MarsTerrainDataV1{}, err
		}
	}
	// open succeeded but the terrain was closed in between.
	return MarsTerrainDataV1{}, fmt.Errorf("%w: terrain is closed", ErrDataUnavailable)
}

// open opens the terrain once, or returns the cached failure while it is
// younger than the retry interval.
func (l *LazyTerrain) open() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	switch {
	case l.closed:
		return fmt.Errorf("%w: terrain is closed", ErrDataUnavailable)
	case l.terr != nil:
		return nil
	case l.lastErr != nil && l.now().Sub(l.lastAttempt) < l.retry:
		return l.lastErr
	case l.reg == nil:
		return fmt.Errorf("%w: no registry", ErrDataUnavailable)
	}
	l.lastAttempt = l.now()
	terr, err := l.reg.Open(l.dataDir)
	if err != nil {
		l.lastErr = err // Open already wraps ErrDataUnavailable
		return err
	}
	l.terr, l.lastErr = terr, nil
	return nil
}

// Close waits for in-flight Build calls, then closes the terrain. Later
// Build calls fail with ErrDataUnavailable. Calling it again is a no-op.
func (l *LazyTerrain) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.closed = true
	if l.terr == nil {
		return nil
	}
	err := l.terr.Close()
	l.terr = nil
	return err
}
