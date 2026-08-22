package oscbank

import (
	"math"
	"math/rand"
	"testing"

	"github.com/cwbudde/glockenspiel/internal/cpufeat"
)

// This file implements the numeric contract from docs/oscillator-bank.md so the
// differential tests and the fuzz harness measure the same thing. The short
// version: packed backends that fuse must agree to the bit, and everything else
// is held to u * E * (6*g(N,d) + folds), where E is a no-cancellation envelope
// and g is the quadrature gain of a contraction of rate d.

const (
	// unitRoundoff is 2^-24, half an ULP at the top of a float32 binade.
	unitRoundoff = 1.0 / float64(int64(1)<<24)

	// contractRoundings is the number of roundings by which a fused and an
	// unfused evaluation of one rotor step can differ: four against two for the
	// accumulator seed, three against two for the real part. Counting every one
	// of them as adversarial is what leaves the bound its headroom.
	contractRoundings = 6

	// fixedFoldRoundings counts the adds on the output path that every backend
	// performs identically: three to fold a block pair's four lane values, and
	// three more in reduceLanes. They do not make the backends differ -- rule
	// two pins their order -- but each one re-rounds operands that already
	// differ, and can round them apart by one more ULP.
	fixedFoldRoundings = 6
)

// hostFeatures is read once, at package init, before any test forces a feature
// set. Reading it later would report whatever the test in flight had forced.
var hostFeatures = cpufeat.Detect()

// backend names one dispatch path the harness can select on this machine.
type backend struct {
	name     string
	features cpufeat.Features

	// packedFused marks a backend that both runs a hand-written packed kernel
	// and has FMA. Those are the reference: they must agree with each other to
	// the bit, and everything else is measured against them.
	packedFused bool

	// bitExactWithPortable marks a backend that is packed but cannot fuse, and
	// that therefore chose to associate its arithmetic exactly as
	// kernel_generic.go does. The contract only holds such a backend to the
	// bound, but a backend that makes the same rounding choices as the portable
	// kernel on the same machine has to reproduce it to the bit, and saying so
	// turns the reference into an exact oracle instead of an approximate one.
	bitExactWithPortable bool
}

// availableBackends lists the dispatch paths reachable on the running machine.
// Phase 2.3 adds entries here; nothing else in the harness has to change.
func availableBackends() []backend {
	backends := []backend{{name: "portable"}}

	if hostFeatures.HasAVX2 && hostFeatures.HasFMA {
		backends = append(backends, backend{
			name: "avx2",
			features: cpufeat.Features{
				HasSSE2: true, HasSSE3: true, HasAVX: true, HasAVX2: true, HasFMA: true,
			},
			packedFused: true,
		})
	}

	// SSE2 is baseline on amd64, so this entry exists on every amd64 runner and
	// on no other architecture. Forcing AVX2 and FMA off is the only way to
	// reach the kernel, since the dispatcher prefers AVX2 whenever it can.
	if hostFeatures.HasSSE2 {
		backends = append(backends, backend{
			name:                 "sse2",
			features:             cpufeat.Features{HasSSE2: true, HasSSE3: true},
			bitExactWithPortable: true,
		})
	}

	if hostFeatures.HasASIMD {
		backends = append(backends, backend{
			name:        "neon",
			features:    cpufeat.Features{HasASIMD: true},
			packedFused: true,
		})
	}

	return backends
}

// rotorState is one bank's worth of AoSoA arrays, independent of Bank so the
// harness can hand a kernel coefficients no configuration would ever produce.
type rotorState struct {
	re, im             []float32
	cosCoeff, sinCoeff []float32
	amp                []float32
	blocks             int
}

func newRotorState(blocks int) rotorState {
	size := blocks * LaneWidth

	return rotorState{
		re:       make([]float32, size),
		im:       make([]float32, size),
		cosCoeff: make([]float32, size),
		sinCoeff: make([]float32, size),
		amp:      make([]float32, size),
		blocks:   blocks,
	}
}

func (s rotorState) clone() rotorState {
	out := newRotorState(s.blocks)

	copy(out.re, s.re)
	copy(out.im, s.im)
	copy(out.cosCoeff, s.cosCoeff)
	copy(out.sinCoeff, s.sinCoeff)
	copy(out.amp, s.amp)

	return out
}

// maxDecayFactor is the largest per-sample contraction any rotor applies. The
// coefficient pair is a rotation scaled by the decay factor, so its magnitude is
// the decay factor.
func (s rotorState) maxDecayFactor() float64 {
	worst := 0.0

	for lane := range s.cosCoeff {
		worst = math.Max(worst, math.Hypot(float64(s.cosCoeff[lane]), float64(s.sinCoeff[lane])))
	}

	return worst
}

