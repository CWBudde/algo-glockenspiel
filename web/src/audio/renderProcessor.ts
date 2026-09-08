// The AudioWorkletProcessor. It contains no synthesis: the worker renders the
// blocks and this drains them, so the only work on the render thread is a copy
// and a postMessage of the buffer that has just been emptied.
//
// It is compiled as its own bundle (imported with `?worker&url` and handed to
// addModule), because a worklet module cannot resolve imports at runtime --
// whatever it needs has to be inlined into the file addModule fetches.

import { BlockQueue } from "./blockQueue";
import { MAX_POOL, POOL_SIZE, PROCESSOR_NAME } from "./protocol";
import type {
  ConsumePort,
  RecycledBuffer,
  RenderStats,
  TransportCredit,
  TransportMessage,
} from "./protocol";

// The AudioWorkletGlobalScope, declared here because it is in neither the DOM
// lib the app is compiled against nor a type package worth adding for six
// lines.
declare abstract class AudioWorkletProcessor {
  readonly port: MessagePort;
}

declare function registerProcessor(
  name: string,
  constructor: new () => AudioWorkletProcessor,
): void;

declare const sampleRate: number;

/** How often the processor reports what it has seen, in seconds. */
const STATS_INTERVAL_S = 0.5;

/**
 * How long a gap between two process() calls ends a burst, in milliseconds.
 *
 * Calls within one burst are separated only by the host's own loop -- a few
 * microseconds -- while the gap between bursts is the length of the output
 * buffer, which is tens of milliseconds on the host this was written for.
 * Anything in between does not occur, so the threshold is not delicate, and 2
 * is comfortably clear of the one-millisecond granularity of the only clock
 * this scope has.
 */
const BURST_GAP_MS = 2;

/**
 * Reporting intervals with no dropout after which the target is lowered by one.
 *
 * Asymmetric with the doubling that raises it, and deliberately slow: growing
 * has to outrun an audible fault, while shrinking is only reclaiming note-on
 * latency and has all the time in the world. Twenty intervals is ten seconds
 * per block given up, so a target inflated by a transient -- a fit run, a burst
 * of page load -- drains away over a minute or two rather than being kept for
 * the life of the session, and no plausible load oscillates slowly enough to
 * ride the loop up and down.
 */
const SHRINK_AFTER_CLEAN_REPORTS = 20;

class RenderProcessor extends AudioWorkletProcessor {
  private readonly queue = new BlockQueue();

  /** The worker's end of the render channel, once the page has transferred it. */
  private producer: MessagePort | null = null;

  private framesSinceReport = 0;

  /**
   * Date.now() at the previous process() entry, or 0 before the first.
   *
   * Date.now and not performance.now: Firefox's AudioWorkletGlobalScope has no
   * `performance` at all, and reaching for it there is a ReferenceError thrown
   * out of process() on every render quantum -- which is silence, not a
   * diagnostic. Its millisecond granularity is ample for the only question
   * being asked, since the two cases it has to separate are "the same
   * millisecond" and "tens of milliseconds later".
   */
  private lastCallMs = 0;

  /** Length of the run of back-to-back process() calls in progress. */
  private burst = 0;

  /** The longest such run seen since the last report. */
  private maxBurst = 0;

  /** Buffers the producer is being asked to keep in circulation. */
  private target = POOL_SIZE;

  /** underruns at the previous report, to tell a fresh dropout from an old one. */
  private reportedUnderruns = 0;

  /** Consecutive reports with no new dropout. */
  private cleanReports = 0;

  /**
   * The buffers emptied during the call in progress, returned in one message.
   *
   * A field rather than a local so the array itself is not reallocated per
   * quantum. It is emptied, not replaced, and the buffers in it are transferred
   * away, so nothing it holds outlives the postMessage that sends it.
   */
  private readonly spent: Float32Array[] = [];

  constructor() {
    super();

    this.port.onmessage = (event: MessageEvent<ConsumePort>) => {
      if (event.data.type !== "consume") {
        return;
      }

      this.producer = event.data.port;
      this.producer.onmessage = (message: MessageEvent<TransportMessage>) => {
        if (message.data.type === "pause") {
          this.queue.unprime();

          return;
        }

        for (const buffer of message.data.buffers) {
          this.queue.push(buffer);
        }
      };
      this.producer.start();
    };
  }

  process(_inputs: Float32Array[][], outputs: Float32Array[][]): boolean {
    const output = outputs[0];
    if (output === undefined || output.length < 2) {
      return true;
    }

    const left = output[0];
    const right = output[1];

    // Burst accounting first, so a starved call is attributed to the burst it
    // belongs to rather than to the next one.
    const now = Date.now();
    this.burst =
      this.lastCallMs !== 0 && now - this.lastCallMs < BURST_GAP_MS
        ? this.burst + 1
        : 1;
    this.lastCallMs = now;
    if (this.burst > this.maxBurst) {
      this.maxBurst = this.burst;
    }

    this.queue.fill(left, right, left.length, (buffer) => {
      this.spent.push(buffer);
    });

    if (this.spent.length > 0) {
      const message: RecycledBuffer = { type: "recycle", buffers: this.spent };
      this.producer?.postMessage(
        message,
        this.spent.map((buffer) => buffer.buffer),
      );
      this.spent.length = 0;
    }

    this.framesSinceReport += left.length;
    if (this.framesSinceReport >= STATS_INTERVAL_S * sampleRate) {
      this.framesSinceReport = 0;

      this.retarget();

      const stats: RenderStats = {
        type: "stats",
        underruns: this.queue.underruns,
        depth: this.queue.depth,
        minDepth: this.queue.takeMinDepth(),
        maxBurst: this.maxBurst,
        target: this.target,
      };
      this.port.postMessage(stats);

      this.maxBurst = 0;
    }

    // Never false: the node stays alive for the life of the page, because the
    // engine it fronts does too and a returned-false processor is not restarted.
    return true;
  }

  /**
   * retarget moves the credit target towards the shallowest queue this host
   * will run without dropouts, and tells the producer when it moves.
   *
   * Two inputs, because neither alone is enough. The burst length is the floor
   * the host imposes directly -- a queue shallower than one burst cannot
   * survive one -- but it is measured against a clock with millisecond
   * granularity and so is an undercount. Fresh dropouts are the ground truth
   * and need no clock at all, but they only say "deeper", not how much deeper,
   * which is why the response to them is to double: an additive climb out of a
   * queue four blocks deep towards the thirty a slow host wants would take
   * fifteen seconds of audible fault to finish.
   */
  private retarget(): void {
    const fresh = this.queue.underruns > this.reportedUnderruns;
    this.reportedUnderruns = this.queue.underruns;

    // One burst is the floor; twice a burst plus the starting pool leaves room
    // for the producer to refill between two of them.
    const floor = Math.min(MAX_POOL, this.maxBurst * 2 + POOL_SIZE);
    let next = Math.max(this.target, floor);

    if (fresh) {
      this.cleanReports = 0;
      next = Math.min(MAX_POOL, Math.max(next, this.target * 2));
    } else {
      this.cleanReports += 1;
      if (this.cleanReports >= SHRINK_AFTER_CLEAN_REPORTS) {
        this.cleanReports = 0;
        next = Math.max(floor, POOL_SIZE, next - 1);
      }
    }

    if (next === this.target) {
      return;
    }

    this.target = next;

    const credit: TransportCredit = { type: "credit", target: next };
    this.producer?.postMessage(credit);
  }
}

registerProcessor(PROCESSOR_NAME, RenderProcessor);
