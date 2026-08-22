package oscbank

import (
	"math"
	"math/rand"
	"testing"

	"github.com/cwbudde/algo-glockenspiel/internal/cpufeat"
)

// voiceTestOscillators builds one voice's configuration. Every voice of a bank
// has the same shape -- two oscillators, three harmonics -- and differs only in
// frequency, decay and amplitude, which is exactly the shape a polyphonic engine
// holds: one preset, many notes.
func voiceTestOscillators(voice int) []Oscillator {
	base := 220 * math.Pow(2, float64(voice)/12)

	return []Oscillator{
		{
			Amplitude: 0.8 - 0.05*float64(voice),
			Frequency: base,
			DecayMs:   400 + 50*float64(voice),
			Harmonics: []float64{1, 0.5, 0.25},
		},
		{
			Amplitude: 0.3 + 0.02*float64(voice),
			Frequency: base * 2.7,
			DecayMs:   120 + 10*float64(voice),
			Harmonics: []float64{0.6, 0.2, 0.1},
		},
	}
}

// interleavedExcitation builds frames frames of LaneWidth streams, one per
// voice, each different from every other so a kernel that read the wrong lane
// could not accidentally produce the right answer.
func interleavedExcitation(frames int, seed int64) []float32 {
	rng := rand.New(rand.NewSource(seed))
	input := make([]float32, frames*LaneWidth)

	for voice := range LaneWidth {
		input[voice] = float32(1 + voice)

		for frame := 1; frame < frames; frame++ {
			input[frame*LaneWidth+voice] = float32(rng.NormFloat64() * 0.05)
		}
	}

	return input
}

// laneOf extracts one voice's samples from an interleaved buffer.
func laneOf(interleaved []float32, voice int) []float32 {
	out := make([]float32, len(interleaved)/LaneWidth)

	for frame := range out {
		out[frame] = interleaved[frame*LaneWidth+voice]
	}

	return out
}

// withBackend forces a dispatch path for the length of fn.
func withBackend(current backend, fn func()) {
	cpufeat.SetForcedFeatures(current.features)

	defer cpufeat.ResetDetection()

	fn()
}

// TestVoiceBankIsBitIdenticalToSingleVoiceRenders is the load-bearing test for
// the whole layout.
//
// There is no cross-voice arithmetic in a [rotor][voice] bank. Lane l's rotors
// are driven by lane l's excitation and summed into lane l's output, and no
// instruction on the path ever mixes two lanes -- that is the entire reason the
// horizontal fold could be deleted rather than reordered. So the right assertion
// is equality, not a tolerance: eight voices rendered together must produce the
// same float32 words as eight voices rendered one at a time. A tolerance here
// would pass while lanes leaked into each other, which is the one bug this
// layout can have and the rotor-major one cannot.
//
// The single-voice references are rendered twice: once with the voice left in
// its own lane, and once moved to lane 0 with its excitation moved with it. The
// first pins that neighbours do not perturb a voice; the second pins that a
// voice does not depend on which lane it sits in.
func TestVoiceBankIsBitIdenticalToSingleVoiceRenders(t *testing.T) {
	// Longer than blockSamples, and not a multiple of it or of the lane width,
	// so the comparison spans a chunk boundary and a ragged final chunk.
	const frames = 3*blockSamples/2 + 7

	input := interleavedExcitation(frames, 20240612)

	for _, current := range availableBackends() {
		t.Run(current.name, func(t *testing.T) {
			together := make([]float32, frames*LaneWidth)

			withBackend(current, func() {
				bank := NewVoiceBank(48000)

				for voice := range LaneWidth {
					if err := bank.SetVoice(voice, voiceTestOscillators(voice)); err != nil {
						t.Fatalf("SetVoice(%d): %v", voice, err)
					}
				}

				bank.ProcessBlock(input, together)
			})

			for voice := range LaneWidth {
				inPlace := make([]float32, frames*LaneWidth)
				moved := make([]float32, frames*LaneWidth)

				// The excitation for the moved render: this voice's stream in
				// lane 0 and silence everywhere else. The bank still advances
				// all eight lanes; seven of them are inert.
				movedInput := make([]float32, frames*LaneWidth)
				for frame := range frames {
					movedInput[frame*LaneWidth] = input[frame*LaneWidth+voice]
				}

				withBackend(current, func() {
					alone := NewVoiceBank(48000)
					if err := alone.SetVoice(voice, voiceTestOscillators(voice)); err != nil {
						t.Fatalf("SetVoice(%d): %v", voice, err)
					}

					alone.ProcessBlock(input, inPlace)

					relocated := NewVoiceBank(48000)
					if err := relocated.SetVoice(0, voiceTestOscillators(voice)); err != nil {
						t.Fatalf("SetVoice(0): %v", err)
					}

					relocated.ProcessBlock(movedInput, moved)
				})

				requireBitIdentical(t, "voice alone in its own lane", laneOf(inPlace, voice), laneOf(together, voice))
				requireBitIdentical(t, "voice moved to lane 0", laneOf(moved, 0), laneOf(together, voice))
			}
		})
	}
}

