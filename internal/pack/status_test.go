package pack_test

import (
	"encoding/json"
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

// writeRunDir fakes the parts of a fitrun run directory the status reader
// looks at, in the order fitrun writes them: a config before the search, a
// trace during it, a result after it. Passing an empty string for a file
// leaves it out, which is how a half-written directory is spelled here.
func writeRunDir(tb testing.TB, dir, config, trace, result string) {
	tb.Helper()

	if err := os.MkdirAll(dir, 0o755); err != nil {
		tb.Fatalf("mkdir %s: %v", dir, err)
	}

	for name, body := range map[string]string{
		"config.json": config,
		"trace.jsonl": trace,
		"result.json": result,
	} {
		if body == "" {
			continue
		}

		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			tb.Fatalf("write %s: %v", name, err)
		}
	}
}

// TestStatusTellsTheThreeStatesApart pins the rule the reader shares with Run
// and with campaign's own status: the three states are decided by which files
// exist, because that is the order fitrun writes them and therefore the only
// thing a directory can be asked without a process to ask.
//
// It matters that this agrees with Run. A note the status calls finished and
// Run would repeat, or the other way round, is a monitor that lies about the
// thing it monitors -- and the interesting case is the third state, a
// cancelled fit, which has a result and is still not done.
func TestStatusTellsTheThreeStatesApart(t *testing.T) {
	dir := t.TempDir()

	writeRunDir(t, filepath.Join(dir, "pending"), "", "", "")
	writeRunDir(t, filepath.Join(dir, "running"), `{"note":94,"max_evaluations":24000}`,
		`{"evaluations":105,"elapsed_ms":733,"current":0.5,"best":0.42}`+"\n", "")
	writeRunDir(t, filepath.Join(dir, "done"), `{"note":94}`, "",
		`{"score":0.3,"evaluations":24009,"elapsed_seconds":164,"stop_reason":"max_evaluations"}`)
	// The reason is fitrun's own constant rather than a hand-written string.
	// Writing "canceled" here is what let the readers compare against
	// "canceled" for as long as they did: the fixture and the code agreed with
	// each other and neither agreed with fitrun, which writes
	// "context_canceled".
	writeRunDir(t, filepath.Join(dir, "canceled"), `{"note":94}`, "",
		`{"score":0.9,"evaluations":10,"stop_reason":"`+fitrun.StopReasonCanceled+`"}`)

	// The literal a much older run directory would carry is still read as
	// cancelled, because a repeated fit is cheaper than a wrong table row.
	writeRunDir(t, filepath.Join(dir, "canceled-legacy"), `{"note":94}`, "",
		`{"score":0.9,"evaluations":10,"stop_reason":"canceled"}`)

	for name, want := range map[string]string{
		"running":         pack.StateRunning,
		"done":            pack.StateDone,
		"canceled":        pack.StateCanceled,
		"canceled-legacy": pack.StateCanceled,
	} {
		status, err := pack.ReadStatus(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}

		if got := status.Notes[0].State; got != want {
			t.Errorf("%s reads as %q, want %q", name, got, want)
		}
	}

	// A directory holding none of them is not a run directory at all, and
	// saying "pending" about it would invent a run that was never planned.
	if _, err := pack.ReadStatus(filepath.Join(dir, "pending")); err == nil {
		t.Error("a directory with no run in it reported a status")
	}
}

// TestStatusReadsTheProgressOfARunInFlight is the whole point of the command:
// a fit that has not finished still has to say where it is.
func TestStatusReadsTheProgressOfARunInFlight(t *testing.T) {
	dir := t.TempDir()

	// Several lines, because the reader wants the last one, and one of them
	// carries the extra keys an improving line has -- the tail reader must not
	// care which shape it lands on.
	trace := strings.Join([]string{
		`{"iteration":1,"evaluations":63,"elapsed_ms":9196,"current":0.484,"best":0.484}`,
		`{"iteration":2,"evaluations":105,"elapsed_ms":15346,"current":0.48,"best":0.48,"score":0.48,"terms":{}}`,
		`{"iteration":3,"evaluations":147,"elapsed_ms":21793,"current":0.47,"best":0.43}`,
		"",
	}, "\n")

	writeRunDir(t, dir, `{"note":94,"max_evaluations":24000,"workers":12}`, trace, "")

	status, err := pack.ReadStatus(dir)
	if err != nil {
		t.Fatalf("ReadStatus: %v", err)
	}

	note := status.Notes[0]
	if note.Evaluations != 147 || note.Best != 0.43 || note.Current != 0.47 {
		t.Fatalf("read evaluations %d best %v current %v, want the last line: 147, 0.43, 0.47",
			note.Evaluations, note.Best, note.Current)
	}

	if note.Note != 94 || status.Budget != 24000 || status.Workers != 12 {
		t.Errorf("note %d budget %d workers %d, want them read from config.json",
			note.Note, status.Budget, status.Workers)
	}

	if !strings.Contains(pack.RenderStatus(status), "147/24000") {
		t.Errorf("the rendering does not say how much of the budget is spent:\n%s", pack.RenderStatus(status))
	}
}

