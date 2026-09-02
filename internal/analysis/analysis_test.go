package analysis_test

import (
	"bytes"
	"errors"
	"math"
	"math/rand"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/cwbudde/algo-glockenspiel/internal/analysis"
)

const sampleRate = 44100

// mode is one decaying sinusoid of a synthetic reference.
type mode struct {
	frequencyHz float64
	amplitude   float64
	halfLifeMs  float64
}

// synthesize renders modes as decaying sinusoids from sample start, over
// seconds of signal, on top of white noise at noiseAmplitude.
func synthesize(modes []mode, start int, seconds, noiseAmplitude float64) []float32 {
	samples := make([]float32, int(seconds*sampleRate))
	random := rand.New(rand.NewSource(1)) //nolint:gosec // deterministic test noise

	for i := range samples {
		value := noiseAmplitude * (2*random.Float64() - 1)

		if i >= start {
			seconds := float64(i-start) / sampleRate
			for _, m := range modes {
				decay := math.Pow(0.5, seconds/(m.halfLifeMs/1000))
				value += m.amplitude * decay * math.Sin(2*math.Pi*m.frequencyHz*seconds)
			}
		}

		samples[i] = float32(value)
	}

	return samples
}

func TestPartialsRecoverSyntheticModes(t *testing.T) {
	modes := []mode{
		{frequencyHz: 1000, amplitude: 0.5, halfLifeMs: 300},
		{frequencyHz: 2531.7, amplitude: 0.1, halfLifeMs: 80},
		{frequencyHz: 6100, amplitude: 0.02, halfLifeMs: 500},
	}

	partials, err := analysis.Partials(synthesize(modes, 0, 1.5, 0), sampleRate, analysis.PartialOptions{})
	if err != nil {
		t.Fatalf("Partials failed: %v", err)
	}

	if len(partials) != len(modes) {
		t.Fatalf("found %d partials, want %d: %+v", len(partials), len(modes), partials)
	}

	for i, want := range modes {
		got := nearest(partials, want.frequencyHz)

		if math.Abs(got.FrequencyHz-want.frequencyHz)/want.frequencyHz > 0.001 {
			t.Errorf("partial %d frequency = %.2f Hz, want %.2f within 0.1%%", i, got.FrequencyHz, want.frequencyHz)
		}

		if math.Abs(got.HalfLifeMs-want.halfLifeMs)/want.halfLifeMs > 0.05 {
			t.Errorf("partial %d half-life = %.1f ms, want %.1f within 5%%", i, got.HalfLifeMs, want.halfLifeMs)
		}

		if wantAttack := 20 * math.Log10(want.amplitude); math.Abs(got.AttackDB-wantAttack) > 1 {
			t.Errorf("partial %d attack = %.2f dB, want %.2f within 1 dB", i, got.AttackDB, wantAttack)
		}

		if i > 0 && got.LevelDB >= 0 {
			t.Errorf("partial %d level = %.2f dB, want below the strongest", i, got.LevelDB)
		}
	}

	if partials[0].LevelDB != 0 {
		t.Errorf("strongest partial level = %g, want 0", partials[0].LevelDB)
	}
}

// nearest is the partial closest in frequency to frequencyHz.
func nearest(partials []analysis.Partial, frequencyHz float64) analysis.Partial {
	best := partials[0]

	for _, partial := range partials[1:] {
		if math.Abs(partial.FrequencyHz-frequencyHz) < math.Abs(best.FrequencyHz-frequencyHz) {
			best = partial
		}
	}

	return best
}

