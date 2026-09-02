package cli

import (
	"bytes"
	"encoding/json"
	"math"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/cwbudde/algo-glockenspiel/internal/analysis"
	"github.com/cwbudde/algo-glockenspiel/internal/wavio"
)

// writeStruck writes a one-mode strike after a short silence, at a modest
// level, so the cut and the gain both have something to do.
func writeStruck(t *testing.T) string {
	t.Helper()

	const (
		rate      = 44100
		lead      = rate / 10
		amplitude = 0.25
	)

	samples := make([]float32, rate)
	for i := lead; i < len(samples); i++ {
		seconds := float64(i-lead) / rate
		samples[i] = float32(amplitude * math.Pow(0.5, seconds/0.15) * math.Sin(2*math.Pi*1200*seconds))
	}

	path := filepath.Join(t.TempDir(), "struck.wav")
	if err := wavio.WriteMono(path, rate, samples); err != nil {
		t.Fatalf("WriteMono failed: %v", err)
	}

	return path
}

func TestRunAnalyzePrintsTheCutAndTheTable(t *testing.T) {
	var out, errOut bytes.Buffer

	code := Run([]string{"analyze", "--reference", writeStruck(t)}, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, errOut.String())
	}

	text := out.String()
	for _, want := range []string{"strike from frame", "gain +12.", "fundamental 1200.0 Hz", "1200.0 Hz", "150 ms"} {
		if !strings.Contains(text, want) {
			t.Errorf("output lacks %q:\n%s", want, text)
		}
	}
}

func TestRunAnalyzeWritesTheDocumentAndTheCutReference(t *testing.T) {
	dir := t.TempDir()
	outputPath := filepath.Join(dir, "run", "analysis.json")
	trimmedPath := filepath.Join(dir, "run", "reference.wav")

	var out, errOut bytes.Buffer

	code := Run([]string{
		"analyze", "--reference", writeStruck(t),
		"--output", outputPath, "--trimmed-out", trimmedPath, "--json",
	}, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, errOut.String())
	}

	var printed analysis.Analysis
	if err := json.Unmarshal(out.Bytes(), &printed); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, out.String())
	}

	written, err := analysis.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}

	if !reflect.DeepEqual(written.Reference, printed.Reference) || len(written.Partials) != len(printed.Partials) {
		t.Errorf("file and stdout disagree: %+v vs %+v", written.Reference, printed.Reference)
	}

	if written.GeneratedBy != analysis.GeneratedBy || written.Reference.GainDB < 11.9 || written.Reference.GainDB > 12.1 {
		t.Errorf("document = %+v, want the marker and a +12 dB gain", written)
	}

	cut, rate, err := wavio.LoadMono(trimmedPath)
	if err != nil {
		t.Fatalf("LoadMono failed: %v", err)
	}

	if rate != written.Reference.SampleRate || len(cut) != written.Reference.End-written.Reference.Onset {
		t.Errorf("cut reference holds %d samples at %d Hz, want %d at %d",
			len(cut), rate, written.Reference.End-written.Reference.Onset, written.Reference.SampleRate)
	}

	peak := float32(0)
	for _, sample := range cut {
		peak = max(peak, float32(math.Abs(float64(sample))))
	}

	if peak < 0.999 {
		t.Errorf("cut reference peak = %g, want full scale", peak)
	}
}

func TestRunAnalyzeRejectsBadOptions(t *testing.T) {
	reference := writeStruck(t)

	for _, args := range [][]string{
		{"analyze"},
		{"analyze", "--reference", reference, "--downmix", "sum"},
		{"analyze", "--reference", reference, "--min-level", "3"},
		{"analyze", "--reference", reference, "--window", "-1s"},
		{"analyze", "--reference", reference, "--frame-size", "0"},
		{"analyze", "--reference", filepath.Join(t.TempDir(), "missing.wav")},
	} {
		var out, errOut bytes.Buffer

		if code := Run(args, &out, &errOut); code == 0 {
			t.Errorf("%v exited 0, want a failure", args)
		}
	}
}

func TestRunAnalyzeHonoursTheWindow(t *testing.T) {
	var out, errOut bytes.Buffer

	code := Run([]string{"analyze", "--reference", writeStruck(t), "--window", "500ms", "--keep-level", "--json"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, errOut.String())
	}

	var document analysis.Analysis
	if err := json.Unmarshal(out.Bytes(), &document); err != nil {
		t.Fatalf("stdout is not JSON: %v", err)
	}

	if document.Reference.EndRule != analysis.EndWindow || document.Reference.End-document.Reference.Onset != 22050 {
		t.Errorf("reference = %+v, want a 22050-sample fixed window", document.Reference)
	}

	if document.Reference.GainDB != 0 {
		t.Errorf("gain = %g dB under --keep-level, want 0", document.Reference.GainDB)
	}
}
