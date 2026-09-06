package pack_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cwbudde/algo-glockenspiel/internal/fitrun"
	"github.com/cwbudde/algo-glockenspiel/internal/pack"
)

// writeAblationChild fakes one fit of an ablation the way the drivers leave
// it: a "<name>.log" beside the fit's directory, written first, and then
// whatever of the run directory the fit has got to. A preset is the one file
// beyond writeRunDir's three that the ablation reader looks at.
func writeAblationChild(tb testing.TB, dir, name, config, trace, result, preset string) {
	tb.Helper()

	if err := os.MkdirAll(dir, 0o755); err != nil {
		tb.Fatalf("mkdir %s: %v", dir, err)
	}

	if err := os.WriteFile(filepath.Join(dir, name+".log"), []byte("started\n"), 0o644); err != nil {
		tb.Fatalf("write log: %v", err)
	}

	child := filepath.Join(dir, name)

	writeRunDir(tb, child, config, trace, result)

	if preset != "" {
		if err := os.WriteFile(filepath.Join(child, fitrun.FilePreset), []byte(preset), 0o644); err != nil {
			tb.Fatalf("write preset: %v", err)
		}
	}
}

const (
	doneResult  = `{"score":0.32,"evaluations":6033,"elapsed_seconds":684,"stop_reason":"max_evaluations"}`
	betaPreset  = `{"parameters":{"decay_keytrack":0.6262508568036182,"modes":[]}}`
	fixedPreset = `{"parameters":{"modes":[]}}`
)

// TestStatusReadsADirectoryOfJointFits is the shape the beta ablation left
// behind: a directory of joint fits, paired by name, with a log beside each.
// Nothing in it says "ablation"; the reader has to see it from what is there.
func TestStatusReadsADirectoryOfJointFits(t *testing.T) {
	dir := t.TempDir()

	writeAblationChild(t, dir, "b00-fixed", `{"note":84,"max_evaluations":6000,"workers":12}`, "", doneResult, fixedPreset)
	writeAblationChild(t, dir, "b00-beta", `{"note":84,"max_evaluations":6000,"search_decay_keytrack":true}`,
		`{"iteration":40,"evaluations":1800,"elapsed_ms":200000,"current":0.35,"best":0.33}`+"\n", "", "")
	// Started, seeding, no config yet: a log and an empty directory.
	writeAblationChild(t, dir, "b01-fixed", "", "", "", "")

	status, err := pack.ReadStatus(dir)
	if err != nil {
		t.Fatalf("ReadStatus: %v", err)
	}

	if status.Kind != pack.KindAblation {
		t.Fatalf("kind is %q, want %q", status.Kind, pack.KindAblation)
	}

	if status.Finished != 1 || status.Running != 1 || status.Pending != 1 {
		t.Fatalf("finished %d running %d pending %d, want 1/1/1", status.Finished, status.Running, status.Pending)
	}

	if status.Budget != 6000 || status.Workers != 12 {
		t.Errorf("budget %d workers %d, want them taken from the first fit's config", status.Budget, status.Workers)
	}

	var names []string
	for _, note := range status.Notes {
		names = append(names, note.Name)

		if note.Dir != note.Name {
			t.Errorf("%s: dir is %q, want the child's own name", note.Name, note.Dir)
		}
	}

	if got, want := strings.Join(names, " "), "b00-beta b00-fixed b01-fixed"; got != want {
		t.Errorf("fits are %q, want them sorted by name: %q", got, want)
	}

	if running := status.Notes[0]; running.State != pack.StateRunning || running.Budget != 6000 || running.Evaluations != 1800 {
		t.Errorf("the running fit reads as %+v, want running, 1800 of 6000", running)
	}

	if !strings.Contains(pack.RenderStatus(status), "1 of 3 fits done") {
		t.Errorf("the rendering does not count fits:\n%s", pack.RenderStatus(status))
	}
}

