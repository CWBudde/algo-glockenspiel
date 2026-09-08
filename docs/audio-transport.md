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
ceiling for safety.

It is where the pool **starts**, not where it stays. The paragraph above used to
end "measured in Chrome, the queue sits at 4 blocks in the steady state and does
not drop below it", and that claim was wrong everywhere it had not been measured
— see [The pool is not a constant](#the-pool-is-not-a-constant).

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

## The pool is not a constant

A render thread does not ask for one quantum at a time. It services its output
buffer in a **burst** of back-to-back `process()` calls and then sleeps for the
length of that buffer, so a queue shallower than one burst ends every burst in
silence however prompt the producer is. The original 4-block pool held 11.6 ms
against hosts that buffer four times that:

|                         | reported output latency | dropouts in 8 s, one note ringing |
| ----------------------- | ----------------------- | --------------------------------- |
| Chromium 151 (headless) | 40.0 ms                 | 34                                |
| Firefox 153 (headless)  | 54.7 ms                 | 603                               |

The queue's low-water mark sat at 0 frames in both. The instantaneous `depth`
the processor used to report could not show this — a queue emptied and refilled
between two samples reads as full — which is why `RenderStats` now carries
`minDepth` and `maxBurst` as well, and why `BlockQueue` tracks its own trough.

So the consumer sets the depth, over the render channel, with `TransportCredit`.
It is the only party that can see how its host actually asks for samples. It
raises the target — doubling, because an additive climb out of four blocks would
take fifteen seconds of audible fault — only while it is actually starving, and
lowers it by one block after ten seconds without a dropout. The depth therefore
settles at the shallowest value the host will accept, which matters because the
depth is also what note-on latency is paid out of: a target chosen once for the
worst host would make every other one less responsive for nothing. `MAX_POOL`
bounds it at 32 blocks, ~93 ms.

The blocks travel in batches in both directions for the same reason. One message
per 128 frames is ~344 tasks a second each way, and every one is an opportunity
for the browser to run something else first; the consumer drains a whole burst
at once and the producer refills the whole pool at once, so the batch is the
natural unit on the wire.

## The `?debug=audio` panel

`web/src/components/AudioDiagnostics.tsx`, reached with `?debug=audio` — the
same kind of query-param switch as `?audio=scriptprocessor`, and for the same
reason: the faults it diagnoses are specific to one browser on one machine, and
the practical way to get numbers is to ask the person sitting at it to add six
characters to the URL.

It reports the host's own latency, the callback burst, the queue's low-water
mark and credit target, and — from the producer — its wake-up jitter, its
**render load**, and its count of silent blocks.

Render load is the number that decides whether a transport can work at all: the
milliseconds spent in `processBlock` over the milliseconds of audio those calls
produced. At 1.0 the producer is exactly realtime and no depth of queue can save
it, because a queue buys time against jitter and never against a deficit that
recurs every block. It is a ratio rather than a distribution of per-block
timings because per-block is below the resolution of the only clock a worker
gets — Firefox coarsens `performance.now()` to a millisecond, and a block is
2.7 ms of audio rendered in a fraction of that, so every sample lands on 0 or 1.

Two clocks, and neither is `performance.now()` everywhere: **Firefox's
`AudioWorkletGlobalScope` has no `performance` object at all**, and reaching for
it there throws a `ReferenceError` out of `process()` on every render quantum,
which is silence rather than a diagnostic. The worklet uses `Date.now()`, whose
millisecond granularity is ample for separating "the same millisecond" from
"tens of milliseconds later".

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
blocked solid for 3 s in 300 ms chunks. Note what this test does and does not
cover: it is the main thread being blocked, on one browser, and it says nothing
about a host whose output buffer is deeper than the queue — which is the fault
that went unnoticed until `?debug=audio` existed.

| Consumer                             | Dropouts |
| ------------------------------------ | -------- |
| `AudioWorkletNode`                   | 0        |
| `ScriptProcessorNode` (the old path) | 280      |

The count is the phase's acceptance criterion made observable rather than
asserted: the processor counts starved render quanta and the status panel shows
the number as soon as it is not zero.
