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
	writeRunDir(t, filepath.Join(dir, "canceled"), `{"note":94}`, "",
		`{"score":0.9,"evaluations":10,"stop_reason":"canceled"}`)

	for name, want := range map[string]string{
		"running":  pack.StateRunning,
		"done":     pack.StateDone,
		"canceled": pack.StateCanceled,
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
