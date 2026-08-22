# The audio transport

How the browser gets its samples: what runs on which thread, why that split and
not one of the other three, and what it costs. Phase 5.2 of [../PLAN.md](../PLAN.md).

## The decision

**The Go module runs in a Web Worker. An `AudioWorkletNode` consumes the blocks
it renders, over a `MessagePort` the two hold directly. The buffers are a fixed
pool that ping-pongs between them, so nothing allocates per block and no audio
passes through the main thread.**

```
main thread                Worker (Go WASM)               AudioWorklet
  noteOn / gain  ────────▶  renders 128-frame blocks
                            copies out of Go's memory
                            postMessage(buffer, transfer) ────▶  queue → output
                                        ◀──── the empty buffer ────
  dropout count  ◀──── node.port ─────────────────────────────── stats
```

The pool is the whole flow-control mechanism. `POOL_SIZE` buffers exist;
`postMessage` transfers one away and detaches it in the sender, so the worker
can only render when it holds a free one, and a buffer coming back from the
consumer _is_ the request for the next block. There is no timer driving the
producer, no queue that can grow without bound, and no separate credit protocol
to get wrong.

`POOL_SIZE = 4` at `BLOCK_FRAMES = 128` is 512 frames, ~11.6 ms at 44.1 kHz.
That is both the jitter the worker may take before the consumer starves and the
worst case for note-on latency, so it is a floor for responsiveness as much as a
ceiling for safety. Measured in Chrome, the queue sits at 4 blocks in the steady
state and does not drop below it.

## What was rejected

**A `SharedArrayBuffer` ring buffer**, the usual answer, is unavailable here.
`SharedArrayBuffer` requires the document to be cross-origin isolated, which
requires COOP and COEP response headers. `internal/server` could send them; the
app is also deployed to GitHub Pages, which cannot. A transport that only works
on one of the two hosts is a transport that is only tested on one of them.

**The Go module inside the `AudioWorkletGlobalScope`** — one thread fewer, one
copy fewer, and the lowest latency available — was the real alternative. Against
it: `wasm_exec.js` refuses to load without `crypto`, `performance`, `TextEncoder`
and `TextDecoder` (it throws by name, `web/wasm_exec.js:84-97`), the Go scheduler
wants `setTimeout` and `clearTimeout`, and the module has to be fetched. The
worklet scope has none of the six, so the price of admission is a hand-written
shim for each, including a UTF-8 `TextDecoder`, maintained against a file that is
required to stay byte-identical to the toolchain's. And it would put Go's garbage
collector on the render thread with no queue in front of it, where a collection
is a dropout rather than a hiccup the buffer absorbs.

**Rendering on the main thread into a worklet queue** is the smallest possible
change and fails the phase's acceptance criterion outright: synthesis would still
compete with React, the wood-texture generator and the Optimize tab's fit
requests for one thread.

## The consumers

Two of them, both thin wrappers around the same `BlockQueue` (`web/src/audio/blockQueue.ts`):

- `renderProcessor.ts`, the `AudioWorkletProcessor`. It is bundled as its own
  file (`?worker&url`) because a worklet module cannot resolve imports at
  runtime; whatever it uses has to be inlined into the file `addModule` fetches.
- the `ScriptProcessorNode` fallback in `useAudioEngine.ts`, for a browser with
  no `AudioWorklet`. The producer is untouched — synthesis is still in the
  worker — so what the fallback costs is the copy running on the main thread
  again. Force it with `?audio=scriptprocessor`, which is how it stays tested.

`BlockQueue` carries a read offset, so the producer's block size and the
consumer's need not agree: the fallback's 512-frame callbacks are fed from the
same 128-frame blocks the worklet takes one at a time.

## Two things that are easy to get wrong

**Connect the graph last.** The consumer is built, then the producer is started,
and only then is the node connected to the destination. Building the graph first
would leave it pulling against an empty queue for as long as the engine takes to
construct — `NewRealtimeEngine` renders every playable note once to measure the
preset — and every one of those quanta is an audible gap on the first strike.

**Silence before the first block is not a dropout.** Chrome calls `process()` on
a source worklet node whether or not it is connected to anything, so the counter
opens with one underrun per render quantum until the producer delivers. Measured
at ~120 of them, none of which anybody could hear. `BlockQueue` counts nothing
until its first `push`.

## What it costs, and what it bought

Note-on now travels main → worker rather than into a function call, so its
latency is one message hop plus what is queued: on the order of 15 ms. For an
instrument struck with a mallet that is a change worth naming, and it is the
price of the criterion below.

Measured in headless Chrome at 48 kHz, with a note ringing and the main thread
blocked solid for 3 s in 300 ms chunks:

| Consumer                             | Dropouts |
| ------------------------------------ | -------- |
| `AudioWorkletNode`                   | 0        |
| `ScriptProcessorNode` (the old path) | 280      |

The count is the phase's acceptance criterion made observable rather than
asserted: the processor counts starved render quanta and the status panel shows
the number as soon as it is not zero.
