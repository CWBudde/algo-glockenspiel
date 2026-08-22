package wavio_test

import (
	"bytes"
	"errors"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/cwbudde/glockenspiel/internal/wavio"
)

// quantisationStep is one 16-bit LSB. Every round-trip assertion below is
// stated against it rather than against an arbitrary epsilon, because the
// error the encoder introduces is exactly a rounding to this grid.
const quantisationStep = 1.0 / 32767.0

// ramp builds a signal that touches both rails and everything between, so a
// sign error or an off-by-one in the scaling shows up somewhere in it.
func ramp(count int) []float32 {
	samples := make([]float32, count)
	for i := range samples {
		samples[i] = float32(2*float64(i)/float64(count-1) - 1)
	}

	return samples
}

func TestRoundTripStaysWithinOneQuantisationStep(t *testing.T) {
	original := ramp(512)

	encoded, err := wavio.MarshalMono(44100, original)
	if err != nil {
		t.Fatalf("MarshalMono failed: %v", err)
	}

	decoded, sampleRate, err := wavio.DecodeMono(bytes.NewReader(encoded), "round trip")
	if err != nil {
		t.Fatalf("DecodeMono failed: %v", err)
	}

	if sampleRate != 44100 {
		t.Fatalf("sample rate = %d, want 44100", sampleRate)
	}

	if len(decoded) != len(original) {
		t.Fatalf("decoded %d samples, want %d", len(decoded), len(original))
	}

	for i := range original {
		// The round trip costs at most half a step of rounding plus the
		// documented 32767/32768 encode/decode asymmetry, which is worth
		// |sample|/32768 and so is itself under one step. Two steps is the
		// honest ceiling; anything larger is a scaling fault, not rounding.
		if diff := math.Abs(float64(decoded[i] - original[i])); diff > 2*quantisationStep {
			t.Fatalf("sample %d: decoded %g, original %g, diff %g exceeds two steps %g",
				i, decoded[i], original[i], diff, 2*quantisationStep)
		}
	}
}

// The write/read round trip is a 0.99997x gain rather than an identity, because
// the encoder quantizes against 32767 and the decoder divides by 32768. That is
// deliberate and explained on pcm16Peak, and it is worth 3.1e-5 relative --
// nothing the fitting objective can resolve. Pin it so that if either
// convention ever moves, the change is a failing test rather than a silent
// level shift in every reference a user fits against.
func TestTheRoundTripCarriesTheDocumentedScaleAsymmetry(t *testing.T) {
	// A value that lands exactly on the 16-bit grid, so rounding contributes
	// nothing and the residual is the scale ratio alone.
	const onGrid = 16384.0 / 32767.0

	encoded, err := wavio.MarshalMono(44100, []float32{onGrid})
	if err != nil {
		t.Fatalf("MarshalMono failed: %v", err)
	}

	decoded, _, err := wavio.DecodeMono(bytes.NewReader(encoded), "asymmetry")
	if err != nil {
		t.Fatalf("DecodeMono failed: %v", err)
	}

	got := float64(decoded[0]) / onGrid

	const wantRatio = 32767.0 / 32768.0
	if math.Abs(got-wantRatio) > 1e-6 {
		t.Fatalf("round-trip gain = %.9f, want the documented %.9f (32767/32768)", got, wantRatio)
	}
}

// A gain of exactly 1.0 is the case a 32767/32768 mismatch shows up in most
// clearly, so it is pinned on its own rather than left to the ramp.
func TestFullScaleSurvivesTheRoundTrip(t *testing.T) {
	encoded, err := wavio.MarshalMono(48000, []float32{1, -1, 0})
	if err != nil {
		t.Fatalf("MarshalMono failed: %v", err)
	}

	decoded, _, err := wavio.DecodeMono(bytes.NewReader(encoded), "full scale")
	if err != nil {
		t.Fatalf("DecodeMono failed: %v", err)
	}

	want := []float32{32767.0 / 32768.0, -32767.0 / 32768.0, 0}
	for i, expected := range want {
		if math.Abs(float64(decoded[i]-expected)) > 1e-6 {
			t.Fatalf("sample %d = %g, want %g", i, decoded[i], expected)
		}
	}
}

// Samples beyond full scale must clip, not wrap. A wrapped sample turns "this
// render is too loud" into a click that sounds like a modelling bug.
func TestOutOfRangeSamplesClip(t *testing.T) {
	encoded, err := wavio.MarshalMono(44100, []float32{4, -4})
	if err != nil {
		t.Fatalf("MarshalMono failed: %v", err)
	}

	decoded, _, err := wavio.DecodeMono(bytes.NewReader(encoded), "clipping")
	if err != nil {
		t.Fatalf("DecodeMono failed: %v", err)
	}

	if decoded[0] <= 0 {
		t.Fatalf("positive overshoot decoded as %g; it wrapped instead of clipping", decoded[0])
	}

	if decoded[1] >= 0 {
		t.Fatalf("negative overshoot decoded as %g; it wrapped instead of clipping", decoded[1])
	}
}