// render runs one chunk through the dispatcher with CPU detection forced, so a
// single machine can exercise every backend. The input is copied into a buffer
// one element longer than the chunk: the packed kernels compute amp*x for the
// next sample one iteration ahead and read input[samples] to do it.
func (s rotorState) render(features cpufeat.Features, input []float32) []float32 {
	cpufeat.SetForcedFeatures(features)

	defer cpufeat.ResetDetection()

	padded := make([]float32, len(input)+1)
	copy(padded, input)

	acc := make([]float32, len(input)*accLanes)
	out := make([]float32, len(input))

	processRotorBlocks(s.re, s.im, s.cosCoeff, s.sinCoeff, s.amp, s.blocks, padded[:len(input)], acc)
	reduceLanes(acc, out)

	return out
}

// errorEnvelope is the scale the per-step rounding error is injected against.
// Per rotor that scale is |amp*x| + d*rho, where rho is the rotor's state
// magnitude; rho obeys rho' = d*rho + |amp*x| exactly because the rotation is
// orthogonal, so the envelope is contractive for the same reason the error is.
//
// Rotors are combined in quadrature, not summed. Their rounding errors are
// independent, and the same argument that lets error compose as sqrt over
// samples applies over lanes: an ell-1 sum would be the adversarial bound, and
// on a 256-rotor bank it is sixteen times too generous to catch anything.
//
// The envelope is deliberately not the peak of the rendered output. A bank
// whose rotors cancel renders quietly and rounds just as badly, and normalizing
// to the realized peak would turn that into a test failure.
func errorEnvelope(state rotorState, input []float32) float64 {
	lanes := len(state.re)
	rho := make([]float64, lanes)
	decay := make([]float64, lanes)

	for lane := range rho {
		rho[lane] = math.Hypot(float64(state.re[lane]), float64(state.im[lane]))
		decay[lane] = math.Hypot(float64(state.cosCoeff[lane]), float64(state.sinCoeff[lane]))
	}

	peak := 0.0

	for _, sample := range input {
		excitation := math.Abs(float64(sample))
		squares := 0.0

		for lane := range rho {
			driven := math.Abs(float64(state.amp[lane])) * excitation
			scale := driven + decay[lane]*rho[lane]
			squares += scale * scale
			rho[lane] = decay[lane]*rho[lane] + driven
		}

		peak = math.Max(peak, math.Sqrt(squares))
	}

	return peak
}

// contractGain is g(N, d) = sqrt((1 - d^2N) / (1 - d^2)), the factor by which N
// steps of uncorrelated per-step error accumulate through a contraction of rate
// d. It saturates at 1/sqrt(1 - d^2) and degenerates to sqrt(N) at d = 1, which
// is why a sustained rotor has no contract.
func contractGain(samples int, decay float64) float64 {
	count := float64(samples)

	if decay >= 1 {
		return math.Sqrt(count)
	}

	if decay <= 0 {
		return 1
	}

	shrink := 1 - decay*decay
	if shrink <= 0 {
		return math.Sqrt(count)
	}

	return math.Sqrt((1 - math.Pow(decay, 2*count)) / shrink)
}

// contractTolerance is the largest divergence the contract permits between a
// fused backend and an unfused one over a chunk of len(input) samples.
//
// Two terms: the recursion, where the backends do different arithmetic and the
// difference accumulates through the contraction, and the reduction, where they
// do identical arithmetic on operands that already differ. Only the first is
// amplified by g; the second is a flat handful of ULPs, but it dominates on the
// first few samples, where g is still 1.
func contractTolerance(state rotorState, input []float32) float64 {
	// One add per extra block pair, accumulating into acc.
	folds := fixedFoldRoundings + max(state.blocks/2-1, 0)

	recursion := contractRoundings * contractGain(len(input), state.maxDecayFactor())

	return unitRoundoff * errorEnvelope(state, input) * (recursion + float64(folds))
}

// bankTolerance is the same bound expressed for a Bank, whose rotor arrays the
// caller cannot reach from outside the package.
func bankTolerance(bank *Bank, input []float32) float64 {
	state := rotorState{
		re:       bank.re,
		im:       bank.im,
		cosCoeff: bank.cosCoeff,
		sinCoeff: bank.sinCoeff,
		amp:      bank.amp,
		blocks:   bank.numBlocks,
	}

	return contractTolerance(state, input)
}

