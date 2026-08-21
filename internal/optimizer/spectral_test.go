package optimizer

import (
	"math"
	"testing"
)

// decayingSine renders an exponentially decaying sinusoid, the simplest stand-in
// for a single mode of the modal model.
func decayingSine(sampleCount, sampleRate int, freqHz, decaySeconds float64) []float32 {
	out := make([]float32, sampleCount)
	for i := range out {
		seconds := float64(i) / float64(sampleRate)
		out[i] = float32(math.Exp(-seconds/decaySeconds) * math.Sin(2*math.Pi*freqHz*seconds))
	}

	return out
}

func TestComputeSpectralErrorIdenticalSignals(t *testing.T) {
	signal := decayingSine(8192, 44100, 2093, 0.4)

	got := ComputeSpectralError(signal, signal, 44100)
	if got != 0 {
		t.Fatalf("expected zero spectral error, got %g", got)
	}
}

func TestComputeSpectralErrorDetectsDifference(t *testing.T) {
	a := decayingSine(8192, 44100, 2093, 0.4)
	b := decayingSine(8192, 44100, 2349, 0.4)

	got := ComputeSpectralError(a, b, 44100)
	if !(got > 0) {
		t.Fatalf("expected positive spectral error, got %g", got)
	}
}

// TestComputeSpectralErrorSeesBeyondTheFirstFrames pins the multi-frame STFT.
// The previous implementation capped the analysis at the first 4096 samples, so
// two signals that agree over that head and diverge completely afterwards
// scored exactly zero.
func TestComputeSpectralErrorSeesBeyondTheFirstFrames(t *testing.T) {
	const (
		sampleRate  = 44100
		sampleCount = 44100
		head        = 4096
	)

	reference := decayingSine(sampleCount, sampleRate, 2093, 0.5)

	truncated := append([]float32(nil), reference...)
	for i := head; i < len(truncated); i++ {
		truncated[i] = 0
	}

	if head >= sampleCount {
		t.Fatalf("test needs a signal longer than the old %d-sample cap", head)
	}

	got := ComputeSpectralError(truncated, reference, sampleRate)
	if !(got > 1) {
		t.Fatalf("expected the tail difference to be scored, got %g dB", got)
	}
}

// TestComputeSpectralErrorRespondsToDecayTime is the property that makes
// --metric spectral usable for fitting DecayMs at all: the cost must fall
// monotonically as the candidate decay approaches the reference decay.
func TestComputeSpectralErrorRespondsToDecayTime(t *testing.T) {
	const (
		sampleRate  = 44100
		sampleCount = 2 * 44100
		freqHz      = 2093.0
		refDecay    = 0.40
	)

	reference := decayingSine(sampleCount, sampleRate, freqHz, refDecay)

	exact := ComputeSpectralError(decayingSine(sampleCount, sampleRate, freqHz, refDecay), reference, sampleRate)
	near := ComputeSpectralError(decayingSine(sampleCount, sampleRate, freqHz, 0.36), reference, sampleRate)
	far := ComputeSpectralError(decayingSine(sampleCount, sampleRate, freqHz, 0.20), reference, sampleRate)

	if exact != 0 {
		t.Fatalf("expected the matching decay to score zero, got %g", exact)
	}

	if !(near < far) {
		t.Fatalf("expected cost to grow with decay mismatch: near=%g far=%g", near, far)
	}

	if !(near > 0) {
		t.Fatalf("expected a small decay mismatch to be visible, got %g", near)
	}
}

func TestSpectralBinWeightFavoursTheInstrumentBand(t *testing.T) {
	rumble := spectralBinWeight(150)
	fundamental := spectralBinWeight(2093)
	partial := spectralBinWeight(7000)
	ultrasonic := spectralBinWeight(19000)

	if !(rumble < fundamental) {
		t.Fatalf("sub-fundamental rumble must not outweigh the fundamental: %g vs %g", rumble, fundamental)
	}

	if fundamental != 1 || partial != 1 {
		t.Fatalf("the instrument band should be flat at 1: %g %g", fundamental, partial)
	}

	if !(ultrasonic < partial) {
		t.Fatalf("expected a roll-off above the partial band: %g vs %g", ultrasonic, partial)
	}
}

func TestSpectralBinWeightIsContinuous(t *testing.T) {
	// A step in the weighting puts a kink in the cost landscape; sample densely
	// across the corners and require small increments.
	previous := spectralBinWeight(10)

	for freqHz := 20.0; freqHz < 22050; freqHz *= 1.001 {
		current := spectralBinWeight(freqHz)
		if math.Abs(current-previous) > 0.01 {
			t.Fatalf("weight jumps at %.1f Hz: %g -> %g", freqHz, previous, current)
		}

		previous = current
	}
}

func TestValidateSpectralInputRejectsShortSignals(t *testing.T) {
	if err := ValidateSpectralInput(SpectralMinSamples()-1, 44100); err == nil {
		t.Fatal("expected a short signal to be rejected")
	}

	if err := ValidateSpectralInput(SpectralMinSamples(), 0); err == nil {
		t.Fatal("expected a zero sample rate to be rejected")
	}

	if err := ValidateSpectralInput(SpectralMinSamples(), 44100); err != nil {
		t.Fatalf("expected a full frame to be accepted, got %v", err)
	}
}

func TestComputeSpectralErrorRejectsShortSignals(t *testing.T) {
	short := make([]float32, SpectralMinSamples()-1)

	got := ComputeSpectralError(short, short, 44100)
	if !math.IsInf(got, 1) {
		t.Fatalf("expected +Inf for an unanalysable signal, got %g", got)
	}
}

func TestParseMetricSupportsSpectral(t *testing.T) {
	got, err := ParseMetric("spectral")
	if err != nil {
		t.Fatalf("ParseMetric failed: %v", err)
	}

	if got != MetricSpectral {
		t.Fatalf("unexpected metric: got %q", got)
	}
}

func BenchmarkComputeSpectralErrorTwoSeconds(b *testing.B) {
	const sampleCount = 2 * 44100

	reference := decayingSine(sampleCount, 44100, 2093, 0.4)
	candidate := decayingSine(sampleCount, 44100, 2100, 0.4)

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_ = ComputeSpectralError(candidate, reference, 44100)
	}
}
