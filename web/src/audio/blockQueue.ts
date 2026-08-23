/**
 * BlockQueue is the consumer side of the transport: rendered blocks in,
 * de-interleaved output out, spent buffers back to the worker.
 *
 * It is deliberately free of Web Audio and of the DOM. The AudioWorklet
 * processor and the ScriptProcessorNode fallback are both thin wrappers around
 * it, which is what keeps the fallback a few lines rather than a second engine,
 * and it is the only part of the transport a unit test can reach: everything
 * else needs a real audio thread.
 *
 * Blocks are stereo-interleaved, as ProcessBlock hands them over
 * (internal/synth/realtime.go), so a block of n frames is 2n floats.
 */
export class BlockQueue {
  private readonly blocks: Float32Array[] = [];

  /** Frames already read out of blocks[0]. */
  private offset = 0;

  /** Render calls that found the queue empty. Never reset; the page reads deltas. */
  underruns = 0;

  /**
   * False until the first block arrives.
   *
   * Silence before the producer has ever delivered anything is not a dropout,
   * and the distinction is not academic: Chrome calls process() on a source
   * worklet node whether or not it is connected to anything, so the counter
   * would otherwise open with one underrun per render quantum for as long as
   * the engine takes to build -- measured at ~120 of them, none of which
   * anybody could have heard.
   */
  private primed = false;

  /** Frames available to fill with. */
  get depth(): number {
    let frames = 0;
    for (const block of this.blocks) {
      frames += block.length / 2;
    }

    return frames - this.offset;
  }

  push(block: Float32Array): void {
    this.blocks.push(block);
    this.primed = true;
  }

  /**
   * unprime puts the queue back in the state it starts in, where silence is not
   * yet a dropout.
   *
   * It is for a producer that is about to stop on purpose and for long enough
   * to matter -- rebuilding the engine around another sound. The reasoning is
   * the one `primed` already carries: silence nobody could have heard as a
   * fault should not be counted as one. The next block re-primes the queue, so
   * a producer that never comes back is silent and uncounted, which is the same
   * thing it looks like before it has started.
   */
  unprime(): void {
    this.primed = false;
  }

  /**
   * fill writes `frames` frames into left and right, taking them from the head
   * of the queue and handing each exhausted buffer to `recycle` so it can be
   * transferred back to the worker and filled again.
   *
   * A block need not be the size of a render quantum -- the read offset carries
   * the remainder over -- so the producer and the consumer are free to disagree
   * about block size, which is what lets the ScriptProcessorNode fallback run
   * 512-frame callbacks against a 128-frame producer.
   *
   * A short queue is not an error the caller has to handle: the rest of the
   * output is zeroed, one underrun is counted, and the number of frames
   * actually filled is returned.
   */
  fill(
    left: Float32Array,
    right: Float32Array,
    frames: number,
    recycle: (buffer: Float32Array) => void,
  ): number {
    let written = 0;

    while (written < frames && this.blocks.length > 0) {
      const head = this.blocks[0];
      const headFrames = head.length / 2;
      const take = Math.min(frames - written, headFrames - this.offset);

      for (let frame = 0; frame < take; frame += 1) {
        const source = (this.offset + frame) * 2;
        left[written + frame] = head[source];
        right[written + frame] = head[source + 1];
      }

      written += take;
      this.offset += take;

      if (this.offset >= headFrames) {
        this.blocks.shift();
        this.offset = 0;
        recycle(head);
      }
    }

    if (written < frames) {
      left.fill(0, written, frames);
      right.fill(0, written, frames);

      if (this.primed) {
        this.underruns += 1;
      }
    }

    return written;
  }
}
