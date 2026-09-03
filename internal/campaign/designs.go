package campaign

import (
	"fmt"
	"math"
	"strings"

	"github.com/cwbudde/algo-glockenspiel/internal/fitrun"
	"github.com/cwbudde/algo-glockenspiel/internal/optimizer"
)

// The covariance representations and the mayfly variant the designs use. They
// are string literals in the optimizer's API, and repeating them here rather
// than spelling them at every use keeps a typo from selecting a default.
const (
	covarianceSeparable = "separable"
	covarianceBlock     = "block"
	variantDESMA        = "desma"
)

// The evaluation budgets.
//
// smokeBudget is a few seconds of search on the short synthetic reference: it
// exists so the end-to-end path is exercised by a test, not so the numbers
// mean anything. It is nevertheless large enough for both arms to move. At the
// 300 it started at, neither did: three CMA-ES runs of a hundred evaluations
// and under five mayfly generations of sixty-three never left the seeded
// vector, so every job scored the same number and the statistics only ever
// exercised the zero-variance branch. Twelve hundred buys each arm enough
// generations to improve on the seed, which is what makes the smoke run a
// rehearsal of the analysis rather than only of the plumbing.
//
// fullBudget is the real one. The 8.4 smoke run spent about 24,000 evaluations
// in 60 s on twelve threads, so a job is a minute and the sixty jobs of
// engine-shape are about an hour. Every arm of a design gets the same cap,
// which is the only thing that makes two backends comparable.
const (
	smokeBudget = 1200
	fullBudget  = 24_000
)

// The seed bases. Each design owns a disjoint range, because a block's seed is
// SeedBase+block and two designs sharing a seed would silently share a search
// trajectory that the analysis would then read as agreement.
const (
	smokeSeedBase       = 120_000
	engineShapeSeedBase = 121_000
	seedHuntSeedBase    = 122_000
)

// The per-run evaluation cap of the restarting CMA-ES arms: five cold restarts
// inside the budget. It divides fullBudget exactly, so an arm that restarts
// until the budget is spent spends all of it rather than stopping a fraction
// of a run short.
const cmaesRunEvaluations = fullBudget / 5

// mayflyPopulation is the swarm size every mayfly arm runs, which is the
// wrapper's own default and what the project has been fitting with.
// mayflyR16Rounds is the round schedule under test: sixteen rounds is fifteen
// restarts on top of the first.
const (
	mayflyPopulation = 10
	mayflyR16Rounds  = 16
)

// smokeRunEvaluations gives the smoke campaign's CMA-ES arm three cold
// restarts inside its budget, so the restart path is exercised by the
// end-to-end test rather than only by the hour-long designs.
const smokeRunEvaluations = smokeBudget / 3

// The references the designs fit against.
const (
	referenceSynthA4 = "testdata/reference/legacy_synth_a4.wav"
	referenceC5      = "testdata/reference/glockenspiel_c5.wav"
)

// defaultSeedHuntWinner is the arm seed-hunt refines when nobody names one. It
// is the design's own prediction, not a result; 8.6 substitutes whatever
// engine-shape actually returned.
const defaultSeedHuntWinner = "blk-cmaes-r"

// seedHuntDimensions is the encoded dimension seed-hunt sizes its populations
// for. Eight modes is what the analysis seeds from the C5 recording and what
// the 8.4 smoke run recorded, and eight modes encode to thirty parameters. A
// different mode count would move the Hansen default, so the arm names would
// stop describing what ran.
const seedHuntDimensions = 30

