package oscbank

import (
	"fmt"
	"math"
	"testing"
)

// smallestNormalF32 is the smallest positive normal float32; anything below it
// and above zero is subnormal.
const smallestNormalF32 = 1.1754944e-38

// denormalTailBank returns a bank whose single oscillator decays fast enough
// that a few hundred thousand silent samples take it all the way through the
// denormal range, plus the excitation chunk that starts it ringing.
func denormalTailBank(tb testing.TB) *Bank {
	tb.Helper()

	bank := New(48000)
	if err := bank.SetOscillators([]Oscillator{{Amplitude: 1, Frequency: 440, DecayMs: 10}}); err != nil {
		tb.Fatalf("SetOscillators failed: %v", err)
	}

	strike := make([]float32, blockSamples)
	strike[0] = 1

	out := make([]float32, blockSamples)
	bank.processChunk(strike, out)

	return bank
}

// maxAbsState returns the largest rotor state magnitude in the bank.
func maxAbsState(bank *Bank) float32 {
	peak := float32(0)

	for i := 0; i < bank.numRotors; i++ {
		for _, v := range [2]float32{bank.re[i], bank.im[i]} {
			if abs := float32(math.Abs(float64(v))); abs > peak {
				peak = abs
			}
		}
	}

	return peak
}

// TestFlushDenormalsDrivesDecayedRotorsToZero pins the win deterministically
// rather than by timing: with flush-to-zero the rotor state goes straight from
// normal to exactly zero and never spends a single block in the denormal range,
// while the same bank under IEEE rules grinds through subnormal magnitudes for
// hundreds of blocks before it finally reaches zero.
func TestFlushDenormalsDrivesDecayedRotorsToZero(t *testing.T) {
	if !FlushDenormalsSupported() {
		t.Skip("no floating-point control register on this architecture")
	}

	const maxChunks = 4000

	flushed := denormalTailBank(t)
	ieee := denormalTailBank(t)

	silence := make([]float32, blockSamples)
	out := make([]float32, blockSamples)

	flushedZeroAt, ieeeZeroAt := -1, -1
	flushedSubnormal, ieeeSubnormal := 0, 0

	for chunk := 1; chunk <= maxChunks; chunk++ {
		func() {
			defer FlushDenormals().Restore()

			flushed.processChunk(silence, out)
		}()

		ieee.processChunk(silence, out)

		if state := maxAbsState(flushed); state == 0 {
			if flushedZeroAt < 0 {
				flushedZeroAt = chunk
			}
		} else if state < smallestNormalF32 {
			flushedSubnormal++
		}

		if state := maxAbsState(ieee); state == 0 {
			if ieeeZeroAt < 0 {
				ieeeZeroAt = chunk
			}
		} else if state < smallestNormalF32 {
			ieeeSubnormal++
		}

		if flushedZeroAt > 0 && ieeeZeroAt > 0 {
			break
		}
	}

	if flushedZeroAt < 0 {
		t.Fatalf("flushed bank never reached zero within %d chunks, peak state %g", maxChunks, maxAbsState(flushed))
	}

	if flushedSubnormal != 0 {
		t.Fatalf("flushed bank held subnormal state for %d chunks, expected none", flushedSubnormal)
	}

	if ieeeSubnormal == 0 {
		t.Fatal("the IEEE bank never entered the denormal range, so this test proves nothing")
	}

	if ieeeZeroAt >= 0 && ieeeZeroAt <= flushedZeroAt {
		t.Fatalf("expected flush-to-zero to reach zero first: flushed=%d ieee=%d", flushedZeroAt, ieeeZeroAt)
	}

	ieeeReport := "still nonzero"
	if ieeeZeroAt > 0 {
		ieeeReport = fmt.Sprintf("zero after %d", ieeeZeroAt)
	}

	t.Logf("zero after %d chunks with flush; without it: %s, %d of %d chunks spent denormal",
		flushedZeroAt, ieeeReport, ieeeSubnormal, maxChunks)
}

// TestFlushDenormalsRestoresCallerMode guards the promise the plugin host cares
// about: whatever mode the caller ran in is the mode it gets back.
func TestFlushDenormalsRestoresCallerMode(t *testing.T) {
	if !FlushDenormalsSupported() {
		t.Skip("no floating-point control register on this architecture")
	}

	before := getFPMode()

	scope := FlushDenormals()

	if inside := getFPMode(); inside&fpFlushMask != fpFlushMask {
		t.Fatalf("expected the flush bits to be set inside the scope, mode %#x", inside)
	}

	scope.Restore()

	if after := getFPMode(); after != before {
		t.Fatalf("mode not restored: before %#x after %#x", before, after)
	}
}

// TestFlushDenormalsNestsWithoutClobbering covers the realtime path, where a
// per-callback scope wraps the per-block scopes inside Bank.ProcessBlock.
func TestFlushDenormalsNestsWithoutClobbering(t *testing.T) {
	if !FlushDenormalsSupported() {
		t.Skip("no floating-point control register on this architecture")
	}

	before := getFPMode()

	outer := FlushDenormals()
	inner := FlushDenormals()

	if inner.applied {
		t.Fatal("expected the inner scope to find the flush bits already set and do nothing")
	}

	inner.Restore()

	if inside := getFPMode(); inside&fpFlushMask != fpFlushMask {
		t.Fatalf("inner Restore cleared the outer scope's bits, mode %#x", inside)
	}

	outer.Restore()

	if after := getFPMode(); after != before {
		t.Fatalf("mode not restored: before %#x after %#x", before, after)
	}
}

// BenchmarkDenormalTail renders silence through a bank whose state has already
// decayed into the denormal range. It is the timing half of the argument that
// TestFlushDenormalsDrivesDecayedRotorsToZero makes deterministically.
func BenchmarkDenormalTail(b *testing.B) {
	bank := denormalTailBank(b)

	silence := make([]float32, blockSamples)
	out := make([]float32, blockSamples)

	// Run the bank down until its state is subnormal but not yet zero, then
	// keep that snapshot as the starting point for every iteration.
	for maxAbsState(bank) >= smallestNormalF32 {
		bank.processChunk(silence, out)

		if maxAbsState(bank) == 0 {
			b.Fatal("bank reached zero before entering the subnormal range")
		}
	}

	re := append([]float32(nil), bank.re...)
	im := append([]float32(nil), bank.im...)

	// Every iteration reloads the subnormal snapshot, because 256 samples of
	// decay would otherwise walk the state out of the denormal range within a
	// few dozen iterations. The reload is a sixteen-float copy and it happens
	// on the clock in both variants, so it cancels out of the comparison
	// instead of being hidden behind StopTimer, which costs far more than the
	// copy does.
	run := func(b *testing.B, flush bool) {
		b.Helper()

		for range b.N {
			copy(bank.re, re)
			copy(bank.im, im)

			// The zero scope restores nothing, so the IEEE variant runs the
			// same code with the mode change left out.
			var scope DenormalScope
			if flush {
				scope = FlushDenormals()
			}

			bank.processChunk(silence, out)
			scope.Restore()
		}
	}

	b.Run("IEEE", func(b *testing.B) { run(b, false) })
	b.Run("Flushed", func(b *testing.B) { run(b, true) })
}
