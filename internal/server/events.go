package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// handleFitEvents streams a job's progress as Server-Sent Events.
//
// The stream is fed from optimizer.Progress by way of fitJob.report -- the same
// callback the CLI hangs checkpointing off -- so nothing inside
// internal/optimizer knows that HTTP exists.
//
// Each event carries a whole fitSnapshot rather than a delta. Progress is a
// sampled quantity: a client that misses one report has not lost information it
// needs, it has one fewer point on a curve, and a self-contained event means a
// reconnecting client is immediately correct rather than correct once it has
// replayed a history the server would otherwise have to keep.
//
// The loop is what makes graceful shutdown work. An SSE response never ends on
// its own, and http.Server.Shutdown waits for active connections, so a stream
// left running would burn the whole ShutdownTimeout on every Ctrl-C and turn a
// clean exit into a five-second hang. Server.Run therefore closes s.shutdown
// before it calls Shutdown, and this select is the other half of that contract.
func (s *Server) handleFitEvents(writer http.ResponseWriter, request *http.Request) {
	if !allowReadMethods(writer, request) {
		return
	}

	job := s.jobs.active()
	if job == nil {
		writeJSONError(writer, http.StatusNotFound, errNoFit.Error())

		return
	}

	// A HEAD probe must not enter the stream. Go suppresses the body for HEAD
	// but not the handler, so a probe against a live fit would sit in the loop
	// below until the fit ended -- up to the whole one-hour budget -- holding a
	// connection that Shutdown then waits for. The headers are the whole of
	// what HEAD asks for.
	if request.Method == http.MethodHead {
		writeEventStreamHeaders(writer)
		writer.WriteHeader(http.StatusOK)

		return
	}

	flusher, ok := writer.(http.Flusher)
	if !ok {
		// Without a flusher every event would sit in a buffer until the
		// response ended, which for a stream that never ends means forever.
		writeJSONError(writer, http.StatusInternalServerError, "this connection cannot stream events")

		return
	}

	// Subscribe before the first snapshot is written, so a report that lands
	// between the two wakes this stream instead of being missed.
	wake, release := job.subscribe()
	defer release()

	writeEventStreamHeaders(writer)
	writer.WriteHeader(http.StatusOK)
	flusher.Flush()

	// The opening event states the current position, so a client that attaches
	// late does not have to wait for the next report to draw anything -- and a
	// client that attaches after the job already finished gets its terminal
	// event immediately rather than blocking on a channel that will never fire.
	if !writeFitEvent(writer, flusher, job.snapshot()) {
		return
	}

	heartbeat := time.NewTicker(fitEventHeartbeat)
	defer heartbeat.Stop()

	for {
		select {
		case <-wake:
			if !writeFitEvent(writer, flusher, job.snapshot()) {
				return
			}
		case <-job.done:
			// Drain one last snapshot rather than trusting that the wake-up
			// for the terminal state was the one just handled: finish sets the
			// state and notifies under the same lock, but the select above may
			// have picked done over a pending wake.
			writeFitEvent(writer, flusher, job.snapshot())

			return
		case <-heartbeat.C:
			// A comment line: valid SSE, ignored by EventSource, and a write
			// that fails when the peer has gone.
			if _, err := io.WriteString(writer, ": keep-alive\n\n"); err != nil {
				return
			}

			flusher.Flush()
		case <-s.shutdown:
			// Say why before going, so the browser's console shows a reason
			// rather than an anonymous dropped connection.
			writeNamedEvent(writer, flusher, "shutdown", []byte(`{"reason":"the server is shutting down"}`))

			return
		case <-request.Context().Done():
			return
		}
	}
}

// writeEventStreamHeaders states what the stream is. It is one function so the
// answer to a HEAD and the answer to a GET cannot drift apart.
func writeEventStreamHeaders(writer http.ResponseWriter) {
	writer.Header().Set("Content-Type", "text/event-stream")
	writer.Header().Set("Cache-Control", "no-store")
	// Named explicitly because a buffering reverse proxy is the one thing that
	// breaks SSE without any error to look at.
	writer.Header().Set("X-Accel-Buffering", "no")
}

// writeFitEvent sends one snapshot. It reports whether the stream is still
// usable; a failed write means the client is gone and the handler must return
// so that its connection stops counting as active.
func writeFitEvent(writer io.Writer, flusher http.Flusher, snapshot fitSnapshot) bool {
	payload, err := json.Marshal(snapshot)
	if err != nil {
		return false
	}

	name := "progress"
	if snapshot.State.terminal() {
		name = "done"
	}

	return writeNamedEvent(writer, flusher, name, payload)
}

// writeNamedEvent frames one SSE event.
//
// The payload is JSON, which cannot contain a raw newline, so it is safe to
// write as a single data line without the multi-line splitting the format
// otherwise requires.
func writeNamedEvent(writer io.Writer, flusher http.Flusher, name string, payload []byte) bool {
	if _, err := fmt.Fprintf(writer, "event: %s\ndata: %s\n\n", name, payload); err != nil {
		return false
	}

	flusher.Flush()

	return true
}
