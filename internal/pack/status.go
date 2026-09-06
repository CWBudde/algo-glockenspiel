package pack

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/cwbudde/algo-glockenspiel/internal/campaign"
	"github.com/cwbudde/algo-glockenspiel/internal/fitrun"
)

// traceTailBytes is how much of a trace file the progress reader looks at.
//
// A trace line is a few hundred bytes and the reader wants the last one, so
// this is a generous several dozen lines rather than a guess at the file size.
// Reading the tail rather than the whole file is what keeps the cost of asking
// independent of how long the run has been going -- a joint fit's trace passes
// a hundred kilobytes inside an hour, and something refreshing every five
// seconds should not re-read all of it to learn one number.
const traceTailBytes = 16 << 10

// Status is where a fit has got to, read from its directory rather than from
// the process running it.
//
// It exists for the reason campaign.Status does, one scale up: a pack run is
// twenty fits of several minutes each and a joint fit is the better part of an
// hour, and until now the only way to follow either was to tail a log on the
// machine running it. Everything here is read from files the run has already
// written and flushed, so it is safe to call from another shell, or from a
// handler, while the run is in flight. Nothing is opened for writing.
//
// It describes three shapes of run, because from the outside they differ only
// in how many progress streams there are and what each one is called. A pack
// run is a manifest and twenty run directories; a joint fit is one run
// directory that happens to score twenty recordings; an ablation is a
// directory of joint fits, paired by name, that exist to be compared. Kind is
// what tells them apart.
type Status struct {
	Pack string `json:"pack"`
	Dir  string `json:"dir"`
	Kind string `json:"kind"`

	Budget  int `json:"budget"`
	Workers int `json:"workers"`

	Notes    []NoteStatus `json:"notes"`
	Finished int          `json:"finished"`
	Running  int          `json:"running"`
	Pending  int          `json:"pending"`
	Canceled int          `json:"canceled"`

	// Stale counts the running fits whose files have gone quiet; see
	// StaleAfter. They are still counted as running, because that is what the
	// directory says, and this is the number that says the directory may be
	// wrong.
	Stale int `json:"stale"`

	Elapsed   Duration `json:"elapsed_ms"`
	Pace      Duration `json:"pace_ms"`
	Remaining Duration `json:"remaining_ms"`

	// Read is when the directory was inspected, so a page showing this can say
	// how stale it is rather than looking live when the run died ten minutes
	// ago.
	Read time.Time `json:"read"`
}

// Duration is a duration whose JSON is whole milliseconds.
//
// time.Duration marshals as nanoseconds, so a field tagged elapsed_ms that
// held one would be off by a factor of a million while looking entirely
// plausible -- a fit thirteen minutes in reads as 972375000000, which is a
// number a reader believes. Milliseconds because that is the unit trace.jsonl
// already writes elapsed in, and two units for one quantity in one run
// directory is one too many.
type Duration time.Duration

// MarshalJSON writes the duration as whole milliseconds.
func (d Duration) MarshalJSON() ([]byte, error) {
	return []byte(strconv.FormatInt(int64(time.Duration(d)/time.Millisecond), 10)), nil
}

// UnmarshalJSON reads whole milliseconds back.
func (d *Duration) UnmarshalJSON(body []byte) error {
	var millis int64
	if err := json.Unmarshal(body, &millis); err != nil {
		return err
	}

	*d = Duration(time.Duration(millis) * time.Millisecond)

	return nil
}

// Duration returns the value as a time.Duration, for the callers that do
// arithmetic on it rather than serialise it.
func (d Duration) Duration() time.Duration { return time.Duration(d) }

// Score is a fit score that may not exist yet.
//
// It is a named type because NaN is not JSON. Marshalling a NaN fails, and the
// failure is silent in the worst possible place: an http.Handler that has
// already written its status line produces an empty 200, so a monitor watching
// a fit that has not scored anything yet shows a blank page rather than an
// error. Absent scores are written as null, which is the mapping trace.go
// already chose for the same reason.
type Score float64