// TestStatusSurvivesAHalfWrittenTraceLine is the failure this reader exists to
// not have. The trace is appended to while it is read, so the read can land
// mid-line -- and a monitor that reports an error every few refreshes because
// it caught the writer mid-flush is one nobody leaves open.
func TestStatusSurvivesAHalfWrittenTraceLine(t *testing.T) {
	dir := t.TempDir()

	trace := `{"iteration":1,"evaluations":63,"elapsed_ms":9196,"current":0.484,"best":0.484}` + "\n" +
		`{"iteration":2,"evaluations":105,"elapsed_m`

	writeRunDir(t, dir, `{"note":94}`, trace, "")

	status, err := pack.ReadStatus(dir)
	if err != nil {
		t.Fatalf("ReadStatus: %v", err)
	}

	if got := status.Notes[0].Evaluations; got != 63 {
		t.Errorf("read %d evaluations, want the last complete line's 63", got)
	}
}

// TestStatusReportsAFitThatHasNoNumbersYet: a run directory written but not
// yet traced is running, and its scores are absent rather than zero. Zero is a
// better score than any fit ever reaches, so reporting it would put a fit that
// has done nothing at the top of any comparison drawn from this.
func TestStatusReportsAFitThatHasNoNumbersYet(t *testing.T) {
	dir := t.TempDir()
	writeRunDir(t, dir, `{"note":94}`, "", "")

	status, err := pack.ReadStatus(dir)
	if err != nil {
		t.Fatalf("ReadStatus: %v", err)
	}

	note := status.Notes[0]
	if !note.Best.NaN() || !note.Current.NaN() {
		t.Fatalf("best %v current %v, want both absent", note.Best, note.Current)
	}

	if !strings.Contains(pack.RenderStatus(status), "n/a") {
		t.Error("the rendering invented a number for a fit that has not reported one")
	}
}

// TestTheServedPageAndItsJSONAgree. The page is what someone watches and the
// JSON is what someone scripts against, and the two disagreeing is the sort of
// thing nobody notices until a number is acted on.
func TestTheServedPageAndItsJSONAgree(t *testing.T) {
	dir := t.TempDir()
	writeRunDir(t, dir, `{"note":94,"max_evaluations":24000}`,
		`{"iteration":3,"evaluations":147,"elapsed_ms":21793,"current":0.47,"best":0.43}`+"\n", "")

	handler := pack.Handler(dir)

	jsonResponse := httptest.NewRecorder()
	handler.ServeHTTP(jsonResponse, httptest.NewRequest(http.MethodGet, "/status.json", nil))

	if jsonResponse.Code != http.StatusOK {
		t.Fatalf("/status.json returned %d", jsonResponse.Code)
	}

	var status pack.Status
	if err := json.Unmarshal(jsonResponse.Body.Bytes(), &status); err != nil {
		t.Fatalf("the served JSON does not decode: %v", err)
	}

	if status.Notes[0].Evaluations != 147 {
		t.Errorf("JSON reports %d evaluations, want 147", status.Notes[0].Evaluations)
	}

	pageResponse := httptest.NewRecorder()
	handler.ServeHTTP(pageResponse, httptest.NewRequest(http.MethodGet, "/", nil))

	if pageResponse.Code != http.StatusOK {
		t.Fatalf("/ returned %d", pageResponse.Code)
	}

	body := pageResponse.Body.String()
	for _, want := range []string{"147/24000", "0.430000", "<table>", "http-equiv=\"refresh\""} {
		if !strings.Contains(body, want) {
			t.Errorf("the page lacks %q:\n%s", want, body)
		}
	}
}

// TestTheServerSaysWhenItCannotRead. A run directory that has been deleted or
// mistyped must not render as an empty but healthy-looking page: the reason to
// have a monitor is to be told, and "no notes" and "no such directory" look
// identical in a table.
func TestTheServerSaysWhenItCannotRead(t *testing.T) {
	handler := pack.Handler(filepath.Join(t.TempDir(), "not-a-run"))

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))

	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("a missing directory returned %d, want %d", response.Code, http.StatusServiceUnavailable)
	}
}

// TestTheServedDurationsAreMilliseconds pins the unit against the field name.
//
// time.Duration marshals as nanoseconds, so a field tagged elapsed_ms holding
// one is wrong by a factor of a million and looks entirely plausible while
// being so: a fit thirteen minutes in reads as 972375000000, which is a number
// a reader believes. The name is the contract, and trace.jsonl already writes
// elapsed in milliseconds, so this is also the run directory agreeing with
// itself.
func TestTheServedDurationsAreMilliseconds(t *testing.T) {
	dir := t.TempDir()
	writeRunDir(t, dir, `{"note":94,"max_evaluations":24000}`,
		`{"iteration":3,"evaluations":147,"elapsed_ms":21793,"current":0.47,"best":0.43}`+"\n", "")

	status, err := pack.ReadStatus(dir)
	if err != nil {
		t.Fatalf("ReadStatus: %v", err)
	}

	body, err := json.Marshal(status)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded struct {
		Notes []struct {
			ElapsedMs int64 `json:"elapsed_ms"`
		} `json:"notes"`
	}

	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if decoded.Notes[0].ElapsedMs != 21793 {
		t.Errorf("elapsed_ms is %d, want the trace's own 21793 milliseconds",
			decoded.Notes[0].ElapsedMs)
	}

	// And the round trip keeps it, so a caller decoding into pack.Status reads
	// the same duration back rather than one scaled by a million.
	var round pack.Status
	if err := json.Unmarshal(body, &round); err != nil {
		t.Fatalf("round trip: %v", err)
	}

	if got := round.Notes[0].Elapsed.Duration(); got != 21793*time.Millisecond {
		t.Errorf("round-tripped elapsed is %s, want 21.793s", got)
	}
}

