package model

import (
	"encoding/json"
	"reflect"
	"testing"
)

// fourModeParams is the bar's working shape: four modes carrying three
// harmonics each, plus a Chebyshev shaper. It is deliberately the widest of the
// fixtures here so that shrinking to any of the others reuses, rather than
// grows, every slice the bar holds.
func fourModeParams() BarParams {
	params := barParamsWithModes([]ModeParams{
		{Amplitude: 1.0, Frequency: 440, DecayMs: 200, Harmonics: []float64{1, 0.5, 0.25}},
		{Amplitude: 0.7, Frequency: 1320, DecayMs: 150, Harmonics: []float64{1, 0.4, 0.2}},
		{Amplitude: 0.5, Frequency: 2200, DecayMs: 100, Harmonics: []float64{1, 0.3, 0.1}},
		{Amplitude: 0.3, Frequency: 3080, DecayMs: 80, Harmonics: []float64{1, 0.2, 0.05}},
	})
	params.InputMix = 0.25
	params.Chebyshev = ChebyshevParams{Enabled: true, HarmonicGains: []float64{1, 0.5, 0.25, 0.125}}

	return params
}

func twoModeParams() BarParams {
	params := barParamsWithModes([]ModeParams{
		{Amplitude: 0.9, Frequency: 660, DecayMs: 300},
		{Amplitude: 0.4, Frequency: 1980, DecayMs: 120, Harmonics: []float64{1, 0.6}},
	})
	params.InputMix = 0.1
	params.FilterFrequency = 3200
	params.Chebyshev = ChebyshevParams{Enabled: true, HarmonicGains: []float64{0.8, 0.2}}

	return params
}

// sixModeParams is wider than fourModeParams in every dimension, so updating a
// bar to it forces every reusable slice to grow.
func sixModeParams() BarParams {
	modes := make([]ModeParams, 6)
	for i := range modes {
		modes[i] = ModeParams{
			Amplitude: 1.0 / float64(i+1),
			Frequency: 440 * float64(i+1),
			DecayMs:   200 - 10*float64(i),
			Harmonics: []float64{1, 0.5, 0.25, 0.125, 0.0625},
		}
	}

	params := barParamsWithModes(modes)
	params.Chebyshev = ChebyshevParams{Enabled: true, HarmonicGains: []float64{1, 0.9, 0.8, 0.7, 0.6, 0.5}}

	return params
}

func renderInto(t *testing.T, bar *Bar, samples int) []float32 {
	t.Helper()

	bar.Reset()

	out := bar.Synthesize(100, samples)

	rendered := make([]float32, len(out))
	copy(rendered, out)

	return rendered
}

func assertSamplesEqual(t *testing.T, what string, want, got []float32) {
	t.Helper()

	if len(want) != len(got) {
		t.Fatalf("%s: length mismatch: want %d, got %d", what, len(want), len(got))
	}

	for i := range want {
		if want[i] != got[i] {
			t.Fatalf("%s: sample %d differs: want %v, got %v", what, i, want[i], got[i])
		}
	}
}

// TestUpdateParamsIsAllocationFreeWhenTheShapeIsUnchanged is the point of the
// whole exercise: a bar that is retuned rather than rebuilt must not touch the
// allocator, because that is what lets a pooled voice be handed a new note from
// the audio thread.
func TestUpdateParamsIsAllocationFreeWhenTheShapeIsUnchanged(t *testing.T) {
	params := fourModeParams()

	bar, err := NewBar(&params, 48000)
	if err != nil {
		t.Fatalf("NewBar: %v", err)
	}

	// Same shape, different values, so the copy has real work to do and cannot
	// be short-circuited by an equality check that does not exist.
	retuned := fourModeParams()
	retuned.FilterFrequency = 5000
	retuned.InputMix = 0.4

	for i := range retuned.Modes {
		retuned.Modes[i].Frequency *= 1.5
		retuned.Modes[i].DecayMs *= 0.5
		retuned.Modes[i].Harmonics[1] = 0.75
	}

	// Warm every slice up to its final capacity before measuring.
	for i := 0; i < 4; i++ {
		if err := bar.UpdateParams(&retuned); err != nil {
			t.Fatalf("UpdateParams: %v", err)
		}
	}

	allocs := testing.AllocsPerRun(200, func() {
		if err := bar.UpdateParams(&retuned); err != nil {
			t.Fatalf("UpdateParams: %v", err)
		}
	})

	if allocs != 0 {
		t.Fatalf("UpdateParams allocated %v times per call on an unchanged shape, want 0", allocs)
	}
}