// MarshalJSON writes an absent score as null rather than failing.
func (s Score) MarshalJSON() ([]byte, error) {
	if math.IsNaN(float64(s)) || math.IsInf(float64(s), 0) {
		return []byte("null"), nil
	}

	return json.Marshal(float64(s))
}

// UnmarshalJSON reads null back as absent, so a round trip through JSON keeps
// the distinction between "no score" and "a score of zero" -- and zero is a
// better score than any fit reaches, so collapsing them would put a fit that
// has done nothing at the top of any ranking drawn from this.
func (s *Score) UnmarshalJSON(body []byte) error {
	if string(body) == "null" {
		*s = Score(math.NaN())

		return nil
	}

	var value float64
	if err := json.Unmarshal(body, &value); err != nil {
		return err
	}

	*s = Score(value)

	return nil
}

// NaN reports whether the score is absent.
func (s Score) NaN() bool { return math.IsNaN(float64(s)) }

// NoteStatus is one fit's share of the progress.
//
// Score is the finished fit's score and Best the best the search has reached
// so far; they are the same number once a fit is done, and only Best exists
// while it is running. Both are NaN when there is nothing to report, which is
// what a reader has to handle anyway, since a fit can finish having never
// improved on a NaN start.
type NoteStatus struct {
	Note  int    `json:"note"`
	Name  string `json:"name"`
	State string `json:"state"`

	// Dir is the run directory relative to Status.Dir, for the shapes whose
	// fits are directories the reader discovered rather than notes a manifest
	// named.
	Dir string `json:"dir,omitempty"`

	// Arm and Keytrack belong to an ablation: which arm of the comparison the
	// fit is, read from its config rather than from its name, and the decay
	// exponent the beta arm found, once it has.
	Arm      string   `json:"arm,omitempty"`
	Keytrack *float64 `json:"keytrack,omitempty"`

	Score       Score    `json:"score"`
	Best        Score    `json:"best"`
	Current     Score    `json:"current"`
	Evaluations int      `json:"evaluations"`
	Budget      int      `json:"budget"`
	Elapsed     Duration `json:"elapsed_ms"`

	// LastWrite is when a running fit last touched its trace, and Stale is
	// whether that was long enough ago to doubt it. Both are absent for a fit
	// that is not running: a finished fit is not expected to write.
	LastWrite *time.Time `json:"last_write,omitempty"`
	Stale     bool       `json:"stale,omitempty"`
}

// The states a fit directory can be in. They are the states campaign and pack
// already resume by rather than a new vocabulary: a directory whose result.json
// records anything but cancellation is one the next run would skip, and one
// holding a config.json and no result.json is the one in flight, because
// fitrun writes the config before the search and the result after it.
const (
	StatePending  = "pending"
	StateRunning  = "running"
	StateDone     = "done"
	StateCanceled = "canceled"
)

// The shapes of directory ReadStatus tells apart.
const (
	KindPack     = "pack"
	KindJoint    = "joint"
	KindAblation = "ablation"
)

// The two arms of an ablation. A fit whose config asked the search for the
// decay exponent is the beta arm; one that left it at the model's law is the
// control it has to beat.
const (
	ArmFixed = "fixed"
	ArmBeta  = "beta"
)

// StaleAfter is how long a running fit's files may go untouched before the
// status calls it stale.
//
// A joint fit writes a trace line every few seconds and result.json within a
// second of the last one, so two minutes is a couple of dozen missed lines: a
// fit that has been suspended, or whose process died without writing a result,
// rather than one between iterations. The files cannot tell those two apart
// and the status does not pretend to; it reports how long the silence has
// lasted and leaves the process table to whoever is at the machine.
const StaleAfter = 2 * time.Minute

