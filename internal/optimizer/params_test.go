package optimizer

import (
	"math"
	"testing"

	"github.com/cwbudde/algo-glockenspiel/model"
)

func TestParamCodecEncodeDecodeRoundTrip(t *testing.T) {
	params := validBarParams()

	codec, err := NewParamCodec(&params)
	if err != nil {
		t.Fatalf("NewParamCodec failed: %v", err)
	}

	encoded, err := codec.EncodeParams(&params)
	if err != nil {
		t.Fatalf("EncodeParams failed: %v", err)
	}

	decoded, err := codec.DecodeParams(encoded)
	if err != nil {
		t.Fatalf("DecodeParams failed: %v", err)
	}

	assertClose(t, decoded.InputMix, params.InputMix, 1e-12, "input mix")
	assertClose(t, decoded.FilterFrequency, params.FilterFrequency, 1e-9, "filter frequency")
	assertClose(t, decoded.BaseFrequency, params.BaseFrequency, 1e-9, "base frequency")

	if decoded.Chebyshev.Enabled != params.Chebyshev.Enabled {
		t.Fatalf("chebyshev enabled mismatch: got %v want %v", decoded.Chebyshev.Enabled, params.Chebyshev.Enabled)
	}

	// The codec writes modes in ascending order of frequency, whatever order
	// they were handed in, so the round trip is exact up to that sort.
	expected := sortedModes(params.Modes)

	for i := range expected {
		assertClose(t, decoded.Modes[i].Amplitude, expected[i].Amplitude, 1e-12, "mode amplitude")
		assertClose(t, decoded.Modes[i].Frequency, expected[i].Frequency, 1e-9, "mode frequency")
		assertClose(t, decoded.Modes[i].DecayMs, expected[i].DecayMs, 1e-9, "mode decay")
	}

	for i := 1; i < len(decoded.Modes); i++ {
		if decoded.Modes[i].Frequency < decoded.Modes[i-1].Frequency {
			t.Fatalf("decoded modes are not sorted by frequency: %v", decoded.Modes)
		}
	}

	for i := range params.Chebyshev.HarmonicGains {
		assertClose(t, decoded.Chebyshev.HarmonicGains[i], params.Chebyshev.HarmonicGains[i], 1e-12, "harmonic gain")
	}
}

func TestParamCodecEncodedBoundsMatchDimension(t *testing.T) {
	params := validBarParams()

	codec, err := NewParamCodec(&params)
	if err != nil {
		t.Fatalf("NewParamCodec failed: %v", err)
	}

	bounds := codec.EncodedBounds()
	if got, want := bounds.Dimension(), codec.Dimension(); got != want {
		t.Fatalf("encoded bounds dimension mismatch: got %d want %d", got, want)
	}

	if !bounds.Contains(mustEncode(t, codec, &params)) {
		t.Fatal("expected encoded parameters to be within generated bounds")
	}
}

func TestBoundsClamp(t *testing.T) {
	params := validBarParams()

	codec, err := NewParamCodec(&params)
	if err != nil {
		t.Fatalf("NewParamCodec failed: %v", err)
	}

	input := make([]float64, codec.Dimension())
	for i := range input {
		input[i] = math.Inf(1)
	}

	input[0] = -10

	clamped, err := codec.EncodedBounds().Clamp(input)
	if err != nil {
		t.Fatalf("Clamp failed: %v", err)
	}

	if !codec.EncodedBounds().Contains(clamped) {
		t.Fatal("expected clamped vector to be within bounds")
	}
}

func TestBoundsNormalizeClampsOutOfRangeValues(t *testing.T) {
	params := validBarParams()

	codec, err := NewParamCodec(&params)
	if err != nil {
		t.Fatalf("NewParamCodec failed: %v", err)
	}

	bounds := codec.EncodedBounds()
	input := mustEncode(t, codec, &params)
	input[0] = bounds.Ranges[0].Max + 0.25
	input[1] = bounds.Ranges[1].Min - 0.5

	normalized, err := bounds.Normalize(input)
	if err != nil {
		t.Fatalf("Normalize failed: %v", err)
	}

	for i, v := range normalized {
		if v < 0 || v > 1 {
			t.Fatalf("normalized component %d escaped the unit cube: %g", i, v)
		}
	}

	denormalized, err := bounds.Denormalize(normalized)
	if err != nil {
		t.Fatalf("Denormalize failed: %v", err)
	}

	if !bounds.Contains(denormalized) {
		t.Fatal("expected denormalized vector to be within bounds")
	}
}

