package oscbank

import (
	"fmt"
	"math"
	"math/rand"
	"testing"
)

// referenceBank is an independent float64 implementation of the same recursion,
// written the obvious way. It exists to catch layout and coefficient mistakes
// that both the portable and the packed kernel could share.
type referenceRotor struct {
	re, im         float64
	cosVal, sinVal float64
	amplitude      float64
}

func newReferenceRotors(oscillators []Oscillator, sampleRate float64) []referenceRotor {
	numHarm := 0

	for _, osc := range oscillators {
		harmonics := max(len(osc.Harmonics), 1)
		numHarm = max(numHarm, harmonics)
	}

	rotors := make([]referenceRotor, 0, len(oscillators)*numHarm)

	for _, osc := range oscillators {
		for harmonic := 0; harmonic < numHarm; harmonic++ {
			gain := 0.0

			switch {
			case len(osc.Harmonics) == 0 && harmonic == 0:
				gain = 1
			case harmonic < len(osc.Harmonics):
				gain = osc.Harmonics[harmonic]
			default:
				rotors = append(rotors, referenceRotor{})
				continue
			}

			if osc.DecayMs <= minDecayMs {
				rotors = append(rotors, referenceRotor{})
				continue
			}

			decay := math.Exp(-math.Ln2 / (0.001 * osc.DecayMs * sampleRate))
			phase := 2 * math.Pi * float64(harmonic+1) * osc.Frequency / sampleRate
			sinVal, cosVal := math.Sincos(phase)

			rotors = append(rotors, referenceRotor{
				cosVal:    decay * cosVal,
				sinVal:    decay * sinVal,
				amplitude: osc.Amplitude * gain,
			})
		}
	}

	return rotors
}

func referenceProcess(rotors []referenceRotor, input []float32) []float32 {
	out := make([]float32, len(input))

	for i, x := range input {
		sum := 0.0

		for r := range rotors {
			rotor := &rotors[r]

			t := rotor.im*rotor.cosVal + rotor.re*rotor.sinVal

			rotor.re = rotor.re*rotor.cosVal - rotor.im*rotor.sinVal
			rotor.im = rotor.amplitude*float64(x) + t

			sum += t
		}

		out[i] = float32(sum)
	}

	return out
}

func testOscillators(numOsc, numHarm int) []Oscillator {
	oscillators := make([]Oscillator, numOsc)

	for i := range oscillators {
		osc := Oscillator{
			Amplitude: 1 - 0.13*float64(i%7),
			Frequency: 220 * math.Pow(2, float64(i%12)/12) * float64(1+i/12),
			DecayMs:   40 + 17*float64(i%9),
		}

		if numHarm > 1 {
			osc.Harmonics = make([]float64, numHarm)
			for k := range osc.Harmonics {
				osc.Harmonics[k] = 1 / float64(k+1)
			}
		}

		oscillators[i] = osc
	}

	return oscillators
}

func strikeInput(n int) []float32 {
	input := make([]float32, n)
	if n > 0 {
		input[0] = 1
	}

	return input
}

func TestBankMatchesScalarReference(t *testing.T) {
	const sampleRate = 48000

	cases := []struct{ numOsc, numHarm int }{
		{1, 1}, {4, 1}, {4, 4}, {7, 3}, {16, 4}, {64, 1},
	}

	for _, tc := range cases {
		t.Run(fmt.Sprintf("%dx%d", tc.numOsc, tc.numHarm), func(t *testing.T) {
			oscillators := testOscillators(tc.numOsc, tc.numHarm)

			bank := New(sampleRate)
			if err := bank.SetOscillators(oscillators); err != nil {
				t.Fatalf("SetOscillators: %v", err)
			}

			input := strikeInput(1024)
			got := make([]float32, len(input))
			bank.ProcessBlock(input, got)

			want := referenceProcess(newReferenceRotors(oscillators, sampleRate), input)

			// float32 rotor state against a float64 reference: the tolerance
			// tracks the peak, not each sample, because the tail decays far
			// below the accumulated rounding of the head.
			peak := 0.0
			for _, v := range want {
				peak = math.Max(peak, math.Abs(float64(v)))
			}

			tolerance := 1e-4 * peak
			for i := range got {
				if math.Abs(float64(got[i]-want[i])) > tolerance {
					t.Fatalf("sample %d: got %g want %g (tolerance %g)", i, got[i], want[i], tolerance)
				}
			}
		})
	}
}

