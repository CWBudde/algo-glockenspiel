package pack

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/cwbudde/algo-glockenspiel/internal/analysis"
	"github.com/cwbudde/algo-glockenspiel/internal/campaign"
	"github.com/cwbudde/algo-glockenspiel/internal/fitrun"
	"github.com/cwbudde/algo-glockenspiel/internal/optimizer"
)

// DefaultMaxCents is how far a recording may sit from equal temperament before
// Plan refuses it.
//
// Fifty cents is half a semitone, so anything past it is nearer some other note
// and the file's name has stopped describing its pitch. The hollandm pack runs
// to five cents and clears this easily; the mooncubedesign pack reaches
// forty-nine and only just does, which is the warning its own README intends.
const DefaultMaxCents = 50

// Options are what Plan needs beyond the directory of recordings.
type Options struct {
	// Modes is how many partials to seed per note. Zero, the default, takes
	// every partial the analysis found.
	//
	// Pinning it to one number across a pack is tempting and is usually wrong.
	// The hollandm pack measures between two and nine partials per note -- the
	// modal content thins out towards the top -- so a pinned count discards
	// partials at the bottom of the pack and cannot invent any at the top,
	// where the fit would be padded with modes the recording does not contain.
	// The per-note fits are independent, so a common dimension buys nothing;
	// what matters is that the modes are sorted by frequency, which the codec
	// already guarantees, so mode k means the same thing at every note.
	Modes int

	// Budget is the evaluation cap per note.
	Budget int

	// SeedBase is the first note's seed; each note takes SeedBase plus its
	// index. It must not collide with a campaign's bases.
	SeedBase int64

	// MaxCents bounds how far from equal temperament a recording may sit.
	// Zero takes DefaultMaxCents.
	MaxCents float64

	// Workers is the parallel evaluation width, recorded so a resumed run
	// keeps it. Zero follows the machine and is written down.
	Workers int

	// Engine selects the backend. The zero value takes the promoted default,
	// mayfly in one warm round plus fifteen cold restarts.
	Engine *fitrun.Engine

	// Profile is the objective. Empty takes balanced.
	Profile optimizer.Metric
}

// DefaultEngine is the arm phase 8.6's engine-shape campaign promoted and the
// CLI now defaults to: Mayfly in one warm round from the reference's own
// partials plus fifteen cold restarts.
func DefaultEngine() fitrun.Engine {
	return fitrun.Engine{
		Name: fitrun.EngineMayfly,
		Mayfly: fitrun.MayflySettings{
			Variant:    "desma",
			Population: 10,
			Epochs:     1,
			Restarts:   15,
		},
	}
}

// Plan reads a directory of per-note recordings, resolves every file to the
// note it actually sounds, and writes the run's manifest.
//
// Every note is measured here rather than at run time, because the note a file
// sounds is what decides the whole job and a plan nobody can read before
// spending machine time on it is not a plan. That measurement costs about a
// second per file.
func Plan(packDir, dir string, opts Options) (*Manifest, error) {
	if opts.Budget <= 0 {
		return nil, fmt.Errorf("budget must be positive, got %d", opts.Budget)
	}

	if opts.Modes < 0 {
		return nil, fmt.Errorf("modes cannot be negative, got %d", opts.Modes)
	}

	if opts.MaxCents == 0 {
		opts.MaxCents = DefaultMaxCents
	}

	if opts.Workers == 0 {
		opts.Workers = runtime.NumCPU()
	}

	engine := DefaultEngine()
	if opts.Engine != nil {
		engine = *opts.Engine
	}

	profile := opts.Profile
	if profile == "" {
		profile = optimizer.MetricBalanced
	}

	if _, err := optimizer.ParseMetric(string(profile)); err != nil {
		return nil, err
	}

	jobs, err := planJobs(packDir, opts)
	if err != nil {
		return nil, err
	}

	binary, err := planningBinary()
	if err != nil {
		return nil, err
	}

	manifest := &Manifest{
		Pack:        filepath.ToSlash(packDir),
		Description: fmt.Sprintf("%d notes from %s", len(jobs), filepath.Base(packDir)),
		Created:     time.Now().UTC(),
		Binary:      binary,
		Profile:     profile,
		Modes:       opts.Modes,
		Budget:      opts.Budget,
		SeedBase:    opts.SeedBase,
		MaxCents:    opts.MaxCents,
		Workers:     opts.Workers,
		Engine:      engine,
		Jobs:        jobs,
	}

	if err := writeManifest(dir, manifest); err != nil {
		return nil, err
	}

	return manifest, nil
}

