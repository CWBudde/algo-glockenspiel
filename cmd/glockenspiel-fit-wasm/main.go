//go:build js && wasm

package main

import "syscall/js"

const (
	namespaceName     = "glockenspielWasm"
	readyCallbackName = "__glockenspielWasmReady"
)

func main() {
	done := make(chan struct{})

	api := js.Global().Get("Object").New()
	api.Set("fitStart", js.FuncOf(globalFits.start))
	api.Set("fitCancel", js.FuncOf(globalFits.cancel))
	api.Set("fitPreset", js.FuncOf(globalFits.preset))
	api.Set("fitRender", js.FuncOf(globalFits.render))

	js.Global().Set(namespaceName, api)
	signalReady(api)

	println("WASM glockenspiel optimizer loaded")
	<-done
}

func signalReady(api js.Value) {
	ready := js.Global().Get(readyCallbackName)
	if ready.Type() == js.TypeFunction {
		ready.Invoke(api)
	}
}
