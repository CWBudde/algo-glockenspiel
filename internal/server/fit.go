package server

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/cwbudde/algo-glockenspiel/assets"
	"github.com/cwbudde/algo-glockenspiel/internal/analysis"
	"github.com/cwbudde/algo-glockenspiel/internal/fitschema"
	"github.com/cwbudde/algo-glockenspiel/internal/optimizer"
	"github.com/cwbudde/algo-glockenspiel/internal/preset"
	"github.com/cwbudde/algo-glockenspiel/internal/synth"
	"github.com/cwbudde/algo-glockenspiel/internal/wavio"
)

// The scalar limits a start request is held to -- iteration and population
// caps, the mayfly and CMA-ES ceilings, the reference's byte and sample-rate
// bounds -- live once, in internal/fitschema, rather than as a constant block
// here and a second, less complete one in internal/browserfit. See that
// package's doc comment for why: the two had already drifted apart before it
// existed.
const (
	// formOverheadBytes is the slack added on top of the reference limit for
	// the multipart envelope and the scalar fields. The fields are a few
	// hundred bytes of numbers; 64 KiB is room to spare without being a second
	// upload channel.
	formOverheadBytes = 64 << 10

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

	// Downmix and WindowMS are the reference loader's policy, as --downmix
	// and --window are on the command line: which channel the fit sees, and
	// a fixed cut length after the onset instead of the strike's own end.
	Downmix  string `json:"downmix"`
	WindowMS int64  `json:"windowMs"`

	// Modes is how the starting modes are chosen, as --modes is on the
	// command line: zero seeds one mode per partial the reference's analysis
	// lists, a positive count seeds the strongest that many, and -1 keeps the
	// starting preset's own modes.
	Modes int `json:"modes"`

	MayflyVariant    string `json:"mayflyVariant"`
	MayflyPreset     string `json:"mayflyPreset"`
	MayflyPopulation int    `json:"mayflyPopulation"`
	MayflySeed       int64  `json:"mayflySeed"`

	MayflyEpochs     int    `json:"mayflyEpochs"`
	MayflyRestarts   int    `json:"mayflyRestarts"`
	MayflyStagnation int    `json:"mayflyStagnation"`
	MayflySelection  string `json:"mayflySelection"`

	// MayflyTargetCost and MayflyNC are pointers because absent and written
	// are different requests. A target cost of zero is a usable target rather
	// than "off", and mayfly.NCAuto is -1 while a written zero means "no
	// crossover at all" -- so collapsing either onto its zero value would turn
	// a field the client left out into a setting it never asked for.
	MayflyTargetCost *float64 `json:"mayflyTargetCost"`
	MayflyNC         *int     `json:"mayflyNc"`
	MayflyNCRatio    *float64 `json:"mayflyNcRatio"`

	// The CMA-ES settings, spelled as the `fit` command spells its flags. A
	// zero lambda takes Hansen's default population, a zero restart count
	// restarts until the budget is spent, and a zero seed asks the backend to
	// pick one.
	CmaesCovariance string  `json:"cmaesCovariance"`
	CmaesLambda     int     `json:"cmaesLambda"`
	CmaesSigma      float64 `json:"cmaesSigma"`
	CmaesSeed       int64   `json:"cmaesSeed"`
	CmaesRestarts   int     `json:"cmaesRestarts"`

	// MayflyTuning is the uploaded tuning document. It arrives as a multipart
	// file part rather than as a scalar field -- exactly as the bounds
	// document does -- so it is never part of the JSON form of a request.
	MayflyTuning *optimizer.MayflyTuning `json:"-"`
}

// timeBudget returns the request's wall-clock budget.
func (r fitRequest) timeBudget() time.Duration {
	return time.Duration(r.TimeBudgetMS) * time.Millisecond
}

// loadOptions is how the request asks the reference to be prepared.
func (r fitRequest) loadOptions() analysis.LoadOptions {
	return analysis.LoadOptions{
		Downmix: analysis.Downmix(r.Downmix),
		Window:  time.Duration(r.WindowMS) * time.Millisecond,
	}
}

