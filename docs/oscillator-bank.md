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
way, and cannot fuse its multiply-adds. The next section says exactly how far
apart that is allowed to put them.

## The numeric contract

Three rules, and every future backend is judged by them.

**Packed kernels are the reference, and they agree with each other to the bit.**
A backend that has FMA fuses; two backends that both have FMA must produce
identical float32 words for identical inputs, on every sample, with no
tolerance. Bit-identity is what makes a render reproducible across machines, and
it is cheap to hold to as long as everyone associates the arithmetic the same
way — which is the second rule.

**The accumulation order is part of the contract, not an implementation
detail.** `reduceLanesGeneric` sums the pairwise tree `(lane[0] + lane[1]) +
(lane[2] + lane[3])`, and the fold-on-store inside the packed kernels must
reproduce exactly that order. Floating-point addition is not associative, so a
backend that folds `((l0 + l2) + l1) + l3` is a different program, not a faster
one. `reduceLanesAVX2` pays a `VPERMPS` to undo `VHADDPS`'s per-half
interleaving for precisely this reason.

**The portable kernel is a correctness reference held to a bound, not to the
bit.** It is not allowed to be a different algorithm — it associates and
accumulates identically — but it must be allowed to round differently, because
the whole point of an FMA is that `a*b + c` rounds once instead of twice, and no
portable Go expression can ask for that. What follows derives how far apart that
one substitution can push the two.

### What one FMA costs

Write `u = 2^-24 ≈ 5.96e-8` for the float32 unit roundoff. Per rotor and sample
the two kernels compute the same three quantities, with different numbers of
roundings:

| Quantity                       | Packed                        | Portable               |
| ------------------------------ | ----------------------------- | ---------------------- |
| `ampx = amp*x`                 | 1 (`VMULPS`)                  | 1                      |
| `im' = ampx + re*sin + im*cos` | 2 (two chained `VFMADD231PS`) | 4 (mul, add, mul, add) |
| `re' = re*cos - im*sin`        | 2 (`VMULPS`, `VFNMADD231PS`)  | 3 (mul, mul, sub)      |
| `t = im' - ampx`               | 1                             | 1                      |

`ampx` and `t` are the same operation on both sides and cancel out of the
difference. The two that differ contribute at most the sum of their own error
budgets, so per step the two kernels can disagree about the new state by

```
|Δim'| <= 6u * (|amp*x| + |re*sin| + |im*cos|)
|Δre'| <= 5u * (|re*cos| + |im*sin|)
```

Both bracketed sums are bounded the same way. The coefficient pair `(cos, sin)`
is a rotation scaled by the decay factor, so `cos² + sin² = d²`, and
Cauchy-Schwarz gives `|re*sin| + |im*cos| <= d*ρ` with `ρ = sqrt(re² + im²)` the
rotor's state magnitude. Take the larger constant and call the per-step
injection

```
δ(n) = 6u * (|amp*x(n)| + d*ρ(n))
```

### Why it does not compound

This is the part worth stating plainly. The state update is
`s(n+1) = d*R(φ)*s(n) + e(n)`, with `R` a rotation and `e` the excitation. An
error `E` already present in the state therefore evolves as
`E(n+1) = d*R(φ)*E(n) + δ(n)`. A rotation preserves magnitude exactly, so

```
||E(n+1)|| <= d*||E(n)|| + ||δ(n)||
```

and because the decay factor is _strictly_ below 1, that recursion is
contractive. Old error is forgotten at the same rate as old signal. Summing the
geometric series over a chunk of `N` samples gives the worst case, in which
every rounding error happens to point the same way:

```
||E(N)|| <= δ_max * (1 - d^N) / (1 - d)
```

Rounding errors do not point the same way — their signs are uncorrelated across
samples and across rotors — so the realistic composition is in quadrature:

```
||E(N)|| <= δ_max * g(N, d),    g(N, d) = sqrt((1 - d^2N) / (1 - d²))
```

`g` is the whole story. For a fast rotor it is small and saturates almost
immediately: a 1 ms half-life at 48 kHz has `d = 0.9857` and `g -> 5.9`, so the
two kernels can never drift more than about `6 * 5.9 * u ≈ 2.1e-6` apart
relative to the signal that drives them, no matter how long the render. A 100 ms
half-life has `d = 1 - 1.44e-4` and `g -> 59`, giving `2.1e-5`. Length only
matters until `N ≈ 1/(1-d)`; past that the bound is flat, because the bank is
forgetting error as fast as it makes it.

At `d = 1` the same formula reads `g = sqrt(N)`, which grows without bound. That
is the formal version of the rule the bank already enforces informally: a
sustained rotor has no numeric contract at all, only a drift rate. Decay below 1
is not just what keeps the recursion from needing renormalization — it is what
makes the backends comparable in the first place.

