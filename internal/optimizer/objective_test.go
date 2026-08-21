package optimizer

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/cwbudde/glockenspiel/internal/preset"
	"github.com/cwbudde/glockenspiel/internal/synth"
	"github.com/go-audio/audio"
	"github.com/go-audio/wav"
)

func TestComputeRMSErrorIdenticalSignals(t *testing.T) {
	signal := []float32{0.1, -0.2, 0.3, -0.4}

	got := ComputeRMSError(signal, signal)
	if got != 0 {
		t.Fatalf("expected zero RMS error, got %g", got)
	}
}

func TestComputeRMSErrorKnownDifference(t *testing.T) {
	a := []float32{0, 0}
	b := []float32{3, 4}

	got := ComputeRMSError(a, b)

	want := math.Sqrt((9.0 + 16.0) / 2.0)
	if math.Abs(got-want) > 1e-12 {
		t.Fatalf("unexpected RMS error: got %.12f want %.12f", got, want)
	}
}

func TestComputeRMSErrorMatchesGenericForLongInput(t *testing.T) {
	a := make([]float32, 257)
	b := make([]float32, 257)

	for i := range a {
		a[i] = float32(math.Sin(float64(i) * 0.13))
		b[i] = float32(math.Cos(float64(i) * 0.07))
	}

	got := ComputeRMSError(a, b)
	want := math.Sqrt(squaredDiffSumGeneric(a, b) / float64(len(a)))

	// The AVX2 kernel now widens each float32 difference to float64 before
	// squaring and accumulating, exactly like the generic path, so the only
	// remaining difference is summation order. A 1e-4 tolerance would have
	// accepted a kernel that was genuinely broken.
	if math.Abs(got-want) > 1e-12 {
		t.Fatalf("unexpected long-input RMS error: got %.15f want %.15f", got, want)
	}
}

func TestComputeLogErrorUsesFloor(t *testing.T) {
	signal := []float32{0.1, -0.2}

	got := ComputeLogError(signal, signal, 1e-12, 0)

	want := math.Log10(1e-12)
	if math.Abs(got-want) > 1e-12 {
		t.Fatalf("unexpected log error: got %.12f want %.12f", got, want)
	}
}

func TestObjectiveEvaluateMatchesReference(t *testing.T) {
	template := loadObjectivePreset(t)
	reference := renderReference(t, template, 44100, 69, 100, 0.1)

	objective, err := NewObjectiveFunction(reference, template, 44100, 69, 100, MetricRMS)
	if err != nil {
		t.Fatalf("NewObjectiveFunction failed: %v", err)
	}

	encoded, err := objective.Codec().EncodeParams(&template.Parameters)
	if err != nil {
		t.Fatalf("EncodeParams failed: %v", err)
	}

	got := objective.Evaluate(encoded)
	if got > 1e-8 {
		t.Fatalf("expected near-zero objective cost, got %.12f", got)
	}
}

// TestObjectiveEvaluateMatchesPCM16RoundTripReference checks that a reference
// which has been through a 16-bit file costs no more than the quantisation
// noise it picked up on the way.
//
// The reference has to be scaled into the PCM16 range first: the default preset
// renders with a peak around +15 dBFS, so writing it to a 16-bit file clips it
// beyond recognition. The objective therefore runs with gain normalisation, the
// mode intended for exactly this situation - a reference at an arbitrary level
// relative to what the model produces.
func TestObjectiveEvaluateMatchesPCM16RoundTripReference(t *testing.T) {
	template := loadObjectivePreset(t)
	reference := scaleToFullScale(renderReference(t, template, 44100, 69, 100, 0.1), 0.9)

	path := filepath.Join(t.TempDir(), "reference.wav")
	if err := writePCM16WAV(path, 44100, reference); err != nil {
		t.Fatalf("writePCM16WAV failed: %v", err)
	}

	loaded, sampleRate, err := loadPCM16WAV(path)
	if err != nil {
		t.Fatalf("loadPCM16WAV failed: %v", err)
	}

	if sampleRate != 44100 {
		t.Fatalf("unexpected sample rate: got %d want 44100", sampleRate)
	}

	config := DefaultObjectiveConfig(MetricRMS)
	config.Gain = GainLeastSquares

	objective, err := NewObjectiveFunctionWithConfig(loaded, template, 44100, 69, 100, config)
	if err != nil {
		t.Fatalf("NewObjectiveFunctionWithConfig failed: %v", err)
	}

	encoded, err := objective.Codec().EncodeParams(&template.Parameters)
	if err != nil {
		t.Fatalf("EncodeParams failed: %v", err)
	}

	got := objective.Evaluate(encoded)
	if got > 1e-4 {
		t.Fatalf("expected near-zero objective cost after PCM16 round-trip, got %.12f", got)
	}
}

