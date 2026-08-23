//go:build js && wasm

package main

import (
	"syscall/js"
	"unsafe"

	embeddedassets "github.com/cwbudde/algo-glockenspiel/assets"
	"github.com/cwbudde/algo-glockenspiel/internal/synth"
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
	// runtime. Invoking it is the happens-after edge that the front end used to
	// approximate with a 50 ms sleep: it fires from Go once every export is in
	// place, so there is no window in which the namespace exists but is only
	// half-populated.
	readyCallbackName = "__glockenspielWasmReady"
)

var globalEngine *synth.RealtimeEngine

// globalMasterGain remembers the gain the page last asked for.
//
// Switching presets builds a new engine, and a new engine starts at its own
// default gain. Without this the volume would silently jump back to whatever
// that default is every time someone changed the sound -- a state the UI has no
// way to observe and would not correct, because from its side nothing about the
// volume changed. Replaying the gain in Go rather than asking the worker to
// re-send it keeps the two commands independent: a setPreset that arrives while
// no setMasterGain has ever been sent still lands on the engine default.
var globalMasterGain = float32(-1)

// globalSampleRate is what init was given, kept so setPreset can rebuild at the
// same rate rather than making the page repeat itself.
var globalSampleRate int

func main() {
	done := make(chan struct{})

	api := js.Global().Get("Object").New()
	api.Set("init", js.FuncOf(wasmInit))
	api.Set("noteOn", js.FuncOf(wasmNoteOn))
	api.Set("setMasterGain", js.FuncOf(wasmSetMasterGain))
	api.Set("setPreset", js.FuncOf(wasmSetPreset))
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

// wasmInit builds the engine. The optional second argument names a built-in
// preset; an absent or empty one is the default, which is what every caller
// written before presets could be chosen still passes.
func wasmInit(_ js.Value, args []js.Value) interface{} {
	if len(args) < 1 {
		return "missing sample rate"
	}

	sampleRate := args[0].Int()

	id := ""
	if len(args) > 1 && args[1].Type() == js.TypeString {
		id = args[1].String()
	}

	globalSampleRate = sampleRate

	return buildEngine(id)
}

// wasmSetPreset swaps the sound the engine plays.
//
// It rebuilds the engine rather than reconfiguring the one that exists.
// RealtimeEngine has no preset API, and giving it one means retuning oscillator
// banks underneath a running audio callback for a change the user made once, by
// hand, in a menu. Rebuilding costs a calibration sweep -- 61 short renders --
// and cannot leave a half-swapped bar behind. Notes ringing at the moment of
// the switch stop, which is the honest reading of "this is a different bar now"
// and the same thing that happens when a sampler changes patch.
//
// There is no race with processBlock: the worker that owns this module is a
// single JS thread, so nothing else can be inside the engine while this runs.
func wasmSetPreset(_ js.Value, args []js.Value) interface{} {
	if len(args) < 1 {
		return "missing preset id"
	}

	if globalSampleRate <= 0 {
		return "engine is not initialised"
	}

	return buildEngine(args[0].String())
}

// buildEngine replaces globalEngine, or leaves it untouched and reports why.
//
// Leaving it untouched matters: an unknown id has to be a message on a silent
// instrument that still plays its old sound, not a null engine that swallows
// every later noteOn.
func buildEngine(id string) interface{} {
	p, err := embeddedassets.Preset(id)
	if err != nil {
		return err.Error()
	}

	s, err := synth.NewSynthesizer(p, globalSampleRate)
	if err != nil {
		return err.Error()
	}

	engine := synth.NewRealtimeEngine(s)
	if globalMasterGain >= 0 {
		engine.SetMasterGain(globalMasterGain)
	}

	globalEngine = engine

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

	globalMasterGain = float32(args[0].Float())
	globalEngine.SetMasterGain(globalMasterGain)

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
