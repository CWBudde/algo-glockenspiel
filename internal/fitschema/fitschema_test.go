package fitschema_test

import (
	"testing"

	"github.com/cwbudde/algo-glockenspiel/internal/fitschema"
)

// TestIntLimitAndFloatLimitReadTheSameTableAsDefault pins the three
// accessors internal/server and internal/browserfit both call to one field's
// values, so a table entry that carries a default, a minimum and a maximum
// answers all three consistently.
func TestIntLimitAndFloatLimitReadTheSameTableAsDefault(t *testing.T) {
	min, max := fitschema.IntLimit("note")
	if min != 0 || max != 127 {
		t.Fatalf("IntLimit(note) = (%d,%d), want (0,127)", min, max)
	}

	if got := fitschema.DefaultInt("note"); got != 69 {
		t.Fatalf("DefaultInt(note) = %d, want 69", got)
	}

	targetMin, targetMax := fitschema.FloatLimit("mayflyTargetCost")
	if targetMin != -fitschema.MaxFitTargetCost || targetMax != fitschema.MaxFitTargetCost {
		t.Fatalf("FloatLimit(mayflyTargetCost) = (%g,%g), want (%g,%g)",
			targetMin, targetMax, -fitschema.MaxFitTargetCost, fitschema.MaxFitTargetCost)
	}
}

// TestMustFieldPanicsOnAnUnknownKey documents IntLimit's contract: a call
// site names a field literally, so a typo is a programming error caught the
// first time the code path runs, not an unbounded check that silently
// accepts everything.
func TestMustFieldPanicsOnAnUnknownKey(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("IntLimit(unknown) did not panic")
		}
	}()

	fitschema.IntLimit("nope")
}

// TestFieldsCoversEveryNameListAndDefaultFitRequestKey is a light structural
// check that the table has not lost a field cmd/gen-fit-schema depends on:
// every InRequest field with HasDefault also has a non-nil Default, and
// every HasLimit field has Min <= Max.
func TestFieldsCoversEveryNameListAndDefaultFitRequestKey(t *testing.T) {
	for _, field := range fitschema.Fields() {
		if field.HasDefault && field.Default == nil {
			t.Fatalf("field %q has HasDefault but a nil Default", field.Key)
		}

		if field.HasLimit && field.Min > field.Max {
			t.Fatalf("field %q has Min %g above Max %g", field.Key, field.Min, field.Max)
		}
	}

	for _, list := range [][]string{
		fitschema.OptimizerNames(),
		fitschema.MetricNames(),
		fitschema.CMAESCovariances(),
		fitschema.MayflyVariants(),
		fitschema.MayflyPresets(),
		fitschema.MayflySelections(),
		fitschema.BoundsKeys(),
	} {
		if len(list) == 0 {
			t.Fatal("a name list is empty")
		}
	}
}
