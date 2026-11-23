package mvplan

// rng is a splitmix64 generator (same construction family as
// songdna's generator): deterministic, allocation-free, and stable
// across platforms. It exists locally so the plan's generative layer
// never depends on a global source of randomness.
type rng struct{ state uint64 }

func newRNG(seed uint64) *rng { return &rng{state: seed} }

func (r *rng) next() uint64 {
	r.state += 0x9E3779B97F4A7C15
	z := r.state
	z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9
	z = (z ^ (z >> 27)) * 0x94D049BB133111EB
	return z ^ (z >> 31)
}

// Float01 returns a uniform float in [0,1).
func (r *rng) Float01() float64 { return float64(r.next()>>11) / float64(1<<53) }

// Intn returns a uniform int in [0,n). n <= 0 yields 0.
func (r *rng) Intn(n int) int {
	if n <= 0 {
		return 0
	}
	return int(r.next() % uint64(n))
}
