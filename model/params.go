package model

import (
	"errors"
	"fmt"
	"math"
)

const (
	// MaxModes bounds a preset's mode count so a malformed file cannot ask for
	// an unbounded allocation.
	MaxModes = 512

	// MaxHarmonics bounds the partials one mode may carry.
	MaxHarmonics = 64

	// InputMixMin and InputMixMax bound BarParams.InputMix, the amount of dry
	// filtered excitation mixed in alongside the oscillator bank's output.
	// Enforced by ValidateBarParams.
	InputMixMin = 0.0
	InputMixMax = 2.0

	// FilterFrequencyMinHz and FilterFrequencyMaxHz bound
	// BarParams.FilterFrequency, the cutoff of the lowpass the excitation
	// passes through before it reaches the bank. Enforced by ValidateBarParams.
	FilterFrequencyMinHz = 20.0
	FilterFrequencyMaxHz = 20000.0

	// AmplitudeMin and AmplitudeMax bound ModeParams.Amplitude. The range is
	// signed because a mode may enter in antiphase with its neighbours, which
	// is part of how a fit shapes the attack. Enforced by ValidateBarParams.
	AmplitudeMin = -2.0
	AmplitudeMax = 2.0

	// FrequencyMinHz and FrequencyMaxHz bound ModeParams.Frequency and
	// BarParams.BaseFrequency. The ceiling sits above any audible rate on
	// purpose: refusing a mode above Nyquist here would make a preset's
	// validity depend on the sample rate it happens to be rendered at.
	// Enforced by ValidateBarParams.
	//
	// That is a statement about validity and not about sound. A mode above
	// Nyquist is *not* a wasted oscillator -- a resonator handed one produces
	// the alias at full amplitude -- so the renderer culls it instead, at the
	// rate it is actually rendering at. See rotorCoefficients in
	// internal/oscbank. Validity is rate-independent, audibility is not, and
	// keeping the two apart is what lets this constant be a guard against
	// nonsense rather than a band limit.
	//
	// The ceiling is 200 kHz rather than 50 kHz because the keyboard's top key
	// moved to MIDI 108. Transposition multiplies every mode frequency by the
	// ratio, so this constant divided by the ratio from a preset's own note to
	// the top key is the real limit on what that preset may be authored with --
	// and a preset authored at note 69 is now stretched 39 semitones, a factor
	// of 9.51, which took the authored ceiling from 10.5 kHz to 5.3 kHz and put
	// the shipped recorded-bar.json (a 9792 Hz mode, 93 kHz at the top key)
	// outside it. The alternative was deleting two of that preset's modes for
	// the second time in its history. Following this constant's own reasoning
	// instead: a 93 kHz mode is not something to refuse at authoring time. It
	// is something the renderer must not sound, which is where it is handled.
	FrequencyMinHz = 0.01
	FrequencyMaxHz = 200000.0

	// DecayMsMin is the shortest decay a mode may carry. See
	// DecayMsValidationMax and DecayMsSearchMax for the two ceilings, and the
	// package overview for why there are two.
	//
	// It is the exact mirror of DecayMsValidationMax and moves for the mirror
	// reason. That ceiling is enforced after transposing a preset *down* to the
	// bottom key, where decays inflate; this floor is enforced after transposing
	// it *up* to the top key, where they shrink. Moving the top key to MIDI 108
	// therefore tightened this floor by the same factor it loosened the ceiling:
	// default.json's shortest mode is 0.5605 ms at note 69 and 0.0589 ms at note
	// 108, which the old 0.1 ms floor refused -- so NewBar failed and the engine
	// dropped every note-on above 98 without a sound.
	//
	// No amount of re-authoring fixes that, because decay_min / 2^((top-note)/12)
	// is invariant under TransposeToNote, exactly as the frequency ceiling is.
	// Lowering the floor is the only lever. 0.01 ms is 0.44 samples at 44.1 kHz,
	// so it sits below the "dies within a sample" line the *search* floor
	// (DecayMsSearchMin, 0.5 ms) is drawn at -- which is right, because these
	// two floors answer different questions. The search has no business spending
	// steps on a mode that is a click; validation has no business refusing to
	// build a bar whose top octave contains one.
	DecayMsMin = 0.01

	// DecayMsValidationMax is the hard ceiling ValidateBarParams enforces: the
	// widest decay a BarParams may carry at the moment it is handed to NewBar.
	//
	// It is deliberately far above the range a preset is *authored* in, because
	// the params NewBar sees are not the params the preset file holds. Playing
	// a preset at a note other than the one it was fitted at transposes it, and
	// transposition divides every decay by the frequency ratio -- correctly so,
	// since a bar an octave lower rings roughly twice as long. Transposing down
	// therefore inflates the decays that reach validation.
	//
	// The number is a policy, not a derivation: five seconds is the longest a
	// struck bar may ring on this instrument, measured where the ringing actually
	// happens, which is after transposition. Everything else follows from it.
	// What a preset file may be *written* with follows from it too, and is
	// therefore not a constant -- see [AuthoredDecayMsMax] and
	// [ValidateAuthoredBarParams]. A preset at or below the bottom key may carry
	// the full 5000 ms, one at note 100 only 1487 ms, one at note 108 only 936 ms;
	// all three ring for the same five seconds at the bottom key.
	//
	// The number did not have to move when the keyboard became a glockenspiel,
	// and that is worth recording rather than leaving to look like luck. It is
	// enforced after transposition, so raising the bottom key from MIDI 36 to 79
	// shortened every transposed decay by a factor of 12 and left five seconds
	// with room to spare -- the hollandm pack's longest bar, 808 ms at MIDI 85,
	// needs 13.7 s at note 36 and 1.1 s at note 79. The floor is the constant that
	// had to move instead; see [DecayMsMin].
	//
	// An earlier revision of this constant derived 5000 from base note 69 alone
	// -- 500 ms at the top of the optimizer's search box, transposed from note 69
	// to note 36, is 3364 ms, rounded up for headroom. That derivation quietly
	// assumed the only base note that exists is 69. Preset.Note is authorable
	// across the whole MIDI range, so it bought nothing: a preset authored at
	// note 100 with a 500 ms decay needs 20159 ms at note 36 and its low register
	// went dead exactly as before. The ceiling cannot guarantee playability on
	// its own, whatever value it takes, because it does not know the base note.
	// The base-note-aware check is what guarantees it.
	//
	// This constant used to be a single DecayMsMax doing both jobs at 500 ms,
	// and the consequence was that MIDI notes 36..52 -- the bottom 17 of the 61
	// playable keys -- were silent: the shipped preset's 188.2 ms first mode
	// becomes 1266 ms at note 36, ValidateBarParams refused it, NewBar failed,
	// and the note-on was discarded without a sound or a diagnostic.
	DecayMsValidationMax = 5000.0

	// DecayMsSearchMin and DecayMsSearchMax are the optimizer's decay search
	// box. That is all they are.
	//
	// The ceiling is emphatically not the one a preset is authored under, which
	// is [AuthoredDecayMsMax] and depends on the base note -- 1487 ms at note 100,
	// 936 ms at note 108 -- so the optimizer narrows its box to that ceiling
	// for the note it fits at, and nothing validates against these values.
	//
	// The ceiling was 500 ms until Phase 8.3, kept there for a step-size
	// argument: decay is log-encoded into the unit cube, so widening the box
	// stretches every step through that dimension. The argument lost to a
	// measurement. A decay here is a half-life, and the only real recording in
	// the repository has a fundamental whose half-life is 677 ms, so a 500 ms
	// box could not contain the one bar the model is meant to reproduce. Two
	// seconds covers a long-ringing bar's fundamental with room to spare; the
	// floor rises from the 0.1 ms validation minimum to half a millisecond
	// because a mode that dies within a sample is a click, not a partial, and
	// the search has no business spending steps there.
	DecayMsSearchMin = 0.5
	DecayMsSearchMax = 2000.0

	// DecayKeytrackDefault is the exponent transposition raises the frequency
	// ratio to before dividing a decay by it. One is the law every preset
	// written before this field existed was authored under -- a bar an octave
	// down rings exactly twice as long -- and a nil BarParams.DecayKeytrack
	// means it, which is what makes the field free to add.
	//
	// It is a pointer rather than a plain float64 for one reason, and it is not
	// a style choice: the neutral value is 1, so Go's zero value would mean
	// beta = 0, a legal and physically meaningful exponent (no key tracking at
	// all, which is roughly what a metallophone measures). Every BarParams
	// literal in this repository and in the module that builds against it would
	// have switched laws with no compile error. The ResolvedStage trick cannot
	// rescue it either, because unlike an empty ChebyshevStage, 0.0 is a value
	// an author will legitimately write.
	DecayKeytrackDefault = 1.0

	// DecayKeytrackMin and DecayKeytrackMax bound BarParams.DecayKeytrack.
	//
	// The range is derived rather than chosen. The authoring ceiling scales as
	// DecayMsValidationMax * 2^(beta*(worst-base)/12), and once it falls under
	// DecayMsMin no preset at that base note can validate at all -- every fit
	// there would fail with an empty search box. Requiring the ceiling to stay
	// above the floor for every authorable note gives beta <= 2.06 going up and
	// |beta| <= 1.95 going down; this range sits inside both, with the measured
	// exponents (-0.24 for a metallophone to +1.22 for a toy glockenspiel)
	// comfortably interior. TestAuthoredCeilingStaysAboveTheDecayFloor is what
	// keeps that a decision rather than an accident.
	DecayKeytrackMin = -1.0
	DecayKeytrackMax = 1.75

	// OutputGainDBMin and OutputGainDBMax bound BarParams.OutputGainDB, the
	// level the finished bar is rendered at. Enforced by ValidateBarParams.
	//
	// The range is wide because it is the one parameter that has to absorb an
	// arbitrary mismatch rather than describe a physical property. A fit scores
	// its candidates with the level solved in closed form and divided out, so
	// the search is free to drift in level and does: the best fit of the
	// Morphagene c6 recording landed 37.11 dB below its reference. Sixty dB
	// each way covers that with room to spare while still being a bound, so a
	// fit that has gone somewhere strange reports the clamp instead of
	// silently asking for a factor of a million.
	OutputGainDBMin = -60.0
	OutputGainDBMax = 60.0

	// HarmonicGainMin and HarmonicGainMax bound both sets of harmonic gains --
	// ModeParams.Harmonics, the partials riding on one mode, and
	// ChebyshevParams.HarmonicGains, the waveshaper's terms. Enforced by
	// ValidateBarParams.
	HarmonicGainMin = 0.0
	HarmonicGainMax = 2.0
)

