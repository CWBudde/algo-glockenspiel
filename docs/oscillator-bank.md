# The Oscillator Bank

`internal/oscbank` is the synthesis core: a bank of `N` decaying quadrature
oscillators, each carrying up to `M` integer-multiple harmonic partials. Both
counts are ordinary runtime values. The four-mode bar model it replaced encoded
its count in a `const NumModes = 4`, five `.s` files and a hand-unrolled scalar
loop.

## The recursion

Every rotor advances by one complex multiply per sample:

```
t  = im*cos + re*sin
re = re*cos - im*sin
im = amp*x + t
```

`cos` and `sin` are the phase-rotation coefficients already scaled by the
per-sample decay factor, so amplitude decay and phase advance cost nothing
extra. `t` is the rotor's contribution to the output. There is no `sin()` in the
inner loop and no state to renormalize, because the decay factor stays below 1 —
a sustained rotor at a decay factor of exactly 1 would need magnitude
renormalization, which this bank deliberately does not do.

## Harmonics

Harmonics are rotors, not waveshaping. Oscillator `n` with harmonic gains
`g[0..M-1]` expands into `M` rotors: rotor `k` runs at `(k+1) * frequency`,
shares the oscillator's decay, and carries amplitude `A * g[k]`. Partials are
therefore computed _on top of_ the oscillators rather than baked into the
excitation ahead of them.

The Chebyshev waveshaper survives as an optional stage on either side of the
bank (`chebyshev.stage`: `excitation` or `output`). `excitation` is the v1
placement and stays the default, which is what keeps v1 presets sounding the
same.

## Layout

State lives in five `[]float32` arrays — `re`, `im`, `cosCoeff`, `sinCoeff`,
`amp` — in AoSoA blocks of eight rotors, rounded up to an **even** number of
blocks. Two blocks is what the AVX2 kernel consumes per pass; the even count
also lets a future 16-lane AVX-512 kernel take two blocks at a time and a 4-lane
NEON or SSE kernel take half a block, with no separate tail path anywhere.

Unused lanes carry zero coefficients and zero amplitude. They stay at zero
forever and contribute nothing, so no masking is needed.

## Accumulation

The old kernel reduced eight lanes to one scalar _per sample_, paying a
`VEXTRACTF128` plus `VHADDPS` plus two conversions inside the sample loop. The
bank does not. Each block pair accumulates into a per-frame partial buffer and
the horizontal sum happens once per chunk:

- the kernel folds a block's two 128-bit halves as it stores, which is free
  because the loop is latency-bound and the shuffle port is idle;
- `reduceLanes` collapses the remaining four partials per frame, eight frames at
  a time, at roughly two uops per sample.

Chunks are 256 samples, so the partial buffer is 4 KiB and stays in L1.

## The AVX2 kernel

`oscbank_avx2_amd64.s` keeps two blocks — sixteen rotors — in registers and
advances them together. One block alone cannot fill the vector ports: the
recursion is latency-bound, not throughput-bound.

The naive ordering puts three dependent operations between one sample's `im` and
the next: a multiply and an add to reach `t`, then another add for `amp*x + t`.
That is twelve cycles per sample. The kernel folds the excitation into the
accumulator seed instead,

```
im' = amp*x + re*sin + im*cos      two chained FMAs
t   = im' - amp*x
```

which leaves both `re'` and `im'` eight cycles from their inputs and takes `t`,
which nothing else depends on, off the critical path. `amp*x` for the next
sample is computed one iteration ahead, which is why the kernel reads one sample
past the end of its input and why the bank hands it a padded scratch buffer.

The portable kernel in `kernel_generic.go` associates the arithmetic the same
way. It cannot fuse its multiply-adds, so the two backends agree to float32
rounding rather than to the bit; making that exact is Phase 2's job.

## Measured performance

512-sample blocks, 12th Gen Intel Core i7-1255U, `taskset -c 0,1`,
`-benchtime 4000x`. The first two rows come from the same benchmark binary
(`go test ./model -bench 'ProcessBlock32$|OscBank4x4'`), so they share a thermal
state:

| Kernel                                    | Rotors | ns/block  | ns per rotor-block |
| ----------------------------------------- | ------ | --------- | ------------------ |
| `QuadDecayOscillator` (float64 AVX2, old) | 4      | 1314–1384 | 329–346            |
| `oscbank` 4 oscillators x 4 harmonics     | 16     | 1128–1154 | 70–72              |
| `oscbank` 4 x 4, portable kernel          | 16     | ~8000     | ~500               |

Scaling, from `go test ./internal/oscbank -bench Bank`:

| Configuration | Rotors | Blocks | ns/block | ns per rotor-block |
| ------------- | ------ | ------ | -------- | ------------------ |
| 4 x 1         | 4      | 2      | ~1140    | ~285               |
| 4 x 4         | 16     | 2      | ~1140    | ~71                |
| 8 x 4         | 32     | 4      | ~2200    | ~69                |
| 16 x 4        | 64     | 8      | ~4200    | ~66                |
| 64 x 1        | 64     | 8      | ~4150    | ~65                |
| 64 x 4        | 256    | 32     | ~16200   | ~63                |

Cost tracks the rotor count divided by the lane width, not the oscillator count.
Sixty-four oscillators cost 3.6x four oscillators, not 16x, because four
oscillators leave twelve of sixteen lanes empty; past one block pair the cost per
rotor is flat to within 10% out to 256 rotors, and drifts slightly _down_ as the
fixed per-pass overhead amortizes.

## Known limits

- Cross-voice lane packing is not implemented. A bank fills its lanes from one
  voice's oscillators; the realtime engine still renders voices serially. Packing
  `voices x oscillators` needs per-lane excitation and per-voice output
  separation, which belongs with the audio-path work in Phase 2.
- Only AVX2 is packed. Everything else runs the portable kernel, which is about
  7x slower. NEON, SSE2 and AVX-512 are Phase 2.
- Denormals are not flushed. A bank left running with no excitation decays into
  denormal state and slows down sharply; Phase 2 sets MXCSR FTZ/DAZ once per
  stream.
- The recursion still costs eight cycles per sample per block pair. Stepping two
  samples at a time through the squared rotation matrix would halve that, at the
  cost of a second coefficient set and a sample-count tail.
