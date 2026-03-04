//go:build linux && cgo && vst3go

package vst3

import (
	"fmt"
	"math"
	"sort"

	"github.com/cwbudde/glockenspiel/internal/model"
	frameworkbus "github.com/cwbudde/vst3go/pkg/framework/bus"
	frameworkparam "github.com/cwbudde/vst3go/pkg/framework/param"
	frameworkprocess "github.com/cwbudde/vst3go/pkg/framework/process"
	frameworkplugin "github.com/cwbudde/vst3go/pkg/framework/plugin"
	"github.com/cwbudde/vst3go/pkg/midi"
)

const (
	defaultPluginNote         = 69
	defaultPluginVelocity     = 100
	defaultPolyphony          = 16
	pluginDecayThresholdDBFS  = -90.0
	pluginQuietBlockThreshold = 8
)

// Processor hosts persistent resonant bars keyed by note/channel. Note-on
// retriggers a bar by resetting its state and applying a new excitation.
type Processor struct {
	*frameworkplugin.BaseProcessor

	active     bool
	snapshot   Snapshot
	voices     []activeVoice
	sampleRate float64
	polyphony  int
	mixBuffer  []float32
}

type activeVoice struct {
	note          uint8
	channel       uint8
	age           uint64
	bar           *model.Bar
	strikeVelocity int
	quietBlocks   int
}

type timedEvent struct {
	offset int
	event  midi.Event
}

// NewProcessor creates a new minimal instrument processor.
func NewProcessor() *Processor {
	p := &Processor{
		BaseProcessor: frameworkplugin.NewBaseProcessor(frameworkbus.NewGenerator()),
		snapshot:      DefaultSnapshot(),
		polyphony:     defaultPolyphony,
	}
	registerParameters(p.Parameters(), p.snapshot)
	p.OnInitialize(func(sampleRate float64, maxBlockSize int32) error {
		p.sampleRate = sampleRate
		if maxBlockSize > 0 {
			p.mixBuffer = make([]float32, maxBlockSize)
		}
		return p.rebuildVoices()
	})
	p.OnSetActive(func(active bool) error {
		p.active = active
		if active {
			return p.rebuildVoices()
		}
		p.voices = nil
		return nil
	})
	return p
}

// ProcessAudio handles MIDI note events and mixes active voices into the stereo output.
func (p *Processor) ProcessAudio(ctx *frameworkprocess.Context) {
	ctx.Clear()
	if !p.active {
		ctx.ClearInputEvents()
		return
	}

	if changed, err := p.syncSnapshotFromParams(); err != nil {
		ctx.ClearInputEvents()
		return
	} else if changed {
		if err := p.rebuildVoices(); err != nil {
			ctx.ClearInputEvents()
			return
		}
	}

	p.processBlock(ctx)
	ctx.ClearInputEvents()
}

func (p *Processor) processBlock(ctx *frameworkprocess.Context) {
	events := collectTimedEvents(ctx.GetAllInputEvents(), ctx.NumSamples())
	start := 0
	for _, te := range events {
		if te.offset > start {
			p.renderSpan(ctx, start, te.offset)
			start = te.offset
		}
		p.handleEvent(te.event)
	}

	if start < ctx.NumSamples() {
		p.renderSpan(ctx, start, ctx.NumSamples())
	}

	p.compactVoices()
}

func collectTimedEvents(events []midi.Event, numSamples int) []timedEvent {
	out := make([]timedEvent, 0, len(events))
	for _, event := range events {
		offset := int(event.SampleOffset())
		if offset < 0 {
			offset = 0
		}
		if offset > numSamples {
			offset = numSamples
		}
		out = append(out, timedEvent{offset: offset, event: event})
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].offset < out[j].offset
	})
	return out
}

func (p *Processor) handleEvent(event midi.Event) {
	switch e := event.(type) {
	case midi.NoteOnEvent:
		if e.Velocity == 0 {
			return
		}
		_ = p.startNote(e.NoteNumber, e.Velocity, e.Channel())
	case midi.ControlChangeEvent:
		switch e.Controller {
		case midi.CCAllNotesOff, midi.CCAllSoundOff:
			p.stopAllVoices()
		}
	}
}

