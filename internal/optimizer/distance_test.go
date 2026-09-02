package optimizer

import (
	"encoding/json"
	"math"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cwbudde/algo-glockenspiel/internal/preset"
	"github.com/cwbudde/algo-glockenspiel/internal/synth"
)

// distanceFixture renders a short reference from the minimal preset so that
// the preset scores against its own render.
func distanceFixture(t *testing.T) (*preset.Preset, []float32) {
	t.Helper()

	template, err := preset.Load(filepath.FromSlash("../../testdata/presets/minimal.json"))
	if err != nil {
		t.Fatalf("load preset: %v", err)
	}

	engine, err := synth.NewSynthesizer(template, 44100)
	if err != nil {
		t.Fatalf("new synthesizer: %v", err)
	}

	// Long enough for the spectral metric, short enough to keep the test fast.
	reference := engine.RenderNote(69, 100, 0.1)

	return template, reference
}

func TestMeasureMatchesEvaluateUnderEveryPolicy(t *testing.T) {
	template, reference := distanceFixture(t)

	// Score a candidate that is not the reference itself, so every term is
	// non-trivial and a mismatch has something to show.
	candidate := template.Clone()
	candidate.Parameters.Modes[0].Frequency *= 1.01
	candidate.Parameters.Modes[0].DecayMs *= 0.8

	for _, policy := range []struct {
		name      string
		alignment AlignmentMode
		gain      GainMode
	}{
		{"raw", AlignNone, GainNone},
		{"aligned", AlignOnsetCorrelation, GainNone},
		{"aligned+gain", AlignOnsetCorrelation, GainLeastSquares},
	} {
		for _, metric := range []Metric{MetricRMS, MetricLog, MetricSpectral} {
			t.Run(policy.name+"/"+string(metric), func(t *testing.T) {
				config := DefaultObjectiveConfig(metric)
				config.Alignment = policy.alignment
				config.Gain = policy.gain

				objective, err := NewObjectiveFunctionWithConfig(reference, template, 44100, 69, 100, config)
				if err != nil {
					t.Fatalf("new objective: %v", err)
				}

				encoded, err := objective.Codec().EncodeParams(&candidate.Parameters)
				if err != nil {
					t.Fatalf("encode: %v", err)
				}

				measurement, err := objective.Measure(encoded)
				if err != nil {
					t.Fatalf("measure: %v", err)
				}

				var got float64

				switch metric {
				case MetricRMS:
					got = measurement.RMS
				case MetricLog:
					got = measurement.Log
				case MetricSpectral:
					got = measurement.Spectral
				}

				if want := objective.Evaluate(encoded); got != want {
					t.Fatalf("Measure.%s = %.17g, Evaluate = %.17g", metric, got, want)
				}

				if measurement.GainApplied != (policy.gain == GainLeastSquares) {
					t.Fatalf("GainApplied = %v under %s", measurement.GainApplied, policy.name)
				}

				if policy.alignment == AlignNone && measurement.Lag != 0 {
					t.Fatalf("raw policy reported lag %d", measurement.Lag)
				}
			})
		}
	}
}

func TestDistanceOfAPresetAgainstItsOwnRenderIsZero(t *testing.T) {
	template, reference := distanceFixture(t)

	report, err := Distance(reference, template, DistanceConfig{SampleRate: 44100, Note: 69, Velocity: 100})
	if err != nil {
		t.Fatalf("distance: %v", err)
	}

	for name, measurement := range map[string]Measurement{
		"raw": report.Raw, "aligned": report.Aligned, "aligned_gain": report.AlignedGain,
	} {
		if measurement.RMS != 0 {
			t.Fatalf("%s: rms %g, want exactly 0 for a self-render", name, measurement.RMS)
		}

		if measurement.Lag != 0 {
			t.Fatalf("%s: lag %d, want 0", name, measurement.Lag)
		}

		if math.Abs(measurement.Gain-1) > 1e-6 {
			t.Fatalf("%s: gain %g, want 1", name, measurement.Gain)
		}

		if measurement.Overlap != len(reference) {
			t.Fatalf("%s: overlap %d, want %d", name, measurement.Overlap, len(reference))
		}
	}

	if report.Reference != report.Render {
		t.Fatalf("reference level %+v differs from render level %+v", report.Reference, report.Render)
	}

	if report.Modes != 4 || report.Dimension != 14 {
		t.Fatalf("modes %d dimension %d, want 4 and 14", report.Modes, report.Dimension)
	}

	if report.Clamped {
		t.Fatal("a preset inside the default box was reported clamped")
	}

	if len(report.Widened) != 0 {
		t.Fatalf("minimal preset widened the default box: %+v", report.Widened)
	}

	// minimal.json sits on input_mix's lower bound and three amplitudes at
	// zero, which is inside the amplitude range; only input_mix is pinned.
	if len(report.Pinned) != 1 || report.Pinned[0].Name != "input_mix" || report.Pinned[0].Bound != "min" {
		t.Fatalf("pinned = %+v, want input_mix at min", report.Pinned)
	}
}

