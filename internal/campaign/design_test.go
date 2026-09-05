package campaign_test

import (
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/cwbudde/algo-glockenspiel/internal/campaign"
	"github.com/cwbudde/algo-glockenspiel/internal/fitrun"
	"github.com/cwbudde/algo-glockenspiel/internal/optimizer"
)

// repoRoot walks up to the module root. The designs name their references
// repo-relative and Plan resolves them against the working directory, which
// under `go test` is the package directory, so every test that validates or
// plans a design runs from here.
func repoRoot(t *testing.T) string {
	t.Helper()

	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("working directory: %v", err)
	}

	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("no go.mod above %q", dir)
		}

		dir = parent
	}
}

func TestRegisteredDesignsAreClosedAndValid(t *testing.T) {
	t.Chdir(repoRoot(t))

	designs := campaign.Registered()

	want := []string{"smoke", "engine-shape", "seed-hunt", "rounds-12k", "rounds-24k", "rounds-48k"}
	if len(designs) != len(want) {
		t.Fatalf("registered %d designs, want %d", len(designs), len(want))
	}

	for index, name := range want {
		if designs[index].Name != name {
			t.Errorf("design %d is %q, want %q", index, designs[index].Name, name)
		}

		found, err := campaign.Lookup(name)
		if err != nil {
			t.Fatalf("lookup %q: %v", name, err)
		}

		if found.Name != name {
			t.Errorf("lookup %q returned design %q", name, found.Name)
		}

		if err := found.Validate(); err != nil {
			t.Errorf("design %q does not validate: %v", name, err)
		}
	}

	_, err := campaign.Lookup("no-such-design")
	if err == nil {
		t.Fatal("lookup of an unregistered design succeeded")
	}

	for _, name := range want {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("lookup error %q does not list the registered design %q", err, name)
		}
	}
}

func TestRegisteredDesignsUseDisjointSeeds(t *testing.T) {
	owner := make(map[int64]string)

	for _, design := range campaign.Registered() {
		for block := range design.Blocks {
			seed := design.SeedBase + int64(block)

			if other, taken := owner[seed]; taken {
				t.Fatalf("designs %q and %q both use seed %d", other, design.Name, seed)
			}

			owner[seed] = design.Name
		}
	}
}

func TestEngineShapeArmsAreEvaluationMatched(t *testing.T) {
	design, err := campaign.Lookup("engine-shape")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}

	if design.Budget <= 0 {
		t.Fatalf("budget is %d, want a positive evaluation cap for every arm", design.Budget)
	}

	// A tenth longer than the budget buys, so that the evaluation cap and not
	// a half-spent iteration is what ends every mayfly run.
	wantIterations := int(math.Ceil(1.1 * float64(design.Budget) / optimizer.MayflyEvaluationsPerIteration()))

	for _, arm := range design.Arms {
		switch arm.Engine.Name {
		case fitrun.EngineMayfly:
			if arm.MaxIterations != wantIterations {
				t.Errorf("arm %q has %d iterations, want %d for a budget of %d evaluations",
					arm.Name, arm.MaxIterations, wantIterations, design.Budget)
			}
		case fitrun.EngineCMAES:
			if arm.MaxIterations != 0 {
				t.Errorf("arm %q caps iterations at %d, but a cmaes arm is bound by evaluations alone",
					arm.Name, arm.MaxIterations)
			}

			perRun := arm.Engine.CMAES.RunEvaluations
			if perRun > 0 && design.Budget%perRun != 0 {
				t.Errorf("arm %q spends %d evaluations per run, which does not divide the budget of %d",
					arm.Name, perRun, design.Budget)
			}
		default:
			t.Errorf("arm %q runs unexpected engine %q", arm.Name, arm.Engine.Name)
		}
	}

	r16, err := design.ArmByName("mayfly-r16")
	if err != nil {
		t.Fatalf("arm: %v", err)
	}

	if rounds := r16.Engine.Mayfly.Restarts + 1; rounds != 16 {
		t.Errorf("mayfly-r16 runs %d rounds, want 16", rounds)
	}

	if r16.RestartsPlanned != 16 {
		t.Errorf("mayfly-r16 plans %d restarts, want 16", r16.RestartsPlanned)
	}
}

func TestSeedHuntRefusesAMayflyWinner(t *testing.T) {
	shape, err := campaign.Lookup("engine-shape")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}

	mayfly, err := shape.ArmByName("mayfly-r16")
	if err != nil {
		t.Fatalf("arm: %v", err)
	}

	if _, err := campaign.SeedHunt(mayfly); err == nil {
		t.Fatal("seed-hunt accepted a mayfly winner, but it varies the initial cmaes population")
	}
}

