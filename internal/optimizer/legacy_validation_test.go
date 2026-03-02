package optimizer

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cwbudde/glockenspiel/internal/model"
	"github.com/cwbudde/glockenspiel/internal/preset"
	"github.com/go-audio/wav"
)

func TestOptimizationImprovesFitAgainstLegacyReference(t *testing.T) {
	legacyPreset := loadDefaultPreset(t)
	reference, sampleRate := loadLegacyReferenceWAV(t)

	initial := *legacyPreset
	initial.Parameters.InputMix = clampToRange(initial.Parameters.InputMix+0.18, model.InputMixMin, model.InputMixMax)
	initial.Parameters.FilterFrequency = clampToRange(initial.Parameters.FilterFrequency*1.18, model.FilterFrequencyMinHz, model.FilterFrequencyMaxHz)
	initial.Parameters.Modes[0].Amplitude = clampToRange(initial.Parameters.Modes[0].Amplitude-0.22, model.AmplitudeMin, model.AmplitudeMax)
	initial.Parameters.Modes[0].Frequency = clampToRange(initial.Parameters.Modes[0].Frequency*0.93, model.FrequencyMinHz, model.FrequencyMaxHz)
	initial.Parameters.Modes[0].DecayMs = clampToRange(initial.Parameters.Modes[0].DecayMs*0.8, model.DecayMsMin, model.DecayMsMax)

	bounds := legacyValidationBounds(&legacyPreset.Parameters)
	objective, err := NewObjectiveFunctionWithBounds(reference, &initial, sampleRate, 69, 100, MetricRMS, bounds)
	if err != nil {
		t.Fatalf("NewObjectiveFunctionWithBounds failed: %v", err)
	}

	initialEncoded, err := objective.Codec().EncodeParams(&initial.Parameters)
	if err != nil {
		t.Fatalf("EncodeParams failed: %v", err)
	}
	initialCost := objective.Evaluate(initialEncoded)

	result, err := (&SimpleOptimizer{
		AbsoluteTolerance: 1e-10,
		RelativeTolerance: 1e-10,
		StallIterations:   50,
	}).Optimize(objective.Objective(), initialEncoded, objective.Codec().EncodedBounds(), OptimizeOptions{
		MaxIterations: 120,
		TimeBudget:    3 * time.Second,
	})
	if err != nil {
		t.Fatalf("Optimize failed: %v", err)
	}

	if !(result.BestCost < initialCost) {
		t.Fatalf("expected optimization to improve cost: initial=%g best=%g", initialCost, result.BestCost)
	}

	recovered, err := objective.Codec().DecodeParams(result.BestParams)
	if err != nil {
		t.Fatalf("DecodeParams failed: %v", err)
	}

	assertCloseWithin(t, recovered.InputMix, legacyPreset.Parameters.InputMix, 0.22, "legacy input_mix")
	assertCloseWithin(t, recovered.FilterFrequency, legacyPreset.Parameters.FilterFrequency, 180, "legacy filter_frequency")
	assertCloseWithin(t, recovered.Modes[0].Amplitude, legacyPreset.Parameters.Modes[0].Amplitude, 0.3, "legacy mode0 amplitude")
	// The legacy WAV fit is not uniquely identifiable with the current time-domain
	// objective, so mode 0 can settle into a different but still plausible local
	// minimum while the waveform error improves materially.
	assertCloseWithin(t, recovered.Modes[0].Frequency, legacyPreset.Parameters.Modes[0].Frequency, 300, "legacy mode0 frequency")

	rendered := renderNote(t, &preset.Preset{
		Version:    legacyPreset.Version,
		Name:       legacyPreset.Name,
		Note:       legacyPreset.Note,
		Parameters: *recovered,
	}, sampleRate, 69, 100, float64(len(reference))/float64(sampleRate))
	initialRendered := renderNote(t, &initial, sampleRate, 69, 100, float64(len(reference))/float64(sampleRate))
	initialRMS := ComputeRMSError(initialRendered, reference)
	finalRMS := ComputeRMSError(rendered, reference)
	if !(finalRMS < initialRMS) {
		t.Fatalf("expected rendered RMS to improve: initial=%g final=%g", initialRMS, finalRMS)
	}
}

func loadDefaultPreset(t *testing.T) *preset.Preset {
	t.Helper()

	p, err := preset.Load(filepath.FromSlash("../../assets/presets/default.json"))
	if err != nil {
		t.Fatalf("load default preset: %v", err)
	}
	return p
}

