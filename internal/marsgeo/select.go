package marsgeo

// splitMix64 is the same generator as internal/game/random.go (copied, not
// imported, so marsgeo stays independent of game code and game stays
// untouched). Fixed algorithm and constants: a seed always picks the same
// location for the same registry.
type splitMix64 struct{ state uint64 }

func (r *splitMix64) next() uint64 {
	r.state += 0x9e3779b97f4a7c15
	v := r.state
	v = (v ^ (v >> 30)) * 0xbf58476d1ce4e5b9
	v = (v ^ (v >> 27)) * 0x94d049bb133111eb
	return v ^ (v >> 31)
}

// intn returns a uniform value in [0, n) by rejection sampling.
func (r *splitMix64) intn(n int) int {
	bound := uint64(n)
	threshold := -bound % bound
	for {
		if v := r.next(); v >= threshold {
			return int(v % bound)
		}
	}
}

// selectLocation maps a seed to (entry index, centre index): the entry is
// drawn first, uniformly, then the centre within that entry's lattice. This
// is uniform per product, not per centre; with several products, small ones
// would be over-represented (docs/MARSGEO.md §6, future consideration).
func selectLocation(seed uint32, centresPerEntry []int) (entry, centre int) {
	rng := splitMix64{state: uint64(seed)}
	entry = rng.intn(len(centresPerEntry))
	centre = rng.intn(centresPerEntry[entry])
	return entry, centre
}
