package optimizer

import (
	"fmt"
	"math"
	"math/cmplx"
	"sort"
	"sync"

	algofft "github.com/cwbudde/algo-fft"
)

const (
	// spectralFrameSize is a power of two on purpose: it keeps algo-fft on its
	// specialised radix-2 codelets. The previous implementation only forced an
	// even length, which could drop the transform onto a naive O(n^2) DFT.
	// 2048 samples is 46 ms at 44.1 kHz, enough resolution (~21.5 Hz) to
	// separate glockenspiel partials while still tracking the decay envelope.
	// It is the legacy spectral metric's only resolution and the composite
	// objective's coarse one.
	spectralFrameSize = 2048

	// spectralFineFrameSize is the composite objective's placement resolution:
	// 8192 points is 5.4 Hz per bin, nine cents at the C5 recording's
	// fundamental, where the 2048-point frame's 21.5 Hz bin is thirty-five.
	spectralFineFrameSize = 8192

	// Magnitudes are normalised to amplitude units, so this floor is -100 dBFS
	// - roughly the PCM16 noise floor. Without it, a bin that is numerically
	// zero in one signal and merely quiet in the other would contribute a
	// meaningless several-hundred-dB error and dominate the whole metric.
	spectralMagnitudeFloor = 1e-5

	// spectralNoiseMarginDB is how far above the reference's own noise
	// estimate the composite objective's floor sits, so that a bin holding
	// nothing but the room in the reference and nothing at all in the
	// candidate contributes nothing.
	spectralNoiseMarginDB = 6.0

	// spectralDynamicRangeDB is how far below the reference's loudest bin the
	// composite objective's floor may sit at most. A synthetic reference has
	// no noise, so its noise floor is the magnitude floor and every bin of
	// numerical residue the candidate carries at -80 dB would be scored
	// against silence; nothing sixty decibels under the strongest partial is
	// what a listener judges the fit by.
	spectralDynamicRangeDB = 60.0
)

// spectralHop is the standard COLA hop for a Hann window: 50 % overlap.
func spectralHop(frameSize int) int {
	return frameSize / 2
}

// SpectralMinSamples returns the shortest signal the spectral metric can score.
func SpectralMinSamples() int {
	return spectralFrameSize
}

// ValidateSpectralInput reports whether the spectral metric can be evaluated for
// a signal of the given length and sample rate.
//
// It exists so that an objective can fail at construction instead of returning
// +Inf for every candidate, which would flatten the cost landscape and leave the
// optimizer wandering with no indication that anything is wrong.
func ValidateSpectralInput(sampleCount, sampleRate int) error {
	if sampleRate <= 0 {
		return fmt.Errorf("spectral metric needs a positive sample rate, got %d", sampleRate)
	}

	if sampleCount < spectralFrameSize {
		return fmt.Errorf("spectral metric needs at least %d samples, got %d", spectralFrameSize, sampleCount)
	}

	if _, err := algofft.NewFastPlanReal64(spectralFrameSize); err != nil {
		return fmt.Errorf("spectral metric has no FFT plan for size %d: %w", spectralFrameSize, err)
	}

	return nil
}

// ComputeSpectralError returns the weighted dB-domain magnitude error between
// two signals, averaged over an STFT of the whole overlap.
//
// The analysis is a multi-frame short-time transform rather than a single
// window over the head of the signal: the decay parameters of a modal model
// only show up in how the partial magnitudes fall off over time, so a metric
// that looks at the first frame alone cannot fit them at all.
func ComputeSpectralError(synth, ref []float32, sampleRate int) float64 {
	return spectralErrorWithGain(synth, ref, sampleRate, 0)
}

