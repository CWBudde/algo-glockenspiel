package server

// The per-job endpoints. Everything under /api/fit answers about the most
// recent job, which is what a client watching one fit wants; everything under
// /api/fit/jobs answers about a named one, which is what a client looking at a
// campaign's worth of history wants. The two families serve the same job
// objects, so a fit does not read differently depending on which URL found it.

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cwbudde/algo-glockenspiel/internal/fitrun"
)

// traceContentType is what trace.jsonl is: one JSON document per line.
const traceContentType = "application/x-ndjson"

// fitJobListing is one row of the job history.
//
// It is deliberately not a fitSnapshot. A history is read whole, one row per
// fit, and a snapshot carries the metric breakdown and the pinned dimensions
// of every one of them; a client that wants those asks for the job itself.
type fitJobListing struct {
	JobID string   `json:"jobId"`
	State fitState `json:"state"`

	StartedAt  time.Time  `json:"startedAt"`
	FinishedAt *time.Time `json:"finishedAt,omitempty"`

	// BestCost is the best the run has found, which for a job still going is a
	// number that is still moving.
	BestCost float64 `json:"bestCost"`

	// Score is what the run recorded in result.json, and it is absent until
	// there is one. It is the same number as BestCost for a finished run, and
	// that is the point: it says the run shipped that cost rather than merely
	// having reached it, which is what makes a row comparable with a campaign's.
	Score *float64 `json:"score,omitempty"`

	Note      int    `json:"note"`
	Velocity  int    `json:"velocity"`
	Optimizer string `json:"optimizer"`
	Metric    string `json:"metric"`
}

// fitJobList is the job history endpoint's body. It is an object rather than a
// bare array so that a later field -- a total, a cursor -- does not change the
// document's type under every client at once.
type fitJobList struct {
	Jobs []fitJobListing `json:"jobs"`
}

// listing describes one job as a history row.
func (j *fitJob) listing() fitJobListing {
	j.mu.Lock()
	defer j.mu.Unlock()

	row := fitJobListing{
		JobID:     j.id,
		State:     j.state,
		StartedAt: j.startedAt,
		BestCost:  j.progress.BestCost,
		Note:      j.request.Note,
		Velocity:  j.request.Velocity,
		Optimizer: j.request.Optimizer,
		Metric:    j.request.Metric,
	}

	if !j.finishedAt.IsZero() {
		finished := j.finishedAt
		row.FinishedAt = &finished
	}

	if j.summary != nil {
		score := j.summary.Score
		row.Score = &score
	}

	return row
}

// handleFitJobs lists the job history, newest first.
func (s *Server) handleFitJobs(writer http.ResponseWriter, request *http.Request) {
	if !allowReadMethods(writer, request) {
		return
	}

	jobs := s.jobs.history()

	body := fitJobList{Jobs: make([]fitJobListing, 0, len(jobs))}
	for _, job := range jobs {
		body.Jobs = append(body.Jobs, job.listing())
	}

	writeJSON(writer, http.StatusOK, body)
}

// handleFitJob reports one job's state, in the same shape /api/fit answers in.
func (s *Server) handleFitJob(writer http.ResponseWriter, request *http.Request) {
	job := s.jobFor(writer, request)
	if job == nil {
		return
	}

	writeJSON(writer, http.StatusOK, job.snapshot())
}

// handleFitJobPreset returns one job's fitted preset.
func (s *Server) handleFitJobPreset(writer http.ResponseWriter, request *http.Request) {
	job := s.jobFor(writer, request)
	if job == nil {
		return
	}

	fitted, ok := s.presetOf(writer, job)
	if !ok {
		return
	}

	writeFitPreset(writer, job, fitted)
}

// handleFitJobAudio renders one job's fitted preset as a WAV.
func (s *Server) handleFitJobAudio(writer http.ResponseWriter, request *http.Request) {
	job := s.jobFor(writer, request)
	if job == nil {
		return
	}

	fitted, ok := s.presetOf(writer, job)
	if !ok {
		return
	}

	s.writeFitAudio(writer, request, job, fitted)
}