// TestTheArmComesFromTheConfigNotTheDirectoryName. The drivers name the
// directories, and a driver that mislabels one would be caught by nobody if
// the monitor took its word for it. The config records what the search
// actually did.
func TestTheArmComesFromTheConfigNotTheDirectoryName(t *testing.T) {
	dir := t.TempDir()

	writeAblationChild(t, dir, "b00-beta", `{"note":84}`, "", doneResult, "")
	writeAblationChild(t, dir, "x", `{"note":84,"search_decay_keytrack":true}`, "", doneResult, "")
	writeAblationChild(t, dir, "y", "", "", "", "")

	status, err := pack.ReadStatus(dir)
	if err != nil {
		t.Fatalf("ReadStatus: %v", err)
	}

	arms := map[string]string{}
	for _, note := range status.Notes {
		arms[note.Name] = note.Arm
	}

	for name, want := range map[string]string{"b00-beta": pack.ArmFixed, "x": pack.ArmBeta, "y": ""} {
		if arms[name] != want {
			t.Errorf("%s reads as arm %q, want %q", name, arms[name], want)
		}
	}
}

// TestTheKeytrackComesFromThePreset: the exponent is the number the ablation
// exists to find, and it is in the preset the beta arm wrote. The fixed arm
// has none, and its JSON must say so by omission rather than by a zero that
// looks like a finding.
func TestTheKeytrackComesFromThePreset(t *testing.T) {
	dir := t.TempDir()

	writeAblationChild(t, dir, "b00-beta", `{"note":84,"search_decay_keytrack":true}`, "", doneResult, betaPreset)
	writeAblationChild(t, dir, "b00-fixed", `{"note":84}`, "", doneResult, fixedPreset)

	status, err := pack.ReadStatus(dir)
	if err != nil {
		t.Fatalf("ReadStatus: %v", err)
	}

	beta, fixed := status.Notes[0], status.Notes[1]

	if beta.Keytrack == nil || *beta.Keytrack < 0.626 || *beta.Keytrack > 0.627 {
		t.Errorf("the beta arm's keytrack is %v, want the preset's 0.6263", beta.Keytrack)
	}

	if fixed.Keytrack != nil {
		t.Errorf("the fixed arm has a keytrack of %v, want none", *fixed.Keytrack)
	}

	body, err := json.Marshal(fixed)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	if strings.Contains(string(body), "keytrack") {
		t.Errorf("the fixed arm's JSON carries a keytrack key:\n%s", body)
	}

	if !strings.Contains(pack.RenderStatus(status), "0.6263") {
		t.Errorf("the rendering does not show the exponent:\n%s", pack.RenderStatus(status))
	}
}

// TestAFitWhoseTraceHasGoneQuietIsStale is the lesson of the SIGSTOPped fit
// that read as healthy for eighty minutes: a running directory whose files
// have stopped changing is the one thing the files can say about a process
// they cannot see.
func TestAFitWhoseTraceHasGoneQuietIsStale(t *testing.T) {
	dir := t.TempDir()

	config := `{"note":84,"max_evaluations":6000}`
	trace := `{"iteration":40,"evaluations":1800,"elapsed_ms":200000,"current":0.35,"best":0.33}` + "\n"

	writeAblationChild(t, dir, "quiet", config, trace, "", "")
	writeAblationChild(t, dir, "fresh", config, trace, "", "")

	old := time.Now().Add(-10 * time.Minute)
	for _, name := range []string{fitrun.FileConfig, fitrun.FileTrace} {
		if err := os.Chtimes(filepath.Join(dir, "quiet", name), old, old); err != nil {
			t.Fatalf("chtimes: %v", err)
		}
	}

	status, err := pack.ReadStatus(dir)
	if err != nil {
		t.Fatalf("ReadStatus: %v", err)
	}

	byName := map[string]pack.NoteStatus{}
	for _, note := range status.Notes {
		byName[note.Name] = note
	}

	quiet, fresh := byName["quiet"], byName["fresh"]

	if !quiet.Stale || quiet.LastWrite == nil || quiet.State != pack.StateRunning {
		t.Errorf("the quiet fit reads as %+v, want running, stale, with a last write", quiet)
	}

	if fresh.Stale || fresh.LastWrite == nil {
		t.Errorf("the fresh fit reads as %+v, want running and not stale", fresh)
	}

	if status.Stale != 1 || status.Running != 2 {
		t.Errorf("stale %d running %d, want 1 stale among 2 running", status.Stale, status.Running)
	}

	rendered := pack.RenderStatus(status)
	if !strings.Contains(rendered, "running (stale") {
		t.Errorf("the rendering does not say the fit is stale:\n%s", rendered)
	}

	// The same rule holds for a single joint fit watched on its own.
	single, err := pack.ReadStatus(filepath.Join(dir, "quiet"))
	if err != nil {
		t.Fatalf("ReadStatus of the joint fit: %v", err)
	}

	if !single.Notes[0].Stale || single.Stale != 1 {
		t.Errorf("watched alone, the quiet fit reads as %+v, want stale", single.Notes[0])
	}
}

