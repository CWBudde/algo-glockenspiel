package server

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/cwbudde/algo-glockenspiel/assets"
	"github.com/cwbudde/algo-glockenspiel/internal/optimizer"
	"github.com/cwbudde/algo-glockenspiel/internal/preset"
	"github.com/cwbudde/algo-glockenspiel/internal/synth"
	"github.com/cwbudde/algo-glockenspiel/internal/wavio"
)

const (
	// defaultMaxReferenceBytes bounds an uploaded reference recording.
	//
	// 16 MiB is about three minutes of 16-bit mono at 44.1 kHz, which is an
	// order of magnitude more than a fit ever wants: the objective renders and
	// scores the whole reference once per candidate evaluation, so a
	// three-minute reference makes a hundred-iteration run take hours. The
	// limit is therefore generous for the intended use and still small enough
	// that the decoded form -- go-audio hands back []int, eight bytes per
	// sample, before it is narrowed to float32 -- stays bounded at roughly
	// 64 MB for a maximal upload.
	defaultMaxReferenceBytes = 16 << 20

	// formOverheadBytes is the slack added on top of the reference limit for
	// the multipart envelope and the scalar fields. The fields are a few
	// hundred bytes of numbers; 64 KiB is room to spare without being a second
	// upload channel.
	formOverheadBytes = 64 << 10

	// maxFitTimeBudget and maxFitIterations bound what one request can book.
	// There is a single fit slot, so an unbounded budget is not merely a long
	// wait for one client: it parks the slot against everyone.
	maxFitTimeBudget = time.Hour
	maxFitIterations = 100_000

	// maxRenderSeconds bounds the audition render. Rendering is linear in
	// duration and the result is held whole in memory before it is sent.
	maxRenderSeconds = 60.0

	// minReferenceSampleRate and maxReferenceSampleRate bound the rate an
	// uploaded WAV may declare. The rate is attacker-controlled -- it is a
	// uint32 in the header that nothing else checks -- and it multiplies every
	// later allocation, so it is bounded at the door rather than at each of the
	// places it is later used. The range covers every rate audio equipment
	// actually produces, from telephony upwards.
	minReferenceSampleRate = 4000
	maxReferenceSampleRate = 192000

	// fitEventHeartbeat is how often an idle SSE stream emits a comment. It
	// keeps an intermediary from reaping a quiet connection, and it is also
	// what makes the server notice a client that vanished without closing --
	// the write fails and the handler returns.
	fitEventHeartbeat = 15 * time.Second
)

// fitRequest is the accepted, validated form of a start request. It mirrors the
// `fit` command's flags, minus everything that names a path: no work directory,
// no checkpoints, no output file. Nothing a client sends is ever used to build
// a filesystem path, which is the cheapest possible answer to path traversal on
// the first write surface this server has.
type fitRequest struct {
	Note          int    `json:"note"`
	Velocity      int    `json:"velocity"`
	Optimizer     string `json:"optimizer"`
	Metric        string `json:"metric"`
	MaxIterations int    `json:"maxIterations"`

	TimeBudgetMS int64 `json:"timeBudgetMs"`
	ReportEvery  int   `json:"reportEvery"`

	Align         bool `json:"align"`
	NormalizeGain bool `json:"normalizeGain"`

	MayflyVariant    string `json:"mayflyVariant"`
	MayflyPopulation int    `json:"mayflyPopulation"`
	MayflySeed       int64  `json:"mayflySeed"`
}

// timeBudget returns the request's wall-clock budget.
func (r fitRequest) timeBudget() time.Duration {
	return time.Duration(r.TimeBudgetMS) * time.Millisecond
}

// defaultFitRequest carries the same defaults as the fit command, so a preset
// fitted from the browser and one fitted from the terminal are the same fit.
func defaultFitRequest() fitRequest {
	return fitRequest{
		Note:             69,
		Velocity:         100,
		Optimizer:        "simple",
		Metric:           string(optimizer.MetricRMS),
		MaxIterations:    100,
		TimeBudgetMS:     (30 * time.Second).Milliseconds(),
		ReportEvery:      10,
		Align:            true,
		NormalizeGain:    false,
		MayflyVariant:    "desma",
		MayflyPopulation: 10,
		MayflySeed:       1,
	}
}