// referenceTolerance bounds a bank's divergence from a float64 reference that
// derives its own coefficients from the same oscillator parameters.
//
// That is a strictly larger question than the backend contract. Rounding
// cos and sin to float32 perturbs the recursion itself: the decay factor
// becomes d(1 + δ) with |δ| <= u, and after n steps the state is off by a
// factor of (1 + δ)^n. Unlike a rounding error, that bias is systematic -- it
// points the same way on every step -- so it composes in the l1 form
// (1 - d^N)/(1 - d) rather than in quadrature, and it dominates the arithmetic
// term for any rotor with a long tail.
func referenceTolerance(bank *Bank, input []float32) float64 {
	state := rotorState{
		re:       bank.re,
		im:       bank.im,
		cosCoeff: bank.cosCoeff,
		sinCoeff: bank.sinCoeff,
		amp:      bank.amp,
		blocks:   bank.numBlocks,
	}

	decay := state.maxDecayFactor()
	samples := float64(len(input))

	coefficientDrift := samples
	if decay < 1 && decay > 0 {
		coefficientDrift = math.Min(samples, (1-math.Pow(decay, samples))/(1-decay))
	}

	return contractTolerance(state, input) +
		unitRoundoff*errorEnvelope(state, input)*coefficientDrift
}

// seedBankState gives a bank the non-zero rotor state a freshly constructed one
// lacks. Without it half the packed kernel multiplies by zero for the whole
// render and the differential tests only exercise the excitation path.
//
// Padding lanes stay at zero: they are what the bank itself would hold, and a
// kernel that let them drift would be a real bug.
func seedBankState(bank *Bank, rng *rand.Rand) {
	for rotor := range bank.numRotors {
		if bank.amp[rotor] == 0 && bank.cosCoeff[rotor] == 0 && bank.sinCoeff[rotor] == 0 {
			continue
		}

		bank.re[rotor] = float32(rng.NormFloat64())
		bank.im[rotor] = float32(rng.NormFloat64())
	}
}

// seedBankAndReference seeds a bank and the independent float64 reference with
// the same initial state, so the two are still comparing the same trajectory.
func seedBankAndReference(bank *Bank, rotors []referenceRotor, rng *rand.Rand) {
	for rotor := range bank.numRotors {
		if bank.amp[rotor] == 0 && bank.cosCoeff[rotor] == 0 && bank.sinCoeff[rotor] == 0 {
			continue
		}

		re, im := rng.NormFloat64(), rng.NormFloat64()

		bank.re[rotor] = float32(re)
		bank.im[rotor] = float32(im)

		// The reference starts from the float32 values the bank actually holds,
		// not from the float64 draws: seeding it with more precision than the
		// bank can store would make the initial state itself a divergence.
		rotors[rotor].re = float64(bank.re[rotor])
		rotors[rotor].im = float64(bank.im[rotor])
	}
}

// requireWithinContract compares one backend against the portable reference.
func requireWithinContract(t *testing.T, label string, got, want []float32, tolerance float64) {
	t.Helper()

	for i := range want {
		diff := math.Abs(float64(got[i]) - float64(want[i]))
		if diff > tolerance {
			t.Fatalf("%s: sample %d diverges by %g, contract allows %g (got %g, portable %g)",
				label, i, diff, tolerance, got[i], want[i])
		}
	}
}

// requireBitIdentical enforces the first rule of the contract.
func requireBitIdentical(t *testing.T, label string, got, want []float32) {
	t.Helper()

	for i := range want {
		if math.Float32bits(got[i]) != math.Float32bits(want[i]) {
			t.Fatalf("%s: sample %d is not bit-identical: %#08x vs %#08x (%g vs %g)",
				label, i, math.Float32bits(got[i]), math.Float32bits(want[i]), got[i], want[i])
		}
	}
}

// The voice-major path gets its own state holder for the same reason the
// rotor-major one has one: the contract has to hold for coefficients no real
// configuration produces, so the harness builds the arrays itself rather than
// going through VoiceBank.
//
// The layout is [rotor][voice]: rotor r of voice l lives at r*LaneWidth+l.
type voiceRotorState struct {
	re, im             []float32
	cosCoeff, sinCoeff []float32
	amp                []float32
	rotors             int
}

func newVoiceRotorState(rotors int) voiceRotorState {
	size := rotors * LaneWidth

	return voiceRotorState{
		re:       make([]float32, size),
		im:       make([]float32, size),
		cosCoeff: make([]float32, size),
		sinCoeff: make([]float32, size),
		amp:      make([]float32, size),
		rotors:   rotors,
	}
}

func (s voiceRotorState) clone() voiceRotorState {
	out := newVoiceRotorState(s.rotors)

	copy(out.re, s.re)
	copy(out.im, s.im)
	copy(out.cosCoeff, s.cosCoeff)
	copy(out.sinCoeff, s.sinCoeff)
	copy(out.amp, s.amp)

	return out
}

func (s voiceRotorState) maxDecayFactor() float64 {
	worst := 0.0

	for lane := range s.cosCoeff {
		worst = math.Max(worst, math.Hypot(float64(s.cosCoeff[lane]), float64(s.sinCoeff[lane])))
	}

	return worst
}