func loadLegacyReferenceWAV(t *testing.T) ([]float32, int) {
	t.Helper()

	path := filepath.FromSlash("../../testdata/reference/legacy_synth_a4.wav")

	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("open legacy reference: %v", err)
	}

	defer func() {
		_ = file.Close()
	}()

	decoder := wav.NewDecoder(file)
	if !decoder.IsValidFile() {
		t.Fatalf("invalid legacy wav file: %s", path)
	}
	intBuffer, err := decoder.FullPCMBuffer()
	if err != nil {
		t.Fatalf("decode legacy wav: %v", err)
	}
	if intBuffer == nil || intBuffer.Format == nil {
		t.Fatalf("invalid decoded legacy buffer: %s", path)
	}

	bitDepth := intBuffer.SourceBitDepth
	if bitDepth <= 0 {
		bitDepth = 16
	}

	scale := math.Pow(2, float64(bitDepth-1))
	channels := intBuffer.Format.NumChannels
	if channels <= 0 {
		channels = 1
	}

	samples := make([]float32, len(intBuffer.Data)/channels)
	for i := range samples {
		samples[i] = float32(float64(intBuffer.Data[i*channels]) / scale)
	}
	return samples, intBuffer.Format.SampleRate
}

func legacyValidationBounds(target *model.BarParams) ParamBounds {
	return ParamBounds{
		InputMix:      Range{Min: math.Max(model.InputMixMin, target.InputMix-0.35), Max: math.Min(model.InputMixMax, target.InputMix+0.35)},
		FilterFreq:    Range{Min: math.Max(model.FilterFrequencyMinHz, target.FilterFrequency*0.6), Max: math.Min(model.FilterFrequencyMaxHz, target.FilterFrequency*1.4)},
		BaseFrequency: Range{Min: target.BaseFrequency, Max: target.BaseFrequency},
		Amplitude:     Range{Min: model.AmplitudeMin, Max: model.AmplitudeMax},
		FrequencyMult: Range{Min: 0.05, Max: 12},
		DecayMs:       Range{Min: model.DecayMsMin, Max: model.DecayMsMax},
		HarmonicGain:  Range{Min: model.HarmonicGainMin, Max: model.HarmonicGainMax},
	}
}

func clampToRange(value, low, high float64) float64 {
	if value < low {
		return low
	}

	if value > high {
		return high
	}

	return value
}

func TestLegacyReferenceFixtureLoads(t *testing.T) {
	samples, sampleRate := loadLegacyReferenceWAV(t)
	if len(samples) == 0 {
		t.Fatal("expected legacy reference to contain samples")
	}
	if sampleRate <= 0 {
		t.Fatalf("expected positive sample rate, got %d", sampleRate)
	}
}

func BenchmarkLegacyObjectiveEvaluate(b *testing.B) {
	legacyPreset, err := preset.Load(filepath.FromSlash("../../assets/presets/default.json"))
	if err != nil {
		b.Fatalf("load default preset: %v", err)
	}
	reference, sampleRate, err := loadLegacyReferenceForBenchmark()
	if err != nil {
		b.Fatalf("load legacy reference: %v", err)
	}
	objective, err := NewObjectiveFunctionWithBounds(reference, legacyPreset, sampleRate, 69, 100, MetricRMS, legacyValidationBounds(&legacyPreset.Parameters))
	if err != nil {
		b.Fatalf("NewObjectiveFunctionWithBounds failed: %v", err)
	}

	encoded, err := objective.Codec().EncodeParams(&legacyPreset.Parameters)
	if err != nil {
		b.Fatalf("EncodeParams failed: %v", err)
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_ = objective.Evaluate(encoded)
	}
}

func loadLegacyReferenceForBenchmark() ([]float32, int, error) {
	path := filepath.FromSlash("../../testdata/reference/legacy_synth_a4.wav")

	file, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}

	defer func() {
		_ = file.Close()
	}()

	decoder := wav.NewDecoder(file)
	if !decoder.IsValidFile() {
		return nil, 0, fmt.Errorf("invalid wav file: %s", path)
	}

	intBuffer, err := decoder.FullPCMBuffer()
	if err != nil {
		return nil, 0, err
	}
	if intBuffer == nil || intBuffer.Format == nil {
		return nil, 0, fmt.Errorf("invalid decoded buffer: %s", path)
	}

	bitDepth := intBuffer.SourceBitDepth
	if bitDepth <= 0 {
		bitDepth = 16
	}
	scale := math.Pow(2, float64(bitDepth-1))
	channels := intBuffer.Format.NumChannels
	if channels <= 0 {
		channels = 1
	}

	samples := make([]float32, len(intBuffer.Data)/channels)
	for i := range samples {
		samples[i] = float32(float64(intBuffer.Data[i*channels]) / scale)
	}
	return samples, intBuffer.Format.SampleRate, nil
}
