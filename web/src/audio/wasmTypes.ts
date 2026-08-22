// The contract with cmd/glockenspiel-wasm. The names here are not a local
// choice: WASM_NAMESPACE and WASM_READY_CALLBACK are spelled out in
// cmd/glockenspiel-wasm/main.go and must stay in step with it, because a rename
// on one side leaves the other waiting forever for a signal that never comes.

export const WASM_NAMESPACE = "glockenspielWasm";
export const WASM_READY_CALLBACK = "__glockenspielWasmReady";

/**
 * How long to wait for the ready signal before giving up. The signal itself is
 * a real happens-after edge, so this is not a race workaround; it only turns a
 * module that died before publishing its exports -- a Go panic during init, a
 * trap in the runtime -- into a visible error instead of a page that sits on
 * "loading" with an empty console.
 */
export const WASM_READY_TIMEOUT_MS = 10000;

/** The exports the module publishes on globalThis once it is up. */
export interface GlockenspielWasm {
  /** Prepares the engine for a sample rate; returns a non-empty error string on failure. */
  init(sampleRate: number): string | undefined;
  noteOn(note: number, velocity: number): void;
  setMasterGain(gain: number): void;
  /** Renders one block and returns a pointer into the Go heap, or 0. */
  processBlock(frames: number): number;
}

/** The Go runtime shim, imported for its side effect by the engine worker. */
export interface GoRuntime {
  importObject: WebAssembly.Imports;
  run(instance: WebAssembly.Instance): Promise<void>;
}

declare global {
  // Published by wasm_exec.js on whichever global scope imports it. Since the
  // module moved into a worker that is the worker's scope, not the page's.
  var Go: { new (): GoRuntime };

  interface Window {
    webkitAudioContext?: typeof AudioContext;
  }
}

export function hasEveryExport(api: unknown): api is GlockenspielWasm {
  if (api === null || typeof api !== "object") {
    return false;
  }

  const candidate = api as Partial<Record<keyof GlockenspielWasm, unknown>>;

  return (
    typeof candidate.init === "function" &&
    typeof candidate.noteOn === "function" &&
    typeof candidate.processBlock === "function" &&
    typeof candidate.setMasterGain === "function"
  );
}
