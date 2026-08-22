package synth

import (
	"math"
	"path/filepath"
	"testing"

	"github.com/cwbudde/algo-glockenspiel/internal/preset"
	"github.com/cwbudde/algo-glockenspiel/internal/wavio"
)

// wantLegacyCorrelation is how closely a render of the shipped preset has to
// track the reference it was fitted against.
const wantLegacyCorrelation = 0.95

// TestLegacyComparisonA4 renders the shipped preset at the reference's note and
// compares the waveform against testdata/reference/legacy_synth_a4.wav.
//
// It used to read a second WAV, testdata/output/go_synth_a4.wav, that nothing
// in this repository has ever produced -- no recipe, no script, no workflow,
// and the path is gitignored -- so the test skipped on a missing file even when
// its environment gate was set. It now renders its own comparison signal, which
// is what the phantom file was standing in for, and decodes the reference
// through internal/wavio rather than through a private copy of the decoder.
// That copy mattered: it carried the same 2^(bits-1) scaling that read this
// file's 32-bit floats as integers, so the test compared a render against a
// square wave.
//
// It ran skipped for one commit, for a stated reason rather than an environment
// variable: the shipped preset did not track this reference, correlation
// -0.5261, which was the Chebyshev shaper's DC offset holding the bar at a
// steady level where the reference decays into silence within 0.557 s. Both
// halves of that are now fixed -- the shaper maps silence to silence and the
// preset has been re-fitted against this file -- and the correlation is 0.9655,
// so the gate is gone and the assertion runs.
//
// Note that the correlation is computed without time alignment, while the
// optimizer's objective aligns candidates before scoring. That makes this a
// strictly stronger check than the fit it came from: two presets can score the
// same against the objective and land differently here, and one of the two
// candidate re-fits did exactly that at -0.4157.
func TestLegacyComparisonA4(t *testing.T) {
	reference, sampleRate, err := wavio.LoadMono(filepath.FromSlash("../../testdata/reference/legacy_synth_a4.wav"))
	if err != nil {
		t.Fatalf("load reference: %v", err)
	}

	p, err := preset.Load(filepath.FromSlash("../../assets/presets/default.json"))
	if err != nil {
		t.Fatalf("load preset: %v", err)
	}

	synthesizer, err := NewSynthesizer(p, sampleRate)
	if err != nil {
		t.Fatalf("NewSynthesizer: %v", err)
	}

	duration := float64(len(reference)) / float64(sampleRate)
	rendered := synthesizer.RenderNote(p.Note, 100, duration)

	n := minInt(len(reference), len(rendered))
	if n == 0 {
		t.Fatal("nothing to compare")
	}

	legacySamples := make([]float64, n)
	goSamples := make([]float64, n)

	for i := 0; i < n; i++ {
		legacySamples[i] = float64(reference[i])
		goSamples[i] = float64(rendered[i])
	}

	corr := correlation(legacySamples, goSamples)
	rms := rmsDifference(legacySamples, goSamples)

	if corr < wantLegacyCorrelation {
		t.Fatalf("correlation too low: got %.4f want >= %.2f (rms=%.6f)", corr, wantLegacyCorrelation, rms)
	}
}

func correlation(first, second []float64) float64 {
	if len(first) == 0 || len(first) != len(second) {
		return 0
	}

	meanA := mean(first)
	meanB := mean(second)

	var (
		num  float64
		denA float64
		denB float64
	)

	for i := range first {
		da := first[i] - meanA
		db := second[i] - meanB
		num += da * db
		denA += da * da
		denB += db * db
	}

	den := math.Sqrt(denA * denB)
	if den == 0 {
		return 0
	}

	return num / den
}

func rmsDifference(first, second []float64) float64 {
	if len(first) == 0 || len(first) != len(second) {
		return math.Inf(1)
	}

	sum := 0.0

	for i := range first {
		d := first[i] - second[i]
		sum += d * d
	}

	return math.Sqrt(sum / float64(len(first)))
}

func mean(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}

	sum := 0.0
	for _, v := range values {
		sum += v
	}

	return sum / float64(len(values))
}

func minInt(a, b int) int {
	if a < b {
		return a
	}

	return b
}