func TestBankHarmonicsAreIntegerMultipleRotors(t *testing.T) {
	const sampleRate = 44100

	withHarmonics := New(sampleRate)
	if err := withHarmonics.SetOscillators([]Oscillator{{
		Amplitude: 0.75,
		Frequency: 500,
		DecayMs:   60,
		Harmonics: []float64{0, 0, 0.5},
	}}); err != nil {
		t.Fatalf("SetOscillators: %v", err)
	}

	// The third partial alone must equal a plain oscillator at 3x the frequency
	// with the harmonic gain folded into the amplitude.
	equivalent := New(sampleRate)
	if err := equivalent.SetOscillators([]Oscillator{{
		Amplitude: 0.75 * 0.5,
		Frequency: 1500,
		DecayMs:   60,
	}}); err != nil {
		t.Fatalf("SetOscillators: %v", err)
	}

	input := strikeInput(512)
	got := make([]float32, len(input))
	want := make([]float32, len(input))

	withHarmonics.ProcessBlock(input, got)
	equivalent.ProcessBlock(input, want)

	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("sample %d: got %g want %g", i, got[i], want[i])
		}
	}

	if withHarmonics.NumHarmonics() != 3 {
		t.Fatalf("NumHarmonics = %d, want 3", withHarmonics.NumHarmonics())
	}

	if withHarmonics.NumRotors() != 3 {
		t.Fatalf("NumRotors = %d, want 3", withHarmonics.NumRotors())
	}
}

func TestBankCountsAreRuntimeConfigurable(t *testing.T) {
	bank := New(48000)

	cases := []struct {
		numOsc, numHarm int
	}{
		{4, 4}, {1, 1}, {64, 3}, {9, 1}, {0, 0}, {3, 11},
	}

	for _, tc := range cases {
		if err := bank.SetOscillators(testOscillators(tc.numOsc, tc.numHarm)); err != nil {
			t.Fatalf("SetOscillators(%d,%d): %v", tc.numOsc, tc.numHarm, err)
		}

		if bank.NumOscillators() != tc.numOsc {
			t.Fatalf("NumOscillators = %d, want %d", bank.NumOscillators(), tc.numOsc)
		}

		wantHarm := tc.numHarm
		if tc.numOsc == 0 {
			wantHarm = 0
		}

		if bank.NumHarmonics() != wantHarm {
			t.Fatalf("NumHarmonics = %d, want %d", bank.NumHarmonics(), wantHarm)
		}

		if bank.NumRotors() != tc.numOsc*wantHarm {
			t.Fatalf("NumRotors = %d, want %d", bank.NumRotors(), tc.numOsc*wantHarm)
		}

		input := strikeInput(300)
		out := make([]float32, len(input))
		bank.ProcessBlock(input, out)
	}
}

func TestBankChunkingIsContinuous(t *testing.T) {
	const sampleRate = 44100

	oscillators := testOscillators(5, 3)
	input := make([]float32, 1000)

	rng := rand.New(rand.NewSource(7))
	for i := range input {
		input[i] = float32(rng.NormFloat64() * 0.1)
	}

	whole := New(sampleRate)
	if err := whole.SetOscillators(oscillators); err != nil {
		t.Fatalf("SetOscillators: %v", err)
	}

	want := make([]float32, len(input))
	whole.ProcessBlock(input, want)

	for _, chunk := range []int{1, 3, 8, 64, 255, 256, 257, 999} {
		streamed := New(sampleRate)
		if err := streamed.SetOscillators(oscillators); err != nil {
			t.Fatalf("SetOscillators: %v", err)
		}

		got := make([]float32, len(input))
		for start := 0; start < len(input); start += chunk {
			end := min(start+chunk, len(input))
			streamed.ProcessBlock(input[start:end], got[start:end])
		}

		for i := range got {
			if got[i] != want[i] {
				t.Fatalf("chunk %d, sample %d: got %g want %g", chunk, i, got[i], want[i])
			}
		}
	}
}

func TestBankEmptyAndMutedAreSilent(t *testing.T) {
	input := strikeInput(64)
	out := make([]float32, len(input))

	for i := range out {
		out[i] = 42
	}

	empty := New(48000)
	if err := empty.SetOscillators(nil); err != nil {
		t.Fatalf("SetOscillators: %v", err)
	}

	empty.ProcessBlock(input, out)

	for i, v := range out {
		if v != 0 {
			t.Fatalf("empty bank sample %d = %g, want 0", i, v)
		}
	}

	muted := New(48000)
	if err := muted.SetOscillators([]Oscillator{{Amplitude: 1, Frequency: 1000, DecayMs: 0}}); err != nil {
		t.Fatalf("SetOscillators: %v", err)
	}

	muted.ProcessBlock(input, out)

	for i, v := range out {
		if v != 0 {
			t.Fatalf("muted bank sample %d = %g, want 0", i, v)
		}
	}
}

