package oscbank

import (
	"math"
	"testing"
)

// nonFusible passes a float32 through its own bit pattern. An integer
// round-trip is a rounding point no compiler will ever contract across, which
// makes it the one way a Go test can spell "round here, and mean it" without
// depending on the same conversions the code under test relies on.
func nonFusible(v float32) float32 {
	return math.Float32frombits(math.Float32bits(v))
}

// TestPortableKernelDoesNotFuse pins the property the whole numeric contract
// rests on: the portable kernel is one program at every GOAMD64 level and on
// every architecture.
//
// It was not, and the failure was invisible for exactly as long as nobody built
// with GOAMD64=v3. The Go specification permits contracting a multiply followed
// by an add into a single rounded operation, and gc does it whenever the target
// has FMA -- arm64 always, amd64 from v3 upwards, where FMA stops being an
// optional extension. The reference then rounds five times per rotor step
// instead of seven, and the SSE2 kernel, which is bit-identical to the unfused
// reference by construction, drifted from it by one to two ULPs. Every other
// backend's tolerance silently moved too, since they are all measured against
// this function.
//
// advanceRotor now binds each product to its own float32 first. This test is what
// stops that from being quietly deleted as noise: it asserts the arithmetic a
// reader would get by rounding at every step, and the self-check below proves
// the assertion is not vacuous by showing the fused evaluation really does
// produce something else for these inputs.
//
// Verified out of band with `go build -gcflags=-S`, which emits zero VFMADD at
// v1, v2, v3 and v4 and zero FMADDS/FMSUBS on arm64. Note that `go tool
// objdump` is no help here: it does not know the VFMADD mnemonics and decodes
// them as MOVL, which is part of why the contraction went unnoticed.
func TestPortableKernelDoesNotFuse(t *testing.T) {
	const (
		reVal  float32 = 0.7431448
		imVal  float32 = -0.3120334
		cosVal float32 = 0.9876543
		sinVal float32 = 0.1234567
		ampVal float32 = 0.6180339
		sample float32 = 0.31830987
	)

	ampx := nonFusible(ampVal * sample)
	reSin := nonFusible(reVal * sinVal)
	imCos := nonFusible(imVal * cosVal)
	reCos := nonFusible(reVal * cosVal)
	imSin := nonFusible(imVal * sinVal)

	wantIm := nonFusible(nonFusible(ampx+reSin) + imCos)
	wantRe := nonFusible(reCos - imSin)
	wantOut := nonFusible(wantIm - ampx)

	// The self-check. Fusing the accumulator seed evaluates it as
	// FMA(im, cos, FMA(re, sin, ampx)), rounding twice instead of four times.
	// If that happened to land on the same float32 as the unfused form, the
	// assertions below would pass on a fused build and prove nothing.
	fusedSeed := float32(float64(imVal)*float64(cosVal) +
		float64(float32(float64(reVal)*float64(sinVal)+float64(ampx))))
	if fusedSeed == wantIm {
		t.Fatalf("test inputs are degenerate: fused and unfused agree at %g", wantIm)
	}

	state := newRotorState(2)
	state.re[0] = reVal
	state.im[0] = imVal
	state.cosCoeff[0] = cosVal
	state.sinCoeff[0] = sinVal
	state.amp[0] = ampVal

	acc := make([]float32, accLanes)
	processRotorBlocksGeneric(state.re, state.im, state.cosCoeff, state.sinCoeff, state.amp, state.blocks,
		[]float32{sample}, acc)

	// Only lane 0 is live, and the fold adds three exact zeros to its term.
	checkBits(t, "im", state.im[0], wantIm)
	checkBits(t, "re", state.re[0], wantRe)
	checkBits(t, "output", acc[0], wantOut)
}

func checkBits(t *testing.T, what string, got, want float32) {
	t.Helper()

	if math.Float32bits(got) != math.Float32bits(want) {
		t.Fatalf("%s: got %#08x (%g), want %#08x (%g) -- the portable kernel is fusing again",
			what, math.Float32bits(got), got, math.Float32bits(want), want)
	}
}
