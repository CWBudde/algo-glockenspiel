package server

// The comparison payload: one document holding both sides of the A/B a
// results view draws, the reference the objective actually scored and the
// render of the preset the fit produced.
//
// It exists so that the picture is made once, here, from the two signals the
// run itself used. A client that fetched both WAVs and transformed them in
// the browser would be drawing a spectrogram of its own making beside a score
// computed from another, and the two would disagree the first time a floor or
// a frame size moved. The transform is optimizer.ComputeSpectrogram, the
// objective's own.

import (
	"errors"
	"fmt"
	"io/fs"
	"math"
	"net/http"
	"path/filepath"

	"github.com/cwbudde/algo-glockenspiel/internal/fitrun"
	"github.com/cwbudde/algo-glockenspiel/internal/fitschema"
	"github.com/cwbudde/algo-glockenspiel/internal/optimizer"
	"github.com/cwbudde/algo-glockenspiel/internal/synth"
	"github.com/cwbudde/algo-glockenspiel/internal/wavio"
)

const (
	// defaultCompareColumns is a waveform envelope wide enough for a chart on
	// a normal display, and maxCompareColumns is well past any of them. The
	// point of both is that the payload's size follows what was asked for and
	// not how long the reference is: a three-minute upload at 48 kHz is eight
	// million samples, and sending them would be a payload nobody can draw.
	defaultCompareColumns = 1024
	maxCompareColumns     = 4096

	// defaultCompareFrames and maxCompareFrames bound the spectrogram the
	// same way, in columns of time. The cap is far lower because a frame is
	// not one number but compareSpectrogramBins of them.
	defaultCompareFrames = 128
	maxCompareFrames     = 256

	// compareSpectrogramBins is how many frequency rows a spectrogram column
	// is reduced to. A coarse frame is 1025 bins, which is more rows than any
	// display has pixels; at 256 the payload's spectral half is bounded at
	// two by 256 by 256 numbers however long the reference is.
	compareSpectrogramBins = 256

	// compareDBDigits and compareWaveformDigits are how far the numbers are
	// rounded before they are encoded. A dB value is drawn on a scale of
	// tens, and a waveform sample on one of thousandths, so the digits below
	// these are noise that only costs bytes: they roughly halve the document.
	compareDBDigits       = 1
	compareWaveformDigits = 5
)

// fitCompare is the whole comparison: both signals, described the same way,
// with the one sample rate they share.
//
// The two sides are the same rate by construction and not by luck: the render
// is made at the job's own rate, which is the rate the uploaded reference
// declared and the rate the fit was scored at.
type fitCompare struct {
	SampleRate int `json:"sampleRate"`

	// Seconds is the span both sides cover, and it is one number because they
	// cover the same one. A reference longer than the render cap is cut to it
	// rather than compared whole against a shorter render: the two are drawn
	// on one time axis, and a side that spanned three minutes beside one that
	// spanned sixty seconds would put the same column at two different
	// moments without saying so anywhere.
	Seconds float64 `json:"seconds"`

	// Columns and Frames are the resolutions that were asked for, after the
	// caps. What each side was actually built at is on the side itself: a
	// signal with fewer samples than columns, or fewer analysis frames than
	// frames, keeps what it has rather than being stretched to fill them.
	Columns int `json:"columns"`
	Frames  int `json:"frames"`

	// FloorDB is the floor both spectrograms are painted against, and it is
	// the reference's own. It is one number for the same reason Seconds is,
	// and the reason is the objective's: spectrogram.errorDB clamps the
	// candidate and the reference alike to the reference's floor, so a render
	// drawn against a floor of its own would show content the score counted
	// as nothing. A clean synthetic render's own floor is its peak less the
	// dynamic range, while a recording's is its noise estimate plus a margin,
	// so the two are never the same number.
	//
	// Absent when the reference has no transform, which is when it is shorter
	// than one analysis frame; neither side carries a spectrogram then.
	FloorDB *float64 `json:"floorDb,omitempty"`

	Reference fitCompareSide `json:"reference"`
	Render    fitCompareSide `json:"render"`
}

// fitCompareSide is one signal: its waveform envelope and its spectrogram.
// How long it is, and the floor it is painted against, are on the comparison
// itself, because both sides share them.
type fitCompareSide struct {
	Samples int `json:"samples"`

	Waveform fitWaveform `json:"waveform"`

	// Spectrogram is absent when the signal is shorter than one analysis
	// frame, which is exactly when the objective measures no spectral term
	// for it either.
	Spectrogram *fitSpectrogram `json:"spectrogram,omitempty"`
}

// fitWaveform is the envelope a waveform is drawn from: the lowest and the
// highest sample in each column. Both are needed because a column of a decayed
// strike is symmetric about zero and a single magnitude per column would draw
// a shape the signal does not have.
type fitWaveform struct {
	Columns int       `json:"columns"`
	Min     []float64 `json:"min"`
	Max     []float64 `json:"max"`
}