// TestUpdateParamsGrowsRatherThanCorruptsOnAShapeChange pins the other half of
// the contract: reuse is an optimization for the unchanged case, never a
// constraint on what a bar may be reconfigured into. Widening past the current
// capacity is allowed to allocate; what it may not do is panic or render from a
// half-updated configuration.
func TestUpdateParamsGrowsRatherThanCorruptsOnAShapeChange(t *testing.T) {
	params := twoModeParams()

	bar, err := NewBar(&params, 48000)
	if err != nil {
		t.Fatalf("NewBar: %v", err)
	}

	wider := sixModeParams()
	if err := bar.UpdateParams(&wider); err != nil {
		t.Fatalf("UpdateParams to a wider shape: %v", err)
	}

	if got := bar.NumModes(); got != len(wider.Modes) {
		t.Fatalf("NumModes after growing: want %d, got %d", len(wider.Modes), got)
	}

	if got := bar.NumHarmonics(); got != len(wider.Modes[0].Harmonics) {
		t.Fatalf("NumHarmonics after growing: want %d, got %d", len(wider.Modes[0].Harmonics), got)
	}

	reference, err := NewBar(&wider, 48000)
	if err != nil {
		t.Fatalf("NewBar for the reference: %v", err)
	}

	assertSamplesEqual(t, "grown bar", renderInto(t, reference, 1024), renderInto(t, bar, 1024))
}

// TestReconfiguredBarRendersLikeAFreshOne is the assertion that catches a copy
// which reuses a slice but leaves stale elements past the new length: an
// in-place retune has to be indistinguishable from building the bar from
// scratch, in both directions and after several hops.
func TestReconfiguredBarRendersLikeAFreshOne(t *testing.T) {
	const (
		sampleRate = 48000
		samples    = 1024
	)

	fixtures := map[string]BarParams{
		"four": fourModeParams(),
		"two":  twoModeParams(),
		"six":  sixModeParams(),
	}

	// Every hop of the walk lands on a shape that is wider or narrower than the
	// one before it, so the reused slices are grown and shrunk repeatedly.
	walk := []string{"four", "two", "six", "two", "four", "six", "four"}

	bar, err := NewBar(paramsPtr(fixtures["four"]), sampleRate)
	if err != nil {
		t.Fatalf("NewBar: %v", err)
	}

	for _, name := range walk {
		target := fixtures[name]

		if err := bar.UpdateParams(&target); err != nil {
			t.Fatalf("UpdateParams to %q: %v", name, err)
		}

		fresh, err := NewBar(&target, sampleRate)
		if err != nil {
			t.Fatalf("NewBar for %q: %v", name, err)
		}

		assertSamplesEqual(t, "retuned to "+name, renderInto(t, fresh, samples), renderInto(t, bar, samples))
	}
}

func paramsPtr(p BarParams) *BarParams { return &p }

// TestUpdateParamsLeavesTheFilterStateAlone documents the deliberate choice not
// to clear the lowpass delay line when the coefficients change. A parameter
// write is not a discontinuity in the signal; a caller that wants a clean slate
// asks for one with Reset.
func TestUpdateParamsLeavesTheFilterStateAlone(t *testing.T) {
	params := fourModeParams()

	bar, err := NewBar(&params, 48000)
	if err != nil {
		t.Fatalf("NewBar: %v", err)
	}

	bar.Synthesize(127, 64)

	stateBefore := bar.lowpass.State()
	if stateBefore == [2]float64{} {
		t.Fatal("expected the lowpass to carry state after rendering")
	}

	unchangedFrequency := params
	if err := bar.UpdateParams(&unchangedFrequency); err != nil {
		t.Fatalf("UpdateParams: %v", err)
	}

	if bar.lowpass.State() != stateBefore {
		t.Fatal("UpdateParams cleared the lowpass state although the cutoff was unchanged")
	}

	retuned := params
	retuned.FilterFrequency = 2000

	if err := bar.UpdateParams(&retuned); err != nil {
		t.Fatalf("UpdateParams: %v", err)
	}

	if bar.lowpass.State() != stateBefore {
		t.Fatal("UpdateParams cleared the lowpass state on a cutoff change")
	}

	bar.Reset()

	if bar.lowpass.State() != [2]float64{} {
		t.Fatal("Reset left lowpass state behind")
	}
}

