package pack

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"math"
	"net"
	"net/http"
	"strings"
	"time"
)

// StatusRefreshSeconds is how often the served page reloads itself.
//
// Five seconds because that is roughly how often a joint fit writes a trace
// line, so the page changes about as often as it refreshes. Polling faster
// would show the same numbers again; polling slower would make the page feel
// stuck during the minutes a search spends not improving.
const StatusRefreshSeconds = 5

// Handler serves a run directory's progress: a page at /, the same thing as
// JSON at /status.json.
//
// The status is read per request rather than cached. A run directory is a few
// small files and the alternative is a monitor that can be wrong, which is the
// one thing a monitor may not be -- and a cache would have to be invalidated
// by the very writes it exists to avoid watching.
//
// It is deliberately read-only and carries no controls. Something that could
// stop a fit would need to be as careful about who is asking as
// internal/server is; this is a page to leave open on a phone, and stays worth
// no more scrutiny than a log tail.
func Handler(dir string) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/status.json", func(writer http.ResponseWriter, request *http.Request) {
		status, err := ReadStatus(dir)
		if err != nil {
			writeStatusError(writer, err)

			return
		}

		// Marshalled before anything is written, so an encoding failure can
		// still be reported as one. Encoding straight into the ResponseWriter
		// sends the 200 first and discovers the problem second, which is how a
		// fit with no score yet served a blank page that looked healthy: NaN is
		// not JSON, and by the time the encoder said so the status line was
		// gone. Score marshals as null now, but the ordering is the part that
		// keeps the next such field from doing it again.
		body, err := json.MarshalIndent(status, "", "  ")
		if err != nil {
			writeStatusError(writer, err)

			return
		}

		writer.Header().Set("Content-Type", "application/json; charset=utf-8")
		writer.Header().Set("Cache-Control", "no-store")
		_, _ = writer.Write(body)
	})

	mux.HandleFunc("/", func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/" {
			http.NotFound(writer, request)

			return
		}

		status, err := ReadStatus(dir)
		if err != nil {
			writeStatusError(writer, err)

			return
		}

		writer.Header().Set("Content-Type", "text/html; charset=utf-8")
		writer.Header().Set("Cache-Control", "no-store")
		_, _ = io.WriteString(writer, StatusPage(status))
	})

	return mux
}

func writeStatusError(writer http.ResponseWriter, err error) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	writer.WriteHeader(http.StatusServiceUnavailable)
	_ = json.NewEncoder(writer).Encode(map[string]string{"error": err.Error()})
}

// Serve runs the status server until the context is cancelled.
//
// It prints the URL it is actually listening on rather than the address it was
// asked for, because the useful case is `--serve :0` or a host with more than
// one address, where the two are not the same string.
func Serve(ctx context.Context, dir, addr string, out io.Writer) error {
	if _, err := ReadStatus(dir); err != nil {
		return err
	}

	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}

	server := &http.Server{
		Handler:           Handler(dir),
		ReadHeaderTimeout: 10 * time.Second,
	}

	_, _ = fmt.Fprintf(out, "watching %s at http://%s/\n", dir, listener.Addr())

	done := make(chan error, 1)

	go func() { done <- server.Serve(listener) }()

	select {
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()

		return server.Shutdown(shutdown)
	case err := <-done:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}

		return err
	}
}

