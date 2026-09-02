package optimizer

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"

	"github.com/cwbudde/mayfly"
)

// The tuning file lives here for the reason the bounds file does: four front
// ends -- the CLI, the fit API, the browser WASM build and the checkpoint
// writer -- read the same document, and a second parser would be a second
// place for the key names, the variant ownership rules and the numeric ranges
// to drift. internal/optimizer already owns the mayfly configuration this
// document describes, so it owns the JSON spelling of it too.
//
// The keys are mayfly.Config's own JSON tags, verbatim and flat rather than
// grouped per dialect, so a knob can be lifted straight out of what
// mayfly.SaveConfigToFile wrote and pasted into a tuning document.

// MayflyConvergence is the JSON form of mayfly.ConvergenceConfig.
//
// TargetCost stays a pointer inside a block that is itself optional because
// upstream models it that way: mayfly.ConvergenceConfig.TargetCost is a
// *float64 so that a disabled target is distinguishable from a target of
// exactly zero, and flattening it here would throw that distinction away.
type MayflyConvergence struct {
	TargetCost           *float64 `json:"target_cost,omitempty"`
	MinImprovement       *float64 `json:"min_improvement"`
	StagnationIterations *int     `json:"stagnation_iterations"`
	MinIterations        *int     `json:"min_iterations"`
}

// MayflySchedule is the wrapper's own run schedule: how many epochs a fit
// runs and how many times it restarts.
//
// It has no counterpart in mayfly.Config, which is exactly why it is nested.
// Keeping it one level down leaves the top level a faithful mirror of mayfly's
// own names, so a knob upstream adds later can never collide with one of ours.
type MayflySchedule struct {
	Epochs   *int `json:"epochs,omitempty"`
	Restarts *int `json:"restarts,omitempty"`
}

// MayflyTuning is the JSON form of a curated subset of mayfly.Config.
//
// Every field is a pointer, and that is the whole design: nil means the key was
// omitted, and an omitted key keeps whatever the variant factory or the preset
// already put there. An empty document is therefore a no-op, and a document
// that names one knob changes one knob -- it never silently resets the rest of
// the configuration to Go zero values.
//
//	{
//	  "variant": "desma",
//	  "npop":    40,
//	  "nc":      -1,
//	  "convergence": {"stagnation_iterations": 50},
//	  "schedule":    {"epochs": 3}
//	}
type MayflyTuning struct {
	// Variant and Preset make a tuning document self-contained: it can name the
	// dialect and the preset it was written for instead of relying on the
	// caller to pass a matching --variant. They are not tunable knobs, so they
	// are absent from MayflyTuningFields and Apply never writes them.
	Variant *string `json:"variant,omitempty"`
	Preset  *string `json:"preset,omitempty"`

	NPop      *int     `json:"npop"`
	NPopF     *int     `json:"npopf"`
	G         *float64 `json:"g"`
	GDamp     *float64 `json:"g_damp"`
	A1        *float64 `json:"a1"`
	A2        *float64 `json:"a2"`
	A3        *float64 `json:"a3"`
	Beta      *float64 `json:"beta"`
	Dance     *float64 `json:"dance"`
	DanceDamp *float64 `json:"dance_damp"`
	FL        *float64 `json:"fl"`
	FLDamp    *float64 `json:"fl_damp"`

	// NC carries four distinct states, which is why it is a pointer over an
	// int that already has a sentinel: nil is "omitted, keep what the factory
	// chose", mayfly.NCAuto (-1) derives the offspring count from nc_ratio, 0
	// means no crossover at all -- upstream's effectiveNC honours a written
	// zero literally -- and any positive value is an explicit count. Collapsing
	// nil into 0 would turn every silent omission into a crossover-free run.
	NC *int `json:"nc"`

	NCRatio        *float64 `json:"nc_ratio"`
	NM             *int     `json:"nm"`
	Mu             *float64 `json:"mu"`
	CrossoverGamma *float64 `json:"crossover_gamma"`
	Selection      *string  `json:"selection"`
	TournamentSize *int     `json:"tournament_size"`
	VelMax         *float64 `json:"vel_max"`
	VelMin         *float64 `json:"vel_min"`

	EliteCount      *int     `json:"elite_count"`
	SearchRange     *float64 `json:"search_range"`
	EnlargeFactor   *float64 `json:"enlarge_factor"`
	ReductionFactor *float64 `json:"reduction_factor"`

	OrthogonalFactor *float64 `json:"orthogonal_factor"`
	ChaosFactor      *float64 `json:"chaos_factor"`

	LevyAlpha            *float64 `json:"levy_alpha"`
	LevyBeta             *float64 `json:"levy_beta"`
	OppositionRate       *float64 `json:"opposition_rate"`
	EliteOppositionCount *int     `json:"elite_opposition_count"`

	InitialTemperature   *float64 `json:"initial_temperature"`
	CoolingRate          *float64 `json:"cooling_rate"`
	CoolingSchedule      *string  `json:"cooling_schedule"`
	CauchyMutationRate   *float64 `json:"cauchy_mutation_rate"`
	GoldenFactor         *float64 `json:"golden_factor"`
	ApplyOBLToGlobalBest *bool    `json:"apply_obl_to_global_best"`

	MedianWeight      *float64 `json:"median_weight"`
	GravityType       *string  `json:"gravity_type"`
	UseWeightedMedian *bool    `json:"use_weighted_median"`

	AquilaWeight          *float64 `json:"aquila_weight"`
	OppositionProbability *float64 `json:"opposition_probability"`
	ArchiveSize           *int     `json:"archive_size"`
	StrategySwitch        *int     `json:"strategy_switch"`

	Convergence *MayflyConvergence `json:"convergence,omitempty"`
	Schedule    *MayflySchedule    `json:"schedule,omitempty"`
}