// TestThePaceIgnoresAFitThatWasSuspended. elapsed_seconds is wall clock, so a
// fit that sat suspended for an hour recorded the hour as work; one such fit
// in a handful would push a mean, and the ETA drawn from it, out by a third
// of an hour per fit left.
func TestThePaceIgnoresAFitThatWasSuspended(t *testing.T) {
	dir := t.TempDir()

	for name, seconds := range map[string]int{"a": 100, "b": 100, "c": 100, "d": 1000} {
		writeAblationChild(t, dir, name, `{"note":84}`, "",
			fmt.Sprintf(`{"score":0.3,"evaluations":6000,"elapsed_seconds":%d,"stop_reason":"max_evaluations"}`, seconds), "")
	}

	writeAblationChild(t, dir, "e", "", "", "", "")

	status, err := pack.ReadStatus(dir)
	if err != nil {
		t.Fatalf("ReadStatus: %v", err)
	}

	if got, want := status.Pace.Duration(), 100*time.Second; got != want {
		t.Errorf("pace is %s, want %s with the suspended fit left out", got, want)
	}

	if got, want := status.Remaining.Duration(), 100*time.Second; got != want {
		t.Errorf("remaining is %s, want %s: one pending fit at the pace", got, want)
	}

	// Spent is spent, though: the hour passed whether or not it was work.
	if got, want := status.Elapsed.Duration(), 1300*time.Second; got != want {
		t.Errorf("spent is %s, want %s", got, want)
	}
}

// TestTheFirstFitIsReportedBeforeItWritesAConfig covers the window a review
// found open: a driver has created the run directory and its log, and fitrun
// is still seeding and analysing, so no config.json exists anywhere in the
// ablation yet. Reporting nothing there is worse than useless -- the reader
// fails, the served page answers 503, and it does so for the first minute of
// every ablation, which is exactly when someone is watching to see that the
// thing they just started is running.
//
// The pending fit has no config, so it carries no note, budget or arm; that
// is the point of listing it anyway. What it does carry is that it exists.
func TestTheFirstFitIsReportedBeforeItWritesAConfig(t *testing.T) {
	dir := t.TempDir()

	for _, name := range []string{"b00-beta", "b00-fixed"} {
		writeAblationChild(t, dir, name, "", "", "", "")
	}

	status, err := pack.ReadStatus(dir)
	if err != nil {
		t.Fatalf("ReadStatus: %v", err)
	}

	if got, want := len(status.Notes), 2; got != want {
		t.Fatalf("read %d fits, want %d before either has written a config", got, want)
	}

	for _, note := range status.Notes {
		if note.State != pack.StatePending {
			t.Errorf("%s is %q, want %q", note.Name, note.State, pack.StatePending)
		}
	}

	if got, want := status.Notes[0].Name, "b00-beta"; got != want {
		t.Errorf("first fit is %q, want %q: pending fits sort with the rest", got, want)
	}
}

// TestADirectoryOfNothingIsNotARun. An empty directory, a directory of logs
// with no fit behind any of them, and a joint fit's own notes/ folder mistaken
// for a sibling all have to be refused rather than reported as a run with
// nothing in it, because "nothing running" and "wrong directory" look the
// same in a table.
func TestADirectoryOfNothingIsNotARun(t *testing.T) {
	empty := t.TempDir()
	if _, err := pack.ReadStatus(empty); err == nil {
		t.Error("an empty directory reported a status")
	}

	logsOnly := t.TempDir()
	if err := os.WriteFile(filepath.Join(logsOnly, "b00-fixed.log"), []byte("started\n"), 0o644); err != nil {
		t.Fatalf("write log: %v", err)
	}

	if _, err := pack.ReadStatus(logsOnly); err == nil {
		t.Error("a directory of logs and no fits reported a status")
	}

	notesOnly := t.TempDir()
	writeRunDir(t, filepath.Join(notesOnly, "notes"), `{"note":84}`, "", "")

	if _, err := pack.ReadStatus(notesOnly); err == nil {
		t.Error("a directory whose only run is called notes reported a status")
	}
}

