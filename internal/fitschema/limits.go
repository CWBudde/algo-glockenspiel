package fitschema

import "time"

// These are the ceilings and floors Fields draws on, named rather than
// inlined so the doc comment explaining each one lives in exactly one place.
// They used to live twice: once as a constant block in
// internal/server/fit.go and once, some of them missing entirely, as
// literals scattered through internal/browserfit's validateRequest -- which
// is how that copy ended up accepting a CMA-ES population, a restart count
// and a target cost the server would have refused.
const (
	// DefaultMaxReferenceBytes bounds an uploaded reference recording.
	//
	// 16 MiB is about three minutes of 16-bit mono at 44.1 kHz, which is an
	// order of magnitude more than a fit ever wants: the objective renders
	// and scores the whole reference once per candidate evaluation, so a
	// three-minute reference makes a hundred-iteration run take hours. The
	// limit is generous for the intended use and still small enough that the
	// decoded form -- go-audio hands back []int, eight bytes per sample,
	// before it is narrowed to float32 -- stays bounded at roughly 64 MB for
	// a maximal upload.
	DefaultMaxReferenceBytes = 16 << 20

	// MaxFitIterations bounds how many iterations one request can book, and
	// doubles as the ceiling on reportEvery and mayflyStagnation, both
	// counted in the same unit. There is a single fit slot, so an unbounded
	// budget is not merely a long wait for one client: it parks the slot
	// against everyone.
	MaxFitIterations = 100_000

	// MaxFitTimeBudget bounds the wall-clock budget a request can book, for
	// the reason MaxFitIterations does.
	MaxFitTimeBudget = time.Hour

	// DefaultTimeBudget is the budget a request gets when it names none.
	DefaultTimeBudget = 30 * time.Second

	// MaxRenderSeconds bounds the audition render. Rendering is linear in
	// duration and the result is held whole in memory before it is sent.
	MaxRenderSeconds = 60.0

	// MinReferenceSampleRate and MaxReferenceSampleRate bound the rate an
	// uploaded WAV may declare. The rate is attacker-controlled -- it is a
	// uint32 in the header that nothing else checks -- and it multiplies
	// every later allocation, so it is bounded at the door rather than at
	// each of the places it is later used. The range covers every rate audio
	// equipment actually produces, from telephony upwards.
	MinReferenceSampleRate = 4000
	MaxReferenceSampleRate = 192000

	// MaxReferenceWindow bounds the fixed cut a request may ask for. The
	// loader clamps the window to the file, so the bound only keeps the
	// value finite and sane; an hour is far past what the upload limit can
	// hold.
	MaxReferenceWindow = time.Hour

	// MaxMayflyPopulation caps the population and everything derived from
	// it: mating pairs the k-th best male with the k-th best female, so the
	// population also caps the usable offspring count, which is why the
	// offspring knobs (mayflyNc, mayflyNcRatio) are held to the same number
	// rather than to one of their own that could drift away from it.
	MaxMayflyPopulation = 4096

	// MayflyEpochsMin is the floor mayflyEpochs is held to: a run always has
	// at least one warm round.
	MayflyEpochsMin = 1

	// MayflyRoundsMax bounds mayflyEpochs and mayflyRestarts, the wrapper's
	// own run schedule: every round costs at least one iteration, so an
	// unbounded count is a request to split the budget into slices too thin
	// to search.
	MayflyRoundsMax = 1000

	// MayflyNCMin is the floor mayflyNc is held to. mayfly.NCAuto is -1, so
	// -1 is the floor rather than zero.
	MayflyNCMin = -1

	// MaxFitTargetCost bounds mayflyTargetCost. A cost is whatever the
	// metric produces, so there is no meaningful ceiling; the bound exists
	// only to keep the value finite and inside a range the engine can
	// validate.
	MaxFitTargetCost = 1e12

	// MaxCMAESLambda caps the CMA-ES population, mirroring
	// MaxMayflyPopulation: one generation evaluates the whole population, so
	// a population above it is a request to spend an entire fit on a single
	// generation.
	MaxCMAESLambda = 4096

	// MaxCMAESRestarts caps the restart count, mirroring the bound the
	// mayfly schedule is held to.
	MaxCMAESRestarts = 1000

	// DefaultMayflyReportEvery is the progress cadence a mayfly run gets when
	// the client names no cadence of its own: every generation. A mayfly
	// iteration is a whole generation while a simple iteration is about one
	// evaluation, so the default cadence for the simple backend -- ten --
	// would mean "report after roughly five hundred renders" for mayfly,
	// long enough that a default time budget ends before the first report
	// and the cost curve stays empty for the whole run.
	DefaultMayflyReportEvery = 1
)
