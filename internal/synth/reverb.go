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
	// the range rather than how much dry is traded away for it.
	maxWetGain = 0.75

	// wetRampSeconds is how long the wet gain takes to cross its whole range
	// when the control jumps. The control is a dial someone drags, so it
	// arrives as a run of small steps rather than one large one, but a step
	// applied instantly is still a discontinuity in the output, and the loudest
	// case -- releasing the dial at one end and clicking the other -- is a jump
	// of the entire range. Ramping it per sample makes every one of those a
	// glide instead of a click.
	wetRampSeconds = 0.03

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
// calibrated for 44.1 kHz and it has no SetSampleRate, so at the 48 kHz an
// AudioContext often runs at it would render a room roughly 9% smaller and
// brighter than the one it was tuned to be. FDNReverb takes the rate in its
// constructor and rescales its reference delays to it.
//
// The networks are run wet-only -- dry 0, wet 1 -- and the blend is done here
// rather than inside them. Their own mix law would apply one gain to a whole
// block, which is exactly the discontinuity wetRampSeconds exists to avoid;
// owning the blend is what lets the gain move per sample.
type stereoReverb struct {
	left  *reverb.FDNReverb
	right *reverb.FDNReverb

	// target is where the control has been put, in wet gain, and wet is where
	// the ramp has actually reached. They are equal except while a change is
	// gliding in.
	target float64
	wet    float64
	// step is how far wet may move per sample.
	step float64

	// scratchL and scratchR carry the block through the two mono networks.
	// Grow-only, like the engine's mixBuffer, so a callback wider than any seen
	// before is the only thing that ever allocates here.
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

	return &stereoReverb{
		left:  left,
		right: right,
		step:  maxWetGain / (wetRampSeconds * float64(sampleRate)),
	}, nil
}

// newReverbChannel builds one network, offset from the nominal room by the two
// deltas that decorrelate the right channel from the left.
func newReverbChannel(sampleRate int, p ReverbParams, preDelayOffset, modRateOffset float64) (*reverb.FDNReverb, error) {
	r, err := reverb.NewFDNReverb(float64(sampleRate))
	if err != nil {
		return nil, err
	}

	// Wet-only: the caller blends. See the type comment.
	if err := r.SetDry(0); err != nil {
		return nil, err
	}

	if err := r.SetWet(1); err != nil {
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
// It moves the ramp's target, not the gain, so a control that is dragged from
// one end to the other is heard as a sweep rather than as a series of steps. It
// allocates nothing and takes no lock -- it stores one float -- so it is safe
// to call between blocks on the thread that renders them, which is the only
// thread that calls it.
func (r *stereoReverb) setMix(mix float64) {
	if mix < 0 {
		mix = 0
	}

	if mix > 1 {
		mix = 1
	}

	r.target = mix * maxWetGain
}

// mix reports the control position the reverb is heading for, 0..1.
func (r *stereoReverb) mix() float64 {
	return r.target / maxWetGain
}

// reset clears the tail and lands the ramp on its target, leaving the reverb in
// the state it would have been in had the current setting always been in force
// and nothing had ever been played through it.
func (r *stereoReverb) reset() {
	r.left.Reset()
	r.right.Reset()
	r.wet = r.target
}

// process adds the reverb to one block of interleaved stereo frames, in place.
//
// A control resting at zero is an exact bypass: the block is returned untouched
// and the networks are not run at all, because a dry instrument should not pay
// for sixteen modulated delay lines that contribute nothing. The state is
// cleared on the way into that condition rather than left standing, so the
// reverb does not resume a room from minutes ago when the control comes back
// up. That does mean closing the control ends the tail rather than letting it
// ring out, which is what a wet-mix control means: the ramp is there so it ends
// without a click, not so it survives.
func (r *stereoReverb) process(interleaved []float32) {
	if r.target == 0 && r.wet == 0 {
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

	wet := r.wet

	for i := range frames {
		wet = approach(wet, r.target, r.step)

		interleaved[i*2] += float32(wet * left[i])
		interleaved[i*2+1] += float32(wet * right[i])
	}

	r.wet = wet

	// Reaching a closed control is where the networks are parked. Doing it here
	// rather than in setMix is what keeps the ramp audible: the block that
	// finishes the glide down is still a block the tail was mixed into.
	if r.target == 0 && r.wet == 0 {
		r.left.Reset()
		r.right.Reset()
	}
}

// approach moves value one step towards target without overshooting it.
func approach(value, target, step float64) float64 {
	if value < target {
		value += step
		if value > target {
			return target
		}

		return value
	}

	if value > target {
		value -= step
		if value < target {
			return target
		}

		return value
	}

	return target
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