func TestSeedHuntArmsAreNamedForTheirPopulations(t *testing.T) {
	design, err := campaign.Lookup("seed-hunt")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}

	if !design.Descriptive {
		t.Error("seed-hunt is not marked descriptive, so analyze would test a contrast it never registered")
	}

	lambda := optimizer.HansenPopulationSize(30)

	want := []string{"blk-cmaes-r-l" + strconv.Itoa(lambda), "blk-cmaes-r-l" + strconv.Itoa(2*lambda)}
	if len(design.Arms) != len(want) {
		t.Fatalf("seed-hunt has %d arms, want %d", len(design.Arms), len(want))
	}

	for index, name := range want {
		if design.Arms[index].Name != name {
			t.Errorf("arm %d is %q, want %q", index, design.Arms[index].Name, name)
		}
	}

	if got := design.Arms[0].Engine.CMAES.Lambda; got != 0 {
		t.Errorf("the default arm pins lambda to %d, but it must resolve at run time", got)
	}

	if got := design.Arms[1].Engine.CMAES.Lambda; got != 2*lambda {
		t.Errorf("the doubled arm has lambda %d, want %d", got, 2*lambda)
	}
}

func TestDesignHashChangesWithTheDesign(t *testing.T) {
	design, err := campaign.Lookup("smoke")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}

	first, err := design.Hash()
	if err != nil {
		t.Fatalf("hash: %v", err)
	}

	again, err := design.Hash()
	if err != nil {
		t.Fatalf("hash: %v", err)
	}

	if first != again {
		t.Fatalf("hash is not stable: %s then %s", first, again)
	}

	design.Budget++

	changed, err := design.Hash()
	if err != nil {
		t.Fatalf("hash: %v", err)
	}

	if changed == first {
		t.Fatal("changing the budget left the design hash unchanged")
	}
}

func TestValidateRejectsAContrastOnAnUnknownArm(t *testing.T) {
	t.Chdir(repoRoot(t))

	design, err := campaign.Lookup("smoke")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}

	design.Contrasts = append(design.Contrasts, campaign.Contrast{Control: "sep-cmaes-r", Candidate: "ghost"})

	if err := design.Validate(); err == nil {
		t.Fatal("a contrast naming an arm the design does not have was accepted")
	}
}

func TestEngineShapeIsTheDesignThePhaseArgued(t *testing.T) {
	design, err := campaign.Lookup("engine-shape")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}

	if design.Budget != 24_000 || design.Blocks != 12 || design.SeedBase != 121_000 {
		t.Fatalf("engine-shape is %d evaluations over %d blocks from seed %d, want 24000, 12 and 121000",
			design.Budget, design.Blocks, design.SeedBase)
	}

	// The shape of every arm, spelled out, because these are the numbers the
	// comparison means and a silent edit to one of them would change what the
	// phase concluded without changing anything that fails.
	want := []struct {
		name            string
		engine          string
		covariance      string
		runEvaluations  int
		lambdaGrowth    float64
		restartLimit    int
		population      int
		rounds          int
		restartsPlanned int
	}{
		{name: "mayfly-single", engine: fitrun.EngineMayfly, population: 10, rounds: 1, restartsPlanned: 1},
		{name: "mayfly-r16", engine: fitrun.EngineMayfly, population: 10, rounds: 16, restartsPlanned: 16},
		{name: "sep-cmaes-r", engine: fitrun.EngineCMAES, covariance: "separable", runEvaluations: 4800},
		{name: "blk-cmaes-r", engine: fitrun.EngineCMAES, covariance: "block", runEvaluations: 4800},
		{name: "sep-cmaes-ipop", engine: fitrun.EngineCMAES, covariance: "separable", lambdaGrowth: 2},
	}

	if len(design.Arms) != len(want) {
		t.Fatalf("engine-shape has %d arms, want %d", len(design.Arms), len(want))
	}

	for index, arm := range design.Arms {
		expected := want[index]

		if arm.Name != expected.name || arm.Engine.Name != expected.engine {
			t.Fatalf("arm %d is %q on %q, want %q on %q",
				index, arm.Name, arm.Engine.Name, expected.name, expected.engine)
		}

		if arm.RestartsPlanned != expected.restartsPlanned {
			t.Errorf("arm %q plans %d restarts, want %d", arm.Name, arm.RestartsPlanned, expected.restartsPlanned)
		}

		if arm.Engine.CMAES.Covariance != expected.covariance {
			t.Errorf("arm %q uses covariance %q, want %q",
				arm.Name, arm.Engine.CMAES.Covariance, expected.covariance)
		}

		if arm.Engine.CMAES.RunEvaluations != expected.runEvaluations {
			t.Errorf("arm %q caps a run at %d evaluations, want %d",
				arm.Name, arm.Engine.CMAES.RunEvaluations, expected.runEvaluations)
		}

		if arm.Engine.CMAES.LambdaGrowth != expected.lambdaGrowth {
			t.Errorf("arm %q grows lambda by %g, want %g",
				arm.Name, arm.Engine.CMAES.LambdaGrowth, expected.lambdaGrowth)
		}

		if arm.Engine.CMAES.RestartLimit != expected.restartLimit {
			t.Errorf("arm %q limits restarts to %d, want %d",
				arm.Name, arm.Engine.CMAES.RestartLimit, expected.restartLimit)
		}

		if arm.Engine.Mayfly.Population != expected.population {
			t.Errorf("arm %q has population %d, want %d", arm.Name, arm.Engine.Mayfly.Population, expected.population)
		}

		if expected.rounds > 0 && arm.Engine.Mayfly.Restarts+1 != expected.rounds {
			t.Errorf("arm %q runs %d rounds, want %d", arm.Name, arm.Engine.Mayfly.Restarts+1, expected.rounds)
		}
	}

	wantContrasts := []campaign.Contrast{
		{Control: "mayfly-r16", Candidate: "blk-cmaes-r", Primary: true},
		{Control: "mayfly-r16", Candidate: "sep-cmaes-r"},
		{Control: "mayfly-single", Candidate: "mayfly-r16"},
	}

	if !reflect.DeepEqual(design.Contrasts, wantContrasts) {
		t.Fatalf("contrasts are %+v, want %+v", design.Contrasts, wantContrasts)
	}
}