// TestVoiceBankRendersEveryVoiceAudibly guards against the previous test being
// satisfied by silence: a bank that rendered nothing at all would be bit-
// identical to eight banks that rendered nothing at all.
func TestVoiceBankRendersEveryVoiceAudibly(t *testing.T) {
	const frames = 512

	bank := NewVoiceBank(48000)

	for voice := range LaneWidth {
		if err := bank.SetVoice(voice, voiceTestOscillators(voice)); err != nil {
			t.Fatalf("SetVoice(%d): %v", voice, err)
		}
	}

	input := interleavedExcitation(frames, 7)
	output := make([]float32, frames*LaneWidth)

	bank.ProcessBlock(input, output)

	for voice := range LaneWidth {
		peak := float32(0)
		for _, sample := range laneOf(output, voice) {
			peak = max(peak, float32(math.Abs(float64(sample))))
		}

		if peak < 0.01 {
			t.Fatalf("voice %d is silent: peak %g", voice, peak)
		}
	}
}

// TestVoiceBankEmptyLanesStaySilent pins the inert-lane invariant. A lane nobody
// configured must produce exact zeros no matter what is fed into it, which is
// what lets an engine drive a partially populated bank without masking.
func TestVoiceBankEmptyLanesStaySilent(t *testing.T) {
	const frames = 300

	bank := NewVoiceBank(48000)

	for _, voice := range []int{1, 4} {
		if err := bank.SetVoice(voice, voiceTestOscillators(voice)); err != nil {
			t.Fatalf("SetVoice(%d): %v", voice, err)
		}
	}

	input := interleavedExcitation(frames, 11)
	output := make([]float32, frames*LaneWidth)

	bank.ProcessBlock(input, output)

	for voice := range LaneWidth {
		if voice == 1 || voice == 4 {
			continue
		}

		for frame, sample := range laneOf(output, voice) {
			if sample != 0 {
				t.Fatalf("unconfigured voice %d rang at frame %d: %g", voice, frame, sample)
			}
		}
	}
}

// TestVoiceBankResetVoiceClearsOnlyThatLane is why ResetVoice exists. Bank.Reset
// clears the arrays wholesale, which on this layout would silence every other
// sounding voice: one voice's state is a stride, not a range.
func TestVoiceBankResetVoiceClearsOnlyThatLane(t *testing.T) {
	const frames = 128

	bank := NewVoiceBank(48000)

	for voice := range LaneWidth {
		if err := bank.SetVoice(voice, voiceTestOscillators(voice)); err != nil {
			t.Fatalf("SetVoice(%d): %v", voice, err)
		}
	}

	input := interleavedExcitation(frames, 13)
	output := make([]float32, frames*LaneWidth)

	bank.ProcessBlock(input, output)

	bank.ResetVoice(3)

	// Nothing drives the bank now, so every lane rings down from its own state.
	silence := make([]float32, frames*LaneWidth)
	tail := make([]float32, frames*LaneWidth)

	bank.ProcessBlock(silence, tail)

	for _, sample := range laneOf(tail, 3) {
		if sample != 0 {
			t.Fatalf("the reset voice still rings: %g", sample)
		}
	}

	for voice := range LaneWidth {
		if voice == 3 {
			continue
		}

		peak := float32(0)
		for _, sample := range laneOf(tail, voice) {
			peak = max(peak, float32(math.Abs(float64(sample))))
		}

		if peak == 0 {
			t.Fatalf("resetting voice 3 silenced voice %d", voice)
		}
	}
}