// NamesDialect reports whether the document chooses the dialect itself, either
// directly or through a preset.
//
// The front ends need this because their variant setting carries a default the
// caller never asked for. Passing that default on would make the engine either
// ignore a document's own variant or refuse its preset as a dialect named
// twice, so a document that describes a whole run would silently run another
// algorithm. It is a method on the tuning rather than a copy in each front end
// for the reason the document lives in this package at all.
func (t *MayflyTuning) NamesDialect() bool {
	return t != nil && (t.Variant != nil || t.Preset != nil)
}

// MayflyTuningField describes one knob: what it is called in JSON, what a human
// should be told about it, and what values mayfly will accept.
//
// It exists so that the range rules have exactly one home. The decoder
// validates against this table, the CLI builds its help text from it, and the
// web form's TypeScript table is generated from it, so a range that moves
// upstream is corrected in one place instead of four.
type MayflyTuningField struct {
	Key          string   // the json tag
	Label        string   // human-readable, for CLI help and the web form
	Kind         string   // "float" | "int" | "enum" | "bool"
	Variant      string   // "" = shared; otherwise the dialect that owns the knob
	Min          *float64 // nil = unbounded
	Max          *float64
	MinExclusive bool
	MaxExclusive bool
	Options      []string // for Kind == "enum"
	Help         string
}

// tuningBound is a spelling of &value that reads as a bound at the call site.
func tuningBound(value float64) *float64 {
	return &value
}

