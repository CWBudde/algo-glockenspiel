package analysis

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/cmplx"
	"sort"

	algofft "github.com/cwbudde/algo-fft"
)

// Partial is one measured component of a reference: where it sits, how loud
// it is against the strongest one, and how fast it decays.
type Partial struct {
	// FrequencyHz is the peak position after quadratic interpolation.
	FrequencyHz float64 `json:"frequency_hz"`

	// LevelDB is the peak level relative to the strongest partial, so the
	// strongest reads zero and every other is negative.
	LevelDB float64 `json:"level_db"`

	// AmplitudeDB is the peak level in dB of amplitude against full scale of
	// the analysed signal, which for a normalised reference is dBFS.
	AmplitudeDB float64 `json:"amplitude_db"`

	// AttackDB is the level the fitted decay line extrapolates to at the
	// onset, in dB against full scale of the analysed signal. It is the
	// amplitude a model mode would need at the strike, which the averaged
	// level is not: a partial that dies in fifty milliseconds is loud at the
	// strike and faint on average.
	AttackDB float64 `json:"attack_db"`

	// HalfLifeMs is the time the partial's narrowband envelope takes to fall
	// by 6.02 dB, from a straight line fitted to that envelope in dB. NaN,
	// written as null, when the envelope does not fall.
	HalfLifeMs float64 `json:"half_life_ms"`
}

// partialJSON is the wire form of a Partial: the half-life is a pointer so
// that an unmeasurable one is null, which encoding/json refuses to write NaN
// as.
type partialJSON struct {
	FrequencyHz float64  `json:"frequency_hz"`
	LevelDB     float64  `json:"level_db"`
	AmplitudeDB float64  `json:"amplitude_db"`
	AttackDB    float64  `json:"attack_db"`
	HalfLifeMs  *float64 `json:"half_life_ms"`
}

// MarshalJSON writes an unmeasurable half-life as null.
func (p Partial) MarshalJSON() ([]byte, error) {
	wire := partialJSON{
		FrequencyHz: p.FrequencyHz,
		LevelDB:     p.LevelDB,
		AmplitudeDB: p.AmplitudeDB,
		AttackDB:    p.AttackDB,
	}

	if !math.IsNaN(p.HalfLifeMs) && !math.IsInf(p.HalfLifeMs, 0) {
		halfLife := p.HalfLifeMs
		wire.HalfLifeMs = &halfLife
	}

	return json.Marshal(wire)
}

// UnmarshalJSON reads the null back as NaN.
func (p *Partial) UnmarshalJSON(data []byte) error {
	var wire partialJSON
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}

	*p = Partial{
		FrequencyHz: wire.FrequencyHz,
		LevelDB:     wire.LevelDB,
		AmplitudeDB: wire.AmplitudeDB,
		AttackDB:    wire.AttackDB,
		HalfLifeMs:  math.NaN(),
	}

	if wire.HalfLifeMs != nil {
		p.HalfLifeMs = *wire.HalfLifeMs
	}

	return nil
}

// PartialOptions is what a caller decides about the partial measurement. The
// zero value is the default.
type PartialOptions struct {
	// FrameSize is the analysis window in samples; zero means
	// DefaultFrameSize.
	FrameSize int

	// MaxPartials caps how many are returned, strongest first; zero means
	// DefaultMaxPartials.
	MaxPartials int

	// MinLevelDB is how far below the strongest partial a peak may sit and
	// still count; zero means DefaultMinLevelDB.
	MinLevelDB float64

	// MinFrequencyHz excludes everything below it -- mains hum, handling
	// noise, room rumble; zero means DefaultMinFrequencyHz.
	MinFrequencyHz float64
}