// mayflyOptimizerName is the backend name validateFitBackend answers to,
// spelled once so the cadence default below and the switch cannot drift apart.
const mayflyOptimizerName = "mayfly"

// cmaesOptimizerName is the backend name validateFitBackend answers to,
// spelled once for the same reason mayflyOptimizerName is.
const cmaesOptimizerName = "cmaes"

// defaultFitRequest carries the same defaults as the fit command, so a preset
// fitted from the browser and one fitted from the terminal are the same fit.
//
// Every value comes from internal/fitschema.Fields rather than being written
// here a second time: it is the same table cmd/gen-fit-schema reads to write
// web/src/api/fitSchema.generated.ts's DEFAULT_FIT_REQUEST, so the two cannot
// name a different default for the same field.
func defaultFitRequest() fitRequest {
	return fitRequest{
		Note:             fitschema.DefaultInt("note"),
		Velocity:         fitschema.DefaultInt("velocity"),
		Optimizer:        fitschema.DefaultString("optimizer"),
		Metric:           fitschema.DefaultString("metric"),
		MaxIterations:    fitschema.DefaultInt("maxIterations"),
		TimeBudgetMS:     fitschema.DefaultDuration("timeBudget").Milliseconds(),
		ReportEvery:      fitschema.DefaultInt("reportEvery"),
		Align:            fitschema.DefaultBool("align"),
		NormalizeGain:    fitschema.DefaultBool("normalizeGain"),
		Downmix:          string(analysis.DownmixFirst),
		MayflyVariant:    fitschema.DefaultString("mayflyVariant"),
		MayflyPopulation: fitschema.DefaultInt("mayflyPopulation"),
		MayflySeed:       fitschema.DefaultInt64("mayflySeed"),
		MayflyEpochs:     fitschema.DefaultInt("mayflyEpochs"),
		MayflyRestarts:   fitschema.DefaultInt("mayflyRestarts"),
		MayflyStagnation: fitschema.DefaultInt("mayflyStagnation"),
		CmaesCovariance:  fitschema.DefaultString("cmaesCovariance"),
		CmaesSigma:       fitschema.DefaultFloat("cmaesSigma"),
	}
}