// TestVoiceBankSetVoiceKeepsNeighboursRinging is the note-on case: retuning one
// lane while the others sound must not disturb them. It holds only while the
// shape is unchanged, which is what an engine driving every voice from one
// preset guarantees, and the doc comment on SetVoice says so.
func TestVoiceBankSetVoiceKeepsNeighboursRinging(t *testing.T) {
	const frames = 96

	build := func() *VoiceBank {
		bank := NewVoiceBank(48000)

		for voice := range LaneWidth {
			if err := bank.SetVoice(voice, voiceTestOscillators(voice)); err != nil {
				t.Fatalf("SetVoice(%d): %v", voice, err)
			}
		}

		return bank
	}

	input := interleavedExcitation(frames, 17)
	silence := make([]float32, frames*LaneWidth)

	untouched := build()
	retuned := build()

	first := make([]float32, frames*LaneWidth)
	second := make([]float32, frames*LaneWidth)

	untouched.ProcessBlock(input, first)
	retuned.ProcessBlock(input, second)

	requireBitIdentical(t, "the two banks start out identical", second, first)

	// Voice 5 gets a different note, with the same oscillator and harmonic
	// counts. Every other lane must carry on exactly as it would have.
	changed := voiceTestOscillators(5)
	changed[0].Frequency *= 1.5

	if err := retuned.SetVoice(5, changed); err != nil {
		t.Fatalf("SetVoice(5): %v", err)
	}

	untouched.ProcessBlock(silence, first)
	retuned.ProcessBlock(silence, second)

	for voice := range LaneWidth {
		if voice == 5 {
			continue
		}

		requireBitIdentical(t, "neighbour of a retuned voice", laneOf(second, voice), laneOf(first, voice))
	}
}

// TestVoiceBankChunkingIsContinuous pins that splitting a render at an arbitrary
// frame boundary changes nothing: the rotor state carries across chunks, and the
// interleaved slicing has to carry with it.
func TestVoiceBankChunkingIsContinuous(t *testing.T) {
	const frames = 2*blockSamples + 61

	input := interleavedExcitation(frames, 19)

	whole := make([]float32, frames*LaneWidth)
	piecewise := make([]float32, frames*LaneWidth)

	build := func() *VoiceBank {
		bank := NewVoiceBank(44100)

		for voice := range LaneWidth {
			if err := bank.SetVoice(voice, voiceTestOscillators(voice)); err != nil {
				t.Fatalf("SetVoice(%d): %v", voice, err)
			}
		}

		return bank
	}

	build().ProcessBlock(input, whole)

	split := build()

	for start := 0; start < frames; start += 37 {
		end := min(start+37, frames)

		split.ProcessBlock(input[start*LaneWidth:end*LaneWidth], piecewise[start*LaneWidth:end*LaneWidth])
	}

	requireBitIdentical(t, "chunked render", piecewise, whole)
}