// TestPackTimingSumsWhatEachFitRecorded is the arithmetic behind the "per
// note, spent, about left" line.
//
// It was taken from the modification times of the result files, which is a
// different measurement wearing the same clothes. A pack runs its notes one at
// a time, so after the first finishes there is one result file, the window
// between first and last is zero, and the estimate vanishes at the moment it
// first becomes useful. Once a second finishes the window covers everything
// except the first note's runtime, so the mean is understated for the rest of
// the run -- quietly, by a plausible-looking amount.
//
// Two finished notes of a known length and one in flight is the smallest case
// that catches both halves.
func TestPackTimingSumsWhatEachFitRecorded(t *testing.T) {
	dir := t.TempDir()

	manifest := `{"pack":"p","budget":24000,"workers":12,"jobs":[
	  {"note":84,"name":"c6","dir":"notes/084"},
	  {"note":85,"name":"cs6","dir":"notes/085"},
	  {"note":86,"name":"d6","dir":"notes/086"},
	  {"note":87,"name":"ds6","dir":"notes/087"}]}`

	if err := os.WriteFile(filepath.Join(dir, pack.FileManifest), []byte(manifest), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	writeRunDir(t, filepath.Join(dir, "notes/084"), `{"note":84}`, "",
		`{"score":0.3,"evaluations":24009,"elapsed_seconds":200,"stop_reason":"max_evaluations"}`)
	writeRunDir(t, filepath.Join(dir, "notes/085"), `{"note":85}`, "",
		`{"score":0.3,"evaluations":24009,"elapsed_seconds":100,"stop_reason":"max_evaluations"}`)
	writeRunDir(t, filepath.Join(dir, "notes/086"), `{"note":86,"max_evaluations":24000}`,
		`{"evaluations":6000,"elapsed_ms":30000,"current":0.5,"best":0.42}`+"\n", "")
	writeRunDir(t, filepath.Join(dir, "notes/087"), "", "", "")

	status, err := pack.ReadStatus(dir)
	if err != nil {
		t.Fatalf("ReadStatus: %v", err)
	}

	if status.Finished != 2 || status.Running != 1 || status.Pending != 1 {
		t.Fatalf("finished %d running %d pending %d, want 2/1/1",
			status.Finished, status.Running, status.Pending)
	}

	if got, want := status.MeanJob.Duration(), 150*time.Second; got != want {
		t.Errorf("mean job is %s, want %s -- the mean of 200s and 100s, not a window between result files",
			got, want)
	}

	// 200 + 100 finished, plus the 30 s the note in flight has already spent.
	if got, want := status.Elapsed.Duration(), 330*time.Second; got != want {
		t.Errorf("spent is %s, want %s", got, want)
	}

	// One pending note at the mean, plus what the note in flight still owes.
	if got, want := status.Remaining.Duration(), 270*time.Second; got != want {
		t.Errorf("remaining is %s, want %s", got, want)
	}
}

// TestARunningNotePastTheMeanIsNotOwedNegativeTime. A note that has already
// run longer than the mean is nearly done, and subtracting its elapsed from
// the estimate without a floor would print a remaining time that counts
// backwards.
func TestARunningNotePastTheMeanIsNotOwedNegativeTime(t *testing.T) {
	dir := t.TempDir()

	manifest := `{"pack":"p","budget":24000,"jobs":[
	  {"note":84,"name":"c6","dir":"notes/084"},
	  {"note":85,"name":"cs6","dir":"notes/085"}]}`

	if err := os.WriteFile(filepath.Join(dir, pack.FileManifest), []byte(manifest), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	writeRunDir(t, filepath.Join(dir, "notes/084"), `{"note":84}`, "",
		`{"score":0.3,"evaluations":24009,"elapsed_seconds":10,"stop_reason":"max_evaluations"}`)
	writeRunDir(t, filepath.Join(dir, "notes/085"), `{"note":85,"max_evaluations":24000}`,
		`{"evaluations":23000,"elapsed_ms":600000,"current":0.5,"best":0.42}`+"\n", "")

	status, err := pack.ReadStatus(dir)
	if err != nil {
		t.Fatalf("ReadStatus: %v", err)
	}

	if status.Remaining < 0 {
		t.Fatalf("remaining is %s, want it floored at zero", status.Remaining.Duration())
	}
}
