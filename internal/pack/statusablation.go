package pack

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/cwbudde/algo-glockenspiel/internal/fitrun"
)

// ablationLogSuffix is the file a driver leaves beside each fit it starts,
// named for the fit's directory. It is the only sign of a fit whose search
// has not yet written its config, which is the first minute of every fit:
// fitrun seeds and analyses before it writes config.json.
const ablationLogSuffix = ".log"

// ablationNotesDir is the folder a joint fit keeps its per-note analysis in,
// and a pack run its per-note fits. Neither is a fit of an ablation, and a
// half-written run directory that has this folder and no config yet must not
// be read as an ablation of one fit called "notes".
const ablationNotesDir = "notes"

// readAblationStatus describes a directory whose children are joint fits.
//
// Nothing in the directory says it is an ablation; the shape is inferred from
// what is there, in the same way a joint fit is inferred from a config at the
// top. That is deliberate: the ablation drivers are shell loops written for
// one question each, and a manifest they would all have to agree on is a
// format nobody has needed yet.
func readAblationStatus(dir string) (*Status, error) {
	names, err := ablationChildren(dir)
	if err != nil {
		return nil, err
	}

	if len(names) == 0 {
		return nil, fmt.Errorf("%s holds neither a pack manifest, a fit run directory, nor a directory of fit runs", dir)
	}

	status := &Status{
		Pack:  filepath.Base(dir),
		Dir:   dir,
		Kind:  KindAblation,
		Notes: make([]NoteStatus, 0, len(names)),
		Read:  time.Now(),
	}

	for _, name := range names {
		note, config := readAblationChild(dir, name, status.Read)

		if config != nil {
			if status.Budget == 0 {
				status.Budget = config.MaxEvaluations
			}

			if status.Workers == 0 {
				status.Workers = config.Workers
			}
		}

		status.Notes = append(status.Notes, note)
		status.count(note)
	}

	status.estimate()

	return status, nil
}

// ablationChildren lists the fits, sorted by name: every subdirectory that is
// a run directory, and every "<name>.log" whose directory is not one yet.
//
// A log with nothing else is a fit that has been started and has not written
// its config, so it is listed as pending rather than left out -- a monitor
// that showed 0 of 24 with nothing running for the first minute of every fit
// would look broken exactly when someone has just started it. A directory
// that is not a run and has no log is skipped: the joint fits keep their
// per-note analysis under notes/, and nothing under an ablation is a fit
// unless a search wrote there or a driver logged there.
//
// The error is only for a directory that cannot be listed; a listable
// directory with no fits in it returns no names, and the caller decides what
// that means.
func ablationChildren(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	runs := map[string]bool{}
	logged := map[string]bool{}

	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, ".") || name == ablationNotesDir {
			continue
		}

		switch {
		case entry.IsDir() && isRunDir(filepath.Join(dir, name)):
			runs[name] = true
		case !entry.IsDir() && strings.HasSuffix(name, ablationLogSuffix):
			logged[strings.TrimSuffix(name, ablationLogSuffix)] = true
		}
	}

	// A log alone is a fit that has been started; a log beside a run directory
	// is the same fit, listed once.
	if len(runs) == 0 {
		return nil, nil
	}

	names := make([]string, 0, len(runs)+len(logged))

	for name := range runs {
		names = append(names, name)
	}

	for name := range logged {
		if !runs[name] {
			names = append(names, name)
		}
	}

	sort.Strings(names)

	return names, nil
}

// readAblationChild reads one fit of an ablation: the run directory as any
// other, plus the two things that make it an arm of a comparison.
//
// The config is returned as well so the caller can take the budget and the
// worker width from the first fit that has one; every fit of an ablation was
// run with the same, or the comparison is not one.
func readAblationChild(dir, name string, now time.Time) (NoteStatus, *statusRunConfig) {
	child := filepath.Join(dir, name)

	note := readNoteStatus(child, 0, now)
	note.Name = name
	note.Dir = name

	config, err := readRunConfig(child)
	if err != nil {
		return note, nil
	}

	note.Note = config.Note
	note.Budget = config.MaxEvaluations
	note.Arm = ArmFixed

	if config.SearchDecayKeytrack {
		note.Arm = ArmBeta
	}

	if note.State == StateDone || note.State == StateCanceled {
		note.Keytrack = readPresetKeytrack(child)
	}

	return note, config
}

// statusPreset is the little of a run's preset.json this package reads, for
// the reason statusRunConfig is partial: the exponent is the one number the
// ablation exists to find, and the rest of the preset is not the monitor's
// business.
type statusPreset struct {
	Parameters struct {
		DecayKeytrack *float64 `json:"decay_keytrack"`
	} `json:"parameters"`
}

// readPresetKeytrack returns the decay exponent a finished fit settled on, or
// nil when the preset does not carry one -- which is what the fixed arm's
// preset looks like, and what a missing or unreadable preset looks like too.
// The two are the same to a reader: there is no exponent to show.
func readPresetKeytrack(dir string) *float64 {
	body, err := os.ReadFile(filepath.Join(dir, fitrun.FilePreset))
	if err != nil {
		return nil
	}

	var preset statusPreset
	if err := json.Unmarshal(body, &preset); err != nil {
		return nil
	}

	return preset.Parameters.DecayKeytrack
}