const (
	// DefaultFrameSize is the analysis window. 16384 points at 44.1 kHz is a
	// 2.69 Hz bin, so quadratic interpolation places a peak to well under a
	// cent at 1 kHz, and a Hann main lobe of four bins still separates partials
	// eleven hertz apart.
	DefaultFrameSize = 16384

	// DefaultMaxPartials caps the list; the model's largest preset holds
	// twelve modes and a struck bar rarely has more measurable partials.
	DefaultMaxPartials = 16

	// DefaultMinLevelDB is the deepest a partial may sit below the strongest.
	// The C5 recording's sixth partial is 39 dB down; below 40 dB what it
	// shows is the broadband residue of the attack, dozens of components a
	// few hertz apart that all die within sixty milliseconds and are not
	// modes of the bar.
	DefaultMinLevelDB = -40.0

	// DefaultMinFrequencyHz excludes mains hum and its first harmonics. The
	// lowest bar the model plays is written well above it.
	DefaultMinFrequencyHz = 200.0

	// peakProminenceDB is how far a peak has to rise above the median of the
	// spectrum around it to be a partial rather than a ripple of the floor.
	peakProminenceDB = 12.0

	// peakNeighbourhoodBins is the half-width, in bins, over which that median
	// is taken: about 160 Hz at the default frame size, wide enough to hold a
	// few hundred floor bins and narrow enough to follow a sloping floor.
	peakNeighbourhoodBins = 60

	// peakSeparationBins is the closest two partials may be. Four bins is the
	// Hann main lobe, so anything closer is leakage of the stronger one.
	peakSeparationBins = 4

	// halfLifeWindow is the Hann window the narrowband envelope is measured
	// with: 2048 samples is 46 ms at 44.1 kHz, a first null 43 Hz away, and
	// a time resolution that still resolves a 50 ms half-life.
	halfLifeWindow = 2048

	// halfLifeHop is the spacing of the narrowband envelope.
	halfLifeHop = 64

	// halfLifeFitRangeDB is how far below its own peak the narrowband envelope
	// is followed when the decay is fitted. Deeper than this, room tail and
	// floor bend the line.
	halfLifeFitRangeDB = 30.0

	// halfLifeFitSeconds is the longest stretch of envelope the line is
	// fitted over. A struck bar's first second is what the ear judges; past
	// it the C5 recording's fundamental steepens from 8.9 to 17 dB/s as the
	// room takes over from the bar, and a line through both is a line through
	// neither. It is also the range testdata/reference/README.md measured
	// over, so the two agree to the millisecond.
	halfLifeFitSeconds = 1.0

	// halfLifeMinDropDB is the least the envelope has to fall for a slope to be
	// worth fitting at all.
	halfLifeMinDropDB = 3.0

	// skirtMarginDB is how far a candidate has to stand above the line shape
	// of a stronger partial to be a partial of its own rather than that
	// partial's skirt. A partial that decays in fifty milliseconds has a
	// Lorentzian line tens of hertz wide, and the Hann window's sidelobes
	// ripple along it into local maxima that are not modes of anything.
	skirtMarginDB = 6.0

	// hannFirstSidelobeDB and hannSidelobeSlopeDB describe the Hann window's
	// sidelobe envelope: -31 dB at two and a half bins, falling 18 dB per
	// octave beyond.
	hannFirstSidelobeDB   = -31.0
	hannFirstSidelobeBins = 2.5
	hannSidelobeSlopeDB   = 18.0

	// amplitudeFloor pins the log of an empty bin at -160 dB of amplitude,
	// far below any recording's floor: unlike the spectral metric's -100 dB
	// pin, the analysis has to report where the floor is, not hide it.
	amplitudeFloor = 1e-8
)

// ErrTooShort reports a signal shorter than the analysis window.
var ErrTooShort = errors.New("reference is shorter than the analysis window")

// Measurement is what the partial analysis found, with what it was told.
type Measurement struct {
	// Partials is the list, strongest first.
	Partials []Partial `json:"partials"`

	// FundamentalHz is the lowest partial within fundamentalWindowDB of the
	// strongest, which is where a struck bar's first mode sits. Zero when
	// nothing was found.
	FundamentalHz float64 `json:"fundamental_hz"`

	// NoiseFloorDB is the median of the averaged spectrum above
	// MinFrequencyHz, in dB of amplitude against full scale: where an empty
	// bin sits in this signal.
	NoiseFloorDB float64 `json:"noise_floor_db"`

	// Options is what the measurement ran with, defaults filled in.
	Options PartialOptions `json:"options"`
}

// fundamentalWindowDB is how far below the strongest partial the fundamental
// may sit. A bar's first mode is usually the strongest; when a higher one
// wins, the first is never far behind.
const fundamentalWindowDB = 30.0

// Partials measures the partials of a signal, strongest first. It is Measure
// without the rest of the report.
func Partials(samples []float32, sampleRate int, options PartialOptions) ([]Partial, error) {
	measurement, err := Measure(samples, sampleRate, options)
	if err != nil {
		return nil, err
	}

	return measurement.Partials, nil
}

