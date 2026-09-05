package optimizer

import (
	"math"
	"strings"
	"testing"
)

// TestMultiNoteScoreIsTheMeanOfItsNotes is the property the aggregate rests on,
// and the one that makes a note's score comparable to its neighbour's.
//
// It is worth asserting rather than reading off the implementation because the
// tempting alternative -- averaging each of the eleven terms across notes and
// scoring once -- produces a different number, and one that quietly defeats the
// saturation the terms are put through.
func TestMultiNoteScoreIsTheMeanOfItsNotes(t *testing.T) {
	template := threeModePreset()
	template.Note = 90

	refA := renderReference(t, template, 44100, 90, 100, 0.5)
	refB := renderReference(t, template, 44100, 100, 100, 0.5)

	config := DefaultObjectiveConfig(MetricBalanced)

	single := func(note int, samples []float32) *ObjectiveFunction {
		t.Helper()

		obj, err := NewObjectiveFunctionWithConfig(samples, template, 44100, note, 100, config)
		if err != nil {
			t.Fatalf("single-note objective at %d: %v", note, err)
		}

		return obj
	}

	objA, objB := single(90, refA), single(100, refB)

	joint, err := NewMultiNoteObjective([]ReferenceInput{
		{Samples: refA, Note: 90},
		{Samples: refB, Note: 100},
	}, template, 44100, 100, config)
	if err != nil {
		t.Fatalf("NewMultiNoteObjective: %v", err)
	}

	if got := joint.Notes(); len(got) != 2 || got[0] != 90 || got[1] != 100 {
		t.Fatalf("Notes() = %v, want [90 100]", got)
	}

	encoded, err := joint.Codec().EncodeParams(&template.Parameters)
	if err != nil {
		t.Fatalf("EncodeParams: %v", err)
	}

	// Several points, not just the template, so this pins a property of the
	// function rather than of one candidate.
	bounds := joint.Codec().EncodedBounds()

	for _, mix := range []float64{0, 0.25, 0.5, 1} {
		candidate := append([]float64(nil), encoded...)
		for i := range candidate {
			candidate[i] = candidate[i]*(1-mix) + bounds.Ranges[i].Max*mix
		}

		want := (objA.Evaluate(candidate) + objB.Evaluate(candidate)) / 2

		if got := joint.Evaluate(candidate); math.Abs(got-want) > 1e-12 {
			t.Errorf("at mix %g the joint score is %.12f, want the mean %.12f", mix, got, want)
		}
	}
}

// TestMultiNoteWithOneReferenceIsTheSingleNoteObjective pins the degenerate
// case, which is what lets every existing caller keep its constructor.
func TestMultiNoteWithOneReferenceIsTheSingleNoteObjective(t *testing.T) {
	template := threeModePreset()
	template.Note = 90

	reference := renderReference(t, template, 44100, 90, 100, 0.5)
	config := DefaultObjectiveConfig(MetricBalanced)

	single, err := NewObjectiveFunctionWithConfig(reference, template, 44100, 90, 100, config)
	if err != nil {
		t.Fatalf("single: %v", err)
	}

	joint, err := NewMultiNoteObjective(
		[]ReferenceInput{{Samples: reference, Note: 90}}, template, 44100, 100, config)
	if err != nil {
		t.Fatalf("joint: %v", err)
	}

	encoded, err := single.Codec().EncodeParams(&template.Parameters)
	if err != nil {
		t.Fatalf("EncodeParams: %v", err)
	}

	if got, want := joint.Evaluate(encoded), single.Evaluate(encoded); got != want {
		t.Errorf("one-reference joint score %v, single-note score %v: they must be the same function", got, want)
	}

	if single.Codec().Dimension() != joint.Codec().Dimension() {
		t.Error("the two objectives disagree about the vector's dimension")
	}
}

// TestMultiNoteRefusesALegacyMetric states the reason in the error rather than
// leaving it to a reader: the legacy metrics are raw errors whose scale follows
// the recording's own level and length, so averaging them across notes weights
// the notes by loudness.
func TestMultiNoteRefusesALegacyMetric(t *testing.T) {
	template := threeModePreset()
	template.Note = 90

	refA := renderReference(t, template, 44100, 90, 100, 0.5)
	refB := renderReference(t, template, 44100, 100, 100, 0.5)

	inputs := []ReferenceInput{{Samples: refA, Note: 90}, {Samples: refB, Note: 100}}

	for _, metric := range []Metric{MetricRMS, MetricLog, MetricSpectral} {
		_, err := NewMultiNoteObjective(inputs, template, 44100, 100, DefaultObjectiveConfig(metric))
		if err == nil {
			t.Errorf("metric %q was accepted for a multi-note objective", metric)

			continue
		}

		if !strings.Contains(err.Error(), "composite") {
			t.Errorf("metric %q: the error does not say what is wanted instead: %v", metric, err)
		}
	}
}

// TestMultiNoteRefusesTwoReferencesAtOneNote catches the mistake that would
// otherwise be silent: the candidate renders once for both, so the note would
// carry twice the weight of every other and nothing in the score would say so.
func TestMultiNoteRefusesTwoReferencesAtOneNote(t *testing.T) {
	template := threeModePreset()
	template.Note = 90

	reference := renderReference(t, template, 44100, 90, 100, 0.5)

	_, err := NewMultiNoteObjective([]ReferenceInput{
		{Samples: reference, Note: 90},
		{Samples: reference, Note: 90},
	}, template, 44100, 100, DefaultObjectiveConfig(MetricBalanced))
	if err == nil {
		t.Fatal("two references at the same note were accepted")
	}

	if !strings.Contains(err.Error(), "weight") {
		t.Errorf("the error does not say what goes wrong: %v", err)
	}
}

// TestMultiNoteRefusesNoReferences keeps refs non-empty, which every accessor
// reading refs[0] depends on.
func TestMultiNoteRefusesNoReferences(t *testing.T) {
	template := threeModePreset()

	if _, err := NewMultiNoteObjective(nil, template, 44100, 100,
		DefaultObjectiveConfig(MetricBalanced)); err == nil {
		t.Fatal("an objective with no references was accepted")
	}
}
