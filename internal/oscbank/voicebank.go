package oscbank

import "fmt"

// VoiceBank renders up to LaneWidth voices at once by making the lane index the
// voice index.
//
// Bank is rotor-major: one voice's N*M rotors fill the lanes, one scalar
// excitation is broadcast to all of them, and the lanes are folded horizontally
// down to one scalar per sample. That is the right layout for an offline render
// of a single note, and it is the layout every golden vector in this package
// pins. It is the wrong layout for a polyphonic realtime engine, which pays for
// it once per sounding voice.
//
// This bank turns the array inside out. The rotor arrays are [rotor][voice]:
// rotor r of every voice sits contiguously in one LaneWidth-wide vector, so one
// packed step advances the same partial of eight different voices. Two things
// follow, and both are the point:
//
//   - the excitation is no longer one scalar. input is [samples][LaneWidth],
//     interleaved, so each voice is driven by its own excitation stream;
//   - there is no horizontal fold. Summing over rotors already produces one
//     value per voice, so the accumulator is the output, interleaved the same
//     way. reduceLanes has no counterpart on this path -- it is removed, not
//     reordered. See rule four in docs/oscillator-bank.md.
//
// Every voice of a bank has the same shape: the same oscillator count and the
// same harmonic count, differing only in frequency, decay and amplitude. That
// is what a polyphonic engine holds anyway -- one preset, many notes -- and it
// is what makes the layout rectangular. A voice that carries fewer oscillators
// than the shape leaves its trailing rotors inert, exactly the way Bank's
// padding lanes are inert.
//
// The cost is idle lanes. With fewer sounding voices than LaneWidth the bank
// still advances all eight, so a single sustained voice is *slower* here than
// in Bank. Bank therefore stays, and offline rendering keeps using it.
type VoiceBank struct {
	sampleRate float64

	// voices[i] is lane i's configuration. A nil or empty entry is a lane with
	// no voice: it holds zero coefficients and zero amplitude forever.
	voices [LaneWidth][]Oscillator

	numOsc  int
	numHarm int

	// numRotors is the per-voice rotor count, numOsc*numHarm. rotors is that
	// rounded up to an even number, because the kernels advance rotors in pairs
	// to hide the recursion's latency and must not need a tail path.
	numRotors int
	rotors    int

	// Rotor arrays in [rotor][voice] order, len == rotors*LaneWidth.
	rotorArrays

	// scratchIn holds one chunk of interleaved excitation with a zero *frame*
	// appended. The packed kernels read one sample ahead to keep the excitation
	// off the recursion's critical path, and on this path a sample is a whole
	// lane vector, so the guard is LaneWidth wide rather than one element.
	scratchIn []float32
}

// NewVoiceBank returns a bank with no voices configured at the given sample
// rate. Every lane starts inert, so a bank nobody has called SetVoice on
// renders silence.
func NewVoiceBank(sampleRate float64) *VoiceBank {
	if sampleRate <= 0 {
		sampleRate = defaultSampleRate
	}

	return &VoiceBank{
		sampleRate: sampleRate,
		scratchIn:  make([]float32, (blockSamples+1)*LaneWidth),
	}
}

// NumVoices returns the number of lanes, which is the number of voices the bank
// renders in one pass whether or not they are all configured.
func (v *VoiceBank) NumVoices() int { return LaneWidth }

// NumOscillators returns the bank's oscillator count N, the largest any single
// voice carries.
func (v *VoiceBank) NumOscillators() int { return v.numOsc }

// NumHarmonics returns the bank's harmonic count M, the largest number of
// partials any single oscillator of any voice carries.
func (v *VoiceBank) NumHarmonics() int { return v.numHarm }

// NumRotors returns the per-voice rotor count, N*M.
func (v *VoiceBank) NumRotors() int { return v.numRotors }

// SampleRate returns the current sample rate.
func (v *VoiceBank) SampleRate() float64 { return v.sampleRate }

// SetVoice configures one lane. Passing an empty or nil slice makes the lane
// inert: zero amplitude and zero coefficients, which is numerically the same
// thing a padding lane is, so an unused lane can neither ring nor produce a
// denormal.
//
// Reconfiguring a lane leaves every other lane's rotor state untouched, which
// is what a note-on has to do while its neighbours are still sounding. The one
// exception is a change of *shape*, and it cuts both ways. The bank is as wide
// as its widest lane, so the shape moves whenever this call changes that
// maximum -- growing it with a wider voice, and equally shrinking it by
// narrowing or clearing the lane that was the widest. Either way rotor r stops
// denoting the same partial, so all rotor state is discarded, the same way
// Bank.SetOscillators discards it, and every lane goes quiet rather than only
// this one.
//
// Clearing a lane is therefore not automatically the cheap operation it looks
// like: passing nil for the widest voice silences the others too. ResetVoice is
// what silences one lane and leaves the rest ringing. A polyphonic engine
// avoids the question entirely by pinning the shape once -- configure every
// lane at the widest shape the preset can produce, and no note-on can move it.
func (v *VoiceBank) SetVoice(index int, oscillators []Oscillator) error {
	if index < 0 || index >= LaneWidth {
		return fmt.Errorf("oscbank: voice index %d out of range [0, %d)", index, LaneWidth)
	}

	for i, osc := range oscillators {
		if err := validateOscillator(i, osc); err != nil {
			return fmt.Errorf("voices[%d]: %w", index, err)
		}
	}

	v.voices[index] = storeOscillators(v.voices[index], oscillators)

	if v.reshape() {
		// The layout moved, so every lane's coefficients are stale and every
		// lane's state means nothing. reshape has already cleared the state.
		v.calculateCoefficients()

		return nil
	}

	v.calculateVoiceCoefficients(index)

	return nil
}

