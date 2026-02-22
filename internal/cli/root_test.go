package cli

import (
	"bytes"
	"io"
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