// maxReferenceBytes is the configured upload limit, or the default.
func (s *Server) maxReferenceBytes() int64 {
	if s.config.MaxReferenceBytes > 0 {
		return s.config.MaxReferenceBytes
	}

	return fitschema.DefaultMaxReferenceBytes
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

	tuning, err := readMayflyTuningPart(request)
	if err != nil {
		writeJSONError(writer, http.StatusBadRequest, err.Error())

		return
	}

	// The document is decoded and handed to the parser before anything is
	// started, so a malformed one is a 400 on this request rather than a job
	// that claims the single fit slot and then fails: parseFitRequest validates
	// the whole mayfly configuration, document included, through
	// validateFitBackend.
	fitSettings, err := parseFitRequest(request, tuning)
	if err != nil {
		writeJSONError(writer, http.StatusBadRequest, err.Error())

		return
	}

	// The reference is read once the request is parsed, because how it is
	// prepared -- the channel, the cut -- is part of the request.
	loaded, upload, err := readReferencePart(request, limit, fitSettings.loadOptions())
	if err != nil {
		writeJSONError(writer, http.StatusBadRequest, err.Error())

		return
	}

	reference, referenceRate := loaded.Samples, loaded.SampleRate

	// The objective is built here and built again inside fitrun.Run, which is
	// a second measurement of a reference the upload limit keeps small and the
	// price of one answer: every way a request can be impossible -- an unknown
	// metric, a box no codec can encode, a box that leaves the model range --
	// is a 400 on this request rather than a job that claims the slot and then
	// fails. What is kept is the codec: it is the same codec fitrun will build
	// from the same inputs, and it is what turns the encoded result back into
	// the pinned dimensions the snapshot reports.
	objective, _, starting, err := buildObjective(fitSettings, reference, referenceRate, template.Clone(), bounds)
	if err != nil {
		writeJSONError(writer, http.StatusBadRequest, err.Error())

		return
	}

	referenceSeconds := float64(len(reference)) / float64(referenceRate)

	details := jobDetails{
		settings:         fitSettings,
		sampleRate:       referenceRate,
		referenceSeconds: referenceSeconds,
		seededModes:      starting.modes,
		bounds:           bounds,
	}

	job, err := s.jobs.start(details, s.config.WorkDir,
		func(dir string) error {
			return writeUpload(dir, upload)
		},
		func(ctx context.Context, job *fitJob) {
			s.runFit(ctx, job, objective.Codec(), template, bounds)
		})
	if err != nil {
		// errServerStopped is jobs.start refusing a request that arrived after
		// stopAll had already run: the server is shutting down, which is
		// neither the client's fault nor a bug here, and logging it as a
		// generic failure reads as one. Everything else jobs.start can fail
		// with -- the run directory could not be made or claimed -- is the
		// server's own problem.
		if errors.Is(err, errServerStopped) {
			writeJSONError(writer, http.StatusServiceUnavailable, errServerStopped.Error())

			return
		}

		s.logf("starting a fit failed: %v", err)
		writeJSONError(writer, http.StatusInternalServerError, "the fit could not be started")

		return
	}

	writeJSON(writer, http.StatusAccepted, job.snapshot())
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

// handleFitCancel stops a fit and waits for it to actually stop.
//
// Waiting is what makes cancel-then-start deterministic: a client that cancels
// and immediately reads the status must not see the run it just stopped still
// reporting itself as running. Both optimizer backends return promptly on a
// cancelled context, so the wait is short; it is nevertheless bounded by the
// request's own context so a wedged backend cannot pin the handler forever.
//
// A job that is only queued is dropped without ever running, and its done
// channel is closed by the drop, so the same wait answers immediately.
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
	// and another begins must not silently kill the newcomer. It is also how a
	// queued job is cancelled by name rather than by being the most recent
	// one. A job that is not there at all is a conflict rather than a silent
	// success: the client is asking to stop something that does not exist.
	if wanted := request.URL.Query().Get("job"); wanted != "" {
		named := s.jobs.lookup(wanted)
		if named == nil {
			writeJSONError(writer, http.StatusConflict,
				fmt.Sprintf("fit %s is not running; the most recent fit is %s", wanted, job.id))

			return
		}

		job = named
	}

	// A job that has already finished is a conflict rather than a silent
	// success, whichever way it was named. Checked once, after job is settled
	// either way, rather than only inside the branch above: cancelling the
	// most recent job by leaving ?job= off must answer the same as cancelling
	// it by its own id, and a terminal job named implicitly used to succeed
	// with 200 while the same job named explicitly was refused with 409.
	if job.snapshot().State.terminal() {
		writeJSONError(writer, http.StatusConflict, fmt.Sprintf("fit %s is not running", job.id))

		return
	}

	s.jobs.cancel(job)

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

// handleFitPreset returns the best preset the most recent job has produced.
func (s *Server) handleFitPreset(writer http.ResponseWriter, request *http.Request) {
	if !allowReadMethods(writer, request) {
		return
	}

	job, fitted, ok := s.fittedPreset(writer)
	if !ok {
		return
	}

	writeFitPreset(writer, job, fitted)
}

// writeFitPreset answers with one fitted preset. Both preset endpoints go
// through it, so the file a client downloads is named after its job either
// way.
func writeFitPreset(writer http.ResponseWriter, job *fitJob, fitted *preset.Preset) {
	writer.Header().Set("Content-Disposition", `attachment; filename="`+job.id+`.json"`)
	writeJSON(writer, http.StatusOK, fitted)
}

// handleFitAudio renders the most recent job's fitted preset as a WAV.
func (s *Server) handleFitAudio(writer http.ResponseWriter, request *http.Request) {
	if !allowReadMethods(writer, request) {
		return
	}

	job, fitted, ok := s.fittedPreset(writer)
	if !ok {
		return
	}

	s.writeFitAudio(writer, request, job, fitted)
}

// writeFitAudio renders one job's preset and sends it.
//
// Both audio endpoints go through it, which is the whole reason the render is
// done on demand rather than served from the run directory's render.wav: that
// file is one fixed note, velocity and length, while ?note=, ?velocity= and
// ?duration= have to keep working for a job out of the history exactly as they
// do for the live one.
func (s *Server) writeFitAudio(
	writer http.ResponseWriter,
	request *http.Request,
	job *fitJob,
	fitted *preset.Preset,
) {
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
	// passed through. A default that ignored fitschema.MaxRenderSeconds would book
	// exactly the render the cap exists to refuse.
	fallback := math.Min(job.referenceSeconds, fitschema.MaxRenderSeconds)

	duration, err := queryFloat(query, "duration", fallback, 0, fitschema.MaxRenderSeconds)
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

	// The render is shifted and scaled into the space the objective scored it
	// in before it is encoded, exactly as the comparison view is: an A/B
	// audition against /reference is the same control the compare view draws,
	// and playing the raw render would let a listener hear a level and timing
	// difference the objective's own gain- and lag-invariance discarded.
	rendered := scoreAligned(engine.RenderNote(note, velocity, duration), job.metricsCopy())

	// The whole file is built before a byte is sent, so a failure halfway
	// through the encode is a 500 rather than a truncated download the browser
	// reports as successful.
	encoded, err := wavio.MarshalMono(job.sampleRate, rendered)
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

	fitted, ok := s.presetOf(writer, job)
	if !ok {
		return nil, nil, false
	}

	return job, fitted, true
}

// presetOf resolves one job's fitted preset, answering the request itself when
// there is none. A job rebuilt at startup reads it back from its run
// directory, which is why a preset that cannot be read is a 500 and a preset
// that was never produced stays the 409 it always was: the first is the
// server's problem and the second is the client asking too early.
func (s *Server) presetOf(writer http.ResponseWriter, job *fitJob) (*preset.Preset, bool) {
	fitted, err := job.fittedPreset()
	if err == nil {
		return fitted, true
	}

	if !job.snapshot().HasPreset {
		writeJSONError(writer, http.StatusConflict,
			fmt.Sprintf("fit %s has produced no preset yet", job.id))

		return nil, false
	}

	s.logf("reading the preset of %s failed: %v", job.id, err)
	writeJSONError(writer, http.StatusInternalServerError, "the fitted preset could not be read")

	return nil, false
}

// readReferencePart decodes the uploaded reference WAV and prepares it the
// way the fit command does: one channel, cut to its first strike, and
// peak-normalised.
//
// The bytes stay in memory and the part's filename is never touched: it is
// attacker-controlled and there is nothing here it could usefully name. The
// only thing taken from the file is its audio.
func readReferencePart(request *http.Request, limit int64, options analysis.LoadOptions) (*analysis.Reference, []byte, error) {
	file, header, err := request.FormFile("reference")
	if err != nil {
		return nil, nil, errors.New("a reference WAV must be uploaded as the multipart field \"reference\"")
	}

	defer func() {
		_ = file.Close()
	}()

	if header.Size > limit {
		return nil, nil, fmt.Errorf("the reference is %d bytes, above the %d byte limit", header.Size, limit)
	}

	// io.LimitReader rather than trust in header.Size: the size is taken from
	// the part's own headers where the multipart reader recorded it, and the
	// limit has to hold whatever the body actually contains.
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, nil, fmt.Errorf("the reference could not be read: %w", err)
	}

	if int64(len(data)) > limit {
		return nil, nil, fmt.Errorf("the reference exceeds the %d byte limit", limit)
	}

	reference, err := analysis.DecodeReference(bytes.NewReader(data), "the uploaded reference", options)
	if err != nil {
		return nil, nil, err
	}

	sampleRate := reference.SampleRate

	// A WAV header states its sample rate as an unsigned 32-bit number, and
	// nothing downstream questions it: the rate becomes the job's, and the
	// audition endpoint sizes its render as duration * sampleRate. A one-second
	// upload claiming two gigahertz therefore asks RenderNote for 1.2e11
	// samples, which is a 480 GB allocation and an unrecoverable
	// "fatal error: out of memory" rather than a failed request.
	if sampleRate < fitschema.MinReferenceSampleRate || sampleRate > fitschema.MaxReferenceSampleRate {
		return nil, nil, fmt.Errorf("the reference declares a sample rate of %d Hz, outside the supported [%d,%d] range",
			sampleRate, fitschema.MinReferenceSampleRate, fitschema.MaxReferenceSampleRate)
	}

	return reference, data, nil
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

// readMayflyTuningPart decodes the optional mayfly tuning document, returning
// nil when the field is absent so the caller keeps whatever the variant factory
// or the preset already chose.
//
// It is a file part rather than a scalar field for the reason the bounds
// document is one: it is a JSON document a client keeps in a file and uploads
// unchanged, and one shape for both means one thing to explain. Like the bounds
// it is read from bytes in memory and the part's filename is never touched;
// optimizer.LoadMayflyTuning takes a path and is deliberately not reachable
// from here.
func readMayflyTuningPart(request *http.Request) (*optimizer.MayflyTuning, error) {
	file, _, err := request.FormFile("mayflyTuning")

	// Only a missing field means "no tuning". Any other failure -- a part that
	// cannot be opened, say -- would otherwise be answered with a fit against
	// the untuned defaults while the client believed its own document was in
	// force.
	if errors.Is(err, http.ErrMissingFile) {
		//nolint:nilnil // an absent field is not an error: the defaults apply.
		return nil, nil
	}

	if err != nil {
		return nil, fmt.Errorf("the mayfly tuning could not be read: %w", err)
	}

	defer func() {
		_ = file.Close()
	}()

	data, err := io.ReadAll(file)
	if err != nil {
		return nil, fmt.Errorf("the mayfly tuning could not be read: %w", err)
	}

	return optimizer.DecodeMayflyTuning(data, "the uploaded mayfly tuning")
}

// startingPreset is the preset a fit starts from and where its modes came
// from: modes is how many were seeded from the reference's partials, zero
// when the uploaded preset's own were kept.
type startingPreset struct {
	preset *preset.Preset
	modes  int
}

// buildObjective validates the request against the reference and assembles the
// objective, returning it with the encoded starting point and the preset that
// point encodes.
func buildObjective(
	settings fitRequest,
	reference []float32,
	referenceRate int,
	template *preset.Preset,
	bounds *optimizer.ParamBounds,
) (*optimizer.ObjectiveFunction, []float64, startingPreset, error) {
	metric, err := optimizer.ParseMetric(settings.Metric)
	if err != nil {
		return nil, nil, startingPreset{}, err
	}

	// The same sequence the fit command runs: measure the reference once,
	// seed the starting modes from it, and draw the frequency box from its
	// fundamental unless the client sent a box of its own.
	measurement := optimizer.MeasureReference(reference, referenceRate)

	template, seeded, err := optimizer.SeedPreset(template, measurement, settings.Note, settings.Modes)
	if err != nil {
		return nil, nil, startingPreset{}, err
	}

	starting := startingPreset{preset: template, modes: seeded}

	config := optimizer.DefaultObjectiveConfig(metric)
	config.Alignment = optimizer.AlignNone
	config.Analysis = measurement
	config.Bounds.Frequency = optimizer.FrequencyBoundsFor(measurement, referenceRate, template.Note, settings.Note)

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
		return nil, nil, startingPreset{}, err
	}

	encoded, err := objective.Codec().EncodeParams(&template.Parameters)
	if err != nil {
		return nil, nil, startingPreset{}, err
	}

	// The default bounds are widened to contain the template, so the starting
	// point is inside the box already; clamping is belt and braces against a
	// preset whose parameters sit on a boundary. With client-supplied strict
	// bounds it is load bearing: the template may sit outside the requested
	// box, and the backend must not be handed an infeasible starting point.
	clamped, err := objective.Codec().EncodedBounds().Clamp(encoded)
	if err != nil {
		return nil, nil, startingPreset{}, err
	}

	return objective, clamped, starting, nil
}