// spectralErrorWithGain scores the candidate with a constant dB offset applied.
//
// A scalar time-domain gain is exactly a constant dB shift of every bin in
// every frame, so level normalisation is a single subtraction here rather than
// a rescaled copy of the signal. The offset must be global: normalising each
// frame separately would divide out precisely the frame-to-frame level changes
// the decay parameters produce.
//
// This is the legacy spectral metric: every bin counts, floored at the
// magnitude floor alone. The composite objective's noise-aware form is
// spectrogram.errorDB.
func spectralErrorWithGain(synth, ref []float32, sampleRate int, gainDB float64) float64 {
	sampleCount := minInt(len(synth), len(ref))
	if sampleRate <= 0 || sampleCount < spectralFrameSize {
		return math.Inf(1)
	}

	scratch, release := acquireSpectralScratch(spectralFrameSize)
	if scratch == nil {
		return math.Inf(1)
	}
	defer release()

	weights := spectralWeights(sampleRate, spectralFrameSize)
	refDB := make([]float64, spectralFrameSize/2+1)

	var weightedSum, weightTotal float64

	for start := 0; start+spectralFrameSize <= sampleCount; start += spectralHop(spectralFrameSize) {
		scratch.transform(ref[start : start+spectralFrameSize])
		copy(refDB, scratch.db)
		scratch.transform(synth[start : start+spectralFrameSize])

		for k := 1; k < spectralFrameSize/2; k++ {
			delta := scratch.db[k] + gainDB - refDB[k]
			weight := weights[k]
			weightedSum += weight * delta * delta
			weightTotal += weight
		}
	}

	if weightTotal == 0 {
		return math.Inf(1)
	}

	return math.Sqrt(weightedSum / weightTotal)
}

// spectralScratch owns one FFT plan and its buffers for one frame size.
// algo-fft plans carry internal scratch state and are not safe for concurrent
// use, so each goroutine draws its own from the pool instead of serialising
// on a mutex.
type spectralScratch struct {
	frameSize int
	plan      *algofft.FastPlanReal[float64, complex128]
	frame     []float64
	spectrum  []complex128
	window    []float64
	magScale  float64

	// db holds the last transformed frame's magnitude in dB per bin.
	db []float64
}

// spectralScratchPools holds one sync.Pool per frame size.
var spectralScratchPools sync.Map // map[int]*sync.Pool

// acquireSpectralScratch draws scratch for a frame size from its pool and
// returns it with the function that puts it back. A nil scratch means no FFT
// plan exists for the size.
func acquireSpectralScratch(frameSize int) (*spectralScratch, func()) {
	pool, ok := spectralScratchPools.Load(frameSize)
	if !ok {
		pool, _ = spectralScratchPools.LoadOrStore(frameSize, &sync.Pool{New: func() any {
			scratch, err := newSpectralScratch(frameSize)
			if err != nil {
				return nil
			}

			return scratch
		}})
	}

	scratchPool := pool.(*sync.Pool)

	scratch, ok := scratchPool.Get().(*spectralScratch)
	if !ok || scratch == nil {
		return nil, func() {}
	}

	return scratch, func() { scratchPool.Put(scratch) }
}

func newSpectralScratch(frameSize int) (*spectralScratch, error) {
	plan, err := algofft.NewFastPlanReal64(frameSize)
	if err != nil {
		return nil, err
	}

	window := make([]float64, frameSize)
	windowSum := 0.0

	for i := range window {
		window[i] = 0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/float64(frameSize))
		windowSum += window[i]
	}

	return &spectralScratch{
		frameSize: frameSize,
		plan:      plan,
		frame:     make([]float64, frameSize),
		spectrum:  make([]complex128, frameSize/2+1),
		window:    window,
		// Normalise to amplitude units so the dB floor has a physical meaning
		// independent of frame size.
		magScale: 2 / windowSum,
		db:       make([]float64, frameSize/2+1),
	}, nil
}

// transform windows one frame, transforms it and leaves its magnitude in dB
// per bin in db.
func (s *spectralScratch) transform(frame []float32) {
	for i, coefficient := range s.window {
		s.frame[i] = float64(frame[i]) * coefficient
	}

	s.plan.Forward(s.spectrum, s.frame)

	for k := range s.db {
		s.db[k] = linToDB(cmplx.Abs(s.spectrum[k]) * s.magScale)
	}
}