// render runs one interleaved chunk through the voice-major seam. input is
// [samples][LaneWidth] and so is the result; there is no reduction pass,
// because in this layout the accumulator is the output.
//
// The portable backend is called directly rather than through the dispatcher.
// On amd64 that is the same function either way, but on arm64 the packed kernel
// is ungated, so dispatching with an empty feature set would run NEON and
// quietly compare it against itself.
func (s voiceRotorState) render(current backend, input []float32) []float32 {
	samples := len(input) / LaneWidth

	// One guard frame: the packed kernels compute amp*x a sample ahead, and a
	// sample is a whole frame here.
	padded := make([]float32, len(input)+LaneWidth)
	copy(padded, input)

	acc := make([]float32, samples*LaneWidth)

	if current.name == "portable" {
		processVoiceRotorsGeneric(s.re, s.im, s.cosCoeff, s.sinCoeff, s.amp, s.rotors, padded, acc)

		return acc
	}

	cpufeat.SetForcedFeatures(current.features)

	defer cpufeat.ResetDetection()

	processVoiceRotors(s.re, s.im, s.cosCoeff, s.sinCoeff, s.amp, s.rotors, padded, acc)

	return acc
}

// voiceContractTolerance is the same bound as contractTolerance, restated for a
// layout in which a lane is a voice.
//
// Two things change and neither is a relaxation. The envelope is per voice: lane
// l accumulates only its own rotors, driven only by its own excitation stream,
// so the quadrature sum runs over the rotors of one lane rather than over the
// whole bank, and the harness takes the worst lane. And the fold term counts the
// adds on this path instead of the old ones: one per rotor pair to sum the pair,
// one per later pair to accumulate into acc, and none at all for a horizontal
// reduction, because there is not one.
func voiceContractTolerance(state voiceRotorState, input []float32) float64 {
	samples := len(input) / LaneWidth
	pairs := state.rotors / 2
	folds := max(2*pairs-1, 0)

	recursion := contractRoundings * contractGain(samples, state.maxDecayFactor())

	worst := 0.0

	for voice := range LaneWidth {
		rho := make([]float64, state.rotors)
		decay := make([]float64, state.rotors)

		for rotor := range rho {
			lane := rotor*LaneWidth + voice
			rho[rotor] = math.Hypot(float64(state.re[lane]), float64(state.im[lane]))
			decay[rotor] = math.Hypot(float64(state.cosCoeff[lane]), float64(state.sinCoeff[lane]))
		}

		peak := 0.0

		for i := range samples {
			excitation := math.Abs(float64(input[i*LaneWidth+voice]))
			squares := 0.0

			for rotor := range rho {
				driven := math.Abs(float64(state.amp[rotor*LaneWidth+voice])) * excitation
				scale := driven + decay[rotor]*rho[rotor]
				squares += scale * scale
				rho[rotor] = decay[rotor]*rho[rotor] + driven
			}

			peak = math.Max(peak, math.Sqrt(squares))
		}

		worst = math.Max(worst, unitRoundoff*peak*(recursion+float64(folds)))
	}

	return worst
}

// requireBackendsAgree holds every backend on this machine to the contract for
// one case: fused packed backends agree with each other to the bit, a packed
// backend that chose the reference's association reproduces it to the bit, and
// everything stays inside tolerance of the portable kernel.
//
// It takes the render as a closure so the rotor-major and voice-major paths are
// judged by the same code rather than by two copies of it that could drift.
func requireBackendsAgree(t *testing.T, backends []backend, tolerance float64, render func(backend) []float32) {
	t.Helper()

	var (
		reference []float32
		fused     []float32
		fusedName string
	)

	for _, current := range backends {
		got := render(current)

		for i, sample := range got {
			if math.IsNaN(float64(sample)) || math.IsInf(float64(sample), 0) {
				t.Fatalf("%s: sample %d is %g on a bounded recursion", current.name, i, sample)
			}
		}

		switch {
		case current.name == "portable":
			reference = got
		case current.packedFused && fused == nil:
			fused, fusedName = got, current.name
		case current.packedFused:
			// Rule one: every fused packed backend is the same program.
			requireBitIdentical(t, current.name+" vs "+fusedName, got, fused)
		}

		if reference != nil {
			requireWithinContract(t, current.name, got, reference, tolerance)

			if current.bitExactWithPortable {
				// Stronger than the contract demands, and the reason to demand
				// it is that it is free: a packed kernel with no FMA has to
				// round four times either way, so associating as the reference
				// does costs nothing and buys an exact oracle.
				requireBitIdentical(t, current.name+" vs portable", got, reference)
			}
		}
	}

	if fused != nil && reference != nil {
		requireWithinContract(t, fusedName, fused, reference, tolerance)
	}
}
