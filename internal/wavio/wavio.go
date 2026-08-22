// Package wavio reads and writes the one WAV shape this project deals in: a
// single channel of PCM, decoded to float32 in [-1,1] and encoded back at 16
// bits.
//
// It exists because three callers had grown their own copy of the same
// twenty-line go-audio/wav dance -- internal/cli/fit.go, internal/cli/synth.go
// and internal/optimizer/legacy_validation_test.go -- and Phase 4.2 was about
// to add a fourth for uploaded references. Four copies of a decoder is four
// places for the bit-depth scaling or the channel stride to drift, and a
// reference that decodes 0.99997x louder in one path than another moves the
// optimum without anything failing.
//
// The reader-based entry points matter as much as the path-based ones: an
// uploaded reference arrives as bytes in memory and must never be written to a
// filesystem path derived from what the client sent.
package wavio

import (
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"

	"github.com/go-audio/audio"
	"github.com/go-audio/wav"
)

// encodeBitDepth is the width every WAV this project writes is quantized to.
// It is fixed rather than configurable because the objective's PCM projection
// (optimizer.ProjectToPCM16Domain) is written against the same number; making
// one of them a parameter without the other would report an error figure for a
// file that does not exist.
const encodeBitDepth = 16

// pcm16Peak is the largest positive value a 16-bit sample can carry, and the
// factor EncodeMono quantizes against.
//
// It is deliberately not the factor DecodeMono divides by. A decoder has to
// scale by 2^(bits-1) -- 32768 here -- because that is what maps the negative
// rail, -32768, onto -1.0; scaling by 32767 instead would decode a full-scale
// negative sample as -1.00003 and clip anything derived from it. Encoding
// against 32768 in return would make +1.0 unrepresentable.
//
// The consequence is that a write/read round trip is not an identity: it is a
// gain of 32767/32768, 0.99997x, or -0.00027 dB. That is 3.1e-5 relative,
// which is a thirtieth of what a 16-bit LSB is worth at low amplitude and far
// below anything the fitting objective can resolve, so both conventions are
// kept as they were rather than one of them being "corrected" -- but it is a
// real asymmetry rather than an accident, so it is named here and pinned by
// TestTheRoundTripCarriesTheDocumentedScaleAsymmetry.
//
// Note that optimizer.ProjectToPCM16Domain is a different thing with a similar
// name: it quantizes in place, using 32767 in *both* directions, because there
// the round trip has to be an identity to keep the cost surface from acquiring
// a step it did not earn.
const pcm16Peak = 32767.0

// ErrInvalidWAV reports input that the decoder does not recognise as a RIFF
// WAVE stream at all, as opposed to one it recognises and fails to read.
// Callers that answer over HTTP use it to tell a bad upload (400) apart from a
// server-side failure (500).
var ErrInvalidWAV = errors.New("not a valid wav file")

// ErrUnsupportedWAV reports a stream that is a valid WAV but carries a sample
// format this package refuses to guess at. It is separate from ErrInvalidWAV
// because the two mean different things to an HTTP caller and to a person: the
// file is fine, we will not read it.
var ErrUnsupportedWAV = errors.New("unsupported wav sample format")

// The format tags a WAVE fmt chunk can carry. go-audio surfaces the tag as
// Decoder.WavAudioFormat but decodes every one of them as integer PCM, which is
// the reason this package has to look at it.
const (
	wavFormatPCM        = 1
	wavFormatIEEEFloat  = 3
	wavFormatExtensible = 0xFFFE
)

// LoadMono reads a WAV file from disk and returns its first channel together
// with the sample rate the file declares.
func LoadMono(path string) ([]float32, int, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, 0, fmt.Errorf("open wav %q: %w", path, err)
	}

	defer func() {
		_ = file.Close()
	}()

	return DecodeMono(file, path)
}