// ModeParams describes one resonant mode.
//
// Harmonics are optional integer-multiple partials computed on top of this
// mode's oscillator: entry k adds a rotor at (k+1) * Frequency sharing DecayMs,
// with its gain applied on top of Amplitude. An empty slice means the mode is a
// single oscillator at its fundamental, which is what every v1 preset describes.
type ModeParams struct {
	Amplitude float64   `json:"amplitude"`
	Frequency float64   `json:"frequency"`
	DecayMs   float64   `json:"decay_ms"`
	Harmonics []float64 `json:"harmonics,omitempty"`
}

// Clone returns a deep copy of the mode.
func (m ModeParams) Clone() ModeParams {
	if len(m.Harmonics) > 0 {
		m.Harmonics = append([]float64(nil), m.Harmonics...)
	}

	return m
}

// copyInto deep-copies the mode into dst, reusing dst's harmonics slice when
// its capacity allows. See [BarParams.CopyInto] for why this exists.
func (m *ModeParams) copyInto(dst *ModeParams) {
	if m == dst {
		return
	}

	dst.Amplitude = m.Amplitude
	dst.Frequency = m.Frequency
	dst.DecayMs = m.DecayMs
	dst.Harmonics = copyFloat64s(dst.Harmonics, m.Harmonics)
}

