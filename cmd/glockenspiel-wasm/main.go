//go:build js && wasm

package main

import (
	"syscall/js"
	"unsafe"

	embeddedassets "github.com/cwbudde/glockenspiel/assets"
	"github.com/cwbudde/glockenspiel/internal/synth"
)

const (
	// namespaceName is the single global this module publishes. Everything the
	// front end may call hangs off it, so the module owns one name on
	// globalThis instead of five. Five bare globals are five chances to
	// collide with a page script, a browser extension or a future bundler that
	// also thinks "wasmInit" is a reasonable thing to call its own, and such a
	// collision fails at the worst possible moment: inside the audio callback,
	// where the wrong function is simply called and nothing reports it.
	namespaceName = "glockenspielWasm"

	// readyCallbackName is the hook the page installs before it starts the Go
	// runtime. Invoking it is the happens-after edge that web/main.js used to
	// approximate with a 50 ms sleep: it fires from Go once every export is in
	// place, so there is no window in which the namespace exists but is only
	// half-populated.
	readyCallbackName = "__glockenspielWasmReady"
)

var globalEngine *synth.RealtimeEngine

func main() {
	done := make(chan struct{})

	api := js.Global().Get("Object").New()
	api.Set("init", js.FuncOf(wasmInit))
	api.Set("noteOn", js.FuncOf(wasmNoteOn))
	api.Set("setMasterGain", js.FuncOf(wasmSetMasterGain))
	api.Set("processBlock", js.FuncOf(wasmProcessBlock))

	// The js.Func values above are never released, and no Go reference to them
	// is kept. Both are deliberate, and neither leaves the exports open to the
	// garbage collector.
	//
	// js.FuncOf stores the Go closure in a package-level map inside syscall/js,
	// keyed by an id that only Release deletes, so the callback stays reachable
	// from Go whether or not the caller holds the returned js.Func. The JS side
	// is likewise independent: _makeFuncWrapper in wasm_exec.js returns a
	// closure over that same id, not over the handle table, so it keeps working
	// after the js.Value that carried it is collected. Measured against Go
	// 1.26.5 with this exact pattern -- the func passed straight into Set and
	// dropped -- runtime.GC does fire finalizeRef and does return the handle id
	// to _idPool, and the exports still answer correctly afterwards, including
	// once that id has been recycled for an unrelated value.
	//
	// Not releasing them is the separate half: they live exactly as long as the
	// module, and main blocks below rather than returning, because a returned
	// main tears the Go runtime down and every exported function with it.
	js.Global().Set(namespaceName, api)
	signalReady(api)

	println("WASM glockenspiel module loaded")
	<-done
}

// signalReady tells the page that the bridge is complete.
//
// The namespace is published first and announced second, so a callback that
// prefers to read globalThis over its own argument sees the same object either
// way. A missing callback is not an error: the module has to stay usable from a
// plain console session or from a harness that never installed one.
func signalReady(api js.Value) {
	ready := js.Global().Get(readyCallbackName)
	if ready.Type() != js.TypeFunction {
		return
	}

	ready.Invoke(api)
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
