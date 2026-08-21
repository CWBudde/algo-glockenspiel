package cli

import (
	"bytes"
	"io"
	"path/filepath"
	"strings"
	"testing"
)

func TestRootHelp(t *testing.T) {
	cmd := NewRootCmd()
	cmd.SetArgs([]string{"--help"})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("expected help command to succeed, got error: %v", err)
	}
}

func TestVersionCommand(t *testing.T) {
	cmd := NewRootCmd()
	cmd.SetArgs([]string{"version"})

	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(io.Discard)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("expected version command to succeed, got error: %v", err)
	}

	got := strings.TrimSpace(out.String())
	if got != "glockenspiel "+version {
		t.Fatalf("unexpected version output: %q", got)
	}
}

func TestRunReportsErrorsOnStderr(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "unknown command", args: []string{"nope"}, want: "unknown command"},
		{name: "unknown flag", args: []string{"synth", "--nope"}, want: "unknown flag"},
		{name: "invalid flag value", args: []string{"synth", "--note", "500"}, want: "note must be in [0,127]"},
		{name: "stray positional arg", args: []string{"synth", "extra"}, want: "unknown command"},
		{name: "missing required flag", args: []string{"fit"}, want: "required flag"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer

			if code := Run(tc.args, &stdout, &stderr); code != 1 {
				t.Fatalf("expected exit code 1, got %d (stderr=%q)", code, stderr.String())
			}

			text := stderr.String()
			if !strings.Contains(text, tc.want) {
				t.Fatalf("expected stderr to mention %q, got %q", tc.want, text)
			}

			if !strings.HasPrefix(text, errorPrefix) {
				t.Fatalf("expected stderr to start with %q, got %q", errorPrefix, text)
			}

			// Cobra must stay silent so the message is not printed twice.
			if strings.Count(text, tc.want) != 1 {
				t.Fatalf("expected exactly one error message, got %q", text)
			}
		})
	}
}

func TestRunSucceeds(t *testing.T) {
	var stdout, stderr bytes.Buffer

	if code := Run([]string{"version"}, &stdout, &stderr); code != 0 {
		t.Fatalf("expected exit code 0, got %d (stderr=%q)", code, stderr.String())
	}

	if stderr.Len() != 0 {
		t.Fatalf("expected empty stderr, got %q", stderr.String())
	}

	if !strings.Contains(stdout.String(), "glockenspiel "+version) {
		t.Fatalf("unexpected stdout: %q", stdout.String())
	}
}

func TestLoadPresetOrDefaultUsesEmbeddedPreset(t *testing.T) {
	loaded, err := loadPresetOrDefault("")
	if err != nil {
		t.Fatalf("loadPresetOrDefault failed: %v", err)
	}

	if loaded == nil {
		t.Fatal("expected embedded preset")
	}
}

func TestLoadPresetOrDefaultUsesExplicitPath(t *testing.T) {
	loaded, err := loadPresetOrDefault(filepath.FromSlash("../../testdata/presets/minimal.json"))
	if err != nil {
		t.Fatalf("loadPresetOrDefault failed: %v", err)
	}

	if loaded == nil {
		t.Fatal("expected preset from explicit path")
	}

	if _, err := loadPresetOrDefault(filepath.Join(t.TempDir(), "absent.json")); err == nil {
		t.Fatal("expected missing preset path to fail")
	}
}