// StatusPage renders the progress as one self-contained HTML page.
//
// No stylesheet, no script and no build step: the page is a table and a meta
// refresh, and it is served by a command whose job is to fit bars rather than
// to ship a front end. The web app in web/ is where an interface belongs; this
// is the thing you leave open on a second screen for an hour.
func StatusPage(status *Status) string {
	var out strings.Builder

	title := fmt.Sprintf("%s -- %d/%d", status.Pack, status.Finished, len(status.Notes))
	if status.Joint {
		title = status.Pack + " -- joint fit"
	}

	_, _ = fmt.Fprintf(&out, `<!doctype html>
<html lang="en">
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta http-equiv="refresh" content="%d">
<title>%s</title>
<style>
 body { font: 14px/1.5 system-ui, sans-serif; margin: 2rem auto; max-width: 52rem; padding: 0 1rem;
        color: #1a1a1a; background: #fbfbfa; }
 h1 { font-size: 1.2rem; margin: 0 0 .25rem; }
 p { margin: .25rem 0; color: #555; }
 table { border-collapse: collapse; width: 100%%; margin-top: 1.25rem; }
 th, td { text-align: left; padding: .3rem .6rem; border-bottom: 1px solid #e4e4e1; }
 th { font-weight: 600; color: #555; }
 td.num { text-align: right; font-variant-numeric: tabular-nums; }
 .running { color: #0a6; font-weight: 600; }
 .done { color: #555; }
 .pending { color: #999; }
 .canceled { color: #b00; }
 .bar { display: block; height: .3rem; background: #e4e4e1; border-radius: 2px; margin-top: .2rem; }
 .bar > i { display: block; height: 100%%; background: #0a6; border-radius: 2px; }
 @media (prefers-color-scheme: dark) {
   body { color: #e8e8e6; background: #16181a; }
   p, th { color: #a0a0a0; }
   th, td { border-bottom-color: #2a2d30; }
   .bar { background: #2a2d30; }
 }
</style>
<h1>%s</h1>
`, StatusRefreshSeconds, html.EscapeString(title), html.EscapeString(title))

	if status.Joint {
		_, _ = fmt.Fprintf(&out, "<p>one preset fitted against %s</p>\n", plural(len(status.Notes), "note"))
	} else {
		_, _ = fmt.Fprintf(&out, "<p>%d of %d notes fitted, %d running, %d pending</p>\n",
			status.Finished, len(status.Notes), status.Running, status.Pending)
	}

	if status.Canceled > 0 {
		_, _ = fmt.Fprintf(&out, "<p>%s will be repeated by the next run</p>\n",
			plural(status.Canceled, "cancelled fit"))
	}

	if status.MeanJob > 0 {
		_, _ = fmt.Fprintf(&out, "<p>%s per note, %s spent, about %s left</p>\n",
			roundSecond(status.MeanJob), roundSecond(status.Elapsed), roundSecond(status.Remaining))
	}

	_, _ = io.WriteString(&out,
		"<table><tr><th>note</th><th>state</th><th>evaluations</th>"+
			"<th>best</th><th>current</th><th>elapsed</th></tr>\n")

	for _, note := range status.Notes {
		name := note.Name
		if name == "" {
			name = fmt.Sprintf("%d", note.Note)
		}

		_, _ = fmt.Fprintf(&out,
			"<tr><td>%s</td><td class=\"%s\">%s</td><td class=\"num\">%s%s</td>"+
				"<td class=\"num\">%s</td><td class=\"num\">%s</td><td class=\"num\">%s</td></tr>\n",
			html.EscapeString(name), note.State, note.State,
			evaluationsOf(note), progressBar(note),
			formatScore(note.Best), formatScore(note.Current), roundSecond(note.Elapsed))
	}

	_, _ = fmt.Fprintf(&out, "</table>\n<p>read %s, refreshing every %ds</p>\n</html>\n",
		status.Read.Format(time.RFC1123), StatusRefreshSeconds)

	return out.String()
}

// progressBar draws the share of the budget a running fit has spent. Only a
// running fit gets one: a finished fit's share is always the whole bar, which
// says nothing, and a pending one has no share to draw.
func progressBar(note NoteStatus) string {
	if note.State != StateRunning || note.Budget <= 0 {
		return ""
	}

	share := math.Min(100, 100*float64(note.Evaluations)/float64(note.Budget))

	return fmt.Sprintf(`<span class="bar"><i style="width:%.1f%%"></i></span>`, share)
}

func plural(count int, noun string) string {
	if count == 1 {
		return fmt.Sprintf("%d %s", count, noun)
	}

	return fmt.Sprintf("%d %ss", count, noun)
}
