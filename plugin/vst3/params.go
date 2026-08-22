package vst3

import "github.com/cwbudde/glockenspiel/model"

const (
	// numModes and numChebyshevGains are the fixed sizes of this plugin's
	// parameter grid. The model no longer exports a fixed mode count -- the
	// oscillator bank sizes itself at runtime -- so the VST3 layer declares the
	// counts its automation IDs are frozen at. The two are independent: a bar
	// may carry any number of modes and any number of Chebyshev gains.
	numModes          = 4
	numChebyshevGains = 4
)

// ParameterID is the stable host-facing identifier for one automatable plugin parameter.
type ParameterID uint32

const (
	ParamInputMix ParameterID = iota
	ParamFilterFrequency
	ParamBaseFrequency
	ParamChebyshevEnabled
	ParamChebyshevGain1
	ParamChebyshevGain2
	ParamChebyshevGain3
	ParamChebyshevGain4
	ParamMode1Amplitude
	ParamMode1Frequency
	ParamMode1DecayMs
	ParamMode2Amplitude
	ParamMode2Frequency
	ParamMode2DecayMs
	ParamMode3Amplitude
	ParamMode3Frequency
	ParamMode3DecayMs
	ParamMode4Amplitude
	ParamMode4Frequency
	ParamMode4DecayMs
)

// ParameterSpec describes one plugin-facing parameter.
type ParameterSpec struct {
	ID      ParameterID
	Key     string
	Name    string
	Unit    string
	Min     float64
	Max     float64
	Default float64
}

// Snapshot is the VST-facing parameter state that the future processor/controller
// layer can exchange with the host.
type Snapshot struct {
	InputMix         float64
	FilterFrequency  float64
	BaseFrequency    float64
	ChebyshevEnabled bool
	ChebyshevGains   [numChebyshevGains]float64
	ModeAmplitude    [numModes]float64
	ModeFrequency    [numModes]float64
	ModeDecayMs      [numModes]float64
}

// parameterSpecs is the host-visible automation surface. Every range here is an
// authoring range, so the decay knobs take model.DecayMsSearchMax rather than
// the far wider model.DecayMsValidationMax: what reaches validation is the
// transposed value, which scaledParamsForNote in the processor divides by the
// note ratio, and stretching the knob to the validation ceiling would spend ten
// times the automation resolution on decays no one dials in by hand.
var parameterSpecs = []ParameterSpec{
	{ID: ParamInputMix, Key: "input_mix", Name: "Input Mix", Unit: "", Min: model.InputMixMin, Max: model.InputMixMax, Default: 0},
	{ID: ParamFilterFrequency, Key: "filter_frequency", Name: "Filter Frequency", Unit: "Hz", Min: model.FilterFrequencyMinHz, Max: model.FilterFrequencyMaxHz, Default: 8000},
	{ID: ParamBaseFrequency, Key: "base_frequency", Name: "Base Frequency", Unit: "Hz", Min: model.FrequencyMinHz, Max: model.FrequencyMaxHz, Default: 440},
	{ID: ParamChebyshevEnabled, Key: "chebyshev_enabled", Name: "Chebyshev Enabled", Unit: "", Min: 0, Max: 1, Default: 1},
	{ID: ParamChebyshevGain1, Key: "chebyshev_gain_1", Name: "Chebyshev Gain 1", Unit: "", Min: model.HarmonicGainMin, Max: model.HarmonicGainMax, Default: 1},
	{ID: ParamChebyshevGain2, Key: "chebyshev_gain_2", Name: "Chebyshev Gain 2", Unit: "", Min: model.HarmonicGainMin, Max: model.HarmonicGainMax, Default: 0},
	{ID: ParamChebyshevGain3, Key: "chebyshev_gain_3", Name: "Chebyshev Gain 3", Unit: "", Min: model.HarmonicGainMin, Max: model.HarmonicGainMax, Default: 0},
	{ID: ParamChebyshevGain4, Key: "chebyshev_gain_4", Name: "Chebyshev Gain 4", Unit: "", Min: model.HarmonicGainMin, Max: model.HarmonicGainMax, Default: 0},
	{ID: ParamMode1Amplitude, Key: "mode_1_amplitude", Name: "Mode 1 Amplitude", Unit: "", Min: model.AmplitudeMin, Max: model.AmplitudeMax, Default: 1},
	{ID: ParamMode1Frequency, Key: "mode_1_frequency", Name: "Mode 1 Frequency", Unit: "Hz", Min: model.FrequencyMinHz, Max: model.FrequencyMaxHz, Default: 440},
	{ID: ParamMode1DecayMs, Key: "mode_1_decay_ms", Name: "Mode 1 Decay", Unit: "ms", Min: model.DecayMsMin, Max: model.DecayMsSearchMax, Default: 100},
	{ID: ParamMode2Amplitude, Key: "mode_2_amplitude", Name: "Mode 2 Amplitude", Unit: "", Min: model.AmplitudeMin, Max: model.AmplitudeMax, Default: 0.5},
	{ID: ParamMode2Frequency, Key: "mode_2_frequency", Name: "Mode 2 Frequency", Unit: "Hz", Min: model.FrequencyMinHz, Max: model.FrequencyMaxHz, Default: 880},
	{ID: ParamMode2DecayMs, Key: "mode_2_decay_ms", Name: "Mode 2 Decay", Unit: "ms", Min: model.DecayMsMin, Max: model.DecayMsSearchMax, Default: 100},
	{ID: ParamMode3Amplitude, Key: "mode_3_amplitude", Name: "Mode 3 Amplitude", Unit: "", Min: model.AmplitudeMin, Max: model.AmplitudeMax, Default: 0.25},
	{ID: ParamMode3Frequency, Key: "mode_3_frequency", Name: "Mode 3 Frequency", Unit: "Hz", Min: model.FrequencyMinHz, Max: model.FrequencyMaxHz, Default: 1320},
	{ID: ParamMode3DecayMs, Key: "mode_3_decay_ms", Name: "Mode 3 Decay", Unit: "ms", Min: model.DecayMsMin, Max: model.DecayMsSearchMax, Default: 100},
	{ID: ParamMode4Amplitude, Key: "mode_4_amplitude", Name: "Mode 4 Amplitude", Unit: "", Min: model.AmplitudeMin, Max: model.AmplitudeMax, Default: 0.125},
	{ID: ParamMode4Frequency, Key: "mode_4_frequency", Name: "Mode 4 Frequency", Unit: "Hz", Min: model.FrequencyMinHz, Max: model.FrequencyMaxHz, Default: 1760},
	{ID: ParamMode4DecayMs, Key: "mode_4_decay_ms", Name: "Mode 4 Decay", Unit: "ms", Min: model.DecayMsMin, Max: model.DecayMsSearchMax, Default: 100},
}

