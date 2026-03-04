//go:build js && wasm

package main

import (
	"syscall/js"
	"unsafe"

	embeddedassets "github.com/cwbudde/glockenspiel/assets"
	"github.com/cwbudde/glockenspiel/internal/synth"
)

var globalEngine *synth.RealtimeEngine

func main() {
	done := make(chan struct{})

	js.Global().Set("wasmInit", js.FuncOf(wasmInit))
	js.Global().Set("wasmNoteOn", js.FuncOf(wasmNoteOn))
	js.Global().Set("wasmSetMasterGain", js.FuncOf(wasmSetMasterGain))
	js.Global().Set("wasmProcessBlock", js.FuncOf(wasmProcessBlock))
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

	globalEngine = synth.NewRealtimeEngine(s)
	return nil
}

func wasmNoteOn(_ js.Value, args []js.Value) interface{} {
	if globalEngine == nil || len(args) < 2 {
		return nil
	}

	globalEngine.NoteOn(args[0].Int(), args[1].Int())
	return nil
}

func wasmSetMasterGain(_ js.Value, args []js.Value) interface{} {
	if globalEngine == nil || len(args) < 1 {
		return nil
	}

	globalEngine.SetMasterGain(float32(args[0].Float()))
	return nil
}

func wasmProcessBlock(_ js.Value, args []js.Value) interface{} {
	if globalEngine == nil || len(args) < 1 {
		return 0
	}

	block := globalEngine.ProcessBlock(args[0].Int())
	if len(block) == 0 {
		return 0
	}

	return float64(uintptr(unsafe.Pointer(&block[0])))
}

func wasmGetMemoryBuffer(_ js.Value, _ []js.Value) interface{} {
	mem := js.Global().Get("__algoGlockenspielWasmMemory")
	if !mem.Truthy() {
		return js.Null()
	}

	return mem.Get("buffer")
}