// ReadStatus reports the progress of a pack run, of a single joint fit, or of
// a directory of joint fits, whichever the directory holds.
//
// The directory decides: a manifest means a pack run and its jobs are the
// notes; a config or a result at the top means the directory is itself one
// fit's run directory; and a directory holding neither, whose children are
// run directories, is an ablation. Guessing from the argument's name was the
// alternative and is worse -- a joint fit is written wherever --out pointed,
// which is a path the caller chose and this package has no claim on.
func ReadStatus(dir string) (*Status, error) {
	manifest, err := ReadManifest(dir)
	if err == nil {
		return readPackStatus(dir, manifest)
	}

	if !os.IsNotExist(err) && !errorsIsMissingManifest(err) {
		return nil, err
	}

	if isRunDir(dir) {
		return readJointStatus(dir)
	}

	return readAblationStatus(dir)
}

// isRunDir reports whether the directory is one fit's output: fitrun writes
// config.json before the search and result.json after it, so a directory with
// either is a run and one with neither is not, whatever else it holds.
func isRunDir(dir string) bool {
	for _, name := range []string{fitrun.FileConfig, fitrun.FileResult} {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			return true
		}
	}

	return false
}

// errorsIsMissingManifest reports whether the error is "this directory holds no
// manifest" rather than "this manifest is broken".
//
// The distinction matters: falling through to the single-fit reader is right
// for a directory that never had a manifest and wrong for one whose manifest
// will not parse, where the honest answer is the parse error. os.IsNotExist
// does not see through the wrapping ReadManifest adds, so the message is
// matched too.
func errorsIsMissingManifest(err error) bool {
	return err != nil && strings.Contains(err.Error(), "no such file")
}

func readPackStatus(dir string, manifest *Manifest) (*Status, error) {
	status := &Status{
		Pack:    manifest.Pack,
		Dir:     dir,
		Kind:    KindPack,
		Budget:  manifest.Budget,
		Workers: manifest.Workers,
		Notes:   make([]NoteStatus, 0, len(manifest.Jobs)),
		Read:    time.Now(),
	}

	for _, job := range manifest.Jobs {
		note := readNoteStatus(filepath.Join(dir, job.Dir), manifest.Budget, status.Read)
		note.Note = job.Note
		note.Name = job.Name
		note.Dir = job.Dir

		status.Notes = append(status.Notes, note)
		status.count(note)
	}

	sort.Slice(status.Notes, func(i, j int) bool { return status.Notes[i].Note < status.Notes[j].Note })
	status.estimate()

	return status, nil
}

// readJointStatus describes a directory that is one run rather than a pack of
// them. The note list holds a single entry, so a caller -- a handler above all
// -- renders one shape whichever kind of run it was handed.
func readJointStatus(dir string) (*Status, error) {
	now := time.Now()

	note := readNoteStatus(dir, 0, now)
	if note.State == StatePending {
		return nil, fmt.Errorf("%s holds neither a pack manifest nor a fit run directory", dir)
	}

	status := &Status{
		Pack:  filepath.Base(dir),
		Dir:   dir,
		Kind:  KindJoint,
		Notes: []NoteStatus{note},
		Read:  now,
	}

	if config, err := readRunConfig(dir); err == nil {
		note.Note = config.Note
		note.Budget = config.MaxEvaluations
		status.Budget = config.MaxEvaluations
		status.Workers = config.Workers
		status.Notes[0] = note
	}

	status.count(note)
	status.estimate()

	return status, nil
}

func (s *Status) count(note NoteStatus) {
	switch note.State {
	case StateDone:
		s.Finished++
	case StateRunning:
		s.Running++
	case StateCanceled:
		s.Canceled++
	default:
		s.Pending++
	}

	if note.Stale {
		s.Stale++
	}
}

