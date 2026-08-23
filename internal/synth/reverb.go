package synth

import (
	"fmt"

	"github.com/cwbudde/algo-dsp/dsp/effects/reverb"
)

// ReverbParams is the fixed character of the room the engine plays in.
//
// Only the wet mix is reachable from a player-facing control; everything here
// is chosen once, for the instrument, and never turned live. That split is
// deliberate. A glockenspiel wants one room, and the useful gesture is "more of
// it" or "less of it" -- exposing decay, damping and pre-delay as well would be
// three more dials whose good settings are the ones written below.
type ReverbParams struct {
	// RT60 is the time the tail takes to fall by 60 dB, in seconds.
	RT60 float64
	// Damp is how much the tail loses its highs as it decays, 0..1.
	Damp float64
	// PreDelay is the gap between the strike and the first reflection, in
	// seconds. It is what keeps the attack legible: without it the tail starts
	// on top of the transient and the bar reads as smeared rather than as
	// struck in a room.
	PreDelay float64
	// ModRate is the delay-line modulation rate in Hz, which breaks up the
	// metallic ringing a static feedback delay network gives a bright source.
	ModRate float64
}

// DefaultReverbParams is the room the glockenspiel is played in.
//
// A small hall rather than a plate or a chamber: 2.2 s is long enough to be
// heard under a bar that is itself gone in about a second, and the damping is
// mild because taking the highs out of a tail under an instrument that is
// nothing but highs leaves a tail that sounds like a different instrument.
func DefaultReverbParams() ReverbParams {
	return ReverbParams{
		RT60:     2.2,
		Damp:     0.35,
		PreDelay: 0.012,
		ModRate:  0.1,
	}
}

const (
	// maxWetGain is the wet gain a fully open mix control asks for. The dry
	// path stays at unity, so this is how much reverb is added at the top of
	// the range rather than how much dry is left.
	maxWetGain = 0.75

	// rightPreDelayOffset and rightModRateOffset detune the right channel's
	// network from the left one. Two identical reverbs fed the two channels of
	// a stereo mix produce two tails that differ only by whatever difference
	// was in the input, which for a centred note is none -- a mono tail glued
	// to the middle of a stereo image the keyboard pan exists to spread out.
	// Offsetting the pre-delay and the modulation rate is the cheapest way to
	// decorrelate them: same room, two microphones, a few milliseconds apart.
	rightPreDelayOffset = 0.0031
	rightModRateOffset  = 0.017
)

// stereoReverb is a pair of feedback delay networks, one per channel, behind
// one wet-mix control.
//
// It is FDNReverb rather than the Freeverb beside it in algo-dsp for one
// reason: the Freeverb's comb and allpass lengths are hardcoded sample counts
// calibrated for 44.1 kHz and it has no SetSampleRate, so on the 48 kHz an
// AudioContext often runs at it would render a room roughly 9% smaller and
// brighter than the one it was tuned to be. FDNReverb takes the rate in its
// constructor and rescales its reference delays to it.
type stereoReverb struct {
	left  *reverb.FDNReverb
	right *reverb.FDNReverb

	// mix is the wet control, 0..1. wet gain is mix*maxWetGain; the dry gain
	// is always 1.
	mix float64

	// tailFrames counts down the frames still worth processing after the mix
	// reached zero. See process for why silence is not immediate.
	tailFrames int
	// tailLength is what tailFrames is reloaded with: RT60 at the engine's
	// sample rate, so a tail that is closed off is allowed to decay by the full
	// 60 dB before the network is reset and skipped.
	tailLength int

	// scratchL and scratchR deinterleave the block for the two mono networks.
	// Grow-only, like the engine's mixBuffer, so a wider callback than the last
	// one is the only thing that ever allocates here.
	scratchL []float64
	scratchR []float64
}

// newStereoReverb builds the pair for a sample rate.
func newStereoReverb(sampleRate int, p ReverbParams) (*stereoReverb, error) {
	if sampleRate <= 0 {
		return nil, fmt.Errorf("sample rate must be positive: %d", sampleRate)
	}

	left, err := newReverbChannel(sampleRate, p, 0, 0)
	if err != nil {
		return nil, fmt.Errorf("left channel: %w", err)
	}

	right, err := newReverbChannel(sampleRate, p, rightPreDelayOffset, rightModRateOffset)
	if err != nil {
		return nil, fmt.Errorf("right channel: %w", err)
	}

	r := &stereoReverb{
		left:       left,
		right:      right,
		tailLength: int(p.RT60 * float64(sampleRate)),
	}

	r.setMix(0)

	return r, nil
}