// Measure measures the partials of a signal, strongest first, and the floor
// they stand on.
//
// The spectrum is the average power over Hann frames of FrameSize at a
// quarter-frame hop, in amplitude units. A peak is a local maximum that stands
// peakProminenceDB above the median of its neighbourhood, within MinLevelDB of
// the strongest peak, and not within peakSeparationBins of a stronger one. Each
// peak is refined by fitting a parabola through the dB values of its three
// bins. The half-life comes from a straight line through the dB envelope of
// the signal heterodyned to the refined frequency.
func Measure(samples []float32, sampleRate int, options PartialOptions) (*Measurement, error) {
	options = options.withDefaults()

	if sampleRate <= 0 {
		return nil, fmt.Errorf("sample rate must be positive, got %d", sampleRate)
	}

	if len(samples) < options.FrameSize {
		return nil, fmt.Errorf("%w: %d samples, window %d", ErrTooShort, len(samples), options.FrameSize)
	}

	spectrum, err := averagedSpectrumDB(samples, options.FrameSize)
	if err != nil {
		return nil, err
	}

	binHz := float64(sampleRate) / float64(options.FrameSize)
	candidates := pickPeaks(spectrum, options, binHz)

	var partials []Partial

	for _, candidate := range candidates {
		if len(partials) >= options.MaxPartials {
			break
		}

		if len(partials) > 0 && candidate.levelDB < partials[0].AmplitudeDB+options.MinLevelDB {
			break
		}

		frequency := candidate.frequencyBins * binHz
		if explainedByASkirt(frequency, candidate.levelDB, partials, binHz) {
			continue
		}

		decay := fitDecay(samples, sampleRate, frequency)
		partials = append(partials, Partial{
			FrequencyHz: frequency,
			AmplitudeDB: candidate.levelDB,
			AttackDB:    decay.attackDB,
			HalfLifeMs:  decay.halfLifeMs,
		})
	}

	for i := range partials {
		partials[i].LevelDB = partials[i].AmplitudeDB - partials[0].AmplitudeDB
	}

	return &Measurement{
		Partials:      partials,
		FundamentalHz: fundamentalOf(partials),
		NoiseFloorDB:  medianAbove(spectrum, max(1, int(math.Ceil(options.MinFrequencyHz/binHz)))),
		Options:       options,
	}, nil
}

// fundamentalOf is the lowest partial within fundamentalWindowDB of the
// strongest, or zero.
func fundamentalOf(partials []Partial) float64 {
	fundamental := 0.0

	for _, partial := range partials {
		if partial.LevelDB < -fundamentalWindowDB {
			continue
		}

		if fundamental == 0 || partial.FrequencyHz < fundamental {
			fundamental = partial.FrequencyHz
		}
	}

	return fundamental
}

// medianAbove is the median of the spectrum from bin lowest up.
func medianAbove(spectrum []float64, lowest int) float64 {
	if lowest >= len(spectrum) {
		return silenceDB
	}

	values := append([]float64(nil), spectrum[lowest:]...)
	sort.Float64s(values)

	return values[len(values)/2]
}

// explainedByASkirt reports whether a candidate at frequencyHz and levelDB
// sits within skirtMarginDB of the line shape of a partial already accepted.
// Accepted partials are always stronger, since candidates arrive in level
// order.
func explainedByASkirt(frequencyHz, levelDB float64, accepted []Partial, binHz float64) bool {
	for _, partial := range accepted {
		deltaHz := math.Abs(frequencyHz - partial.FrequencyHz)
		if deltaHz < peakSeparationBins*binHz {
			return true
		}

		if levelDB < partial.AmplitudeDB+skirtDB(deltaHz, deltaHz/binHz, partial.HalfLifeMs)+skirtMarginDB {
			return true
		}
	}

	return false
}

// skirtDB is the attenuation, at deltaHz from a partial with the given
// half-life, of that partial's own line shape: the Lorentzian of its
// exponential decay or the Hann window's sidelobe envelope, whichever is
// higher. A partial that does not decay has no Lorentzian skirt.
func skirtDB(deltaHz, deltaBins, halfLifeMs float64) float64 {
	hann := hannFirstSidelobeDB
	if deltaBins > hannFirstSidelobeBins {
		hann -= hannSidelobeSlopeDB * math.Log2(deltaBins/hannFirstSidelobeBins)
	}

	if math.IsNaN(halfLifeMs) || math.IsInf(halfLifeMs, 0) {
		return hann
	}

	timeConstant := halfLifeMs / 1000 / math.Ln2
	detuning := 2 * math.Pi * deltaHz * timeConstant
	lorentzian := -10 * math.Log10(1+detuning*detuning)

	return math.Max(hann, lorentzian)
}