// estimate fills in the timing lines from the fits that have already finished.
//
// It says nothing at all until one has. A mean over zero jobs is not a
// conservative estimate, it is an invented one, and a progress line that
// invents its own remaining time is worse than a progress line that admits it
// does not know yet.
//
// The durations are each fit's own ElapsedSeconds summed, not the window
// between the first and last result file. Result-file timestamps look like the
// same measurement and are not: a sequential pack has one result file after
// the first note finishes, so the window is zero and the estimate disappears
// exactly when it is first wanted -- and from then on it omits the whole of
// the first note's runtime, understating every number it prints. Summing what
// each run recorded about itself is also the only version that stays right if
// the pack is ever run with several notes in flight.
//
// The pace is a median rather than a mean, with the outliers dropped first,
// because ElapsedSeconds is wall clock. A fit that was suspended for an hour
// records the hour as work, and one such fit in a dozen moves a mean by a
// third of an hour per fit while leaving the median where it was. The spent
// figure keeps every second, though: that time passed whether or not the
// search was using it.
func (s *Status) estimate() {
	var (
		done, running time.Duration
		finished      []float64
	)

	for _, note := range s.Notes {
		switch note.State {
		case StateDone:
			done += note.Elapsed.Duration()
			finished = append(finished, note.Elapsed.Duration().Seconds())
		case StateRunning:
			running += note.Elapsed.Duration()
		}
	}

	if len(finished) == 0 {
		return
	}

	s.Pace = Duration(pace(finished) * float64(time.Second))
	s.Elapsed = Duration(done + running)

	// What a note in flight has already spent is subtracted from what it is
	// expected to cost, floored at zero: a note that has run past the pace is
	// nearly finished, not owed negative time.
	remaining := s.Pace.Duration()*time.Duration(s.Pending+s.Running) - running
	if remaining < 0 {
		remaining = 0
	}

	s.Remaining = Duration(remaining)
}

// pace is the median of the durations that are believable: a fit whose clock
// ran more than twice the median of all of them did not spend that time
// computing, and is left out before the median is taken again.
func pace(seconds []float64) float64 {
	median := campaign.Median(seconds)

	kept := make([]float64, 0, len(seconds))

	for _, value := range seconds {
		if value <= 2*median {
			kept = append(kept, value)
		}
	}

	return campaign.Median(kept)
}

// readNoteStatus reads one fit's run directory.
//
// The order of the checks is the order fitrun writes the files, which is what
// makes a partly written directory readable rather than ambiguous: result.json
// exists only after the search, config.json only before it, and neither exists
// before the directory is a run directory at all.
//
// now is when the caller started reading, passed in rather than taken here so
// that every fit of one status is judged stale against the same clock.
func readNoteStatus(dir string, budget int, now time.Time) NoteStatus {
	note := NoteStatus{
		State:   StatePending,
		Score:   Score(math.NaN()),
		Best:    Score(math.NaN()),
		Current: Score(math.NaN()),
		Budget:  budget,
	}

	if summary, err := readSummary(dir); err == nil {
		note.State = StateDone
		if wasCanceled(summary.StopReason) {
			note.State = StateCanceled
		}

		note.Score = Score(summary.Score)
		note.Best = Score(summary.Score)
		note.Current = Score(summary.Score)
		note.Evaluations = summary.Evaluations
		note.Elapsed = Duration(summary.ElapsedSeconds * float64(time.Second))

		return note
	}

	if _, err := os.Stat(filepath.Join(dir, fitrun.FileConfig)); err != nil {
		return note
	}

	note.State = StateRunning

	if progress, ok := readTraceTail(dir); ok {
		note.Best = Score(progress.Best)
		note.Current = Score(progress.Current)
		note.Evaluations = progress.Evaluations
		note.Elapsed = Duration(time.Duration(progress.ElapsedMs) * time.Millisecond)
	}

	markLastWrite(&note, dir, now)

	return note
}