// MayflyTuningFields returns the knob table, in the order it is documented:
// the shared mayfly parameters first, then one block per dialect, then the
// convergence and schedule blocks.
//
// It returns a fresh copy on every call because the table carries Options
// slices, and a caller that sorted or trimmed a shared slice would silently
// reshape everyone else's help text.
//
//nolint:funlen // one literal table entry per knob; splitting it hides the table.
func MayflyTuningFields() []MayflyTuningField {
	selection := []string{"tournament", "rank"}
	schedules := []string{"exponential", "linear", "logarithmic"}
	gravity := []string{"linear", "exponential", "sigmoid"}

	return []MayflyTuningField{
		{
			Key: "npop", Label: "Male population", Kind: "int",
			Min: tuningBound(2),
			Help: "Number of male mayflies. Mating pairs the k-th best male with the k-th " +
				"best female, so this also caps the usable offspring count.",
		},
		{
			Key: "npopf", Label: "Female population", Kind: "int",
			Min:  tuningBound(2),
			Help: "Number of female mayflies.",
		},
		{
			Key: "g", Label: "Inertia weight", Kind: "float",
			Min: tuningBound(0), Max: tuningBound(1),
			Help: "Inertia weight on the velocity update.",
		},
		{
			Key: "g_damp", Label: "Inertia damping", Kind: "float",
			Min: tuningBound(0), MinExclusive: true,
			Help: "Per-iteration multiplier applied to the inertia weight.",
		},
		{
			Key: "a1", Label: "Personal best pull", Kind: "float",
			Min:  tuningBound(0),
			Help: "Attraction coefficient towards a mayfly's own best position.",
		},
		{
			Key: "a2", Label: "Global best pull", Kind: "float",
			Min:  tuningBound(0),
			Help: "Attraction coefficient towards the global best position.",
		},
		{
			Key: "a3", Label: "Mating pull", Kind: "float",
			Min:  tuningBound(0),
			Help: "Attraction coefficient between mating partners.",
		},
		{
			Key: "beta", Label: "Visibility exponent", Kind: "float",
			Min: tuningBound(0), MinExclusive: true,
			Help: "Exponent of the distance-based visibility term.",
		},
		{
			Key: "dance", Label: "Nuptial dance", Kind: "float",
			Help: "Random-walk amplitude of the best males' nuptial dance.",
		},
		{
			Key: "dance_damp", Label: "Nuptial dance damping", Kind: "float",
			Help: "Per-iteration multiplier applied to the nuptial dance amplitude.",
		},
		{
			Key: "fl", Label: "Female random walk", Kind: "float",
			Help: "Random-walk amplitude of unmated females.",
		},
		{
			Key: "fl_damp", Label: "Female random walk damping", Kind: "float",
			Help: "Per-iteration multiplier applied to the female random walk.",
		},
		{
			Key: "nc", Label: "Offspring count", Kind: "int",
			Min: tuningBound(mayfly.NCAuto),
			Help: "Crossover offspring per iteration. -1 (mayfly.NCAuto) derives the count " +
				"from nc_ratio, 0 disables crossover entirely, and a positive value is taken " +
				"literally.",
		},
		{
			Key: "nc_ratio", Label: "Offspring ratio", Kind: "float",
			Min:  tuningBound(0),
			Help: "Offspring count as a fraction of the male population, used only when nc is -1.",
		},
		{
			Key: "nm", Label: "Mutant count", Kind: "int",
			Min:  tuningBound(0),
			Help: "Mutants drawn from the offspring each iteration. Zero derives 5% of npop.",
		},
		{
			Key: "mu", Label: "Mutation rate", Kind: "float",
			Min: tuningBound(0), Max: tuningBound(1),
			Help: "Per-dimension probability that a mutant is perturbed.",
		},
		{
			Key: "crossover_gamma", Label: "Crossover gamma", Kind: "float",
			Help: "Blend-crossover expansion factor. A non-positive value falls back to the default.",
		},
		{
			Key: "selection", Label: "Parent selection", Kind: "enum",
			Options: selection,
			Help: "How crossover pairs parents: \"rank\" mates the k-th best of each sex, " +
				"\"tournament\" draws tournament_size candidates and mates the fittest.",
		},
		{
			Key: "tournament_size", Label: "Tournament size", Kind: "int",
			Min:  tuningBound(0),
			Help: "Candidates drawn per tournament. Used only when selection is \"tournament\".",
		},
		{
			Key: "vel_max", Label: "Velocity ceiling", Kind: "float",
			Help: "Upper velocity clamp. Zero lets mayfly derive one from the search box.",
		},
		{
			Key: "vel_min", Label: "Velocity floor", Kind: "float",
			Help: "Lower velocity clamp. Zero lets mayfly derive one from the search box.",
		},

		{
			Key: "elite_count", Label: "Elite count", Kind: "int", Variant: "desma",
			Min:  tuningBound(0),
			Help: "Number of elites that receive the differential-evolution step.",
		},
		{
			Key: "search_range", Label: "Search range", Kind: "float", Variant: "desma",
			Min:  tuningBound(0),
			Help: "Initial elite search radius. Zero lets mayfly derive it from the search box.",
		},
		{
			Key: "enlarge_factor", Label: "Enlarge factor", Kind: "float", Variant: "desma",
			Min: tuningBound(0), MinExclusive: true,
			Help: "Multiplier applied to the search range after a successful elite step.",
		},
		{
			Key: "reduction_factor", Label: "Reduction factor", Kind: "float", Variant: "desma",
			Min: tuningBound(0), MinExclusive: true,
			Help: "Multiplier applied to the search range after a failed elite step.",
		},

		{
			Key: "orthogonal_factor", Label: "Orthogonal factor", Kind: "float", Variant: "olce",
			Min: tuningBound(0), Max: tuningBound(1),
			Help: "Weight of the orthogonal learning stage. Zero disables the stage and frees " +
				"its share of the evaluation budget.",
		},
		{
			Key: "chaos_factor", Label: "Chaos factor", Kind: "float", Variant: "olce",
			Min: tuningBound(0), Max: tuningBound(1),
			Help: "Initial chaotic search radius, decaying to zero. Zero disables the stage and " +
				"frees its share of the evaluation budget.",
		},

		{
			Key: "levy_alpha", Label: "Levy alpha", Kind: "float", Variant: "eobbma",
			Min: tuningBound(0), MinExclusive: true, Max: tuningBound(2),
			Help: "Stability index of the Levy flight. Smaller values give heavier tails.",
		},
		{
			Key: "levy_beta", Label: "Levy beta", Kind: "float", Variant: "eobbma",
			Min: tuningBound(0), MinExclusive: true,
			Help: "Scale of the Levy flight step.",
		},
		{
			Key: "opposition_rate", Label: "Opposition rate", Kind: "float", Variant: "eobbma",
			Min: tuningBound(0), Max: tuningBound(1),
			Help: "Fraction of elites that receive opposition-based learning.",
		},
		{
			Key: "elite_opposition_count", Label: "Elite opposition count", Kind: "int", Variant: "eobbma",
			Min:  tuningBound(0),
			Help: "Number of top solutions considered elite for opposition-based learning.",
		},

		{
			Key: "initial_temperature", Label: "Initial temperature", Kind: "float", Variant: "gsasma",
			Min: tuningBound(0), MinExclusive: true,
			Help: "Starting temperature of the simulated-annealing acceptance test.",
		},
		{
			Key: "cooling_rate", Label: "Cooling rate", Kind: "float", Variant: "gsasma",
			Min: tuningBound(0), MinExclusive: true, Max: tuningBound(1), MaxExclusive: true,
			Help: "Per-iteration temperature multiplier. Must stay strictly inside (0,1) so the " +
				"schedule actually cools without collapsing at once.",
		},
		{
			Key: "cooling_schedule", Label: "Cooling schedule", Kind: "enum", Variant: "gsasma",
			Options: schedules,
			Help:    "Shape of the temperature decay.",
		},
		{
			Key: "golden_factor", Label: "Golden factor", Kind: "float", Variant: "gsasma",
			Help: "Weight of the golden-sine step.",
		},

		// Both knobs read UseHMMA upstream. Mayfly attributed them to GSASMA
		// through v0.6.0 and moved them to HMMA in v0.7.0, so writing either
		// one under gsasma configures a stage that dialect never runs.
		{
			Key: "cauchy_mutation_rate", Label: "Cauchy mutation rate", Kind: "float", Variant: "hmma",
			Min: tuningBound(0), Max: tuningBound(1),
			Help: "Share of mutations drawn from a Cauchy rather than a Gaussian distribution.",
		},
		{
			Key: "apply_obl_to_global_best", Label: "OBL on global best", Kind: "bool", Variant: "hmma",
			Help: "Apply opposition-based learning to the global best every tenth iteration.",
		},

		{
			Key: "median_weight", Label: "Median weight", Kind: "float", Variant: "mpma",
			Min: tuningBound(0), Max: tuningBound(1),
			Help: "Balance between the population median and the global best as a guide.",
		},
		{
			Key: "gravity_type", Label: "Gravity schedule", Kind: "enum", Variant: "mpma",
			Options: gravity,
			Help:    "Shape of the non-linear gravity coefficient decay.",
		},
		{
			Key: "use_weighted_median", Label: "Weighted median", Kind: "bool", Variant: "mpma",
			Help: "Weight the median by fitness instead of taking the plain median.",
		},

		{
			Key: "aquila_weight", Label: "Aquila weight", Kind: "float", Variant: "aoblmoa",
			Min: tuningBound(0), Max: tuningBound(1),
			Help: "Probability that an individual takes an Aquila step instead of a mayfly step. " +
				"Deprecated: the published algorithm has no such knob, and omitting the key lets " +
				"mayfly decide the branch by its fitness test instead.",
		},
		{
			Key: "opposition_probability", Label: "Opposition probability", Kind: "float", Variant: "aoblmoa",
			Min: tuningBound(0), Max: tuningBound(1),
			Help: "Probability that a solution receives opposition-based learning. " +
				"Read only by the pre-paper branch draw: AOBLMOA itself opposes every offspring, ungated.",
		},
		{
			Key: "archive_size", Label: "Archive size", Kind: "int", Variant: "aoblmoa",
			Min:  tuningBound(0),
			Help: "Capacity of the caller-managed Pareto archive.",
		},
		{
			Key: "strategy_switch", Label: "Strategy switch", Kind: "int", Variant: "aoblmoa",
			Min: tuningBound(0),
			Help: "Iteration at which Aquila switches from exploration to exploitation. Zero " +
				"lets mayfly derive two thirds of the iteration budget.",
		},

		{
			Key: "target_cost", Label: "Target cost", Kind: "float",
			Help: "Stop once the best cost reaches this value. Omitting the key disables the " +
				"target, which is why zero is a usable target rather than \"off\".",
		},
		{
			Key: "min_improvement", Label: "Minimum improvement", Kind: "float",
			Min:  tuningBound(0),
			Help: "Cost reduction required to reset the stagnation counter.",
		},
		{
			Key: "stagnation_iterations", Label: "Stagnation iterations", Kind: "int",
			Min:  tuningBound(0),
			Help: "Stop after this many iterations without a sufficient improvement. Zero disables it.",
		},
		{
			Key: "min_iterations", Label: "Minimum iterations", Kind: "int",
			Min:  tuningBound(0),
			Help: "Iterations that must complete before either stopping rule may fire.",
		},

		{
			Key: "epochs", Label: "Epochs", Kind: "int",
			Min:  tuningBound(1),
			Help: "How many optimizer epochs the wrapper runs per fit.",
		},
		{
			Key: "restarts", Label: "Restarts", Kind: "int",
			Min:  tuningBound(0),
			Help: "How many times the wrapper restarts from a fresh population.",
		},
	}
}