func linToDB(x float64) float64 {
	if x < spectralMagnitudeFloor {
		x = spectralMagnitudeFloor
	}

	return 20 * math.Log10(x)
}

// spectrogram is the reference side of the composite objective's spectral
// term at one resolution: every frame's magnitude in dB, transformed once at
// construction, plus the floor the comparison is made above.
//
// The floor is the reference's own noise estimate plus spectralNoiseMarginDB,
// never below the magnitude floor. A bin below it in both signals contributes
// nothing; a bin above it in either is compared with both clamped to it. That
// is what stops a modal model being scored on its failure to synthesise the
// room: for the C5 recording most bins of most frames hold nothing but the
// floor, and under the legacy metric they outvoted the partials.
type spectrogram struct {
	frameSize int
	hop       int
	frames    [][]float64
	floorDB   float64
	weights   []float64
}

// newSpectrogram transforms the reference at one frame size. Nil when the
// reference is shorter than a frame.
func newSpectrogram(reference []float32, sampleRate, frameSize int) *spectrogram {
	if sampleRate <= 0 || len(reference) < frameSize {
		return nil
	}

	scratch, release := acquireSpectralScratch(frameSize)
	if scratch == nil {
		return nil
	}
	defer release()

	weights := spectralWeights(sampleRate, frameSize)
	hop := spectralHop(frameSize)

	var (
		frames [][]float64
		levels []float64
		peakDB = math.Inf(-1)
	)

	for start := 0; start+frameSize <= len(reference); start += hop {
		scratch.transform(reference[start : start+frameSize])
		frame := append([]float64(nil), scratch.db...)
		frames = append(frames, frame)

		for k := 1; k < frameSize/2; k++ {
			if weights[k] > 0 {
				levels = append(levels, frame[k])
				peakDB = math.Max(peakDB, frame[k])
			}
		}
	}

	return &spectrogram{
		frameSize: frameSize,
		hop:       hop,
		frames:    frames,
		floorDB:   spectralFloorDB(levels, peakDB),
		weights:   weights,
	}
}

// spectralFloorDB is the reference's noise estimate plus the margin, or the
// dynamic range under its loudest bin, whichever is higher, and never below
// the magnitude floor. The noise estimate is the median of every weighted
// bin of every frame: in a struck bar's spectrogram the partials occupy a
// few bins of a few frames, so the median is where an empty bin sits.
func spectralFloorDB(levels []float64, peakDB float64) float64 {
	floor := linToDB(0)

	if len(levels) == 0 {
		return floor
	}

	sort.Float64s(levels)

	noise := levels[len(levels)/2] + spectralNoiseMarginDB

	return math.Max(floor, math.Max(noise, peakDB-spectralDynamicRangeDB))
}

// errorDB scores a candidate against the reference frames after a dB gain.
//
// The candidate and the reference are already aligned: sample i of one is
// sample i of the other. reference is the aligned reference; it is the
// signal the frames were taken from unless the alignment dropped its head,
// in which case the frames are retaken from it here, which is the slow path
// a reference that starts at its strike never takes.
func (s *spectrogram) errorDB(candidate, reference []float32, retake bool, gainDB float64) float64 {
	if s == nil {
		return math.NaN()
	}

	sampleCount := minInt(len(candidate), len(reference))
	if sampleCount < s.frameSize {
		return math.NaN()
	}

	scratch, release := acquireSpectralScratch(s.frameSize)
	if scratch == nil {
		return math.NaN()
	}
	defer release()

	var (
		reference64 []float64
		refScratch  *spectralScratch
		refRelease  func()
	)

	if retake {
		refScratch, refRelease = acquireSpectralScratch(s.frameSize)
		if refScratch == nil {
			return math.NaN()
		}
		defer refRelease()
	}

	var weightedSum, weightTotal float64

	for index, start := 0, 0; start+s.frameSize <= sampleCount; index, start = index+1, start+s.hop {
		if retake {
			refScratch.transform(reference[start : start+s.frameSize])
			reference64 = refScratch.db
		} else {
			if index >= len(s.frames) {
				break
			}

			reference64 = s.frames[index]
		}

		scratch.transform(candidate[start : start+s.frameSize])

		for k := 1; k < s.frameSize/2; k++ {
			refDB := reference64[k]
			candDB := scratch.db[k] + gainDB

			if refDB < s.floorDB && candDB < s.floorDB {
				continue
			}

			delta := math.Max(candDB, s.floorDB) - math.Max(refDB, s.floorDB)
			weight := s.weights[k]
			weightedSum += weight * delta * delta
			weightTotal += weight
		}
	}

	if weightTotal == 0 {
		return 0
	}

	return math.Sqrt(weightedSum / weightTotal)
}