// ChebyshevStage selects where the Chebyshev waveshaper sits in the chain.
type ChebyshevStage string

const (
	// ChebyshevStageExcitation shapes the filtered excitation before it reaches
	// the oscillators. This is what v1 presets describe and stays the default,
	// so their rendering is unchanged.
	ChebyshevStageExcitation ChebyshevStage = "excitation"

	// ChebyshevStageOutput shapes the oscillator bank's output instead, which
	// is the post-oscillator placement the shaper was always meant to have.
	ChebyshevStageOutput ChebyshevStage = "output"
)

// ChebyshevParams controls harmonic excitation.
type ChebyshevParams struct {
	Enabled       bool           `json:"enabled"`
	Stage         ChebyshevStage `json:"stage,omitempty"`
	HarmonicGains []float64      `json:"harmonic_gains"`
}

// ResolvedStage returns the shaper stage, defaulting to the v1 placement.
func (c ChebyshevParams) ResolvedStage() ChebyshevStage {
	if c.Stage == ChebyshevStageOutput {
		return ChebyshevStageOutput
	}

	return ChebyshevStageExcitation
}

// Clone returns a deep copy of the Chebyshev parameters.
func (c ChebyshevParams) Clone() ChebyshevParams {
	if len(c.HarmonicGains) > 0 {
		c.HarmonicGains = append([]float64(nil), c.HarmonicGains...)
	}

	return c
}