// validateFitBackend checks that a request names a backend the server
// actually runs, and that the backend's own settings are usable, before a
// reference has even been read.
//
// It used to be named selectOptimizer and return the optimizer.Optimizer it
// built, back when parseFitRequest's caller took that value and ran with it.
// The one remaining call, from parseFitRequest itself, discards it: runFit
// builds its own backend later, from the codec the reference produces, which
// is the block partition this function never had. So there was nothing left
// to select, only a request to check -- and check with a hand-spelled
// "simple", cmaesOptimizerName, mayflyOptimizerName switch that could disagree
// with fitschema.OptimizerNames(), the table cmd/gen-fit-schema reads to build
// the browser's dropdown. Add a backend there without a case here, and the
// dropdown would offer it while this refused it as unsupported before a fit
// ever started. The name is now checked against that same table, so the two
// cannot drift apart; only the backend-specific settings below are still
// spelled per backend, because that validation is CMAES's and mayfly's own to
// own and the table has no room for it.
func validateFitBackend(settings fitRequest) error {
	if !slices.Contains(fitschema.OptimizerNames(), settings.Optimizer) {
		return fmt.Errorf("unsupported optimizer %q", settings.Optimizer)
	}

	// The configuration is built and checked here rather than left to
	// Optimize, so a bad request is a 400 on the start request instead of a
	// job that is accepted, takes the single fit slot, and then fails.
	switch settings.Optimizer {
	case cmaesOptimizerName:
		backend := &optimizer.CMAESOptimizer{
			Covariance:   settings.CmaesCovariance,
			Lambda:       settings.CmaesLambda,
			InitialSigma: settings.CmaesSigma,
			Seed:         settings.CmaesSeed,
			RestartLimit: settings.CmaesRestarts,
		}

		return backend.Validate(settings.MaxIterations)
	case mayflyOptimizerName:
		backend := &optimizer.MayflyOptimizer{
			Variant:    settings.MayflyVariant,
			Preset:     settings.MayflyPreset,
			Population: settings.MayflyPopulation,
			Seed:       settings.MayflySeed,
			Tuning:     settings.mayflyTuning(),
		}

		return backend.Validate(settings.MaxIterations)
	default:
		// "simple" (and any future backend the table lists but this switch has
		// nothing further to say about) needs no settings of its own checked.
		return nil
	}
}

