package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// execute runs the root command against captured streams and returns what it
// wrote and whether it failed. The command tree is rebuilt per call, so flags
// set by one test do not survive into the next.
func execute(t *testing.T, args ...string) (string, error) {
	t.Helper()

	var out bytes.Buffer

	cmd := NewRootCmd()
	cmd.SetArgs(args)
	cmd.SetOut(&out)
	cmd.SetErr(&out)

	err := cmd.Execute()

	return out.String(), err
}

func TestListNamesEveryRegisteredDesign(t *testing.T) {
	out, err := execute(t, "list")
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	for _, name := range []string{"smoke", "engine-shape", "seed-hunt"} {
		if !strings.Contains(out, name+":") {
			t.Errorf("list does not mention design %q:\n%s", name, out)
		}
	}
}

// TestPlanWritesAManifestAndPrintsTheTable covers the whole plan path against
// the smoke design, which is the one that names a reference the working
// directory of a test cannot see: plan has to find the repository root for
// itself, and that step is only exercised end to end.
func TestPlanWritesAManifestAndPrintsTheTable(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "smoke")

	restoreWorkingDirectory(t)

	out, err := execute(t, "plan", "smoke", "--dir", dir, "--workers", "2")
	if err != nil {
		t.Fatalf("plan smoke: %v\n%s", err, out)
	}

	if _, err := os.Stat(filepath.Join(dir, "manifest.json")); err != nil {
		t.Fatalf("manifest: %v", err)
	}

	for _, want := range []string{"mayfly-single", "sep-cmaes-r", "2 workers", "| arm | engine |"} {
		if !strings.Contains(out, want) {
			t.Errorf("plan output does not contain %q:\n%s", want, out)
		}
	}
}

// TestPlanRefusesASeedHuntWinnerThatIsNotACMAESArm pins the one flag the
// harness has. A mayfly winner would produce a design whose two arms differ in
// a population size the backend does not have, which is a comparison of
// nothing.
func TestPlanRefusesASeedHuntWinnerThatIsNotACMAESArm(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "seed-hunt")

	restoreWorkingDirectory(t)

	out, err := execute(t, "plan", "seed-hunt", "--dir", dir, "--winner", "mayfly-r16")
	if err == nil {
		t.Fatalf("plan accepted a mayfly winner:\n%s", out)
	}

	if !strings.Contains(err.Error(), "mayfly-r16") {
		t.Errorf("error does not name the arm: %v", err)
	}

	if _, statErr := os.Stat(dir); statErr == nil {
		t.Errorf("a refused plan left a campaign directory behind at %s", dir)
	}
}

// restoreWorkingDirectory puts the process back where the test started. Plan
// moves to the repository root so a design's relative reference resolves, and
// a test that left it there would change what every later test in the package
// sees.
func restoreWorkingDirectory(t *testing.T) {
	t.Helper()

	before, err := os.Getwd()
	if err != nil {
		t.Fatalf("working directory: %v", err)
	}

	t.Cleanup(func() {
		if err := os.Chdir(before); err != nil {
			t.Fatalf("restore working directory: %v", err)
		}
	})
}
