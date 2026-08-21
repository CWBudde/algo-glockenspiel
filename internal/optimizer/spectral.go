package optimizer

import (
	"fmt"
	"math"
	"math/cmplx"
	"sync"

	algofft "github.com/cwbudde/algo-fft"
)

const (
	// spectralFrameSize is a power of two on purpose: it keeps algo-fft on its
	// specialised radix-2 codelets. The previous implementation only forced an
	// even length, which could drop the transform onto a naive O(n^2) DFT.
	// 2048 samples is 46 ms at 44.1 kHz, enough resolution (~21.5 Hz) to
	// separate glockenspiel partials while still tracking the decay envelope.
	spectralFrameSize = 2048

	// 50 % overlap: the standard COLA hop for a Hann window.
	spectralHopSize = spectralFrameSize / 2

	// Magnitudes are normalised to amplitude units, so this floor is -100 dBFS
	// - roughly the PCM16 noise floor. Without it, a bin that is numerically
	// zero in one signal and merely quiet in the other would contribute a
	// meaningless several-hundred-dB error and dominate the whole metric.
	spectralMagnitudeFloor = 1e-5
)

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

	if _, err := newSpectralFFTPlan(); err != nil {
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
func spectralErrorWithGain(synth, ref []float32, sampleRate int, gainDB float64) float64 {
	sampleCount := minInt(len(synth), len(ref))
	if sampleRate <= 0 || sampleCount < spectralFrameSize {
		return math.Inf(1)
	}

	scratch, ok := spectralScratchPool.Get().(*spectralScratch)
	if !ok || scratch == nil {
		return math.Inf(1)
	}
	defer spectralScratchPool.Put(scratch)

	weights := spectralWeights(sampleRate)

	var weightedSum, weightTotal float64

	for start := 0; start+spectralFrameSize <= sampleCount; start += spectralHopSize {
		scratch.transform(synth[start:start+spectralFrameSize], ref[start:start+spectralFrameSize])

		for k := 1; k < spectralFrameSize/2; k++ {
			delta := scratch.magnitudeDB(scratch.specA, k) + gainDB - scratch.magnitudeDB(scratch.specB, k)
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

// spectralScratch owns one FFT plan and its buffers. algo-fft plans carry
// internal scratch state and are not safe for concurrent use, so each
// goroutine draws its own from the pool instead of serialising on a mutex.
type spectralScratch struct {
	plan     *algofft.FastPlanReal[float64, complex128]
	frameA   []float64
	frameB   []float64
	specA    []complex128
	specB    []complex128
	window   []float64
	magScale float64
}

var spectralScratchPool = sync.Pool{New: func() any {
	scratch, err := newSpectralScratch()
	if err != nil {
		return nil
	}

	return scratch
}}

func newSpectralFFTPlan() (*algofft.FastPlanReal[float64, complex128], error) {
	return algofft.NewFastPlanReal64(spectralFrameSize)
}

func newSpectralScratch() (*spectralScratch, error) {
	plan, err := newSpectralFFTPlan()
	if err != nil {
		return nil, err
	}

	window := make([]float64, spectralFrameSize)
	windowSum := 0.0

	for i := range window {
		window[i] = 0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/float64(spectralFrameSize))
		windowSum += window[i]
	}

	return &spectralScratch{
		plan:   plan,
		frameA: make([]float64, spectralFrameSize),
		frameB: make([]float64, spectralFrameSize),
		specA:  make([]complex128, spectralFrameSize/2+1),
		specB:  make([]complex128, spectralFrameSize/2+1),
		window: window,
		// Normalise to amplitude units so the dB floor has a physical meaning
		// independent of frame size.
		magScale: 2 / windowSum,
	}, nil
}

func (s *spectralScratch) transform(frameA, frameB []float32) {
	for i, coefficient := range s.window {
		s.frameA[i] = float64(frameA[i]) * coefficient
		s.frameB[i] = float64(frameB[i]) * coefficient
	}

	s.plan.Forward(s.specA, s.frameA)
	s.plan.Forward(s.specB, s.frameB)
}

func (s *spectralScratch) magnitudeDB(spectrum []complex128, bin int) float64 {
	return linToDB(cmplx.Abs(spectrum[bin]) * s.magScale)
}

func linToDB(x float64) float64 {
	if x < spectralMagnitudeFloor {
		x = spectralMagnitudeFloor
	}

	return 20 * math.Log10(x)
}

var spectralWeightCache sync.Map // map[int][]float64, keyed by sample rate

// spectralWeights returns the per-bin weights for a sample rate. The slice is
// built once and never mutated, so concurrent readers can share it.
func spectralWeights(sampleRate int) []float64 {
	if cached, ok := spectralWeightCache.Load(sampleRate); ok {
		return cached.([]float64)
	}

	binHz := float64(sampleRate) / float64(spectralFrameSize)
	weights := make([]float64, spectralFrameSize/2+1)

	for k := range weights {
		weights[k] = spectralBinWeight(float64(k) * binHz)
	}

	actual, _ := spectralWeightCache.LoadOrStore(sampleRate, weights)

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
	switch value {
	case string(MetricRMS):
		return MetricRMS, nil
	case string(MetricLog):
		return MetricLog, nil
	case string(MetricSpectral):
		return MetricSpectral, nil
	default:
		return "", fmt.Errorf("unsupported metric %q", value)
	}
}