// mayflyIterations converts an evaluation budget into the iteration cap mayfly
// needs.
//
// Mayfly has no evaluation budget of its own, so a run capped only on
// evaluations is a run whose annealing schedules were sized for a nominal
// length they never reach: the swarm keeps cooling as if it had thousands of
// iterations left and the cap cuts it off mid-schedule. Task 1 measured that
// as a real loss. Sizing the iteration cap from the measured cost of an
// iteration gives the schedules a realistic length while the evaluation cap
// stays the budget that actually binds.
// The cap is ten percent long on purpose. Sized exactly, the last iteration is
// half spent when the evaluation cap cuts it, and a mayfly job then reports the
// cap as the reason it stopped only by accident of rounding. Ten percent of
// slack makes the evaluation cap the thing that always ends the run, at the
// cost of an annealing schedule that is a tenth longer than the run turns out
// to be. That is the lesser error: a schedule sized for a slightly longer run
// cools slightly too slowly, while a run that ends on its iteration cap has
// spent less than its budget and is not comparable with the arm beside it.
func mayflyIterations(budget int) int {
	return int(math.Ceil(1.1 * float64(budget) / optimizer.MayflyEvaluationsPerIteration()))
}

// smokeDesign is the end-to-end test's campaign and `just campaign-smoke`. Two
// blocks of two arms at 1,200 evaluations on the short synthetic reference runs
// in seconds.
func smokeDesign() Design {
	return Design{
		Name:        "smoke",
		Description: "Four short jobs on the synthetic A4 reference, to exercise plan, run and collect.",
		Reference:   referenceSynthA4,
		Note:        69,
		Profile:     optimizer.MetricBalanced,
		Budget:      smokeBudget,
		Blocks:      2,
		SeedBase:    smokeSeedBase,
		Arms: []Arm{
			{
				Name: "sep-cmaes-r",
				Engine: fitrun.Engine{
					Name: fitrun.EngineCMAES,
					CMAES: fitrun.CMAESSettings{
						Covariance:     covarianceSeparable,
						RunEvaluations: smokeRunEvaluations,
					},
				},
			},
			{
				Name: "mayfly-single",
				Engine: fitrun.Engine{
					Name:   fitrun.EngineMayfly,
					Mayfly: fitrun.MayflySettings{Variant: variantDESMA, Population: mayflyPopulation},
				},
				MaxIterations:   mayflyIterations(smokeBudget),
				RestartsPlanned: 1,
			},
		},
		Contrasts: []Contrast{
			{Control: "mayfly-single", Candidate: "sep-cmaes-r", Primary: true},
		},
	}
}

// engineShapeDesign is the phase's actual question: does a CMA-ES arm beat the
// mayfly arm the project ships, and does the shape of the restart ladder
// matter more than the backend.
//
// Twelve blocks of five arms is sixty jobs, about an hour at the budget. The
// mayfly arms differ only in their round schedule and the CMA-ES arms only in
// covariance and restart ladder, so each contrast isolates one decision.
func engineShapeDesign() Design {
	return Design{
		Name:        "engine-shape",
		Description: "Backend and restart shape on the C5 recording, twelve blocks of five arms at 24,000 evaluations.",
		Reference:   referenceC5,
		Note:        72,
		Profile:     optimizer.MetricBalanced,
		Budget:      fullBudget,
		Blocks:      12,
		SeedBase:    engineShapeSeedBase,
		Arms: []Arm{
			{
				Name: "mayfly-single",
				Engine: fitrun.Engine{
					Name:   fitrun.EngineMayfly,
					Mayfly: fitrun.MayflySettings{Variant: variantDESMA, Population: mayflyPopulation},
				},
				MaxIterations:   mayflyIterations(fullBudget),
				RestartsPlanned: 1,
			},
			{
				Name: "mayfly-r16",
				Engine: fitrun.Engine{
					Name: fitrun.EngineMayfly,
					Mayfly: fitrun.MayflySettings{
						Variant:    variantDESMA,
						Population: mayflyPopulation,
						Epochs:     1,
						Restarts:   mayflyR16Rounds - 1,
					},
				},
				MaxIterations:   mayflyIterations(fullBudget),
				RestartsPlanned: mayflyR16Rounds,
			},
			{
				Name: "sep-cmaes-r",
				Engine: fitrun.Engine{
					Name: fitrun.EngineCMAES,
					CMAES: fitrun.CMAESSettings{
						Covariance:     covarianceSeparable,
						RunEvaluations: cmaesRunEvaluations,
						RestartLimit:   0,
					},
				},
			},
			{
				Name: "blk-cmaes-r",
				Engine: fitrun.Engine{
					Name: fitrun.EngineCMAES,
					CMAES: fitrun.CMAESSettings{
						Covariance:     covarianceBlock,
						RunEvaluations: cmaesRunEvaluations,
						RestartLimit:   0,
					},
				},
			},
			{
				// IPOP: no per-run cap, so each run ends on Hansen's own
				// criteria and the next one doubles the population. The
				// evaluation budget is what ends the ladder.
				Name: "sep-cmaes-ipop",
				Engine: fitrun.Engine{
					Name: fitrun.EngineCMAES,
					CMAES: fitrun.CMAESSettings{
						Covariance:   covarianceSeparable,
						RestartLimit: 0,
						LambdaGrowth: 2,
					},
				},
			},
		},
		Contrasts: []Contrast{
			{Control: "mayfly-r16", Candidate: "blk-cmaes-r", Primary: true},
			{Control: "mayfly-r16", Candidate: "sep-cmaes-r"},
			{Control: "mayfly-single", Candidate: "mayfly-r16"},
		},
	}
}

