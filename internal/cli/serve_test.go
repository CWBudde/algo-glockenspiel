package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestServeCommandIsRegistered(t *testing.T) {
	found := false

	for _, sub := range NewRootCmd().Commands() {
		if sub.Name() == "serve" {
			found = true

			if sub.Flags().Lookup("addr") == nil {
				t.Fatal("serve has no --addr flag")
			}
		}
	}

	if !found {
		t.Fatal("serve command is not registered on the root command")
	}
}

// A run that is interrupted before it begins must still come back cleanly, and
// it must have said out loud that the WebAssembly module is missing -- the
// failure mode this command exists to make visible.
func TestServeWarnsAboutMissingWasmAndShutsDown(t *testing.T) {
	cmd := NewRootCmd()
	cmd.SetArgs([]string{"serve", "--addr", "127.0.0.1:0", "--dist", t.TempDir()})

	var stdout, stderr bytes.Buffer

	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	cmd.SetContext(ctx)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("serve returned %v, want nil after a cancelled context", err)
	}

	if !strings.Contains(stderr.String(), "just build-web") {
		t.Fatalf("expected the missing-wasm warning on stderr, got %q", stderr.String())
	}
}

func TestServeRejectsEmptyAddr(t *testing.T) {
	var stdout, stderr bytes.Buffer

	if code := Run([]string{"serve", "--addr", ""}, &stdout, &stderr); code != 1 {
		t.Fatalf("expected exit code 1, got %d", code)
	}

	if !strings.Contains(stderr.String(), "addr is required") {
		t.Fatalf("unexpected stderr: %q", stderr.String())
	}
}
