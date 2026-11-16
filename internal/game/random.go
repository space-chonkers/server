package game

type randomSource struct {
	state uint64
}

func newRandomSource(seed uint64) *randomSource {
	return &randomSource{state: seed}
}

// next uses SplitMix64 so seeded worlds are stable across Go runtime versions.
func (r *randomSource) next() uint64 {
	r.state += 0x9e3779b97f4a7c15
	value := r.state
	value = (value ^ (value >> 30)) * 0xbf58476d1ce4e5b9
	value = (value ^ (value >> 27)) * 0x94d049bb133111eb
	return value ^ (value >> 31)
}

func (r *randomSource) intn(n int) int {
	bound := uint64(n)
	threshold := -bound % bound
	for {
		value := r.next()
		if value >= threshold {
			return int(value % bound)
		}
	}
}

// float64 returns a uniform value in [0, 1) from the top 53 bits of next.
func (r *randomSource) float64() float64 {
	return float64(r.next()>>11) / (1 << 53)
}

func (r *randomSource) shuffleCells(cells []Cell) {
	for i := len(cells) - 1; i > 0; i-- {
		j := r.intn(i + 1)
		cells[i], cells[j] = cells[j], cells[i]
	}
}