func TestPartialsDoNotReportTheSkirtOfAFastDecayAsPartials(t *testing.T) {
	modes := []mode{
		{frequencyHz: 2000, amplitude: 0.5, halfLifeMs: 30},
		{frequencyHz: 3000, amplitude: 0.5 * math.Pow(10, -30.0/20), halfLifeMs: 400},
	}

	partials, err := analysis.Partials(synthesize(modes, 0, 1, 0), sampleRate, analysis.PartialOptions{})
	if err != nil {
		t.Fatalf("Partials failed: %v", err)
	}

	if len(partials) != 2 {
		t.Fatalf("found %d partials, want the two modes and none of the skirt: %+v", len(partials), partials)
	}

	for _, want := range modes {
		if got := nearest(partials, want.frequencyHz); math.Abs(got.FrequencyHz-want.frequencyHz) > 1 {
			t.Errorf("no partial at %.0f Hz: %+v", want.frequencyHz, partials)
		}
	}
}

func TestMeasureReportsFundamentalAndFloor(t *testing.T) {
	modes := []mode{
		{frequencyHz: 3000, amplitude: 0.5, halfLifeMs: 200},
		{frequencyHz: 1200, amplitude: 0.2, halfLifeMs: 200},
		{frequencyHz: 400, amplitude: 0.005, halfLifeMs: 200},
	}

	measurement, err := analysis.Measure(synthesize(modes, 0, 1, 1e-4), sampleRate, analysis.PartialOptions{})
	if err != nil {
		t.Fatalf("Measure failed: %v", err)
	}

	// The 400 Hz mode is 40 dB down: too weak to be the fundamental, which is
	// the lowest partial within 30 dB of the strongest.
	if math.Abs(measurement.FundamentalHz-1200) > 2 {
		t.Errorf("fundamental = %.1f Hz, want 1200", measurement.FundamentalHz)
	}

	// White noise of RMS sigma reads sigma*sqrt(6/N) per Hann bin of N points
	// with the window's gain divided out: 5.8e-5 * sqrt(6/16384) is -119 dB.
	if math.Abs(measurement.NoiseFloorDB - -119) > 3 {
		t.Errorf("noise floor = %.1f dB, want about -119 for the white noise", measurement.NoiseFloorDB)
	}

	if measurement.Options.FrameSize != analysis.DefaultFrameSize {
		t.Errorf("options frame size = %d, want the default %d recorded", measurement.Options.FrameSize, analysis.DefaultFrameSize)
	}
}

func TestPartialsRefuseASignalShorterThanTheWindow(t *testing.T) {
	_, err := analysis.Partials(make([]float32, 100), sampleRate, analysis.PartialOptions{})
	if !errors.Is(err, analysis.ErrTooShort) {
		t.Fatalf("error = %v, want ErrTooShort", err)
	}
}

func TestPrepareReferenceCutsBeforeASecondStrike(t *testing.T) {
	const (
		firstStrike  = sampleRate / 5
		secondStrike = sampleRate * 3 / 2
	)

	modes := []mode{{frequencyHz: 1000, amplitude: 0.5, halfLifeMs: 100}}
	first := synthesize(modes, firstStrike, 2.5, 0)
	second := synthesize(modes, secondStrike, 2.5, 0)

	for i := range first {
		first[i] += second[i]
	}

	reference, err := analysis.PrepareReference([][]float32{first}, sampleRate, analysis.LoadOptions{})
	if err != nil {
		t.Fatalf("PrepareReference failed: %v", err)
	}

	if math.Abs(float64(reference.Onset-firstStrike)) > analysis.OnsetBlockSize {
		t.Errorf("onset = %d, want about %d", reference.Onset, firstStrike)
	}

	if reference.EndRule != analysis.EndSecondOnset {
		t.Errorf("end rule = %q, want %q", reference.EndRule, analysis.EndSecondOnset)
	}

	if reference.End > secondStrike || reference.End < secondStrike-sampleRate/10 {
		t.Errorf("end = %d, want within 100 ms before the second strike at %d", reference.End, secondStrike)
	}

	if len(reference.Samples) != reference.End-reference.Onset {
		t.Errorf("cut holds %d samples, want end-onset = %d", len(reference.Samples), reference.End-reference.Onset)
	}
}

