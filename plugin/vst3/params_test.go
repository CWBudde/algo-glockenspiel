package vst3

import (
	"reflect"
	"testing"

	"github.com/cwbudde/glockenspiel/assets"
	"github.com/cwbudde/glockenspiel/model"
)

func TestParameterSpecsHaveStableUniqueIDs(t *testing.T) {
	specs := ParameterSpecs()
	if len(specs) != 20 {
		t.Fatalf("unexpected parameter count: got %d want 20", len(specs))
	}

	seenIDs := make(map[ParameterID]struct{}, len(specs))

	seenKeys := make(map[string]struct{}, len(specs))
	for _, spec := range specs {
		if _, ok := seenIDs[spec.ID]; ok {
			t.Fatalf("duplicate parameter id: %d", spec.ID)
		}

		seenIDs[spec.ID] = struct{}{}

		if _, ok := seenKeys[spec.Key]; ok {
			t.Fatalf("duplicate parameter key: %q", spec.Key)
		}

		seenKeys[spec.Key] = struct{}{}

		if spec.Min > spec.Max {
			t.Fatalf("invalid range for %q: min=%f max=%f", spec.Key, spec.Min, spec.Max)
		}

		if spec.Default < spec.Min || spec.Default > spec.Max {
			t.Fatalf("default out of range for %q: default=%f", spec.Key, spec.Default)
		}
	}
}

func TestSnapshotRoundTripBarParams(t *testing.T) {
	input := model.BarParams{
		InputMix:        0.25,
		FilterFrequency: 5400,
		BaseFrequency:   440,
		Modes: []model.ModeParams{
			{Amplitude: 1.0, Frequency: 440, DecayMs: 120},
			{Amplitude: 0.7, Frequency: 1180, DecayMs: 90},
			{Amplitude: 0.3, Frequency: 2010, DecayMs: 70},
			{Amplitude: 0.1, Frequency: 3180, DecayMs: 40},
		},
		Chebyshev: model.ChebyshevParams{
			Enabled:       true,
			HarmonicGains: []float64{1.0, 0.3, 0.15, 0.05},
		},
	}

	snapshot := SnapshotFromBarParams(&input)
	output := snapshot.ToBarParams()

	if output.InputMix != input.InputMix {
		t.Fatalf("input mix mismatch: got %f want %f", output.InputMix, input.InputMix)
	}

	if output.FilterFrequency != input.FilterFrequency {
		t.Fatalf("filter frequency mismatch: got %f want %f", output.FilterFrequency, input.FilterFrequency)
	}

	if output.BaseFrequency != input.BaseFrequency {
		t.Fatalf("base frequency mismatch: got %f want %f", output.BaseFrequency, input.BaseFrequency)
	}

	if output.Chebyshev.Enabled != input.Chebyshev.Enabled {
		t.Fatalf("chebyshev enabled mismatch: got %v want %v", output.Chebyshev.Enabled, input.Chebyshev.Enabled)
	}

	for i := 0; i < numModes; i++ {
		if output.Chebyshev.HarmonicGains[i] != input.Chebyshev.HarmonicGains[i] {
			t.Fatalf("harmonic gain mismatch at %d: got %f want %f", i, output.Chebyshev.HarmonicGains[i], input.Chebyshev.HarmonicGains[i])
		}

		if !reflect.DeepEqual(output.Modes[i], input.Modes[i]) {
			t.Fatalf("mode mismatch at %d: got %+v want %+v", i, output.Modes[i], input.Modes[i])
		}
	}
}

// TestSnapshotCopiesChebyshevGainsIndependentlyOfModeCount pins that the two
// counts are unrelated: a single-mode bar still carries all four gains.
func TestSnapshotCopiesChebyshevGainsIndependentlyOfModeCount(t *testing.T) {
	params := model.BarParams{
		InputMix:        0.5,
		FilterFrequency: 8000,
		BaseFrequency:   440,
		Modes:           []model.ModeParams{{Amplitude: 1, Frequency: 1000, DecayMs: 100}},
		Chebyshev: model.ChebyshevParams{
			Enabled:       true,
			HarmonicGains: []float64{1, 0.5, 0.3, 0.2},
		},
	}

	snapshot := SnapshotFromBarParams(&params)

	for i, want := range params.Chebyshev.HarmonicGains {
		if snapshot.ChebyshevGains[i] != want {
			t.Fatalf("gain %d = %v, want %v", i, snapshot.ChebyshevGains[i], want)
		}
	}
}

// TestDefaultSnapshotMatchesTheShippedPreset is the guard on the one piece of
// duplicated data in this package.
//
// defaultSnapshot is a transcription of assets/presets/default.json, kept as a
// literal so that this package's only dependency stays model -- it is the
// surface Phase 6 splits into its own module, and an external module cannot
// import internal/preset, which is what reaching for assets would pull in. A
// literal can drift, and this one had, twice: 5d7af10 rescaled every mode
// amplitude by 0.1147 and left the plugin 18.8 dB loud, and the Chebyshev DC
// fix plus the re-fit that followed it left the plugin rendering at -37.42 dBFS
// against the preset's -3.19.
//
// Neither showed up anywhere, because nothing compared the two. This does. When
// the preset is re-fitted, the numbers above move with it.
//
// The import of assets lives here rather than in the package proper for the
// same reason the literal does: a test is far easier to relocate than a runtime
// dependency when the split happens.
func TestDefaultSnapshotMatchesTheShippedPreset(t *testing.T) {
	shipped, err := assets.DefaultPreset()
	if err != nil {
		t.Fatalf("load the shipped preset: %v", err)
	}

	want := SnapshotFromBarParams(&shipped.Parameters)
	if got := DefaultSnapshot(); got != want {
		t.Errorf("the plugin default has drifted from assets/presets/default.json\n got: %+v\nwant: %+v", got, want)
	}

	// SnapshotFromBarParams silently takes the first numModes modes and the
	// first numChebyshevGains gains, so a preset that outgrew either would make
	// the comparison above pass while describing something the plugin cannot
	// represent.
	if len(shipped.Parameters.Modes) != numModes {
		t.Errorf("the shipped preset carries %d modes, but the plugin exposes %d",
			len(shipped.Parameters.Modes), numModes)
	}

	if len(shipped.Parameters.Chebyshev.HarmonicGains) != numChebyshevGains {
		t.Errorf("the shipped preset carries %d Chebyshev gains, but the plugin exposes %d",
			len(shipped.Parameters.Chebyshev.HarmonicGains), numChebyshevGains)
	}
}
