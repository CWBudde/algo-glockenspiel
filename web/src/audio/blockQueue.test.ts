import { describe, expect, it } from "vitest";

import { BlockQueue } from "./blockQueue";

/** interleaved builds one block of `frames` frames as [n, -n, n+1, -(n+1), ...]. */
function interleaved(frames: number, first: number): Float32Array {
  const block = new Float32Array(frames * 2);
  for (let frame = 0; frame < frames; frame += 1) {
    block[frame * 2] = first + frame;
    block[frame * 2 + 1] = -(first + frame);
  }

  return block;
}

function output(frames: number): [Float32Array, Float32Array] {
  return [new Float32Array(frames), new Float32Array(frames)];
}

describe("BlockQueue", () => {
  it("de-interleaves one block into the two channels", () => {
    const queue = new BlockQueue();
    queue.push(interleaved(4, 1));

    const [left, right] = output(4);
    const spent: Float32Array[] = [];
    const written = queue.fill(left, right, 4, (buffer) => spent.push(buffer));

    expect(written).toBe(4);
    expect(Array.from(left)).toEqual([1, 2, 3, 4]);
    expect(Array.from(right)).toEqual([-1, -2, -3, -4]);
    expect(spent).toHaveLength(1);
    expect(queue.underruns).toBe(0);
  });

  it("spans several producer blocks in one fill", () => {
    const queue = new BlockQueue();
    queue.push(interleaved(2, 1));
    queue.push(interleaved(2, 3));

    const [left, right] = output(4);
    const spent: Float32Array[] = [];
    queue.fill(left, right, 4, (buffer) => spent.push(buffer));

    expect(Array.from(left)).toEqual([1, 2, 3, 4]);
    // Both blocks are recycled, in the order they were consumed.
    expect(spent).toHaveLength(2);
    expect(spent[0][0]).toBe(1);
    expect(spent[1][0]).toBe(3);
  });

  it("carries the remainder of a block over to the next fill", () => {
    const queue = new BlockQueue();
    queue.push(interleaved(4, 1));

    const spent: Float32Array[] = [];
    const [firstLeft, firstRight] = output(3);
    queue.fill(firstLeft, firstRight, 3, (buffer) => spent.push(buffer));

    expect(Array.from(firstLeft)).toEqual([1, 2, 3]);
    // Still partly full, so it has not gone back to the producer yet.
    expect(spent).toHaveLength(0);
    expect(queue.depth).toBe(1);

    queue.push(interleaved(4, 5));

    const [secondLeft, secondRight] = output(3);
    queue.fill(secondLeft, secondRight, 3, (buffer) => spent.push(buffer));

    expect(Array.from(secondLeft)).toEqual([4, 5, 6]);
    expect(spent).toHaveLength(1);
  });

  it("counts one underrun per starved call and zeroes the rest of the output", () => {
    const queue = new BlockQueue();
    queue.push(interleaved(2, 1));

    const [left, right] = output(4);
    left.fill(9);
    right.fill(9);

    const written = queue.fill(left, right, 4, () => undefined);

    expect(written).toBe(2);
    expect(Array.from(left)).toEqual([1, 2, 0, 0]);
    expect(Array.from(right)).toEqual([-1, -2, 0, 0]);
    expect(queue.underruns).toBe(1);

    queue.fill(left, right, 4, () => undefined);
    expect(queue.underruns).toBe(2);
  });

  it("does not count silence before the first block as a dropout", () => {
    // Chrome runs a source worklet's process() whether or not the node is
    // connected, so the consumer is asked for output before the producer has
    // started. That is not a dropout, and counting it would report a few
    // hundred of them on every page that ever made a sound.
    const queue = new BlockQueue();

    const [left, right] = output(4);
    queue.fill(left, right, 4, () => undefined);
    queue.fill(left, right, 4, () => undefined);

    expect(queue.underruns).toBe(0);

    queue.push(interleaved(1, 1));
    queue.fill(left, right, 4, () => undefined);

    expect(queue.underruns).toBe(1);
  });

  it("feeds a 512-frame consumer from a 128-frame producer", () => {
    // The ScriptProcessorNode fallback runs 512-frame callbacks against the
    // worker's 128-frame blocks, so the two sizes must not have to agree.
    const queue = new BlockQueue();
    for (let block = 0; block < 4; block += 1) {
      queue.push(interleaved(128, block * 128 + 1));
    }

    expect(queue.depth).toBe(512);

    const [left, right] = output(512);
    const spent: Float32Array[] = [];
    const written = queue.fill(left, right, 512, (buffer) =>
      spent.push(buffer),
    );

    expect(written).toBe(512);
    expect(left[0]).toBe(1);
    expect(left[511]).toBe(512);
    expect(spent).toHaveLength(4);
    expect(queue.depth).toBe(0);
    expect(queue.underruns).toBe(0);
  });

  it("stops counting dropouts after unprime, and starts again on the next block", () => {
    const queue = new BlockQueue();
    queue.push(interleaved(4, 1));

    const [left, right] = output(4);
    queue.fill(left, right, 4, () => undefined);
    queue.fill(left, right, 4, () => undefined);
    expect(queue.underruns).toBe(1);

    // The producer is about to rebuild the engine, which outlasts the queue by
    // more than an order of magnitude. That silence is deliberate.
    queue.unprime();
    queue.fill(left, right, 4, () => undefined);
    queue.fill(left, right, 4, () => undefined);
    expect(queue.underruns).toBe(1);

    // Once it delivers again, a dry queue is a fault once more.
    queue.push(interleaved(4, 9));
    queue.fill(left, right, 4, () => undefined);
    queue.fill(left, right, 4, () => undefined);
    expect(queue.underruns).toBe(2);
  });
});
