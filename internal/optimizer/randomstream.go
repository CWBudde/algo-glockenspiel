package optimizer

// Deriving a run's several random streams from the one seed it reports.
//
// A restarting search needs more than one stream: the library's generator for
// each round or run, a warm round's initial population, a cold run's starting
// mean. Every one of them has to come from the seed the run reports, because
// that seed is what a checkpoint records and what reproduces the run.
//
// The obvious derivation is arithmetic -- seed+k for one family, seed-k-1 for
// another -- and it is wrong in a way only a designed comparison shows. It
// separates one run's families from each other, which is all it was written to
// do, but it does not separate two runs. A campaign block's seed is
// SeedBase+block, so consecutive blocks differ by one, and an offset makes
// block b's round k the same stream as block b+1's round k-1. A sixteen-round
// arm's twelve blocks then share fourteen of their fifteen restarts, and a
// paired design counts them as independent samples when they are not: the
// spread within an arm collapses and a paired t against that spread claims a
// precision the data does not hold. Phase 8.6 found it by noticing two blocks
// of one arm writing a bit-identical preset.
//
// Mixing removes the coupling. Adjacent seeds produce unrelated streams, the
// families cannot reach each other because the index is multiplied past them,
// and every stream stays a pure function of the reported seed.
const (
	// streamPrimary is the library's own generator for a round or a run.
	streamPrimary uint64 = 0
	// streamWarm is a warm round's initial population draw.
	streamWarm uint64 = 1
	// streamColdMean is a cold run's uniform starting mean.
	streamColdMean uint64 = 2
	// streamFamilies is how many of them there are, and the stride that keeps
	// one family's index from reaching another's.
	streamFamilies uint64 = 3
)

// derivedSeed returns one family's stream at one index.
//
// Index zero of the primary family is the resolved seed unchanged, so the seed
// a run reports and checkpoints still reproduces it from its beginning. Every
// other stream is mixed.
func derivedSeed(base int64, family uint64, index int) int64 {
	if family == streamPrimary && index == 0 {
		return base
	}

	return mixSeed(base, uint64(index)*streamFamilies+family)
}

// mixSeed is splitmix64 over the base seed and a label. The result is forced
// positive and non-zero because a zero seed means "choose one" everywhere else
// in this package, so no derived stream may produce it, and because a negative
// seed is not a value every backend accepts.
func mixSeed(base int64, label uint64) int64 {
	x := uint64(base)*0x9E3779B97F4A7C15 + label*0xBF58476D1CE4E5B9
	x ^= x >> 30
	x *= 0xBF58476D1CE4E5B9
	x ^= x >> 27
	x *= 0x94D049BB133111EB
	x ^= x >> 31

	seed := int64(x >> 1)
	if seed == 0 {
		return 1
	}

	return seed
}
