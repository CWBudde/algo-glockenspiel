package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cwbudde/algo-glockenspiel/internal/optimizer"
	"github.com/cwbudde/algo-glockenspiel/internal/preset"
	"github.com/cwbudde/algo-glockenspiel/internal/synth"
	"github.com/cwbudde/algo-glockenspiel/internal/wavio"
	"github.com/spf13/cobra"
)

// writeSelfReference renders the minimal preset to a WAV and returns its path,
// so the preset can be scored against its own render.
func writeSelfReference(t *testing.T, seconds float64) (string, string) {
	t.Helper()

	presetPath := filepath.FromSlash("../../testdata/presets/minimal.json")

	p, err := preset.Load(presetPath)
	if err != nil {
		t.Fatalf("load preset: %v", err)
	}

	engine, err := synth.NewSynthesizer(p, 44100)
	if err != nil {
		t.Fatalf("new synthesizer: %v", err)
	}

	referencePath := filepath.Join(t.TempDir(), "reference.wav")
	if err := wavio.WriteMono(referencePath, 44100, engine.RenderNote(69, 100, seconds)); err != nil {
		t.Fatalf("write reference wav: %v", err)
	}

	return referencePath, presetPath
}

func TestRunDistancePrintsEveryPolicy(t *testing.T) {
	referencePath, presetPath := writeSelfReference(t, 0.1)

	var out bytes.Buffer

	cmd := &cobra.Command{}
	cmd.SetOut(&out)
	cmd.SetErr(io.Discard)

	err := runDistance(cmd, distanceOptions{
		referencePath: referencePath,
		presetPath:    presetPath,
		note:          69,
		velocity:      100,
		sampleRate:    44100,
	})
	if err != nil {
		t.Fatalf("runDistance failed: %v", err)
	}

	got := out.String()

	for _, want := range []string{
		"reference " + referencePath,
		"preset Minimal Valid Preset (" + presetPath + ")",
		"4 modes, 14 dimensions",
		"\nraw ", "\naligned ", "\naligned+gain ",
		"pinned: 1 of 14 dimensions on a bound",
		"input_mix = 0 (min 0)",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("output lacks %q:\n%s", want, got)
		}
	}

	if strings.Contains(got, "widened") || strings.Contains(got, "clamped") {
		t.Fatalf("a preset inside the default box reported a moved or clamped box:\n%s", got)
	}
}

func TestRunDistanceJSONScoresSelfRenderAtZero(t *testing.T) {
	referencePath, presetPath := writeSelfReference(t, 0.1)

	var out bytes.Buffer

	cmd := &cobra.Command{}
	cmd.SetOut(&out)
	cmd.SetErr(io.Discard)

	// The loader cuts the reference at its onset, which for a render is a few
	// samples into the filtered impulse, so the render aligns to it at a small
	// positive lag; the level is kept so that the aligned row without gain
	// can read zero.
	err := runDistance(cmd, distanceOptions{
		referencePath: referencePath,
		presetPath:    presetPath,
		note:          69,
		velocity:      100,
		sampleRate:    44100,
		jsonOutput:    true,
		reference:     referenceOptions{keepLevel: true},
	})
	if err != nil {
		t.Fatalf("runDistance failed: %v", err)
	}

	var report optimizer.DistanceReport
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatalf("decode report: %v\n%s", err, out.String())
	}

	// The WAV round trip quantises the reference to 16 bits, so the render
	// differs from it by at most half a step; the aligned score must still be
	// a small fraction of that step.
	if report.Aligned.RMS > 1.0/65536 || report.Aligned.Lag < 0 || report.Aligned.Lag > 8 {
		t.Fatalf("self render scored rms %g at lag %d", report.Aligned.RMS, report.Aligned.Lag)
	}

	if report.Raw.RMS < report.Aligned.RMS {
		t.Fatalf("raw %g scores better than aligned %g", report.Raw.RMS, report.Aligned.RMS)
	}

	if report.Metrics.Waveform > 0.01 || report.Scores[string(optimizer.MetricBalanced)] > 0.05 {
		t.Fatalf("the composite terms do not read a self render as its own: %+v, scores %v", report.Metrics, report.Scores)
	}

	if report.AlignedGain.RMS > report.Aligned.RMS {
		t.Fatalf("dividing out the optimal gain raised the error: %g > %g", report.AlignedGain.RMS, report.Aligned.RMS)
	}

	if report.Aligned.Spectral == 0 && report.ReferenceSamples >= optimizer.SpectralMinSamples() {
		// A quantised reference is not identical, so an exactly zero spectral
		// term would mean the term was not computed.
		t.Fatal("spectral term is exactly zero against a quantised reference")
	}
}