// copyInto deep-copies the Chebyshev parameters into dst, reusing dst's gain
// slice when its capacity allows.
func (c *ChebyshevParams) copyInto(dst *ChebyshevParams) {
	if c == dst {
		return
	}

	dst.Enabled = c.Enabled
	dst.Stage = c.Stage
	dst.HarmonicGains = copyFloat64s(dst.HarmonicGains, c.HarmonicGains)
}

// BarParams are the top-level model parameters for one bar.
//
// Modes is a slice: the mode count is runtime configuration. Copy BarParams
// with Clone rather than assignment, or the copy shares this slice.
type BarParams struct {
	InputMix        float64         `json:"input_mix"`
	FilterFrequency float64         `json:"filter_frequency"`
	BaseFrequency   float64         `json:"base_frequency"`
	Modes           []ModeParams    `json:"modes"`
	Chebyshev       ChebyshevParams `json:"chebyshev"`

	// OutputGainDB is the level the finished bar renders at, in dB. Zero is
	// unity, which is why it is omitempty: every preset written before this
	// field existed renders exactly as it did.
	//
	// It is the one parameter a fit does not search. The objective solves the
	// level in closed form and subtracts it from every spectral, envelope and
	// onset term, so level is a flat ridge in the search -- the same shape as
	// BaseFrequency, and excluded for the same reason. Searching it would buy
	// a dimension with no gradient. The fit measures it instead, once, from
	// the render it already performs.
	//
	// Transposition leaves it alone. A bar an octave down rings longer and
	// lower, which is why DecayMs and Frequency scale, but it does not get
	// louder for being transposed.
	OutputGainDB float64 `json:"output_gain_db,omitempty"`

	// DecayKeytrack is the exponent transposition raises the frequency ratio to
	// before dividing a decay by it: DecayMs /= ratio^DecayKeytrack.
	//
	// Nil means [DecayKeytrackDefault], 1, which is what every preset written
	// before this field existed was authored under and is why nil rather than
	// zero is the neutral value -- see the constant for why the field is a
	// pointer at all.
	//
	// It exists because 1 is not what struck bars measure. Across the four
	// reference packs the exponent runs from -0.24 for a metallophone, whose
	// ring barely tracks pitch at all, to +1.22 for a toy glockenspiel, whose
	// top bars die faster than transposition predicts. One preset per
	// instrument cannot be right for all of them with the exponent nailed down.
	//
	// It is not a search dimension of a single-note fit, and must not become
	// one: at one note it trades off exactly against DecayMs, which is the same
	// gauge freedom BaseFrequency is excluded for. Only an objective spanning
	// several notes can see it.
	DecayKeytrack *float64 `json:"decay_keytrack,omitempty"`
}

// ResolvedDecayKeytrack returns the key-tracking exponent, which is
// [DecayKeytrackDefault] when the field is absent.
func (p *BarParams) ResolvedDecayKeytrack() float64 {
	if p == nil || p.DecayKeytrack == nil {
		return DecayKeytrackDefault
	}

	return *p.DecayKeytrack
}

// Clone returns a deep copy, safe to mutate independently of the original.
//
// Clone is the convenient form: it starts from an empty destination, so every
// non-empty slice it copies is freshly allocated. Code on the audio path that
// already owns a destination should use [BarParams.CopyInto] instead, which
// reuses the buffers that destination already holds.
func (p BarParams) Clone() BarParams {
	var dst BarParams
	p.CopyInto(&dst)

	return dst
}

