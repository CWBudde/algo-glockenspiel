package oscbank

import (
	"math"
	"math/rand"
	"testing"
)

// Coefficient regimes. The interesting failures in a decaying recursion do not
// live in the middle of the parameter space, they live at the two ends of it,
// so the fuzzer gets an explicit knob for which end it is exploring rather than
// having to find them by luck.
const (
	regimeSustained  = iota // decay factors just below 1: the slowest contraction
	regimeCollapsing        // decay fast enough to reach denormals inside a chunk
	regimeLogUniform        // the whole range at once
	regimeSplit             // half the rotors sustained, half collapsing
	regimeCount
)

// Amplitude regimes, for the excitation path rather than the recursion.
const (
	amplitudeUnit   = iota // ordinary amplitudes around one
	amplitudeTiny          // amplitudes small enough to drive denormal state
	amplitudeSpread        // several decades between the loudest and quietest rotor
	amplitudeSilent        // no excitation at all; only the seeded state rings
	amplitudeModeCount
)

// FuzzOscBankMatchesGeneric holds every backend on the running machine to the
// numeric contract in docs/oscillator-bank.md: fused packed backends agree with
// each other to the bit, and every backend stays inside contractTolerance of the
// portable reference.
//
// It drives processRotorBlocks directly rather than going through Bank, because
// Bank can only produce coefficients that correspond to a real oscillator and
// the contract has to hold for the ones it cannot.
func FuzzOscBankMatchesGeneric(f *testing.F) {
	// The seed corpus is the pathology list, not random bytes. Everything here
	// runs as an ordinary test in CI, so these cases are covered whether or not
	// anyone ever runs the fuzzer.
	seeds := []struct {
		name        string
		pairs       uint8
		samples     uint8
		regime      uint8
		amplitude   uint8
		silentLanes uint32
		seed        int64
	}{
		{"single-sample", 0, 0, regimeLogUniform, amplitudeUnit, 0, 1},
		{"sustained-full-chunk", 3, 255, regimeSustained, amplitudeUnit, 0, 2},
		{"collapsing-to-denormal", 1, 200, regimeCollapsing, amplitudeTiny, 0, 3},
		{"ragged-length-7", 0, 6, regimeLogUniform, amplitudeUnit, 0, 4},
		{"ragged-length-9", 1, 8, regimeSustained, amplitudeSpread, 0, 5},
		{"ragged-length-255", 3, 254, regimeSplit, amplitudeSpread, 0, 6},
		{"half-the-lanes-empty", 2, 100, regimeSustained, amplitudeUnit, 0x0000FFFF, 7},
		{"one-live-lane", 3, 64, regimeSustained, amplitudeUnit, 0xFFFFFFFE, 8},
		{"no-excitation", 1, 128, regimeSustained, amplitudeSilent, 0, 9},
		{"tiny-amplitudes-long", 2, 255, regimeCollapsing, amplitudeTiny, 0xAAAAAAAA, 10},
		{"widest-bank", 3, 255, regimeSplit, amplitudeSpread, 0x00FF00FF, 11},

		// Partially-populated lanes, which the voice-major reading of the mask
		// turns into a bank with some voices sounding and some not. That is the
		// ordinary state of a polyphonic engine, and it is the one pathology the
		// rotor-major path never had: there, an unused lane is padding the bank
		// itself controls, while here it is a voice the caller has not set.
		{"two-voices-idle", 2, 100, regimeSustained, amplitudeUnit, 0b00100100, 12},
		{"one-voice-sounding", 3, 200, regimeSplit, amplitudeSpread, 0b11111110, 13},
		{"alternating-voices", 1, 63, regimeCollapsing, amplitudeUnit, 0b01010101, 14},
	}

	for _, seed := range seeds {
		f.Add(seed.pairs, seed.samples, seed.regime, seed.amplitude, seed.silentLanes, seed.seed)
	}

	backends := availableBackends()

	f.Fuzz(func(t *testing.T, pairs, samples, regime, amplitude uint8, silentLanes uint32, seed int64) {
		// Blocks come in pairs because the kernels consume them that way; the
		// bank guarantees an even count for exactly this reason.
		blocks := 2 * (1 + int(pairs)%4)
		chunk := 1 + int(samples)

		state, input := generateCase(blocks, chunk, int(regime)%regimeCount, int(amplitude)%amplitudeModeCount, silentLanes, seed)

		tolerance := contractTolerance(state, input)
		if math.IsNaN(tolerance) || math.IsInf(tolerance, 0) {
			t.Skip("degenerate case: the error envelope is not finite")
		}

		requireBackendsAgree(t, backends, tolerance, func(current backend) []float32 {
			return state.clone().render(current.features, input)
		})

		// The same parameters, read as a voice-major bank: the block count
		// becomes a rotor count, the silent-lane mask becomes a silent-voice
		// mask, and the excitation becomes one stream per voice. Everything the
		// pathology list is exercising -- both decay extremes, zero-amplitude
		// rotors, chunk lengths that are not multiples of eight, a single-sample
		// chunk -- means the same thing on either layout, so the two paths share
		// a corpus rather than needing two.
		voiceState, voiceInput := generateVoiceCase(
			blocks, chunk, int(regime)%regimeCount, int(amplitude)%amplitudeModeCount, silentLanes, seed,
		)

		voiceTolerance := voiceContractTolerance(voiceState, voiceInput)
		if math.IsNaN(voiceTolerance) || math.IsInf(voiceTolerance, 0) {
			return
		}

		requireBackendsAgree(t, backends, voiceTolerance, func(current backend) []float32 {
			return voiceState.clone().render(current, voiceInput)
		})
	})
}