func TestDistanceReportsWidenedBoundsAndPinnedDimensions(t *testing.T) {
	template, reference := distanceFixture(t)

	// Push a mode to 25 kHz, past the 20 kHz default box, and an amplitude
	// onto its bound. The pushed mode is written second but is the highest,
	// so the codec names it by the index it takes once sorted: the last.
	written := template.Clone()
	written.Parameters.Modes[1].Frequency = 25000
	written.Parameters.Modes[1].Amplitude = 2

	report, err := Distance(reference, written, DistanceConfig{SampleRate: 44100, Note: 69, Velocity: 100})
	if err != nil {
		t.Fatalf("distance: %v", err)
	}

	if len(report.Widened) != 1 || report.Widened[0].Name != "frequency" || report.Widened[0].Side != "max" || report.Widened[0].To != 25000 {
		t.Fatalf("widened = %+v, want frequency max widened to 25000", report.Widened)
	}

	names := make([]string, 0, len(report.Pinned))
	for _, pinned := range report.Pinned {
		names = append(names, pinned.Name+"@"+pinned.Bound)
	}

	joined := strings.Join(names, ",")

	for _, want := range []string{"modes[3].amplitude@max", "modes[3].frequency@max", "input_mix@min"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("pinned %q lacks %s", joined, want)
		}
	}

	for _, pinned := range report.Pinned {
		if pinned.Name == "modes[3].frequency" && math.Abs(pinned.Limit-25000) > 1e-6 {
			t.Fatalf("frequency limit reported in encoded units: %+v", pinned)
		}
	}
}

func TestDistanceStrictBoundsClampAndReportIt(t *testing.T) {
	template, reference := distanceFixture(t)

	written := template.Clone()
	written.Parameters.Modes[1].Frequency = 25000

	report, err := Distance(reference, written, DistanceConfig{SampleRate: 44100, Note: 69, Velocity: 100, StrictBounds: true})
	if err != nil {
		t.Fatalf("distance: %v", err)
	}

	if !report.Clamped {
		t.Fatal("a preset outside a strict box was not reported clamped")
	}

	if len(report.Widened) != 0 {
		t.Fatalf("strict bounds were widened: %+v", report.Widened)
	}
}

func TestMeasurementJSONWritesNonFiniteAsNull(t *testing.T) {
	data, err := json.Marshal(Measurement{RMS: 0.5, Log: -0.3, Spectral: math.Inf(1), Gain: 1})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	if got := string(data); !strings.Contains(got, `"spectral":null`) || !strings.Contains(got, `"rms":0.5`) {
		t.Fatalf("unexpected JSON: %s", got)
	}

	levels, err := json.Marshal(MeasureLevel(nil))
	if err != nil {
		t.Fatalf("marshal levels: %v", err)
	}

	if got := string(levels); got != `{"peak_dbfs":null,"rms_dbfs":null}` {
		t.Fatalf("unexpected silent level JSON: %s", got)
	}
}

func TestMeasureLevel(t *testing.T) {
	got := MeasureLevel([]float32{0.5, -0.5, 0.5, -0.5})

	if math.Abs(got.PeakDBFS-(-6.0206)) > 1e-3 || math.Abs(got.RMSDBFS-(-6.0206)) > 1e-3 {
		t.Fatalf("level = %+v, want -6.02 dBFS for both", got)
	}
}

func TestDimensionNamesCoverEveryDimension(t *testing.T) {
	template, _ := distanceFixture(t)

	codec, err := NewParamCodec(&template.Parameters)
	if err != nil {
		t.Fatalf("codec: %v", err)
	}

	names := codec.DimensionNames()
	if len(names) != codec.Dimension() {
		t.Fatalf("%d names for %d dimensions", len(names), codec.Dimension())
	}

	if names[0] != "input_mix" || names[2] != "modes[0].amplitude" || names[3] != "modes[0].frequency" || names[4] != "modes[0].decay_ms" {
		t.Fatalf("unexpected names: %v", names)
	}

	for i := range names {
		want := i == 1 || (i >= 2 && (i-2)%3 != 0)
		if codec.isLogDimension(i) != want {
			t.Fatalf("dimension %d (%s) log = %v, want %v", i, names[i], codec.isLogDimension(i), want)
		}
	}
}