func (p *Processor) startNote(note, velocity, channel uint8) error {
	for i := range p.voices {
		if p.voices[i].note == note && p.voices[i].channel == channel {
			p.voices[i].bar.Reset()
			p.voices[i].strikeVelocity = int(velocity)
			p.voices[i].quietBlocks = 0
			p.voices[i].age = nextVoiceAge()
			return nil
		}
	}

	if p.polyphony <= 0 {
		p.polyphony = defaultPolyphony
	}

	if len(p.voices) >= p.polyphony {
		oldest := 0
		for i := 1; i < len(p.voices); i++ {
			if p.voices[i].age < p.voices[oldest].age {
				oldest = i
			}
		}
		copy(p.voices[oldest:], p.voices[oldest+1:])
		p.voices = p.voices[:len(p.voices)-1]
	}

	params := scaledParamsForNote(p.snapshot.ToBarParams(), int(note), defaultPluginNote)
	bar, err := model.NewBar(&params, int(p.sampleRate))
	if err != nil {
		return err
	}
	bar.Reset()

	p.voices = append(p.voices, activeVoice{
		note:           note,
		channel:        channel,
		age:            nextVoiceAge(),
		bar:            bar,
		strikeVelocity: int(velocity),
	})
	return nil
}

func (p *Processor) stopAllVoices() {
	p.voices = nil
}

var voiceAgeCounter uint64

func nextVoiceAge() uint64 {
	voiceAgeCounter++
	return voiceAgeCounter
}

func (p *Processor) renderSpan(ctx *frameworkprocess.Context, start, end int) {
	if end <= start || len(p.voices) == 0 || len(ctx.Output) == 0 {
		return
	}

	segmentLen := end - start
	if len(p.mixBuffer) < segmentLen {
		p.mixBuffer = make([]float32, segmentLen)
	}

	threshold := math.Pow(10, pluginDecayThresholdDBFS/20)
	for i := range p.voices {
		voice := &p.voices[i]
		if voice.bar == nil {
			continue
		}
		buf := p.mixBuffer[:segmentLen]
		var rendered []float32
		if voice.strikeVelocity > 0 {
			rendered = voice.bar.Synthesize(voice.strikeVelocity, segmentLen)
			voice.strikeVelocity = 0
		} else {
			clear(buf)
			rendered = voice.bar.ProcessExcitation(buf)
		}

		for ch := range ctx.Output {
			out := ctx.Output[ch][start:end]
			for j := 0; j < len(rendered); j++ {
				out[j] += rendered[j]
			}
		}

		if shouldRetire(rendered, threshold) {
			voice.quietBlocks++
		} else {
			voice.quietBlocks = 0
		}
	}
}

func (p *Processor) compactVoices() {
	dst := p.voices[:0]
	for _, voice := range p.voices {
		if voice.bar != nil && voice.quietBlocks < pluginQuietBlockThreshold {
			dst = append(dst, voice)
		}
	}
	p.voices = dst
}

func registerParameters(registry *frameworkparam.Registry, defaults Snapshot) {
	for _, spec := range ParameterSpecs() {
		builder := frameworkparam.New(uint32(spec.ID), spec.Name).
			ShortName(spec.Name).
			Range(spec.Min, spec.Max).
			Default(spec.Default).
			Unit(spec.Unit)
		if spec.Min == 0 && spec.Max == 1 && spec.Key == "chebyshev_enabled" {
			builder = builder.Toggle()
		}
		param := builder.Build()
		_ = registry.Add(param)
	}

	applySnapshotToRegistry(registry, defaults)
}

func applySnapshotToRegistry(registry *frameworkparam.Registry, snapshot Snapshot) {
	for _, spec := range ParameterSpecs() {
		param := registry.Get(uint32(spec.ID))
		if param == nil {
			continue
		}
		param.SetPlainValue(snapshotValue(snapshot, spec.ID))
	}
}

func snapshotValue(snapshot Snapshot, id ParameterID) float64 {
	switch id {
	case ParamInputMix:
		return snapshot.InputMix
	case ParamFilterFrequency:
		return snapshot.FilterFrequency
	case ParamBaseFrequency:
		return snapshot.BaseFrequency
	case ParamChebyshevEnabled:
		if snapshot.ChebyshevEnabled {
			return 1
		}
		return 0
	case ParamChebyshevGain1:
		return snapshot.ChebyshevGains[0]
	case ParamChebyshevGain2:
		return snapshot.ChebyshevGains[1]
	case ParamChebyshevGain3:
		return snapshot.ChebyshevGains[2]
	case ParamChebyshevGain4:
		return snapshot.ChebyshevGains[3]
	case ParamMode1Amplitude:
		return snapshot.ModeAmplitude[0]
	case ParamMode1Frequency:
		return snapshot.ModeFrequency[0]
	case ParamMode1DecayMs:
		return snapshot.ModeDecayMs[0]
	case ParamMode2Amplitude:
		return snapshot.ModeAmplitude[1]
	case ParamMode2Frequency:
		return snapshot.ModeFrequency[1]
	case ParamMode2DecayMs:
		return snapshot.ModeDecayMs[1]
	case ParamMode3Amplitude:
		return snapshot.ModeAmplitude[2]
	case ParamMode3Frequency:
		return snapshot.ModeFrequency[2]
	case ParamMode3DecayMs:
		return snapshot.ModeDecayMs[2]
	case ParamMode4Amplitude:
		return snapshot.ModeAmplitude[3]
	case ParamMode4Frequency:
		return snapshot.ModeFrequency[3]
	case ParamMode4DecayMs:
		return snapshot.ModeDecayMs[3]
	default:
		return 0
	}
}

