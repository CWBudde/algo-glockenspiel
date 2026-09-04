package optimizer

// The objective's short-time transform, handed out for display.
//
// A picture of a spectrogram beside a score computed from a different one is
// worse than no picture: the eye finds a partial where the number found none
// and the reader trusts the eye. So there is one transform. spectrogram is
// the composite objective's own reference side, floor and all, and this file
// only copies what it already computed into a shape that can leave the
// package.

// The two resolutions the composite objective works at, exported so a caller
// that wants the same picture the objective scored at can name one rather
// than guess a frame size that happens to match.
//
// The coarse size is where the frame-to-frame envelope of each partial is,
// which is what a display of a whole strike shows; the fine one resolves a
// partial's placement and costs four times as many bins to do it.
const (
	SpectrogramCoarseFrameSize = spectralFrameSize
	SpectrogramFineFrameSize   = spectralFineFrameSize
)

// SpectrogramView is one short-time transform: every frame's magnitude in dB
// per bin, plus the noise-aware floor the objective compares above.
//
// DB is Frames rows of Bins values, bin k covering k*SampleRate/FrameSize
// hertz. FloorDB is not a constant: it is the reference's own noise estimate
// plus a margin, or the dynamic range under its loudest bin, whichever is
// higher, so a display that paints everything at or below it as empty paints
// exactly what the score ignores.
type SpectrogramView struct {
	FrameSize int `json:"frameSize"`
	Hop       int `json:"hop"`
	Frames    int `json:"frames"`
	Bins      int `json:"bins"`

	FloorDB float64     `json:"floorDb"`
	DB      [][]float64 `json:"db"`
}

// ComputeSpectrogram transforms a signal exactly as the composite objective
// transforms its reference. Nil when the signal is shorter than one frame or
// no FFT plan exists for the size, which is what the objective does with such
// a signal too: it measures no spectral term at all.
func ComputeSpectrogram(signal []float32, sampleRate, frameSize int) *SpectrogramView {
	taken := newSpectrogram(signal, sampleRate, frameSize)
	if taken == nil {
		return nil
	}

	view := &SpectrogramView{
		FrameSize: taken.frameSize,
		Hop:       taken.hop,
		Frames:    len(taken.frames),
		Bins:      taken.frameSize/2 + 1,
		FloorDB:   taken.floorDB,
		DB:        make([][]float64, len(taken.frames)),
	}

	// Copied rather than aliased: the frames belong to a spectrogram this
	// call is about to drop, but a caller that held a row of it would be
	// holding the objective's own scratch shape and every future change to
	// how that is pooled would reach into a view somebody is drawing.
	for i, frame := range taken.frames {
		view.DB[i] = append([]float64(nil), frame...)
	}

	return view
}