// DecodeMono decodes a WAV stream and returns its first channel as float32
// samples plus the declared sample rate. source only names the origin in error
// messages; it is never used as a path.
//
// Integer PCM lands in [-1,1] by construction, since it is divided by the
// widest magnitude its bit depth can hold. IEEE float carries whatever was
// written, which for a file mastered above full scale is legitimately outside
// that range -- clamping it here would quietly reshape a reference rather than
// read it. Every sample is finite, though: see rejectNonFinite.
//
// A multi-channel file is reduced by taking channel zero rather than by mixing
// down. The fitting objective compares one rendered voice against the
// reference, and a stereo recording of a struck bar carries the same event in
// both channels with a room delay between them -- summing them comb-filters
// exactly the partials the fit is trying to place.
func DecodeMono(reader io.ReadSeeker, source string) ([]float32, int, error) {
	decoder := wav.NewDecoder(reader)
	if !decoder.IsValidFile() {
		return nil, 0, fmt.Errorf("%s: %w", source, ErrInvalidWAV)
	}

	intBuffer, err := decoder.FullPCMBuffer()
	if err != nil {
		return nil, 0, fmt.Errorf("decode wav %q: %w", source, err)
	}

	if intBuffer == nil || intBuffer.Format == nil {
		return nil, 0, fmt.Errorf("decode wav %q: %w", source, ErrInvalidWAV)
	}

	// A file that declares no bit depth is read as 16-bit, which is what every
	// WAV this project writes is. Guessing is better than dividing by zero.
	bitDepth := intBuffer.SourceBitDepth
	if bitDepth <= 0 {
		bitDepth = encodeBitDepth
	}

	convert, err := sampleConverter(decoder.WavAudioFormat, bitDepth, source)
	if err != nil {
		return nil, 0, err
	}

	channels := intBuffer.Format.NumChannels
	if channels <= 0 {
		channels = 1
	}

	samples := make([]float32, len(intBuffer.Data)/channels)
	for i := range samples {
		samples[i] = convert(intBuffer.Data[i*channels])
	}

	// Only the float path can produce a non-finite sample, so only it pays for
	// the scan.
	if decoder.WavAudioFormat == wavFormatIEEEFloat {
		if err := rejectNonFinite(samples, source); err != nil {
			return nil, 0, err
		}
	}

	return samples, intBuffer.Format.SampleRate, nil
}

// rejectNonFinite fails a decode that produced a NaN or an infinity.
//
// An integer sample cannot be either, but a float file can carry both, and
// nothing downstream is prepared for them. internal/optimizer's
// validateObjectiveInputs checks only that a reference is non-empty, so a
// single NaN makes every RMS and every correlation against that reference NaN
// as well: the objective stops ordering candidates, the optimizer spends its
// whole budget comparing values none of which is better than any other, and it
// reports a fit at the end as though it had found one.
//
// Failing here rather than sanitising is the same call the format check above
// makes. There is no defensible value to substitute -- zero invents silence
// where the file says something is wrong, and clamping an infinity to full
// scale invents a transient -- and a reference the caller believes in is worth
// a message rather than a repair.
func rejectNonFinite(samples []float32, source string) error {
	for i, sample := range samples {
		if math.IsInf(float64(sample), 0) || math.IsNaN(float64(sample)) {
			return fmt.Errorf("%s: %w: sample %d is %v", source, ErrInvalidWAV, i, sample)
		}
	}

	return nil
}

// sampleConverter returns the function that turns one go-audio sample word into
// a float32 in [-1,1], chosen by the format tag the fmt chunk declares.
//
// go-audio hands every format back as an int, including IEEE float: its 32-bit
// reader is int(int32(binary.LittleEndian.Uint32(...))) and its float path
// carries "TODO: fix the float64 conversion (current int implementation)". So a
// float sample arrives here as its own bit pattern read as a signed integer,
// and dividing that by 2^31 is not a decode, it is a reinterpretation.
//
// What that produced is worth naming, because it is not a subtle error and it
// went unnoticed for the life of the repository. Every float32 in [0.06, 1.0]
// has an exponent that puts its bit pattern between 0x3D80.. and 0x3F80..;
// divided by 2^31 that is roughly +0.49, and the sign bit turns the negatives
// into roughly -0.51. Any recording at a sane level therefore decoded to a
// square wave of about +-0.5, whatever it actually contained. The shipped
// reference at testdata/reference/legacy_synth_a4.wav is a struck bar with peak
// 1.0, RMS 0.193 and a crest factor of 5.18; through the old path it arrived as
// RMS 0.5004 with a crest factor of 1.14. internal/cli/fit.go and
// internal/server/fit.go both read user references through here, so every fit
// against a 32-bit float WAV -- the format a DAW exports by default -- was
// fitting a square wave.
func sampleConverter(format uint16, bitDepth int, source string) (func(int) float32, error) {
	if format == wavFormatIEEEFloat {
		if bitDepth != 32 {
			return nil, fmt.Errorf("%s: %w: %d-bit IEEE float", source, ErrUnsupportedWAV, bitDepth)
		}

		// Back through the same door go-audio came out of: the int holds the
		// sign-extended 32 bits of the original float, so narrowing to int32
		// and reading those bits as a float32 is exact for every value,
		// denormals and infinities included.
		return func(sample int) float32 {
			return math.Float32frombits(uint32(int32(sample)))
		}, nil
	}

	// WAVE_FORMAT_EXTENSIBLE names its real format in a subformat GUID that
	// go-audio does not surface, so at 32 bits there is no telling PCM from
	// float -- and the two differ by about 30 dB, silently. Below 32 bits the
	// ambiguity does not arise: nothing writes 8-, 16- or 24-bit IEEE float, so
	// an extensible file that narrow is PCM and decodes as it always has.
	if format == wavFormatExtensible && bitDepth >= 32 {
		return nil, fmt.Errorf(
			"%s: %w: WAVE_FORMAT_EXTENSIBLE at %d bits, which may be PCM or float; "+
				"re-export as 16- or 24-bit PCM, or as plain 32-bit float",
			source, ErrUnsupportedWAV, bitDepth,
		)
	}

	// Everything else is integer PCM. A tag of zero -- a fmt chunk that carried
	// none -- is treated as PCM for the same reason a missing bit depth is
	// treated as 16: this package's own files are 16-bit PCM and guessing beats
	// refusing. An unrecognised tag lands here too, decoding exactly as it did
	// before this function existed.
	scale := math.Pow(2, float64(bitDepth-1))

	return func(sample int) float32 {
		return float32(float64(sample) / scale)
	}, nil
}