// maxReferenceBytes is the configured upload limit, or the default.
func (s *Server) maxReferenceBytes() int64 {
	if s.config.MaxReferenceBytes > 0 {
		return s.config.MaxReferenceBytes
	}

	return defaultMaxReferenceBytes
}

// handleFitStart accepts a reference recording and starts a fit.
//
// The whole request is bounded before anything is read: http.MaxBytesReader
// caps the body, and ParseMultipartForm is given a memory budget above that cap
// so it never spills a part to a temporary file. An oversized body therefore
// fails at the reader rather than after megabytes have been written somewhere.
func (s *Server) handleFitStart(writer http.ResponseWriter, request *http.Request) {
	if !allowPostMethod(writer, request) {
		return
	}

	limit := s.maxReferenceBytes()
	request.Body = http.MaxBytesReader(writer, request.Body, limit+formOverheadBytes)

	if err := request.ParseMultipartForm(limit + formOverheadBytes); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeJSONError(writer, http.StatusRequestEntityTooLarge,
				fmt.Sprintf("the request exceeds the %d byte reference limit", limit))

			return
		}

		writeJSONError(writer, http.StatusBadRequest, "the request is not a valid multipart form: "+err.Error())

		return
	}

	defer func() {
		_ = request.MultipartForm.RemoveAll()
	}()

	reference, referenceRate, err := readReferencePart(request, limit)
	if err != nil {
		writeJSONError(writer, http.StatusBadRequest, err.Error())

		return
	}

	template, err := readPresetPart(request)
	if err != nil {
		writeJSONError(writer, http.StatusBadRequest, err.Error())

		return
	}

	bounds, err := readBoundsPart(request)
	if err != nil {
		writeJSONError(writer, http.StatusBadRequest, err.Error())

		return
	}

	fitSettings, err := parseFitRequest(request)
	if err != nil {
		writeJSONError(writer, http.StatusBadRequest, err.Error())

		return
	}

	objective, initial, err := buildObjective(fitSettings, reference, referenceRate, template, bounds)
	if err != nil {
		writeJSONError(writer, http.StatusBadRequest, err.Error())

		return
	}

	referenceSeconds := float64(len(reference)) / float64(referenceRate)

	job, err := s.jobs.start(fitSettings, referenceRate, referenceSeconds,
		func(ctx context.Context, job *fitJob) {
			s.runFit(ctx, job, objective, initial, template)
		})
	if err != nil {
		writeJSONError(writer, http.StatusConflict, err.Error())

		return
	}

	writeJSON(writer, http.StatusAccepted, job.snapshot())
}

// runFit is the body of a job. It is the same sequence runFit in
// internal/cli/fit.go performs, with the file-shaped parts removed: no
// checkpoint files, no rendered output on disk, and the fitted preset kept in
// memory for the read endpoints instead of saved to a path.
func (s *Server) runFit(
	ctx context.Context,
	job *fitJob,
	objective *optimizer.ObjectiveFunction,
	initial []float64,
	template *preset.Preset,
) {
	settings := job.request

	backend, err := selectOptimizer(settings)
	if err != nil {
		job.finish(fitFailed, nil, nil, err)

		return
	}

	result, err := backend.Optimize(ctx, objective.Objective(), initial, objective.Codec().EncodedBounds(),
		optimizer.OptimizeOptions{
			MaxIterations: settings.MaxIterations,
			TimeBudget:    settings.timeBudget(),
			ReportEvery:   settings.ReportEvery,
			Report:        job.report,
		})
	if err != nil {
		job.finish(fitFailed, nil, nil, err)

		return
	}

	bestParams, err := objective.Codec().DecodeParams(result.BestParams)
	if err != nil {
		job.finish(fitFailed, nil, result, err)

		return
	}

	fitted := template.Clone()
	fitted.Parameters = *bestParams

	// A cancelled run still produced the best parameters it found, so the
	// preset is kept either way; only the state says which happened. Losing it
	// would make "cancel" mean "throw away the last ten minutes", which is the
	// opposite of what someone watching a cost curve flatten out wants.
	state := fitSucceeded
	if ctx.Err() != nil {
		state = fitCanceled
	}

	job.finish(state, fitted, result, nil)

	s.logf("fit %s finished: state=%s best=%0.6g stop=%s iterations=%d evals=%d",
		job.id, state, result.BestCost, result.StopReason, result.Iterations, result.Evaluations)
}