// defaultSnapshot is assets/presets/default.json, transcribed.
//
// Transcribed rather than loaded, because this package depends on model and
// nothing else on purpose -- it is the surface Phase 6 splits into its own
// module, and reaching for assets would drag internal/preset along with it,
// which an external module cannot import at all. The cost of that choice is
// that the numbers can drift, and they had:
// TestDefaultSnapshotMatchesTheShippedPreset is what makes the drift a failing
// test rather than a quiet one.
//
// It had drifted twice before that test existed. 5d7af10 divided every mode
// amplitude by 8.72 and this copy kept the old values, so a plugin instance
// started 18.8 dB louder than the CLI; then the shaper fix removed the DC those
// values had been leaning on and the same copy rendered at -37.42 dBFS against
// the shipped preset's -3.19, a 34 dB gap in the other direction.
var defaultSnapshot = Snapshot{
	InputMix:         2.0,
	FilterFrequency:  1303.6960400974592,
	BaseFrequency:    440.0,
	ChebyshevEnabled: true,
	ChebyshevGains:   [numChebyshevGains]float64{1.3710558525404255, 0.0, 0.20036314305373643, 0.0},
	ModeAmplitude:    [numModes]float64{2.0, 2.0, 2.0, -2.0},
	ModeFrequency:    [numModes]float64{1756.5243235169935, 4516.145411643994, 1328.9984749886657, 1855.0239239312777},
	ModeDecayMs:      [numModes]float64{170.44361397312102, 3.4763848726009345, 0.5604835696794853, 1.7888034585370858},
}

// ParameterSpecs returns the stable parameter definitions for the first VST3 spike.
func ParameterSpecs() []ParameterSpec {
	return append([]ParameterSpec(nil), parameterSpecs...)
}

// DefaultSnapshot returns the current plugin default parameter state.
func DefaultSnapshot() Snapshot {
	return defaultSnapshot
}

// SnapshotFromBarParams projects model parameters into plugin-facing parameter state.
func SnapshotFromBarParams(params *model.BarParams) Snapshot {
	if params == nil {
		return Snapshot{}
	}

	snapshot := Snapshot{
		InputMix:         params.InputMix,
		FilterFrequency:  params.FilterFrequency,
		BaseFrequency:    params.BaseFrequency,
		ChebyshevEnabled: params.Chebyshev.Enabled,
	}

	for i := 0; i < numModes && i < len(params.Modes); i++ {
		snapshot.ModeAmplitude[i] = params.Modes[i].Amplitude
		snapshot.ModeFrequency[i] = params.Modes[i].Frequency
		snapshot.ModeDecayMs[i] = params.Modes[i].DecayMs
	}

	// The gain count is unrelated to the mode count, so it gets its own bound.
	for i := 0; i < numChebyshevGains && i < len(params.Chebyshev.HarmonicGains); i++ {
		snapshot.ChebyshevGains[i] = params.Chebyshev.HarmonicGains[i]
	}

	return snapshot
}

// ToBarParams projects plugin-facing parameter state back into model parameters.
func (s Snapshot) ToBarParams() model.BarParams {
	var params model.BarParams

	params.InputMix = s.InputMix
	params.FilterFrequency = s.FilterFrequency
	params.BaseFrequency = s.BaseFrequency
	params.Chebyshev.Enabled = s.ChebyshevEnabled
	params.Chebyshev.HarmonicGains = make([]float64, numChebyshevGains)
	params.Modes = make([]model.ModeParams, numModes)

	for i := range numChebyshevGains {
		params.Chebyshev.HarmonicGains[i] = s.ChebyshevGains[i]
	}

	for i := range numModes {
		params.Modes[i] = model.ModeParams{
			Amplitude: s.ModeAmplitude[i],
			Frequency: s.ModeFrequency[i],
			DecayMs:   s.ModeDecayMs[i],
		}
	}

	return params
}