// ResetVoice clears one lane's rotor state and leaves every other lane running.
// Bank.Reset cannot be used for this: rotor state for one voice is a stride
// through the arrays, not a contiguous range.
func (v *VoiceBank) ResetVoice(index int) {
	if index < 0 || index >= LaneWidth {
		return
	}

	for rotor := range v.rotors {
		v.re[rotor*LaneWidth+index] = 0
		v.im[rotor*LaneWidth+index] = 0
	}
}

// Reset clears every lane's rotor state.
func (v *VoiceBank) Reset() {
	clear(v.re)
	clear(v.im)
}

// SetSampleRate updates the sample rate and recomputes every rotor coefficient.
func (v *VoiceBank) SetSampleRate(sampleRate float64) {
	if sampleRate <= 0 {
		return
	}

	v.sampleRate = sampleRate
	v.calculateCoefficients()
}

// MaxDecayFactor returns the largest per-sample decay factor across every rotor
// of every lane.
func (v *VoiceBank) MaxDecayFactor() float64 {
	worst := 0.0

	for _, decay := range v.decayFactor {
		if decay > worst {
			worst = decay
		}
	}

	return worst
}

// ProcessBlock renders one block of interleaved excitation into interleaved
// output. Both buffers are [samples][LaneWidth] with lane i carrying voice i,
// len(input) must be a multiple of LaneWidth, and output must be at least as
// long as input.
//
// No horizontal fold happens here. Lane i of the output is voice i's signal,
// and separating the voices is the caller's deinterleave.
func (v *VoiceBank) ProcessBlock(input, output []float32) {
	if len(input)%LaneWidth != 0 {
		panic("oscbank: interleaved input length must be a multiple of LaneWidth")
	}

	if len(output) < len(input) {
		panic("oscbank: output buffer too small")
	}

	if v.rotors == 0 || len(input) == 0 {
		clear(output[:len(input)])
		return
	}

	// The rotors are what decay into denormal state, and every path into the
	// recursion goes through this function, so the save-set-restore sits here
	// and costs nothing against a whole block.
	scope := FlushDenormals()
	defer scope.Restore()

	frames := len(input) / LaneWidth

	for start := 0; start < frames; start += blockSamples {
		end := min(start+blockSamples, frames)

		v.processChunk(input[start*LaneWidth:end*LaneWidth], output[start*LaneWidth:end*LaneWidth])
	}
}

func (v *VoiceBank) processChunk(input, output []float32) {
	copy(v.scratchIn, input)
	clear(v.scratchIn[len(input) : len(input)+LaneWidth])

	processVoiceRotors(v.re, v.im, v.cosCoeff, v.sinCoeff, v.amp, v.rotors,
		v.scratchIn[:len(input)+LaneWidth], output)
}

// reshape recomputes the bank's shape from its lanes and resizes the rotor
// arrays if it moved. It reports whether the layout changed, in which case all
// rotor state has been discarded and every lane needs its coefficients again.
func (v *VoiceBank) reshape() bool {
	numOsc, numHarm := 0, 0

	for _, voice := range v.voices {
		if len(voice) > numOsc {
			numOsc = len(voice)
		}

		for _, osc := range voice {
			harmonics := max(len(osc.Harmonics), 1)
			if harmonics > numHarm {
				numHarm = harmonics
			}
		}
	}

	if numOsc == v.numOsc && numHarm == v.numHarm {
		return false
	}

	v.numOsc = numOsc
	v.numHarm = numHarm
	v.numRotors = numOsc * numHarm
	v.rotors = roundUpToEven(v.numRotors)

	v.allocate(v.rotors * LaneWidth)

	return true
}

func (v *VoiceBank) calculateCoefficients() {
	v.clearCoefficients()

	for index := range v.voices {
		v.writeVoiceCoefficients(index)
	}
}

// calculateVoiceCoefficients rewrites one lane and leaves the others alone.
func (v *VoiceBank) calculateVoiceCoefficients(index int) {
	for rotor := range v.rotors {
		lane := rotor*LaneWidth + index

		v.cosCoeff[lane] = 0
		v.sinCoeff[lane] = 0
		v.amp[lane] = 0
		v.decayFactor[lane] = 0
	}

	v.writeVoiceCoefficients(index)
}

// writeVoiceCoefficients fills one lane's live rotors. It assumes the lane has
// already been zeroed, so a rotor this voice does not reach stays inert.
func (v *VoiceBank) writeVoiceCoefficients(index int) {
	rotor := 0

	for _, osc := range v.voices[index] {
		decayFactor, decaying := decayFactorFor(osc.DecayMs, v.sampleRate)

		for harmonic := 0; harmonic < v.numHarm; harmonic++ {
			coeff, active := rotorCoefficients(osc, harmonic, decayFactor, decaying, v.sampleRate)
			if active {
				lane := rotor*LaneWidth + index

				v.decayFactor[lane] = coeff.decay
				v.cosCoeff[lane] = coeff.cos
				v.sinCoeff[lane] = coeff.sin
				v.amp[lane] = coeff.amp
			}

			rotor++
		}
	}
}