func TestWriteAndLoadRoundTripThroughDisk(t *testing.T) {
	// A nested path exercises the parent-directory creation the CLI relies on:
	// `fit --work-dir out/fit-a4` writes into a directory that does not exist.
	path := filepath.Join(t.TempDir(), "nested", "render.wav")

	original := ramp(64)
	if err := wavio.WriteMono(path, 22050, original); err != nil {
		t.Fatalf("WriteMono failed: %v", err)
	}

	decoded, sampleRate, err := wavio.LoadMono(path)
	if err != nil {
		t.Fatalf("LoadMono failed: %v", err)
	}

	if sampleRate != 22050 {
		t.Fatalf("sample rate = %d, want 22050", sampleRate)
	}

	if len(decoded) != len(original) {
		t.Fatalf("decoded %d samples, want %d", len(decoded), len(original))
	}
}

// MarshalMono and WriteMono must produce the same bytes. They share EncodeMono,
// and the point of this test is that the in-memory seeker behaves like a file
// under the encoder's rewind-to-patch-the-header pass.
func TestMarshalMatchesTheBytesWriteMonoPutsOnDisk(t *testing.T) {
	samples := ramp(200)

	path := filepath.Join(t.TempDir(), "render.wav")
	if err := wavio.WriteMono(path, 44100, samples); err != nil {
		t.Fatalf("WriteMono failed: %v", err)
	}

	onDisk, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}

	inMemory, err := wavio.MarshalMono(44100, samples)
	if err != nil {
		t.Fatalf("MarshalMono failed: %v", err)
	}

	if !bytes.Equal(onDisk, inMemory) {
		t.Fatalf("MarshalMono produced %d bytes, WriteMono %d, and they differ",
			len(inMemory), len(onDisk))
	}
}

// Garbage has to be reported as invalid input rather than as a server-side
// failure: the HTTP upload path turns ErrInvalidWAV into a 400 and everything
// else into a 500.
func TestNonWAVInputIsReportedAsInvalid(t *testing.T) {
	tests := []struct {
		name string
		data []byte
	}{
		{name: "empty", data: nil},
		{name: "text", data: []byte("this is not a wav file, it is a sentence")},
		{name: "riff header only", data: []byte("RIFF\x24\x00\x00\x00WAVE")},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			_, _, err := wavio.DecodeMono(bytes.NewReader(testCase.data), "hostile upload")
			if err == nil {
				t.Fatal("expected an error")
			}

			if !errors.Is(err, wavio.ErrInvalidWAV) {
				t.Fatalf("error %v does not wrap ErrInvalidWAV, so a caller cannot tell "+
					"bad input from a server fault", err)
			}
		})
	}
}

func TestLoadMonoReportsAMissingFile(t *testing.T) {
	_, _, err := wavio.LoadMono(filepath.Join(t.TempDir(), "absent.wav"))
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("error = %v, want it to wrap os.ErrNotExist", err)
	}
}

// A stereo file must be reduced to channel zero, not summed. Summing a stereo
// recording of a struck bar comb-filters exactly the partials a fit is trying
// to place. The two channels here are deliberately opposite, so a mixdown
// would return silence and be unmistakable.
func TestStereoTakesTheFirstChannelRatherThanMixingDown(t *testing.T) {
	const frames = 64

	left := make([]float32, frames)
	interleaved := make([]float32, 0, frames*2)

	for i := range frames {
		value := float32(i+1) / frames
		left[i] = value
		interleaved = append(interleaved, value, -value)
	}

	// MarshalMono always writes one channel, so build the stereo file by
	// patching the mono header the encoder produced: two channels, twice the
	// block align and byte rate. The sample data is already interleaved.
	encoded, err := wavio.MarshalMono(44100, interleaved)
	if err != nil {
		t.Fatalf("MarshalMono failed: %v", err)
	}

	stereo := makeStereo(t, encoded)

	decoded, sampleRate, err := wavio.DecodeMono(bytes.NewReader(stereo), "stereo")
	if err != nil {
		t.Fatalf("DecodeMono failed: %v", err)
	}

	if sampleRate != 44100 {
		t.Fatalf("sample rate = %d, want 44100", sampleRate)
	}

	if len(decoded) != frames {
		t.Fatalf("decoded %d frames, want %d", len(decoded), frames)
	}

	for i := range frames {
		if math.Abs(float64(decoded[i]-left[i])) > quantisationStep {
			t.Fatalf("frame %d = %g, want the left channel value %g", i, decoded[i], left[i])
		}
	}
}

// makeStereo rewrites a mono WAV's format chunk to declare two channels. The
// fmt chunk of a 16-bit PCM file is fixed-layout, so the three fields that
// depend on the channel count sit at known offsets from the chunk start.
func makeStereo(t *testing.T, mono []byte) []byte {
	t.Helper()

	index := bytes.Index(mono, []byte("fmt "))
	if index < 0 {
		t.Fatal("no fmt chunk in the encoded file")
	}

	stereo := append([]byte(nil), mono...)
	body := index + 8

	putUint16(stereo[body+2:], 2)         // channels
	putUint32(stereo[body+8:], 44100*2*2) // byte rate
	putUint16(stereo[body+12:], 2*2)      // block align

	return stereo
}

func putUint16(dst []byte, value uint16) {
	dst[0] = byte(value)
	dst[1] = byte(value >> 8)
}

func putUint32(dst []byte, value uint32) {
	dst[0] = byte(value)
	dst[1] = byte(value >> 8)
	dst[2] = byte(value >> 16)
	dst[3] = byte(value >> 24)
}