### The tolerance a test may use

Scaling the bound to a bank means summing the per-rotor injection scale, since
rotor errors are independent but the output is their sum:

```
E(n) = Σ_r (|amp_r * x(n)| + d_r * ρ_r(n))
```

`E` is a no-cancellation envelope: it is deliberately not the peak of the
rendered output. A bank whose rotors happen to cancel produces a small output
and exactly the same absolute error, so normalizing to the realized peak turns a
correct backend into a failing one. `contract_test.go` computes `E` by running
the magnitude recursion `ρ(n+1) = d*ρ(n) + |amp*x(n)|` alongside the render,
which costs one extra scalar pass and is contractive for the same reason the
error is.

The reduction adds a second term that is easy to forget. Rule two makes every
backend fold the lanes in the same order with the same number of adds, so the
reduction cannot make two backends _disagree_ — but each of those adds re-rounds
operands that already differ, and can round them a further ULP apart. There are
three adds to fold a block pair's four lanes, three more in `reduceLanes`, and
one per additional block pair accumulating into `acc`. That term is flat in `N`,
which is why it is invisible in a long render and dominant on the first sample,
where `g` is still 1:

```
tol = u * max_n E(n) * (6 * g(N, d_max) + 6 + pairs - 1)
```

Measured against the AVX2 kernel, the worst realized ratio over the differential
grid is about 0.2, and about 0.35 over several million fuzz executions — halving
both constants makes the harness fail within a second. That is the intended
calibration. The bound is not a rubber stamp: a new backend that lands inside it
is conforming, and one that exceeds it has an actual bug rather than bad luck.

### Denormals

`FlushDenormals` puts the calling OS thread into flush-to-zero and
denormals-are-zero for the duration of a block — MXCSR FTZ+DAZ on amd64, FPCR FZ
on arm64, a documented no-op on wasm — and `DenormalScope.Restore` puts the
thread's mode back exactly as it was, so a host's own policy survives being
called. The scope holds the goroutine to its thread while it is open, because
restoring a saved mode onto a thread that was never in it would be worse than
not saving it.

That guarantee is part of the numeric contract, not a performance footnote.
Every backend runs inside the same scope, so they still round the same way as
each other; what changes is that a rotor ringing down past about `1e-38` reaches
exactly zero on all of them instead of drifting through the denormal range at
whatever precision each backend's tail happens to have. The bound above is
unaffected — flushing only ever moves a value toward zero, and the envelope
already dominates anything that small — but the _reason_ it is unaffected is
worth stating: without the scope, denormals are the one place where two
backends could legitimately disagree by 100% of a very small number. The fuzz
corpus drives rotors deliberately into that range for exactly that reason.

### The SSE2 corollary

SSE2 has no FMA. A packed SSE2 kernel therefore sits on the _portable_ side of
the contract, not the packed side: it is not required to be bit-identical to
AVX2, and it must not be tested as if it were. It is held to the same
`6u * g(N, d)` bound as `kernel_generic.go`, for the same reason and by the same
harness. Only its lane fold has to be exact — the accumulation order is rule
two, and rule two has no tolerance.

What SSE2 _is_ required to be bit-identical to is the portable kernel on the
same machine, if and only if it makes the same rounding choices. It turns out to
make them, and deliberately: see "The SSE2 kernel" below. A packed kernel with
no FMA rounds `im'` four times however it is written, so associating exactly as
`kernel_generic.go` does costs it nothing — same instruction count, same
dependency chain, same register pressure — and it upgrades the portable kernel
from an approximate oracle for SSE2 into an exact one.
`TestSSE2IsBitIdenticalToPortable` and the fuzz harness assert it, so the
kernel's association cannot quietly drift away from the reference's.

That is a claim about amd64 only, and it is the same wrinkle as the one below:
it holds because the Go compiler cannot fuse `a + b*c` on a target where FMA is
not baseline. The bound remains the contract; the bit-identity is a stronger
property this particular backend happens to be able to offer for free.

There is a matching wrinkle on arm64 that is easy to trip over. The Go
specification permits the compiler to fuse `a + b*c` into a single rounded
operation, and the arm64 backend does exactly that, while the amd64 backend
cannot because FMA is not part of the amd64 baseline. So `kernel_generic.go` is
not one program: on arm64 it already emits `FMADD`/`FMSUB` in the same places
the AVX2 kernel emits `VFMADD231PS`/`VFNMADD231PS`, and the divergence this
section bounds is an amd64-only phenomenon. The bound holds on both; it is
simply slack on arm64. Never write a test that requires the portable kernel to
be bit-identical to itself across architectures.

## The SSE2 kernel

