package optimizer

import (
	"math"
	"testing"

	"github.com/cwbudde/glockenspiel/internal/model"
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

	for i := range model.NumModes {
		assertClose(t, decoded.Modes[i].Amplitude, params.Modes[i].Amplitude, 1e-12, "mode amplitude")
		assertClose(t, decoded.Modes[i].Frequency, params.Modes[i].Frequency, 1e-9, "mode frequency")
		assertClose(t, decoded.Modes[i].DecayMs, params.Modes[i].DecayMs, 1e-12, "mode decay")
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

func TestBoundsMirror(t *testing.T) {
	params := validBarParams()
	codec, err := NewParamCodec(&params)
	if err != nil {
		t.Fatalf("NewParamCodec failed: %v", err)
	}

	bounds := codec.EncodedBounds()
	input := mustEncode(t, codec, &params)
	input[0] = bounds.Ranges[0].Max + 0.25
	input[1] = bounds.Ranges[1].Min - 0.5
	input[len(input)-1] = bounds.Ranges[len(bounds.Ranges)-1].Max + 0.75

	mirrored, err := bounds.Mirror(input)
	if err != nil {
		t.Fatalf("Mirror failed: %v", err)
	}
	if !bounds.Contains(mirrored) {
		t.Fatal("expected mirrored vector to be within bounds")
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
		Modes: [model.NumModes]model.ModeParams{
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
