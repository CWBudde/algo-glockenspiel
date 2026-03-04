package vst3

import "github.com/cwbudde/glockenspiel/internal/model"

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
	InputMix          float64
	FilterFrequency   float64
	BaseFrequency     float64
	ChebyshevEnabled  bool
	ChebyshevGains    [model.NumModes]float64
	ModeAmplitude     [model.NumModes]float64
	ModeFrequency     [model.NumModes]float64
	ModeDecayMs       [model.NumModes]float64
}

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
	{ID: ParamMode1DecayMs, Key: "mode_1_decay_ms", Name: "Mode 1 Decay", Unit: "ms", Min: model.DecayMsMin, Max: model.DecayMsMax, Default: 100},
	{ID: ParamMode2Amplitude, Key: "mode_2_amplitude", Name: "Mode 2 Amplitude", Unit: "", Min: model.AmplitudeMin, Max: model.AmplitudeMax, Default: 0.5},
	{ID: ParamMode2Frequency, Key: "mode_2_frequency", Name: "Mode 2 Frequency", Unit: "Hz", Min: model.FrequencyMinHz, Max: model.FrequencyMaxHz, Default: 880},
	{ID: ParamMode2DecayMs, Key: "mode_2_decay_ms", Name: "Mode 2 Decay", Unit: "ms", Min: model.DecayMsMin, Max: model.DecayMsMax, Default: 100},
	{ID: ParamMode3Amplitude, Key: "mode_3_amplitude", Name: "Mode 3 Amplitude", Unit: "", Min: model.AmplitudeMin, Max: model.AmplitudeMax, Default: 0.25},
	{ID: ParamMode3Frequency, Key: "mode_3_frequency", Name: "Mode 3 Frequency", Unit: "Hz", Min: model.FrequencyMinHz, Max: model.FrequencyMaxHz, Default: 1320},
	{ID: ParamMode3DecayMs, Key: "mode_3_decay_ms", Name: "Mode 3 Decay", Unit: "ms", Min: model.DecayMsMin, Max: model.DecayMsMax, Default: 100},
	{ID: ParamMode4Amplitude, Key: "mode_4_amplitude", Name: "Mode 4 Amplitude", Unit: "", Min: model.AmplitudeMin, Max: model.AmplitudeMax, Default: 0.125},
	{ID: ParamMode4Frequency, Key: "mode_4_frequency", Name: "Mode 4 Frequency", Unit: "Hz", Min: model.FrequencyMinHz, Max: model.FrequencyMaxHz, Default: 1760},
	{ID: ParamMode4DecayMs, Key: "mode_4_decay_ms", Name: "Mode 4 Decay", Unit: "ms", Min: model.DecayMsMin, Max: model.DecayMsMax, Default: 100},
}

var defaultSnapshot = Snapshot{
	InputMix:         0.472433640370972,
	FilterFrequency:  522.935295651445,
	BaseFrequency:    440.0,
	ChebyshevEnabled: true,
	ChebyshevGains:   [model.NumModes]float64{1.0, 0.5, 0.3, 0.2},
	ModeAmplitude:    [model.NumModes]float64{0.885860562324524, 1.99459731578827, -0.464719623327255, 0.363913357257843},
	ModeFrequency:    [model.NumModes]float64{1756.64123535156, 4768.10693359375, 38.241283416748, 32.6347961425781},
	ModeDecayMs:      [model.NumModes]float64{188.223281860352, 1.60327112674713, 5.55945539474487, 8.6815824508667},
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

	for i := 0; i < model.NumModes; i++ {
		snapshot.ModeAmplitude[i] = params.Modes[i].Amplitude
		snapshot.ModeFrequency[i] = params.Modes[i].Frequency
		snapshot.ModeDecayMs[i] = params.Modes[i].DecayMs
		if i < len(params.Chebyshev.HarmonicGains) {
			snapshot.ChebyshevGains[i] = params.Chebyshev.HarmonicGains[i]
		}
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
	params.Chebyshev.HarmonicGains = make([]float64, model.NumModes)

	for i := 0; i < model.NumModes; i++ {
		params.Chebyshev.HarmonicGains[i] = s.ChebyshevGains[i]
		params.Modes[i] = model.ModeParams{
			Amplitude: s.ModeAmplitude[i],
			Frequency: s.ModeFrequency[i],
			DecayMs:   s.ModeDecayMs[i],
		}
	}

	return params
}