// CopyInto deep-copies p into dst, reusing dst's slices wherever their capacity
// already suffices and only allocating when it does not.
//
// This exists so that a Bar which is retuned rather than rebuilt — a voice
// taken from a pool, where a note-on must not allocate — can absorb a new
// parameter set without touching the allocator. A plain Clone cannot serve that role: it
// allocates the Modes slice, every non-empty Harmonics slice and the Chebyshev
// gains on every single call, however little the shape actually changed.
//
// The copy is deep, so dst never aliases p's backing arrays and the two can be
// mutated independently, exactly as with Clone. That holds even when dst starts
// out already sharing an array with p, as a shallow struct copy such as
// dst := *p leaves it: reusing that array would turn the copy into a no-op and
// quietly keep the two views aliased, so a shared array is replaced rather than
// written into. See [sharesBacking] for what that check does and does not see.
// Copying a value into itself is a no-op.
//
// Nil-ness is preserved rather than normalized to an empty slice, because
// BarParams round-trips through JSON and a nil slice and an empty one do not
// encode alike. That costs nothing: a nil source needs no buffer to copy into
// either.
func (p *BarParams) CopyInto(dst *BarParams) {
	// Copying a value into itself is a no-op, and it has to be spelled out
	// rather than left to fall through: the overlap handling below would see
	// dst's arrays aliasing p's, replace them with fresh ones, and — dst being
	// p — leave p pointing at those empty arrays before anything was read out
	// of the originals.
	if p == dst {
		return
	}

	dst.InputMix = p.InputMix
	dst.FilterFrequency = p.FilterFrequency
	dst.BaseFrequency = p.BaseFrequency
	dst.OutputGainDB = p.OutputGainDB

	// The pointee is copied rather than the pointer shared: CopyInto is the
	// allocation-free audio path and its whole contract is that dst is a deep
	// copy. The aliasing arm matters for the same reason sharesBacking does
	// below -- a shallow dst := *p leaves the two pointers equal, and writing
	// through one would be a no-op that left them aliased.
	if p.DecayKeytrack == nil {
		dst.DecayKeytrack = nil
	} else {
		if dst.DecayKeytrack == nil || dst.DecayKeytrack == p.DecayKeytrack {
			dst.DecayKeytrack = new(float64)
		}

		*dst.DecayKeytrack = *p.DecayKeytrack
	}

	if p.Modes == nil {
		dst.Modes = nil
	} else {
		if dst.Modes != nil && cap(dst.Modes) >= len(p.Modes) && !sharesBacking(dst.Modes, p.Modes) {
			dst.Modes = dst.Modes[:len(p.Modes)]
		} else {
			dst.Modes = make([]ModeParams, len(p.Modes))
		}

		for i := range p.Modes {
			p.Modes[i].copyInto(&dst.Modes[i])
		}
	}

	p.Chebyshev.copyInto(&dst.Chebyshev)
}

// copyFloat64s copies src into dst, reusing dst's backing array when it is
// large enough. A nil src yields a nil result, so callers that care about the
// nil/empty distinction keep it.
func copyFloat64s(dst, src []float64) []float64 {
	if src == nil {
		return nil
	}

	// dst != nil matters for the empty-but-not-nil source: reslicing a nil dst
	// to length zero would hand back a nil slice and silently turn [] into null.
	// sharesBacking matters for a dst that already aliases src, where reusing
	// the array would leave the two views pointing at the same elements.
	if dst != nil && cap(dst) >= len(src) && !sharesBacking(dst, src) {
		dst = dst[:len(src)]
	} else {
		dst = make([]float64, len(src))
	}

	copy(dst, src)

	return dst
}

// sharesBacking reports whether dst and src are views onto the same backing
// array. It is the guard that keeps a copy-into from degenerating into an
// alias when the destination was seeded from the source, which a shallow struct
// copy does for free: after dst := *p, dst.Modes and p.Modes are the same array.
//
// Comparing the first element of each slice expanded to its full capacity
// catches every alias a shallow copy or a leading reslice can produce. A slice
// deliberately offset into the middle of the other's array is not detected;
// ordering two pointers to decide that needs unsafe, and no caller in this
// codebase constructs one.
func sharesBacking[T any](dst, src []T) bool {
	if len(dst) == 0 || len(src) == 0 {
		return false
	}

	return &dst[:cap(dst)][0] == &src[:cap(src)][0]
}

