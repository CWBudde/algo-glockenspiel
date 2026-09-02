package analysis

import (
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"time"

	"github.com/cwbudde/algo-glockenspiel/internal/wavio"
)

// Downmix names how a multi-channel file is reduced to the one channel the
// model is fitted against.
type Downmix string

const (
	// DownmixFirst keeps channel zero, which is what wavio.LoadMono and hence
	// every fit so far has done. It is the default because a stereo recording
	// of one struck bar holds the same event in both channels with a room
	// delay between them, and summing them comb-filters the partials.
	DownmixFirst Downmix = "first"

	// DownmixMean averages the channels. It is offered for a file whose
	// channels are a genuine mono pair rather than a spaced one.
	DownmixMean Downmix = "mean"
)

// ParseDownmix reads a downmix name from the command line. An empty name is
// the default.
func ParseDownmix(value string) (Downmix, error) {
	switch Downmix(value) {
	case "", DownmixFirst:
		return DownmixFirst, nil
	case DownmixMean:
		return DownmixMean, nil
	default:
		return "", fmt.Errorf("unsupported downmix %q (use %q or %q)", value, DownmixFirst, DownmixMean)
	}
}

// EndRule names what decided where the strike ends.
type EndRule string

const (
	// EndSecondOnset means a second event was found in the tail and the strike
	// was cut just before it.
	EndSecondOnset EndRule = "second onset"

	// EndFloor means the tail decayed into the floor and was cut where it
	// stopped falling.
	EndFloor EndRule = "tail reached floor"

	// EndOfFile means nothing in the file ended the strike.
	EndOfFile EndRule = "end of file"

	// EndWindow means the caller asked for a fixed length after the onset.
	EndWindow EndRule = "fixed window"
)

// LoadOptions is what a caller decides about a reference. The zero value is
// the default: first channel, strike cut where the tail ends, peak-normalised.
type LoadOptions struct {
	// Downmix chooses the channel reduction; empty means DownmixFirst.
	Downmix Downmix

	// Window, when positive, cuts the reference to this length after the
	// onset regardless of what the tail does. Zero asks for the automatic
	// cut: up to the next onset, or to where the tail stops falling.
	Window time.Duration

	// KeepLevel leaves the samples at the file's level. By default the cut
	// reference is scaled so its peak is full scale, because a recording's
	// level is someone's gain staging and not the bar's loudness.
	KeepLevel bool
}

// Reference is a reference after the decisions in LoadOptions were applied,
// together with a record of what they did.
type Reference struct {
	// Samples is the cut, downmixed, scaled signal, starting at the onset.
	Samples []float32 `json:"-"`

	// SampleRate is the rate the file declared.
	SampleRate int `json:"sample_rate"`

	// Channels is how many the file held.
	Channels int `json:"channels"`

	// Downmix is the reduction that was applied.
	Downmix Downmix `json:"downmix"`

	// Frames is the length of the file before the cut.
	Frames int `json:"frames"`

	// Onset is the sample of the file at which the strike begins and Samples
	// starts.
	Onset int `json:"onset"`

	// End is the sample of the file at which Samples stops, exclusive.
	End int `json:"end"`

	// EndRule says what decided End.
	EndRule EndRule `json:"end_rule"`

	// Seconds is the cut length.
	Seconds float64 `json:"seconds"`

	// PeakBefore is the peak of the downmixed file before scaling.
	PeakBefore float64 `json:"peak_before"`

	// GainDB is the scaling that was applied, in dB. Zero under KeepLevel.
	GainDB float64 `json:"gain_db"`
}

// ErrSilentReference reports a file with no signal to cut a strike from.
var ErrSilentReference = errors.New("reference is silent")

// LoadReference reads a WAV file and applies the options to it.
func LoadReference(path string, options LoadOptions) (*Reference, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open reference %q: %w", path, err)
	}

	defer func() {
		_ = file.Close()
	}()

	return DecodeReference(file, path, options)
}

// DecodeReference reads a WAV stream and applies the options to it. source
// names the origin in error messages only.
func DecodeReference(reader io.ReadSeeker, source string, options LoadOptions) (*Reference, error) {
	channels, sampleRate, err := wavio.DecodeChannels(reader, source)
	if err != nil {
		return nil, err
	}

	return PrepareReference(channels, sampleRate, options)
}

// PrepareReference applies the options to already decoded channels. It is the
// step behind DecodeReference, exposed so that a caller holding samples --
// the server, a test -- does not have to encode them first.
func PrepareReference(channels [][]float32, sampleRate int, options LoadOptions) (*Reference, error) {
	if len(channels) == 0 || len(channels[0]) == 0 {
		return nil, ErrSilentReference
	}

	if sampleRate <= 0 {
		return nil, fmt.Errorf("reference sample rate must be positive, got %d", sampleRate)
	}

	downmix, err := ParseDownmix(string(options.Downmix))
	if err != nil {
		return nil, err
	}

	mixed := downmixChannels(channels, downmix)

	peak := 0.0
	for _, sample := range mixed {
		peak = math.Max(peak, math.Abs(float64(sample)))
	}

	if peak <= 0 {
		return nil, ErrSilentReference
	}

	onset := Onset(mixed)
	end, rule := strikeEnd(mixed, onset, sampleRate)

	if options.Window > 0 {
		end = min(len(mixed), onset+int(math.Round(options.Window.Seconds()*float64(sampleRate))))
		rule = EndWindow
	}

	if end <= onset {
		return nil, fmt.Errorf("reference cut is empty: onset %d, end %d", onset, end)
	}

	gain := 1.0
	if !options.KeepLevel {
		gain = 1 / peak
	}

	cut := make([]float32, end-onset)
	for i, sample := range mixed[onset:end] {
		cut[i] = float32(float64(sample) * gain)
	}

	return &Reference{
		Samples:    cut,
		SampleRate: sampleRate,
		Channels:   len(channels),
		Downmix:    downmix,
		Frames:     len(mixed),
		Onset:      onset,
		End:        end,
		EndRule:    rule,
		Seconds:    float64(end-onset) / float64(sampleRate),
		PeakBefore: peak,
		GainDB:     20 * math.Log10(gain),
	}, nil
}

// downmixChannels reduces the channels to one under the named policy.
func downmixChannels(channels [][]float32, downmix Downmix) []float32 {
	if downmix != DownmixMean || len(channels) == 1 {
		return channels[0]
	}

	mixed := make([]float32, len(channels[0]))
	scale := 1 / float32(len(channels))

	for _, channel := range channels {
		for i := range mixed {
			mixed[i] += channel[i] * scale
		}
	}

	return mixed
}