// handleFitStatus reports the most recent job.
func (s *Server) handleFitStatus(writer http.ResponseWriter, request *http.Request) {
	if !allowReadMethods(writer, request) {
		return
	}

	job := s.jobs.active()
	if job == nil {
		writeJSONError(writer, http.StatusNotFound, errNoFit.Error())

		return
	}

	writeJSON(writer, http.StatusOK, job.snapshot())
}

// handleFitCancel stops the running fit and waits for it to actually stop.
//
// Waiting is what makes cancel-then-start deterministic. If cancel returned as
// soon as the context was cancelled, a client that immediately started a new
// fit would race the old goroutine's last evaluation and get a 409 it could do
// nothing about but retry. Both optimizer backends return promptly on a
// cancelled context, so the wait is short; it is nevertheless bounded by the
// request's own context so a wedged backend cannot pin the handler forever.
func (s *Server) handleFitCancel(writer http.ResponseWriter, request *http.Request) {
	if !allowPostMethod(writer, request) {
		return
	}

	job := s.jobs.active()
	if job == nil {
		writeJSONError(writer, http.StatusNotFound, errNoFit.Error())

		return
	}

	// An explicit job id makes cancel safe against the race it would otherwise
	// have: a client that decides to cancel while the run it is watching ends
	// and another begins must not silently kill the newcomer.
	if wanted := request.URL.Query().Get("job"); wanted != "" && wanted != job.id {
		writeJSONError(writer, http.StatusConflict,
			fmt.Sprintf("the current fit is %s, not %s", job.id, wanted))

		return
	}

	job.cancel()

	select {
	case <-job.done:
		writeJSON(writer, http.StatusOK, job.snapshot())
	case <-request.Context().Done():
		// The caller gave up waiting; the cancellation itself already took
		// effect, so this is not a failure to cancel.
		writeJSON(writer, http.StatusAccepted, job.snapshot())
	case <-s.shutdown:
		writeJSON(writer, http.StatusAccepted, job.snapshot())
	}
}

// handleFitPreset returns the best preset the current job has produced.
func (s *Server) handleFitPreset(writer http.ResponseWriter, request *http.Request) {
	if !allowReadMethods(writer, request) {
		return
	}

	job, fitted, ok := s.fittedPreset(writer)
	if !ok {
		return
	}

	writer.Header().Set("Content-Disposition", `attachment; filename="`+job.id+`.json"`)
	writeJSON(writer, http.StatusOK, fitted)
}

// handleFitAudio renders the fitted preset and returns it as a WAV.
func (s *Server) handleFitAudio(writer http.ResponseWriter, request *http.Request) {
	if !allowReadMethods(writer, request) {
		return
	}

	job, fitted, ok := s.fittedPreset(writer)
	if !ok {
		return
	}

	query := request.URL.Query()

	note, err := queryInt(query, "note", job.request.Note, 0, 127)
	if err != nil {
		writeJSONError(writer, http.StatusBadRequest, err.Error())

		return
	}

	velocity, err := queryInt(query, "velocity", job.request.Velocity, 0, 127)
	if err != nil {
		writeJSONError(writer, http.StatusBadRequest, err.Error())

		return
	}

	// The reference may be longer than the render cap -- the upload limit
	// allows several minutes of audio -- so the fallback is clamped rather than
	// passed through. A default that ignored maxRenderSeconds would book
	// exactly the render the cap exists to refuse.
	fallback := math.Min(job.referenceSeconds, maxRenderSeconds)

	duration, err := queryFloat(query, "duration", fallback, 0, maxRenderSeconds)
	if err != nil {
		writeJSONError(writer, http.StatusBadRequest, err.Error())

		return
	}

	engine, err := synth.NewSynthesizer(fitted, job.sampleRate)
	if err != nil {
		s.logf("render for %s failed: %v", job.id, err)
		writeJSONError(writer, http.StatusInternalServerError, "the fitted preset could not be rendered")

		return
	}

	// The whole file is built before a byte is sent, so a failure halfway
	// through the encode is a 500 rather than a truncated download the browser
	// reports as successful.
	encoded, err := wavio.MarshalMono(job.sampleRate, engine.RenderNote(note, velocity, duration))
	if err != nil {
		s.logf("encoding the render for %s failed: %v", job.id, err)
		writeJSONError(writer, http.StatusInternalServerError, "the render could not be encoded")

		return
	}

	writer.Header().Set("Content-Type", "audio/wav")
	writer.Header().Set("Content-Length", strconv.Itoa(len(encoded)))
	// The render depends on the job, the note and the duration, none of which
	// are in the URL as a content hash, so a cached copy could belong to an
	// earlier fit.
	writer.Header().Set("Cache-Control", "no-store")

	if request.Method == http.MethodHead {
		return
	}

	if _, err := writer.Write(encoded); err != nil {
		s.logf("sending the render for %s failed: %v", job.id, err)
	}
}

