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

import { BLOCK_FRAMES, MAX_POOL, POOL_SIZE } from "./protocol";
import type {
  ConsumerMessage,
  EngineCommand,
  EngineEvent,
  ProducerStats,
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

/** The rate the engine was built at, so a block's worth of audio has a duration. */
let renderSampleRate = 0;

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
 * Preset documents the page has handed over but the module has not been given
 * yet.
 *
 * They queue for the same reason the chosen id does: the Optimize tab can
 * produce a preset on a page that has never made a sound, so the module may
 * not exist when one arrives. Applied in arrival order, and before init, so an
 * id registered here is resolvable by the time the first engine is built.
 */
let pendingRegistrations: { presetId: string; document: string }[] = [];

/**
 * The buffers not currently in flight. postMessage transfers a buffer away and
 * detaches it here, so a block is only rendered when there is a free one to
 * render into; a buffer coming back is therefore the credit that asks for the
 * next block, and the pool size alone bounds how far ahead the worker may run.
 */
let free: Float32Array[] = [];

/**
 * How many buffers should exist in total, free and in flight together.
 *
 * The consumer sets it (see TransportCredit): it is the only party that can see
 * how its host actually asks for samples. The worker's part is to make the
 * count true -- allocate when it rises, drop returning buffers when it falls --
 * and otherwise to keep treating a buffer in hand as the credit to render one
 * block, which is unchanged.
 */
let target = POOL_SIZE;

/** Buffers that exist right now, free plus in flight. */
let poolSize = 0;

/**
 * setTarget adjusts the pool towards `next`.
 *
 * Growth is immediate, because the consumer only asks for it while it is
 * starving and a buffer that arrives after the next burst is a buffer that did
 * not help. Shrinking is lazy -- handled where buffers come back -- because the
 * ones over the new target are in flight, and there is nowhere to take them
 * from until the consumer is finished with them.
 */
function setTarget(next: number): void {
  target = Math.max(POOL_SIZE, Math.min(MAX_POOL, Math.floor(next)));

  while (poolSize < target) {
    free.push(new Float32Array(BLOCK_FRAMES * 2));
    poolSize += 1;
  }
}

/**
 * Sampler is a fixed-capacity reservoir of millisecond timings that can be
 * asked for its own percentiles.
 *
 * Fixed capacity, and sorted into a buffer it owns, because this runs on the
 * thread that has to stay ahead of the audio callback: a growing array would be
 * an allocation every few milliseconds and a fresh sort per report would be a
 * second one. Once full it keeps the first `capacity` samples of the window
 * rather than evicting, which is the cheapest policy that still answers the
 * question being asked -- whether the producer is ever late -- because `max` is
 * tracked separately and outside the reservoir.
 */
class Sampler {
  private readonly samples: Float64Array;
  private readonly scratch: Float64Array;
  private count = 0;
  private worst = 0;

  constructor(capacity: number) {
    this.samples = new Float64Array(capacity);
    this.scratch = new Float64Array(capacity);
  }

  add(ms: number): void {
    if (ms > this.worst) {
      this.worst = ms;
    }

    if (this.count < this.samples.length) {
      this.samples[this.count] = ms;
      this.count += 1;
    }
  }

  /** take returns the window's percentiles and starts a fresh one. */
  take(): { p50: number; p99: number; max: number } {
    if (this.count === 0) {
      this.worst = 0;

      return { p50: 0, p99: 0, max: 0 };
    }

    const window = this.scratch.subarray(0, this.count);
    window.set(this.samples.subarray(0, this.count));
    window.sort();

    const at = (fraction: number): number =>
      window[Math.min(this.count - 1, Math.floor(fraction * this.count))];

    const stats = { p50: at(0.5), p99: at(0.99), max: this.worst };

    this.count = 0;
    this.worst = 0;

    return stats;
  }
}

/** How often the producer reports its timings, in milliseconds. */
const PRODUCER_STATS_INTERVAL_MS = 500;

const wakeupSampler = new Sampler(1024);

/** Milliseconds spent inside processBlock since the last report. */
let renderMs = 0;

/** Blocks rendered since the last report. */
let renderedBlocks = 0;

/** performance.now() at the previous pump() entry, or 0 before the first. */
let lastPumpMs = 0;

/** performance.now() at the last producer stats report. */
let lastReportMs = 0;

/**
 * Blocks sent as silence because interleavedFrames could not build a view.
 *
 * See ProducerStats.silentBlocks: this is the tick the dropout counter cannot
 * see, so it is counted here or it is not counted anywhere.
 */
let silentBlocks = 0;

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

/**
 * flushRegistrations hands every queued document to the module.
 *
 * A rejected document is reported and dropped rather than retried: it is
 * rejected by preset.Decode, which is deterministic, so a retry would report
 * the same failure on every later flush.
 */
function flushRegistrations(): void {
  if (api === null || pendingRegistrations.length === 0) {
    return;
  }

  const queued = pendingRegistrations;
  pendingRegistrations = [];

  for (const registration of queued) {
    const failure = api.addPreset(registration.presetId, registration.document);
    if (typeof failure === "string" && failure.length > 0) {
      post({ type: "error", message: failure });
    }
  }
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
  flushRegistrations();
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

  const entered = performance.now();
  if (lastPumpMs !== 0) {
    wakeupSampler.add(entered - lastPumpMs);
  }
  lastPumpMs = entered;

  if (entered - lastReportMs >= PRODUCER_STATS_INTERVAL_MS) {
    lastReportMs = entered;

    const audioMs = (renderedBlocks * BLOCK_FRAMES * 1000) / renderSampleRate;
    const report: ProducerStats = {
      type: "producerStats",
      wakeup: wakeupSampler.take(),
      load: audioMs > 0 ? renderMs / audioMs : 0,
      blocks: renderedBlocks,
      silentBlocks,
    };
    post(report);

    renderMs = 0;
    renderedBlocks = 0;
  }

  if (free.length === 0) {
    return;
  }

  // Rendered into `free` in place and then handed over as one batch: the
  // consumer is going to take the whole lot in its next burst anyway, and one
  // message per block is one scheduling opportunity per block for the browser
  // to run something else first.
  const batch = free;
  free = [];

  const startedRender = performance.now();

  for (const block of batch) {
    const ptr = api.processBlock(BLOCK_FRAMES);
    const view =
      ptr === 0 ? null : interleavedFrames(memory, Number(ptr), BLOCK_FRAMES);

    if (view === null) {
      silentBlocks += 1;
      block.fill(0);
    } else {
      // The samples have to be copied rather than transferred: they live in
      // Go's linear memory, which this worker does not own and cannot give
      // away. 256 floats per block is the price of the whole arrangement.
      block.set(view);
    }
  }

  renderMs += performance.now() - startedRender;
  renderedBlocks += batch.length;

  const message: RenderedBlock = { type: "block", buffers: batch };
  consumer.postMessage(
    message,
    batch.map((block) => block.buffer),
  );
}

function startRendering(sampleRate: number, port: MessagePort): void {
  if (api === null) {
    post({
      type: "error",
      message: "the WebAssembly module is not loaded yet",
    });

    return;
  }

  // Before init rather than after: presetId may name a document registered
  // while the module was still loading, and init resolves it.
  flushRegistrations();

  const initError = api.init(sampleRate, presetId);
  if (typeof initError === "string" && initError.length > 0) {
    post({ type: "error", message: initError });

    return;
  }

  consumer = port;
  consumer.onmessage = (event: MessageEvent<ConsumerMessage>) => {
    if (event.data.type === "credit") {
      setTarget(event.data.target);
      pump();

      return;
    }

    for (const buffer of event.data.buffers) {
      // A buffer over the current target is dropped rather than kept: this is
      // where a shrink actually happens, because until now it was in flight and
      // out of reach.
      if (poolSize > target) {
        poolSize -= 1;
        continue;
      }

      free.push(buffer);
    }

    pump();
  };
  consumer.start();

  renderSampleRate = sampleRate;

  free = newPool();
  poolSize = free.length;
  target = POOL_SIZE;
  rendering = true;

  // A fresh window: the gap across a graph that was not running is not jitter
  // the producer is answerable for, and it dwarfs everything real.
  lastPumpMs = 0;
  lastReportMs = performance.now();

  post({ type: "started", sampleRate });
  pump();
}

function stopRendering(): void {
  rendering = false;
  lastPumpMs = 0;
  consumer?.close();
  consumer = null;
  // The buffers in flight are gone with the port, so the pool is rebuilt rather
  // than reused: a restart begins with POOL_SIZE buffers whatever happened to
  // the last graph, and with the target the consumer will raise again if its
  // host still needs it raised.
  free = [];
  poolSize = 0;
  target = POOL_SIZE;
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

    case "setReverb":
      // No TransportPause and no pump(): nothing is rebuilt, so the render loop
      // never goes quiet and there is no deliberate gap to declare. The module
      // remembers the value across an engine rebuild the same way it remembers
      // the master gain, so a swap does not silently drop the room.
      api?.setReverb(command.mix);
      break;

    case "registerPreset":
      // No TransportPause and no pump(): nothing is built here, so the render
      // loop never goes quiet. The gap belongs to the setPreset that follows.
      pendingRegistrations.push({
        presetId: command.presetId,
        document: command.document,
      });
      flushRegistrations();
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
