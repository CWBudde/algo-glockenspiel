// The AudioWorkletProcessor. It contains no synthesis: the worker renders the
// blocks and this drains them, so the only work on the render thread is a copy
// and a postMessage of the buffer that has just been emptied.
//
// It is compiled as its own bundle (imported with `?worker&url` and handed to
// addModule), because a worklet module cannot resolve imports at runtime --
// whatever it needs has to be inlined into the file addModule fetches.

import { BlockQueue } from "./blockQueue";
import { PROCESSOR_NAME } from "./protocol";
import type {
  ConsumePort,
  RecycledBuffer,
  RenderedBlock,
  RenderStats,
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

class RenderProcessor extends AudioWorkletProcessor {
  private readonly queue = new BlockQueue();

  /** The worker's end of the render channel, once the page has transferred it. */
  private producer: MessagePort | null = null;

  private framesSinceReport = 0;

  constructor() {
    super();

    this.port.onmessage = (event: MessageEvent<ConsumePort>) => {
      if (event.data.type !== "consume") {
        return;
      }

      this.producer = event.data.port;
      this.producer.onmessage = (block: MessageEvent<RenderedBlock>) => {
        this.queue.push(block.data.buffer);
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

    this.queue.fill(left, right, left.length, (buffer) => {
      const message: RecycledBuffer = { type: "recycle", buffer };
      this.producer?.postMessage(message, [buffer.buffer]);
    });

    this.framesSinceReport += left.length;
    if (this.framesSinceReport >= STATS_INTERVAL_S * sampleRate) {
      this.framesSinceReport = 0;

      const stats: RenderStats = {
        type: "stats",
        underruns: this.queue.underruns,
        depth: this.queue.depth,
      };
      this.port.postMessage(stats);
    }

    // Never false: the node stays alive for the life of the page, because the
    // engine it fronts does too and a returned-false processor is not restarted.
    return true;
  }
}

registerProcessor(PROCESSOR_NAME, RenderProcessor);