// fittedPreset resolves the current job and its preset, answering the request
// itself when either is missing. The second return says whether the caller
// still owns the response.
func (s *Server) fittedPreset(writer http.ResponseWriter) (*fitJob, *preset.Preset, bool) {
	job := s.jobs.active()
	if job == nil {
		writeJSONError(writer, http.StatusNotFound, errNoFit.Error())

		return nil, nil, false
	}

	fitted := job.presetCopy()
	if fitted == nil {
		writeJSONError(writer, http.StatusConflict,
			fmt.Sprintf("fit %s has produced no preset yet", job.id))

		return nil, nil, false
	}

	return job, fitted, true
}

// readReferencePart decodes the uploaded reference WAV.
//
// The bytes stay in memory and the part's filename is never touched: it is
// attacker-controlled and there is nothing here it could usefully name. The
// only thing taken from the file is its audio.
func readReferencePart(request *http.Request, limit int64) ([]float32, int, error) {
	file, header, err := request.FormFile("reference")
	if err != nil {
		return nil, 0, errors.New("a reference WAV must be uploaded as the multipart field \"reference\"")
	}

	defer func() {
		_ = file.Close()
	}()

	if header.Size > limit {
		return nil, 0, fmt.Errorf("the reference is %d bytes, above the %d byte limit", header.Size, limit)
	}

	// io.LimitReader rather than trust in header.Size: the size is taken from
	// the part's own headers where the multipart reader recorded it, and the
	// limit has to hold whatever the body actually contains.
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, 0, fmt.Errorf("the reference could not be read: %w", err)
	}

	if int64(len(data)) > limit {
		return nil, 0, fmt.Errorf("the reference exceeds the %d byte limit", limit)
	}

	samples, sampleRate, err := wavio.DecodeMono(bytes.NewReader(data), "the uploaded reference")
	if err != nil {
		return nil, 0, err
	}

	if len(samples) == 0 {
		return nil, 0, errors.New("the reference contains no samples")
	}

	// A WAV header states its sample rate as an unsigned 32-bit number, and
	// nothing downstream questions it: the rate becomes the job's, and the
	// audition endpoint sizes its render as duration * sampleRate. A one-second
	// upload claiming two gigahertz therefore asks RenderNote for 1.2e11
	// samples, which is a 480 GB allocation and an unrecoverable
	// "fatal error: out of memory" rather than a failed request.
	if sampleRate < minReferenceSampleRate || sampleRate > maxReferenceSampleRate {
		return nil, 0, fmt.Errorf("the reference declares a sample rate of %d Hz, outside the supported [%d,%d] range",
			sampleRate, minReferenceSampleRate, maxReferenceSampleRate)
	}

	return samples, sampleRate, nil
}

// readPresetPart decodes an optional starting preset, falling back to the
// embedded default. Like the reference it is decoded from bytes; preset.Load
// takes a path and is deliberately not reachable from here.
func readPresetPart(request *http.Request) (*preset.Preset, error) {
	file, _, err := request.FormFile("preset")
	if err != nil {
		return assets.DefaultPreset()
	}

	defer func() {
		_ = file.Close()
	}()

	data, err := io.ReadAll(file)
	if err != nil {
		return nil, fmt.Errorf("the starting preset could not be read: %w", err)
	}

	return preset.Decode(data, "the uploaded starting preset")
}

