// The contract with both cmd/glockenspiel-wasm entry points. These names are
// spelled out in each main.go and must stay in step with this file, because a
// rename on one side leaves its worker waiting forever for a ready signal.

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
export interface GlockenspielAudioWasm {
  /**
   * Prepares the engine for a sample rate; returns a non-empty error string on
   * failure. presetId names a built-in sound; omitted or empty is the default.
   */
  init(sampleRate: number, presetId?: string): string | undefined;
  noteOn(note: number, velocity: number): void;
  setMasterGain(gain: number): void;
  /**
   * Rebuilds the engine around another built-in sound, keeping the master gain.
   * Returns a non-empty error string on failure, and leaves the engine playing
   * the sound it already had.
   */
  setPreset(presetId: string): string | undefined;
  /**
   * Sets how much of the output goes through the engine's room, 0..1.
   *
   * Unlike setPreset this rebuilds nothing and never interrupts the render, so
   * it is safe to call as fast as a dial produces values. The engine glides the
   * change in over a few milliseconds rather than applying it between blocks.
   */
  setReverb(mix: number): void;
  /** Renders one block and returns a pointer into the Go heap, or 0. */
  processBlock(frames: number): number;
}

export interface GlockenspielFitWasm {
  /** Starts an asynchronous optimizer run; returns an error string if rejected. */
  fitStart(
    requestJSON: string,
    reference: Uint8Array,
    preset: Uint8Array,
    bounds: Uint8Array,
    onSnapshot: (snapshotJSON: string) => void,
  ): string | null | undefined;
  /** Requests cancellation; the terminal snapshot still arrives through onSnapshot. */
  fitCancel(jobId: string): string | null | undefined;
  /** Returns the best preset JSON or an error. */
  fitPreset(): WasmResult<string>;
  /** Renders the best preset as a mono WAV or returns an error. */
  fitRender(
    note: number,
    velocity: number,
    duration: number,
  ): WasmResult<Uint8Array>;
}

export interface WasmResult<T> {
  data?: T;
  error?: string;
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

export function hasAudioExports(api: unknown): api is GlockenspielAudioWasm {
  if (api === null || typeof api !== "object") {
    return false;
  }

  const candidate = api as Partial<
    Record<keyof GlockenspielAudioWasm, unknown>
  >;

  return (
    typeof candidate.init === "function" &&
    typeof candidate.noteOn === "function" &&
    typeof candidate.processBlock === "function" &&
    typeof candidate.setMasterGain === "function" &&
    typeof candidate.setPreset === "function" &&
    typeof candidate.setReverb === "function"
  );
}

export function hasFitExports(api: unknown): api is GlockenspielFitWasm {
  if (api === null || typeof api !== "object") {
    return false;
  }

  const candidate = api as Partial<Record<keyof GlockenspielFitWasm, unknown>>;

  return (
    typeof candidate.fitStart === "function" &&
    typeof candidate.fitCancel === "function" &&
    typeof candidate.fitPreset === "function" &&
    typeof candidate.fitRender === "function"
  );
}