func TestPrepareReferenceStopsWhereTheTailReachesTheFloor(t *testing.T) {
	modes := []mode{{frequencyHz: 1000, amplitude: 0.5, halfLifeMs: 100}}

	reference, err := analysis.PrepareReference(
		[][]float32{synthesize(modes, 0, 3, 5e-4)}, sampleRate, analysis.LoadOptions{})
	if err != nil {
		t.Fatalf("PrepareReference failed: %v", err)
	}

	if reference.EndRule != analysis.EndFloor {
		t.Errorf("end rule = %q, want %q", reference.EndRule, analysis.EndFloor)
	}

	// The strike is 60 dB down after ten half-lives, one second, and the
	// noise sits about 57 dB below the strike's RMS.
	if reference.Seconds < 0.7 || reference.Seconds > 1.5 {
		t.Errorf("cut = %.2f s, want about a second, where the decay meets the floor", reference.Seconds)
	}
}

func TestPrepareReferenceKeepsAWholeCleanDecay(t *testing.T) {
	modes := []mode{{frequencyHz: 1000, amplitude: 0.5, halfLifeMs: 200}}

	reference, err := analysis.PrepareReference(
		[][]float32{synthesize(modes, 0, 1, 0)}, sampleRate, analysis.LoadOptions{})
	if err != nil {
		t.Fatalf("PrepareReference failed: %v", err)
	}

	if reference.EndRule != analysis.EndOfFile || reference.End != sampleRate {
		t.Errorf("end = %d under %q, want the whole file under %q", reference.End, reference.EndRule, analysis.EndOfFile)
	}
}

func TestPrepareReferenceWindowAndLevel(t *testing.T) {
	modes := []mode{{frequencyHz: 1000, amplitude: 0.25, halfLifeMs: 200}}
	samples := synthesize(modes, 0, 1, 0)

	reference, err := analysis.PrepareReference(
		[][]float32{samples}, sampleRate, analysis.LoadOptions{Window: 500 * time.Millisecond})
	if err != nil {
		t.Fatalf("PrepareReference failed: %v", err)
	}

	if reference.EndRule != analysis.EndWindow || len(reference.Samples) != sampleRate/2 {
		t.Errorf("cut = %d samples under %q, want %d under %q", len(reference.Samples), reference.EndRule, sampleRate/2, analysis.EndWindow)
	}

	if math.Abs(reference.GainDB-20*math.Log10(1/reference.PeakBefore)) > 1e-9 {
		t.Errorf("gain = %.3f dB, want the peak %.4f brought to full scale", reference.GainDB, reference.PeakBefore)
	}

	peak := 0.0
	for _, sample := range reference.Samples {
		peak = math.Max(peak, math.Abs(float64(sample)))
	}

	if math.Abs(peak-1) > 1e-6 {
		t.Errorf("normalised peak = %g, want 1", peak)
	}

	kept, err := analysis.PrepareReference(
		[][]float32{samples}, sampleRate, analysis.LoadOptions{KeepLevel: true})
	if err != nil {
		t.Fatalf("PrepareReference failed: %v", err)
	}

	if kept.GainDB != 0 || kept.Samples[0] != samples[kept.Onset] {
		t.Errorf("KeepLevel applied gain %g dB", kept.GainDB)
	}
}