// mayflyTuning folds the request's scalar mayfly settings into a tuning
// document and lays the uploaded document on top of it.
//
// Nothing here writes a scalar onto a configuration itself. Building one
// document and handing it to MayflyOptimizer.Tuning is what keeps each knob
// written in exactly one place, and it makes precedence one sentence: the
// uploaded document wins over the form fields.
func (r fitRequest) mayflyTuning() *optimizer.MayflyTuning {
	// Epochs and restarts are the wrapper's own schedule and their defaults are
	// the wrapper's own, so writing them always costs nothing and keeps the two
	// fields readable straight off the request.
	epochs, restarts := r.MayflyEpochs, r.MayflyRestarts

	scalars := &optimizer.MayflyTuning{
		NC:      r.MayflyNC,
		NCRatio: r.MayflyNCRatio,
		Schedule: &optimizer.MayflySchedule{
			Epochs:   &epochs,
			Restarts: &restarts,
		},
	}

	if r.MayflySelection != "" {
		selection := r.MayflySelection
		scalars.Selection = &selection
	}

	// Stagnation is written only when it was asked for. Zero is the "no
	// stagnation rule" default, so writing it unconditionally would silently
	// switch off a rule a named preset had turned on -- a client that never
	// mentioned the field would be changing it.
	if r.MayflyStagnation > 0 || r.MayflyTargetCost != nil {
		scalars.Convergence = &optimizer.MayflyConvergence{
			TargetCost: r.MayflyTargetCost,
		}

		if r.MayflyStagnation > 0 {
			stagnation := r.MayflyStagnation
			scalars.Convergence.StagnationIterations = &stagnation
		}
	}

	return scalars.Overlay(r.MayflyTuning)
}