// markLastWrite records when a running fit last wrote anything, and whether
// that is long enough ago to doubt it.
//
// The trace is the file a search touches most often, and the config is the
// one it wrote when it started, so the newer of the two is the last sign of
// life: a fit that has written its config and no trace line yet is judged
// from the config's age, not called stale for having no trace at all.
func markLastWrite(note *NoteStatus, dir string, now time.Time) {
	var last time.Time

	for _, name := range []string{fitrun.FileTrace, fitrun.FileConfig} {
		info, err := os.Stat(filepath.Join(dir, name))
		if err == nil && info.ModTime().After(last) {
			last = info.ModTime()
		}
	}

	if last.IsZero() {
		return
	}

	note.LastWrite = &last
	note.Stale = now.Sub(last) > StaleAfter
}

func readSummary(dir string) (*fitrun.Summary, error) {
	body, err := os.ReadFile(filepath.Join(dir, fitrun.FileResult))
	if err != nil {
		return nil, err
	}

	var summary fitrun.Summary
	if err := json.Unmarshal(body, &summary); err != nil {
		return nil, err
	}

	return &summary, nil
}

// runConfig is the little of a run's config.json this package reads. It is a
// separate, deliberately partial struct rather than fitrun's own: a status
// reader that failed because the config gained a field it does not care about
// would be a monitor that breaks when the thing it monitors improves.
type statusRunConfig struct {
	Note           int `json:"note"`
	MaxEvaluations int `json:"max_evaluations"`
	Workers        int `json:"workers"`

	// SearchDecayKeytrack is what makes a fit the beta arm of an ablation.
	// It is read from the config, not from the directory's name, because the
	// name is the driver's choice and the config is what the search did.
	SearchDecayKeytrack bool `json:"search_decay_keytrack"`
}

func readRunConfig(dir string) (*statusRunConfig, error) {
	body, err := os.ReadFile(filepath.Join(dir, fitrun.FileConfig))
	if err != nil {
		return nil, err
	}

	var config statusRunConfig
	if err := json.Unmarshal(body, &config); err != nil {
		return nil, err
	}

	return &config, nil
}

// traceProgress is the last line of a trace file: where the search had got to
// the last time it wrote anything down.
type traceProgress struct {
	Evaluations int     `json:"evaluations"`
	ElapsedMs   int64   `json:"elapsed_ms"`
	Current     float64 `json:"current"`
	Best        float64 `json:"best"`
}

// readTraceTail reads the last complete line of a run's trace.
//
// The trace is flushed line by line during the search, which is what makes it
// the progress signal rather than log.txt: it is already JSON, so following a
// run costs no parsing of a human-readable format that exists to be read by
// humans. Only the tail is read, and the first line in it is dropped, because
// a tail that begins mid-line is not JSON and a run in flight is being
// appended to while this reads.
func readTraceTail(dir string) (traceProgress, bool) {
	var progress traceProgress

	file, err := os.Open(filepath.Join(dir, fitrun.FileTrace))
	if err != nil {
		return progress, false
	}

	defer func() { _ = file.Close() }()

	info, err := file.Stat()
	if err != nil {
		return progress, false
	}

	offset := info.Size() - traceTailBytes
	if offset < 0 {
		offset = 0
	}

	buffer := make([]byte, info.Size()-offset)

	if _, err := file.ReadAt(buffer, offset); err != nil {
		return progress, false
	}

	lines := strings.Split(strings.TrimRight(string(buffer), "\n"), "\n")
	if offset > 0 && len(lines) > 0 {
		lines = lines[1:]
	}

	for i := len(lines) - 1; i >= 0; i-- {
		if json.Unmarshal([]byte(lines[i]), &progress) == nil && progress.Evaluations > 0 {
			return progress, true
		}
	}

	return progress, false
}