// spectralWeightKey identifies a weight table by sample rate and frame size.
type spectralWeightKey struct {
	sampleRate int
	frameSize  int
}

var spectralWeightCache sync.Map // map[spectralWeightKey][]float64

// spectralWeights returns the per-bin weights for a sample rate and frame
// size. The slice is built once and never mutated, so concurrent readers can
// share it.
func spectralWeights(sampleRate, frameSize int) []float64 {
	key := spectralWeightKey{sampleRate: sampleRate, frameSize: frameSize}

	if cached, ok := spectralWeightCache.Load(key); ok {
		return cached.([]float64)
	}

	binHz := float64(sampleRate) / float64(frameSize)
	weights := make([]float64, frameSize/2+1)

	for k := range weights {
		weights[k] = spectralBinWeight(float64(k) * binHz)
	}

	actual, _ := spectralWeightCache.LoadOrStore(key, weights)

	return actual.([]float64)
}

// spectralBinWeight returns the fitting weight for a bin at freqHz.
//
// The weighting is derived from what a glockenspiel actually radiates. Even the
// lowest bar of a common g5..c8 instrument has its fundamental near 800 Hz, and
// the perceptually decisive inharmonic partials sit at roughly 2.7x, 5.4x and
// 8.9x that, so the modal structure lives between ~1 kHz and ~10 kHz. Whatever
// energy a *recording* holds below the fundamental is room rumble, stand
// resonance and handling noise - none of it is anything the model can or should
// reproduce. The previous 3/2/1 step function weighted that band highest of
// all, which pointed the fit at the noise.
//
// The ramps are smooth in log frequency rather than stepped: a discontinuity in
// the weighting puts a kink in the cost landscape at the parameter values that
// move a partial across the step, which is exactly where a local optimizer
// should not be given a false edge to slide along.
func spectralBinWeight(freqHz float64) float64 {
	const (
		subFundamentalWeight = 0.15
		lowCornerHz          = 400.0
		bandStartHz          = 900.0
		bandEndHz            = 8000.0
		topCornerHz          = 16000.0
		topWeight            = 0.30
	)

	switch {
	case freqHz <= lowCornerHz:
		return subFundamentalWeight
	case freqHz < bandStartHz:
		return lerp(subFundamentalWeight, 1, logFraction(freqHz, lowCornerHz, bandStartHz))
	case freqHz <= bandEndHz:
		return 1
	case freqHz < topCornerHz:
		return lerp(1, topWeight, logFraction(freqHz, bandEndHz, topCornerHz))
	default:
		return topWeight
	}
}

func logFraction(value, low, high float64) float64 {
	return math.Log(value/low) / math.Log(high/low)
}

func lerp(from, to, fraction float64) float64 {
	return from + (to-from)*fraction
}

// ParseMetric converts a user-facing metric string into a Metric value.
func ParseMetric(value string) (Metric, error) {
	switch Metric(value) {
	case MetricRMS, MetricLog, MetricSpectral, MetricBalanced, MetricPlacement, MetricPolish:
		return Metric(value), nil
	default:
		return "", fmt.Errorf("unsupported metric %q", value)
	}
}

// MetricNames lists every metric ParseMetric accepts, composite profiles first.
func MetricNames() []string {
	return []string{
		string(MetricBalanced), string(MetricPlacement), string(MetricPolish),
		string(MetricRMS), string(MetricLog), string(MetricSpectral),
	}
}