// SeedHunt builds the seed-hunt design around a winning CMA-ES arm of
// engine-shape.
//
// The question is descriptive: at a fixed budget, does a larger initial
// population buy anything, or does it only buy fewer generations? So the two
// arms are the winner at Hansen's default population and the winner at twice
// it, and the analysis reports the distributions rather than a test. Forty
// eight blocks is what a descriptive answer at this spread needs.
//
// The population sizes come from optimizer.HansenPopulationSize at thirty
// dimensions, which assumes the analysis seeds eight modes for the C5
// recording; that is what the 8.4 smoke run recorded. The first arm leaves
// Lambda zero so the backend resolves the same default at run time, and only
// the second states a number.
func SeedHunt(winner Arm) (Design, error) {
	if winner.Engine.Name != fitrun.EngineCMAES {
		return Design{}, fmt.Errorf("seed-hunt refines a cmaes arm, but arm %q runs engine %q",
			winner.Name, winner.Engine.Name)
	}

	lambda := optimizer.HansenPopulationSize(seedHuntDimensions)

	base := engineShapeDesign()

	small := winner
	small.Name = fmt.Sprintf("%s-l%d", winner.Name, lambda)
	small.Engine.CMAES.Lambda = 0

	large := winner
	large.Name = fmt.Sprintf("%s-l%d", winner.Name, 2*lambda)
	large.Engine.CMAES.Lambda = 2 * lambda

	return Design{
		Name: "seed-hunt",
		Description: fmt.Sprintf(
			"Initial population of %s at the Hansen default (%d) against twice it, forty eight blocks, descriptive.",
			winner.Name, lambda),
		Reference:   base.Reference,
		Note:        base.Note,
		Profile:     base.Profile,
		Budget:      base.Budget,
		Blocks:      48,
		SeedBase:    seedHuntSeedBase,
		Arms:        []Arm{small, large},
		Descriptive: true,
	}, nil
}

// Registered returns every design, in registration order.
func Registered() []Design {
	return []Design{smokeDesign(), engineShapeDesign(), defaultSeedHunt()}
}

// defaultSeedHunt is seed-hunt around the default winner. The winner is an arm
// of engine-shape and a CMA-ES one, so SeedHunt cannot fail here; a failure
// would mean the registry contradicts itself and there is nothing sensible to
// return.
func defaultSeedHunt() Design {
	winner, err := engineShapeDesign().ArmByName(defaultSeedHuntWinner)
	if err != nil {
		panic(fmt.Sprintf("campaign: default seed-hunt winner: %v", err))
	}

	design, err := SeedHunt(winner)
	if err != nil {
		panic(fmt.Sprintf("campaign: default seed-hunt design: %v", err))
	}

	return design
}

// Lookup returns a registered design by name.
func Lookup(name string) (Design, error) {
	designs := Registered()

	for _, design := range designs {
		if design.Name == name {
			return design, nil
		}
	}

	names := make([]string, 0, len(designs))
	for _, design := range designs {
		names = append(names, design.Name)
	}

	return Design{}, fmt.Errorf("unknown design %q, registered designs are %s", name, strings.Join(names, ", "))
}