func TestRunDistanceShortReferenceReportsSpectralAsUnavailable(t *testing.T) {
	// 0.02 s is 882 samples, under the spectral metric's 2048-sample frame,
	// which must not turn the whole report into an error.
	referencePath, presetPath := writeSelfReference(t, 0.02)

	var out bytes.Buffer

	cmd := &cobra.Command{}
	cmd.SetOut(&out)
	cmd.SetErr(io.Discard)

	err := runDistance(cmd, distanceOptions{
		referencePath: referencePath,
		presetPath:    presetPath,
		note:          69,
		velocity:      100,
		sampleRate:    44100,
	})
	if err != nil {
		t.Fatalf("runDistance failed: %v", err)
	}

	if got := out.String(); !strings.Contains(got, "n/a") {
		t.Fatalf("expected the spectral column to read n/a:\n%s", got)
	}
}

func TestRunDistanceRejectsSampleRateMismatch(t *testing.T) {
	referencePath, presetPath := writeSelfReference(t, 0.05)

	cmd := &cobra.Command{}
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)

	err := runDistance(cmd, distanceOptions{
		referencePath: referencePath,
		presetPath:    presetPath,
		note:          69,
		velocity:      100,
		sampleRate:    48000,
	})
	if err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("expected a sample rate mismatch error, got %v", err)
	}
}

func TestRunDistanceStrictBoundsReportClamping(t *testing.T) {
	referencePath, presetPath := writeSelfReference(t, 0.05)

	// minimal.json carries a 100 ms decay; a box that stops at 50 ms cannot
	// contain it, and the report has to say so rather than widen.
	boundsPath := filepath.Join(t.TempDir(), "bounds.json")
	if err := os.WriteFile(boundsPath, []byte(`{"decay_ms": [10.0, 50.0]}`), 0o644); err != nil {
		t.Fatalf("write bounds: %v", err)
	}

	var out bytes.Buffer

	cmd := &cobra.Command{}
	cmd.SetOut(&out)
	cmd.SetErr(io.Discard)

	err := runDistance(cmd, distanceOptions{
		referencePath: referencePath,
		presetPath:    presetPath,
		boundsPath:    boundsPath,
		note:          69,
		velocity:      100,
		sampleRate:    44100,
	})
	if err != nil {
		t.Fatalf("runDistance failed: %v", err)
	}

	if got := out.String(); !strings.Contains(got, "clamped preset") {
		t.Fatalf("expected the clamping warning:\n%s", got)
	}
}

func TestDistanceCommandIsRegistered(t *testing.T) {
	var stdout, stderr bytes.Buffer

	if code := Run([]string{"distance", "--help"}, &stdout, &stderr); code != 0 {
		t.Fatalf("distance --help exited %d: %s", code, stderr.String())
	}

	if !strings.Contains(stdout.String(), "--json") {
		t.Fatalf("help does not list --json:\n%s", stdout.String())
	}

	if code := Run([]string{"distance"}, &stdout, &stderr); code != 1 || !strings.Contains(stderr.String(), "required flag") {
		t.Fatalf("distance without --reference exited %d: %s", code, stderr.String())
	}
}