func TestBankResetClearsState(t *testing.T) {
	bank := New(48000)
	if err := bank.SetOscillators(testOscillators(4, 4)); err != nil {
		t.Fatalf("SetOscillators: %v", err)
	}

	input := strikeInput(256)
	first := make([]float32, len(input))
	second := make([]float32, len(input))

	bank.ProcessBlock(input, first)
	bank.Reset()
	bank.ProcessBlock(input, second)

	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("sample %d after reset: got %g want %g", i, second[i], first[i])
		}
	}
}

func TestBankRejectsNonFiniteParameters(t *testing.T) {
	bank := New(48000)

	cases := []Oscillator{
		{Amplitude: math.NaN(), Frequency: 1000, DecayMs: 10},
		{Amplitude: 1, Frequency: math.Inf(1), DecayMs: 10},
		{Amplitude: 1, Frequency: 1000, DecayMs: math.NaN()},
		{Amplitude: 1, Frequency: 1000, DecayMs: -1},
		{Amplitude: 1, Frequency: 1000, DecayMs: 10, Harmonics: []float64{math.NaN()}},
	}

	for i, osc := range cases {
		if err := bank.SetOscillators([]Oscillator{osc}); err == nil {
			t.Fatalf("case %d: expected an error", i)
		}
	}
}

func TestBankMaxDecayFactor(t *testing.T) {
	const sampleRate = 48000

	bank := New(sampleRate)
	if err := bank.SetOscillators([]Oscillator{
		{Amplitude: 1, Frequency: 1000, DecayMs: 10},
		{Amplitude: 1, Frequency: 2000, DecayMs: 200},
	}); err != nil {
		t.Fatalf("SetOscillators: %v", err)
	}

	want := math.Exp(-math.Ln2 / (0.001 * 200 * sampleRate))
	if got := bank.MaxDecayFactor(); math.Abs(got-want) > 1e-12 {
		t.Fatalf("MaxDecayFactor = %g, want %g", got, want)
	}
}

func benchmarkBank(b *testing.B, numOsc, numHarm int) {
	b.Helper()

	bank := New(48000)
	if err := bank.SetOscillators(testOscillators(numOsc, numHarm)); err != nil {
		b.Fatalf("SetOscillators: %v", err)
	}

	input := strikeInput(512)
	output := make([]float32, len(input))

	b.ReportAllocs()
	b.ResetTimer()

	for range b.N {
		bank.ProcessBlock(input, output)
	}

	rotors := bank.NumRotors()
	if rotors > 0 && b.Elapsed() > 0 {
		nsPerOp := float64(b.Elapsed().Nanoseconds()) / float64(b.N)
		b.ReportMetric(nsPerOp/float64(rotors), "ns/rotor-block")
	}
}

func BenchmarkBank4x1(b *testing.B)  { benchmarkBank(b, 4, 1) }
func BenchmarkBank4x4(b *testing.B)  { benchmarkBank(b, 4, 4) }
func BenchmarkBank8x4(b *testing.B)  { benchmarkBank(b, 8, 4) }
func BenchmarkBank16x4(b *testing.B) { benchmarkBank(b, 16, 4) }
func BenchmarkBank64x1(b *testing.B) { benchmarkBank(b, 64, 1) }
func BenchmarkBank64x4(b *testing.B) { benchmarkBank(b, 64, 4) }

// TestBankPacksRotorsIntoLanes is the structural side of the scaling claim:
// cost follows the rotor count divided by the lane width, not the oscillator
// count. Sixty-four oscillators are eight packed blocks, not sixty-four serial
// units, and four oscillators cost the same as sixteen because they share one
// block pair.
func TestBankPacksRotorsIntoLanes(t *testing.T) {
	cases := []struct {
		numOsc, numHarm int
		wantBlocks      int
	}{
		{1, 1, 2},
		{4, 1, 2},
		{8, 1, 2},
		{16, 1, 2},
		{17, 1, 4},
		{64, 1, 8},
		{4, 4, 2},
		{64, 4, 32},
	}

	bank := New(48000)

	for _, tc := range cases {
		if err := bank.SetOscillators(testOscillators(tc.numOsc, tc.numHarm)); err != nil {
			t.Fatalf("SetOscillators(%d,%d): %v", tc.numOsc, tc.numHarm, err)
		}

		if bank.numBlocks != tc.wantBlocks {
			t.Fatalf("%dx%d: %d blocks, want %d", tc.numOsc, tc.numHarm, bank.numBlocks, tc.wantBlocks)
		}
	}
}
