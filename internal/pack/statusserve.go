package pack

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"
)

// StatusRefreshSeconds is how often the served page asks for the status again.
//
// Five seconds because that is roughly how often a joint fit writes a trace
// line, so the page changes about as often as it asks. Polling faster would
// show the same numbers again; polling slower would make the page feel stuck
// during the minutes a search spends not improving.
const StatusRefreshSeconds = 5

// Handler serves a run directory's progress: a page at /, and the JSON the
// page polls at /status.json, which is also the thing to script against.
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

		page, err := StatusPage(status)
		if err != nil {
			writeStatusError(writer, err)

			return
		}

		writer.Header().Set("Content-Type", "text/html; charset=utf-8")
		writer.Header().Set("Cache-Control", "no-store")
		_, _ = io.WriteString(writer, page)
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