// MayflyTuningKeys names the accepted knobs, in the order MayflyTuningFields
// documents them. It is derived rather than written out so the list the CLI
// help text and the API error messages advertise cannot fall out of step with
// the table.
var MayflyTuningKeys = func() []string {
	fields := MayflyTuningFields()
	keys := make([]string, 0, len(fields))

	for _, field := range fields {
		keys = append(keys, field.Key)
	}

	return keys
}()

// describe renders the field's accepted range the way an error message wants
// it. It reads the bounds off the table so no message restates a rule.
func (field MayflyTuningField) describe() string {
	if field.Kind == "enum" {
		return "one of " + quoteTuningOptions(field.Options)
	}

	if field.Key == "nc" {
		return fmt.Sprintf("at least %d (mayfly.NCAuto), which derives the count from nc_ratio", mayfly.NCAuto)
	}

	switch {
	case field.Min != nil && field.Max != nil:
		lower, upper := "[", "]"

		if field.MinExclusive {
			lower = "("
		}

		if field.MaxExclusive {
			upper = ")"
		}

		return fmt.Sprintf("in %s%g, %g%s", lower, *field.Min, *field.Max, upper)

	case field.Min != nil && field.MinExclusive:
		return fmt.Sprintf("greater than %g", *field.Min)

	case field.Min != nil:
		return fmt.Sprintf("at least %g", *field.Min)

	case field.Max != nil && field.MaxExclusive:
		return fmt.Sprintf("less than %g", *field.Max)

	case field.Max != nil:
		return fmt.Sprintf("at most %g", *field.Max)
	}

	return "finite"
}