// TestTheServedPageFollowsTheAppTheme pins the three-part contract the web
// app's stylesheet documents: the stored choice read before the first paint,
// the system preference honoured unless the choice says light, and the
// explicit choice winning either way -- with the two dark blocks carrying the
// same declarations, since a token defined in only one of them is a colour
// that follows the switch in one direction and not the other.
func TestTheServedPageFollowsTheAppTheme(t *testing.T) {
	dir := t.TempDir()
	writeRunDir(t, dir, `{"note":94,"max_evaluations":24000}`, "", "")

	response := httptest.NewRecorder()
	pack.Handler(dir).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))

	body := response.Body.String()

	for _, want := range []string{
		`localStorage.getItem("algo-glockenspiel:theme")`,
		`:root:not([data-theme="light"])`,
		`:root[data-theme="dark"]`,
		`color-scheme: var(--color-scheme)`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the page lacks %q", want)
		}
	}

	media := blockAfter(t, body, `:root:not([data-theme="light"])`)
	explicit := blockAfter(t, body, `:root[data-theme="dark"]`)

	if media != explicit {
		t.Errorf("the two dark blocks differ:\n%s\n---\n%s", media, explicit)
	}

	if !strings.Contains(media, "--canvas") {
		t.Errorf("the dark block repaints no canvas:\n%s", media)
	}
}

// blockAfter returns the text between the first "{" after the selector and
// its matching "}".
func blockAfter(tb testing.TB, css, selector string) string {
	tb.Helper()

	start := strings.Index(css, selector)
	if start < 0 {
		tb.Fatalf("no %q in the page", selector)
	}

	rest := css[start+len(selector):]

	open := strings.Index(rest, "{")
	end := strings.Index(rest, "}")

	if open < 0 || end < open {
		tb.Fatalf("no block after %q", selector)
	}

	return strings.TrimSpace(rest[open+1 : end])
}

// TestTheServedScriptPollsInPlace pins the behaviours the page has instead of
// a meta refresh, by their spelling in the script: it asks for status.json, it
// stays quiet while hidden and catches up when shown, and it says so rather
// than going blank when an answer does not come.
func TestTheServedScriptPollsInPlace(t *testing.T) {
	dir := t.TempDir()
	writeRunDir(t, dir, `{"note":94,"max_evaluations":24000}`, "", "")

	response := httptest.NewRecorder()
	pack.Handler(dir).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))

	body := response.Body.String()

	for _, want := range []string{
		`fetch("status.json"`,
		`cache: "no-store"`,
		"document.hidden",
		`"visibilitychange"`,
		"retrying",
		"AbortController",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the script lacks %q", want)
		}
	}

	for _, banned := range []string{"location.reload", "http-equiv", "innerHTML"} {
		if strings.Contains(body, banned) {
			t.Errorf("the page still uses %q", banned)
		}
	}
}

// TestTheServedPageCannotBeEndedByARunName. The status is embedded as JSON
// inside a script element, and a run directory called "</script>" is a run
// directory somebody can create. The encoder's escaping is what keeps it
// data; this is the test that would notice the encoder being swapped for one
// that does not.
func TestTheServedPageCannotBeEndedByARunName(t *testing.T) {
	parent := t.TempDir()
	dir := filepath.Join(parent, "</script><b>x")

	writeRunDir(t, dir, `{"note":94}`, "", "")

	page, err := pack.StatusPage(mustReadStatus(t, dir))
	if err != nil {
		t.Fatalf("StatusPage: %v", err)
	}

	if strings.Count(page, "</script>") != 3 {
		t.Errorf("the page has %d script closers, want the page's own three:\n%s",
			strings.Count(page, "</script>"), page)
	}

	if strings.Contains(page, "<b>x") {
		t.Error("the run's name reached the page unescaped")
	}
}

func mustReadStatus(tb testing.TB, dir string) *pack.Status {
	tb.Helper()

	status, err := pack.ReadStatus(dir)
	if err != nil {
		tb.Fatalf("ReadStatus: %v", err)
	}

	return status
}
