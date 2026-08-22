package synth

import (
	"math"
	"path/filepath"
	"testing"

	"github.com/cwbudde/glockenspiel/internal/preset"
)

// wantDefaultPresetPeakDBFS pins the shipped preset's output level.
//
// The preset used to render at a peak of about 6.174, roughly +15.8 dBFS. That
// is not a taste question: a 16-bit render of it clips beyond recognition, and
// any fit against a normalized recording has to travel some 16 dB of pure gain
// before the modal structure it is supposed to be fitting matters at all. The
// amplitudes were divided by 8.72 to land here, and this test exists so the
// level cannot drift back unnoticed -- an edit to the modes that changes the
// peak by more than a fraction of a dB has to say so.
const (
	wantDefaultPresetPeakDBFS    = -3.0
	defaultPresetPeakToleranceDB = 0.25
)

// TestDefaultPresetRendersNearMinusThreeDBFS renders the shipped preset at its
// own note and asserts the peak.
//
// It renders at 44.1 kHz, the rate the rest of the preset fixtures use. The
// peak is mildly rate-dependent -- the strike is a single-sample impulse, so a
// higher rate feeds the modes a shorter, wider-band excitation and 48 kHz comes
// out at about -2.3 dBFS -- which is why the tolerance guards a level rather
// than an exact sample value, and why the target sits at -3 rather than at 0:
// there has to be headroom for the rate the caller actually picks.
func TestDefaultPresetRendersNearMinusThreeDBFS(t *testing.T) {
	p, err := preset.Load(filepath.FromSlash("../../assets/presets/default.json"))
	if err != nil {
		t.Fatalf("load default preset: %v", err)
	}

	synthesizer, err := NewSynthesizer(p, 44100)
	if err != nil {
		t.Fatalf("NewSynthesizer: %v", err)
	}

	rendered := synthesizer.RenderNote(p.Note, 100, 1.0)
	if len(rendered) == 0 {
		t.Fatal("default preset rendered nothing")
	}

	peak := 0.0

	for _, sample := range rendered {
		if abs := math.Abs(float64(sample)); abs > peak {
			peak = abs
		}
	}

	if peak <= 0 {
		t.Fatal("default preset rendered silence")
	}

	peakDBFS := 20 * math.Log10(peak)
	if math.Abs(peakDBFS-wantDefaultPresetPeakDBFS) > defaultPresetPeakToleranceDB {
		t.Fatalf("default preset peak = %.6f (%+.3f dBFS), want %+.1f dBFS within %.2f dB",
			peak, peakDBFS, wantDefaultPresetPeakDBFS, defaultPresetPeakToleranceDB)
	}
}

// TestDefaultPresetModesKeepTheirSigns guards the one thing the rescale had to
// preserve. Mode 2 is negative; scaling keeps it negative and keeps its
// magnitude in proportion to the rest, whereas clamping a rescale to a positive
// range would quietly change the phase relationship between the modes and with
// it the attack transient.
func TestDefaultPresetModesKeepTheirSigns(t *testing.T) {
	p, err := preset.Load(filepath.FromSlash("../../assets/presets/default.json"))
	if err != nil {
		t.Fatalf("load default preset: %v", err)
	}

	wantSigns := []float64{1, 1, -1, 1}
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
