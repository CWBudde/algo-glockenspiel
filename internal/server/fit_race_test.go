package server_test

// The race-detector test. It lives in a file of its own because it is the one
// test that drives every shared structure at once -- the queue, the job store
// and the SSE subscriber set -- and because fit_test.go sits at the file
// length this repository holds a Go file to.

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// The job manager is shared mutable state reached from every request goroutine
// and from the goroutine running the fit. Hammer all of it at once; the
// assertion this test carries is the race detector's, so it is only meaningful
// under -race.
func TestConcurrentAccessIsRaceClean(t *testing.T) {
	srv := newFitServer(t)
	httpServer := httptest.NewServer(srv.Handler())

	t.Cleanup(httpServer.Close)

	reference := referenceWAV(t, testReferenceLength, testSampleRate)

	body, contentType := multipartFit(t, reference, endlessFit())

	started, err := http.Post(httpServer.URL+"/api/fit/start", contentType, body)
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	_, _ = io.Copy(io.Discard, started.Body)
	_ = started.Body.Close()

	var waiting sync.WaitGroup

	stop := make(chan struct{})

	// Readers: status, preset and audio, all racing the reporting goroutine.
	for range 4 {
		waiting.Add(1)

		go func() {
			defer waiting.Done()

			for {
				select {
				case <-stop:
					return
				default:
				}

				for _, target := range []string{"/api/fit", "/api/fit/preset", "/api/fit/audio"} {
					response, err := http.Get(httpServer.URL + target)
					if err != nil {
						return
					}

					_, _ = io.Copy(io.Discard, response.Body)
					_ = response.Body.Close()
				}
			}
		}()
	}

	// Subscribers coming and going, which is what exercises the subscriber map
	// against notifyLocked.
	for range 3 {
		waiting.Add(1)

		go func() {
			defer waiting.Done()

			for {
				select {
				case <-stop:
					return
				default:
				}

				response, err := http.Get(httpServer.URL + "/api/fit/events")
				if err != nil {
					return
				}

				buffer := make([]byte, 256)
				_, _ = response.Body.Read(buffer)
				_ = response.Body.Close()
			}
		}()
	}

	// Readers of the history and of one job by name, which is what exercises
	// the store and the queue against the starts below: the list is copied
	// under the manager's lock while start is appending to it and the worker
	// is popping from it.
	for range 2 {
		waiting.Add(1)

		go func() {
			defer waiting.Done()

			for {
				select {
				case <-stop:
					return
				default:
				}

				response, err := http.Get(httpServer.URL + "/api/fit/jobs")
				if err != nil {
					return
				}

				body, _ := io.ReadAll(response.Body)
				_ = response.Body.Close()

				for _, id := range jobIDsIn(body) {
					for _, suffix := range []string{"", "/preset", "/trace"} {
						named, err := http.Get(httpServer.URL + "/api/fit/jobs/" + id + suffix)
						if err != nil {
							return
						}

						_, _ = io.Copy(io.Discard, named.Body)
						_ = named.Body.Close()
					}
				}
			}
		}()
	}

	// Start requests, which used to be refused and are now queued behind the
	// one that is running.
	for range 2 {
		waiting.Add(1)

		go func() {
			defer waiting.Done()

			for {
				select {
				case <-stop:
					return
				default:
				}

				attempt, contentType := multipartFit(t, reference, endlessFit())

				response, err := http.Post(httpServer.URL+"/api/fit/start", contentType, attempt)
				if err != nil {
					return
				}

				_, _ = io.Copy(io.Discard, response.Body)
				_ = response.Body.Close()
			}
		}()
	}

	time.Sleep(250 * time.Millisecond)

	cancelled, err := http.Post(httpServer.URL+"/api/fit/cancel", "", nil)
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}

	cancelledBody, _ := io.ReadAll(cancelled.Body)
	_ = cancelled.Body.Close()

	close(stop)
	waiting.Wait()

	// The cancel is allowed to answer 200 or -- if one of the racing readers
	// happened to be mid-flight -- to report a state that is already terminal.
	// What it may not do is leave the job running.
	if cancelled.StatusCode != http.StatusOK {
		t.Fatalf("cancel = %d: %s", cancelled.StatusCode, cancelledBody)
	}

	if state := decodeSnapshot(t, cancelledBody).State; state == "running" {
		t.Fatalf("cancel returned with the job still running")
	}
}

// jobIDsIn pulls the job ids out of a history response without decoding the
// whole document. The race test only needs something to ask about, not a
// parsed history, and it reads the body while jobs are being added to it.
func jobIDsIn(body []byte) []string {
	var list struct {
		Jobs []struct {
			JobID string `json:"jobId"`
		} `json:"jobs"`
	}

	if err := json.Unmarshal(body, &list); err != nil {
		return nil
	}

	ids := make([]string, 0, len(list.Jobs))
	for _, job := range list.Jobs {
		ids = append(ids, job.JobID)
	}

	return ids
}