// TestParamCodecEncodesDecayLogarithmically guards the log encoding: a decay
// constant is a ratio-scale quantity, so equal ratios must be equal distances
// in the encoded vector.
func TestParamCodecEncodesDecayLogarithmically(t *testing.T) {
	params := validBarParams()

	// In ascending frequency order, which is the order the codec encodes in.
	params.Modes[3].DecayMs = 10
	params.Modes[2].DecayMs = 20
	params.Modes[0].DecayMs = 100
	params.Modes[1].DecayMs = 200

	codec, err := NewParamCodec(&params)
	if err != nil {
		t.Fatalf("NewParamCodec failed: %v", err)
	}

	encoded := mustEncode(t, codec, &params)
	firstStep := encoded[2+1*3+2] - encoded[2+0*3+2]
	secondStep := encoded[2+3*3+2] - encoded[2+2*3+2]

	if math.Abs(firstStep-secondStep) > 1e-12 {
		t.Fatalf("equal decay ratios must be equal encoded steps: %g vs %g", firstStep, secondStep)
	}
}

func TestDecodeParamsRejectsWrongLength(t *testing.T) {
	params := validBarParams()

	codec, err := NewParamCodec(&params)
	if err != nil {
		t.Fatalf("NewParamCodec failed: %v", err)
	}

	if _, err := codec.DecodeParams([]float64{1, 2, 3}); err == nil {
		t.Fatal("expected DecodeParams to reject wrong vector length")
	}
}

func TestTopLevelDecodeParamsUsesTemplateMetadata(t *testing.T) {
	params := validBarParams()
	params.Chebyshev.Enabled = false

	encoded, err := EncodeParams(&params)
	if err != nil {
		t.Fatalf("EncodeParams failed: %v", err)
	}

	decoded, err := DecodeParams(encoded, &params)
	if err != nil {
		t.Fatalf("DecodeParams failed: %v", err)
	}

	if decoded.Chebyshev.Enabled {
		t.Fatal("expected decoded params to preserve chebyshev enabled flag from template")
	}
}

func mustEncode(t *testing.T, codec *ParamCodec, params *model.BarParams) []float64 {
	t.Helper()

	encoded, err := codec.EncodeParams(params)
	if err != nil {
		t.Fatalf("EncodeParams failed: %v", err)
	}

	return encoded
}

func validBarParams() model.BarParams {
	return model.BarParams{
		InputMix:        0.472433640370972,
		FilterFrequency: 522.935295651445,
		BaseFrequency:   440.0,
		Modes: []model.ModeParams{
			{Amplitude: 0.885860562324524, Frequency: 1756.64123535156, DecayMs: 188.223281860352},
			{Amplitude: 1.99459731578827, Frequency: 4768.10693359375, DecayMs: 1.60327112674713},
			{Amplitude: -0.464719623327255, Frequency: 38.241283416748, DecayMs: 5.55945539474487},
			{Amplitude: 0.363913357257843, Frequency: 32.6347961425781, DecayMs: 8.6815824508667},
		},
		Chebyshev: model.ChebyshevParams{
			Enabled:       true,
			HarmonicGains: []float64{1.0, 0.5, 0.3, 0.2},
		},
	}
}

func assertClose(t *testing.T, got, want, tol float64, label string) {
	t.Helper()

	if math.Abs(got-want) > tol {
		t.Fatalf("%s mismatch: got %.12f want %.12f", label, got, want)
	}
}

func TestNewParamCodecWithStrictBoundsDoesNotWiden(t *testing.T) {
	params := validBarParams()
	params.Modes[0].Amplitude = 1.99

	bounds := DefaultParamBounds
	bounds.Amplitude = Range{Min: -1, Max: 1}

	strict, err := NewParamCodecWithStrictBounds(&params, bounds)
	if err != nil {
		t.Fatalf("NewParamCodecWithStrictBounds failed: %v", err)
	}

	amplitude := strict.EncodedBounds().Ranges[2]
	if amplitude.Min != -1 || amplitude.Max != 1 {
		t.Fatalf("strict bounds were widened to %+v", amplitude)
	}

	lenient, err := NewParamCodecWithBounds(&params, bounds)
	if err != nil {
		t.Fatalf("NewParamCodecWithBounds failed: %v", err)
	}

	if lenient.EncodedBounds().Ranges[2].Max <= 1 {
		t.Fatal("expected the default constructor to keep widening bounds")
	}
}

