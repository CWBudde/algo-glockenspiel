package optimizer

import (
	"math"
	"path/filepath"
	"testing"

	"github.com/cwbudde/glockenspiel/internal/preset"
	"github.com/cwbudde/glockenspiel/internal/synth"
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