// quoteTuningOptions renders an enum's choices for an error message.
func quoteTuningOptions(options []string) string {
	quoted := make([]string, 0, len(options))

	for _, option := range options {
		quoted = append(quoted, fmt.Sprintf("%q", option))
	}

	return joinWithComma(quoted)
}

// joinWithComma joins already-rendered parts, avoiding a strings import for a
// single call site.
func joinWithComma(parts []string) string {
	out := ""

	for index, part := range parts {
		if index > 0 {
			out += ", "
		}

		out += part
	}

	return out
}

// tuningKnob is one key the caller actually wrote, paired with the value it
// carries and the assignment it stands for. Decoding validates these and Apply
// runs them, so a knob is described in exactly one place.
type tuningKnob struct {
	key    string
	number *float64
	count  *int
	choice *string
	flag   *bool
	apply  func(cfg *mayfly.Config)
}

// knobs lists the keys this document actually sets, in table order. Fields left
// nil are skipped, which is what makes an omitted key a no-op.
//
//nolint:funlen // one registration per knob; the length is the table's, not logic's.
func (t *MayflyTuning) knobs() []tuningKnob {
	var knobs []tuningKnob

	addFloat := func(key string, value *float64, set func(cfg *mayfly.Config, value float64)) {
		if value == nil {
			return
		}

		// A knob with no setter is wrapper-owned: it is validated like any other
		// but never written to a mayfly.Config.
		if set == nil {
			knobs = append(knobs, tuningKnob{key: key, number: value})

			return
		}

		knobs = append(knobs, tuningKnob{
			key:    key,
			number: value,
			apply:  func(cfg *mayfly.Config) { set(cfg, *value) },
		})
	}

	addInt := func(key string, value *int, set func(cfg *mayfly.Config, value int)) {
		if value == nil {
			return
		}

		// A knob with no setter is wrapper-owned: it is validated like any other
		// but never written to a mayfly.Config.
		if set == nil {
			knobs = append(knobs, tuningKnob{key: key, count: value})

			return
		}

		knobs = append(knobs, tuningKnob{
			key:   key,
			count: value,
			apply: func(cfg *mayfly.Config) { set(cfg, *value) },
		})
	}

	addEnum := func(key string, value *string, set func(cfg *mayfly.Config, value string)) {
		if value == nil {
			return
		}

		// A knob with no setter is wrapper-owned: it is validated like any other
		// but never written to a mayfly.Config.
		if set == nil {
			knobs = append(knobs, tuningKnob{key: key, choice: value})

			return
		}

		knobs = append(knobs, tuningKnob{
			key:    key,
			choice: value,
			apply:  func(cfg *mayfly.Config) { set(cfg, *value) },
		})
	}

	addBool := func(key string, value *bool, set func(cfg *mayfly.Config, value bool)) {
		if value == nil {
			return
		}

		// A knob with no setter is wrapper-owned: it is validated like any other
		// but never written to a mayfly.Config.
		if set == nil {
			knobs = append(knobs, tuningKnob{key: key, flag: value})

			return
		}

		knobs = append(knobs, tuningKnob{
			key:   key,
			flag:  value,
			apply: func(cfg *mayfly.Config) { set(cfg, *value) },
		})
	}

	addInt("npop", t.NPop, func(cfg *mayfly.Config, value int) { cfg.NPop = value })
	addInt("npopf", t.NPopF, func(cfg *mayfly.Config, value int) { cfg.NPopF = value })
	addFloat("g", t.G, func(cfg *mayfly.Config, value float64) { cfg.G = value })
	addFloat("g_damp", t.GDamp, func(cfg *mayfly.Config, value float64) { cfg.GDamp = value })
	addFloat("a1", t.A1, func(cfg *mayfly.Config, value float64) { cfg.A1 = value })
	addFloat("a2", t.A2, func(cfg *mayfly.Config, value float64) { cfg.A2 = value })
	addFloat("a3", t.A3, func(cfg *mayfly.Config, value float64) { cfg.A3 = value })
	addFloat("beta", t.Beta, func(cfg *mayfly.Config, value float64) { cfg.Beta = value })
	addFloat("dance", t.Dance, func(cfg *mayfly.Config, value float64) { cfg.Dance = value })
	addFloat("dance_damp", t.DanceDamp, func(cfg *mayfly.Config, value float64) { cfg.DanceDamp = value })
	addFloat("fl", t.FL, func(cfg *mayfly.Config, value float64) { cfg.FL = value })
	addFloat("fl_damp", t.FLDamp, func(cfg *mayfly.Config, value float64) { cfg.FLDamp = value })
	addInt("nc", t.NC, func(cfg *mayfly.Config, value int) { cfg.NC = value })
	addFloat("nc_ratio", t.NCRatio, func(cfg *mayfly.Config, value float64) { cfg.NCRatio = value })
	addInt("nm", t.NM, func(cfg *mayfly.Config, value int) { cfg.NM = value })
	addFloat("mu", t.Mu, func(cfg *mayfly.Config, value float64) { cfg.Mu = value })
	addFloat("crossover_gamma", t.CrossoverGamma, func(cfg *mayfly.Config, value float64) {
		cfg.CrossoverGamma = value
	})
	addEnum("selection", t.Selection, func(cfg *mayfly.Config, value string) {
		cfg.Selection = mayfly.SelectionStrategy(value)
	})
	addInt("tournament_size", t.TournamentSize, func(cfg *mayfly.Config, value int) {
		cfg.TournamentSize = value
	})
	addFloat("vel_max", t.VelMax, func(cfg *mayfly.Config, value float64) { cfg.VelMax = value })
	addFloat("vel_min", t.VelMin, func(cfg *mayfly.Config, value float64) { cfg.VelMin = value })

	addInt("elite_count", t.EliteCount, func(cfg *mayfly.Config, value int) { cfg.EliteCount = value })
	addFloat("search_range", t.SearchRange, func(cfg *mayfly.Config, value float64) { cfg.SearchRange = value })
	addFloat("enlarge_factor", t.EnlargeFactor, func(cfg *mayfly.Config, value float64) {
		cfg.EnlargeFactor = value
	})
	addFloat("reduction_factor", t.ReductionFactor, func(cfg *mayfly.Config, value float64) {
		cfg.ReductionFactor = value
	})

	addFloat("orthogonal_factor", t.OrthogonalFactor, func(cfg *mayfly.Config, value float64) {
		cfg.OrthogonalFactor = value
	})
	addFloat("chaos_factor", t.ChaosFactor, func(cfg *mayfly.Config, value float64) { cfg.ChaosFactor = value })

	addFloat("levy_alpha", t.LevyAlpha, func(cfg *mayfly.Config, value float64) { cfg.LevyAlpha = value })
	addFloat("levy_beta", t.LevyBeta, func(cfg *mayfly.Config, value float64) { cfg.LevyBeta = value })
	addFloat("opposition_rate", t.OppositionRate, func(cfg *mayfly.Config, value float64) {
		cfg.OppositionRate = value
	})
	addInt("elite_opposition_count", t.EliteOppositionCount, func(cfg *mayfly.Config, value int) {
		cfg.EliteOppositionCount = value
	})

	addFloat("initial_temperature", t.InitialTemperature, func(cfg *mayfly.Config, value float64) {
		cfg.InitialTemperature = value
	})
	addFloat("cooling_rate", t.CoolingRate, func(cfg *mayfly.Config, value float64) { cfg.CoolingRate = value })
	addEnum("cooling_schedule", t.CoolingSchedule, func(cfg *mayfly.Config, value string) {
		cfg.CoolingSchedule = value
	})
	addFloat("cauchy_mutation_rate", t.CauchyMutationRate, func(cfg *mayfly.Config, value float64) {
		cfg.CauchyMutationRate = value
	})
	addFloat("golden_factor", t.GoldenFactor, func(cfg *mayfly.Config, value float64) { cfg.GoldenFactor = value })
	addBool("apply_obl_to_global_best", t.ApplyOBLToGlobalBest, func(cfg *mayfly.Config, value bool) {
		cfg.ApplyOBLToGlobalBest = value
	})

	addFloat("median_weight", t.MedianWeight, func(cfg *mayfly.Config, value float64) { cfg.MedianWeight = value })
	addEnum("gravity_type", t.GravityType, func(cfg *mayfly.Config, value string) { cfg.GravityType = value })
	addBool("use_weighted_median", t.UseWeightedMedian, func(cfg *mayfly.Config, value bool) {
		cfg.UseWeightedMedian = value
	})

	// The deprecated field is still written, and only when the key is present:
	// it is the one way back to the branch draw AOBLMOA used before mayfly
	// v0.6.0 reimplemented it after its paper, and a document that leaves the
	// key out leaves the sentinel the library defaults to, which is the paper's
	// fitness test. Dropping the knob would silently retune every tuning
	// document that names it.
	//nolint:staticcheck // SA1019: deprecated on purpose, see above.
	addFloat("aquila_weight", t.AquilaWeight, func(cfg *mayfly.Config, value float64) { cfg.AquilaWeight = value })
	addFloat("opposition_probability", t.OppositionProbability, func(cfg *mayfly.Config, value float64) {
		cfg.OppositionProbability = value
	})
	addInt("archive_size", t.ArchiveSize, func(cfg *mayfly.Config, value int) { cfg.ArchiveSize = value })
	addInt("strategy_switch", t.StrategySwitch, func(cfg *mayfly.Config, value int) { cfg.StrategySwitch = value })

	if t.Convergence != nil {
		// Apply guarantees cfg.Convergence exists before these run, because the
		// block being present is what creates it.
		addFloat("target_cost", t.Convergence.TargetCost, func(cfg *mayfly.Config, value float64) {
			cost := value
			cfg.Convergence.TargetCost = &cost
		})
		addFloat("min_improvement", t.Convergence.MinImprovement, func(cfg *mayfly.Config, value float64) {
			cfg.Convergence.MinImprovement = value
		})
		addInt("stagnation_iterations", t.Convergence.StagnationIterations, func(cfg *mayfly.Config, value int) {
			cfg.Convergence.StagnationIterations = value
		})
		addInt("min_iterations", t.Convergence.MinIterations, func(cfg *mayfly.Config, value int) {
			cfg.Convergence.MinIterations = value
		})
	}

	if t.Schedule != nil {
		// The schedule is wrapper-owned: it is validated here but never written
		// to a mayfly.Config, so these knobs carry no apply function.
		addInt("epochs", t.Schedule.Epochs, nil)
		addInt("restarts", t.Schedule.Restarts, nil)
	}

	return knobs
}