// readBoundsPart decodes the optional search bounds, returning nil when the
// field is absent so the caller keeps the default box. Like the preset it is
// read from bytes in memory and the part's filename is never touched;
// optimizer.LoadParamBounds takes a path and is deliberately not reachable
// from here.
func readBoundsPart(request *http.Request) (*optimizer.ParamBounds, error) {
	file, _, err := request.FormFile("bounds")

	// Only a missing field means "no bounds". Any other failure -- a part that
	// cannot be opened, say -- would otherwise be answered with a fit against
	// the default box while the client believed its own box was in force.
	if errors.Is(err, http.ErrMissingFile) {
		//nolint:nilnil // an absent field is not an error: the default box applies.
		return nil, nil
	}

	if err != nil {
		return nil, fmt.Errorf("the bounds could not be read: %w", err)
	}

	defer func() {
		_ = file.Close()
	}()

	data, err := io.ReadAll(file)
	if err != nil {
		return nil, fmt.Errorf("the bounds could not be read: %w", err)
	}

	bounds, err := optimizer.DecodeParamBounds(data, "the uploaded bounds")
	if err != nil {
		return nil, err
	}

	return &bounds, nil
}

// buildObjective validates the request against the reference and assembles the
// objective, returning it with the encoded starting point.
func buildObjective(
	settings fitRequest,
	reference []float32,
	referenceRate int,
	template *preset.Preset,
	bounds *optimizer.ParamBounds,
) (*optimizer.ObjectiveFunction, []float64, error) {
	metric, err := optimizer.ParseMetric(settings.Metric)
	if err != nil {
		return nil, nil, err
	}

	config := optimizer.DefaultObjectiveConfig(metric)
	config.Alignment = optimizer.AlignNone

	if bounds != nil {
		config.Bounds = *bounds
		// Bounds the client sent are a hard constraint, exactly as --bounds is
		// on the command line: they must not be widened to fit whatever the
		// starting preset happens to contain, or the fitted preset can violate
		// the limits that were asked for.
		config.StrictBounds = true
	}

	if settings.Align {
		config.Alignment = optimizer.AlignOnsetCorrelation
	}

	if settings.NormalizeGain {
		config.Gain = optimizer.GainLeastSquares
	}

	// The sample rate comes from the reference rather than from the request.
	// The CLI takes --sample-rate and errors when it disagrees with the file,
	// which is a useful check for a typed command line and pure friction for an
	// upload: the file already knows, and a mismatch there could only ever be
	// the client restating what it just sent.
	objective, err := optimizer.NewObjectiveFunctionWithConfig(
		reference, template, referenceRate, settings.Note, settings.Velocity, config,
	)
	if err != nil {
		return nil, nil, err
	}

	encoded, err := objective.Codec().EncodeParams(&template.Parameters)
	if err != nil {
		return nil, nil, err
	}

	// The default bounds are widened to contain the template, so the starting
	// point is inside the box already; clamping is belt and braces against a
	// preset whose parameters sit on a boundary. With client-supplied strict
	// bounds it is load bearing: the template may sit outside the requested
	// box, and the backend must not be handed an infeasible starting point.
	clamped, err := objective.Codec().EncodedBounds().Clamp(encoded)
	if err != nil {
		return nil, nil, err
	}

	return objective, clamped, nil
}

// selectOptimizer maps the request's backend name onto an implementation.
func selectOptimizer(settings fitRequest) (optimizer.Optimizer, error) {
	switch settings.Optimizer {
	case "simple":
		return &optimizer.SimpleOptimizer{}, nil
	case "mayfly":
		backend := &optimizer.MayflyOptimizer{
			Variant:    settings.MayflyVariant,
			Population: settings.MayflyPopulation,
			Seed:       settings.MayflySeed,
		}

		// The configuration is built and checked here rather than left to
		// Optimize, so a bad request is a 400 on the start request instead of a
		// job that is accepted, takes the single fit slot, and then fails.
		if err := backend.Validate(settings.MaxIterations); err != nil {
			return nil, err
		}

		return backend, nil
	default:
		return nil, fmt.Errorf("unsupported optimizer %q", settings.Optimizer)
	}
}