// scaleToFullScale rescales samples so their peak sits at the given level.
func scaleToFullScale(samples []float32, peak float32) []float32 {
	highest := float32(0)

	for _, sample := range samples {
		if abs := float32(math.Abs(float64(sample))); abs > highest {
			highest = abs
		}
	}

	if highest == 0 {
		return samples
	}

	out := make([]float32, len(samples))
	for i, sample := range samples {
		out[i] = sample * peak / highest
	}

	return out
}

func TestObjectiveEvaluatePenalizesDifferentParams(t *testing.T) {
	template := loadObjectivePreset(t)
	reference := renderReference(t, template, 44100, 69, 100, 0.1)

	objective, err := NewObjectiveFunction(reference, template, 44100, 69, 100, MetricRMS)
	if err != nil {
		t.Fatalf("NewObjectiveFunction failed: %v", err)
	}

	modified := template.Parameters
	modified.InputMix = 0
	modified.Modes[0].Amplitude *= 0.5

	encoded, err := objective.Codec().EncodeParams(&modified)
	if err != nil {
		t.Fatalf("EncodeParams failed: %v", err)
	}

	got := objective.Evaluate(encoded)
	if got <= 1e-5 {
		t.Fatalf("expected modified parameters to increase cost, got %.12f", got)
	}
}

func TestNewObjectiveFunctionRejectsBadInput(t *testing.T) {
	template := loadObjectivePreset(t)

	if _, err := NewObjectiveFunction(nil, template, 44100, 69, 100, MetricRMS); err == nil {
		t.Fatal("expected empty reference to fail")
	}

	if _, err := NewObjectiveFunction([]float32{0}, template, 0, 69, 100, MetricRMS); err == nil {
		t.Fatal("expected invalid sample rate to fail")
	}

	if _, err := NewObjectiveFunction([]float32{0}, template, 44100, 69, 100, Metric("bad")); err == nil {
		t.Fatal("expected invalid metric to fail")
	}
}

// TestNewObjectiveFunctionRejectsShortSpectralReference replaces an assertion
// that used to require the opposite. A 512-sample reference is shorter than one
// STFT frame, so the spectral metric could produce nothing but +Inf; every
// candidate then scored the same and the optimizer wandered over a flat
// landscape with no error reported anywhere.
func TestNewObjectiveFunctionRejectsShortSpectralReference(t *testing.T) {
	template := loadObjectivePreset(t)
	reference := make([]float32, 512)
	reference[0] = 1

	if _, err := NewObjectiveFunction(reference, template, 44100, 69, 100, MetricSpectral); err == nil {
		t.Fatal("expected a reference shorter than one STFT frame to be rejected")
	}
}

func TestNewObjectiveFunctionAcceptsSpectralMetric(t *testing.T) {
	template := loadObjectivePreset(t)
	reference := renderReference(t, template, 44100, 69, 100, 0.5)

	if _, err := NewObjectiveFunction(reference, template, 44100, 69, 100, MetricSpectral); err != nil {
		t.Fatalf("expected spectral metric constructor to succeed, got %v", err)
	}
}

func renderReference(t *testing.T, p *preset.Preset, sampleRate, note, velocity int, duration float64) []float32 {
	t.Helper()

	engine, err := synth.NewSynthesizer(p, sampleRate)
	if err != nil {
		t.Fatalf("NewSynthesizer failed: %v", err)
	}

	return engine.RenderNote(note, velocity, duration)
}

func loadObjectivePreset(t *testing.T) *preset.Preset {
	t.Helper()

	p, err := preset.Load(filepath.FromSlash("../../assets/presets/default.json"))
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	return p
}

func writePCM16WAV(path string, sampleRate int, samples []float32) error {
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() {
		_ = file.Close()
	}()

	encoder := wav.NewEncoder(file, sampleRate, 16, 1, 1)

	intData := make([]int, len(samples))
	for i, sample := range samples {
		intData[i] = int(float32ToPCM16(sample))
	}

	buffer := &audio.IntBuffer{
		Format: &audio.Format{
			NumChannels: 1,
			SampleRate:  sampleRate,
		},
		SourceBitDepth: 16,
		Data:           intData,
	}
	if err := encoder.Write(buffer); err != nil {
		return err
	}

	return encoder.Close()
}

func loadPCM16WAV(path string) ([]float32, int, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer func() {
		_ = file.Close()
	}()

	decoder := wav.NewDecoder(file)

	intBuffer, err := decoder.FullPCMBuffer()
	if err != nil {
		return nil, 0, err
	}

	samples := make([]float32, len(intBuffer.Data))
	for i, sample := range intBuffer.Data {
		samples[i] = pcm16ToFloat32(int16(sample))
	}

	return samples, intBuffer.Format.SampleRate, nil
}