// WriteMono encodes samples as a 16-bit mono WAV at path, creating the parent
// directory if it is missing.
func WriteMono(path string, sampleRate int, samples []float32) error {
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create output directory: %w", err)
		}
	}

	file, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create output file %q: %w", path, err)
	}

	defer func() {
		_ = file.Close()
	}()

	return EncodeMono(file, sampleRate, samples)
}

// EncodeMono writes samples as a 16-bit mono WAV.
//
// The writer has to be seekable because a RIFF header states the length of a
// chunk that has not been written yet; the encoder rewinds to fill it in on
// Close. MarshalMono wraps a byte slice for callers that have no file.
func EncodeMono(writer io.WriteSeeker, sampleRate int, samples []float32) error {
	encoder := wav.NewEncoder(writer, sampleRate, encodeBitDepth, 1, 1)

	intData := make([]int, len(samples))
	for i, sample := range samples {
		intData[i] = float32ToInt16(sample)
	}

	buffer := &audio.IntBuffer{
		Format: &audio.Format{
			NumChannels: 1,
			SampleRate:  sampleRate,
		},
		SourceBitDepth: encodeBitDepth,
		Data:           intData,
	}

	if err := encoder.Write(buffer); err != nil {
		return fmt.Errorf("write wav data: %w", err)
	}

	if err := encoder.Close(); err != nil {
		return fmt.Errorf("close wav writer: %w", err)
	}

	return nil
}

// MarshalMono encodes samples as a 16-bit mono WAV held in memory.
//
// The HTTP render endpoint needs the whole file before it answers -- it has to
// send a Content-Length, and a half-written body after a mid-stream failure
// would reach the browser as a truncated but successful download.
func MarshalMono(sampleRate int, samples []float32) ([]byte, error) {
	sink := &memoryWriteSeeker{}
	if err := EncodeMono(sink, sampleRate, samples); err != nil {
		return nil, err
	}

	return sink.data, nil
}

// float32ToInt16 quantizes one sample, clipping rather than wrapping. A sample
// beyond full scale is a render that is too loud, and a wrapped sample would
// turn that into a click that sounds like a modelling bug.
func float32ToInt16(sample float32) int {
	v := math.Max(-1, math.Min(1, float64(sample)))

	return int(math.Round(v * pcm16Peak))
}

// memoryWriteSeeker is the seekable sink MarshalMono hands the encoder. It is
// deliberately minimal: the encoder only ever appends and then seeks back
// inside what it already wrote, so seeking past the end never happens and is
// reported rather than silently zero-filled.
type memoryWriteSeeker struct {
	data   []byte
	offset int64
}

func (m *memoryWriteSeeker) Write(chunk []byte) (int, error) {
	end := m.offset + int64(len(chunk))
	if end > int64(len(m.data)) {
		m.data = append(m.data, make([]byte, end-int64(len(m.data)))...)
	}

	copy(m.data[m.offset:end], chunk)
	m.offset = end

	return len(chunk), nil
}

func (m *memoryWriteSeeker) Seek(offset int64, whence int) (int64, error) {
	var next int64

	switch whence {
	case io.SeekStart:
		next = offset
	case io.SeekCurrent:
		next = m.offset + offset
	case io.SeekEnd:
		next = int64(len(m.data)) + offset
	default:
		return 0, fmt.Errorf("wavio: invalid whence %d", whence)
	}

	if next < 0 || next > int64(len(m.data)) {
		return 0, fmt.Errorf("wavio: seek to %d is outside the %d bytes written", next, len(m.data))
	}

	m.offset = next

	return next, nil
}