`oscbank_sse2_amd64.s` takes the same block pair as the AVX2 kernel and splits
it four ways: XMM is four lanes, so sixteen rotors are four half-blocks — block
A low, A high, B low, B high — advanced together. This is the case the even
block count in "Layout" was rounded for, and it needs no tail path.

Its one real design decision is the association, and the SSE2 corollary above is
the answer: it evaluates `(amp*x + re*sin) + im*cos` strictly left to right,
which is what `kernel_generic.go` compiles to on amd64, rather than seeding an
accumulator the way the AVX2 kernel's FMA chain does. Both cost four roundings
and one dependency chain of two adds. Only one of them is bit-identical to the
reference. Every operand order in the kernel is load-bearing for that: `ADDPS`
returns its destination when both operands are NaN and `SUBPS` is not
commutative at all, so the accumulator is always the destination.

Sixteen XMM registers cannot hold `re`, `im`, `cos`, `sin` and `amp` for four
half-blocks — that is twenty — so `cos`, `sin` and `amp` are reloaded from L1
every sample. Twelve loads at two per cycle fit comfortably inside a loop that
is latency-bound on two dependent adds per half-block, so the reloads are free.
The `amp*x` register for each half-block is dead the moment its half-block has
consumed it and is reused in place to carry that half-block's `t` to the fold,
which is what makes the whole pass fit in sixteen registers at all.

`reduceLanes` has no SSE2 path and should not grow one. `VHADDPS` is SSE3, so a
4-lane fold would have to transpose four frames with `UNPCKLPS`/`UNPCKHPS`
first — about four uops per frame, which is what the scalar loop already costs.
The reduction is memory-shaped, not arithmetic-shaped. A third implementation of
a summation order that rule two pins exactly would be one more place to get rule
two wrong, for nothing.

## Measured performance

512-sample blocks, 12th Gen Intel Core i7-1255U, `taskset -c 0,1`,
`-benchtime 4000x`. The first two rows were read off one benchmark binary, so
they share a thermal state:

| Kernel                                  | Rotors | ns/block  | ns per rotor-block |
| --------------------------------------- | ------ | --------- | ------------------ |
| retired four-mode kernel (float64 AVX2) | 4      | 1314–1384 | 329–346            |
| `oscbank` 4 oscillators x 4 harmonics   | 16     | 1128–1154 | 70–72              |
| `oscbank` 4 x 4, SSE2 kernel            | 16     | 2850–3400 | 178–212            |
| `oscbank` 4 x 4, portable kernel        | 16     | ~8000     | ~500               |

The SSE2 row lands where a 4-lane unfused kernel should: about 2.1x the AVX2
kernel and about 3x faster than the portable one. Half the lanes accounts for
most of the gap and the missing FMA for the rest — two multiplies and two adds
per rotor and sample where AVX2 issues two FMAs.

That row was measured on a loaded machine, and the honest way to read it is as a
ratio. The same binary in the same run reported 1336–1372 ns/block for AVX2 and
8755–11608 for the portable kernel, both around 15% above their own rows above,
so the absolute SSE2 figure is inflated by roughly the same amount and the
ratios are what survive.

The first row is history, not something to re-run: `QuadDecayOscillator` and its
five `.s` files were deleted in Phase 2.1 once nothing rendered through them. It
is kept because it is the number this bank had to beat, and it did — four times
the oscillator work for 15% less time.

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

## Preset compatibility

Schema v2 exists alongside v1 rather than replacing it. `Load` holds a `"1.0"`
document to the v1 rules — exactly four modes, no per-mode harmonics, no
explicit shaper stage — so a file that quietly grew v2 fields is reported
instead of rendering differently than its version claims. `preset.Upgrade`
converts, and `Save` preserves the version it was given.

`BarParams.Modes` being a slice made plain struct assignment a bug:
`scaledParamsForNote` was transposing the synthesizer's own preset on every
note. It clones now, and `TestRenderingIsIndependentOfPresetState` guards it.

## Known limits

- Cross-voice lane packing is not implemented. A bank fills its lanes from one
  voice's oscillators; the realtime engine still renders voices serially. Packing
  `voices x oscillators` needs per-lane excitation and per-voice output
  separation, which belongs with the audio-path work in Phase 2.
- AVX2 and SSE2 are packed. Everything else runs the portable kernel, which is
  about 7x slower. NEON is Phase 2.3; AVX-512 is deferred, because CI cannot
  prove it correct on a runner pool that only sometimes has the instructions.
- The recursion still costs eight cycles per sample per block pair. Stepping two
  samples at a time through the squared rotation matrix would halve that, at the
  cost of a second coefficient set and a sample-count tail.
- The optimizer does not search per-mode harmonic gains. `ParamCodec` sizes
  itself from the template's mode count but carries per-mode harmonics through
  unchanged.
