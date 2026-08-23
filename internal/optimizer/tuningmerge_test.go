package optimizer

import (
	"reflect"
	"testing"
)

func TestMayflyTuningOverlay(t *testing.T) {
	floatPtr := func(v float64) *float64 { return &v }
	intPtr := func(v int) *int { return &v }

	t.Run("nil sides", func(t *testing.T) {
		base := &MayflyTuning{Mu: floatPtr(0.5)}

		if got := base.Overlay(nil); got != base {
			t.Fatal("a nil override must keep the base")
		}

		var nilBase *MayflyTuning
		if got := nilBase.Overlay(base); got != base {
			t.Fatal("a nil base must yield the override")
		}
	})

	t.Run("the override wins and the rest survives", func(t *testing.T) {
		base := &MayflyTuning{Mu: floatPtr(0.5), NCRatio: floatPtr(1.0)}
		override := &MayflyTuning{NCRatio: floatPtr(0.25)}

		merged := base.Overlay(override)
		if *merged.Mu != 0.5 {
			t.Fatalf("an unset key must survive: got %v", *merged.Mu)
		}

		if *merged.NCRatio != 0.25 {
			t.Fatalf("a set key must win: got %v", *merged.NCRatio)
		}
	})

	t.Run("the base is not modified", func(t *testing.T) {
		base := &MayflyTuning{NCRatio: floatPtr(1.0)}
		_ = base.Overlay(&MayflyTuning{NCRatio: floatPtr(0.25)})

		if *base.NCRatio != 1.0 {
			t.Fatalf("Overlay modified its receiver: got %v", *base.NCRatio)
		}
	})

	t.Run("nested blocks merge key by key", func(t *testing.T) {
		base := &MayflyTuning{Convergence: &MayflyConvergence{
			StagnationIterations: intPtr(10),
			MinIterations:        intPtr(2),
		}}
		override := &MayflyTuning{Convergence: &MayflyConvergence{
			StagnationIterations: intPtr(40),
		}}

		merged := base.Overlay(override)
		if *merged.Convergence.StagnationIterations != 40 {
			t.Fatalf("nested key did not win: got %v", *merged.Convergence.StagnationIterations)
		}

		if merged.Convergence.MinIterations == nil || *merged.Convergence.MinIterations != 2 {
			t.Fatal("a nested key the override omitted was discarded")
		}

		if *base.Convergence.StagnationIterations != 10 {
			t.Fatal("Overlay modified the receiver's nested block")
		}
	})

	t.Run("every field is mergeable", func(t *testing.T) {
		// A field added to MayflyTuning but not handled here would be silently
		// unmergeable, so walk the whole struct rather than trusting a list.
		full := fullyPopulatedTuning(t)

		merged := (&MayflyTuning{}).Overlay(full)
		if !reflect.DeepEqual(merged, full) {
			t.Fatal("overlaying onto an empty document did not reproduce it")
		}
	})
}

// fullyPopulatedTuning fills every pointer field with a non-zero value.
func fullyPopulatedTuning(t *testing.T) *MayflyTuning {
	t.Helper()

	tuning := &MayflyTuning{}
	populate(reflect.ValueOf(tuning).Elem())

	return tuning
}

func populate(value reflect.Value) {
	for i := range value.NumField() {
		field := value.Field(i)
		if field.Kind() != reflect.Pointer {
			continue
		}

		created := reflect.New(field.Type().Elem())

		switch created.Elem().Kind() {
		case reflect.Struct:
			populate(created.Elem())
		case reflect.Float64:
			created.Elem().SetFloat(0.5)
		case reflect.Int:
			created.Elem().SetInt(3)
		case reflect.String:
			created.Elem().SetString("x")
		case reflect.Bool:
			created.Elem().SetBool(true)
		}

		field.Set(created)
	}
}