// RenderStatus writes the progress as the few lines someone waiting on a fit
// wants: how far along it is, what is running right now, and how long is left.
func RenderStatus(status *Status) string {
	var out strings.Builder

	_, _ = fmt.Fprintf(&out, "%s: %s\n", status.Pack, headline(status))

	if status.Stale > 0 {
		_, _ = fmt.Fprintf(&out, "%d running fit(s) have written nothing for over %s\n",
			status.Stale, StaleAfter)
	}

	if status.Canceled > 0 {
		_, _ = fmt.Fprintf(&out, "%d cancelled fit(s) will be repeated by the next run\n", status.Canceled)
	}

	if status.Pace > 0 {
		_, _ = fmt.Fprintf(&out, "%s per fit (median), %s spent, about %s left\n",
			roundSecond(status.Pace), roundSecond(status.Elapsed), roundSecond(status.Remaining))
	}

	if status.Kind == KindAblation {
		renderAblationTable(&out, status)
	} else {
		renderNoteTable(&out, status)
	}

	return out.String()
}

// headline is the one line that says how far the run has got, worded for the
// shape it is: a pack fits notes, an ablation runs fits, and a joint fit is
// one thing that is either done or not.
func headline(status *Status) string {
	switch status.Kind {
	case KindJoint:
		return "one joint fit"
	case KindAblation:
		return fmt.Sprintf("%d of %d fits done", status.Finished, len(status.Notes))
	default:
		return fmt.Sprintf("%d of %d notes fitted", status.Finished, len(status.Notes))
	}
}

func renderNoteTable(out *strings.Builder, status *Status) {
	_, _ = fmt.Fprintf(out, "\n| note | state | evaluations | best | current | elapsed |\n")
	_, _ = fmt.Fprintf(out, "| --- | --- | --- | --- | --- | --- |\n")

	for _, note := range status.Notes {
		name := note.Name
		if name == "" {
			name = fmt.Sprintf("%d", note.Note)
		}

		_, _ = fmt.Fprintf(out, "| %s | %s | %s | %s | %s | %s |\n",
			name, stateOf(note, status.Read), evaluationsOf(note),
			formatScore(note.Best), formatScore(note.Current), roundSecond(note.Elapsed))
	}
}

func renderAblationTable(out *strings.Builder, status *Status) {
	_, _ = fmt.Fprintf(out, "\n| fit | arm | state | score | keytrack | evaluations | elapsed |\n")
	_, _ = fmt.Fprintf(out, "| --- | --- | --- | --- | --- | --- | --- |\n")

	for _, note := range status.Notes {
		_, _ = fmt.Fprintf(out, "| %s | %s | %s | %s | %s | %s | %s |\n",
			note.Name, note.Arm, stateOf(note, status.Read), formatScore(note.Best),
			formatKeytrack(note.Keytrack), evaluationsOf(note), roundSecond(note.Elapsed))
	}
}

// stateOf is the state with the silence appended when there is one to
// report: "running (stale 12m0s)" says more than either word alone.
func stateOf(note NoteStatus, read time.Time) string {
	if !note.Stale || note.LastWrite == nil {
		return note.State
	}

	return fmt.Sprintf("%s (stale %s)", note.State, read.Sub(*note.LastWrite).Round(time.Second))
}

func formatKeytrack(value *float64) string {
	if value == nil {
		return "n/a"
	}

	return fmt.Sprintf("%.4f", *value)
}

func evaluationsOf(note NoteStatus) string {
	if note.Budget <= 0 {
		return fmt.Sprintf("%d", note.Evaluations)
	}

	return fmt.Sprintf("%d/%d (%.0f%%)", note.Evaluations, note.Budget,
		100*float64(note.Evaluations)/float64(note.Budget))
}

func formatScore(score Score) string {
	if score.NaN() {
		return "n/a"
	}

	return fmt.Sprintf("%.6f", float64(score))
}

// roundSecond keeps a duration readable: nobody waiting on an hour of fitting
// wants it reported to the nanosecond.
func roundSecond(d Duration) time.Duration {
	return d.Duration().Round(time.Second)
}
