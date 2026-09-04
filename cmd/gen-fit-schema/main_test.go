package main

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/cwbudde/algo-glockenspiel/internal/fitschema"
)

// repoRootPath resolves a path relative to the repository root regardless of
// `go test`'s working directory, which is the package directory rather than
// the root `go run` expects typescriptPath to be relative to.
func repoRootPath(rel string) string {
	_, file, _, _ := runtime.Caller(0)

	return filepath.Join(filepath.Dir(file), "..", "..", rel)
}

// TestRenderTypeScriptMatchesTheCheckedInFile pins renderTypeScript's output
// to what web/src/api/fitSchema.generated.ts actually holds: --check compares
// exactly these two byte strings, so a mismatch here is the same failure
// `just gen-fit-schema --check` would report, without needing a subprocess.
func TestRenderTypeScriptMatchesTheCheckedInFile(t *testing.T) {
	current, err := os.ReadFile(repoRootPath(typescriptPath))
	if err != nil {
		t.Fatalf("read %s: %v", typescriptPath, err)
	}

	generated := renderTypeScript()

	if !bytes.Equal(current, generated) {
		t.Fatalf("%s is out of date: run `go run ./cmd/gen-fit-schema`", typescriptPath)
	}
}

// TestNumberFormatsFixedPointAndSafeIntegerSentinels pins the two special
// cases the generated FIT_LIMITS object depends on: a JS safe-integer bound
// spelled as the constant TypeScript itself would use, and everything else
// in fixed notation so a byte count does not turn into 1.6777216e+07.
func TestNumberFormatsFixedPointAndSafeIntegerSentinels(t *testing.T) {
	cases := map[float64]string{
		16777216:                    "16777216",
		1e12:                        "1000000000000",
		-1e12:                       "-1000000000000",
		fitschema.JSMaxSafeInteger:  "Number.MAX_SAFE_INTEGER",
		-fitschema.JSMaxSafeInteger: "Number.MIN_SAFE_INTEGER",
	}

	for value, want := range cases {
		if got := number(value); got != want {
			t.Fatalf("number(%v) = %q, want %q", value, got, want)
		}
	}
}