func TestPrepareReferenceDownmix(t *testing.T) {
	modes := []mode{{frequencyHz: 1000, amplitude: 0.5, halfLifeMs: 200}}
	left := synthesize(modes, 0, 1, 0)
	inverted := make([]float32, len(left))

	for i, sample := range left {
		inverted[i] = -sample
	}

	first, err := analysis.PrepareReference([][]float32{left, inverted}, sampleRate, analysis.LoadOptions{KeepLevel: true})
	if err != nil {
		t.Fatalf("PrepareReference failed: %v", err)
	}

	if first.Channels != 2 || first.Downmix != analysis.DownmixFirst || first.Samples[100] != left[100+first.Onset] {
		t.Errorf("default downmix did not keep channel zero: %+v", first.Downmix)
	}

	_, err = analysis.PrepareReference([][]float32{left, inverted}, sampleRate, analysis.LoadOptions{Downmix: analysis.DownmixMean})
	if !errors.Is(err, analysis.ErrSilentReference) {
		t.Errorf("mean of a channel and its inverse: error = %v, want ErrSilentReference", err)
	}

	mean, err := analysis.PrepareReference([][]float32{left, left}, sampleRate, analysis.LoadOptions{Downmix: analysis.DownmixMean, KeepLevel: true})
	if err != nil {
		t.Fatalf("PrepareReference failed: %v", err)
	}

	if mean.Downmix != analysis.DownmixMean || mean.Samples[100] != first.Samples[100] {
		t.Errorf("mean of two equal channels differs from the channel")
	}

	if _, err := analysis.ParseDownmix("sum"); err == nil {
		t.Error("ParseDownmix accepted an unknown policy")
	}
}

func TestTheC5RecordingMeasuresAsTheHandTableSays(t *testing.T) {
	path := filepath.Join("..", "..", "testdata", "reference", "glockenspiel_c5.wav")

	document, err := analysis.Analyze(path, analysis.LoadOptions{}, analysis.PartialOptions{})
	if err != nil {
		t.Fatalf("Analyze failed: %v", err)
	}

	if document.Reference.Channels != 2 || document.Reference.Frames != 317816 {
		t.Errorf("reference = %+v, want the two-channel 317816-frame file", document.Reference)
	}

	// The strike ends where the tail stops falling, before the second event
	// at two seconds that testdata/reference/README.md describes.
	if document.Reference.Seconds < 1.4 || document.Reference.Seconds > 1.9 || document.Reference.EndRule != analysis.EndFloor {
		t.Errorf("cut = %.2f s under %q, want the first strike only", document.Reference.Seconds, document.Reference.EndRule)
	}

	// testdata/reference/README.md, measured by hand over the first second.
	// The half-lives there are early-decay figures; the fitted line here
	// covers the whole cut, over which the fundamental steepens.
	hand := []struct {
		frequencyHz float64
		halfLifeMs  float64
	}{
		{1053.6, 677},
		{3096.9, 117},
		{8023.8, 204},
		{5836.8, 55},
		{4139.8, 626},
		{3705.2, 71},
	}

	for _, want := range hand {
		var found *analysis.Partial

		for i := range document.Partials {
			if math.Abs(document.Partials[i].FrequencyHz-want.frequencyHz)/want.frequencyHz <= 0.005 {
				found = &document.Partials[i]

				break
			}
		}

		if found == nil {
			t.Errorf("no partial within 0.5%% of %.1f Hz in %+v", want.frequencyHz, document.Partials)

			continue
		}

		if math.Abs(found.HalfLifeMs-want.halfLifeMs)/want.halfLifeMs > 0.2 {
			t.Errorf("%.1f Hz half-life = %.0f ms, want %.0f within 20%%", want.frequencyHz, found.HalfLifeMs, want.halfLifeMs)
		}
	}

	if document.Partials[0].FrequencyHz < 1053 || document.Partials[0].FrequencyHz > 1054.5 {
		t.Errorf("strongest partial at %.1f Hz, want the 1053.6 Hz fundamental", document.Partials[0].FrequencyHz)
	}

	if math.Abs(document.FundamentalHz-document.Partials[0].FrequencyHz) > 1e-9 {
		t.Errorf("fundamental = %.1f Hz, want the strongest partial", document.FundamentalHz)
	}
}