// fitSpectrogram is optimizer.SpectrogramView reduced to a drawable size.
//
// DB is Frames rows of Bins values, each the loudest bin of the frames and
// bins it stands for: the maximum rather than the mean, because a partial
// occupies one bin of a spectrogram whose neighbours hold nothing, and
// averaging it with them would fade out precisely what the picture is of.
type fitSpectrogram struct {
	Frames int `json:"frames"`
	Bins   int `json:"bins"`

	// FrameSize and Hop are the transform's own, in samples, so a reader can
	// work out what one column covers.
	FrameSize int `json:"frameSize"`
	Hop       int `json:"hop"`

	// PeakDB is the loudest value in this reduced matrix, so a display can
	// scale between the comparison's shared floor and it without a pass over
	// the data. It is per side because the two peaks are a real difference
	// between the signals, which the floor is not.
	PeakDB float64 `json:"peakDb"`

	// MaxHz is the top of the frequency axis, which is the Nyquist rate: row
	// r of Bins covers r*MaxHz/Bins upwards.
	MaxHz float64 `json:"maxHz"`

	DB [][]float64 `json:"db"`
}

// handleFitJobCompare answers with both sides of one job's comparison.
func (s *Server) handleFitJobCompare(writer http.ResponseWriter, request *http.Request) {
	job := s.jobFor(writer, request)
	if job == nil {
		return
	}

	query := request.URL.Query()

	columns, err := queryInt(query, "columns", defaultCompareColumns, 1, maxCompareColumns)
	if err != nil {
		writeJSONError(writer, http.StatusBadRequest, err.Error())

		return
	}

	frames, err := queryInt(query, "frames", defaultCompareFrames, 1, maxCompareFrames)
	if err != nil {
		writeJSONError(writer, http.StatusBadRequest, err.Error())

		return
	}

	fitted, ok := s.presetOf(writer, job)
	if !ok {
		return
	}

	reference, ok := s.referenceOf(writer, job)
	if !ok {
		return
	}

	// The comparison spans the reference, which is what the fit was scored
	// over, held to the same cap the audition render is. The reference is cut
	// to that span rather than only the render being clamped to it: a longer
	// reference drawn beside a sixty-second render would put the same column
	// at two different moments on the two sides. Cutting it here is also what
	// bounds the transform below, which runs at full resolution before
	// anything is reduced.
	seconds := math.Min(float64(len(reference))/float64(job.sampleRate), fitschema.MaxRenderSeconds)
	reference = reference[:int(seconds*float64(job.sampleRate))]

	engine, err := synth.NewSynthesizer(fitted, job.sampleRate)
	if err != nil {
		s.logf("render for %s failed: %v", job.id, err)
		writeJSONError(writer, http.StatusInternalServerError, "the fitted preset could not be rendered")

		return
	}

	rendered := engine.RenderNote(job.request.Note, job.request.Velocity, seconds)

	// The reference's transform is taken first because its floor is the one
	// both pictures are painted against, exactly as the objective scores both
	// signals against it.
	referenceView := optimizer.ComputeSpectrogram(reference, job.sampleRate, optimizer.SpectrogramCoarseFrameSize)

	body := fitCompare{
		SampleRate: job.sampleRate,
		Seconds:    seconds,
		Columns:    columns,
		Frames:     frames,
	}

	body.Reference = fitCompareSide{Samples: len(reference), Waveform: waveformEnvelope(reference, columns)}
	body.Render = fitCompareSide{Samples: len(rendered), Waveform: waveformEnvelope(rendered, columns)}

	// No reference transform is no spectral half at all, on either side. The
	// render's own floor is not a substitute for the reference's: a picture
	// painted against it would be the one thing this payload exists to
	// prevent, a display that disagrees with the score. Both sides are the
	// same length, so this is the case where neither has a frame anyway.
	if referenceView != nil {
		floor := roundTo(referenceView.FloorDB, compareDBDigits)
		body.FloorDB = &floor

		renderView := optimizer.ComputeSpectrogram(rendered, job.sampleRate, optimizer.SpectrogramCoarseFrameSize)

		body.Reference.Spectrogram = reduceSpectrogram(referenceView, job.sampleRate, frames, referenceView.FloorDB)
		body.Render.Spectrogram = reduceSpectrogram(renderView, job.sampleRate, frames, referenceView.FloorDB)
	}

	writeJSON(writer, http.StatusOK, body)
}

// handleFitJobReference serves the reference the objective actually scored.
//
// It is reference.wav from the run directory rather than the upload: the
// upload may be stereo, may run past the strike, and is at whatever level it
// was recorded at, while this is the cut, downmixed, peak-normalised mono the
// fit was measured against. An A/B against the render has to be against this
// one, or the difference a listener hears is partly the loader's.
func (s *Server) handleFitJobReference(writer http.ResponseWriter, request *http.Request) {
	job := s.jobFor(writer, request)
	if job == nil {
		return
	}

	s.serveRunFile(writer, request, job, fitrun.FileReference, "audio/wav",
		fmt.Sprintf("fit %s has no reference recording", job.id))
}