// TestTheRoundScheduleLadderVariesOnlyItsBudget is what makes the ladder a
// measurement of the budget rather than of three unrelated designs. The rungs
// exist because engine-shape's answer is confounded by its cap -- mayfly-single
// was still improving at 98.8% of the budget when it beat mayfly-r16 -- so a
// rung that differed from its siblings in anything but the budget would
// reintroduce exactly the confound the ladder is built to remove.
//
// The iteration cap is the one thing that must move with the budget: it is
// derived from it, and a rung carrying another rung's cap would anneal on a
// schedule sized for a run it never has.
func TestTheRoundScheduleLadderVariesOnlyItsBudget(t *testing.T) {
	t.Chdir(repoRoot(t))

	rungs := []string{"rounds-12k", "rounds-24k", "rounds-48k"}
	budgets := make(map[int]string, len(rungs))

	var first campaign.Design

	for index, name := range rungs {
		design, err := campaign.Lookup(name)
		if err != nil {
			t.Fatalf("lookup %q: %v", name, err)
		}

		if other, taken := budgets[design.Budget]; taken {
			t.Errorf("rungs %q and %q share the budget %d, so the ladder has a rung that measures nothing",
				other, name, design.Budget)
		}

		budgets[design.Budget] = name

		if index == 0 {
			first = design

			continue
		}

		if design.Reference != first.Reference || design.Note != first.Note ||
			design.Profile != first.Profile || design.Blocks != first.Blocks {
			t.Errorf("rung %q differs from %q in something other than its budget", name, first.Name)
		}

		if len(design.Arms) != len(first.Arms) {
			t.Fatalf("rung %q has %d arms, want the %d every rung carries", name, len(design.Arms), len(first.Arms))
		}

		for i, arm := range design.Arms {
			if arm.Name != first.Arms[i].Name {
				t.Errorf("rung %q arm %d is %q, want %q", name, i, arm.Name, first.Arms[i].Name)
			}

			if arm.Engine != first.Arms[i].Engine {
				t.Errorf("rung %q arm %q is configured differently from the same arm of %q",
					name, arm.Name, first.Name)
			}

			// The cap has to be this rung's own, or the arm anneals on a
			// schedule sized for a different run.
			if arm.MaxIterations <= first.Arms[i].MaxIterations {
				t.Errorf("rung %q arm %q caps iterations at %d, which is not above %q's %d at a smaller budget",
					name, arm.Name, arm.MaxIterations, first.Name, first.Arms[i].MaxIterations)
			}
		}
	}
}