// handleFitJobTrace streams one job's trace, one JSON document per line.
//
// The file is sent as it is on disk rather than parsed and re-encoded: it is
// the record the campaign scores from, and a client comparing a served fit
// with a campaign job has to be reading the same bytes. A running job's trace
// is answered too, and is simply as long as the run has got.
func (s *Server) handleFitJobTrace(writer http.ResponseWriter, request *http.Request) {
	job := s.jobFor(writer, request)
	if job == nil {
		return
	}

	s.serveRunFile(writer, request, job, fitrun.FileTrace, traceContentType,
		fmt.Sprintf("fit %s has written no trace", job.id))
}

// serveRunFile streams one file out of a job's run directory.
//
// The file is sent as it is on disk rather than parsed and re-encoded: these
// are the records the campaign tooling reads, and a client comparing a served
// fit with a campaign job has to be reading the same bytes. A file a run has
// not written yet is a 404 with the reason the caller supplies, because what
// is missing means something different for each of them.
func (s *Server) serveRunFile(
	writer http.ResponseWriter,
	request *http.Request,
	job *fitJob,
	name, contentType, missing string,
) {
	// The path is built from the job's own recorded directory and a constant
	// name, never from the id in the URL: only a job the server itself created
	// or read back has a directory at all, so nothing a client sends reaches
	// the filesystem.
	path := filepath.Join(job.dir, name)

	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			writeJSONError(writer, http.StatusNotFound, missing)

			return
		}

		s.logf("reading %s of %s failed: %v", name, job.id, err)
		writeJSONError(writer, http.StatusInternalServerError, "the file could not be read")

		return
	}

	defer func() {
		_ = file.Close()
	}()

	writer.Header().Set("Content-Type", contentType)
	// A running job's files grow: its trace gains a line per report, and its
	// reference is written before the search starts. The URL does not change
	// when they do, so a cached copy would be an earlier moment of the same
	// run served as the current one.
	writer.Header().Set("Cache-Control", "no-store")

	if request.Method == http.MethodHead {
		return
	}

	if _, err := io.Copy(writer, file); err != nil {
		s.logf("sending %s of %s failed: %v", name, job.id, err)
	}
}

// jobFor resolves the {id} wildcard, answering the request itself when the id
// is unusable or names nothing. A nil return means the caller no longer owns
// the response.
func (s *Server) jobFor(writer http.ResponseWriter, request *http.Request) *fitJob {
	if !allowReadMethods(writer, request) {
		return nil
	}

	id := request.PathValue("id")

	// The id is checked before anything is looked up, so a traversal attempt
	// is refused as the malformed request it is rather than reported as a job
	// that happens not to exist. Nothing here reaches a path either way -- a
	// job's directory is the one the server recorded, not one built from this
	// string -- but a 400 says which of the two mistakes was made.
	if !validJobID(id) {
		writeJSONError(writer, http.StatusBadRequest,
			fmt.Sprintf("%q is not a job id", id))

		return nil
	}

	job := s.jobs.lookup(id)
	if job == nil {
		writeJSONError(writer, http.StatusNotFound,
			fmt.Sprintf("there is no fit %s", id))

		return nil
	}

	return job
}

// validJobID reports whether an id can be a run directory's name.
//
// A job id is one path segment and nothing else: a separator of either kind, a
// dot segment, or anything the local OS would still read as an escape -- a
// drive letter, a reserved device name -- is refused. It mirrors the check
// handleStatic makes on an asset path, for the same reason and with the same
// tools, because a percent-encoded separator survives net/http's own
// normalisation and reappears once PathValue has unescaped it.
func validJobID(id string) bool {
	if id == "" || id == "." || id == ".." {
		return false
	}

	if strings.ContainsAny(id, `/\`) || strings.Contains(id, "..") {
		return false
	}

	return fs.ValidPath(id) && filepath.IsLocal(id)
}