// TestVoiceBankMatchesRotorMajorBankWithinContract is the semantic check the
// bit-identity test cannot be: it asks whether a voice-major render is the same
// *sound* as the rotor-major one, not the same program.
//
// It cannot be bit-identical and must not be asserted as if it were. Bank sums a
// voice's rotors through a lane fold -- a pairwise tree over four accumulator
// lanes -- while VoiceBank sums them rotor pair by rotor pair into the output.
// Same rotors, same coefficients, different summation order, so the two differ
// by the fold term of the contract and nothing more.
func TestVoiceBankMatchesRotorMajorBankWithinContract(t *testing.T) {
	const frames = 400

	oscillators := voiceTestOscillators(2)

	mono := New(48000)
	if err := mono.SetOscillators(oscillators); err != nil {
		t.Fatalf("SetOscillators: %v", err)
	}

	poly := NewVoiceBank(48000)
	if err := poly.SetVoice(6, oscillators); err != nil {
		t.Fatalf("SetVoice: %v", err)
	}

	scalarInput := make([]float32, frames)
	scalarInput[0] = 1

	for i := 1; i < frames; i++ {
		scalarInput[i] = float32(math.Sin(float64(i)) * 0.05)
	}

	interleaved := make([]float32, frames*LaneWidth)
	for i := range frames {
		interleaved[i*LaneWidth+6] = scalarInput[i]
	}

	want := make([]float32, frames)
	mono.ProcessBlock(scalarInput, want)

	polyOut := make([]float32, frames*LaneWidth)
	poly.ProcessBlock(interleaved, polyOut)

	requireWithinContract(t, "voice-major vs rotor-major", laneOf(polyOut, 6), want,
		bankTolerance(mono, scalarInput))
}

func TestVoiceBankRejectsBadInput(t *testing.T) {
	bank := NewVoiceBank(48000)

	if err := bank.SetVoice(-1, nil); err == nil {
		t.Fatal("a negative voice index was accepted")
	}

	if err := bank.SetVoice(LaneWidth, nil); err == nil {
		t.Fatal("an out-of-range voice index was accepted")
	}

	if err := bank.SetVoice(0, []Oscillator{{Amplitude: math.NaN(), Frequency: 440, DecayMs: 100}}); err == nil {
		t.Fatal("a non-finite amplitude was accepted")
	}

	if err := bank.SetVoice(0, []Oscillator{{Amplitude: 1, Frequency: 440, DecayMs: -1}}); err == nil {
		t.Fatal("a negative decay was accepted")
	}

	defer func() {
		if recover() == nil {
			t.Fatal("a ragged interleaved buffer did not panic")
		}
	}()

	bank.ProcessBlock(make([]float32, LaneWidth+1), make([]float32, LaneWidth+1))
}

// TestVoiceBankMutedVoiceIsInert covers the decay floor. A voice at or below
// minDecayMs must hold zero coefficients rather than a division by zero, so an
// engine can park a lane by muting it instead of by clearing it.
func TestVoiceBankMutedVoiceIsInert(t *testing.T) {
	const frames = 64

	bank := NewVoiceBank(48000)

	muted := voiceTestOscillators(0)
	for i := range muted {
		muted[i].DecayMs = 0
	}

	if err := bank.SetVoice(0, muted); err != nil {
		t.Fatalf("SetVoice: %v", err)
	}

	if err := bank.SetVoice(1, voiceTestOscillators(1)); err != nil {
		t.Fatalf("SetVoice: %v", err)
	}

	input := interleavedExcitation(frames, 23)
	output := make([]float32, frames*LaneWidth)

	bank.ProcessBlock(input, output)

	for frame, sample := range laneOf(output, 0) {
		if sample != 0 {
			t.Fatalf("a muted voice produced %g at frame %d", sample, frame)
		}
	}
}

