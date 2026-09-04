package optimizer

import (
	"math"
)

const (
	// onsetFrameSize is the window the onset term hears, in samples. It is a
	// power of two so it draws an FFT plan from the same pool the spectral
	// terms use, and 512 samples is 11.6 ms at 44.1 kHz and 10.7 ms at 48 kHz
	// -- the span testdata/reference/README.md calls the attack, where the C5
	// recording holds 4.3 % of its energy and 72 % of that sits between 4.5
	// and 7 kHz.
	//
	// The window is deliberately one frame rather than a spectrogram. A strike
	// is a single event, and what makes it sound struck is how its energy is
	// spread across the spectrum in that one instant, not how that spread
	// evolves -- the evolution is already the two spectral terms' business.
	onsetFrameSize = 512

	// onsetBandsPerOctave aggregates the frame's bins into third-octave bands.
	//
	// Bands, not bins, because at this window a bin is 86 to 94 Hz wide and a
	// click has no line structure to resolve: comparing bin against bin would
	// score the noise between two renders of the same strike. Third-octave is
	// the coarsest split that still separates the 4-8 kHz mallet band from the
	// partials below it, which is the distinction this term exists to make.
	onsetBandsPerOctave = 3

	// onsetLowHz is where the bands start. Below it a 512-sample frame has no
	// resolution left and the strike carries nothing but the fundamental's
	// skirt.
	onsetLowHz = 200.0

	// onsetHighFraction caps the bands below Nyquist, leaving the top of the
	// band where a resampler's rolloff lives out of the term.
	onsetHighFraction = 0.45

	// onsetFloorRangeDB is how far below the reference's loudest band the
	// term still hears. A band 60 dB down in an 11 ms window is not part of
	// the strike, and without the floor an empty band in a synthetic candidate
	// would cost more than every audible band together.
	onsetFloorRangeDB = 60.0
)

// onsetBand is one third-octave band as a half-open range of FFT bins.
type onsetBand struct {
	firstBin int
	lastBin  int
}

// onsetBands lays out third-octave bands over the bins of an onsetFrameSize
// frame, from onsetLowHz to onsetHighFraction of the sample rate.
//
// The bands walk the bins rather than being taken from their edges alone: a
// third-octave band below about 500 Hz is narrower than a bin at this window,
// so several would name the same bin and the low end would count many times
// over. Each band therefore starts after the previous one ends, and a band
// with no bin left to claim is dropped. What survives is a partition of the
// bins that is third-octave where the resolution allows it and one bin wide
// where it does not, which is the log-frequency weighting the term wants.
func onsetBands(sampleRate int) []onsetBand {
	if sampleRate <= 0 {
		return nil
	}

	binHz := float64(sampleRate) / float64(onsetFrameSize)
	highHz := onsetHighFraction * float64(sampleRate)
	lastBin := onsetFrameSize / 2

	var (
		bands []onsetBand
		next  int
	)

	ratio := math.Pow(2, 1/float64(onsetBandsPerOctave))

	for lowHz := onsetLowHz; lowHz < highHz; lowHz *= ratio {
		first := int(math.Round(lowHz / binHz))
		if first < next {
			first = next
		}

		last := int(math.Round(math.Min(lowHz*ratio, highHz)/binHz)) - 1
		if last > lastBin {
			last = lastBin
		}

		if last < first {
			continue
		}

		bands = append(bands, onsetBand{firstBin: first, lastBin: last})
		next = last + 1
	}

	return bands
}

// onsetLevels is the level in dB of each band over the first onsetFrameSize
// samples of a strike, scaled by gain -- a linear amplitude factor, 1 for a
// signal already at the level it should be compared at -- or nil when the
// strike is too short to fill the frame or no FFT plan exists for it.
//
// The gain is applied inside the transform rather than added to the levels
// afterwards, because the magnitude floor underneath them is absolute. See
// spectralScratch.transform for what flooring a candidate in its own scale
// costs.
//
// The frame is not zero-padded to reach the window. A strike shorter than the
// frame has no measurable attack spectrum, and padding one would report the
// silence after it as part of the strike.
func onsetLevels(strike []float32, bands []onsetBand, gain float64) []float64 {
	if len(strike) < onsetFrameSize || len(bands) == 0 {
		return nil
	}

	scratch, release := acquireSpectralScratch(onsetFrameSize)
	if scratch == nil {
		return nil
	}

	defer release()

	scratch.transform(strike[:onsetFrameSize], gain)

	levels := make([]float64, len(bands))

	for i, band := range bands {
		var power float64

		for k := band.firstBin; k <= band.lastBin && k < len(scratch.db); k++ {
			power += math.Pow(10, scratch.db[k]/10)
		}

		if power <= 0 {
			levels[i] = math.Inf(-1)

			continue
		}

		levels[i] = 10 * math.Log10(power)
	}

	return levels
}

// onsetFloorDB is the level the two band sets are clamped to: the reference's
// loudest band less onsetFloorRangeDB.
func onsetFloorDB(levels []float64) float64 {
	loudest := math.Inf(-1)
	for _, level := range levels {
		loudest = math.Max(loudest, level)
	}

	if math.IsInf(loudest, -1) {
		return math.Inf(-1)
	}

	return loudest - onsetFloorRangeDB
}

// onsetError is the RMS dB difference between the candidate's strike spectrum
// at the solved gain and the reference's, band by band, with both clamped to
// the reference's floor.
//
// It is NaN when either side has no measurable strike, which leaves the term
// out of the score rather than scoring it as perfect.
func (r *compositeReference) onsetError(candidate []float32, gainDB float64) float64 {
	if len(r.onsetLevels) == 0 {
		return math.NaN()
	}

	levels := onsetLevels(candidate, r.onsetBands, amplitudeFactor(gainDB))
	if len(levels) != len(r.onsetLevels) {
		return math.NaN()
	}

	var sum float64

	for i := range levels {
		delta := math.Max(levels[i], r.onsetFloor) - math.Max(r.onsetLevels[i], r.onsetFloor)
		sum += delta * delta
	}

	return math.Sqrt(sum / float64(len(levels)))
}
