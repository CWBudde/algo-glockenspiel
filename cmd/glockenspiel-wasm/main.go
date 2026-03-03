//go:build js && wasm

package main

import (
	"fmt"
	"syscall/js"
	"unsafe"

	embeddedassets "github.com/cwbudde/glockenspiel/assets"
	"github.com/cwbudde/glockenspiel/internal/synth"
)

const (
	webNoteDurationSeconds = 4.0
	webDecayDBFS           = -72.0
)

var (
	globalSynth *synth.Synthesizer
	renderedBuf []float32
	noteCache   map[string][]float32
)

func main() {
	done := make(chan struct{})

	js.Global().Set("wasmInit", js.FuncOf(wasmInit))
	js.Global().Set("wasmRenderNote", js.FuncOf(wasmRenderNote))
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

	globalSynth = s
	renderedBuf = make([]float32, 0, sampleRate*2)
	noteCache = make(map[string][]float32)

	return nil
}

func wasmRenderNote(_ js.Value, args []js.Value) interface{} {
	result := map[string]interface{}{
		"ptr":    float64(0),
		"length": 0,
	}
	if globalSynth == nil || len(args) < 2 {
		return result
	}

	note := args[0].Int()
	velocity := args[1].Int()
	cacheKey := fmt.Sprintf("%d:%d", note, velocity)

	if cached, ok := noteCache[cacheKey]; ok && len(cached) > 0 {
		result["ptr"] = float64(uintptr(unsafe.Pointer(&cached[0])))
		result["length"] = len(cached)
		return result
	}

	audio := globalSynth.RenderNoteWithOptions(note, velocity, webNoteDurationSeconds, synth.RenderOptions{
		AutoStop:  true,
		DecayDBFS: webDecayDBFS,
	})
	if len(audio) == 0 {
		renderedBuf = renderedBuf[:0]
		return result
	}

	renderedBuf = append(renderedBuf[:0], audio...)
	cached := append([]float32(nil), renderedBuf...)
	noteCache[cacheKey] = cached
	result["ptr"] = float64(uintptr(unsafe.Pointer(&cached[0])))
	result["length"] = len(cached)

	return result
}

func wasmGetMemoryBuffer(_ js.Value, _ []js.Value) interface{} {
	mem := js.Global().Get("__algoGlockenspielWasmMemory")
	if !mem.Truthy() {
		return js.Null()
	}

	return mem.Get("buffer")
}