// TestVoiceBankShapeIsTheWidestVoice pins the rectangular-layout rule: the bank
// sizes itself to the largest voice, and a voice that carries fewer oscillators
// leaves its trailing rotors inert instead of reading a neighbour's.
func TestVoiceBankShapeIsTheWidestVoice(t *testing.T) {
	bank := NewVoiceBank(48000)

	if err := bank.SetVoice(0, []Oscillator{{Amplitude: 1, Frequency: 440, DecayMs: 100}}); err != nil {
		t.Fatalf("SetVoice(0): %v", err)
	}

	if got := bank.NumRotors(); got != 1 {
		t.Fatalf("one oscillator with no harmonics is %d rotors, want 1", got)
	}

	if err := bank.SetVoice(3, voiceTestOscillators(3)); err != nil {
		t.Fatalf("SetVoice(3): %v", err)
	}

	if got, want := bank.NumOscillators(), 2; got != want {
		t.Fatalf("NumOscillators = %d, want %d", got, want)
	}

	if got, want := bank.NumHarmonics(), 3; got != want {
		t.Fatalf("NumHarmonics = %d, want %d", got, want)
	}

	if got, want := bank.NumRotors(), 6; got != want {
		t.Fatalf("NumRotors = %d, want %d", got, want)
	}

	// Voice 0 carries one oscillator with one partial, so five of its six rotor
	// slots must be inert.
	live := 0

	for rotor := range bank.NumRotors() {
		if bank.amp[rotor*LaneWidth] != 0 {
			live++
		}
	}

	if live != 1 {
		t.Fatalf("the narrow voice occupies %d rotors, want 1", live)
	}
}

func BenchmarkVoiceBank8Voices2x3(b *testing.B) {
	const frames = 512

	bank := NewVoiceBank(48000)

	for voice := range LaneWidth {
		if err := bank.SetVoice(voice, voiceTestOscillators(voice)); err != nil {
			b.Fatalf("SetVoice(%d): %v", voice, err)
		}
	}

	input := interleavedExcitation(frames, 29)
	output := make([]float32, frames*LaneWidth)

	b.ResetTimer()

	for range b.N {
		bank.ProcessBlock(input, output)
	}
}

// TestVoiceBankNarrowingTheWidestLaneResetsEveryLane pins the half of the shape
// rule that is easy to miss: the bank is as wide as its widest lane, so
// narrowing or clearing that lane moves the shape downwards just as surely as a
// wider voice moves it upwards, and either direction discards every lane's
// rotor state. It is the reason SetVoice(index, nil) is not the way to silence
// one voice while its neighbours ring -- ResetVoice is.
func TestVoiceBankNarrowingTheWidestLaneResetsEveryLane(t *testing.T) {
	const frames = 64

	bank := NewVoiceBank(48000)

	// Lane 0 is the widest: two oscillators of three harmonics against one
	// oscillator of one harmonic everywhere else.
	if err := bank.SetVoice(0, voiceTestOscillators(0)); err != nil {
		t.Fatalf("SetVoice(0): %v", err)
	}

	narrow := []Oscillator{{Amplitude: 0.5, Frequency: 440, DecayMs: 500}}

	for voice := 1; voice < LaneWidth; voice++ {
		if err := bank.SetVoice(voice, narrow); err != nil {
			t.Fatalf("SetVoice(%d): %v", voice, err)
		}
	}

	if got, want := bank.NumRotors(), 2*3; got != want {
		t.Fatalf("NumRotors() = %d, want %d -- the bank should be as wide as lane 0", got, want)
	}

	// Ring every lane, so there is state to lose.
	bank.ProcessBlock(interleavedExcitation(frames, 23), make([]float32, frames*LaneWidth))

	if err := bank.SetVoice(0, narrow); err != nil {
		t.Fatalf("SetVoice(0, narrow): %v", err)
	}

	if got, want := bank.NumRotors(), 1; got != want {
		t.Fatalf("NumRotors() = %d, want %d after the widest lane narrowed", got, want)
	}

	// Every lane, not only lane 0, is silent into silence: the reshape cleared
	// the whole bank.
	silence := make([]float32, frames*LaneWidth)
	output := make([]float32, frames*LaneWidth)

	bank.ProcessBlock(silence, output)

	for _, sample := range output {
		if sample != 0 {
			t.Fatalf("a lane still rings after the shape shrank; rotor state should have been discarded")
		}
	}
}
