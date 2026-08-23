// The worker that owns the Go module. Everything the synthesiser does happens
// here: the page only sends notes and reads status, and the audio thread only
// drains the blocks this worker renders ahead of it.
//
// Why a worker and not the AudioWorkletGlobalScope, which would be one thread
// fewer and one copy fewer: wasm_exec.js refuses to load without crypto,
// performance, TextEncoder and TextDecoder, none of which that scope has, and
// the Go scheduler wants setTimeout on top of them. Running the module there
// would also put Go's garbage collector on the render thread, where a pause is
// a glitch with no queue in front of it to absorb it. See
// docs/audio-transport.md for the decision in full.

import { BLOCK_FRAMES, POOL_SIZE } from "./protocol";
import type {
  EngineCommand,
  EngineEvent,
  RecycledBuffer,
  RenderedBlock,
  TransportPause,
} from "./protocol";
import { loadWasm } from "./loadWasm";
import { hasAudioExports, type GlockenspielAudioWasm } from "./wasmTypes";

/**
 * The slice of the worker global scope this file touches, declared locally
 * rather than pulled in with `lib="webworker"`: that lib and the DOM lib the
 * app is compiled against redeclare each other's globals, and the app needs the
 * DOM one. The two module globals are here because the Go module publishes them
 * on whatever `js.Global()` is, which in a worker is this object.
 */
interface WorkerScope {
  postMessage(message: EngineEvent): void;
  onmessage: ((event: MessageEvent<EngineCommand>) => void) | null;
}

const scope = self as unknown as WorkerScope;

let api: GlockenspielAudioWasm | null = null;
let memory: WebAssembly.Memory | null = null;
let loading: Promise<void> | null = null;

/** The consumer's end of the render channel: the worklet, or the page's fallback. */
let consumer: MessagePort | null = null;

/** True once init has been given a sample rate and rendering may begin. */
let rendering = false;

/**
 * The built-in sound the engine should play. Empty means the module's own
 * default, so the worker never has to name it.
 *
 * It is remembered rather than forwarded straight through, because the picker
 * is reachable before there is an engine to tell: the AudioContext only exists
 * after the first strike, so a sound chosen on a freshly loaded page has to
 * survive until init runs. Once init has run, a change is applied immediately.
 */
let presetId = "";

/**
 * The buffers not currently in flight. postMessage transfers a buffer away and
 * detaches it here, so a block is only rendered when there is a free one to
 * render into; a buffer coming back is therefore the credit that asks for the
 * next block, and the pool size alone bounds how far ahead the worker may run.
 */
let free: Float32Array[] = [];

/**
 * The cached view over Go's heap, plus the two facts that decide whether it is
 * still valid: which buffer it was cut from and which region of it it covers.
 */
interface InterleavedCache {
  view: Float32Array | null;
  buffer: ArrayBufferLike | null;
  ptr: number;
}

const cache: InterleavedCache = { view: null, buffer: null, ptr: 0 };

// interleavedFrames returns a Float32Array over `frames` stereo frames starting
// at `ptr` in the WASM linear memory, reusing the previous view when nothing
// relevant has changed. A view per block is one allocation every ~2.9 ms at
// 128 frames and 44.1 kHz, on a thread whose whole job is to stay ahead of the
// audio callback.
//
// The hazard this function exists for: a WebAssembly.Memory grows when Go's
// heap grows, and growing DETACHES the old ArrayBuffer. A view hoisted out of
// the render path and never rechecked then points into a buffer that no longer
// backs anything -- and it does not throw. Measured in Chrome, after
// `memory.grow(1)`: the old buffer reports byteLength 0, `memory.buffer` is a
// different object, the stale view's length drops to 0, and indexing it returns
// `undefined`, which becomes NaN the moment it is copied into an output buffer.
// So the symptom is not an exception at the point of the mistake but a channel
// of NaN -- silence, or worse depending on what the graph does with it --
// starting at whatever unrelated moment the heap happened to grow: typically
// minutes in, once, and never while a debugger is attached. Hence three checks,
// all of them cheap:
//
//   - buffer identity: memory.buffer returns a *new* ArrayBuffer object after a
//     grow, so an identity comparison catches the detachment directly;
//   - byteLength === 0: how a detached ArrayBuffer reports itself. Re-reading
//     memory.buffer every call should already have handed us the live buffer,
//     but constructing a view over a detached one throws, and this runs inside
//     a message handler where a throw is an unhandled rejection and a stalled
//     queue. Skipping the block yields one block of silence instead;
//   - the pointer and length: ProcessBlock hands back a pointer into a Go slice,
//     and Go is free to move or resize that allocation between calls, so a
//     stable buffer does not imply a stable region.
//
// Returns null when no view can be built, in which case the caller sends
// silence for that block.
function interleavedFrames(
  wasmMemory: WebAssembly.Memory,
  ptr: number,
  frames: number,
): Float32Array | null {
  const floats = frames * 2;
  const buffer = wasmMemory.buffer;

  if (buffer.byteLength === 0) {
    cache.view = null;
    cache.buffer = null;

    return null;
  }

  if (
    cache.view === null ||
    cache.buffer !== buffer ||
    cache.ptr !== ptr ||
    cache.view.length !== floats
  ) {
    cache.view = new Float32Array(buffer, ptr, floats);
    cache.buffer = buffer;
    cache.ptr = ptr;
  }

  return cache.view;
}

