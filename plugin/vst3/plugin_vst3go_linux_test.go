//go:build linux && cgo && vst3go

package vst3

import (
	"testing"

	frameworkprocess "github.com/cwbudde/vst3go/pkg/framework/process"
	_ "github.com/cwbudde/vst3go/pkg/plugin/cbridge"
	"github.com/cwbudde/vst3go/pkg/midi"
)

func TestNewProcessorRegistersAllParameters(t *testing.T) {
	processor := NewProcessor()

	got := processor.GetParameters().Count()
	want := int32(len(ParameterSpecs()))
	if got != want {
		t.Fatalf("unexpected parameter count: got %d want %d", got, want)
	}
}

func TestPluginInfo(t *testing.T) {
	info := (Plugin{}).GetInfo()
	if info.ID == "" {
		t.Fatal("expected non-empty plugin id")
	}
	if info.Name != pluginName {
		t.Fatalf("unexpected plugin name: got %q want %q", info.Name, pluginName)
	}
}

func TestProcessAudioRendersMIDINoteOn(t *testing.T) {
	processor := NewProcessor()
	if err := processor.Initialize(48000, 512); err != nil {
		t.Fatalf("initialize processor: %v", err)
	}
	if err := processor.SetActive(true); err != nil {
		t.Fatalf("activate processor: %v", err)
	}

	ctx := frameworkprocess.NewContext(512, processor.GetParameters())
	ctx.Output = [][]float32{
		make([]float32, 512),
		make([]float32, 512),
	}
	ctx.AddInputEvent(midi.NoteOnEvent{
		BaseEvent:  midi.BaseEvent{EventChannel: 0, Offset: 0},
		NoteNumber: 69,
		Velocity:   100,
	})

	processor.ProcessAudio(ctx)

	nonZero := false
	for i := range ctx.Output[0] {
		if ctx.Output[0][i] != 0 || ctx.Output[1][i] != 0 {
			nonZero = true
			break
		}
	}
	if !nonZero {
		t.Fatal("expected processor to render non-zero output after activation")
	}
}

func TestProcessAudioNoteOffDoesNotStopDecay(t *testing.T) {
	processor := NewProcessor()
	if err := processor.Initialize(48000, 512); err != nil {
		t.Fatalf("initialize processor: %v", err)
	}
	if err := processor.SetActive(true); err != nil {
		t.Fatalf("activate processor: %v", err)
	}

	ctx := frameworkprocess.NewContext(256, processor.GetParameters())
	ctx.Output = [][]float32{
		make([]float32, 256),
		make([]float32, 256),
	}
	ctx.AddInputEvent(midi.NoteOnEvent{
		BaseEvent:  midi.BaseEvent{EventChannel: 0, Offset: 0},
		NoteNumber: 69,
		Velocity:   100,
	})
	processor.ProcessAudio(ctx)

	ctx = frameworkprocess.NewContext(256, processor.GetParameters())
	ctx.Output = [][]float32{
		make([]float32, 256),
		make([]float32, 256),
	}
	ctx.AddInputEvent(midi.NoteOffEvent{
		BaseEvent:  midi.BaseEvent{EventChannel: 0, Offset: 0},
		NoteNumber: 69,
		Velocity:   0,
	})
	processor.ProcessAudio(ctx)

	nonZero := false
	for i := range ctx.Output[0] {
		if ctx.Output[0][i] != 0 || ctx.Output[1][i] != 0 {
			nonZero = true
			break
		}
	}
	if !nonZero {
		t.Fatal("expected bar to keep decaying after note-off")
	}
}

func TestProcessAudioSupportsPolyphony(t *testing.T) {
	processor := NewProcessor()
	if err := processor.Initialize(48000, 512); err != nil {
		t.Fatalf("initialize processor: %v", err)
	}
	if err := processor.SetActive(true); err != nil {
		t.Fatalf("activate processor: %v", err)
	}

	ctxOne := frameworkprocess.NewContext(256, processor.GetParameters())
	ctxOne.Output = [][]float32{
		make([]float32, 256),
		make([]float32, 256),
	}
	ctxOne.AddInputEvent(midi.NoteOnEvent{
		BaseEvent:  midi.BaseEvent{EventChannel: 0, Offset: 0},
		NoteNumber: 69,
		Velocity:   100,
	})
	processor.ProcessAudio(ctxOne)

	maxOne := maxAbs(ctxOne.Output[0])

	processor = NewProcessor()
	if err := processor.Initialize(48000, 512); err != nil {
		t.Fatalf("initialize processor: %v", err)
	}
	if err := processor.SetActive(true); err != nil {
		t.Fatalf("activate processor: %v", err)
	}

	ctxTwo := frameworkprocess.NewContext(256, processor.GetParameters())
	ctxTwo.Output = [][]float32{
		make([]float32, 256),
		make([]float32, 256),
	}
	ctxTwo.AddInputEvent(midi.NoteOnEvent{
		BaseEvent:  midi.BaseEvent{EventChannel: 0, Offset: 0},
		NoteNumber: 69,
		Velocity:   100,
	})
	ctxTwo.AddInputEvent(midi.NoteOnEvent{
		BaseEvent:  midi.BaseEvent{EventChannel: 0, Offset: 0},
		NoteNumber: 72,
		Velocity:   100,
	})
	processor.ProcessAudio(ctxTwo)

	maxTwo := maxAbs(ctxTwo.Output[0])
	if maxTwo <= maxOne {
		t.Fatalf("expected polyphonic output to exceed single-note output: one=%f two=%f", maxOne, maxTwo)
	}
}

func TestProcessAudioRetriggerReusesPersistentVoice(t *testing.T) {
	processor := NewProcessor()
	if err := processor.Initialize(48000, 512); err != nil {
		t.Fatalf("initialize processor: %v", err)
	}
	if err := processor.SetActive(true); err != nil {
		t.Fatalf("activate processor: %v", err)
	}

	ctx := frameworkprocess.NewContext(128, processor.GetParameters())
	ctx.Output = [][]float32{
		make([]float32, 128),
		make([]float32, 128),
	}
	ctx.AddInputEvent(midi.NoteOnEvent{
		BaseEvent:  midi.BaseEvent{EventChannel: 0, Offset: 0},
		NoteNumber: 69,
		Velocity:   100,
	})
	processor.ProcessAudio(ctx)

	if len(processor.voices) != 1 {
		t.Fatalf("expected one active resonator after first note-on, got %d", len(processor.voices))
	}

	ctx = frameworkprocess.NewContext(128, processor.GetParameters())
	ctx.Output = [][]float32{
		make([]float32, 128),
		make([]float32, 128),
	}
	ctx.AddInputEvent(midi.NoteOnEvent{
		BaseEvent:  midi.BaseEvent{EventChannel: 0, Offset: 0},
		NoteNumber: 69,
		Velocity:   100,
	})
	processor.ProcessAudio(ctx)

	if len(processor.voices) != 1 {
		t.Fatalf("expected retriggered note to reuse the existing resonator, got %d voices", len(processor.voices))
	}
}

func maxAbs(samples []float32) float32 {
	var max float32
	for _, sample := range samples {
		if sample < 0 {
			sample = -sample
		}
		if sample > max {
			max = sample
		}
	}
	return max
}
