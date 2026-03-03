//go:build js && wasm

package main

import (
	"fmt"
	"math"
	"syscall/js"
	"unsafe"

	embeddedassets "github.com/cwbudde/glockenspiel/assets"
	"github.com/cwbudde/glockenspiel/internal/synth"
)

const (
	webNoteDurationSeconds = 4.0
	webDecayDBFS           = -72.0
	webMinGain             = 0.1
	webMaxVoices           = 64
)

type voice struct {
	samples []float32
	pos     int
	left    float32
	right   float32
}

type engine struct {
	synth      *synth.Synthesizer
	noteCache  map[string][]float32
	voices     []voice
	mixBuffer  []float32
	masterGain float32
}

var globalEngine *engine

func main() {
	done := make(chan struct{})

	js.Global().Set("wasmInit", js.FuncOf(wasmInit))
	js.Global().Set("wasmNoteOn", js.FuncOf(wasmNoteOn))
	js.Global().Set("wasmSetMasterGain", js.FuncOf(wasmSetMasterGain))
	js.Global().Set("wasmProcessBlock", js.FuncOf(wasmProcessBlock))
	js.Global().Set("wasmPrewarmNotes", js.FuncOf(wasmPrewarmNotes))
	js.Global().Set("wasmGetMemoryBuffer", js.FuncOf(wasmGetMemoryBuffer))

	println("WASM glockenspiel module loaded")
	<-done
}

func wasmInit(_ js.Value, args []js.Value) interface{} {
	if len(args) < 1 {
		return "missing sample rate"
	}

	sampleRate := args[0].Int()
	p, err := embeddedassets.DefaultPreset()
	if err != nil {
		return err.Error()
	}

	s, err := synth.NewSynthesizer(p, sampleRate)
	if err != nil {
		return err.Error()
	}

	globalEngine = &engine{
		synth:      s,
		noteCache:  make(map[string][]float32),
		voices:     make([]voice, 0, 16),
		mixBuffer:  make([]float32, 128*2),
		masterGain: 0.7,
	}

	return nil
}

func wasmNoteOn(_ js.Value, args []js.Value) interface{} {
	if globalEngine == nil || len(args) < 2 {
		return nil
	}

	globalEngine.noteOn(args[0].Int(), args[1].Int())
	return nil
}

func wasmSetMasterGain(_ js.Value, args []js.Value) interface{} {
	if globalEngine == nil || len(args) < 1 {
		return nil
	}

	value := float32(args[0].Float())
	if value < webMinGain {
		value = webMinGain
	}
	if value > 1 {
		value = 1
	}

	globalEngine.masterGain = value
	return nil
}

func wasmProcessBlock(_ js.Value, args []js.Value) interface{} {
	if globalEngine == nil || len(args) < 1 {
		return 0
	}

	frames := args[0].Int()
	if frames <= 0 {
		return 0
	}

	ptr := globalEngine.processBlock(frames)
	if ptr == nil {
		return 0
	}

	return float64(uintptr(unsafe.Pointer(ptr)))
}

func wasmPrewarmNotes(_ js.Value, args []js.Value) interface{} {
	if globalEngine == nil || len(args) < 3 {
		return nil
	}

	startNote := args[0].Int()
	count := args[1].Int()
	velocity := args[2].Int()
	if count <= 0 {
		return nil
	}

	for i := 0; i < count; i++ {
		_, _ = globalEngine.cachedNote(startNote+i, velocity)
	}

	return nil
}

func wasmGetMemoryBuffer(_ js.Value, _ []js.Value) interface{} {
	mem := js.Global().Get("__algoGlockenspielWasmMemory")
	if !mem.Truthy() {
		return js.Null()
	}

	return mem.Get("buffer")
}

func (e *engine) noteOn(note, velocity int) {
	samples, ok := e.cachedNote(note, velocity)
	if !ok || len(samples) == 0 {
		return
	}

	if len(e.voices) >= webMaxVoices {
		copy(e.voices[0:], e.voices[1:])
		e.voices = e.voices[:len(e.voices)-1]
	}

	left, right := gainsForNote(note, e.masterGain)
	e.voices = append(e.voices, voice{
		samples: samples,
		left:    left,
		right:   right,
	})
}

func (e *engine) cachedNote(note, velocity int) ([]float32, bool) {
	cacheKey := fmt.Sprintf("%d:%d", note, velocity)
	if cached, ok := e.noteCache[cacheKey]; ok {
		return cached, true
	}

	audio := e.synth.RenderNoteWithOptions(note, velocity, webNoteDurationSeconds, synth.RenderOptions{
		AutoStop:  true,
		DecayDBFS: webDecayDBFS,
	})
	if len(audio) == 0 {
		return nil, false
	}

	cached := append([]float32(nil), audio...)
	e.noteCache[cacheKey] = cached
	return cached, true
}

func (e *engine) processBlock(frames int) *float32 {
	required := frames * 2
	if len(e.mixBuffer) < required {
		e.mixBuffer = make([]float32, required)
	}

	buf := e.mixBuffer[:required]
	clear(buf)

	writeIndex := 0
	for _, v := range e.voices {
		remaining := len(v.samples) - v.pos
		if remaining <= 0 {
			continue
		}

		n := frames
		if remaining < n {
			n = remaining
		}

		for i := 0; i < n; i++ {
			sample := v.samples[v.pos+i]
			buf[i*2] += sample * v.left
			buf[i*2+1] += sample * v.right
		}

		v.pos += n
		if v.pos < len(v.samples) {
			e.voices[writeIndex] = v
			writeIndex++
		}
	}
	e.voices = e.voices[:writeIndex]

	for i := range buf {
		buf[i] = hardClip(buf[i])
	}

	return &buf[0]
}

func gainsForNote(note int, gain float32) (float32, float32) {
	const (
		firstNote = 72
		semitones = 24
	)

	relative := float32(note-firstNote) / float32(semitones-1)
	pan := relative*1.2 - 0.6

	left := gain * (1 - pan) * 0.5
	right := gain * (1 + pan) * 0.5
	return left, right
}

func hardClip(v float32) float32 {
	if v > 1 {
		return 1
	}
	if v < -1 {
		return -1
	}
	if math.Abs(float64(v)) < 1e-30 {
		return 0
	}
	return v
}
