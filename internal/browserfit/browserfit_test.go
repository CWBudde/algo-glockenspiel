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
	"github.com/cwbudde/algo-glockenspiel/internal/optimizer"
	"github.com/cwbudde/algo-glockenspiel/internal/preset"
	"github.com/cwbudde/algo-glockenspiel/internal/synth"
	"github.com/cwbudde/algo-glockenspiel/internal/wavio"
)

func TestBrowserFitRunsFromMemoryAndProducesArtifacts(t *testing.T) {
	t.Parallel()

	templateData, referenceData := testReference(t)

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
		// The nested row is the one that justifies carrying the tuning document
		// inline: DisallowUnknownFields reaches into it, so a misspelled knob is
		// rejected here exactly as the standalone document parser rejects it.
		`{"mayflyTuning":{"coolingrate":0.9}}`,
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
			name: "epochs",
			mutate: func(request *browserfit.Request) {
				request.MayflyEpochs = 1001
			},
			message: "mayflyEpochs must be in [1,1000]",
		},
		{
			name: "restarts",
			mutate: func(request *browserfit.Request) {
				request.MayflyRestarts = -1
			},
			message: "mayflyRestarts must be in [0,1000]",
		},
		{
			name: "stagnation",
			mutate: func(request *browserfit.Request) {
				request.MayflyStagnation = -1
			},
			message: "mayflyStagnation must be in [0,",
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

func TestBrowserFitRunsWithTheCMAESBackend(t *testing.T) {
	t.Parallel()

	templateData, referenceData := testReference(t)

	request := defaultRequest()
	request.Optimizer = "cmaes"
	// Block mode is the one that needs the codec's partition, so it is the mode
	// worth running here: a backend built without it is refused by Optimize.
	request.CmaesCovariance = "block"
	request.CmaesLambda = 4
	request.CmaesSigma = 0.3
	request.CmaesSeed = 5
	request.CmaesRestarts = 1
	request.MaxIterations = 2

	prepared, err := browserfit.New(request, referenceData, templateData, nil)
	if err != nil {
		t.Fatalf("prepare browser fit: %v", err)
	}

	result, err := prepared.Run(context.Background(), nil, nil)
	if err != nil {
		t.Fatalf("run browser fit: %v", err)
	}

	if result.Evaluations == 0 {
		t.Fatal("fit reported no objective evaluations")
	}

	if result.Restarts != 1 {
		t.Fatalf("result reports %d restarts, want the one the limit allows", result.Restarts)
	}

	if _, err = prepared.Preset(result.BestParams); err != nil {
		t.Fatalf("decode fitted preset: %v", err)
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
		MayflyPreset:     "",
		MayflyEpochs:     1,
		MayflyRestarts:   0,
		MayflyStagnation: 0,
		MayflyTargetCost: nil,
		MayflyNC:         nil,
		MayflyNCRatio:    nil,
		MayflySelection:  "",
		MayflyTuning:     nil,
	}
}

func TestBrowserFitRunsWithInlineMayflyTuning(t *testing.T) {
	t.Parallel()

	templateData, referenceData := testReference(t)

	document := `{
		"note":69,"velocity":100,"optimizer":"mayfly","metric":"rms",
		"maxIterations":8,"timeBudget":"5s","reportEvery":1,
		"align":false,"normalizeGain":false,
		"mayflyVariant":"desma","mayflyPopulation":4,"mayflySeed":"7",
		"mayflyEpochs":1,
		"mayflyTuning":{"npop":6,"convergence":{"stagnation_iterations":3}}
	}`

	request, err := browserfit.DecodeRequest([]byte(document))
	if err != nil {
		t.Fatalf("decode request: %v", err)
	}

	if request.MayflyTuning == nil || request.MayflyTuning.NPop == nil || *request.MayflyTuning.NPop != 6 {
		t.Fatalf("inline tuning = %+v, want npop 6", request.MayflyTuning)
	}

	prepared, err := browserfit.New(request, referenceData, templateData, nil)
	if err != nil {
		t.Fatalf("prepare browser fit: %v", err)
	}

	var resolved optimizer.ResolvedMayfly

	prepared.OnMayflyResolve(func(report optimizer.ResolvedMayfly) { resolved = report })

	result, err := prepared.Run(context.Background(), nil, nil)
	if err != nil {
		t.Fatalf("run browser fit: %v", err)
	}

	if result.Evaluations == 0 {
		t.Fatal("fit reported no objective evaluations")
	}

	if resolved.Variant != "desma" || resolved.Seed != 7 {
		t.Fatalf("resolved = %+v, want variant desma and seed 7", resolved)
	}
}

func TestDecodeRequestDistinguishesMayflyNCStates(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		document string
		want     *int
	}{
		{name: "absent", document: `{"note":69}`, want: nil},
		{name: "zero", document: `{"note":69,"mayflyNc":0}`, want: intPointer(0)},
		{name: "auto", document: `{"note":69,"mayflyNc":-1}`, want: intPointer(-1)},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			request, err := browserfit.DecodeRequest([]byte(test.document))
			if err != nil {
				t.Fatalf("decode request: %v", err)
			}

			switch {
			case test.want == nil && request.MayflyNC != nil:
				t.Fatalf("mayflyNc = %d, want absent", *request.MayflyNC)
			case test.want != nil && request.MayflyNC == nil:
				t.Fatalf("mayflyNc absent, want %d", *test.want)
			case test.want != nil && *request.MayflyNC != *test.want:
				t.Fatalf("mayflyNc = %d, want %d", *request.MayflyNC, *test.want)
			}
		})
	}
}

func intPointer(value int) *int { return &value }

// testReference renders the minimal preset so a fit has something to match.
func testReference(t *testing.T) (templateData, referenceData []byte) {
	t.Helper()

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

	referenceData, err = wavio.MarshalMono(44100, engine.RenderNote(69, 100, 0.02))
	if err != nil {
		t.Fatalf("encode reference: %v", err)
	}

	return templateData, referenceData
}
