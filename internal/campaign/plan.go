package campaign

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/cwbudde/algo-glockenspiel/internal/fitrun"
)

// Plan writes a campaign directory's manifest and returns it.
//
// It resolves everything that has to be pinned before any job runs: the
// reference's content hash, the running binary and its build identity, and the
// full job list with its seeds. Nothing is searched here, so a plan is cheap
// enough to read before committing an hour of machine time to it.
//
// The design's reference is repo-relative and is resolved against the working
// directory. A worker count of zero is resolved to the machine's CPU count and
// recorded, so the manifest always fixes a width.
func Plan(design Design, dir string, workers int) (*Manifest, error) {
	if err := design.Validate(); err != nil {
		return nil, err
	}

	if workers < 0 {
		return nil, fmt.Errorf("workers cannot be negative, got %d", workers)
	}

	// A width of zero would let every job follow the machine it happened to
	// run on, so a campaign resumed on another box would mix widths. 8.4
	// proved the result is bit-identical across widths at a fixed seed, so
	// pinning the number costs nothing and the elapsed column stays readable.
	if workers == 0 {
		workers = runtime.NumCPU()
	}

	hash, err := design.Hash()
	if err != nil {
		return nil, err
	}

	reference, err := hashReference(design.Reference)
	if err != nil {
		return nil, err
	}

	binary, err := planningBinary()
	if err != nil {
		return nil, err
	}

	manifest := &Manifest{
		Design:     design,
		DesignHash: hash,
		Created:    time.Now().UTC(),
		Binary:     binary,
		Reference:  reference,
		Workers:    workers,
		Jobs:       design.jobs(),
	}

	if err := writeManifest(dir, manifest); err != nil {
		return nil, err
	}

	return manifest, nil
}

// hashReference resolves the design's reference to an absolute path and hashes
// it. The absolute path is recorded so a campaign can be resumed from any
// working directory, while the hash is what actually identifies the recording.
func hashReference(path string) (FileHash, error) {
	absolute, err := filepath.Abs(filepath.FromSlash(path))
	if err != nil {
		return FileHash{}, fmt.Errorf("resolve reference %q: %w", path, err)
	}

	sum, err := fitrun.FileSHA256(absolute)
	if err != nil {
		return FileHash{}, err
	}

	return FileHash{Path: absolute, SHA256: sum}, nil
}

// planningBinary describes the executable doing the planning.
func planningBinary() (Binary, error) {
	path, err := os.Executable()
	if err != nil {
		return Binary{}, fmt.Errorf("locate the running binary: %w", err)
	}

	sum, err := fitrun.FileSHA256(path)
	if err != nil {
		return Binary{}, err
	}

	return Binary{Path: path, SHA256: sum, Identity: fitrun.ReadIdentity()}, nil
}

// writeManifest creates the campaign directory and writes the manifest
// exclusively. An existing manifest is refused rather than replaced.
func writeManifest(dir string, manifest *Manifest) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create campaign directory %q: %w", dir, err)
	}

	path := filepath.Join(dir, FileManifest)

	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		if os.IsExist(err) {
			return fmt.Errorf(
				"campaign %q already exists: planning it again would write a second design beside run "+
					"directories the first design produced, and the pairing of results to design would be lost; "+
					"collect the campaign or plan into a new directory",
				dir)
		}

		return fmt.Errorf("create manifest %q: %w", path, err)
	}

	defer func() { _ = file.Close() }()

	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")

	if err := encoder.Encode(manifest); err != nil {
		return fmt.Errorf("write manifest %q: %w", path, err)
	}

	return file.Close()
}

// PlanTable renders the arms of a planned campaign as a Markdown table, so the
// command that plans one can print what it is about to spend an hour on.
func PlanTable(manifest *Manifest) string {
	var out strings.Builder

	design := manifest.Design

	fmt.Fprintf(&out, "%s: %s\n\n", design.Name, design.Description)
	fmt.Fprintf(&out, "%d blocks x %d arms = %d jobs, %d evaluations each, seeds %d..%d, %d workers\n\n",
		design.Blocks, len(design.Arms), len(manifest.Jobs), design.Budget,
		design.SeedBase, design.SeedBase+int64(design.Blocks)-1, manifest.Workers)

	out.WriteString("| arm | engine | shape | per-run evals | restarts planned | budget |\n")
	out.WriteString("| --- | --- | --- | --- | --- | --- |\n")

	for _, arm := range design.Arms {
		fmt.Fprintf(&out, "| %s | %s | %s | %s | %s | %d |\n",
			arm.Name, arm.Engine.Name, armShape(arm), armRunEvaluations(arm, design.Budget),
			armRestarts(arm), design.Budget)
	}

	return out.String()
}

// armShape is the one phrase that says what distinguishes this arm from its
// neighbours in the table.
func armShape(arm Arm) string {
	switch arm.Engine.Name {
	case fitrun.EngineMayfly:
		settings := arm.Engine.Mayfly
		shape := fmt.Sprintf("%s, population %d", settings.Variant, settings.Population)

		if settings.Restarts > 0 || settings.Epochs > 1 {
			shape += fmt.Sprintf(", %d rounds", settings.Restarts+1)
		}

		return shape
	case fitrun.EngineCMAES:
		settings := arm.Engine.CMAES
		shape := settings.Covariance

		if settings.LambdaGrowth > 0 {
			shape += fmt.Sprintf(", lambda growth %g", settings.LambdaGrowth)
		}

		if settings.Lambda > 0 {
			shape += fmt.Sprintf(", lambda %d", settings.Lambda)
		}

		return shape
	default:
		return arm.Engine.Name
	}
}

// armRunEvaluations is what one run of the arm may spend before the wrapper
// restarts it. An arm with no per-run cap has the whole budget in one run.
func armRunEvaluations(arm Arm, budget int) string {
	if arm.Engine.Name == fitrun.EngineCMAES && arm.Engine.CMAES.RunEvaluations > 0 {
		return strconv.Itoa(arm.Engine.CMAES.RunEvaluations)
	}

	return strconv.Itoa(budget)
}

// armRestarts renders the declared restart shape, where zero means "restart
// until the budget is spent" rather than "no restarts".
func armRestarts(arm Arm) string {
	if arm.RestartsPlanned == 0 {
		return "until budget"
	}

	return strconv.Itoa(arm.RestartsPlanned)
}
