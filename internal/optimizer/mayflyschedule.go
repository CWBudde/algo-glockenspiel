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

// roundStream and warmStream are a mayfly round's two streams. seed.go says
// why they are mixed rather than offset.
func roundStream(base int64, round int) int64 {
	return derivedSeed(base, streamPrimary, round)
}

func warmStream(base int64, round int) int64 {
	return derivedSeed(base, streamWarm, round)
}