// newReverbChannel builds one network, offset from the nominal room by the two
// deltas that decorrelate the right channel from the left.
func newReverbChannel(sampleRate int, p ReverbParams, preDelayOffset, modRateOffset float64) (*reverb.FDNReverb, error) {
	r, err := reverb.NewFDNReverb(float64(sampleRate))
	if err != nil {
		return nil, err
	}

	// The dry gain never moves. The mix control rides the wet gain alone, so
	// that a closed control is an exact bypass and an opening one adds a tail
	// rather than trading the strike away for it -- a crossfade would make a
	// glockenspiel go dull on the way to going wet.
	if err := r.SetDry(1); err != nil {
		return nil, err
	}

	if err := r.SetRT60(p.RT60); err != nil {
		return nil, err
	}

	if err := r.SetDamp(p.Damp); err != nil {
		return nil, err
	}

	if err := r.SetPreDelay(p.PreDelay + preDelayOffset); err != nil {
		return nil, err
	}

	if err := r.SetModRate(p.ModRate + modRateOffset); err != nil {
		return nil, err
	}

	return r, nil
}

// setMix updates the wet control, clamped to 0..1.
//
// It allocates nothing and takes no lock: FDNReverb.SetWet is a field store,
// and only SetSampleRate resizes the delay lines, which happens once in the
// constructor. So this is safe to call between blocks on the thread that
// renders them, which is the only thread that calls it.
func (r *stereoReverb) setMix(mix float64) {
	if mix < 0 {
		mix = 0
	}

	if mix > 1 {
		mix = 1
	}

	// Closing the control arms the tail budget. While it is open the budget is
	// not consulted at all, so there is nothing to arm in the other direction.
	if mix == 0 && r.mix > 0 {
		r.tailFrames = r.tailLength
	}

	r.mix = mix

	wet := mix * maxWetGain

	// The errors are dropped on purpose: SetWet rejects only negative and
	// non-finite values, and wet is a clamped product of two constants and a
	// clamped input, so neither is reachable.
	_ = r.left.SetWet(wet)
	_ = r.right.SetWet(wet)
}

// reset clears the tail without changing the mix.
func (r *stereoReverb) reset() {
	r.left.Reset()
	r.right.Reset()
	r.tailFrames = 0
}

// process applies the reverb to one block of interleaved stereo frames, in
// place.
//
// A closed mix is not immediately a skip. Turning the control to zero while a
// tail is ringing has to let that tail decay rather than cut it off mid-air, so
// the networks keep running for RT60 worth of frames afterwards -- their wet
// gain is already zero, so what is still audible is only what the delay lines
// held when the control closed. Once that budget is spent the state is cleared
// and the block is left untouched, because a dry instrument should not go on
// paying for sixteen modulated delay lines it contributes nothing to.
func (r *stereoReverb) process(interleaved []float32) {
	if r.mix == 0 && r.tailFrames <= 0 {
		return
	}

	frames := len(interleaved) / 2
	if frames == 0 {
		return
	}

	r.ensureScratch(frames)

	left := r.scratchL[:frames]
	right := r.scratchR[:frames]

	for i := range frames {
		left[i] = float64(interleaved[i*2])
		right[i] = float64(interleaved[i*2+1])
	}

	r.left.ProcessInPlace(left)
	r.right.ProcessInPlace(right)

	for i := range frames {
		interleaved[i*2] = float32(left[i])
		interleaved[i*2+1] = float32(right[i])
	}

	if r.mix == 0 {
		r.tailFrames -= frames

		if r.tailFrames <= 0 {
			r.left.Reset()
			r.right.Reset()
			r.tailFrames = 0
		}
	}
}

// ensureScratch grows the deinterleave buffers to hold frames, and never
// shrinks them.
func (r *stereoReverb) ensureScratch(frames int) {
	if len(r.scratchL) >= frames {
		return
	}

	r.scratchL = make([]float64, frames)
	r.scratchR = make([]float64, frames)
}
