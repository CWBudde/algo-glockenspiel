package browserfit_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cwbudde/algo-glockenspiel/internal/browserfit"
	"github.com/cwbudde/algo-glockenspiel/internal/preset"
	"github.com/cwbudde/algo-glockenspiel/internal/synth"
	"github.com/cwbudde/algo-glockenspiel/internal/wavio"
)

func TestBrowserFitRunsFromMemoryAndProducesArtifacts(t *testing.T) {
	t.Parallel()

	templateData, err := os.ReadFile(filepath.FromSlash("../../testdata/presets/minimal.json"))
	if err != nil {
		t.Fatalf("read template: %v", err)
	}

	template, err := preset.Decode(templateData, "test template")
	if err != nil {
		t.Fatalf("decode template: %v", err)
	}

	engine, err := synth.NewSynthesizer(template, 44100)
	if err != nil {
		t.Fatalf("build reference synthesizer: %v", err)
	}

	referenceData, err := wavio.MarshalMono(44100, engine.RenderNote(69, 100, 0.02))
	if err != nil {
		t.Fatalf("encode reference: %v", err)
	}

	prepared, err := browserfit.New(defaultRequest(), referenceData, templateData, nil)
	if err != nil {
		t.Fatalf("prepare browser fit: %v", err)
	}

	if prepared.SampleRate() != 44100 {
		t.Fatalf("sample rate = %d, want 44100", prepared.SampleRate())
	}

	if prepared.ReferenceSeconds() <= 0 {
		t.Fatalf("reference duration = %g, want positive", prepared.ReferenceSeconds())
	}

	result, err := prepared.Run(context.Background(), nil, nil)
	if err != nil {
		t.Fatalf("run browser fit: %v", err)
	}

	if result.Evaluations == 0 {
		t.Fatal("fit reported no objective evaluations")
	}

	fitted, err := prepared.Preset(result.BestParams)
	if err != nil {
		t.Fatalf("decode fitted preset: %v", err)
	}

	presetData, err := browserfit.MarshalPreset(fitted)
	if err != nil {
		t.Fatalf("marshal fitted preset: %v", err)
	}

	if !json.Valid(presetData) || presetData[len(presetData)-1] != '\n' {
		t.Fatalf("preset artifact is not newline-terminated JSON: %q", presetData)
	}

	audio, err := browserfit.Render(fitted, prepared.SampleRate(), 69, 100, 10*time.Millisecond)
	if err != nil {
		t.Fatalf("render fitted preset: %v", err)
	}

	if len(audio) < 12 || string(audio[:4]) != "RIFF" || string(audio[8:12]) != "WAVE" {
		t.Fatalf("audio artifact is not a WAV: %q", audio)
	}
}

func TestDecodeRequestRejectsUnknownAndTrailingFields(t *testing.T) {
	t.Parallel()

	for _, document := range []string{
		`{"note":69,"typo":true}`,
		`{"note":69} {"note":70}`,
	} {
		if _, err := browserfit.DecodeRequest([]byte(document)); err == nil {
			t.Fatalf("DecodeRequest(%q) succeeded, want error", document)
		}
	}
}

func TestBrowserFitValidatesBrowserControlledSettingsFirst(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		mutate  func(*browserfit.Request)
		message string
	}{
		{
			name: "note",
			mutate: func(request *browserfit.Request) {
				request.Note = 128
			},
			message: "note must be in",
		},
		{
			name: "duration",
			mutate: func(request *browserfit.Request) {
				request.TimeBudget = "forever"
			},
			message: "timeBudget must be a duration",
		},
		{
			name: "optimizer",
			mutate: func(request *browserfit.Request) {
				request.Optimizer = "unknown"
			},
			message: `unsupported optimizer "unknown"`,
		},
		{
			name: "seed",
			mutate: func(request *browserfit.Request) {
				request.MayflySeed = "9223372036854775808"
			},
			message: "mayflySeed must be a 64-bit whole number",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			request := defaultRequest()
			test.mutate(&request)

			_, err := browserfit.New(request, []byte("not decoded"), nil, nil)
			if err == nil || !strings.Contains(err.Error(), test.message) {
				t.Fatalf("error = %v, want message containing %q", err, test.message)
			}
		})
	}
}

func defaultRequest() browserfit.Request {
	return browserfit.Request{
		Note:             69,
		Velocity:         100,
		Optimizer:        "simple",
		Metric:           "rms",
		MaxIterations:    1,
		TimeBudget:       "5s",
		ReportEvery:      1,
		Align:            false,
		NormalizeGain:    false,
		MayflyVariant:    "desma",
		MayflyPopulation: 10,
		MayflySeed:       "1",
	}
}
