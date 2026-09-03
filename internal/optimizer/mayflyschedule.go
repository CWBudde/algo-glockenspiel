package optimizer

// A Mayfly run can be split into several shorter searches instead of one long
// one. Two kinds exist here and the difference is the whole point.
//
// An epoch reseeds the next search from the best candidate found so far, so it
// inherits that candidate's basin and refines it. A restart does not chain: it
// draws a fresh population and explores independently, which is what lets a run
// escape a basin it should never have entered.
//
// The defaults -- one epoch, no restarts -- reproduce a single search exactly.
// That is deliberate rather than cautious: the sibling algo-piano project
// measured round length as the dominant setting and found that at typical
// budgets restarting costs more than it buys, while warm starting is the
// second-largest effect. So warm rounds are the knob worth reaching for, and
// cold ones are opt-in.

// mayflySchedule splits one iteration budget into consecutive searches.
type mayflySchedule struct {
	// epochs is the number of warm rounds, each reseeded from the running best.
	// Values below one mean one.
	epochs int
	// restarts is the number of cold rounds appended after the warm ones. Each
	// starts from a uniformly random population.
	restarts int
}

// scheduleFor reads the schedule a tuning document asked for. A nil document,
// or one with no schedule block, gives a single warm round.
func scheduleFor(tuning *MayflyTuning) mayflySchedule {
	schedule := mayflySchedule{epochs: 1}

	if tuning == nil || tuning.Schedule == nil {
		return schedule
	}

	if tuning.Schedule.Epochs != nil {
		schedule.epochs = *tuning.Schedule.Epochs
	}

	if tuning.Schedule.Restarts != nil {
		schedule.restarts = *tuning.Schedule.Restarts
	}

	return schedule
}

// rounds is how many searches the schedule runs in total.
func (s mayflySchedule) rounds() int {
	return maxInt(1, s.epochs) + maxInt(0, s.restarts)
}

// warm reports whether a zero-based round reseeds from the running best. Only
// the warm rounds after the first actually carry the incumbent forward; the
// very first round is seeded from the caller's starting point either way.
func (s mayflySchedule) warm(round int) bool {
	return round < maxInt(1, s.epochs)
}

// plan splits total iterations across the rounds, giving the remainder to the
// earliest ones so the budget is spent exactly.
//
// The split is by iterations and never by population. algo-piano's audit
// measured an iteration at far more evaluations than the naive count its own
// derivation assumed, and the resulting round lengths were wrong by more than
// a factor of two. The budget a caller sets is the budget the run gets.
func (s mayflySchedule) plan(total int) []int {
	rounds := s.rounds()
	total = maxInt(1, total)

	// More rounds than iterations would give some of them nothing to do, and a
	// zero-iteration round is a configuration mayfly rejects outright.
	if rounds > total {
		rounds = total
	}

	return splitEvenly(total, rounds)
}

// splitEvenly divides a budget into parts of as equal a size as integers
// allow, giving the remainder to the earliest parts so the total is spent
// exactly. Both the iteration budget and the evaluation cap are split this
// way, so a caller comparing the two sees the same round boundaries.
func splitEvenly(total, parts int) []int {
	budgets := make([]int, parts)
	base, remainder := total/parts, total%parts

	for i := range budgets {
		budgets[i] = base
		if i < remainder {
			budgets[i]++
		}
	}

	return budgets
}

// shortestRound is the smallest per-round budget the plan produces. It is what
// a convergence window has to fit inside, because a window wider than a round
// can never be reached before the round ends.
func (s mayflySchedule) shortestRound(total int) int {
	return shortestBudget(s.plan(total))
}

// shortestBudget is the smallest round in a plan. plan gives the remainder to
// the earliest rounds, so the last one is always the smallest.
func shortestBudget(budgets []int) int {
	return budgets[len(budgets)-1]
}

// roundStream and warmStream derive a round's random streams from the run's
// seed.
//
// The obvious derivations -- seed-round for the round and seed+round+1 for the
// warm population -- keep a single run's streams apart from each other, which
// is all they were written to do. They do not keep two runs apart. A campaign
// block's seed is SeedBase+block, so consecutive blocks differ by one, and an
// arithmetic offset makes block b's round r the same stream as block b+1's
// round r+1: a sixteen-round arm's twelve blocks then share fourteen of their
// fifteen restarts and are not the independent samples a paired design counts
// them as. Phase 8.6 found two blocks of mayfly-r16 writing a bit-identical
// preset that way.
//
// Mixing instead of offsetting removes the coupling: adjacent seeds produce
// unrelated streams, and the two families cannot collide with each other
// because the label's low bit separates them. Round zero keeps the resolved
// seed unchanged, so the seed a run reports and checkpoints still reproduces
// it from the beginning.
func roundStream(base int64, round int) int64 {
	if round == 0 {
		return base
	}

	return mixSeed(base, uint64(round)<<1)
}

func warmStream(base int64, round int) int64 {
	return mixSeed(base, uint64(round)<<1|1)
}

// mixSeed is splitmix64 over the base seed and a label. The result is forced
// positive and non-zero because a zero seed means "choose one" everywhere else
// in this package and a derived stream must never mean that.
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
