// The message contract between the three threads that now share the audio
// path: the page, the worker that owns the Go module, and the consumer that
// feeds the output. It is declared once, here, for the same reason
// src/api/types.ts transcribes the fit API's wire structs by hand -- a field
// renamed on one side should be a type error, not a silence nobody can trace.

/**
 * Frames per rendered block.
 *
 * 128 is the Web Audio render quantum, so a block covers exactly one call to
 * process() and the queue never has to straddle two of them in the steady
 * state. (BlockQueue handles any size; this is the size that costs the least.)
 */
export const BLOCK_FRAMES = 128;

/**
 * How many buffers exist. They are the whole pool: the worker renders only
 * into a free one and gets it back from the consumer, so POOL_SIZE bounds both
 * the queue depth and the total allocation, and nothing allocates per block.
 *
 * Four blocks is 512 frames, ~11.6 ms at 44.1 kHz. That is the jitter the
 * worker may take -- a Go GC pause, a slow tick of the browser's task queue --
 * before the consumer starves, and it is also what note-on latency is paid out
 * of, so it is a floor for responsiveness as much as a ceiling for safety.
 */
export const POOL_SIZE = 4;

/** The name registerProcessor is called with, and the one addModule then serves. */
export const PROCESSOR_NAME = "glockenspiel-render";

/** Page -> worker. */
export type EngineCommand =
  | {
      /**
       * Fetch and instantiate the module. baseURL is the page's, because a
       * bundled worker is served from assets/ and would otherwise resolve
       * "manifest.json" against the wrong directory.
       */
      type: "load";
      baseURL: string;
    }
  | {
      /** Prepare the engine and start rendering into the transferred port. */
      type: "start";
      sampleRate: number;
      port: MessagePort;
    }
  | { type: "noteOn"; note: number; velocity: number }
  | { type: "setMasterGain"; gain: number }
  | {
      /**
       * Choose which built-in sound the engine plays. An empty id is the
       * engine's own default, so the page never has to know its name.
       *
       * It is accepted before the engine has started, because it will be: the
       * AudioContext cannot be created until the first strike, so the picker is
       * reachable for as long as the user likes before "start" ever runs. The
       * worker holds the last id and hands it to init, which is why choosing a
       * sound and then striking a bar plays that sound rather than the default
       * followed by a swap.
       */
      type: "setPreset";
      presetId: string;
    }
  /** Drop the consumer and rebuild the pool, so a restart begins from a known state. */
  | { type: "stop" };

/** Worker -> page. */
export type EngineEvent =
  | { type: "loaded" }
  | { type: "started"; sampleRate: number }
  | { type: "error"; message: string };

/** Worker -> consumer, over the dedicated channel. */
export interface RenderedBlock {
  type: "block";
  buffer: Float32Array;
}

/**
 * Worker -> consumer, warning that the producer is about to go quiet on
 * purpose.
 *
 * Rebuilding the engine for a new sound takes far longer than the queue is
 * deep, so the consumer runs dry. That silence is deliberate and must not be
 * counted as a dropout: the counter is permanent and is shown in the deck, so
 * without this a change of sound would leave the page reporting a fault that
 * never happened. The queue re-primes itself on the next block, so there is no
 * matching resume.
 */
export interface TransportPause {
  type: "pause";
}

/** Everything the worker sends down the render channel. */
export type TransportMessage = RenderedBlock | TransportPause;

/** Consumer -> worker, the same buffer travelling back to be filled again. */
export interface RecycledBuffer {
  type: "recycle";
  buffer: Float32Array;
}

/** Page -> worklet, handing over the end of the channel the worker renders into. */
export interface ConsumePort {
  type: "consume";
  port: MessagePort;
}

/** Worklet -> page, the telemetry behind "no dropouts under load". */
export interface RenderStats {
  type: "stats";
  /** Render quanta that found the queue empty, since the graph started. */
  underruns: number;
  /** Frames waiting in the queue at the time of the report. */
  depth: number;
}
