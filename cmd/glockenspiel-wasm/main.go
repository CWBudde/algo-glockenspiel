//go:build js && wasm

package main

import (
	"syscall/js"
	"unsafe"

	embeddedassets "github.com/cwbudde/algo-glockenspiel/assets"
	"github.com/cwbudde/algo-glockenspiel/internal/preset"
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

// engines caches one engine per preset id.
//
// Building one is not cheap: NewRealtimeEngine measures the preset once per
// playable note to build its level trims, which is 44 ms natively for the
// shipped preset and **165 to 190 ms measured in the browser**. The audio
// transport carries four 128-frame buffers, about 11.6 ms at 44.1 kHz, so a
// rebuild on the worker that also feeds that transport outlasts the queue by
// more than an order of magnitude. Paying it once per preset instead of once
// per swap turns a stall on every change of sound into a stall the first time
// each sound is chosen -- which is also the only time the transport has to be
// told to expect the gap.
var engines = map[string]*synth.RealtimeEngine{}

// customPresets holds the preset documents the page handed over at runtime,
// keyed by the id it addresses them with.
//
// They are what makes an optimizer result playable without a rebuild of this
// binary: everything in assets is embedded at compile time, so a preset fitted
// in the Optimize tab has no id to be chosen by. The map lives for as long as
// the module does and no longer -- reloading the page is what clears it, which
// is the whole contract the front end offers for a fitted sound.
var customPresets = map[string]*preset.Preset{}

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

// globalReverbMix remembers the reverb the page last asked for, for the same
// reason globalMasterGain does: a rebuilt engine starts dry, and nothing on the
// page knows the room went away, so nothing would put it back.
var globalReverbMix = float32(-1)

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
	api.Set("addPreset", js.FuncOf(wasmAddPreset))
	api.Set("setReverb", js.FuncOf(wasmSetReverb))
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
	// Resolved before it is used as a cache key, so "" and "default" cannot
	// occupy two entries holding two engines for one sound.
	if id == "" {
		id = embeddedassets.DefaultID
	}

	p, ok := customPresets[id]
	if !ok {
		embedded, err := embeddedassets.Preset(id)
		if err != nil {
			return err.Error()
		}

		p = embedded
	}

	engine, cached := engines[id]
	if !cached {
		s, buildErr := synth.NewSynthesizer(p, globalSampleRate)
		if buildErr != nil {
			return buildErr.Error()
		}

		engine = synth.NewRealtimeEngine(s)
		engines[id] = engine
	}

	// A cached engine comes back holding whatever was ringing when it was set
	// aside, because retirement only happens in ProcessBlock and nothing has
	// been processing it. Silencing on the way in is what keeps a swap from
	// resuming a note the player cut off minutes ago.
	engine.Silence()

	if globalMasterGain >= 0 {
		engine.SetMasterGain(globalMasterGain)
	}

	if globalReverbMix >= 0 {
		engine.SetReverbMix(globalReverbMix)
	}

	globalEngine = engine

	return nil
}

// wasmAddPreset registers a preset document under an id the page chose, so
// that a later setPreset with that id plays it.
//
// It decodes rather than stores the text, for two reasons. The document is
// validated once, here, where the caller is still holding the error -- rather
// than at the next strike, where a bad preset would be a silent instrument.
// And it is the same preset.Decode the embedded assets go through, so a
// registered sound is a sound in exactly the sense the built-in ones are,
// schema upgrade included.
//
// Registering does not build an engine and does not touch the one that is
// playing: this is a cheap call the page may make while a note is ringing, and
// the expensive part happens on the setPreset that follows.
func wasmAddPreset(_ js.Value, args []js.Value) interface{} {
	if len(args) < 2 || args[0].Type() != js.TypeString || args[1].Type() != js.TypeString {
		return "addPreset takes an id and a preset document"
	}

	id := args[0].String()
	if id == "" {
		return "missing preset id"
	}

	// An id that names an embedded preset would shadow it for the rest of the
	// session, so the built-in sound the picker still offers would play
	// something else. Refused rather than resolved, because the front end mints
	// these ids and a collision is its bug, not the user's choice.
	if _, err := embeddedassets.Preset(id); err == nil {
		return "preset " + id + " is a built-in sound"
	}

	decoded, err := preset.Decode([]byte(args[1].String()), "custom preset "+id)
	if err != nil {
		return err.Error()
	}

	customPresets[id] = decoded

	// A cached engine was built around the previous document under this id, so
	// it has to go: keeping it would make a re-registration a no-op that plays
	// the old sound with no way to tell.
	delete(engines, id)

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

// wasmSetReverb sets how much of the output goes through the room.
//
// Unlike setPreset this does not rebuild anything and does not interrupt the
// render, so the worker has no gap to declare to the transport: the engine
// keeps answering processBlock throughout, and the change glides in over the
// next few milliseconds rather than landing between two blocks.
func wasmSetReverb(_ js.Value, args []js.Value) interface{} {
	if len(args) < 1 {
		return nil
	}

	globalReverbMix = float32(args[0].Float())

	if globalEngine == nil {
		return nil
	}

	globalEngine.SetReverbMix(globalReverbMix)

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