// LoadMayflyTuning reads a tuning file. It mirrors LoadParamBounds, down to
// naming the path in the error, so the two optional documents a fit accepts
// fail the same way.
func LoadMayflyTuning(path string) (*MayflyTuning, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read tuning %q: %w", path, err)
	}

	return DecodeMayflyTuning(data, path)
}

// DecodeMayflyTuning parses a tuning document. source names the origin for
// error messages, so bytes that never came from a file -- a multipart field, a
// textarea in the browser -- can still be reported usefully.
//
// Unknown keys are rejected: a misspelled knob that was silently ignored would
// run a fit at the factory default while the caller believed it had tuned
// something. So is anything following the object: a second document appended
// to the first would be dropped without a word.
//
// Every supplied value is then checked against MayflyTuningFields, because
// mayfly rejects an out-of-range configuration only once the run starts, and by
// then the caller has already uploaded a WAV and waited.
func DecodeMayflyTuning(data []byte, source string) (*MayflyTuning, error) {
	var tuning MayflyTuning

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(&tuning); err != nil {
		return nil, fmt.Errorf("decode tuning %q: %w", source, err)
	}

	if err := decoder.Decode(new(json.RawMessage)); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("decode tuning %q: unexpected content after the tuning object", source)
	}

	table := make(map[string]MayflyTuningField, len(MayflyTuningKeys))
	for _, field := range MayflyTuningFields() {
		table[field.Key] = field
	}

	for _, knob := range tuning.knobs() {
		field, ok := table[knob.key]
		if !ok {
			return nil, fmt.Errorf("tuning %q: %s is not a known key", source, knob.key)
		}

		if err := validateTuningKnob(source, field, knob); err != nil {
			return nil, err
		}
	}

	return &tuning, nil
}