// generateCase builds one bank's worth of coefficients and one chunk of
// excitation. Coefficients are drawn as a decay factor and a phase rather than
// as an unconstrained (cos, sin) pair, because a pair with a magnitude above 1
// describes a growing recursion, and the contract only covers contractions.
func generateCase(blocks, chunk, regime, amplitude int, silentLanes uint32, seed int64) (rotorState, []float32) {
	rng := rand.New(rand.NewSource(seed))
	state := newRotorState(blocks)
	input := make([]float32, chunk)

	for lane := range state.re {
		// Padding lanes are what a partly filled bank actually holds: zero
		// coefficients, zero amplitude, zero state, forever.
		if silentLanes>>(lane%32)&1 == 1 {
			continue
		}

		decay := decayForRegime(rng, regime, lane)
		phase := 2 * math.Pi * rng.Float64()
		sinVal, cosVal := math.Sincos(phase)

		state.cosCoeff[lane] = float32(decay * cosVal)
		state.sinCoeff[lane] = float32(decay * sinVal)
		state.amp[lane] = float32(amplitudeFor(rng, amplitude, lane))

		// Non-zero initial state on every live lane. A zeroed bank leaves half
		// of every packed kernel multiplying by zero for the whole render.
		scale := 1.0
		if amplitude == amplitudeTiny {
			scale = 1e-30
		}

		state.re[lane] = float32(scale * rng.NormFloat64())
		state.im[lane] = float32(scale * rng.NormFloat64())
	}

	if amplitude != amplitudeSilent {
		input[0] = 1

		for i := 1; i < len(input); i++ {
			input[i] = float32(rng.NormFloat64() * 0.05)
		}
	}

	return state, input
}

// decayForRegime returns a per-sample decay factor strictly inside [0, 1).
// The exponent is drawn logarithmically: exp(-1e-7) is a rotor that has barely
// decayed after a whole chunk, exp(-24) is one that reaches denormals in a
// handful of samples.
func decayForRegime(rng *rand.Rand, regime, lane int) float64 {
	const (
		slowest = -16.1 // ln(1e-7), a decay factor of 1 - 1e-7
		fastest = 3.2   // ln(24.5), a decay factor of about 2e-11
	)

	switch regime {
	case regimeSustained:
		return math.Exp(-math.Exp(slowest + 4*rng.Float64()))
	case regimeCollapsing:
		return math.Exp(-math.Exp(fastest - 4*rng.Float64()))
	case regimeSplit:
		if lane%2 == 0 {
			return math.Exp(-math.Exp(slowest + 2*rng.Float64()))
		}

		return math.Exp(-math.Exp(fastest - 2*rng.Float64()))
	default:
		return math.Exp(-math.Exp(slowest + (fastest-slowest)*rng.Float64()))
	}
}

func amplitudeFor(rng *rand.Rand, mode, lane int) float64 {
	switch mode {
	case amplitudeTiny:
		return 1e-30 * rng.NormFloat64()
	case amplitudeSpread:
		return math.Pow(10, -6*rng.Float64()) * rng.NormFloat64()
	case amplitudeSilent:
		return 0
	default:
		if lane%8 == 3 {
			// A live rotor with no drive: it rings down from its seeded state
			// while its neighbours are excited, which is what a harmonic with a
			// zero gain looks like.
			return 0
		}

		return rng.NormFloat64()
	}
}

// generateVoiceCase builds one voice-major bank and one chunk of interleaved
// excitation from the same knobs generateCase uses.
//
// silentVoices is read one bit per voice, low bit first, so a mask that means
// "these rotor lanes are padding" on the rotor-major path means "these voices
// are not sounding" here. Both are the case where part of a vector is inert;
// which part, and who owns it, is what the two layouts disagree about.
//
// Every voice gets its own excitation stream, including the silent ones. Driving
// a lane whose amplitudes are all zero costs nothing numerically and is exactly
// what a lane-crosstalk bug would need in order to show up.
func generateVoiceCase(rotors, chunk, regime, amplitude int, silentVoices uint32, seed int64) (voiceRotorState, []float32) {
	rng := rand.New(rand.NewSource(seed))
	state := newVoiceRotorState(rotors)
	input := make([]float32, chunk*LaneWidth)

	for voice := range LaneWidth {
		if silentVoices>>voice&1 == 1 {
			continue
		}

		for rotor := range rotors {
			lane := rotor*LaneWidth + voice

			decay := decayForRegime(rng, regime, rotor)
			phase := 2 * math.Pi * rng.Float64()
			sinVal, cosVal := math.Sincos(phase)

			state.cosCoeff[lane] = float32(decay * cosVal)
			state.sinCoeff[lane] = float32(decay * sinVal)
			state.amp[lane] = float32(amplitudeFor(rng, amplitude, rotor))

			scale := 1.0
			if amplitude == amplitudeTiny {
				scale = 1e-30
			}

			state.re[lane] = float32(scale * rng.NormFloat64())
			state.im[lane] = float32(scale * rng.NormFloat64())
		}
	}

	if amplitude != amplitudeSilent {
		for voice := range LaneWidth {
			input[voice] = 1
		}

		for i := LaneWidth; i < len(input); i++ {
			input[i] = float32(rng.NormFloat64() * 0.05)
		}
	}

	return state, input
}
