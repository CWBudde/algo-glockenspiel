package synth

import (
	"math"
	"path/filepath"
	"testing"

	"github.com/cwbudde/algo-glockenspiel/assets"
	"github.com/cwbudde/algo-glockenspiel/internal/preset"
)

// wantBuiltinPresetPeakDBFS pins every shipped preset's output level.
//
// The default preset used to render at a peak of about 6.174, roughly +15.8
// dBFS. That is not a taste question: a 16-bit render of it clips beyond
// recognition, and any fit against a normalized recording has to travel some
// 16 dB of pure gain before the modal structure it is supposed to be fitting
// matters at all. The amplitudes were divided by 8.72 to land here, and this
// test exists so the level cannot drift back unnoticed -- an edit to the modes
// that changes the peak by more than a fraction of a dB has to say so.
//
// It is a headroom rule, not a loudness rule, and the two are not the same
// thing: a preset fitted to a bar with a tall strike transient reaches this
// peak at a lower RMS than one fitted to a bar without. Matching the peaks is
// what keeps a render from clipping at a sample rate the author did not pick;
// what a preset then sounds like next to another one is a property of the bar.
// The values live in level.go, because a fit now writes presets to this same
// target and the two must not be able to drift apart.
const (
	wantBuiltinPresetPeakDBFS    = PresetPeakTargetDBFS
	builtinPresetPeakToleranceDB = PresetPeakToleranceDB
)

// TestBuiltinPresetsRenderNearMinusThreeDBFS renders every embedded preset at
// its own note and asserts the peak.
//
// It renders at 44.1 kHz, the rate the rest of the preset fixtures use. The
// peak is mildly rate-dependent -- the strike is a single-sample impulse, so a
// higher rate feeds the modes a shorter, wider-band excitation and 48 kHz comes
// out at about -2.3 dBFS -- which is why the tolerance guards a level rather
// than an exact sample value, and why the target sits at -3 rather than at 0:
// there has to be headroom for the rate the caller actually picks.
//
// It reads the embedded set rather than the directory, because the embedded set
// is what ships and what the browser plays.
func TestBuiltinPresetsRenderNearMinusThreeDBFS(t *testing.T) {
	ids, err := assets.IDs()
	if err != nil {
		t.Fatalf("assets.IDs: %v", err)
	}

	for _, id := range ids {
		p, err := assets.Preset(id)
		if err != nil {
			t.Fatalf("load preset %q: %v", id, err)
		}

		synthesizer, err := NewSynthesizer(p, 44100)
		if err != nil {
			t.Fatalf("NewSynthesizer(%q): %v", id, err)
		}

		peak := peakOf(synthesizer.RenderNote(p.Note, 100, 1.0))
		if peak <= 0 {
			t.Fatalf("preset %q rendered silence at its own note", id)
		}

		peakDBFS := 20 * math.Log10(peak)
		if math.Abs(peakDBFS-wantBuiltinPresetPeakDBFS) > builtinPresetPeakToleranceDB {
			t.Fatalf("preset %q peak = %.6f (%+.3f dBFS), want %+.1f dBFS within %.2f dB",
				id, peak, peakDBFS, wantBuiltinPresetPeakDBFS, builtinPresetPeakToleranceDB)
		}
	}
}

// TestDefaultPresetModesKeepTheirSigns guards the one thing a rescale of the
// shipped preset has to preserve: a mode's sign is a phase relationship with
// the others, and it shapes the attack transient. Clamping a rescale to a
// positive range, or dividing by a negative factor, would change the sound
// while leaving the peak level the test above pins exactly where it was.
//
// The pattern is the re-fit preset's, {+, +, +, -}, and it is not the same one
// the preset carried before: that was {+, +, -, +}. A re-fit is entitled to
// land on a different sign pattern, since it is solving for the waveform rather
// than adjusting the one it was given -- so the list below moves when the
// preset is re-fitted, and stays put for anything short of that.
func TestDefaultPresetModesKeepTheirSigns(t *testing.T) {
	p, err := preset.Load(filepath.FromSlash("../../assets/presets/default.json"))
	if err != nil {
		t.Fatalf("load default preset: %v", err)
	}

	wantSigns := []float64{1, 1, 1, -1}
	if len(p.Parameters.Modes) != len(wantSigns) {
		t.Fatalf("default preset mode count = %d, want %d", len(p.Parameters.Modes), len(wantSigns))
	}

	for i, want := range wantSigns {
		amplitude := p.Parameters.Modes[i].Amplitude
		if amplitude == 0 || math.Signbit(amplitude) != math.Signbit(want) {
			t.Fatalf("mode %d amplitude = %g, want sign %+.0f", i, amplitude, want)
		}
	}
}