func TestTheA4RenderIsOnePartial(t *testing.T) {
	path := filepath.Join("..", "..", "testdata", "reference", "legacy_synth_a4.wav")

	document, err := analysis.Analyze(path, analysis.LoadOptions{}, analysis.PartialOptions{})
	if err != nil {
		t.Fatalf("Analyze failed: %v", err)
	}

	if document.Reference.EndRule != analysis.EndOfFile {
		t.Errorf("end rule = %q, want the whole render", document.Reference.EndRule)
	}

	if len(document.Partials) != 1 || math.Abs(document.Partials[0].FrequencyHz-1756.5) > 1 {
		t.Errorf("partials = %+v, want the single 1756.5 Hz mode within 40 dB", document.Partials)
	}
}

func TestAnalysisRoundTripsThroughJSON(t *testing.T) {
	modes := []mode{{frequencyHz: 1000, amplitude: 0.5, halfLifeMs: 200}}

	reference, err := analysis.PrepareReference([][]float32{synthesize(modes, 0, 1, 0)}, sampleRate, analysis.LoadOptions{})
	if err != nil {
		t.Fatalf("PrepareReference failed: %v", err)
	}

	document, err := analysis.AnalyzeReference("synthetic", reference, analysis.PartialOptions{})
	if err != nil {
		t.Fatalf("AnalyzeReference failed: %v", err)
	}

	document.Partials = append(document.Partials, analysis.Partial{FrequencyHz: 5000, LevelDB: -20, HalfLifeMs: math.NaN()})

	var buffer bytes.Buffer
	if err := document.Write(&buffer); err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	if !bytes.Contains(buffer.Bytes(), []byte(`"half_life_ms": null`)) {
		t.Errorf("NaN half-life was not written as null:\n%s", buffer.String())
	}

	if bytes.Contains(buffer.Bytes(), []byte(`"samples"`)) {
		t.Errorf("the audio was written into the document")
	}

	decoded, err := analysis.Read(&buffer)
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}

	document.Reference.Samples = nil

	if decoded.Source != "synthetic" || !reflect.DeepEqual(decoded.Reference, document.Reference) {
		t.Errorf("reference record did not round-trip: %+v vs %+v", decoded.Reference, document.Reference)
	}

	if len(decoded.Partials) != 2 || !math.IsNaN(decoded.Partials[1].HalfLifeMs) {
		t.Errorf("partials did not round-trip: %+v", decoded.Partials)
	}

	if decoded.Partials[0] != document.Partials[0] {
		t.Errorf("measured partial did not round-trip: %+v vs %+v", decoded.Partials[0], document.Partials[0])
	}

	path := filepath.Join(t.TempDir(), "run", "analysis.json")
	if err := document.WriteFile(path); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	fromDisk, err := analysis.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}

	if fromDisk.FundamentalHz != document.FundamentalHz {
		t.Errorf("fundamental did not round-trip through disk")
	}

	if _, err := analysis.Read(bytes.NewReader([]byte(`{"partials": []}`))); err == nil {
		t.Error("Read accepted a document without the generated_by marker")
	}
}

func TestOnsetOfSilenceIsZero(t *testing.T) {
	if got := analysis.Onset(make([]float32, 1000)); got != 0 {
		t.Errorf("Onset(silence) = %d, want 0", got)
	}

	if got := analysis.Onset(nil); got != 0 {
		t.Errorf("Onset(nil) = %d, want 0", got)
	}
}

func TestDecaySlopeOfASingleModeIsItsHalfLife(t *testing.T) {
	// A 200 ms half-life is -30.1 dB/s.
	signal := synthesize([]mode{{frequencyHz: 1000, amplitude: 0.5, halfLifeMs: 200}}, 0, 1.5, 0)

	slope := analysis.DecaySlopeDBps(signal, 44100)
	if math.Abs(slope+30.1) > 1.5 {
		t.Fatalf("slope = %.2f dB/s, want -30.1", slope)
	}

	if got := analysis.DecaySlopeDBps(make([]float32, 44100), 44100); !math.IsNaN(got) {
		t.Fatalf("slope of silence = %g, want NaN", got)
	}
}