// ValidateBarParams validates bar model parameters.
func ValidateBarParams(params *BarParams) error {
	if params == nil {
		return errors.New("bar params cannot be nil")
	}

	if err := validateFiniteRange("input_mix", params.InputMix, InputMixMin, InputMixMax); err != nil {
		return err
	}

	if err := validateFiniteRange("filter_frequency", params.FilterFrequency, FilterFrequencyMinHz, FilterFrequencyMaxHz); err != nil {
		return err
	}

	if err := validateFiniteRange("base_frequency", params.BaseFrequency, FrequencyMinHz, FrequencyMaxHz); err != nil {
		return err
	}

	if err := validateFiniteRange("output_gain_db", params.OutputGainDB, OutputGainDBMin, OutputGainDBMax); err != nil {
		return err
	}

	// A nil exponent is the absence of a value rather than a zero, so it is not
	// range-checked: it means DecayKeytrackDefault, which is inside the range by
	// construction.
	if params.DecayKeytrack != nil {
		err := validateFiniteRange("decay_keytrack", *params.DecayKeytrack, DecayKeytrackMin, DecayKeytrackMax)
		if err != nil {
			return err
		}
	}

	if len(params.Modes) > MaxModes {
		return fmt.Errorf("modes: %d exceeds the maximum of %d", len(params.Modes), MaxModes)
	}

	for modeIndex, mode := range params.Modes {
		if len(mode.Harmonics) > MaxHarmonics {
			return fmt.Errorf("modes[%d].harmonics: %d exceeds the maximum of %d", modeIndex, len(mode.Harmonics), MaxHarmonics)
		}

		for harmonicIndex, gain := range mode.Harmonics {
			if !isFiniteInRange(gain, HarmonicGainMin, HarmonicGainMax) {
				return fmt.Errorf("modes[%d].harmonics[%d] out of range [%g, %g]: %g",
					modeIndex, harmonicIndex, HarmonicGainMin, HarmonicGainMax, gain)
			}
		}

		if !isFiniteInRange(mode.Amplitude, AmplitudeMin, AmplitudeMax) {
			return rangeErrorf("modes[%d].amplitude", modeIndex, mode.Amplitude, AmplitudeMin, AmplitudeMax)
		}

		if !isFiniteInRange(mode.Frequency, FrequencyMinHz, FrequencyMaxHz) {
			return rangeErrorf("modes[%d].frequency", modeIndex, mode.Frequency, FrequencyMinHz, FrequencyMaxHz)
		}

		// The validation ceiling, not the authoring bound: these params may
		// already have been transposed down, which inflates every decay.
		if !isFiniteInRange(mode.DecayMs, DecayMsMin, DecayMsValidationMax) {
			return rangeErrorf("modes[%d].decay_ms", modeIndex, mode.DecayMs, DecayMsMin, DecayMsValidationMax)
		}
	}

	if stage := params.Chebyshev.Stage; stage != "" && stage != ChebyshevStageExcitation && stage != ChebyshevStageOutput {
		return fmt.Errorf("chebyshev.stage must be %q or %q: %q", ChebyshevStageExcitation, ChebyshevStageOutput, stage)
	}

	for gainIndex, gain := range params.Chebyshev.HarmonicGains {
		if !isFiniteInRange(gain, HarmonicGainMin, HarmonicGainMax) {
			return rangeErrorf("chebyshev.harmonic_gains[%d]", gainIndex, gain, HarmonicGainMin, HarmonicGainMax)
		}
	}

	return nil
}

func isFiniteInRange(value, min, max float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= min && value <= max
}

func validateFiniteRange(field string, value, min, max float64) error {
	if !isFiniteInRange(value, min, max) {
		return rangeError(field, value, min, max)
	}

	return nil
}

func rangeError(field string, value, min, max float64) error {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return fmt.Errorf("%s must be finite", field)
	}

	return fmt.Errorf("%s out of range [%g, %g]: %g", field, min, max, value)
}

func rangeErrorf(fieldFmt string, index int, value, min, max float64) error {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return fmt.Errorf(fieldFmt+" must be finite", index)
	}

	return fmt.Errorf(fieldFmt+" out of range [%g, %g]: %g", index, min, max, value)
}