// TestObjectiveFitsReferenceWithLeadingSilence is the regression test for the
// missing time alignment. The reference is the exact render of the template,
// shifted by a deliberate offset. Without alignment the objective compares
// sample i of the candidate with sample i+offset of the reference; at the modal
// frequencies of this preset a handful of samples is already most of a period,
// so the *correct* parameters score worse than random ones.
func TestObjectiveFitsReferenceWithLeadingSilence(t *testing.T) {
	const (
		sampleRate = 44100
		offset     = 37
	)

	template := loadObjectivePreset(t)
	rendered := renderReference(t, template, sampleRate, 69, 100, 0.2)

	shifted := make([]float32, offset+len(rendered))
	copy(shifted[offset:], rendered)

	encodedFor := func(objective *ObjectiveFunction) []float64 {
		t.Helper()

		encoded, err := objective.Codec().EncodeParams(&template.Parameters)
		if err != nil {
			t.Fatalf("EncodeParams failed: %v", err)
		}

		return encoded
	}

	config := DefaultObjectiveConfig(MetricRMS)

	aligned, err := NewObjectiveFunctionWithConfig(shifted, template, sampleRate, 69, 100, config)
	if err != nil {
		t.Fatalf("NewObjectiveFunctionWithConfig failed: %v", err)
	}

	config.Alignment = AlignNone

	unaligned, err := NewObjectiveFunctionWithConfig(shifted, template, sampleRate, 69, 100, config)
	if err != nil {
		t.Fatalf("NewObjectiveFunctionWithConfig failed: %v", err)
	}

	alignedCost := aligned.Evaluate(encodedFor(aligned))
	unalignedCost := unaligned.Evaluate(encodedFor(unaligned))

	if alignedCost > 1e-6 {
		t.Fatalf("expected the shifted reference to be recovered exactly, got %.12f", alignedCost)
	}

	if !(unalignedCost > 100*alignedCost) {
		t.Fatalf("expected the unaligned comparison to be much worse: aligned=%g unaligned=%g", alignedCost, unalignedCost)
	}
}

func TestAlignmentPlanRecoversKnownLag(t *testing.T) {
	const sampleRate = 44100

	// Leading silence gives the onset detector something to lock onto in both
	// directions; a lag of -7 samples is already a full phase inversion at
	// 1756 Hz, which is the whole reason this has to be sample-accurate.
	reference := make([]float32, 9192)
	copy(reference[1000:], decayingSine(8192, sampleRate, 1756, 0.3))

	for _, lag := range []int{-500, -37, -7, 0, 7, 37, 500} {
		shifted := shiftSignal(reference, lag)

		plan := NewAlignmentPlan(reference, sampleRate, AlignOnsetCorrelation, GainNone, 0)

		got := plan.BestLag(shifted)
		if got != lag {
			t.Fatalf("expected lag %d, got %d", lag, got)
		}

		aligned, target := plan.Align(shifted, reference)
		if err := alignedRMSError(aligned, target, GainNone); err > 1e-6 {
			t.Fatalf("expected a clean fit after aligning lag %d, got %g", lag, err)
		}
	}
}

// shiftSignal returns signal delayed by lag samples (negative lag advances it),
// keeping the length constant.
func shiftSignal(signal []float32, lag int) []float32 {
	out := make([]float32, len(signal))

	for i := range out {
		src := i - lag
		if src >= 0 && src < len(signal) {
			out[i] = signal[src]
		}
	}

	return out
}

// TestObjectiveGainNormalizationMatchesLevel covers the missing level
// normalisation: a reference recorded hotter than the model can render pushes
// the fit into amplitude saturation before any modal structure is matched.
func TestObjectiveGainNormalizationMatchesLevel(t *testing.T) {
	const (
		sampleRate = 44100
		hotter     = 2.0 // +6 dB
	)

	template := loadObjectivePreset(t)
	rendered := renderReference(t, template, sampleRate, 69, 100, 0.2)

	hot := make([]float32, len(rendered))
	for i, sample := range rendered {
		hot[i] = sample * hotter
	}

	config := DefaultObjectiveConfig(MetricRMS)
	config.Gain = GainLeastSquares

	normalized, err := NewObjectiveFunctionWithConfig(hot, template, sampleRate, 69, 100, config)
	if err != nil {
		t.Fatalf("NewObjectiveFunctionWithConfig failed: %v", err)
	}

	config.Gain = GainNone

	plain, err := NewObjectiveFunctionWithConfig(hot, template, sampleRate, 69, 100, config)
	if err != nil {
		t.Fatalf("NewObjectiveFunctionWithConfig failed: %v", err)
	}

	encoded, err := normalized.Codec().EncodeParams(&template.Parameters)
	if err != nil {
		t.Fatalf("EncodeParams failed: %v", err)
	}

	normalizedCost := normalized.Evaluate(encoded)
	plainCost := plain.Evaluate(encoded)

	if normalizedCost > 1e-6 {
		t.Fatalf("expected gain normalisation to remove the level difference, got %.12f", normalizedCost)
	}

	if !(plainCost > 100*normalizedCost) {
		t.Fatalf("expected the un-normalised cost to be much worse: %g vs %g", plainCost, normalizedCost)
	}
}