// validateTuningKnob checks one written value against its table entry.
func validateTuningKnob(source string, field MayflyTuningField, knob tuningKnob) error {
	switch {
	case knob.number != nil:
		return validateTuningNumber(source, field, *knob.number)

	case knob.count != nil:
		return validateTuningNumber(source, field, float64(*knob.count))

	case knob.choice != nil:
		for _, option := range field.Options {
			if *knob.choice == option {
				return nil
			}
		}

		return fmt.Errorf("tuning %q: %s must be %s, got %q", source, field.Key, field.describe(), *knob.choice)
	}

	return nil
}

// validateTuningNumber rejects non-finite values outright -- mayfly sanitises
// them mid-run, so a NaN written here would not fail, it would quietly become
// something else -- and then applies the table's bounds.
func validateTuningNumber(source string, field MayflyTuningField, value float64) error {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return fmt.Errorf("tuning %q: %s must be finite", source, field.Key)
	}

	below := field.Min != nil && (value < *field.Min || (field.MinExclusive && value == *field.Min))
	above := field.Max != nil && (value > *field.Max || (field.MaxExclusive && value == *field.Max))

	if below || above {
		return fmt.Errorf("tuning %q: %s must be %s, got %g", source, field.Key, field.describe(), value)
	}

	return nil
}