func (o PartialOptions) withDefaults() PartialOptions {
	if o.FrameSize <= 0 {
		o.FrameSize = DefaultFrameSize
	}

	if o.MaxPartials <= 0 {
		o.MaxPartials = DefaultMaxPartials
	}

	if o.MinLevelDB == 0 {
		o.MinLevelDB = DefaultMinLevelDB
	}

	if o.MinFrequencyHz <= 0 {
		o.MinFrequencyHz = DefaultMinFrequencyHz
	}

	return o
}

// averagedSpectrumDB is the mean power spectrum over Hann frames at a quarter
// frame hop, in dB of amplitude with the window's gain divided out, so a
// full-scale sine reads about 0 dB.
func averagedSpectrumDB(samples []float32, frameSize int) ([]float64, error) {
	plan, err := algofft.NewFastPlanReal64(frameSize)
	if err != nil {
		return nil, fmt.Errorf("fft plan for %d points: %w", frameSize, err)
	}

	window := make([]float64, frameSize)
	windowSum := 0.0

	for i := range window {
		window[i] = 0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/float64(frameSize))
		windowSum += window[i]
	}

	scale := 2 / windowSum
	hop := frameSize / 4
	frame := make([]float64, frameSize)
	spectrum := make([]complex128, frameSize/2+1)
	power := make([]float64, frameSize/2+1)
	frames := 0

	for start := 0; start+frameSize <= len(samples); start += hop {
		for i, coefficient := range window {
			frame[i] = float64(samples[start+i]) * coefficient
		}

		plan.Forward(spectrum, frame)

		for bin, value := range spectrum {
			magnitude := cmplx.Abs(value) * scale
			power[bin] += magnitude * magnitude
		}

		frames++
	}

	for bin := range power {
		power[bin] = 20 * math.Log10(math.Max(amplitudeFloor, math.Sqrt(power[bin]/float64(frames))))
	}

	return power, nil
}

// spectralPeak is a refined local maximum of the averaged spectrum.
type spectralPeak struct {
	bin           int
	frequencyBins float64
	levelDB       float64
}

// pickPeaks finds every local maximum of a dB spectrum that stands out from
// its neighbourhood, strongest first, each refined to a fraction of a bin.
func pickPeaks(spectrum []float64, options PartialOptions, binHz float64) []spectralPeak {
	lowest := max(1, int(math.Ceil(options.MinFrequencyHz/binHz)))

	var candidates []spectralPeak

	for bin := lowest; bin < len(spectrum)-1; bin++ {
		if spectrum[bin] <= spectrum[bin-1] || spectrum[bin] < spectrum[bin+1] {
			continue
		}

		if spectrum[bin]-neighbourhoodMedian(spectrum, bin) < peakProminenceDB {
			continue
		}

		offset, level := refinePeak(spectrum[bin-1], spectrum[bin], spectrum[bin+1])
		candidates = append(candidates, spectralPeak{
			bin:           bin,
			frequencyBins: float64(bin) + offset,
			levelDB:       level,
		})
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		return candidates[i].levelDB > candidates[j].levelDB
	})

	return candidates
}

// neighbourhoodMedian is the median dB level of the bins around bin, the
// centre excluded, which is where the floor sits when the bin is a partial.
func neighbourhoodMedian(spectrum []float64, bin int) float64 {
	from := max(1, bin-peakNeighbourhoodBins)
	to := min(len(spectrum), bin+peakNeighbourhoodBins+1)
	values := make([]float64, 0, to-from)

	for i := from; i < to; i++ {
		if i != bin {
			values = append(values, spectrum[i])
		}
	}

	sort.Float64s(values)

	return values[len(values)/2]
}

// refinePeak fits a parabola through three dB values and returns the offset
// of its vertex from the centre bin, in bins, and the level there.
func refinePeak(left, centre, right float64) (float64, float64) {
	denominator := left - 2*centre + right
	if denominator >= 0 {
		return 0, centre
	}

	offset := 0.5 * (left - right) / denominator

	return offset, centre - 0.25*(left-right)*offset
}