// referenceOf reads the reference the run scored, answering the request
// itself when there is none. The second return says whether the caller still
// owns the response.
func (s *Server) referenceOf(writer http.ResponseWriter, job *fitJob) ([]float32, bool) {
	path := filepath.Join(job.dir, fitrun.FileReference)

	samples, rate, err := wavio.LoadMono(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			writeJSONError(writer, http.StatusConflict,
				fmt.Sprintf("fit %s has no reference recording", job.id))

			return nil, false
		}

		s.logf("reading the reference of %s failed: %v", job.id, err)
		writeJSONError(writer, http.StatusInternalServerError, "the reference could not be read")

		return nil, false
	}

	// The file was written by the run at the rate the job records, so a
	// disagreement means the directory is not the one the job thinks it is.
	// Comparing two signals at different rates would put the render's
	// partials at the wrong place on a shared axis, which is worse than an
	// error because it looks like a bad fit.
	if rate != job.sampleRate {
		s.logf("fit %s: reference.wav is at %d Hz but the job ran at %d Hz", job.id, rate, job.sampleRate)
		writeJSONError(writer, http.StatusInternalServerError, "the reference does not match the fit")

		return nil, false
	}

	return samples, true
}

// waveformEnvelope reduces a signal to the lowest and highest sample of each
// of columns equal spans.
//
// A signal with fewer samples than columns keeps one column per sample rather
// than inventing spans that hold nothing: the envelope is then the signal
// itself, which is what a client asking for more detail than exists should
// get.
func waveformEnvelope(signal []float32, columns int) fitWaveform {
	if len(signal) == 0 || columns <= 0 {
		return fitWaveform{Min: []float64{}, Max: []float64{}}
	}

	if columns > len(signal) {
		columns = len(signal)
	}

	envelope := fitWaveform{
		Columns: columns,
		Min:     make([]float64, columns),
		Max:     make([]float64, columns),
	}

	for column := range columns {
		// The span is computed from the column index rather than accumulated,
		// so the last column ends exactly at the last sample however the
		// division rounds and no sample is left out of the picture.
		from := column * len(signal) / columns
		to := (column + 1) * len(signal) / columns

		if to <= from {
			to = from + 1
		}

		lowest, highest := float64(signal[from]), float64(signal[from])

		for _, sample := range signal[from:to] {
			lowest = math.Min(lowest, float64(sample))
			highest = math.Max(highest, float64(sample))
		}

		envelope.Min[column] = roundTo(lowest, compareWaveformDigits)
		envelope.Max[column] = roundTo(highest, compareWaveformDigits)
	}

	return envelope
}

// reduceSpectrogram cuts the objective's own transform down to at most frames
// columns by compareSpectrogramBins rows, painted against floorDB. Nil for a
// signal that was shorter than one frame and so has no transform.
//
// floorDB is the reference's floor for both sides, and every value is held to
// it, which is what spectrogram.errorDB does to the candidate and the
// reference alike. Without it a clean render, whose own floor sits its full
// dynamic range under its peak, would show detail the score treated as
// nothing at all.
func reduceSpectrogram(view *optimizer.SpectrogramView, sampleRate, frames int, floorDB float64) *fitSpectrogram {
	if view == nil {
		return nil
	}

	columns := min(frames, view.Frames)
	rows := min(compareSpectrogramBins, view.Bins)

	reduced := &fitSpectrogram{
		Frames:    columns,
		Bins:      rows,
		FrameSize: view.FrameSize,
		Hop:       view.Hop,
		PeakDB:    math.Inf(-1),
		MaxHz:     float64(sampleRate) / 2,
		DB:        make([][]float64, columns),
	}

	for column := range columns {
		fromFrame := column * view.Frames / columns
		toFrame := (column + 1) * view.Frames / columns

		if toFrame <= fromFrame {
			toFrame = fromFrame + 1
		}

		row := make([]float64, rows)

		for bin := range rows {
			fromBin := bin * view.Bins / rows
			toBin := (bin + 1) * view.Bins / rows

			if toBin <= fromBin {
				toBin = fromBin + 1
			}

			loudest := math.Inf(-1)

			for _, frame := range view.DB[fromFrame:toFrame] {
				for _, value := range frame[fromBin:toBin] {
					loudest = math.Max(loudest, math.Max(value, floorDB))
				}
			}

			row[bin] = roundTo(loudest, compareDBDigits)
			reduced.PeakDB = math.Max(reduced.PeakDB, row[bin])
		}

		reduced.DB[column] = row
	}

	return reduced
}

// roundTo rounds a value to a number of decimal places, so the encoded
// document carries the digits a display can use and not the seventeen a
// float64 prints.
func roundTo(value float64, digits int) float64 {
	scale := math.Pow10(digits)

	return math.Round(value*scale) / scale
}