// Apply writes every key the document set onto cfg, leaving the rest of the
// configuration -- the variant factory's or the preset's choices -- untouched.
// A nil receiver is a no-op, so a caller that has no tuning document need not
// branch.
//
// variant is the canonical short name of the dialect the run uses: one of ma,
// desma, olce, eobbma, gsasma, mpma or aoblmoa. A knob owned by a different
// dialect is an error rather than a silent write, because mayfly ignores the
// fields of the variants it is not running: the value would land on the config,
// change nothing, and leave the caller believing it had tuned the run.
//
// The schedule block is not applied. It is wrapper-owned, so the caller reads
// it straight off the struct (t.Schedule) after validation.
func (t *MayflyTuning) Apply(cfg *mayfly.Config, variant string) error {
	if t == nil {
		return nil
	}

	if cfg == nil {
		return errors.New("tuning: config is nil")
	}

	owners := make(map[string]string, len(MayflyTuningKeys))
	for _, field := range MayflyTuningFields() {
		owners[field.Key] = field.Variant
	}

	knobs := t.knobs()

	for _, knob := range knobs {
		if owner := owners[knob.key]; owner != "" && owner != variant {
			return fmt.Errorf("tuning key %q belongs to variant %s, but the run uses %s",
				knob.key, owner, variant)
		}
	}

	// The block being written is what creates the convergence config: leaving
	// it nil is how a document says "keep mayfly's default of no early exit".
	if t.Convergence != nil && cfg.Convergence == nil {
		cfg.Convergence = &mayfly.ConvergenceConfig{}
	}

	for _, knob := range knobs {
		if knob.apply == nil {
			continue
		}

		knob.apply(cfg)
	}

	return nil
}