function post(event: EngineEvent): void {
  scope.postMessage(event);
}

export function messageOf(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

/**
 * load fetches the shim and the module and runs the Go runtime.
 *
 * wasm_exec.js is imported for its side effect rather than fetched and eval'd:
 * it is an IIFE that assigns globalThis.Go and exports nothing, so a module
 * import publishes the constructor here exactly as the classic script tag used
 * to publish it on the page. It stays byte-identical to the Go toolchain's copy
 * either way -- scripts/build-wasm.sh refuses to build when it drifts -- which
 * is why it is imported by URL and never bundled.
 */
async function load(baseURL: string): Promise<void> {
  const loaded = await loadWasm(
    baseURL,
    "audio",
    hasAudioExports,
    (runtimeError: unknown) => {
      rendering = false;
      post({
        type: "error",
        message: `WASM runtime stopped: ${messageOf(runtimeError)}`,
      });
    },
  );

  api = loaded.api;
  memory = loaded.memory;
  post({ type: "loaded" });
}

/** newPool builds the buffers the worker and the consumer pass back and forth. */
function newPool(): Float32Array[] {
  return Array.from(
    { length: POOL_SIZE },
    () => new Float32Array(BLOCK_FRAMES * 2),
  );
}

/**
 * pump renders into every free buffer and sends each one on.
 *
 * It is called from exactly two places -- the start of rendering, which primes
 * the queue, and a buffer coming back -- so the loop cannot run away: it stops
 * when the pool is empty, and the pool only refills at the consumer's pace.
 */
function pump(): void {
  if (!rendering || api === null || memory === null || consumer === null) {
    return;
  }

  while (free.length > 0) {
    const block = free.pop();
    if (block === undefined) {
      return;
    }

    const ptr = api.processBlock(BLOCK_FRAMES);
    const view =
      ptr === 0 ? null : interleavedFrames(memory, Number(ptr), BLOCK_FRAMES);

    if (view === null) {
      block.fill(0);
    } else {
      // The samples have to be copied rather than transferred: they live in
      // Go's linear memory, which this worker does not own and cannot give
      // away. 256 floats per block is the price of the whole arrangement.
      block.set(view);
    }

    const message: RenderedBlock = { type: "block", buffer: block };
    consumer.postMessage(message, [block.buffer]);
  }
}

function startRendering(sampleRate: number, port: MessagePort): void {
  if (api === null) {
    post({
      type: "error",
      message: "the WebAssembly module is not loaded yet",
    });

    return;
  }

  const initError = api.init(sampleRate, presetId);
  if (typeof initError === "string" && initError.length > 0) {
    post({ type: "error", message: initError });

    return;
  }

  consumer = port;
  consumer.onmessage = (event: MessageEvent<RecycledBuffer>) => {
    free.push(event.data.buffer);
    pump();
  };
  consumer.start();

  free = newPool();
  rendering = true;

  post({ type: "started", sampleRate });
  pump();
}

function stopRendering(): void {
  rendering = false;
  consumer?.close();
  consumer = null;
  // The buffers in flight are gone with the port, so the pool is rebuilt rather
  // than reused: a restart begins with POOL_SIZE buffers whatever happened to
  // the last graph.
  free = [];
}

scope.onmessage = (event: MessageEvent<EngineCommand>) => {
  const command = event.data;

  switch (command.type) {
    case "load":
      // The page mounts once, but a StrictMode double-effect or a hot reload
      // must not start two Go runtimes in one worker.
      loading ??= load(command.baseURL).catch((error: unknown) => {
        post({ type: "error", message: messageOf(error) });
      });
      break;

    case "start":
      startRendering(command.sampleRate, command.port);
      break;

    case "noteOn":
      api?.noteOn(command.note, command.velocity);
      break;

    case "setMasterGain":
      api?.setMasterGain(command.gain);
      break;

    case "setPreset": {
      presetId = command.presetId;

      // Before the engine exists there is nothing to swap and nothing to
      // report: the id above is what init will be given. Applying it twice --
      // here and again at init -- would pay for a calibration sweep the first
      // strike is about to pay for anyway.
      if (!rendering || api === null) {
        break;
      }

      // Building an engine for a sound that has not been chosen before takes
      // 165-190 ms in the browser, against a queue four blocks deep -- about
      // 11.6 ms at 44.1 kHz. The consumer therefore runs dry, and it has to be
      // told that the silence is deliberate or it will record more than a
      // dozen dropouts for a fault that never happened. Sent unconditionally,
      // because whether this particular swap is the cheap cached one is a fact
      // on the Go side and not worth a round trip to learn.
      const pause: TransportPause = { type: "pause" };
      consumer?.postMessage(pause);

      const presetError = api.setPreset(presetId);
      if (typeof presetError === "string" && presetError.length > 0) {
        post({ type: "error", message: presetError });
      }

      // The queue is empty and every buffer is back here, so nothing restarts
      // the render loop on its own: the recycle messages that normally ask for
      // the next block have all been delivered already.
      pump();

      break;
    }

    case "stop":
      stopRendering();
      break;
  }
};