func TestOptimalGainRecoversScalarFactor(t *testing.T) {
	base := decayingSine(4096, 44100, 1756, 0.3)

	scaled := make([]float32, len(base))
	for i, sample := range base {
		scaled[i] = sample * 0.25
	}

	got := OptimalGain(base, scaled)
	if math.Abs(got-0.25) > 1e-6 {
		t.Fatalf("unexpected optimal gain: got %g want 0.25", got)
	}
}

// TestPCM16RoundTripIsIdentity pins the scale fix: the encoder used to multiply
// by 32767 while the decoder divided by 32768, so the round trip applied a
// silent 0.99997x gain instead of being a pure quantisation.
func TestPCM16RoundTripIsIdentity(t *testing.T) {
	for _, value := range []float32{-1, -0.5, -1e-4, 0, 1e-4, 0.5, 1} {
		got := pcm16ToFloat32(float32ToPCM16(value))
		if math.Abs(float64(got-value)) > 0.5/pcm16Scale {
			t.Fatalf("round trip of %g drifted to %g", value, got)
		}
	}

	if got := pcm16ToFloat32(float32ToPCM16(1)); got != 1 {
		t.Fatalf("full scale must survive the round trip exactly, got %g", got)
	}

	samples := []float32{0.123456, -0.98765, 0.000001}

	once := append([]float32(nil), samples...)
	ProjectToPCM16Domain(once)

	twice := append([]float32(nil), once...)
	ProjectToPCM16Domain(twice)

	for i := range once {
		if once[i] != twice[i] {
			t.Fatalf("quantisation is not idempotent at %d: %g vs %g", i, once[i], twice[i])
		}
	}
}

// TestObjectiveKeepsReferencePrecision guards against re-introducing the 16-bit
// projection of the reference, which silently discarded eight bits of a 24-bit
// recording.
func TestObjectiveKeepsReferencePrecision(t *testing.T) {
	template := loadObjectivePreset(t)
	reference := renderReference(t, template, 44100, 69, 100, 0.1)

	// A value that is deliberately not on the 16-bit grid.
	reference[10] = 0.5 + 0.25/pcm16Scale

	objective, err := NewObjectiveFunction(reference, template, 44100, 69, 100, MetricRMS)
	if err != nil {
		t.Fatalf("NewObjectiveFunction failed: %v", err)
	}

	if objective.reference[10] != reference[10] {
		t.Fatalf("reference was quantised: got %v want %v", objective.reference[10], reference[10])
	}
}

func BenchmarkAlignmentPlanBestLag(b *testing.B) {
	const (
		sampleRate  = 44100
		sampleCount = 2 * 44100
	)

	reference := decayingSine(sampleCount, sampleRate, 2093, 0.4)
	candidate := shiftSignal(reference, 311)
	plan := NewAlignmentPlan(reference, sampleRate, AlignOnsetCorrelation, GainNone, 0)

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		if plan.BestLag(candidate) != 311 {
			b.Fatal("unexpected lag")
		}
	}
}

// TestSquaredDiffSumMatchesGenericAcrossLengths sweeps the dispatch threshold
// and the vector tail so a broken kernel cannot hide in an untested length.
func TestSquaredDiffSumMatchesGenericAcrossLengths(t *testing.T) {
	for length := 0; length <= 200; length++ {
		a := make([]float32, length)
		b := make([]float32, length)

		for i := range a {
			a[i] = float32(math.Sin(float64(i) * 0.13))
			b[i] = float32(math.Cos(float64(i) * 0.07))
		}

		got := squaredDiffSum(a, b)

		want := squaredDiffSumGeneric(a, b)
		if math.Abs(got-want) > 1e-12*math.Max(1, math.Abs(want)) {
			t.Fatalf("length %d: got %.15f want %.15f", length, got, want)
		}
	}
}

func BenchmarkSquaredDiffSum(b *testing.B) {
	for _, length := range []int{8, 16, 32, 64, 4096} {
		left := make([]float32, length)
		right := make([]float32, length)

		for i := range left {
			left[i] = float32(math.Sin(float64(i) * 0.13))
			right[i] = float32(math.Cos(float64(i) * 0.07))
		}

		b.Run(fmt.Sprintf("dispatch-%d", length), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				_ = squaredDiffSum(left, right)
			}
		})

		b.Run(fmt.Sprintf("generic-%d", length), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				_ = squaredDiffSumGeneric(left, right)
			}
		})
	}
}
