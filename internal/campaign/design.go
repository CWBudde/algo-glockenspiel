// Package campaign runs a designed comparison of optimizer arms and records
// enough of it to be argued about later.
//
// A campaign is a design (which arms, how many blocks, which contrasts matter)
// turned into a manifest of jobs, then into one fitrun run directory per job,
// then into a single results.csv. The three steps are separate because they
// fail differently: planning is cheap and must refuse to overwrite an existing
// campaign, running is hours long and must resume, and collecting reads runs
// that were produced by a build which may no longer be checked out.
package campaign

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"

	"github.com/cwbudde/algo-glockenspiel/internal/fitrun"
	"github.com/cwbudde/algo-glockenspiel/internal/optimizer"
)

// Design is one experiment: what to fit, under which budget, with which arms,
// and which differences between them the analysis is allowed to test.
//
// It is a value rather than a file because a design is an argument about how
// the comparison is fair, and an argument belongs in reviewed source next to
// the derivation of its constants. The manifest echoes the whole value and its
// hash so a result set can be tied back to the design that produced it even
// after the source has moved on.
type Design struct {
	Name        string `json:"name"`
	Description string `json:"description"`

	// Reference is the recording to fit, as a repo-relative path. Plan and Run
	// resolve it against the working directory.
	Reference string `json:"reference"`

	Note    int              `json:"note"`
	Profile optimizer.Metric `json:"profile"`

	// Budget is the evaluation cap every job of every arm gets. Matching the
	// arms on evaluations rather than on time or iterations is what makes the
	// comparison a comparison: an evaluation is one render whichever backend
	// spends it.
	Budget int `json:"budget"`

	// Blocks is how many times the whole set of arms is run. One block is one
	// seed, shared by every arm in it, so the arms of a block see the same
	// random stream and the block is the unit a paired analysis pairs on.
	Blocks   int   `json:"blocks"`
	SeedBase int64 `json:"seed_base"`

	Arms []Arm `json:"arms"`

	// Contrasts are the comparisons the analysis is registered to make. They
	// are declared before any data exists, because a contrast chosen after
	// looking at the numbers is not a test of anything.
	Contrasts []Contrast `json:"contrasts"`

	// Descriptive marks a design that asks how a knob behaves rather than
	// whether one arm beats another. The analysis prints no inferential
	// statistics for it.
	Descriptive bool `json:"descriptive"`

	// Modes is fitrun.Spec.Modes: how many measured partials to seed. Zero
	// seeds every partial the analysis found.
	Modes int `json:"modes"`
}

// Arm is one configuration under test. MaxIterations is the backend's own
// iteration cap, which only mayfly needs; the evaluation cap in Design.Budget
// is what actually binds every arm.
type Arm struct {
	Name            string        `json:"name"`
	Engine          fitrun.Engine `json:"engine"`
	MaxIterations   int           `json:"max_iterations"`
	RestartsPlanned int           `json:"restarts_planned"`
}

// Contrast is one registered comparison between two arms. Primary marks the
// one the design exists to answer; the rest are secondary and the analysis
// says so.
type Contrast struct {
	Control   string `json:"control"`
	Candidate string `json:"candidate"`
	Primary   bool   `json:"primary"`
}

// Validate rejects a design that could not be analysed as written.
//
// The checks are the ones whose violation would only show up as a confusing
// result: a duplicated arm name silently merges two arms in the analysis, a
// contrast naming an arm that is not there produces an empty comparison rather
// than an error, and a single block leaves a paired test with no pairs.
func (d Design) Validate() error {
	if d.Name == "" {
		return fmt.Errorf("design has no name")
	}

	if d.Budget <= 0 {
		return fmt.Errorf("design %q: budget must be positive, got %d", d.Name, d.Budget)
	}

	if d.Blocks <= 0 {
		return fmt.Errorf("design %q: blocks must be positive, got %d", d.Name, d.Blocks)
	}

	if len(d.Arms) == 0 {
		return fmt.Errorf("design %q: has no arms", d.Name)
	}

	seen := make(map[string]bool, len(d.Arms))

	for _, arm := range d.Arms {
		if arm.Name == "" {
			return fmt.Errorf("design %q: an arm has no name", d.Name)
		}

		if seen[arm.Name] {
			return fmt.Errorf("design %q: arm %q is declared twice", d.Name, arm.Name)
		}

		seen[arm.Name] = true
	}

	if err := d.validateContrasts(seen); err != nil {
		return err
	}

	if !d.Descriptive {
		if len(d.Contrasts) == 0 {
			return fmt.Errorf("design %q: a non-descriptive design needs at least one contrast", d.Name)
		}

		if d.Blocks < 2 {
			return fmt.Errorf("design %q: a non-descriptive design needs at least two blocks, got %d", d.Name, d.Blocks)
		}
	}

	if d.Reference == "" {
		return fmt.Errorf("design %q: has no reference", d.Name)
	}

	if _, err := os.Stat(d.Reference); err != nil {
		return fmt.Errorf("design %q: reference %q: %w", d.Name, d.Reference, err)
	}

	return nil
}

// validateContrasts checks every contrast against the arm names and allows at
// most one primary.
func (d Design) validateContrasts(arms map[string]bool) error {
	primaries := 0

	for _, contrast := range d.Contrasts {
		if contrast.Control == contrast.Candidate {
			return fmt.Errorf("design %q: contrast compares arm %q with itself", d.Name, contrast.Control)
		}

		if !arms[contrast.Control] {
			return fmt.Errorf("design %q: contrast names unknown control arm %q", d.Name, contrast.Control)
		}

		if !arms[contrast.Candidate] {
			return fmt.Errorf("design %q: contrast names unknown candidate arm %q", d.Name, contrast.Candidate)
		}

		if contrast.Primary {
			primaries++
		}
	}

	if primaries > 1 {
		return fmt.Errorf("design %q: %d primary contrasts, want at most one", d.Name, primaries)
	}

	return nil
}

// Hash is the SHA-256 of the design's JSON. The design types hold no maps, so
// the encoding is deterministic and two campaigns planned from the same source
// carry the same hash.
func (d Design) Hash() (string, error) {
	encoded, err := json.Marshal(d)
	if err != nil {
		return "", fmt.Errorf("encode design %q: %w", d.Name, err)
	}

	sum := sha256.Sum256(encoded)

	return hex.EncodeToString(sum[:]), nil
}

// ArmByName returns one arm of the design.
func (d Design) ArmByName(name string) (Arm, error) {
	for _, arm := range d.Arms {
		if arm.Name == name {
			return arm, nil
		}
	}

	return Arm{}, fmt.Errorf("design %q has no arm %q", d.Name, name)
}