// planJobs measures every WAV in the pack directory and builds the job list in
// ascending note order.
func planJobs(packDir string, opts Options) ([]Job, error) {
	entries, err := os.ReadDir(packDir)
	if err != nil {
		return nil, fmt.Errorf("read pack directory %q: %w", packDir, err)
	}

	jobs := make([]Job, 0, len(entries))

	for _, entry := range entries {
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".wav") {
			continue
		}

		stem := strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name()))
		path := filepath.Join(packDir, entry.Name())

		measured, err := analysis.Analyze(path, analysis.LoadOptions{}, analysis.PartialOptions{})
		if err != nil {
			return nil, fmt.Errorf("measure %q: %w", path, err)
		}

		note, cents, err := ResolveNote(stem, measured.FundamentalHz, opts.MaxCents)
		if err != nil {
			return nil, err
		}

		absolute, err := filepath.Abs(path)
		if err != nil {
			return nil, fmt.Errorf("resolve %q: %w", path, err)
		}

		sum, err := fitrun.FileSHA256(absolute)
		if err != nil {
			return nil, err
		}

		jobs = append(jobs, Job{
			Name:      stem,
			Note:      note,
			Cents:     cents,
			Reference: campaign.FileHash{Path: absolute, SHA256: sum},
			Dir:       jobDir(note),
		})
	}

	if len(jobs) == 0 {
		return nil, fmt.Errorf("pack directory %q holds no .wav files", packDir)
	}

	sort.Slice(jobs, func(i, j int) bool { return jobs[i].Note < jobs[j].Note })

	for i := range jobs {
		// The seed follows the note's position in the pack rather than the note
		// number, so two packs planned at the same base get the same streams
		// only where they hold the same number of notes. Phase 8.6's finding
		// stands behind this: streams derived from a seed by arithmetic couple
		// runs that should be independent, so bases must not overlap at all.
		jobs[i].Seed = opts.SeedBase + int64(i)
		jobs[i].ID = fmt.Sprintf("n%03d-%s", jobs[i].Note, jobs[i].Name)

		if i > 0 && jobs[i].Note == jobs[i-1].Note {
			return nil, fmt.Errorf(
				"%s.wav and %s.wav both sound MIDI %d, so one of them would overwrite the other's run directory",
				jobs[i-1].Name, jobs[i].Name, jobs[i].Note)
		}
	}

	return jobs, nil
}

// planningBinary describes the executable doing the planning, so run can refuse
// to continue under a different build.
func planningBinary() (campaign.Binary, error) {
	path, err := os.Executable()
	if err != nil {
		return campaign.Binary{}, fmt.Errorf("locate the running binary: %w", err)
	}

	sum, err := fitrun.FileSHA256(path)
	if err != nil {
		return campaign.Binary{}, err
	}

	return campaign.Binary{Path: path, SHA256: sum, Identity: fitrun.ReadIdentity()}, nil
}

// Table renders a planned run as a Markdown table, so the command that plans
// one can print what it is about to spend.
func Table(manifest *Manifest) string {
	var out strings.Builder

	fmt.Fprintf(&out, "%s\n\n", manifest.Description)
	fmt.Fprintf(&out, "%d notes, %d evaluations each, seeds %d..%d, %d workers, profile %s\n\n",
		len(manifest.Jobs), manifest.Budget, manifest.SeedBase,
		manifest.SeedBase+int64(len(manifest.Jobs))-1, manifest.Workers, manifest.Profile)

	out.WriteString("| note | name | MIDI | cents off | seed |\n")
	out.WriteString("| --- | --- | --- | --- | --- |\n")

	for i, job := range manifest.Jobs {
		fmt.Fprintf(&out, "| %d | %s | %d | %+.1f | %d |\n", i, job.Name, job.Note, job.Cents, job.Seed)
	}

	return out.String()
}