func (p *Processor) syncSnapshotFromParams() (bool, error) {
	registry := p.Parameters()
	if registry == nil {
		return false, fmt.Errorf("parameter registry is nil")
	}

	next := Snapshot{
		InputMix:         registry.Get(uint32(ParamInputMix)).GetPlainValue(),
		FilterFrequency:  registry.Get(uint32(ParamFilterFrequency)).GetPlainValue(),
		BaseFrequency:    registry.Get(uint32(ParamBaseFrequency)).GetPlainValue(),
		ChebyshevEnabled: registry.Get(uint32(ParamChebyshevEnabled)).GetPlainValue() >= 0.5,
	}
	next.ChebyshevGains[0] = registry.Get(uint32(ParamChebyshevGain1)).GetPlainValue()
	next.ChebyshevGains[1] = registry.Get(uint32(ParamChebyshevGain2)).GetPlainValue()
	next.ChebyshevGains[2] = registry.Get(uint32(ParamChebyshevGain3)).GetPlainValue()
	next.ChebyshevGains[3] = registry.Get(uint32(ParamChebyshevGain4)).GetPlainValue()
	next.ModeAmplitude[0] = registry.Get(uint32(ParamMode1Amplitude)).GetPlainValue()
	next.ModeAmplitude[1] = registry.Get(uint32(ParamMode2Amplitude)).GetPlainValue()
	next.ModeAmplitude[2] = registry.Get(uint32(ParamMode3Amplitude)).GetPlainValue()
	next.ModeAmplitude[3] = registry.Get(uint32(ParamMode4Amplitude)).GetPlainValue()
	next.ModeFrequency[0] = registry.Get(uint32(ParamMode1Frequency)).GetPlainValue()
	next.ModeFrequency[1] = registry.Get(uint32(ParamMode2Frequency)).GetPlainValue()
	next.ModeFrequency[2] = registry.Get(uint32(ParamMode3Frequency)).GetPlainValue()
	next.ModeFrequency[3] = registry.Get(uint32(ParamMode4Frequency)).GetPlainValue()
	next.ModeDecayMs[0] = registry.Get(uint32(ParamMode1DecayMs)).GetPlainValue()
	next.ModeDecayMs[1] = registry.Get(uint32(ParamMode2DecayMs)).GetPlainValue()
	next.ModeDecayMs[2] = registry.Get(uint32(ParamMode3DecayMs)).GetPlainValue()
	next.ModeDecayMs[3] = registry.Get(uint32(ParamMode4DecayMs)).GetPlainValue()

	if next == p.snapshot {
		return false, nil
	}

	p.snapshot = next
	return true, nil
}

func (p *Processor) rebuildVoices() error {
	for i := range p.voices {
		params := scaledParamsForNote(p.snapshot.ToBarParams(), int(p.voices[i].note), defaultPluginNote)
		bar, err := model.NewBar(&params, int(p.sampleRate))
		if err != nil {
			return err
		}
		p.voices[i].bar = bar
		p.voices[i].quietBlocks = 0
	}
	return nil
}

func scaledParamsForNote(params model.BarParams, note, baseNote int) model.BarParams {
	ratio := math.Pow(2, float64(note-baseNote)/12)
	params.BaseFrequency *= ratio
	for i := 0; i < model.NumModes; i++ {
		params.Modes[i].Frequency *= ratio
		if ratio > 0 {
			params.Modes[i].DecayMs /= ratio
		}
	}
	return params
}

func shouldRetire(block []float32, threshold float64) bool {
	if len(block) == 0 {
		return true
	}
	sum := 0.0
	for _, sample := range block {
		v := float64(sample)
		sum += v * v
	}
	rms := math.Sqrt(sum / float64(len(block)))
	return rms < threshold
}
