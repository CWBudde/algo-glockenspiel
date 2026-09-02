package optimizer

import (
	"context"
	"math"
	"strings"
	"testing"
)

// polishFixture is a reference the model can reproduce exactly: it is rendered
// from the template itself, so the encoded template is the target the polish
// stage has to walk back to.
type polishFixture struct {
	objective *ObjectiveFunction
	seed      []float64
	nudged    []float64
}

func newPolishFixture(t *testing.T) polishFixture {
	t.Helper()

	template := loadObjectivePreset(t)
	reference := renderReference(t, template, 44100, 69, 100, 0.3)

	objective, err := NewObjectiveFunction(reference, template, 44100, 69, 100, MetricBalanced)
	if err != nil {
		t.Fatalf("NewObjectiveFunction failed: %v", err)
	}

	seed, err := objective.Codec().EncodeParams(&template.Parameters)
	if err != nil {
		t.Fatalf("EncodeParams failed: %v", err)
	}

	return polishFixture{
		objective: objective,
		seed:      seed,
		nudged:    nudgeFrequencies(t, objective.Codec(), seed, 3),
	}
}

// nudgeFrequencies detunes every mode by the given number of cents, which is
// the kind of small error a global search leaves behind and the polish stage
// exists to remove.
func nudgeFrequencies(t *testing.T, codec *ParamCodec, encoded []float64, cents float64) []float64 {
	t.Helper()

	params, err := codec.DecodeParams(encoded)
	if err != nil {
		t.Fatalf("DecodeParams failed: %v", err)
	}

	factor := math.Pow(2, cents/1200)
	for i := range params.Modes {
		params.Modes[i].Frequency *= factor
	}

	nudged, err := codec.EncodeParams(params)
	if err != nil {
		t.Fatalf("EncodeParams failed: %v", err)
	}

	return nudged
}

func TestPolishObjectiveSharesTheCodecOfThePrimary(t *testing.T) {
	fixture := newPolishFixture(t)

	polish, err := fixture.objective.WithMetric(MetricPolish)
	if err != nil {
		t.Fatalf("WithMetric failed: %v", err)
	}

	if polish.Metric() != MetricPolish {
		t.Fatalf("expected the polish metric, got %q", polish.Metric())
	}

	if got, want := polish.Codec().Dimension(), fixture.objective.Codec().Dimension(); got != want {
		t.Fatalf("polish codec has dimension %d, primary has %d", got, want)
	}

	primaryBounds := fixture.objective.Codec().EncodedBounds()

	polishBounds := polish.Codec().EncodedBounds()
	if len(polishBounds.Ranges) != len(primaryBounds.Ranges) {
		t.Fatalf("polish bounds have %d ranges, primary has %d", len(polishBounds.Ranges), len(primaryBounds.Ranges))
	}

	for i := range primaryBounds.Ranges {
		if polishBounds.Ranges[i] != primaryBounds.Ranges[i] {
			t.Fatalf("encoded bounds differ at dimension %d: polish %+v primary %+v",
				i, polishBounds.Ranges[i], primaryBounds.Ranges[i])
		}
	}
}

func TestPolishAcceptsAnImprovementOnADetunedIncumbent(t *testing.T) {
	for _, engine := range []string{PolishEngineNelderMead, PolishEngineCMAES} {
		t.Run(engine, func(t *testing.T) {
			fixture := newPolishFixture(t)

			result, err := Polish(context.Background(), fixture.objective, fixture.nudged, PolishOptions{
				Engine:        engine,
				MaxIterations: 120,
				Seed:          7,
			})
			if err != nil {
				t.Fatalf("Polish failed: %v", err)
			}

			if !result.Accepted {
				t.Fatalf("expected the polish to be accepted: primary %g -> %g, polish %g -> %g",
					result.PrimaryBefore, result.PrimaryAfter, result.PolishBefore, result.PolishAfter)
			}

			if result.PrimaryAfter >= result.PrimaryBefore {
				t.Fatalf("accepted a polish that did not lower the primary cost: %g -> %g",
					result.PrimaryBefore, result.PrimaryAfter)
			}

			t.Logf("%s: primary %g -> %g, polish %g -> %g, iterations %d, evaluations %d",
				engine, result.PrimaryBefore, result.PrimaryAfter,
				result.PolishBefore, result.PolishAfter, result.Iterations, result.Evaluations)
		})
	}
}

func TestPolishKeepsTheIncumbentWhenItCannotImprove(t *testing.T) {
	fixture := newPolishFixture(t)

	result, err := Polish(context.Background(), fixture.objective, fixture.seed, PolishOptions{
		Engine:        PolishEngineNelderMead,
		MaxIterations: 60,
	})
	if err != nil {
		t.Fatalf("Polish failed: %v", err)
	}

	if result.Accepted {
		t.Fatalf("expected no acceptance from an incumbent already at the target: primary %g -> %g",
			result.PrimaryBefore, result.PrimaryAfter)
	}

	assertSameVector(t, result.Params, fixture.seed)

	t.Logf("rejected: primary %g -> %g, polish %g -> %g",
		result.PrimaryBefore, result.PrimaryAfter, result.PolishBefore, result.PolishAfter)
}

func TestPolishReturnsTheIncumbentOnACanceledContext(t *testing.T) {
	fixture := newPolishFixture(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	result, err := Polish(ctx, fixture.objective, fixture.nudged, PolishOptions{
		Engine:        PolishEngineCMAES,
		MaxIterations: 120,
	})
	if err != nil {
		t.Fatalf("a canceled polish must not be an error, got %v", err)
	}

	if result.Accepted {
		t.Fatal("a canceled polish must not be accepted")
	}

	assertSameVector(t, result.Params, fixture.nudged)
}

func TestPolishRejectsAnUnknownEngine(t *testing.T) {
	fixture := newPolishFixture(t)

	if _, err := Polish(context.Background(), fixture.objective, fixture.seed, PolishOptions{Engine: PolishEngineNone}); err == nil {
		t.Fatal("expected an error for the none engine")
	}
}

func TestPolishRejectsAStepWiderThanTheBox(t *testing.T) {
	fixture := newPolishFixture(t)

	_, err := Polish(context.Background(), fixture.objective, fixture.seed, PolishOptions{
		Engine:        PolishEngineNelderMead,
		Sigma:         1.5,
		MaxIterations: 5,
	})
	if err == nil || !strings.Contains(err.Error(), "polish sigma") {
		t.Fatalf("expected a step wider than the box to be refused, got %v", err)
	}
}

func assertSameVector(t *testing.T, got, want []float64) {
	t.Helper()

	if len(got) != len(want) {
		t.Fatalf("vector has %d dimensions, want %d", len(got), len(want))
	}

	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("dimension %d is %g, want %g", i, got[i], want[i])
		}
	}
}
