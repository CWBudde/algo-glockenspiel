import {
  useCallback,
  useEffect,
  useRef,
  useState,
  type RefObject,
} from "react";

import { messageOf } from "./useWasmEngine";
import type { GlockenspielWasm } from "./wasmTypes";

/**
 * The cached view over Go's heap, plus the two facts that decide whether it is
 * still valid: which buffer it was cut from and which region of it it covers.
 *
 * These live in a plain object held by a ref, never in React state. The audio
 * callback runs on the audio thread's schedule and must not depend on a render
 * having happened, and a state update per block would be ~86 renders a second.
 */
interface InterleavedCache {
  view: Float32Array | null;
  buffer: ArrayBufferLike | null;
  ptr: number;
}

// interleavedFrames returns a Float32Array over `frames` stereo frames starting
// at `ptr` in the WASM linear memory, reusing the previous view when nothing
// relevant has changed. A view per callback is one allocation every ~11.6 ms at
// 512 frames and 44.1 kHz, on the one thread that must not pause for a GC.
//
// The hazard this function exists for: a WebAssembly.Memory grows when Go's
// heap grows, and growing DETACHES the old ArrayBuffer. A view hoisted out of
// the callback and never rechecked then points into a buffer that no longer
// backs anything -- and it does not throw. Measured in Chrome, after
// `memory.grow(1)`: the old buffer reports byteLength 0, `memory.buffer` is a
// different object, the stale view's length drops to 0, and indexing it returns
// `undefined`, which becomes NaN the moment it is written into the output
// buffer. So the symptom is not an exception at the point of the mistake but a
// channel of NaN -- silence, or worse depending on what the graph does with it
// -- starting at whatever unrelated moment the heap happened to grow: typically
// minutes in, once, and never while a debugger is attached. Hence three checks,
// all of them cheap:
//
//   - buffer identity: memory.buffer returns a *new* ArrayBuffer object after a
//     grow, so an identity comparison catches the detachment directly;
//   - byteLength === 0: how a detached ArrayBuffer reports itself. Re-reading
//     memory.buffer every call should already have handed us the live buffer,
//     but constructing a view over a detached one throws, and throwing out of
//     onaudioprocess is not how this should fail. Skipping the block yields one
//     buffer of silence instead;
//   - the pointer and length: ProcessBlock hands back a pointer into a Go slice,
//     and Go is free to move or resize that allocation between calls, so a
//     stable buffer does not imply a stable region.
//
// Returns null when no view can be built, in which case the caller leaves the
// output silent for that block.
function interleavedFrames(
  cache: InterleavedCache,
  memory: WebAssembly.Memory,
  ptr: number,
  frames: number,
): Float32Array | null {
  const floats = frames * 2;
  const buffer = memory.buffer;

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

export interface AudioEngine {
  /** True once the graph is running and notes will be heard. */
  ready: boolean;
  /** What the status panel should say about the audio, or "" for nothing yet. */
  status: string;
  error: boolean;
  /** Starts the graph if it is not running. Idempotent and safe to race. */
  start: () => Promise<void>;
  /** The synchronous answer to "can I strike right now", for the strike path. */
  isReady: () => boolean;
}

/**
 * useAudioEngine owns the AudioContext and the ScriptProcessorNode, created
 * lazily on the first strike because a browser will not start an AudioContext
 * without a user gesture.
 *
 * masterGain is pushed into the module whenever it changes, including while a
 * note is ringing, so the Volume dial moves the sound that is already playing.
 */
export function useAudioEngine(
  wasm: GlockenspielWasm | null,
  memoryRef: RefObject<WebAssembly.Memory | null>,
  masterGain: number,
): AudioEngine {
  const [ready, setReady] = useState(false);
  const [status, setStatus] = useState("");
  const [error, setError] = useState(false);

  const contextRef = useRef<AudioContext | null>(null);
  const outputRef = useRef<ScriptProcessorNode | null>(null);
  const readyRef = useRef(false);
  const startPromiseRef = useRef<Promise<void> | null>(null);
  const wasmRef = useRef<GlockenspielWasm | null>(wasm);
  const masterGainRef = useRef(masterGain);
  const cacheRef = useRef<InterleavedCache>({
    view: null,
    buffer: null,
    ptr: 0,
  });

  useEffect(() => {
    wasmRef.current = wasm;
    masterGainRef.current = masterGain;

    // Push the gain into a running engine, so the dial moves a note that is
    // already ringing rather than only the next one.
    if (readyRef.current && wasm) {
      wasm.setMasterGain(masterGain);
    }
  }, [wasm, masterGain]);

  // teardown disconnects and closes whatever half of the graph exists. It is
  // written to be safe on a graph that was never finished, because the failure
  // path in start needs exactly that.
  const teardown = useCallback(() => {
    outputRef.current?.disconnect();
    outputRef.current = null;
    void contextRef.current?.close();
    contextRef.current = null;
    readyRef.current = false;
  }, []);

  const start = useCallback(async () => {
    if (readyRef.current) {
      return;
    }

    if (startPromiseRef.current) {
      return startPromiseRef.current;
    }

    const startup = (async () => {
      const module = wasmRef.current;
      if (!module) {
        throw new Error("the WebAssembly module is not loaded yet");
      }

      const Context = window.AudioContext ?? window.webkitAudioContext;
      if (!Context) {
        throw new Error("this browser has no Web Audio support");
      }

      const context = new Context();
      contextRef.current = context;

      const initError = module.init(context.sampleRate);
      if (typeof initError === "string" && initError.length > 0) {
        throw new Error(initError);
      }

      const output = context.createScriptProcessor(512, 0, 2);
      output.onaudioprocess = (event) => {
        const buffer = event.outputBuffer;
        const left = buffer.getChannelData(0);
        const right = buffer.getChannelData(1);

        left.fill(0);
        right.fill(0);

        const memory = memoryRef.current;
        const engine = wasmRef.current;
        if (!memory || !engine) {
          return;
        }

        const interleavedPtr = engine.processBlock(left.length);
        if (!interleavedPtr) {
          return;
        }

        const interleaved = interleavedFrames(
          cacheRef.current,
          memory,
          Number(interleavedPtr),
          left.length,
        );
        if (interleaved === null) {
          return;
        }

        for (let frame = 0; frame < left.length; frame += 1) {
          left[frame] = interleaved[frame * 2];
          right[frame] = interleaved[frame * 2 + 1];
        }
      };

      output.connect(context.destination);
      outputRef.current = output;

      await context.resume();

      module.setMasterGain(masterGainRef.current);

      readyRef.current = true;
      setReady(true);
      setError(false);
      setStatus(`Ready at ${Math.round(context.sampleRate)} Hz`);
    })();

    startPromiseRef.current = startup;

    try {
      await startup;
    } catch (startError) {
      // Startup can fail after the AudioContext exists -- a module that
      // refuses the sample rate, a resume() the browser rejects -- and the
      // refs are already pointing at that half-built graph. Without this the
      // next strike would open a second context over the first, which stays
      // open and keeps its share of the (small) per-page context budget.
      teardown();
      setReady(false);

      console.error(startError);
      setStatus(messageOf(startError));
      setError(true);

      throw startError;
    } finally {
      startPromiseRef.current = null;
    }
  }, [memoryRef, teardown]);

  const isReady = useCallback(() => readyRef.current, []);

  useEffect(
    () => () => {
      // The page owns exactly one graph, but a hot reload or an unmount of the
      // Play tab must not leave a ScriptProcessorNode pulling blocks out of a
      // module nothing is listening to.
      teardown();
    },
    [teardown],
  );

  return { ready, status, error, start, isReady };
}
