package marsgeo

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestLazyTerrainOpensOnFirstUse(t *testing.T) {
	dir := t.TempDir()
	reg, _ := writeSynthRegistry(t, dir, mapProduct(232.49))
	l := NewLazyTerrain(reg, dir, time.Minute)
	defer l.Close()
	if l.terr != nil {
		t.Fatal("opened at construction")
	}
	want, err := func() (MarsTerrainDataV1, error) {
		terr, err := reg.Open(dir)
		if err != nil {
			return MarsTerrainDataV1{}, err
		}
		defer terr.Close()
		return terr.Build(3)
	}()
	if err != nil {
		t.Fatal(err)
	}
	got, err := l.Build(3)
	if err != nil {
		t.Fatal(err)
	}
	if got.Center != want.Center || got.Elevation.SamplesMeters[100] != want.Elevation.SamplesMeters[100] {
		t.Fatal("LazyTerrain.Build differs from Terrain.Build")
	}
	if l.RegistryVersion() != "test-1" {
		t.Fatalf("RegistryVersion = %q", l.RegistryVersion())
	}
}

func TestLazyTerrainCachesFailedOpen(t *testing.T) {
	dir := t.TempDir()
	reg, _ := writeSynthRegistry(t, dir, mapProduct(232.49))
	file := filepath.Join(dir, "synthetic.IMG")
	hidden := file + ".hidden"
	if err := os.Rename(file, hidden); err != nil {
		t.Fatal(err)
	}
	clock := time.Unix(1000, 0)
	l := NewLazyTerrain(reg, dir, 30*time.Second)
	l.now = func() time.Time { return clock }
	defer l.Close()

	if _, err := l.Build(1); !errors.Is(err, ErrDataUnavailable) {
		t.Fatalf("missing file: %v", err)
	}
	// The data appears, but within the retry interval the cached failure stands.
	os.Rename(hidden, file)
	clock = clock.Add(29 * time.Second)
	if _, err := l.Build(1); !errors.Is(err, ErrDataUnavailable) {
		t.Fatalf("retried before the interval: %v", err)
	}
	clock = clock.Add(2 * time.Second)
	if _, err := l.Build(1); err != nil {
		t.Fatalf("not retried after the interval: %v", err)
	}
}

func TestLazyTerrainUnconfigured(t *testing.T) {
	for name, l := range map[string]*LazyTerrain{
		"no data dir":  NewLazyTerrain(&Registry{RegistryVersion: "v", Entries: []Entry{{}}}, "", 0),
		"nil registry": NewLazyTerrain(nil, "/nonexistent", 0),
		"empty":        NewLazyTerrain(&Registry{RegistryVersion: "v"}, t.TempDir(), 0),
	} {
		if _, err := l.Build(1); !errors.Is(err, ErrDataUnavailable) {
			t.Errorf("%s: %v", name, err)
		}
		if err := l.Close(); err != nil {
			t.Errorf("%s: Close %v", name, err)
		}
	}
}

// TestLazyTerrainCloseDuringBuilds: Close while many goroutines build; run
// with -race. Every Build either succeeds with a full payload or fails with
// ErrDataUnavailable; nothing panics or reads a closed file mid-build.
func TestLazyTerrainCloseDuringBuilds(t *testing.T) {
	dir := t.TempDir()
	reg, _ := writeSynthRegistry(t, dir, mapProductSized(232.49, 450))
	l := NewLazyTerrain(reg, dir, time.Minute)
	if _, err := l.Build(0); err != nil {
		t.Fatal(err)
	}
	var ok, unavailable atomic.Int64
	var wg sync.WaitGroup
	start := make(chan struct{})
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			<-start
			for i := 0; i < 30; i++ {
				d, err := l.Build(uint32(g*1000 + i))
				switch {
				case err == nil && len(d.Elevation.SamplesMeters) == 2601:
					ok.Add(1)
				case errors.Is(err, ErrDataUnavailable):
					unavailable.Add(1)
				default:
					t.Errorf("Build: %d samples, %v", len(d.Elevation.SamplesMeters), err)
				}
			}
		}(g)
	}
	close(start)
	time.Sleep(5 * time.Millisecond)
	if err := l.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	wg.Wait()
	if _, err := l.Build(1); !errors.Is(err, ErrDataUnavailable) {
		t.Fatalf("Build after Close: %v", err)
	}
	if err := l.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	t.Logf("%d builds succeeded before Close, %d refused after", ok.Load(), unavailable.Load())
}
