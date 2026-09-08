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
 * How many buffers the pool starts with. They are the whole flow control: the
 * worker renders only into a free one and gets it back from the consumer, so
 * the pool size bounds both the queue depth and the total allocation, and
 * nothing allocates per block.
 *
 * Four blocks is 512 frames, ~11.6 ms at 44.1 kHz -- the jitter the worker may
 * take, a Go GC pause or a slow tick of the browser's task queue, before the
 * consumer starves. It is also what note-on latency is paid out of, so it is a
 * floor for responsiveness as much as a ceiling for safety, which is why it is
 * where the pool *starts* rather than where it stays: the consumer raises the
 * target when this proves too shallow for its host (see TransportCredit) and
 * lowers it again when it does not.
 */
export const POOL_SIZE = 4;

/**
 * The most buffers the consumer may ask for: 32 blocks, 4096 frames, ~93 ms at
 * 44.1 kHz.
 *
 * A ceiling exists because the target is raised by a feedback loop, and a loop
 * with no bound turns a permanently broken producer into unbounded memory and
 * unbounded note-on latency rather than into an audible fault. 93 ms comfortably
 * covers the worst host buffer measured here (Firefox on Linux reports an
 * output latency of ~55 ms) plus the producer jitter on top of it, and a
 * transport that still starves at 93 ms of queue is not starving for want of
 * queue.
 */
export const MAX_POOL = 32;

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
  | {
      /**
       * Make a preset document playable under an id, without playing it.
       *
       * This is how a sound that does not exist at build time -- an optimizer
       * result -- becomes choosable: everything in assets is embedded in the
       * module, so a fitted preset has no id to be chosen by until one is given
       * to it here. A "setPreset" naming that id follows whenever the user
       * picks it, and only then is an engine built.
       *
       * Accepted before the module has loaded, for the same reason setPreset
       * is: the Optimize tab is reachable on a page that has never made a
       * sound. The worker holds the registration until there is a module to
       * hand it to, and applies it before init, so the id is resolvable by the
       * time the first engine is built.
       */
      type: "registerPreset";
      presetId: string;
      document: string;
    }
  | {
      /**
       * How much of the output goes through the engine's room, 0..1.
       *
       * It is a plain live setter, unlike setPreset: nothing is rebuilt, the
       * render never stops, and the consumer therefore needs no warning that a
       * gap is coming.
       */
      type: "setReverb";
      mix: number;
    }
  /** Drop the consumer and rebuild the pool, so a restart begins from a known state. */
  | { type: "stop" };

/** Worker -> page. */
export type EngineEvent =
  | { type: "loaded" }
  | { type: "started"; sampleRate: number }
  | { type: "error"; message: string }
  | ProducerStats;

/**
 * Worker -> consumer, over the dedicated channel.
 *
 * A batch rather than a block. One message per 128 frames is ~344 tasks a
 * second in each direction, and every one of them is an opportunity for the
 * browser to run something else first; the consumer drains its whole burst in
 * one go and the producer refills the whole pool in one go, so the natural unit
 * on the wire is the batch either way. The buffers are transferred, so the cost
 * of carrying several is the array.
 */
export interface RenderedBlock {
  type: "block";
  buffers: Float32Array[];
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

/** Consumer -> worker, the same buffers travelling back to be filled again. */
export interface RecycledBuffer {
  type: "recycle";
  buffers: Float32Array[];
}

/**
 * Consumer -> worker, how many buffers the consumer wants in circulation.
 *
 * The pool is the flow control, so its size is the queue's depth, and the right
 * depth is a property of the host that only the consumer can see. A render
 * thread does not ask for one quantum at a time: it services its output buffer
 * in a burst of back-to-back process() calls and then sleeps for the length of
 * that buffer. A queue shallower than one burst therefore ends every burst in
 * silence no matter how prompt the producer is -- which is the shape of the
 * fault this message exists to fix, measured at 603 dropouts in eight seconds
 * on Firefox against a host that buffers ~55 ms and a pool that held 11.6 ms.
 *
 * The consumer raises the target only while it is actually starving and lowers
 * it again after a long clean run, so the depth settles at the smallest value
 * the host will accept. That matters: the depth is also what note-on latency is
 * paid out of, so a target chosen once for the worst host would make every
 * other one less responsive for nothing.
 */
export interface TransportCredit {
  type: "credit";
  target: number;
}

/** Everything the consumer sends back up the render channel. */
export type ConsumerMessage = RecycledBuffer | TransportCredit;

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
  /**
   * The lowest depth seen during the reporting interval, in frames.
   *
   * `depth` alone cannot show a starving transport: it is sampled at one
   * arbitrary instant, and a queue that is emptied and refilled between two
   * samples reads as full. This is the number that says how much margin the
   * producer actually has.
   */
  minDepth: number;
  /**
   * The longest run of process() calls the render thread made back to back,
   * during the reporting interval.
   *
   * A host that services its output buffer in one burst calls process() once
   * per render quantum in that buffer, with no wall-clock gap between the
   * calls, and then sleeps for the length of the buffer. The queue therefore
   * has to be deeper than one burst, not merely deeper than one quantum: a
   * burst of 16 asks for 2048 frames in the time it takes to run a memcpy, and
   * no amount of producer promptness can answer it if the frames are not
   * already there.
   *
   * 1 means the host asks for one quantum at a time, which is what the
   * transport was designed and measured against.
   */
  maxBurst: number;
  /** Buffers the consumer is currently asking the producer to keep in flight. */
  target: number;
}

/**
 * Worker -> page, what the producer side of the transport is doing.
 *
 * The consumer's stats say the queue ran dry; these say whether the producer
 * was late or merely never asked. The pair is what separates "the host drains
 * faster than a message round trip can answer" from "a render or a GC pause
 * overran the buffer", which are different faults with different fixes and look
 * identical from the output.
 */
export interface ProducerStats {
  type: "producerStats";
  /** Milliseconds between successive pump() entries: median, p99, worst. */
  wakeup: { p50: number; p99: number; max: number };
  /**
   * What fraction of realtime the render actually consumes: milliseconds spent
   * in processBlock over the milliseconds of audio those calls produced.
   *
   * A ratio and not a distribution of per-block timings, because per-block is
   * below the resolution of the only clock a worker is given -- Firefox
   * coarsens performance.now() to a millisecond, and a block is 2.7 ms of audio
   * rendered in a fraction of that, so every sample lands on 0 or 1 and the
   * percentiles are noise. Summed over a reporting interval the granularity
   * amortises away, and the resulting number is the one that actually decides
   * whether a transport can work at all: at 1.0 the producer is exactly
   * realtime and no depth of queue can save it, because a queue only buys time
   * against jitter, never against a deficit that recurs every block.
   */
  load: number;
  /** Blocks rendered during the interval, so `load` can be read for confidence. */
  blocks: number;
  /**
   * Blocks sent as silence because no view over Go's memory could be built,
   * since the graph started.
   *
   * Counted separately from underruns and never folded into them: this is an
   * audible tick that the dropout counter is blind to by construction -- the
   * block is delivered, on time, and merely contains nothing -- so a page
   * reporting clicks with a clean counter has exactly one place to look.
   */
  silentBlocks: number;
}