func TestCopyIntoIsDeepAndReusesTheDestination(t *testing.T) {
	source := fourModeParams()

	var dst BarParams
	source.CopyInto(&dst)

	modesBefore := &dst.Modes[0]
	gainsBefore := &dst.Chebyshev.HarmonicGains[0]

	// A second copy of the same shape must land in the very same backing arrays.
	source.CopyInto(&dst)

	if &dst.Modes[0] != modesBefore {
		t.Fatal("CopyInto reallocated the modes slice for an unchanged shape")
	}

	if &dst.Chebyshev.HarmonicGains[0] != gainsBefore {
		t.Fatal("CopyInto reallocated the Chebyshev gains for an unchanged shape")
	}

	dst.Modes[0].Frequency = 1
	dst.Modes[0].Harmonics[0] = 0
	dst.Chebyshev.HarmonicGains[0] = 0

	if source.Modes[0].Frequency == 1 || source.Modes[0].Harmonics[0] == 0 || source.Chebyshev.HarmonicGains[0] == 0 {
		t.Fatal("CopyInto shares backing arrays with the source")
	}

	// Shrinking must not leave the elements past the new length visible.
	narrow := twoModeParams()
	narrow.CopyInto(&dst)

	if len(dst.Modes) != len(narrow.Modes) {
		t.Fatalf("modes after shrinking: want %d, got %d", len(narrow.Modes), len(dst.Modes))
	}

	if dst.Modes[0].Harmonics != nil {
		t.Fatalf("stale harmonics survived the shrink: %v", dst.Modes[0].Harmonics)
	}
}

// TestCopyIntoBreaksAliasingWithTheSource covers a destination that already
// shares its backing arrays with the source, which is what a shallow struct
// copy leaves behind. Reusing those arrays would make CopyInto a no-op that
// silently keeps the two values aliased, so the deep-copy guarantee would hold
// on paper and not in fact.
func TestCopyIntoBreaksAliasingWithTheSource(t *testing.T) {
	source := fourModeParams()

	// The shallow copy: dst.Modes, dst.Modes[i].Harmonics and the Chebyshev
	// gains are all the very same arrays the source holds.
	dst := source
	source.CopyInto(&dst)

	dst.Modes[0].Frequency = 12345
	dst.Modes[0].Harmonics[0] = 777
	dst.Chebyshev.HarmonicGains[0] = 999

	if source.Modes[0].Frequency != 440 {
		t.Fatalf("CopyInto left the modes slice aliased: source frequency is %v", source.Modes[0].Frequency)
	}

	if source.Modes[0].Harmonics[0] != 1 {
		t.Fatalf("CopyInto left the harmonics aliased: source gain is %v", source.Modes[0].Harmonics[0])
	}

	if source.Chebyshev.HarmonicGains[0] != 1 {
		t.Fatalf("CopyInto left the Chebyshev gains aliased: source gain is %v", source.Chebyshev.HarmonicGains[0])
	}

	// Self-copy has to stay a well-behaved no-op rather than corrupting p.
	selfCopy := fourModeParams()
	want := selfCopy.Clone()
	selfCopy.CopyInto(&selfCopy)

	if !reflect.DeepEqual(selfCopy, want) {
		t.Fatalf("self-copy changed the value:\n want %+v\n  got %+v", want, selfCopy)
	}
}

// TestCopyIntoPreservesNilness guards the JSON round-trip: BarParams is
// serialized, and a nil slice and an empty one do not encode alike.
func TestCopyIntoPreservesNilness(t *testing.T) {
	cases := map[string][]float64{
		"nil":   nil,
		"empty": {},
	}

	for name, gains := range cases {
		t.Run(name, func(t *testing.T) {
			source := barParamsWithModes(nil)
			source.Chebyshev = ChebyshevParams{HarmonicGains: gains}

			// Start from a populated destination so the reuse path is the one
			// under test, not the fresh-allocation path.
			dst := fourModeParams()
			source.CopyInto(&dst)

			want, err := json.Marshal(source)
			if err != nil {
				t.Fatalf("marshal source: %v", err)
			}

			got, err := json.Marshal(dst)
			if err != nil {
				t.Fatalf("marshal destination: %v", err)
			}

			if string(want) != string(got) {
				t.Fatalf("JSON differs after CopyInto:\n want %s\n  got %s", want, got)
			}
		})
	}
}