// TestDecodeParamsPreservesChebyshevStage guards the shaper placement: it is
// template metadata, not a search dimension, so dropping it would render every
// evaluation of an output-stage preset through the excitation-stage chain.
func TestDecodeParamsPreservesChebyshevStage(t *testing.T) {
	params := validBarParams()
	params.Chebyshev.Stage = model.ChebyshevStageOutput

	codec, err := NewParamCodec(&params)
	if err != nil {
		t.Fatalf("NewParamCodec failed: %v", err)
	}

	decoded, err := codec.DecodeParams(mustEncode(t, codec, &params))
	if err != nil {
		t.Fatalf("DecodeParams failed: %v", err)
	}

	if decoded.Chebyshev.Stage != model.ChebyshevStageOutput {
		t.Fatalf("chebyshev stage = %q, want %q", decoded.Chebyshev.Stage, model.ChebyshevStageOutput)
	}
}

// TestDefaultBoundsUseTheAuthoringCeiling guards the half of the decay split
// that must not move. Raising model.DecayMsValidationMax widens what a preset
// file may contain once transposed; it must not widen what a fit searches,
// which is the model's own search box and, per fit, no more than the note
// the template is authored at may carry.
func TestDefaultBoundsUseTheAuthoringCeiling(t *testing.T) {
	if got := DefaultParamBounds.DecayMs.Max; got != model.DecayMsSearchMax {
		t.Fatalf("DefaultParamBounds.DecayMs.Max = %g, want the search bound %g",
			got, model.DecayMsSearchMax)
	}

	if DefaultParamBounds.DecayMs.Max >= model.DecayMsValidationMax {
		t.Fatalf("the search box reaches the validation ceiling %g; it must stay the narrower of the two",
			model.DecayMsValidationMax)
	}
}

func TestParamCodecBlockGroupsPartitionEveryDimension(t *testing.T) {
	params := validBarParams()

	codec, err := NewParamCodec(&params)
	if err != nil {
		t.Fatalf("NewParamCodec failed: %v", err)
	}

	groups := codec.BlockGroups()
	if len(groups) != codec.ModeCount()+1 {
		t.Fatalf("expected one group per mode plus one for the scalars, got %d", len(groups))
	}

	seen := make([]bool, codec.Dimension())

	for groupIndex, group := range groups {
		if len(group) == 0 {
			t.Fatalf("group %d is empty", groupIndex)
		}

		for _, dimension := range group {
			if dimension < 0 || dimension >= codec.Dimension() {
				t.Fatalf("group %d holds dimension %d outside [0, %d)", groupIndex, dimension, codec.Dimension())
			}

			if seen[dimension] {
				t.Fatalf("dimension %d appears in more than one group", dimension)
			}

			seen[dimension] = true
		}
	}

	for dimension, present := range seen {
		if !present {
			t.Fatalf("dimension %d is in no group", dimension)
		}
	}
}

// TestParamCodecBlockGroupsKeepAModeTogether is the point of the partition: a
// mode's amplitude, frequency and decay are what a dense block has to
// correlate, so they must never be split across two blocks.
func TestParamCodecBlockGroupsKeepAModeTogether(t *testing.T) {
	params := validBarParams()

	codec, err := NewParamCodec(&params)
	if err != nil {
		t.Fatalf("NewParamCodec failed: %v", err)
	}

	groups := codec.BlockGroups()
	names := codec.DimensionNames()

	for mode := range codec.ModeCount() {
		base := scalarParameterCount + mode*3
		triple := []int{base, base + 1, base + 2}

		home := -1

		for groupIndex, group := range groups {
			for _, dimension := range group {
				if dimension == base {
					home = groupIndex
				}
			}
		}

		if home < 0 {
			t.Fatalf("mode %d has no group", mode)
		}

		if len(groups[home]) != 3 {
			t.Fatalf("mode %d shares its group with %v", mode, groups[home])
		}

		for i, dimension := range triple {
			if groups[home][i] != dimension {
				t.Fatalf("mode %d group %v does not hold %s at %d",
					mode, groups[home], names[dimension], dimension)
			}
		}
	}
}