// decayFit is what a straight line through a partial's dB envelope says.
type decayFit struct {
	// halfLifeMs is the time to fall 6.02 dB; NaN when the envelope does not
	// fall.
	halfLifeMs float64
	// attackDB is the line's value at the onset, or the envelope's peak when
	// there is no line.
	attackDB float64
}

// fitDecay measures how long the component at frequencyHz takes to fall by
// 6.02 dB, from a least-squares line through its narrowband envelope in dB,
// and where that line starts.
//
// The envelope is the magnitude of the signal heterodyned to the frequency and
// smoothed by a Hann window, sampled every halfLifeHop. The line is fitted from
// the envelope's peak down to where it has fallen halfLifeFitRangeDB, or over
// halfLifeFitSeconds, whichever comes first. A tail that is not exponential --
// a room, a beating pair -- gets the least-squares slope of that range. NaN
// when the envelope never falls halfLifeMinDropDB.
func fitDecay(samples []float32, sampleRate int, frequencyHz float64) decayFit {
	envelope := narrowbandEnvelopeDB(samples, sampleRate, frequencyHz)
	if len(envelope) == 0 {
		return decayFit{halfLifeMs: math.NaN(), attackDB: math.NaN()}
	}

	peak := 0
	for i, level := range envelope {
		if level > envelope[peak] {
			peak = i
		}
	}

	unmeasured := decayFit{halfLifeMs: math.NaN(), attackDB: envelope[peak]}
	stop := envelope[peak] - halfLifeFitRangeDB
	limit := min(len(envelope), peak+1+int(halfLifeFitSeconds*float64(sampleRate)/halfLifeHop))
	end := peak + 1

	for end < limit && envelope[end] > stop {
		end++
	}

	if end-peak < 2 || envelope[peak]-envelope[end-1] < halfLifeMinDropDB {
		return unmeasured
	}

	seconds := float64(halfLifeHop) / float64(sampleRate)
	slope, intercept := fitLine(envelope[peak:end], seconds)

	if slope >= 0 {
		return unmeasured
	}

	// Each envelope point describes the centre of its window, so the line's
	// value at the onset is half a window before the first point.
	window := min(halfLifeWindow, len(samples))
	firstCentre := (float64(peak*halfLifeHop) + float64(window)/2) / float64(sampleRate)

	return decayFit{
		halfLifeMs: -20 * math.Log10(2) / slope * 1000,
		attackDB:   intercept - slope*firstCentre,
	}
}

// narrowbandEnvelopeDB is the dB envelope of the component at frequencyHz.
func narrowbandEnvelopeDB(samples []float32, sampleRate int, frequencyHz float64) []float64 {
	window := min(halfLifeWindow, len(samples))
	if window < 2 {
		return nil
	}

	coefficients := make([]float64, window)
	windowSum := 0.0

	for i := range coefficients {
		coefficients[i] = 0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/float64(window))
		windowSum += coefficients[i]
	}

	scale := 2 / windowSum
	radians := -2 * math.Pi * frequencyHz / float64(sampleRate)
	envelope := make([]float64, 0, len(samples)/halfLifeHop+1)

	for start := 0; start+window <= len(samples); start += halfLifeHop {
		sum := complex(0, 0)

		for i, coefficient := range coefficients {
			phase := radians * float64(start+i)
			sum += complex(float64(samples[start+i])*coefficient, 0) * cmplx.Rect(1, phase)
		}

		envelope = append(envelope, 20*math.Log10(math.Max(amplitudeFloor, cmplx.Abs(sum)*scale)))
	}

	return envelope
}

// fitLine is the least-squares line through equally spaced values against
// time: its slope per second and its value at the first sample.
func fitLine(values []float64, spacing float64) (float64, float64) {
	count := float64(len(values))
	meanX := (count - 1) / 2
	meanY := 0.0

	for _, value := range values {
		meanY += value
	}

	meanY /= count

	covariance, variance := 0.0, 0.0

	for i, value := range values {
		dx := float64(i) - meanX
		covariance += dx * (value - meanY)
		variance += dx * dx
	}

	if variance == 0 {
		return 0, meanY
	}

	slopePerStep := covariance / variance

	return slopePerStep / spacing, meanY - slopePerStep*meanX
}
